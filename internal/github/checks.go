package github

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"git.pegasko.art/catsona/gstatus/internal/model"
)

type apiCheck struct {
	ID          int64     `json:"id"`
	Name        string    `json:"name"`
	State       string    `json:"status"`
	Conclusion  string    `json:"conclusion"`
	URL         string    `json:"details_url"`
	StartedAt   time.Time `json:"started_at"`
	CompletedAt time.Time `json:"completed_at"`
	App         struct {
		Slug string `json:"slug"`
	} `json:"app"`
	Suite struct {
		ID int64 `json:"id"`
	} `json:"check_suite"`
}

type apiRun struct {
	ID         int64     `json:"id"`
	WorkflowID int64     `json:"workflow_id"`
	Attempt    int       `json:"run_attempt"`
	Name       string    `json:"name"`
	Event      string    `json:"event"`
	HeadSHA    string    `json:"head_sha"`
	HeadBranch string    `json:"head_branch"`
	SuiteID    int64     `json:"check_suite_id"`
	State      string    `json:"status"`
	Conclusion string    `json:"conclusion"`
	URL        string    `json:"html_url"`
	CreatedAt  time.Time `json:"created_at"`
	StartedAt  time.Time `json:"run_started_at"`
	UpdatedAt  time.Time `json:"updated_at"`
	PRs        []struct {
		Number int `json:"number"`
		Head   struct {
			SHA string `json:"sha"`
		} `json:"head"`
	} `json:"pull_requests"`
}

func (c *Client) loadChecks(ctx context.Context, token string, pr *model.PR) error {
	seen := map[int64]bool{}
	revisions := map[string]bool{}
	runs := map[int64]apiRun{}
	suites := map[string]int64{}
	for _, revision := range []string{pr.HeadSHA, pr.MergeSHA} {
		if revision == "" || revisions[revision] {
			continue
		}
		revisions[revision] = true

		workflows, err := c.workflowRuns(ctx, token, pr.Repository, "head_sha="+url.QueryEscape(revision))
		if err != nil {
			return err
		}
		for _, run := range workflows {
			if runMatches(run, *pr) {
				runs[run.ID] = run
			}
		}

		for page := 1; ; page++ {
			var response struct {
				Checks []apiCheck `json:"check_runs"`
			}
			endpoint := fmt.Sprintf("repos/%s/commits/%s/check-runs?filter=all&per_page=100&page=%d", pr.Repository, revision, page)
			if err := c.api(ctx, token, endpoint, &response); err != nil {
				return err
			}

			for _, check := range response.Checks {
				if seen[check.ID] {
					continue
				}
				seen[check.ID] = true

				value := model.Check{
					ID: "check:" + strconv.FormatInt(check.ID, 10), Name: check.Name, App: check.App.Slug,
					Revision: revision, State: check.State, Conclusion: check.Conclusion, Url: check.URL,
					StartedAt: check.StartedAt, CompletedAt: check.CompletedAt,
				}
				suites[value.ID] = check.Suite.ID
				prefix := "https://github.com/" + pr.Repository + "/actions/runs/"
				if strings.HasPrefix(strings.ToLower(check.URL), strings.ToLower(prefix)) {
					match := regexp.MustCompile(`/actions/runs/(\d+)`).FindStringSubmatch(check.URL)
					if len(match) == 2 {
						value.RunID, _ = strconv.ParseInt(match[1], 10, 64)
					}
				}

				pr.Checks = append(pr.Checks, value)
				pr.LastCI = latest(pr.LastCI, latest(check.StartedAt, check.CompletedAt))
			}

			if len(response.Checks) < 100 {
				break
			}
		}

		statuses, err := pages[struct {
			ID        int64     `json:"id"`
			Name      string    `json:"context"`
			State     string    `json:"state"`
			URL       string    `json:"target_url"`
			CreatedAt time.Time `json:"created_at"`
		}](ctx, c, token, fmt.Sprintf("repos/%s/commits/%s/statuses", pr.Repository, revision))
		if err != nil {
			return err
		}

		contexts := map[string]model.Check{}
		for _, status := range statuses {
			if previous, exists := contexts[status.Name]; exists && !status.CreatedAt.After(previous.CreatedAt) {
				continue
			}

			conclusion := ""
			state := status.State
			if terminal(state) {
				conclusion = state
				state = "completed"
			}

			contexts[status.Name] = model.Check{
				ID: "status:" + strconv.FormatInt(status.ID, 10), Name: status.Name, App: "commit-status",
				Revision: revision, State: state, Conclusion: conclusion, Url: status.URL, CreatedAt: status.CreatedAt,
			}
			pr.LastCI = latest(pr.LastCI, status.CreatedAt)
		}

		for _, check := range contexts {
			pr.Checks = append(pr.Checks, check)
		}
	}

	for index := range pr.Checks {
		check := &pr.Checks[index]
		if check.RunID == 0 && check.App == "github-actions" && suites[check.ID] != 0 {
			suiteID := suites[check.ID]
			for _, run := range runs {
				if run.SuiteID == suiteID {
					check.RunID = run.ID
					break
				}
			}
			if check.RunID == 0 {
				workflows, err := c.workflowRuns(ctx, token, pr.Repository, fmt.Sprintf("check_suite_id=%d", suiteID))
				if err != nil {
					return err
				}
				for _, run := range workflows {
					if runMatches(run, *pr) {
						runs[run.ID] = run
						check.RunID = max(check.RunID, run.ID)
					}
				}
			}
		}

		if check.RunID == 0 {
			continue
		}
		if _, exists := runs[check.RunID]; !exists {
			var run apiRun
			if err := c.api(ctx, token, fmt.Sprintf("repos/%s/actions/runs/%d", pr.Repository, check.RunID), &run); err != nil {
				return err
			}
			if !runMatches(run, *pr) {
				return fmt.Errorf("workflow run %d belongs to an outdated PR revision", run.ID)
			}
			runs[run.ID] = run
		}
	}

	for _, run := range runs {
		name := run.Name
		if name == "" {
			name = fmt.Sprintf("workflow %d", run.WorkflowID)
		}
		pr.Runs = append(pr.Runs, model.Run{
			ID: run.ID, WorkflowID: run.WorkflowID, Attempt: run.Attempt, Name: name, Event: run.Event,
			HeadSHA: run.HeadSHA, State: run.State, Conclusion: run.Conclusion, Url: run.URL,
			CreatedAt: run.CreatedAt, StartedAt: run.StartedAt, UpdatedAt: run.UpdatedAt,
		})
		pr.LastCI = latest(pr.LastCI, latest(run.CreatedAt, latest(run.StartedAt, run.UpdatedAt)))
	}

	sort.Slice(pr.Checks, func(i, j int) bool { return pr.Checks[i].ID < pr.Checks[j].ID })
	sort.Slice(pr.Runs, func(i, j int) bool { return pr.Runs[i].ID < pr.Runs[j].ID })
	return nil
}

func runMatches(run apiRun, pr model.PR) bool {
	for _, linked := range run.PRs {
		if linked.Number == pr.Number && linked.Head.SHA == pr.HeadSHA {
			return true
		}
	}
	if len(run.PRs) > 0 && strings.HasPrefix(run.Event, "pull_request") {
		return false
	}
	return (run.HeadSHA == pr.HeadSHA || (pr.MergeSHA != "" && run.HeadSHA == pr.MergeSHA)) && run.HeadBranch == pr.HeadRef
}

func (c *Client) workflowRuns(ctx context.Context, token, repository, qualifier string) ([]apiRun, error) {
	var result []apiRun
	for page := 1; ; page++ {
		var response struct {
			Total int      `json:"total_count"`
			Runs  []apiRun `json:"workflow_runs"`
		}
		endpoint := fmt.Sprintf("repos/%s/actions/runs?%s&per_page=100&page=%d", repository, qualifier, page)
		if err := c.api(ctx, token, endpoint, &response); err != nil {
			return nil, err
		}
		if response.Total > 1000 {
			return nil, fmt.Errorf("CI history exceeds GitHub's 1000-run query limit")
		}
		result = append(result, response.Runs...)
		if len(response.Runs) < 100 {
			return result, nil
		}
	}
}

// Result reports accepted requests and each failed request without retrying mutations.
type Result struct {
	Accepted int
	Failures []string
}

// Rerun applies the requested operation to current, completed Actions executions.
func (c *Client) Rerun(ctx context.Context, token string, selected model.PR, failedOnly bool) (Result, error) {
	current, err := c.basic(ctx, token, selected)
	if err != nil {
		return Result{}, err
	}

	if current.HeadSHA != selected.HeadSHA || current.MergeSHA != selected.MergeSHA {
		return Result{}, fmt.Errorf("PR revision changed; refresh before rerunning CI")
	}

	if err := c.loadChecks(ctx, token, &current); err != nil {
		return Result{}, err
	}

	latestRuns := map[string]model.Run{}
	for _, run := range current.Runs {
		key := fmt.Sprintf("%d:%s:%s", run.WorkflowID, run.Event, run.HeadSHA)
		if run.ID >= latestRuns[key].ID {
			latestRuns[key] = run
		}
	}

	var result Result
	for _, run := range latestRuns {
		if run.State != "completed" {
			continue
		}

		if failedOnly && run.Conclusion != "failure" && run.Conclusion != "timed_out" && run.Conclusion != "startup_failure" {
			continue
		}

		args := []string{"run", "rerun", strconv.FormatInt(run.ID, 10), "--repo", current.Repository}
		if failedOnly {
			args = append(args, "--failed")
		}

		if _, err := c.output(ctx, token, args...); err != nil {
			result.Failures = append(result.Failures, fmt.Sprintf("%s: %v", run.Name, err))
		} else {
			result.Accepted++
		}
	}

	if result.Accepted == 0 && len(result.Failures) == 0 {
		return result, fmt.Errorf("no completed GitHub Actions runs match this operation")
	}

	return result, nil
}

// Cancel requests cancellation of current, active Actions executions without retrying mutations.
func (c *Client) Cancel(ctx context.Context, token string, selected model.PR) (Result, error) {
	current, err := c.basic(ctx, token, selected)
	if err != nil {
		return Result{}, err
	}
	if current.HeadSHA != selected.HeadSHA || current.MergeSHA != selected.MergeSHA {
		return Result{}, fmt.Errorf("PR revision changed; refresh before cancelling CI")
	}
	if err := c.loadChecks(ctx, token, &current); err != nil {
		return Result{}, err
	}

	latestRuns := map[string]model.Run{}
	for _, run := range current.Runs {
		key := fmt.Sprintf("%d:%s:%s", run.WorkflowID, run.Event, run.HeadSHA)
		if run.ID >= latestRuns[key].ID {
			latestRuns[key] = run
		}
	}

	var result Result
	for _, run := range latestRuns {
		if run.State == "completed" || run.State == "" {
			continue
		}
		args := []string{"run", "cancel", strconv.FormatInt(run.ID, 10), "--repo", current.Repository}
		if _, err := c.output(ctx, token, args...); err != nil {
			result.Failures = append(result.Failures, fmt.Sprintf("%s: %v", run.Name, err))
		} else {
			result.Accepted++
		}
	}
	if result.Accepted == 0 && len(result.Failures) == 0 {
		return result, fmt.Errorf("no active GitHub Actions runs match this PR")
	}
	return result, nil
}
