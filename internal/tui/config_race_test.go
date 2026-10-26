package tui

import (
	"crypto/sha256"
	"os"
	"testing"

	"github.com/catsona/gstatus/internal/config"
)

func TestConflictOverwritePreservesExternalFieldsBeforeWatcher(t *testing.T) {
	m, _, _ := testModel(t, config.Default())
	m.startEditor("webhook_url", "https://example.org/new")
	external := config.Default()
	external.Read.MarkOnOpen = false
	if err := config.Save(m.path, external); err != nil {
		t.Fatal(err)
	}

	m.saveEditor(false)
	if !m.conflict {
		t.Fatal("expected version conflict")
	}
	m.saveEditor(true)
	actual, err := config.Load(m.path)
	if err != nil || actual.Read.MarkOnOpen || actual.Notifications.WebhookURL != "https://example.org/new" {
		t.Fatalf("field overwrite lost an external change: %+v %v", actual, err)
	}
}

func TestStartupConfigAndHashReferToTheSameFile(t *testing.T) {
	m, backend, desktop := testModel(t, config.Default())
	external := config.Default()
	external.Read.MarkOnOpen = false
	if err := config.Save(m.path, external); err != nil {
		t.Fatal(err)
	}

	next := New(Options{ConfigPath: m.path, Config: config.Default(), Backend: backend, Desktop: desktop})
	data, err := os.ReadFile(m.path)
	if err != nil {
		t.Fatal(err)
	}
	next.Update(config.Change{Config: external, Hash: sha256.Sum256(data)})
	if next.cfg.Read.MarkOnOpen || backend.cfg.Read.MarkOnOpen {
		t.Fatal("startup paired a new file hash with an outdated config")
	}
}
