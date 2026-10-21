package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRejectsInvalidConfiguration(t *testing.T) {
	for _, input := range []string{
		"refresh:\n  interval: 1s\n",
		"unknown: true\n",
		"notifications:\n  template: '{{.PR.Missing}}'\n",
		"notifications:\n  template: '{{if eq .Event.Type \"ci.failure\"}}{{.PR.Missing}}{{end}}'\n",
		"notifications:\n  events: [typo]\n",
		"keys:\n  open: [r]\n",
		"keys:\n  copy_url: [shift+r]\n",
		"repositories: [not-a-repository]\n",
		"version: 2\n",
	} {
		t.Run(input, func(t *testing.T) {
			if _, err := Parse([]byte(input)); err == nil {
				t.Fatalf("accepted invalid config: %s", input)
			}
		})
	}
}

func TestFunctionKeysAndModifierOrder(t *testing.T) {
	_, err := Parse([]byte("keys:\n  copy_url: [f1]\n  open: [shift+ctrl+x]\nnotifications:\n  template: '{{with .PR}}{{.Url}}{{end}} {{range .PR.Runs}}{{.Name}}{{end}}'\n"))
	if err != nil {
		t.Fatalf("valid bindings or template rejected: %v", err)
	}
}

func TestEmptyOverridesAndAtomicSave(t *testing.T) {
	cfg, err := Parse([]byte("repositories: []\nnotifications:\n  events: []\nkeys:\n  open: []\n"))
	if err != nil {
		t.Fatal(err)
	}

	if len(cfg.Notifications.Events) != 0 || len(cfg.Keys["open"]) != 0 || !cfg.Read.MarkOnOpen {
		t.Fatalf("empty overrides or defaults lost: %+v", cfg)
	}

	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := Save(path, cfg); err != nil {
		t.Fatal(err)
	}

	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}

	if len(loaded.Notifications.Events) != 0 || len(loaded.Keys["open"]) != 0 {
		t.Fatal("empty overrides were lost after saving")
	}

	invalid := loaded
	invalid.Refresh.Interval = 1
	if err := Save(path, invalid); err == nil {
		t.Fatal("saved invalid settings")
	}

	if _, err := Load(path); err != nil {
		t.Fatalf("invalid save damaged previous settings: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("config permissions: %v %v", info, err)
	}
}
