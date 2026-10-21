package main

import (
	"bytes"
	"runtime/debug"
	"testing"
)

func TestVersion(t *testing.T) {
	var output bytes.Buffer

	if err := run([]string{"--version"}, &output); err != nil {
		t.Fatal(err)
	}

	if got := output.String(); got != "gstatus dev\n" {
		t.Fatalf("version output = %q", got)
	}
}

func TestInstalledModuleVersion(t *testing.T) {
	for _, scenario := range []struct {
		name     string
		linked   string
		module   string
		expected string
	}{
		{"release archive", "v0.1.0", "(devel)", "v0.1.0"},
		{"tagged go install", "dev", "v0.1.0", "v0.1.0"},
		{"commit go install", "dev", "v0.0.0-20261021141101-5a1f478486d9", "v0.0.0-20261021141101-5a1f478486d9"},
		{"linked version wins", "v0.2.0", "v0.1.0", "v0.2.0"},
		{"development build", "dev", "(devel)", "dev"},
		{"missing metadata", "dev", "", "dev"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			info := debug.BuildInfo{Main: debug.Module{Version: scenario.module}}
			if actual := resolveVersion(scenario.linked, &info); actual != scenario.expected {
				t.Fatalf("version = %q, want %q", actual, scenario.expected)
			}
		})
	}
}

func TestUnknownArgument(t *testing.T) {
	var output bytes.Buffer

	if err := run([]string{"--unknown"}, &output); err == nil {
		t.Fatal("unsupported option was accepted")
	}
}
