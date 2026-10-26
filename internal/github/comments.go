package github

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/catsona/gstatus/internal/model"
)

type apiComment struct {
	ID        int64     `json:"id"`
	URL       string    `json:"html_url"`
	CreatedAt time.Time `json:"created_at"`
	ReplyTo   int64     `json:"in_reply_to_id"`
	ReviewID  int64     `json:"pull_request_review_id"`
	User      struct {
		Login string `json:"login"`
	} `json:"user"`
}

func (c *Client) loadComments(ctx context.Context, token string, pr *model.PR) error {
	base := fmt.Sprintf("repos/%s/pulls/%d", pr.Repository, pr.Number)
	reviews, err := pages[struct {
		ID        int64     `json:"id"`
		State     string    `json:"state"`
		URL       string    `json:"html_url"`
		Submitted time.Time `json:"submitted_at"`
		User      struct {
			Login string `json:"login"`
		} `json:"user"`
	}](ctx, c, token, base+"/reviews")
	if err != nil {
		return err
	}

	pending := map[int64]bool{}
	decisions := map[string]model.Review{}
	pr.ReviewStatus = "none"
	for _, review := range reviews {
		if review.State == "PENDING" || review.Submitted.IsZero() {
			pending[review.ID] = true
			continue
		}

		value := model.Review{
			ID: strconv.FormatInt(review.ID, 10), Author: review.User.Login,
			State: review.State, Url: review.URL, SubmittedAt: review.Submitted,
		}
		pr.Reviews = append(pr.Reviews, value)
		pr.LastReview = latest(pr.LastReview, review.Submitted)
		pr.ReviewStatus = "commented"

		if review.State != "COMMENTED" && !value.SubmittedAt.Before(decisions[value.Author].SubmittedAt) {
			decisions[value.Author] = value
		}
	}

	for _, review := range decisions {
		if review.State == "CHANGES_REQUESTED" {
			pr.ReviewStatus = "changes_requested"
			break
		}

		if review.State == "APPROVED" {
			pr.ReviewStatus = "approved"
		}
	}

	reviewComments, err := pages[apiComment](ctx, c, token, base+"/comments")
	if err != nil {
		return err
	}

	for _, comment := range reviewComments {
		if pending[comment.ReviewID] {
			continue
		}

		root := comment.ReplyTo
		if root == 0 {
			root = comment.ID
		}

		pr.Comments = append(pr.Comments, model.Comment{
			ID: "review:" + strconv.FormatInt(comment.ID, 10), Author: comment.User.Login,
			Url: comment.URL, ThreadID: "review:" + strconv.FormatInt(root, 10), CreatedAt: comment.CreatedAt,
		})
		pr.LastMessage = latest(pr.LastMessage, comment.CreatedAt)
	}

	endpoint := fmt.Sprintf("repos/%s/issues/%d/comments", pr.Repository, pr.Number)
	comments, err := pages[apiComment](ctx, c, token, endpoint)
	if err != nil {
		return err
	}

	for _, comment := range comments {
		pr.Comments = append(pr.Comments, model.Comment{
			ID: "issue:" + strconv.FormatInt(comment.ID, 10), Author: comment.User.Login,
			Url: comment.URL, CreatedAt: comment.CreatedAt,
		})
		pr.LastMessage = latest(pr.LastMessage, comment.CreatedAt)
	}

	return nil
}

func latest(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}

	return a
}

func terminal(state string) bool {
	return strings.EqualFold(state, "completed") || strings.EqualFold(state, "success") || strings.EqualFold(state, "failure") || strings.EqualFold(state, "error")
}
