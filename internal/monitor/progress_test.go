package monitor

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"git.pegasko.art/catsona/gstatus/internal/config"
	"git.pegasko.art/catsona/gstatus/internal/model"
	"git.pegasko.art/catsona/gstatus/internal/state"
)

type progressSource struct {
	accountSource
	prs     []model.PR
	started chan string
	gates   map[string]chan struct{}
	failure error
}

func (s *progressSource) List(context.Context, string, string, []string, string) ([]model.PR, error) {
	return s.prs, nil
}

func (s *progressSource) Detail(ctx context.Context, _ string, pr model.PR) (model.PR, error) {
	s.mu.Lock()
	gate, started, failure := s.gates[pr.ID], s.started, s.failure
	s.mu.Unlock()
	if started != nil {
		started <- pr.ID
	}
	if gate != nil {
		select {
		case <-gate:
		case <-ctx.Done():
			return model.PR{}, ctx.Err()
		}
	}
	return pr, failure
}

func waitRefreshState(t *testing.T, engine *Engine, id string, expected RefreshState) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if engine.Snapshot().RefreshStates[id] == expected {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("refresh state for %s: got %q, want %q", id, engine.Snapshot().RefreshStates[id], expected)
}

func TestFullRefreshReportsWorkerQueueAndReturnsToIdle(t *testing.T) {
	store, err := state.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	permits := make(chan struct{}, 6)
	source := &progressSource{accountSource: accountSource{token: "alice"}, started: make(chan string, 6), gates: map[string]chan struct{}{}}
	for i := 0; i < 6; i++ {
		id := fmt.Sprintf("PR_%d", i)
		source.prs = append(source.prs, model.PR{ID: id, Repository: "acme/app", State: "open", Complete: true})
		source.gates[id] = permits
	}
	engine := New(source, store, config.Default())
	if err := engine.CheckAccount(ctx); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- engine.Refresh(ctx) }()
	finished := false
	defer func() {
		cancel()
		if !finished {
			<-done
		}
	}()
	for i := 0; i < 4; i++ {
		select {
		case <-source.started:
		case <-time.After(3 * time.Second):
			t.Fatal("refresh workers did not start")
		}
	}

	snapshot := engine.Snapshot()
	counts := map[RefreshState]int{}
	for _, status := range snapshot.RefreshStates {
		counts[status]++
	}
	if counts[RefreshRunning] != 4 || counts[RefreshQueued] != 2 || len(snapshot.Records) != 6 {
		t.Fatalf("incorrect worker states: %v, records=%d", snapshot.RefreshStates, len(snapshot.Records))
	}
	for i := 0; i < 6; i++ {
		permits <- struct{}{}
	}
	for _, pr := range source.prs {
		waitRefreshState(t, engine, pr.ID, RefreshIdle)
	}
	if err := <-done; err != nil {
		finished = true
		t.Fatal(err)
	}
	finished = true
	if snapshot.RefreshStates[source.prs[0].ID] == RefreshIdle {
		t.Fatal("a published snapshot changed after worker completion")
	}
}

func TestSelectedRefreshShowsQueuedWhileAnotherRefreshRuns(t *testing.T) {
	store, err := state.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	source := &progressSource{accountSource: accountSource{token: "alice"}, prs: []model.PR{
		{ID: "a", Repository: "acme/app", State: "open", Complete: true},
		{ID: "b", Repository: "acme/app", State: "open", Complete: true},
	}}
	engine := New(source, store, config.Default())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := engine.CheckAccount(ctx); err != nil {
		t.Fatal(err)
	}
	if err := engine.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	source.started = make(chan string, 2)
	source.gates = map[string]chan struct{}{"a": make(chan struct{}), "b": make(chan struct{})}
	first, second := make(chan error, 1), make(chan error, 1)
	go func() { first <- engine.RefreshPR(ctx, "a") }()
	defer func() {
		cancel()
		<-first
	}()
	waitRefreshState(t, engine, "a", RefreshRunning)
	go func() { second <- engine.RefreshPR(ctx, "b") }()
	defer func() {
		cancel()
		<-second
	}()
	waitRefreshState(t, engine, "b", RefreshQueued)
	if engine.Snapshot().RefreshStates["a"] != RefreshRunning {
		t.Fatal("queued work hid the currently running request")
	}
	close(source.gates["a"])
	waitRefreshState(t, engine, "b", RefreshRunning)
	close(source.gates["b"])
	waitRefreshState(t, engine, "b", RefreshIdle)
}

func TestFailedSelectedRefreshClearsItsIndicator(t *testing.T) {
	store, err := state.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	source := &progressSource{accountSource: accountSource{token: "alice"}, prs: []model.PR{{ID: "a", Repository: "acme/app", State: "open", Complete: true}}}
	engine := New(source, store, config.Default())
	if err := engine.CheckAccount(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := engine.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	source.failure = errors.New("source unavailable")
	if err := engine.RefreshPR(context.Background(), "a"); err == nil {
		t.Fatal("source failure was ignored")
	}
	if state := engine.Snapshot().RefreshStates["a"]; state != RefreshIdle {
		t.Fatalf("failed refresh stayed active: %q", state)
	}
}

func TestFullRefreshFailureClearsQueuedIndicators(t *testing.T) {
	store, err := state.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	source := &progressSource{accountSource: accountSource{token: "alice"}, prs: []model.PR{{ID: "a", Repository: "acme/app", State: "open", Complete: true}}}
	engine := New(source, store, config.Default())
	if err := engine.CheckAccount(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := engine.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	source.failure = errors.New("source unavailable")
	engine.RequestRefresh()
	if engine.Snapshot().RefreshStates["a"] != RefreshQueued {
		t.Fatal("requested full refresh did not queue the known PR")
	}
	if err := engine.Refresh(context.Background()); err == nil {
		t.Fatal("source failure was ignored")
	}
	if status := engine.Snapshot().RefreshStates["a"]; status != RefreshIdle {
		t.Fatalf("failed full refresh stayed active: %q", status)
	}
}

func TestCancelledQueuedRefreshDoesNotClearTheRunningPR(t *testing.T) {
	store, err := state.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	source := &progressSource{accountSource: accountSource{token: "alice"}, prs: []model.PR{
		{ID: "a", Repository: "acme/app", State: "open", Complete: true},
		{ID: "b", Repository: "acme/app", State: "open", Complete: true},
	}}
	engine := New(source, store, config.Default())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := engine.CheckAccount(ctx); err != nil {
		t.Fatal(err)
	}
	if err := engine.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	source.gates = map[string]chan struct{}{"a": make(chan struct{})}
	source.started = make(chan string, 2)
	first := make(chan error, 1)
	go func() { first <- engine.RefreshPR(ctx, "a") }()
	defer func() {
		cancel()
		<-first
	}()
	waitRefreshState(t, engine, "a", RefreshRunning)
	queuedCtx, cancelQueued := context.WithCancel(ctx)
	defer cancelQueued()
	second := make(chan error, 1)
	go func() { second <- engine.RefreshPR(queuedCtx, "b") }()
	finished := false
	defer func() {
		cancel()
		if !finished {
			<-second
		}
	}()
	waitRefreshState(t, engine, "b", RefreshQueued)
	cancelQueued()
	waitRefreshState(t, engine, "b", RefreshIdle)
	if engine.Snapshot().RefreshStates["a"] != RefreshRunning {
		t.Fatal("cancelling queued work hid the running PR")
	}
	close(source.gates["a"])
	if err := <-second; !errors.Is(err, context.Canceled) {
		finished = true
		t.Fatalf("queued cancellation was ignored: %v", err)
	}
	finished = true
	if len(source.started) != 1 {
		t.Fatal("cancelled queued PR reached the source")
	}
}
