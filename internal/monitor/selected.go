package monitor

import (
	"context"
	"errors"
	"fmt"
	"time"

	"git.pegasko.art/catsona/gstatus/internal/github"
	"git.pegasko.art/catsona/gstatus/internal/model"
)

// RefreshPR fetches one known PR and reconciles its events without rediscovering the list.
func (e *Engine) RefreshPR(ctx context.Context, id string) error {
	e.refreshMu.Lock()
	defer e.refreshMu.Unlock()
	token, err := e.source.Credentials(ctx)
	if err != nil {
		return err
	}

	e.mu.Lock()
	if token != e.token || e.authError != nil {
		e.mu.Unlock()
		_ = e.CheckAccount(ctx)
		return fmt.Errorf("active account changed; select the PR again")
	}
	record, exists := e.data.Records[id]
	if !exists || !inScope(record.PR.Repository, e.cfg.Repositories) {
		e.mu.Unlock()
		return fmt.Errorf("PR is not loaded in the current scope")
	}
	if time.Now().Before(e.nextAllowed) {
		until := e.nextAllowed
		e.mu.Unlock()
		return fmt.Errorf("refresh paused until %s", until.Local().Format(time.TimeOnly))
	}

	account, generation := e.account, e.generation
	fetchCtx, cancel := context.WithCancel(e.sessionCtx)
	stop := context.AfterFunc(ctx, cancel)
	defer stop()
	defer cancel()
	e.cancelFetch = cancel
	e.mu.Unlock()

	pr, err := e.source.Detail(fetchCtx, token, record.PR)
	e.mu.Lock()
	defer e.mu.Unlock()
	if generation != e.generation || fetchCtx.Err() != nil {
		return context.Canceled
	}
	if err == nil && (!pr.Complete || pr.ID != id) {
		err = fmt.Errorf("GitHub returned an incomplete or different PR")
	}
	if err == nil {
		data, deliveries := Reconcile(account, e.data, []model.PR{pr}, e.data.Scope, time.Now(), false, e.cfg.Notifications)
		if saveErr := e.store.Save(account, data, deliveries); saveErr != nil {
			err = saveErr
		} else {
			e.data = data
		}
	}
	if err != nil {
		var limited *github.RateLimitError
		if errors.As(err, &limited) {
			e.nextAllowed = limited.Until
		}
	}
	e.lastError = err
	e.publishLocked()
	return err
}
