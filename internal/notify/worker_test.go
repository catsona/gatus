package notify

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"git.pegasko.art/catsona/gstatus/internal/config"
	"git.pegasko.art/catsona/gstatus/internal/model"
	"git.pegasko.art/catsona/gstatus/internal/state"
)

func notificationStore(t *testing.T) (*state.Store, model.Account) {
	t.Helper()
	store, err := state.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	account := model.Account{ID: "1", Login: "alice", Host: "github.com"}
	return store, account
}

func TestWebhookSerializesTextAndFiltersAccounts(t *testing.T) {
	store, alice := notificationStore(t)
	delivery := model.Delivery{ID: "event", Account: alice, PR: model.PR{Title: "A \"quote\"\n猫"}, Event: model.Event{Type: "ci.success", Summary: "CI passed"}}
	if err := store.Save(alice, model.AccountState{}, []model.Delivery{delivery}); err != nil {
		t.Fatal(err)
	}

	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Method != "POST" || r.Header.Get("Content-Type") != "application/json" {
			t.Error("incorrect webhook request contract")
		}

		var payload map[string]string
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil || len(payload) != 1 || payload["text"] != "A \"quote\"\n猫 | CI passed" {
			t.Errorf("invalid JSON payload: %+v %v", payload, err)
		}
		w.WriteHeader(204)
	}))
	defer server.Close()
	cfg := config.Default().Notifications
	cfg.Enabled, cfg.WebhookURL, cfg.Template = true, server.URL, "{{.PR.Title}} | {{.Event.Summary}}"
	worker := Worker{Store: store, Client: server.Client()}

	if err := worker.Tick(context.Background(), model.Account{ID: "2", Host: "github.com"}, cfg, time.Now()); err != nil || requests.Load() != 0 {
		t.Fatal("another account's queue was delivered")
	}
	if err := worker.Tick(context.Background(), alice, cfg, time.Now()); err != nil || requests.Load() != 1 {
		t.Fatalf("delivery failed: %v", err)
	}
	if pending, err := store.Pending(alice); err != nil || len(pending) != 0 {
		t.Fatalf("successful event still pending: %+v %v", pending, err)
	}
}

func TestRetryAfterAndPermanentFailure(t *testing.T) {
	store, account := notificationStore(t)
	if err := store.Save(account, model.AccountState{}, []model.Delivery{{ID: "retry", Account: account, Event: model.Event{Type: "ci.failure"}}}); err != nil {
		t.Fatal(err)
	}

	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests.Add(1) == 1 {
			w.Header().Set("Retry-After", "30")
			w.WriteHeader(429)
		} else {
			w.WriteHeader(400)
		}
	}))
	defer server.Close()
	cfg := config.Default().Notifications
	cfg.Enabled, cfg.WebhookURL = true, server.URL
	worker := Worker{Store: store, Client: server.Client()}
	now := time.Now()
	if err := worker.Tick(context.Background(), account, cfg, now); err == nil {
		t.Fatal("rate limit was not reported")
	}

	pending, err := store.Pending(account)
	if err != nil || len(pending) != 1 || pending[0].Attempts != 1 || pending[0].NextAttempt.Before(now.Add(30*time.Second)) {
		t.Fatalf("retry was not persisted: %+v %v", pending, err)
	}
	_ = worker.Tick(context.Background(), account, cfg, now.Add(5*time.Second))
	if requests.Load() != 1 {
		t.Fatal("retried before Retry-After elapsed")
	}

	_ = worker.Tick(context.Background(), account, cfg, now.Add(time.Minute))
	_ = worker.Tick(context.Background(), account, cfg, now.Add(2*time.Minute))
	if requests.Load() != 2 {
		t.Fatal("retried a permanent error")
	}

	cfg.Enabled = false
	if err := worker.Tick(context.Background(), account, cfg, now.Add(3*time.Minute)); err != nil {
		t.Fatal(err)
	}
	pending, _ = store.Pending(account)
	if len(pending) != 0 {
		t.Fatal("disabled notification remained queued")
	}
}

func TestRetryBudgetStopsAfterFiveAttempts(t *testing.T) {
	store, account := notificationStore(t)
	_ = store.Save(account, model.AccountState{}, []model.Delivery{{ID: "event", Account: account, Event: model.Event{Type: "ci.failure"}}})
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.WriteHeader(500)
	}))
	defer server.Close()
	cfg := config.Default().Notifications
	cfg.Enabled, cfg.WebhookURL = true, server.URL
	worker := Worker{Store: store, Client: server.Client()}
	for i := 0; i < 8; i++ {
		_ = worker.Tick(context.Background(), account, cfg, time.Now().Add(time.Duration(i)*time.Hour))
	}
	if requests.Load() != 5 {
		t.Fatalf("HTTP attempts = %d, want five", requests.Load())
	}
	pending, _ := store.Pending(account)
	if len(pending) != 1 || !strings.Contains(pending[0].LastError, "500") {
		t.Fatal("exhausted delivery lost its error")
	}
}
