package github

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"git.pegasko.art/catsona/gstatus/internal/model"
)

func TestDetailMapsBranchConflictsWithoutTreatingRulesAsConflicts(t *testing.T) {
	for _, scenario := range []struct{ name, mergeable, state, want string }{
		{"mergeable", "true", "clean", "ok"},
		{"conflict", "false", "dirty", "conflict"},
		{"rules blocked", "true", "blocked", "ok"},
		{"computing", "null", "unknown", "unknown"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			base := detailFixture(t, filepath.Join(t.TempDir(), "calls"))
			body := fmt.Sprintf(`{"node_id":"PR_7","number":7,"title":"Fix","state":"open","head":{"sha":"head","ref":"feature","repo":{"full_name":"alice/app"}},"mergeable":%s,"mergeable_state":%q}`, scenario.mergeable, scenario.state)
			client := fixtureClient(t, "for endpoint do :; done\ncase \"$endpoint\" in 'repos/acme/app/pulls/7') printf 'HTTP/1.1 200 OK\\r\\n\\r\\n%s' '"+body+"' ;; *) exec '"+base.Executable+"' \"$@\" ;; esac\n")
			pr, err := client.Detail(context.Background(), "token", model.PR{Repository: "acme/app", Number: 7})
			if err != nil || pr.BranchStatus != scenario.want || !pr.Complete {
				t.Fatalf("branch status=%q, want=%q, complete=%t, error=%v", pr.BranchStatus, scenario.want, pr.Complete, err)
			}
		})
	}
}

func TestDetailUsesBranchStatusFromFinalRevisionCheck(t *testing.T) {
	base := detailFixture(t, filepath.Join(t.TempDir(), "calls"))
	flag := filepath.Join(t.TempDir(), "requested")
	client := fixtureClient(t, "for endpoint do :; done\ncase \"$endpoint\" in 'repos/acme/app/pulls/7') value=null; if [ -f '"+flag+"' ]; then value=false; fi; touch '"+flag+"'; printf 'HTTP/1.1 200 OK\\r\\n\\r\\n{\"node_id\":\"PR_7\",\"number\":7,\"state\":\"open\",\"head\":{\"sha\":\"head\"},\"mergeable\":%s}' \"$value\" ;; *) exec '"+base.Executable+"' \"$@\" ;; esac\n")
	pr, err := client.Detail(context.Background(), "token", model.PR{Repository: "acme/app", Number: 7})
	if err != nil || pr.BranchStatus != "conflict" {
		t.Fatalf("final branch status=%q, error=%v", pr.BranchStatus, err)
	}
}

func TestDetailUsesStateFromFinalRevisionCheck(t *testing.T) {
	base := detailFixture(t, filepath.Join(t.TempDir(), "calls"))
	flag := filepath.Join(t.TempDir(), "requested")
	client := fixtureClient(t, "for endpoint do :; done\ncase \"$endpoint\" in 'repos/acme/app/pulls/7') state=open; merged=false; draft=true; if [ -f '"+flag+"' ]; then state=closed; merged=true; draft=false; fi; touch '"+flag+"'; printf 'HTTP/1.1 200 OK\\r\\n\\r\\n{\"node_id\":\"PR_7\",\"number\":7,\"state\":\"%s\",\"merged\":%s,\"draft\":%s,\"head\":{\"sha\":\"head\"}}' \"$state\" \"$merged\" \"$draft\" ;; *) exec '"+base.Executable+"' \"$@\" ;; esac\n")
	pr, err := client.Detail(context.Background(), "token", model.PR{Repository: "acme/app", Number: 7})
	if err != nil || pr.State != "merged" || pr.Draft {
		t.Fatalf("final PR state=%q draft=%t, error=%v", pr.State, pr.Draft, err)
	}
}
