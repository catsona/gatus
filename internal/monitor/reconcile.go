package monitor

import (
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/catsona/gstatus/internal/config"
	"github.com/catsona/gstatus/internal/model"
)

// Reconcile derives events from complete revisions and never mutates the input state.
func Reconcile(account model.Account, current model.AccountState, prs []model.PR, repositories []string, now time.Time, complete bool, notifications config.Notifications) (model.AccountState, []model.Delivery) {
	next := current
	next.Records = maps.Clone(current.Records)
	if next.Records == nil {
		next.Records = map[string]model.Record{}
	}

	var deliveries []model.Delivery
	for _, pr := range prs {
		if !pr.Complete {
			continue
		}

		pr.Checks = slices.Clone(pr.Checks)
		pr.Runs = slices.Clone(pr.Runs)
		pr.Comments = slices.Clone(pr.Comments)
		pr.Reviews = slices.Clone(pr.Reviews)
		old, exists := current.Records[pr.ID]
		record := old
		record.Pending = slices.Clone(old.Pending)
		record.Unread = maps.Clone(old.Unread)
		if record.Unread == nil {
			record.Unread = map[string]int{}
		}

		baseline := !current.Initialized || (!exists && (pr.CreatedAt.Before(current.Since) || !inScope(pr.Repository, current.Scope)))
		status, fingerprint, summary := aggregateCI(pr)
		pr.CIStatus = status
		record.PR = pr

		emit := func(event model.Event) {
			event.ID = account.Key() + ":" + pr.ID + ":" + event.ID
			record.Sequence++
			record.Pending = append(record.Pending, model.PendingEvent{Sequence: record.Sequence, Type: event.Type})
			record.Unread[event.Type]++

			if notifications.Enabled && slices.Contains(notifications.Events, event.Type) {
				deliveries = append(deliveries, model.Delivery{ID: event.ID, Account: account, PR: pr, Event: event})
			}
		}

		if baseline || !exists {
			record.CINotified = fingerprint
			record.CICandidate = ""
			if !baseline {
				emit(model.Event{ID: "created", Type: "pr.created", Summary: "PR created", Url: pr.Url, OccurredAt: pr.CreatedAt.Format(time.RFC3339)})
			}
		} else {
			knownComments := map[string]bool{}
			for _, comment := range old.PR.Comments {
				knownComments[comment.ID] = true
			}

			participated := map[string]time.Time{}
			for _, comment := range pr.Comments {
				if comment.ThreadID == "" || !strings.EqualFold(comment.Author, account.Login) {
					continue
				}

				when, exists := participated[comment.ThreadID]
				if !exists || comment.CreatedAt.Before(when) {
					participated[comment.ThreadID] = comment.CreatedAt
				}
			}

			for _, comment := range pr.Comments {
				if knownComments[comment.ID] || strings.EqualFold(comment.Author, account.Login) {
					continue
				}

				eventType, label := "comment.created", "Comment"
				if when, exists := participated[comment.ThreadID]; comment.ThreadID != "" && exists && !comment.CreatedAt.Before(when) {
					eventType, label = "thread.reply", "Reply"
				}

				emit(model.Event{ID: "comment:" + comment.ID, Type: eventType, Summary: label + " from @" + comment.Author, Url: comment.Url, Actor: comment.Author, OccurredAt: comment.CreatedAt.Format(time.RFC3339)})
			}

			knownReviews := map[string]bool{}
			for _, review := range old.PR.Reviews {
				knownReviews[review.ID] = true
			}

			for _, review := range pr.Reviews {
				if !knownReviews[review.ID] && !strings.EqualFold(review.Author, account.Login) {
					emit(model.Event{ID: "review:" + review.ID, Type: "review.submitted", Summary: "Review from @" + review.Author + ": " + review.State, Url: review.Url, Actor: review.Author, OccurredAt: review.SubmittedAt.Format(time.RFC3339)})
				}
			}

			if old.PR.State != pr.State || old.PR.Draft != pr.Draft {
				previous, value := stateName(old.PR), stateName(pr)
				emit(model.Event{ID: fmt.Sprintf("state:%d:%s", record.Sequence+1, value), Type: "pr.state_changed", Summary: "PR " + previous + " → " + value, Url: pr.Url, Field: "state", Old: previous, New: value, OccurredAt: pr.UpdatedAt.Format(time.RFC3339)})
			}

			if status == "success" || status == "failure" {
				if fingerprint != record.CICandidate {
					record.CICandidate = fingerprint
					record.CICandidateAt = now
				} else if fingerprint != record.CINotified && now.Sub(record.CICandidateAt) >= 5*time.Second {
					emit(model.Event{ID: "ci:" + fingerprint, Type: "ci." + status, Summary: summary, Url: pr.Url + "/checks", Field: "ci", Old: old.PR.CIStatus, New: status, OccurredAt: pr.LastCI.Format(time.RFC3339)})
					record.CINotified = fingerprint
				}
			} else {
				record.CICandidate = ""
			}
		}

		next.Records[pr.ID] = record
	}

	if complete {
		next.Initialized = true
		if next.Since.IsZero() {
			next.Since = now
		}
		next.Scope = slices.Clone(repositories)
	}

	return next, deliveries
}

// Acknowledge preserves events and manual marks created after the displayed sequence.
func Acknowledge(record model.Record, through uint64) model.Record {
	through = min(through, record.Sequence)
	record.Seen = max(record.Seen, through)
	record.Unread = map[string]int{}
	var remaining []model.PendingEvent
	for _, event := range record.Pending {
		if event.Sequence > record.Seen {
			remaining = append(remaining, event)
			record.Unread[event.Type]++
		}
	}
	record.Pending = remaining
	if record.ManualSequence <= through {
		record.ManualUnread = false
	}

	return record
}

func stateName(pr model.PR) string {
	if pr.Draft && pr.State == "open" {
		return "draft"
	}

	return pr.State
}

func inScope(repository string, repositories []string) bool {
	return len(repositories) == 0 || slices.ContainsFunc(repositories, func(value string) bool {
		return strings.EqualFold(value, repository)
	})
}
