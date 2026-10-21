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
