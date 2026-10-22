package github

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	"git.pegasko.art/catsona/gstatus/internal/model"
)

type issue struct {
	ID            string    `json:"node_id"`
	Number        int       `json:"number"`
	Title         string    `json:"title"`
	URL           string    `json:"html_url"`
	RepositoryURL string    `json:"repository_url"`
	State         string    `json:"state"`
	Draft         bool      `json:"draft"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

type pull struct {
	issue
	Merged    bool   `json:"merged"`
	Merge     string `json:"merge_commit_sha"`
	Mergeable *bool  `json:"mergeable"`
	Head      struct {
		SHA  string `json:"sha"`
		Ref  string `json:"ref"`
		Repo struct {
			Name string `json:"full_name"`
		} `json:"repo"`
	} `json:"head"`
}

// List retrieves every search page, splitting ranges that exceed GitHub's search limit.
func (c *Client) List(ctx context.Context, token, login string, repositories []string, filter string) ([]model.PR, error) {
	query := "is:pr author:" + login
	switch filter {
	case "open":
		query += " is:open"
	case "draft":
		query += " is:open is:draft"
	case "merged":
		query += " is:merged"
	case "closed":
		query += " is:closed is:unmerged"
	}

	for _, repository := range repositories {
		query += " repo:" + repository
	}

	return c.search(ctx, token, query, time.Unix(0, 0).UTC(), time.Now().UTC().Add(24*time.Hour))
}

func (c *Client) search(ctx context.Context, token, query string, from, to time.Time) ([]model.PR, error) {
	query += " created:>=" + from.Format(time.RFC3339) + " created:<" + to.Format(time.RFC3339)
	var result []model.PR
	for page := 1; ; page++ {
		var response struct {
			Total      int     `json:"total_count"`
			Incomplete bool    `json:"incomplete_results"`
			Items      []issue `json:"items"`
		}
		endpoint := fmt.Sprintf("search/issues?q=%s&sort=created&order=asc&per_page=100&page=%d", url.QueryEscape(query), page)
		if err := c.api(ctx, token, endpoint, &response); err != nil {
			return nil, err
		}

		if response.Incomplete {
			return nil, fmt.Errorf("GitHub returned an incomplete PR search; retry the refresh")
		}

		if response.Total > 1000 {
			if to.Sub(from) <= time.Second {
				return nil, fmt.Errorf("PR search exceeds 1000 results in one second")
			}

			base := strings.Split(query, " created:>=")[0]
			middle := from.Add(to.Sub(from) / 2).Truncate(time.Second)
			left, err := c.search(ctx, token, base, from, middle)
			if err != nil {
				return nil, err
			}

			right, err := c.search(ctx, token, base, middle, to)
			return append(left, right...), err
		}

		for _, item := range response.Items {
			result = append(result, item.pr())
		}

		if len(response.Items) < 100 || len(result) >= response.Total {
			return result, nil
		}
	}
}

func (i issue) pr() model.PR {
	repository := strings.TrimPrefix(i.RepositoryURL, "https://api.github.com/repos/")
	return model.PR{
		ID: i.ID, Repository: repository, Number: i.Number, Url: i.URL, Title: i.Title,
		State: i.State, Draft: i.Draft, CreatedAt: i.CreatedAt, UpdatedAt: i.UpdatedAt,
	}
}

func (c *Client) basic(ctx context.Context, token string, pr model.PR) (model.PR, error) {
	var response pull
	endpoint := fmt.Sprintf("repos/%s/pulls/%d", pr.Repository, pr.Number)
	if err := c.api(ctx, token, endpoint, &response); err != nil {
		return model.PR{}, err
	}

	result := response.issue.pr()
	result.Repository = pr.Repository
	result.HeadSHA = response.Head.SHA
	result.HeadRef = response.Head.Ref
	result.HeadRepo = response.Head.Repo.Name
	result.MergeSHA = response.Merge
	result.BranchStatus = "unknown"
	if response.Mergeable != nil {
		result.BranchStatus = "conflict"
		if *response.Mergeable {
			result.BranchStatus = "ok"
		}
	}
	if response.Merged {
		result.State = "merged"
	}

	if result.ID == "" || result.HeadSHA == "" {
		return model.PR{}, fmt.Errorf("GitHub returned an incomplete PR identity")
	}

	return result, nil
}

// Detail returns a consistent complete revision or leaves the caller's snapshot untouched.
func (c *Client) Detail(ctx context.Context, token string, pr model.PR) (model.PR, error) {
	result, err := c.basic(ctx, token, pr)
	if err != nil {
		return model.PR{}, err
	}

	if err := c.loadComments(ctx, token, &result); err != nil {
		return model.PR{}, err
	}

	if err := c.loadChecks(ctx, token, &result); err != nil {
		return model.PR{}, err
	}

	verified, err := c.basic(ctx, token, result)
	if err != nil {
		return model.PR{}, err
	}

	if verified.HeadSHA != result.HeadSHA || verified.MergeSHA != result.MergeSHA {
		return model.PR{}, fmt.Errorf("PR revision changed during refresh")
	}

	result.BranchStatus = verified.BranchStatus
	result.Complete = true
	return result, nil
}
