package monitor

import "context"

// RefreshState describes live fetching activity, independently of CI and read state.
type RefreshState string

const (
	RefreshIdle    RefreshState = ""
	RefreshQueued  RefreshState = "queued"
	RefreshRunning RefreshState = "refreshing"
)

type refreshJob struct {
	generation uint64
	states     map[string]RefreshState
}

func (e *Engine) newRefreshJobLocked(ids ...string) *refreshJob {
	job := &refreshJob{generation: e.generation, states: map[string]RefreshState{}}
	if len(ids) == 0 {
		for id, record := range e.data.Records {
			if inScope(record.PR.Repository, e.cfg.Repositories) && (record.PR.State == "open" || e.cfg.View.Filter == "all" || record.PR.State == e.cfg.View.Filter) {
				job.states[id] = RefreshQueued
			}
		}
	} else {
		for _, id := range ids {
			job.states[id] = RefreshQueued
		}
	}
	e.refreshJobs[job] = struct{}{}
	return job
}

func (e *Engine) requestRefreshLocked() {
	if len(e.requests) != 0 {
		return
	}
	job := e.newRefreshJobLocked()
	e.requests <- job
	e.publishLocked()
}

func (e *Engine) finishRefreshJob(job *refreshJob) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, exists := e.refreshJobs[job]; exists {
		delete(e.refreshJobs, job)
		e.publishLocked()
	}
}

func (e *Engine) setRefreshState(job *refreshJob, id string, state RefreshState) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	if job.generation != e.generation {
		return false
	}
	if _, exists := e.refreshJobs[job]; !exists {
		return false
	}
	if state == RefreshIdle {
		delete(job.states, id)
	} else {
		job.states[id] = state
	}
	e.publishLocked()
	return true
}

func (e *Engine) observeRefreshContext(ctx context.Context, job *refreshJob) func() {
	stop := context.AfterFunc(ctx, func() { e.finishRefreshJob(job) })
	return func() {
		stop()
		e.finishRefreshJob(job)
	}
}
