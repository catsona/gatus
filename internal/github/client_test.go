package github

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func fixtureClient(t *testing.T, script string) *Client {
	t.Helper()
	path := filepath.Join(t.TempDir(), "gh")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nset -eu\n"+script), 0700); err != nil {
		t.Fatal(err)
	}

	return &Client{Executable: path}
}

func TestCommandsPinCredentialsAndRemoveDebugEnvironment(t *testing.T) {
	t.Setenv("GH_TOKEN", "other-user")
	t.Setenv("GITHUB_TOKEN", "another-user")
	t.Setenv("GH_DEBUG", "api")
	client := fixtureClient(t, `printf '%s|%s|%s' "$GH_TOKEN" "${GITHUB_TOKEN-unset}" "${GH_DEBUG-unset}"`)

	output, err := client.output(context.Background(), "captured-account", "api", "user")
	if err != nil || string(output) != "captured-account|unset|unset" {
		t.Fatalf("credentials were not isolated: %q %v", output, err)
	}
}

func TestCommandErrorsRedactTokens(t *testing.T) {
	client := fixtureClient(t, `printf '%s' "$GH_TOKEN" >&2; exit 1`)
	_, err := client.output(context.Background(), "secret-token", "api", "user")
	if err == nil || strings.Contains(err.Error(), "secret-token") {
		t.Fatalf("unsafe error: %v", err)
	}
}

func TestSearchPaginatesAllResults(t *testing.T) {
	items := make([]map[string]any, 100)
	for i := range items {
		items[i] = map[string]any{"node_id": strings.Repeat("P", i+1), "number": i + 1, "title": "PR", "repository_url": "https://api.github.com/repos/acme/app"}
	}

	first, _ := json.Marshal(map[string]any{"total_count": 101, "items": items})
	second := `{"total_count":101,"items":[{"node_id":"last","number":101,"title":"Last PR","repository_url":"https://api.github.com/repos/acme/app"}]}`
	client := fixtureClient(t, "printf 'HTTP/1.1 200 OK\\r\\n\\r\\n'\ncase \"$*\" in\n *page=2*) printf '%s' '"+second+"' ;;\n *) printf '%s' '"+string(first)+"' ;;\nesac\n")

	prs, err := client.List(context.Background(), "account-token", "alice", nil, "open")
	if err != nil || len(prs) != 101 || prs[100].Number != 101 || prs[100].Repository != "acme/app" {
		t.Fatalf("paginated PRs: count=%d error=%v", len(prs), err)
	}
}

func TestConditionalRequestsReuseOnlyMatchingAccountCache(t *testing.T) {
	client := fixtureClient(t, `case "$*" in
  *If-None-Match*) printf 'HTTP/1.1 304 Not Modified\r\n\r\n' ;;
  *) printf 'HTTP/1.1 200 OK\r\nETag: cached\r\n\r\n{"login":"%s"}' "$GH_TOKEN" ;;
esac`)

	var value struct{ Login string }
	for _, token := range []string{"alice", "alice", "bob"} {
		if err := client.api(context.Background(), token, "user", &value); err != nil || value.Login != token {
			t.Fatalf("cached identity for %s = %s, %v", token, value.Login, err)
		}
	}
}

func TestConcurrentRequestsKeepAccountCachesSeparate(t *testing.T) {
	client := fixtureClient(t, `printf 'HTTP/1.1 200 OK\r\nETag: cached\r\n\r\n{"login":"%s"}' "$GH_TOKEN"`)
	var workers sync.WaitGroup
	for i := 0; i < 16; i++ {
		workers.Go(func() {
			var value struct{ Login string }
			if err := client.api(context.Background(), "alice", "user", &value); err != nil || value.Login != "alice" {
				t.Errorf("concurrent identity: %+v %v", value, err)
			}
		})
	}
	workers.Wait()
}

func TestIncompleteSearchAndMalformedResponsesFail(t *testing.T) {
	for _, body := range []string{`{"incomplete_results":true}`, `not-json`} {
		client := fixtureClient(t, "printf 'HTTP/1.1 200 OK\\r\\n\\r\\n%s' '"+body+"'")
		if _, err := client.List(context.Background(), "token", "alice", nil, "open"); err == nil {
			t.Fatalf("accepted incomplete response: %s", body)
		}
	}
}

func TestCredentialsAreNotBlockedByActiveNetworkRequests(t *testing.T) {
	directory := t.TempDir()
	client := fixtureClient(t, "if [ \"$1\" = auth ]; then printf credential; exit 0; fi\nprintf ready > '"+directory+"/'\"$1\"\nexec /bin/sleep 5\n")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var workers sync.WaitGroup
	for i := 0; i < 4; i++ {
		name := fmt.Sprintf("request-%d", i)
		workers.Go(func() { _, _ = client.output(ctx, "token", name) })
	}

	deadline := time.Now().Add(3 * time.Second)
	for {
		files, _ := filepath.Glob(filepath.Join(directory, "request-*"))
		if len(files) == 4 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("network fixtures did not start")
		}
		time.Sleep(10 * time.Millisecond)
	}

	token, err := client.Credentials(context.Background())
	cancel()
	workers.Wait()
	if err != nil || token != "credential" {
		t.Fatalf("account detection waited behind PR fetches: %q %v", token, err)
	}
}
