package monitor

import (
	"crypto/sha256"
	"fmt"
	"sort"
	"strings"
	"time"

	"git.pegasko.art/catsona/gstatus/internal/model"
)

func aggregateCI(pr model.PR) (status, fingerprint, summary string) {
	if !pr.Complete {
		return "unknown", "", ""
	}

	runs := map[string]model.Run{}
	for _, run := range pr.Runs {
		if run.HeadSHA != pr.HeadSHA && (pr.MergeSHA == "" || run.HeadSHA != pr.MergeSHA) {
			continue
		}

		key := fmt.Sprintf("%d:%s:%s", run.WorkflowID, run.Event, run.HeadSHA)
		if run.ID >= runs[key].ID {
			runs[key] = run
		}
	}

	activeIDs := map[int64]bool{}
	var parts, failures []string
	pending, running, failed, cancelled, succeeded, unknown := false, false, false, false, false, false
	observe := func(state, conclusion, name string) {
		switch strings.ToLower(state) {
		case "completed":
			switch strings.ToLower(conclusion) {
			case "success":
				succeeded = true
			case "failure", "error", "timed_out", "startup_failure", "action_required":
				failed = true
				failures = append(failures, name)
			case "cancelled", "stale":
				cancelled = true
			case "neutral", "skipped":
			default:
				unknown = true
			}
		case "in_progress":
			running = true
		default:
			pending = true
		}
	}

	for _, run := range runs {
		activeIDs[run.ID] = true
		parts = append(parts, fmt.Sprintf("run:%d:%d:%s:%s", run.ID, run.Attempt, run.State, run.Conclusion))
		observe(run.State, run.Conclusion, run.Name)
	}

	checks := map[string]model.Check{}
	for _, check := range pr.Checks {
		if check.Revision != pr.HeadSHA && (pr.MergeSHA == "" || check.Revision != pr.MergeSHA) {
			continue
		}

		if check.RunID != 0 && !activeIDs[check.RunID] {
			continue
		}

		key := check.Revision + ":" + check.App + ":" + check.Name
		previous, exists := checks[key]
		if !exists || !checkTime(check).Before(checkTime(previous)) {
			checks[key] = check
		}
	}

	for _, check := range checks {
		parts = append(parts, fmt.Sprintf("check:%s:%s:%s:%s", check.ID, check.State, check.Conclusion, checkTime(check).Format(time.RFC3339Nano)))
		observe(check.State, check.Conclusion, check.Name)
	}

	if len(parts) == 0 {
		return "none", "", ""
	}

	sort.Strings(parts)
	fingerprint = fmt.Sprintf("%x", sha256.Sum256([]byte(pr.HeadSHA+":"+pr.MergeSHA+":"+strings.Join(parts, "|"))))

	if pending || running {
		if failed {
			return "failing", fingerprint, ""
		}

		if running {
			return "running", fingerprint, ""
		}

		return "pending", fingerprint, ""
	}

	if unknown {
		return "unknown", fingerprint, ""
	}

	if cancelled {
		return "cancelled", fingerprint, ""
	}

	if failed {
		sort.Strings(failures)
		return "failure", fingerprint, "CI failed: " + strings.Join(unique(failures), ", ")
	}

	if succeeded {
		return "success", fingerprint, "CI passed"
	}

	return "skipped", fingerprint, ""
}

func unique(values []string) []string {
	var result []string
	for _, value := range values {
		if len(result) == 0 || result[len(result)-1] != value {
			result = append(result, value)
		}
	}

	return result
}

func checkTime(check model.Check) time.Time {
	result := check.CreatedAt
	for _, candidate := range []time.Time{check.StartedAt, check.CompletedAt} {
		if candidate.After(result) {
			result = candidate
		}
	}

	return result
}
