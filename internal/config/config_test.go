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
		"keys:\n  quit: [q]\n  refresh: [ctrl+c]\n",
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

func TestNewSortDirectionShortcutPreservesExistingBindings(t *testing.T) {
	for _, binding := range []string{"S", "shift+s"} {
		cfg, err := Parse([]byte("keys:\n  sort: [" + binding + "]\n"))
		if err != nil {
			t.Fatalf("existing config no longer loads: %v", err)
		}
		if len(cfg.Keys["sort_direction"]) != 0 || len(cfg.Keys["sort"]) != 1 || cfg.Keys["sort"][0] != binding {
			t.Fatalf("existing shortcut was replaced: %+v", cfg.Keys)
		}
	}
	if _, err := Parse([]byte("keys:\n  sort: [S]\n  sort_direction: [S]\n")); err == nil {
		t.Fatal("explicit duplicate bindings were accepted")
	}
}

func TestNewHerdrShortcutPreservesExistingBindings(t *testing.T) {
	cfg, err := Parse([]byte("keys:\n  open: [e]\n"))
	if err != nil {
		t.Fatalf("existing config no longer loads: %v", err)
	}
	if len(cfg.Keys["open_herdr"]) != 0 || len(cfg.Keys["open"]) != 1 || cfg.Keys["open"][0] != "e" {
		t.Fatalf("existing shortcut was replaced: %+v", cfg.Keys)
	}
	if _, err := Parse([]byte("keys:\n  open: [e]\n  open_herdr: [e]\n")); err == nil {
		t.Fatal("explicit duplicate bindings were accepted")
	}
}

func TestTemplateFieldsThroughIndex(t *testing.T) {
	cfg := Default()
	cfg.Notifications.Template = `{{if .Event.Type}}{{$check := index .PR.Checks 0}}{{$check.Missing}}{{end}}`
	if err := cfg.Validate(); err == nil {
		t.Fatal("accepted an invalid field on an indexed check")
	}

	cfg.Notifications.Template = `{{if .Event.Type}}{{$check := index .PR.Checks 0}}{{$check.Name}}{{end}}`
	if err := cfg.Validate(); err != nil {
		t.Fatalf("valid indexed template rejected: %v", err)
	}
}

func TestFunctionKeysAndModifierOrder(t *testing.T) {
	_, err := Parse([]byte("keys:\n  copy_url: [f1]\n  open: [shift+ctrl+x]\nnotifications:\n  template: '{{with .PR}}{{.Url}}{{end}} {{range .PR.Runs}}{{.Name}}{{end}}'\n"))
	if err != nil {
		t.Fatalf("valid bindings or template rejected: %v", err)
	}
}

func TestHerdrConfiguration(t *testing.T) {
	cfg, err := Parse([]byte("herdr:\n  enabled: true\n  root: ~/spaces\n"))
	if err != nil || !cfg.Herdr.Enabled || cfg.Herdr.Root != "~/spaces" {
		t.Fatalf("herdr settings were not parsed: %+v %v", cfg.Herdr, err)
	}

	defaults, err := Parse([]byte("{}\n"))
	if err != nil || defaults.Herdr.Enabled || defaults.Herdr.Root != "~" {
		t.Fatalf("herdr defaults changed: %+v %v", defaults.Herdr, err)
	}
}

func TestHerdrEnabledRequiresRoot(t *testing.T) {
	if _, err := Parse([]byte("herdr:\n  enabled: true\n  root: '  '\n")); err == nil {
		t.Fatal("enabled Herdr integration accepted a blank root")
	}
}

func TestHerdrSpaceActionsUseSeparateDefaults(t *testing.T) {
	cfg := Default()
	if len(cfg.Keys["open_herdr"]) != 1 || cfg.Keys["open_herdr"][0] != "e" {
		t.Fatalf("open Herdr shortcut = %v", cfg.Keys["open_herdr"])
	}
	if len(cfg.Keys["create_herdr"]) != 1 || cfg.Keys["create_herdr"][0] != "shift+e" {
		t.Fatalf("create Herdr shortcut = %v", cfg.Keys["create_herdr"])
	}

	parsed, err := Parse([]byte("keys:\n  create_herdr: [shift+e]\n"))
	if err != nil || len(parsed.Keys["create_herdr"]) != 1 {
		t.Fatalf("create Herdr shortcut did not round-trip: %+v %v", parsed.Keys, err)
	}

	parsed, err = Parse([]byte("keys:\n  open: [E]\n"))
	if err != nil || len(parsed.Keys["create_herdr"]) != 0 {
		t.Fatalf("existing uppercase E binding was not preserved: %+v %v", parsed.Keys, err)
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
