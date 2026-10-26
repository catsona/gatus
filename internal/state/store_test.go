package state

import (
	"path/filepath"
	"testing"

	"git.pegasko.art/catsona/gstatus/internal/model"
)

func TestAccountStateAndDeliverySurviveReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}

	alice := model.Account{ID: "1", Login: "alice", Host: "github.com"}
	bob := model.Account{ID: "2", Login: "bob", Host: "github.com"}
	data := model.AccountState{Initialized: true, Records: map[string]model.Record{
		"PR_1": {PR: model.PR{ID: "PR_1", Title: "Alice's PR"}, Sequence: 4, Seen: 2, ManualUnread: true},
	}}
	delivery := model.Delivery{ID: "event-4", Account: alice, Event: model.Event{Type: "ci.failure"}}

	if err := store.Save(alice, data, []model.Delivery{delivery}); err != nil {
		t.Fatal(err)
	}

	other, err := store.Load(bob)
	if err != nil || other.Initialized || len(other.Records) != 0 {
		t.Fatalf("account state leaked: %+v %v", other, err)
	}

	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	store, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	restored, err := store.Load(alice)
	if err != nil || restored.Records["PR_1"].Seen != 2 || !restored.Records["PR_1"].ManualUnread {
		t.Fatalf("read state was lost: %+v %v", restored, err)
	}

	pending, err := store.Pending(alice)
	if err != nil || len(pending) != 1 || pending[0].ID != "event-4" {
		t.Fatalf("pending delivery was lost: %+v %v", pending, err)
	}

	if err := store.RemoveDelivery(alice, "event-4"); err != nil {
		t.Fatal(err)
	}

	pending, err = store.Pending(bob)
	if err != nil || len(pending) != 0 {
		t.Fatalf("delivery crossed account boundary: %+v %v", pending, err)
	}
}

func TestHerdrLinkSurvivesReopenAndSharesWorkspace(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}

	first := model.HerdrLink{PRURL: "https://github.com/acme/app/pull/1", WorkspaceID: "space-1", Label: "Renamed", Root: "/work/one"}
	second := model.HerdrLink{PRURL: "https://github.com/acme/app/pull/2", WorkspaceID: "space-1", Label: "Renamed", Root: "/work/one"}
	if err := store.SaveHerdrLink(first); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveHerdrLink(second); err != nil {
		t.Fatal(err)
	}

	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	loaded, ok, err := store.HerdrLink(first.PRURL)
	if err != nil || !ok || loaded != first {
		t.Fatalf("first Herdr link = %+v, %t, %v", loaded, ok, err)
	}
	loaded, ok, err = store.HerdrLink(second.PRURL)
	if err != nil || !ok || loaded.WorkspaceID != first.WorkspaceID {
		t.Fatalf("shared Herdr workspace = %+v, %t, %v", loaded, ok, err)
	}
}

func TestHerdrLinkReplacementAndRemoval(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	url := "https://github.com/acme/app/pull/1"
	if err := store.SaveHerdrLink(model.HerdrLink{PRURL: url, WorkspaceID: "space-1"}); err != nil {
		t.Fatal(err)
	}
	replacement := model.HerdrLink{PRURL: url, WorkspaceID: "space-2", Label: "New", Root: "/work/two"}
	if err := store.SaveHerdrLink(replacement); err != nil {
		t.Fatal(err)
	}
	loaded, ok, err := store.HerdrLink(url)
	if err != nil || !ok || loaded != replacement {
		t.Fatalf("replacement = %+v, %t, %v", loaded, ok, err)
	}
	if err := store.RemoveHerdrLink(url); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := store.HerdrLink(url); err != nil || ok {
		t.Fatalf("removed Herdr link = %t, %v", ok, err)
	}
}
