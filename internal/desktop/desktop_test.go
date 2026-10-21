package desktop

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestBrowserReceivesURLAsAnArgument(t *testing.T) {
	directory := t.TempDir()
	output := filepath.Join(directory, "opened")
	browser := filepath.Join(directory, "browser")
	if err := os.WriteFile(browser, []byte("#!/bin/sh\nprintf '%s' \"$1\" > '"+output+"'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GH_BROWSER", browser)
	url := "https://github.com/acme/app/pull/7"
	if err := (Native{}).Open(context.Background(), url); err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(output)
	if err != nil || string(got) != url {
		t.Fatalf("browser received %q: %v", got, err)
	}
}

func TestClipboardPreservesTheURL(t *testing.T) {
	directory := t.TempDir()
	output := filepath.Join(directory, "copied")
	for _, name := range []string{"pbcopy", "wl-copy"} {
		if err := os.WriteFile(filepath.Join(directory, name), []byte("#!/bin/sh\n/bin/cat > '"+output+"'\n"), 0700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", directory)
	url := "https://github.com/acme/app/pull/7"
	if err := (Native{}).Copy(context.Background(), url); err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(output)
	if err != nil || string(got) != url {
		t.Fatalf("clipboard received %q: %v", got, err)
	}
}
