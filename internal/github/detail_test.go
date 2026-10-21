package github

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"git.pegasko.art/catsona/gstatus/internal/model"
)

func detailFixture(t *testing.T, calls string) *Client {
	t.Helper()
	responses := map[string]string{
		"repos/acme/app/pulls/7":                  `{"node_id":"PR_7","number":7,"title":"Fix","state":"open","head":{"sha":"head","ref":"feature","repo":{"full_name":"alice/app"}},"merge_commit_sha":null}`,
		"repos/acme/app/pulls/7/reviews?":         `[{"id":1,"user":{"login":"alice"},"state":"COMMENTED","submitted_at":"2026-01-01T01:00:00Z"},{"id":2,"user":{"login":"bob"},"state":"APPROVED","submitted_at":"2026-01-01T02:00:00Z"}]`,
		"repos/acme/app/pulls/7/comments?":        `[{"id":10,"user":{"login":"alice"},"pull_request_review_id":1,"created_at":"2026-01-01T01:00:00Z"},{"id":11,"user":{"login":"bob"},"pull_request_review_id":2,"in_reply_to_id":10,"created_at":"2026-01-01T02:00:00Z"}]`,
		"repos/acme/app/issues/7/comments?":       `[{"id":10,"user":{"login":"charlie"},"created_at":"2026-01-01T03:00:00Z"}]`,
		"repos/acme/app/commits/head/check-runs?": `{"total_count":1,"check_runs":[{"id":55,"name":"tests","status":"completed","conclusion":"failure","details_url":"https://github.com/acme/app/actions/runs/41/job/55","app":{"slug":"github-actions"},"started_at":"2026-01-01T00:00:00Z","completed_at":"2026-01-01T00:10:00Z"}]}`,
		"repos/acme/app/commits/head/statuses?":   `[]`,
		"repos/acme/app/actions/runs/41":          `{"id":41,"workflow_id":5,"name":"Tests","run_attempt":2,"event":"pull_request","head_sha":"head","status":"completed","conclusion":"failure","pull_requests":[{"number":7,"head":{"sha":"head"}}]}`,
	}

	directory := t.TempDir()
	var keys []string
	for key := range responses {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool { return len(keys[i]) > len(keys[j]) })

	var script strings.Builder
	fmt.Fprintf(&script, "if [ \"$1\" = run ]; then printf '%%s\\n' \"$*\" >> '%s'; exit 0; fi\n", calls)
	script.WriteString("for endpoint do :; done\nprintf 'HTTP/1.1 200 OK\\r\\n\\r\\n'\ncase \"$endpoint\" in\n")
	for i, key := range keys {
		path := filepath.Join(directory, fmt.Sprintf("%d.json", i))
		if err := os.WriteFile(path, []byte(responses[key]), 0600); err != nil {
			t.Fatal(err)
		}

		pattern := "'" + key + "'"
		if strings.HasSuffix(key, "?") {
			pattern += "*"
		}

		fmt.Fprintf(&script, "%s) cat '%s' ;;\n", pattern, path)
	}
	script.WriteString("*) printf 'missing fixture: %s' \"$endpoint\" >&2; exit 1 ;;\nesac\n")

	return fixtureClient(t, script.String())
}

func TestDetailLoadsThreadsReviewsAndCurrentAttempt(t *testing.T) {
	client := detailFixture(t, filepath.Join(t.TempDir(), "calls"))
	pr, err := client.Detail(context.Background(), "token", model.PR{Repository: "acme/app", Number: 7})
	if err != nil {
		t.Fatal(err)
	}

	if !pr.Complete || pr.HeadSHA != "head" || pr.ReviewStatus != "approved" || len(pr.Runs) != 1 || pr.Runs[0].Attempt != 2 {
		t.Fatalf("incomplete revision: %+v", pr)
	}

	if len(pr.Comments) != 3 || pr.Comments[1].ThreadID != "review:10" || pr.Comments[2].ID != "issue:10" {
		t.Fatalf("thread identity or issue/review IDs lost: %+v", pr.Comments)
	}

	if len(pr.Checks) != 1 || pr.Checks[0].RunID != 41 || pr.LastMessage.Hour() != 3 {
		t.Fatalf("check or message timestamp lost: %+v", pr)
	}
}

func TestRerunFailedAndRejectChangedRevision(t *testing.T) {
	calls := filepath.Join(t.TempDir(), "calls")
	client := detailFixture(t, calls)
	selected := model.PR{Repository: "acme/app", Number: 7, HeadSHA: "head"}

	result, err := client.Rerun(context.Background(), "token", selected, true)
	if err != nil || result.Accepted != 1 {
		t.Fatalf("rerun: %+v %v", result, err)
	}

	selected.HeadSHA = "old"
	if _, err := client.Rerun(context.Background(), "token", selected, false); err == nil {
		t.Fatal("reran a revision that changed after selection")
	}

	data, err := os.ReadFile(calls)
	if err != nil || string(data) != "run rerun 41 --repo acme/app --failed\n" {
		t.Fatalf("incorrect rerun requests: %q %v", data, err)
	}
}
