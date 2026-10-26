package herdr

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestClientFocusesWorkspaceWithMatchingPRToken(t *testing.T) {
	directory, executable, log := fakeHerdr(t)
	t.Setenv("HERDR_SCENARIO", "existing")

	client := Client{Executable: executable}
	err := client.Open(context.Background(), Request{
		Root:  filepath.Join(directory, "spaces"),
		Label: "PR #42 - owner/repo",
		PRKey: "https://github.com/owner/repo/pull/42",
	})
	if err != nil {
		t.Fatal(err)
	}

	lines := readLines(t, log)
	want := []string{"workspace list", "workspace focus w1"}
	if strings.Join(lines, "\n") != strings.Join(want, "\n") {
		t.Fatalf("commands = %q, want %q", lines, want)
	}
}

func TestClientCreatesAndTagsWorkspaceAfterMissingToken(t *testing.T) {
	directory, executable, log := fakeHerdr(t)
	t.Setenv("HERDR_SCENARIO", "create")

	client := Client{Executable: executable}
	err := client.Open(context.Background(), Request{
		Root:  filepath.Join(directory, "spaces"),
		Label: "PR #42 - owner/repo",
		PRKey: "https://github.com/owner/repo/pull/42",
	})
	if err != nil {
		t.Fatal(err)
	}

	lines := readLines(t, log)
	want := []string{
		"workspace list",
		"workspace create --cwd " + filepath.Join(directory, "spaces") + " --label PR #42 - owner/repo --focus",
		"workspace report-metadata w2 --source gstatus --token gstatus_pr=https://github.com/owner/repo/pull/42",
	}
	if strings.Join(lines, "\n") != strings.Join(want, "\n") {
		t.Fatalf("commands = %q, want %q", lines, want)
	}
}

func TestClientRecreatesWorkspaceWhenFocusReportsMissing(t *testing.T) {
	directory, executable, log := fakeHerdr(t)
	t.Setenv("HERDR_SCENARIO", "focus_missing")

	client := Client{Executable: executable}
	err := client.Open(context.Background(), Request{
		Root:  filepath.Join(directory, "spaces"),
		Label: "PR #42 - owner/repo",
		PRKey: "https://github.com/owner/repo/pull/42",
	})
	if err != nil {
		t.Fatal(err)
	}

	lines := readLines(t, log)
	if len(lines) != 4 || lines[1] != "workspace focus w1" || !strings.HasPrefix(lines[2], "workspace create --cwd ") {
		t.Fatalf("commands = %q", lines)
	}
}

func TestClientExpandsHomeAndRejectsMalformedResponses(t *testing.T) {
	directory, executable, log := fakeHerdr(t)
	home := filepath.Join(directory, "home")
	if err := os.MkdirAll(home, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("HERDR_SCENARIO", "create")

	client := Client{Executable: executable}
	if err := client.Open(context.Background(), Request{Root: "~/spaces", Label: "PR #42", PRKey: "pr-42"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(readLines(t, log), "\n"), "--cwd "+filepath.Join(home, "spaces")) {
		t.Fatalf("home was not expanded: %q", readLines(t, log))
	}

	t.Setenv("HERDR_SCENARIO", "malformed")
	if err := client.Open(context.Background(), Request{Root: home, Label: "PR #42", PRKey: "pr-42"}); err == nil {
		t.Fatal("malformed workspace response was accepted")
	}

	t.Setenv("HERDR_SCENARIO", "failure")
	if err := client.Open(context.Background(), Request{Root: home, Label: "PR #42", PRKey: "pr-42"}); err == nil {
		t.Fatal("failed Herdr command was accepted")
	}
}

func fakeHerdr(t *testing.T) (string, string, string) {
	t.Helper()
	directory := t.TempDir()
	log := filepath.Join(directory, "commands")
	executable := filepath.Join(directory, "herdr")
	script := `#!/bin/sh
printf '%s\n' "$*" >> "$HERDR_LOG"
case "$HERDR_SCENARIO:$2" in
existing:list|focus_missing:list|create:list)
  if [ "$HERDR_SCENARIO" = "existing" ] || [ "$HERDR_SCENARIO" = "focus_missing" ]; then
    printf '%s' '{"result":{"workspaces":[{"workspace_id":"w1","tokens":{"gstatus_pr":"https://github.com/owner/repo/pull/42"}}]}}'
  else
    printf '%s' '{"result":{"workspaces":[]}}'
  fi
  ;;
focus_missing:focus)
  printf '%s' '{"error":{"code":"workspace_not_found","message":"closed"}}' >&2
  exit 1
  ;;
create:create)
  printf '%s' '{"result":{"workspace":{"workspace_id":"w2"}}}'
  ;;
focus_missing:create)
  printf '%s' '{"result":{"workspace":{"workspace_id":"w3"}}}'
  ;;
existing:focus|create:report-metadata|focus_missing:report-metadata)
  printf '%s' '{"result":{}}'
  ;;
malformed:list)
  printf '%s' 'not json'
  ;;
failure:list)
  printf '%s' 'herdr unavailable' >&2
  exit 1
  ;;
esac
`
	if err := os.WriteFile(executable, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HERDR_LOG", log)
	return directory, executable, log
}

func readLines(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSpace(string(data)), "\n")
}
