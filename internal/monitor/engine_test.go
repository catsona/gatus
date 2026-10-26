package monitor

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/catsona/gatus/internal/config"
	"github.com/catsona/gatus/internal/github"
	"github.com/catsona/gatus/internal/model"
	"github.com/catsona/gatus/internal/state"
)

type accountSource struct {
	mu      sync.Mutex
	token   string
	started chan struct{}
	release chan struct{}
	cancels int
}

func (s *accountSource) Credentials(context.Context) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.token, nil
}

func (s *accountSource) Account(_ context.Context, token string) (model.Account, error) {
	return model.Account{ID: token, Login: token, Host: "github.com"}, nil
}

func (s *accountSource) List(_ context.Context, token, _ string, _ []string, _ string) ([]model.PR, error) {
	return []model.PR{{ID: "PR_" + token, Repository: "acme/app", State: "open", Title: token}}, nil
}

func (s *accountSource) Detail(_ context.Context, token string, pr model.PR) (model.PR, error) {
	if token == "alice" && s.started != nil {
		close(s.started)
		<-s.release
	}

	pr.HeadSHA = "head"
	pr.Complete = true
	return pr, nil
}

func (s *accountSource) Rerun(context.Context, string, model.PR, bool) (github.Result, error) {
	return github.Result{Accepted: 1}, nil
}

func (s *accountSource) Cancel(context.Context, string, model.PR) (github.Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cancels++
	return github.Result{Accepted: 1}, nil
}

func TestLiveAccountSwitchRejectsDelayedOldResult(t *testing.T) {
	store, err := state.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	source := &accountSource{token: "alice", started: make(chan struct{}), release: make(chan struct{})}
	cfg := config.Default()
	cfg.Refresh.Enabled = false
	engine := New(source, store, cfg)
	if err := engine.CheckAccount(context.Background()); err != nil {
		t.Fatal(err)
	}

	finished := make(chan error, 1)
	go func() { finished <- engine.Refresh(context.Background()) }()
	select {
	case <-source.started:
	case <-time.After(3 * time.Second):
		t.Fatal("old account fetch did not start")
	}

	source.mu.Lock()
	source.token = "bob"
	source.mu.Unlock()
	if err := engine.CheckAccount(context.Background()); err != nil {
		t.Fatal(err)
	}

	if snapshot := engine.Snapshot(); snapshot.Account.Login != "bob" || len(snapshot.Records) != 0 {
		t.Fatalf("old user remained visible: %+v", snapshot)
	}
	if len(engine.Snapshot().RefreshStates) != 0 {
		t.Fatal("old account refresh indicators survived account switching")
	}

	close(source.release)
	<-finished
	if err := engine.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}

	snapshot := engine.Snapshot()
	if len(snapshot.Records) != 1 || snapshot.Records[0].PR.Title != "bob" || snapshot.Records[0].UnreadCount() != 0 {
		t.Fatalf("wrong account state: %+v", snapshot)
	}

	alice, err := store.Load(model.Account{Host: "github.com", ID: "alice"})
	if err != nil || len(alice.Records) != 0 {
		t.Fatalf("cancelled result was persisted: %+v %v", alice, err)
	}
}

func TestRerunDetectsAccountSwitchBeforeMutation(t *testing.T) {
	store, err := state.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	source := &accountSource{token: "alice"}
	engine := New(source, store, config.Default())
	if err := engine.CheckAccount(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := engine.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}

	source.mu.Lock()
	source.token = "bob"
	source.mu.Unlock()
	if _, err := engine.Rerun(context.Background(), "PR_alice", true); err == nil {
		t.Fatal("rerun accepted after the active account changed")
	}
}

func TestCancelDetectsAccountSwitchBeforeMutation(t *testing.T) {
	store, err := state.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	source := &accountSource{token: "alice"}
	engine := New(source, store, config.Default())
	if err := engine.CheckAccount(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := engine.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}

	source.mu.Lock()
	source.token = "bob"
	source.mu.Unlock()
	if _, err := engine.Cancel(context.Background(), "PR_alice"); err == nil {
		t.Fatal("cancel accepted after the active account changed")
	}
	if source.cancels != 0 {
		t.Fatal("sent cancellation using an inactive account")
	}
}

func TestAccountWatcherRunsWithAutoRefreshDisabled(t *testing.T) {
	store, err := state.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	source := &accountSource{token: "alice"}
	cfg := config.Default()
	cfg.Refresh.Enabled = false
	engine := New(source, store, cfg)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { engine.Run(ctx); close(done) }()
	defer func() { cancel(); <-done }()

	waitFor := func(login string) {
		t.Helper()
		timeout := time.NewTimer(4 * time.Second)
		defer timeout.Stop()
		for {
			select {
			case snapshot := <-engine.Updates():
				if snapshot.Account.Login == login {
					return
				}
			case <-timeout.C:
				t.Fatalf("account watcher did not switch to %s", login)
			}
		}
	}

	waitFor("alice")
	source.mu.Lock()
	source.token = "bob"
	source.mu.Unlock()
	waitFor("bob")
}
