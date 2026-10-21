package github

import (
	"context"
	"fmt"
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
}

type apiRun struct {
	ID         int64     `json:"id"`
	WorkflowID int64     `json:"workflow_id"`
	Attempt    int       `json:"run_attempt"`
	Name       string    `json:"name"`
	Event      string    `json:"event"`
	HeadSHA    string    `json:"head_sha"`
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
	for _, revision := range []string{pr.HeadSHA, pr.MergeSHA} {
		if revision == "" || (revision == pr.MergeSHA && revision == pr.HeadSHA && len(pr.Checks) > 0) {
			continue
		}

		for page := 1; ; page++ {
			var response struct {
				Checks []apiCheck `json:"check_runs"`
			}
			endpoint := fmt.Sprintf("repos/%s/commits/%s/check-runs?filter=latest&per_page=100&page=%d", pr.Repository, revision, page)
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

	runIDs := map[int64]bool{}
	for _, check := range pr.Checks {
		if check.RunID == 0 || runIDs[check.RunID] {
			continue
		}
		runIDs[check.RunID] = true

		var run apiRun
		if err := c.api(ctx, token, fmt.Sprintf("repos/%s/actions/runs/%d", pr.Repository, check.RunID), &run); err != nil {
			return err
		}

		matches := run.HeadSHA == pr.HeadSHA || (pr.MergeSHA != "" && run.HeadSHA == pr.MergeSHA)
		for _, linked := range run.PRs {
			matches = matches || (linked.Number == pr.Number && linked.Head.SHA == pr.HeadSHA)
		}

		if !matches {
			return fmt.Errorf("workflow run %d belongs to an outdated PR revision", run.ID)
		}

		pr.Runs = append(pr.Runs, model.Run{
			ID: run.ID, WorkflowID: run.WorkflowID, Attempt: run.Attempt, Name: run.Name, Event: run.Event,
			HeadSHA: run.HeadSHA, State: run.State, Conclusion: run.Conclusion, Url: run.URL,
			CreatedAt: run.CreatedAt, StartedAt: run.StartedAt, UpdatedAt: run.UpdatedAt,
		})
		pr.LastCI = latest(pr.LastCI, latest(run.CreatedAt, latest(run.StartedAt, run.UpdatedAt)))
	}

	sort.Slice(pr.Checks, func(i, j int) bool { return pr.Checks[i].ID < pr.Checks[j].ID })
	sort.Slice(pr.Runs, func(i, j int) bool { return pr.Runs[i].ID < pr.Runs[j].ID })
	return nil
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
