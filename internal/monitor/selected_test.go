package monitor

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"git.pegasko.art/catsona/gstatus/internal/config"
	"git.pegasko.art/catsona/gstatus/internal/model"
	"git.pegasko.art/catsona/gstatus/internal/state"
)

type selectedSource struct {
	accountSource
	prs       []model.PR
	lists     int
	details   []string
	detailErr error
}

func (s *selectedSource) List(context.Context, string, string, []string, string) ([]model.PR, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lists++
	return append([]model.PR(nil), s.prs...), nil
}

func (s *selectedSource) Detail(_ context.Context, _ string, requested model.PR) (model.PR, error) {
	s.mu.Lock()
	s.details = append(s.details, requested.ID)
	err, started, release := s.detailErr, s.started, s.release
	var result model.PR
	for _, pr := range s.prs {
		if pr.ID == requested.ID {
			result = pr
		}
	}
	s.mu.Unlock()

	if started != nil {
		close(started)
		<-release
	}
	return result, err
}

func TestSelectedRefreshLoadsOnlyOnePRAndPreservesOtherReadState(t *testing.T) {
	store, err := state.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	source := &selectedSource{accountSource: accountSource{token: "alice"}, prs: []model.PR{
		{ID: "a", Repository: "acme/app", Title: "Alpha", State: "open", Complete: true},
		{ID: "b", Repository: "acme/app", Title: "Beta", State: "open", Complete: true},
	}}
	cfg := config.Default()
	cfg.Refresh.Enabled = false
	engine := New(source, store, cfg)
	if err := engine.CheckAccount(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := engine.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := engine.MarkUnread("b"); err != nil {
		t.Fatal(err)
	}
	before, err := store.Load(model.Account{ID: "alice", Host: "github.com"})
	if err != nil {
		t.Fatal(err)
	}

	source.details = nil
	source.prs[0].Title = "Alpha updated"
	source.prs[0].Comments = []model.Comment{{ID: "comment:1", Author: "bob", CreatedAt: time.Now()}}
	if err := engine.RefreshPR(context.Background(), "a"); err != nil {
		t.Fatal(err)
	}
	after, err := store.Load(model.Account{ID: "alice", Host: "github.com"})
	if err != nil {
		t.Fatal(err)
	}
	if source.lists != 1 || len(source.details) != 1 || source.details[0] != "a" {
		t.Fatalf("selected refresh fetched the whole list: lists=%d details=%v", source.lists, source.details)
	}
	if after.Records["a"].PR.Title != "Alpha updated" || after.Records["a"].Unread["comment.created"] != 1 {
		t.Fatalf("selected status or new events were lost: %+v", after.Records["a"])
	}
	if !reflect.DeepEqual(before.Records["b"], after.Records["b"]) {
		t.Fatal("selected refresh changed another PR's status or read state")
	}
}

func TestSelectedRefreshRejectsDelayedResultAfterAccountSwitch(t *testing.T) {
	store, err := state.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	source := &selectedSource{accountSource: accountSource{token: "alice"}, prs: []model.PR{{ID: "a", Repository: "acme/app", State: "open", Complete: true}}}
	engine := New(source, store, config.Default())
	if err := engine.CheckAccount(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := engine.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	source.started, source.release = make(chan struct{}), make(chan struct{})
	finished := make(chan error, 1)
	go func() { finished <- engine.RefreshPR(context.Background(), "a") }()
	select {
	case <-source.started:
	case <-time.After(3 * time.Second):
		close(source.release)
		t.Fatal("selected refresh did not start")
	}

	source.mu.Lock()
	source.token = "bob"
	source.mu.Unlock()
	accountErr := engine.CheckAccount(context.Background())
	close(source.release)
	if err := <-finished; !errors.Is(err, context.Canceled) {
		t.Fatalf("old result was accepted: %v", err)
	}
	if accountErr != nil {
		t.Fatal(accountErr)
	}
	if snapshot := engine.Snapshot(); snapshot.Account.Login != "bob" || len(snapshot.Records) != 0 {
		t.Fatalf("old selected PR leaked into the new account: %+v", snapshot)
	}
}

func TestSelectedRefreshFailureKeepsLastGoodStatus(t *testing.T) {
	store, err := state.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	source := &selectedSource{accountSource: accountSource{token: "alice"}, prs: []model.PR{{ID: "a", Repository: "acme/app", Title: "Last good", State: "open", Complete: true}}}
	engine := New(source, store, config.Default())
	if err := engine.CheckAccount(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := engine.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	source.detailErr = errors.New("GitHub unavailable")
	if err := engine.RefreshPR(context.Background(), "a"); err == nil {
		t.Fatal("selected refresh hid a source failure")
	}
	if records := engine.Snapshot().Records; len(records) != 1 || records[0].PR.Title != "Last good" {
		t.Fatalf("failed refresh erased the last good status: %+v", records)
	}
}
