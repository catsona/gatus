package monitor

import (
	"testing"
	"time"

	"github.com/catsona/gstatus/internal/config"
	"github.com/catsona/gstatus/internal/model"
)

func TestQueuedExternalRetryDoesNotConfirmPreviousSuccess(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	account := model.Account{ID: "1", Host: "github.com", Login: "alice"}
	cfg := config.Default().Notifications
	cfg.Enabled = true
	pr := model.PR{ID: "PR", HeadSHA: "head", Complete: true}
	state, _ := Reconcile(account, model.AccountState{}, []model.PR{pr}, nil, now, true, cfg)

	pr.Checks = []model.Check{{
		ID: "check:100", Name: "external-tests", App: "external", Revision: "head",
		State: "completed", Conclusion: "success", StartedAt: now.Add(time.Second), CompletedAt: now.Add(2 * time.Second),
	}}
	state, _ = Reconcile(account, state, []model.PR{pr}, nil, now.Add(3*time.Second), true, cfg)
	pr.Checks = append(pr.Checks, model.Check{ID: "check:101", Name: "external-tests", App: "external", Revision: "head", State: "queued"})

	state, deliveries := Reconcile(account, state, []model.PR{pr}, nil, now.Add(9*time.Second), true, cfg)
	if state.Records[pr.ID].PR.CIStatus != "pending" || len(deliveries) != 0 {
		t.Fatalf("confirmed old green during a queued retry: status=%s, deliveries=%+v", state.Records[pr.ID].PR.CIStatus, deliveries)
	}

	pr.Checks[1].State, pr.Checks[1].Conclusion = "completed", "failure"
	pr.Checks[1].CompletedAt = now.Add(10 * time.Second)
	state, _ = Reconcile(account, state, []model.PR{pr}, nil, now.Add(11*time.Second), true, cfg)
	_, deliveries = Reconcile(account, state, []model.PR{pr}, nil, now.Add(17*time.Second), true, cfg)
	if len(deliveries) != 1 || deliveries[0].Event.Type != "ci.failure" {
		t.Fatalf("latest retry result was not reported: %+v", deliveries)
	}
}
