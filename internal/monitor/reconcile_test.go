package monitor

import (
	"testing"
	"time"

	"github.com/catsona/gatus/internal/config"
	"github.com/catsona/gatus/internal/model"
)

func TestBaselineRepliesAndLateAcknowledgement(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	account := model.Account{ID: "1", Login: "alice", Host: "github.com"}
	cfg := config.Default().Notifications
	cfg.Enabled = true
	pr := model.PR{ID: "PR_1", Repository: "acme/app", HeadSHA: "head", Complete: true, State: "open", Comments: []model.Comment{
		{ID: "root", ThreadID: "root", Author: "alice", CreatedAt: now},
	}}

	state, deliveries := Reconcile(account, model.AccountState{}, []model.PR{pr}, nil, now, true, cfg)
	if !state.Initialized || state.Records[pr.ID].UnreadCount() != 0 || len(deliveries) != 0 {
		t.Fatalf("first load was not a read baseline: %+v", state)
	}

	pr.Comments = append(pr.Comments, model.Comment{ID: "reply", ThreadID: "root", Author: "bob", CreatedAt: now.Add(time.Second)})
	state, deliveries = Reconcile(account, state, []model.PR{pr}, nil, now.Add(time.Second), true, cfg)
	if len(deliveries) != 1 || deliveries[0].Event.Type != "thread.reply" || state.Records[pr.ID].UnreadCount() != 1 {
		t.Fatalf("reply not classified exactly once: %+v", deliveries)
	}

	openedSequence := state.Records[pr.ID].Sequence
	pr.Comments = append(pr.Comments, model.Comment{ID: "later", Author: "charlie", CreatedAt: now.Add(2 * time.Second)})
	state, _ = Reconcile(account, state, []model.PR{pr}, nil, now.Add(2*time.Second), true, cfg)
	record := Acknowledge(state.Records[pr.ID], openedSequence)
	if record.UnreadCount() != 1 || record.Unread["comment.created"] != 1 {
		t.Fatalf("opening acknowledged a later event: %+v", record)
	}
}

func TestCINotifiesOnceAfterWholeRetryFinishes(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	account := model.Account{ID: "1", Login: "alice", Host: "github.com"}
	cfg := config.Default().Notifications
	cfg.Enabled = true
	pr := model.PR{ID: "PR_1", HeadSHA: "head", Complete: true, Checks: []model.Check{
		{ID: "test", Name: "tests", Revision: "head", RunID: 9, State: "completed", Conclusion: "failure"},
	}, Runs: []model.Run{{ID: 9, WorkflowID: 1, Attempt: 1, HeadSHA: "head", State: "completed", Conclusion: "failure"}}}
	state, _ := Reconcile(account, model.AccountState{}, []model.PR{pr}, nil, now, true, cfg)

	pr.Runs[0].Attempt = 2
	pr.Runs[0].State = "in_progress"
	pr.Checks = append(pr.Checks, model.Check{ID: "lint", Name: "lint", Revision: "head", State: "in_progress"})
	state, pending := Reconcile(account, state, []model.PR{pr}, nil, now.Add(time.Second), true, cfg)
	if len(pending) != 0 {
		t.Fatal("notified while another check was running")
	}

	pr.Runs[0].State = "completed"
	pr.Checks[1].State = "completed"
	pr.Checks[1].Conclusion = "success"
	state, pending = Reconcile(account, state, []model.PR{pr}, nil, now.Add(2*time.Second), true, cfg)
	if len(pending) != 0 {
		t.Fatal("notified before confirming the terminal snapshot")
	}

	state, pending = Reconcile(account, state, []model.PR{pr}, nil, now.Add(8*time.Second), true, cfg)
	if len(pending) != 1 || pending[0].Event.Type != "ci.failure" {
		t.Fatalf("retry result missing: %+v", pending)
	}

	_, pending = Reconcile(account, state, []model.PR{pr}, nil, now.Add(20*time.Second), true, cfg)
	if len(pending) != 0 {
		t.Fatal("repeated the same CI result")
	}

	pr.HeadSHA = "new-head"
	pr.Checks[0].Conclusion = "cancelled"
	_, pending = Reconcile(account, state, []model.PR{pr}, nil, now.Add(30*time.Second), true, cfg)
	if len(pending) != 0 {
		t.Fatal("notified about obsolete CI after a push")
	}
}

func TestSameReviewStatusStillDetectsNewReview(t *testing.T) {
	now := time.Now()
	account := model.Account{ID: "1", Login: "alice", Host: "github.com"}
	pr := model.PR{ID: "PR", Complete: true, ReviewStatus: "approved", Reviews: []model.Review{{ID: "1", Author: "bob"}}}
	cfg := config.Default().Notifications
	cfg.Enabled = true
	state, _ := Reconcile(account, model.AccountState{}, []model.PR{pr}, nil, now, true, cfg)

	pr.Reviews = append(pr.Reviews, model.Review{ID: "2", Author: "charlie", State: "APPROVED"})
	state, deliveries := Reconcile(account, state, []model.PR{pr}, nil, now.Add(time.Second), true, cfg)
	if len(deliveries) != 1 || deliveries[0].Event.Type != "review.submitted" || state.Records[pr.ID].UnreadCount() != 1 {
		t.Fatalf("new approve was lost: %+v", deliveries)
	}
}
