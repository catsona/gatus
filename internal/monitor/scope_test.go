package monitor

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"git.pegasko.art/catsona/gstatus/internal/config"
	"git.pegasko.art/catsona/gstatus/internal/model"
	"git.pegasko.art/catsona/gstatus/internal/state"
)

type scopedSource struct {
	accountSource
	prs     []model.PR
	filters []string
	details []string
	gate    chan struct{}
	started chan struct{}
	once    sync.Once
}

func (s *scopedSource) List(_ context.Context, _ string, _ string, _ []string, filter string) ([]model.PR, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.filters = append(s.filters, filter)
	var result []model.PR
	for _, pr := range s.prs {
		match := filter == "all" || pr.State == filter || filter == "draft" && pr.State == "open" && pr.Draft
		if match {
			result = append(result, pr)
		}
	}
	return result, nil
}

func (s *scopedSource) Detail(ctx context.Context, _ string, requested model.PR) (model.PR, error) {
	s.mu.Lock()
	s.details = append(s.details, requested.ID)
	gate, started := s.gate, s.started
	var result model.PR
	for _, pr := range s.prs {
		if pr.ID == requested.ID {
			result = pr
		}
	}
	s.mu.Unlock()
	if started != nil {
		s.once.Do(func() { close(started) })
	}
	if gate != nil {
		select {
		case <-gate:
		case <-ctx.Done():
			return model.PR{}, ctx.Err()
		}
	}
	return result, nil
}

func TestStateFilterRestrictsDiscoveryDetailsAndSnapshotButSearchDoesNot(t *testing.T) {
	store, err := state.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	source := &scopedSource{accountSource: accountSource{token: "alice"}, prs: []model.PR{
		{ID: "open", Repository: "acme/app", Title: "Alpha", State: "open", Complete: true},
		{ID: "draft", Repository: "acme/app", Title: "Beta", State: "open", Draft: true, Complete: true},
		{ID: "closed", Repository: "acme/app", State: "closed", Complete: true},
		{ID: "merged", Repository: "acme/app", State: "merged", Complete: true},
	}}
	cfg := config.Default()
	cfg.View.Filter = "all"
	engine := New(source, store, cfg)
	ctx := context.Background()
	if err := engine.CheckAccount(ctx); err != nil {
		t.Fatal(err)
	}
	if err := engine.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	if err := engine.MarkUnread("merged"); err != nil {
		t.Fatal(err)
	}

	for _, scenario := range []struct {
		filter string
		ids    []string
	}{
		{"open", []string{"draft", "open"}},
		{"draft", []string{"draft"}},
		{"closed", []string{"closed"}},
		{"merged", []string{"merged"}},
		{"all", []string{"closed", "draft", "merged", "open"}},
	} {
		t.Run(scenario.filter, func(t *testing.T) {
			source.details, source.filters = nil, nil
			cfg.View.Filter, cfg.View.Search = scenario.filter, "Alpha"
			engine.SetConfig(cfg)
			snapshot := engine.Snapshot()
			if len(snapshot.Records) != len(scenario.ids) || len(snapshot.RefreshStates) != len(scenario.ids) {
				t.Fatalf("excluded PRs still monitored or queued: records=%d states=%v", len(snapshot.Records), snapshot.RefreshStates)
			}
			if err := engine.Refresh(ctx); err != nil {
				t.Fatal(err)
			}
			slices.Sort(source.details)
			if !slices.Equal(source.filters, []string{scenario.filter}) || !slices.Equal(source.details, scenario.ids) {
				t.Fatalf("filter=%s discoveries=%v details=%v", scenario.filter, source.filters, source.details)
			}
			if scenario.filter == "all" {
				for _, record := range engine.Snapshot().Records {
					if record.PR.ID == "merged" && !record.ManualUnread {
						t.Fatal("filter changes discarded the read state")
					}
				}
			}
		})
	}
}

func TestPRLeavingStateFilterStopsFutureRefreshes(t *testing.T) {
	for _, next := range []struct {
		filter, initial, state string
		draft                  bool
	}{
		{"open", "open", "merged", false},
		{"draft", "open", "open", true},
		{"closed", "closed", "open", false},
	} {
		t.Run(next.filter, func(t *testing.T) {
			store, err := state.Open(filepath.Join(t.TempDir(), "state.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			source := &scopedSource{accountSource: accountSource{token: "alice"}, prs: []model.PR{
				{ID: "a", Repository: "acme/app", State: next.initial, Draft: next.draft, Complete: true},
			}}
			cfg := config.Default()
			cfg.View.Filter = next.filter
			engine := New(source, store, cfg)
			ctx := context.Background()
			if err := engine.CheckAccount(ctx); err != nil {
				t.Fatal(err)
			}
			if err := engine.Refresh(ctx); err != nil {
				t.Fatal(err)
			}
			source.prs[0].State, source.prs[0].Draft = next.state, false
			if err := engine.Refresh(ctx); err != nil {
				t.Fatal(err)
			}
			if len(engine.Snapshot().Records) != 0 {
				t.Fatal("PR remains monitored after leaving its state filter")
			}
			cached, err := store.Load(model.Account{ID: "alice", Host: "github.com"})
			if err != nil || cached.Records["a"].Unread["pr.state_changed"] != 1 {
				t.Fatalf("final transition was lost: %+v %v", cached, err)
			}
			source.details = nil
			if err := engine.Refresh(ctx); err != nil {
				t.Fatal(err)
			}
			if len(source.details) != 0 {
				t.Fatalf("excluded PR fetched again: %v", source.details)
			}
			if err := engine.RefreshPR(ctx, "a"); err == nil || len(source.details) != 0 {
				t.Fatalf("selected refresh polled an excluded PR: details=%v error=%v", source.details, err)
			}
		})
	}
}

func TestStateFilterChangeCancelsOldWorkAndKeepsItsResultOut(t *testing.T) {
	store, err := state.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	source := &scopedSource{accountSource: accountSource{token: "alice"}, prs: []model.PR{
		{ID: "open", Repository: "acme/app", State: "open", Complete: true},
		{ID: "closed", Repository: "acme/app", State: "closed", Complete: true},
	}}
	cfg := config.Default()
	cfg.View.Filter = "all"
	engine := New(source, store, cfg)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := engine.CheckAccount(ctx); err != nil {
		t.Fatal(err)
	}
	if err := engine.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	source.gate, source.started = make(chan struct{}), make(chan struct{})
	done := make(chan error, 1)
	go func() { done <- engine.Refresh(ctx) }()
	select {
	case <-source.started:
	case <-time.After(3 * time.Second):
		cancel()
		<-done
		t.Fatal("refresh did not start")
	}
	cfg.View.Filter = "closed"
	engine.SetConfig(cfg)
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("old-scope refresh was accepted: %v", err)
	}
	if snapshot := engine.Snapshot(); len(snapshot.Records) != 1 || snapshot.Records[0].PR.ID != "closed" {
		t.Fatalf("old-scope records remained visible: %+v", snapshot.Records)
	}
	source.mu.Lock()
	source.gate, source.started = nil, nil
	source.details = nil
	source.mu.Unlock()
	if err := engine.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(source.details, []string{"closed"}) {
		t.Fatalf("new filter polled old PRs: %v", source.details)
	}
}
