package monitor

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"sync"
	"time"

	"git.pegasko.art/catsona/gstatus/internal/config"
	"git.pegasko.art/catsona/gstatus/internal/github"
	"git.pegasko.art/catsona/gstatus/internal/model"
	"git.pegasko.art/catsona/gstatus/internal/state"
)

// Source binds every data or CI operation to an explicitly captured credential.
type Source interface {
	Credentials(context.Context) (string, error)
	Account(context.Context, string) (model.Account, error)
	List(context.Context, string, string, []string, string) ([]model.PR, error)
	Detail(context.Context, string, model.PR) (model.PR, error)
	Rerun(context.Context, string, model.PR, bool) (github.Result, error)
}

// Snapshot is an immutable view of the current account and loading progress.
type Snapshot struct {
	Account    model.Account
	Records    []model.Record
	Generation uint64
	UpdatedAt  time.Time
	Loading    bool
	Loaded     int
	Total      int
	Error      string
}

// Engine isolates account generations and commits acknowledged state with its events.
type Engine struct {
	source        Source
	store         *state.Store
	mu            sync.Mutex
	accountMu     sync.Mutex
	refreshMu     sync.Mutex
	cfg           config.Config
	account       model.Account
	token         string
	data          model.AccountState
	generation    uint64
	sessionCtx    context.Context
	cancelSession context.CancelFunc
	cancelFetch   context.CancelFunc
	updatedAt     time.Time
	lastAttempt   time.Time
	nextAllowed   time.Time
	loading       bool
	loaded        int
	total         int
	preview       map[string]model.PR
	lastError     error
	authError     error
	updates       chan Snapshot
	requests      chan struct{}
}

// New creates an idle monitor; Run starts independent account and PR polling loops.
func New(source Source, store *state.Store, cfg config.Config) *Engine {
	return &Engine{
		source: source, store: store, cfg: cfg,
		data:    model.AccountState{Records: map[string]model.Record{}},
		updates: make(chan Snapshot, 1), requests: make(chan struct{}, 1),
		preview: map[string]model.PR{}, sessionCtx: context.Background(),
	}
}

// Updates coalesces snapshots so a slow renderer cannot block background work.
func (e *Engine) Updates() <-chan Snapshot {
	return e.updates
}

// Snapshot returns owned record maps that callers may safely sort and inspect.
func (e *Engine) Snapshot() Snapshot {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.snapshotLocked()
}

func (e *Engine) snapshotLocked() Snapshot {
	result := Snapshot{Account: e.account, Generation: e.generation, UpdatedAt: e.updatedAt, Loading: e.loading, Loaded: e.loaded, Total: e.total}
	for _, record := range e.data.Records {
		if !inScope(record.PR.Repository, e.cfg.Repositories) {
			continue
		}

		record.Unread = maps.Clone(record.Unread)
		record.Pending = slices.Clone(record.Pending)
		result.Records = append(result.Records, record)
	}

	for id, pr := range e.preview {
		if _, exists := e.data.Records[id]; !exists {
			result.Records = append(result.Records, model.Record{PR: pr})
		}
	}

	if e.lastError != nil {
		result.Error = e.lastError.Error()
	}
	if e.authError != nil {
		result.Error = e.authError.Error()
	}

	return result
}

func (e *Engine) publishLocked() {
	snapshot := e.snapshotLocked()
	select {
	case e.updates <- snapshot:
	default:
		select {
		case <-e.updates:
		default:
		}
		select {
		case e.updates <- snapshot:
		default:
		}
	}
}

// RequestRefresh coalesces repeated manual requests without blocking input.
func (e *Engine) RequestRefresh() {
	select {
	case e.requests <- struct{}{}:
	default:
	}
}

// SetConfig replaces validated settings and cancels work for an outdated scope.
func (e *Engine) SetConfig(cfg config.Config) {
	e.mu.Lock()
	changed := !slices.Equal(e.cfg.Repositories, cfg.Repositories) || e.cfg.View.Filter != cfg.View.Filter
	e.cfg = cfg
	if changed && e.cancelFetch != nil {
		e.cancelFetch()
	}
	e.publishLocked()
	e.mu.Unlock()

	if changed {
		e.RequestRefresh()
	}
}

// Session returns delivery settings and a context cancelled when the account changes.
func (e *Engine) Session() (model.Account, context.Context, config.Config) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.authError != nil {
		return model.Account{}, e.sessionCtx, e.cfg
	}

	return e.account, e.sessionCtx, e.cfg
}

// CheckAccount detects effective credentials independently of PR refresh settings.
func (e *Engine) CheckAccount(ctx context.Context) error {
	e.accountMu.Lock()
	defer e.accountMu.Unlock()

	token, err := e.source.Credentials(ctx)
	if err != nil {
		e.mu.Lock()
		e.authError = err
		e.publishLocked()
		e.mu.Unlock()
		return err
	}

	e.mu.Lock()
	if token == e.token && e.account.ID != "" {
		changed := e.authError != nil
		e.authError = nil
		if changed {
			e.publishLocked()
		}
		e.mu.Unlock()
		return nil
	}

	if e.cancelSession != nil {
		e.cancelSession()
	}
	if e.cancelFetch != nil {
		e.cancelFetch()
	}
	e.generation++
	generation := e.generation
	e.account = model.Account{}
	e.loaded, e.total = 0, 0
	e.data = model.AccountState{Records: map[string]model.Record{}}
	e.preview = map[string]model.PR{}
	e.token = token
	e.updatedAt = time.Time{}
	e.lastError, e.authError = nil, nil
	e.loading = true
	e.sessionCtx, e.cancelSession = context.WithCancel(ctx)
	sessionCtx := e.sessionCtx
	e.publishLocked()
	e.mu.Unlock()

	account, err := e.source.Account(sessionCtx, token)
	var data model.AccountState
	if err == nil {
		data, err = e.store.Load(account)
	}

	e.mu.Lock()
	defer e.mu.Unlock()
	if generation != e.generation {
		return context.Canceled
	}

	e.loading = false
	if err != nil {
		e.authError = err
		e.publishLocked()
		return err
	}

	e.account, e.data = account, data
	e.lastAttempt, e.nextAllowed = time.Time{}, time.Time{}
	e.publishLocked()
	e.RequestRefresh()
	return nil
}

// Refresh loads a complete source generation while retaining good data on errors.
func (e *Engine) Refresh(ctx context.Context) error {
	e.refreshMu.Lock()
	defer e.refreshMu.Unlock()
	e.mu.Lock()
	if e.account.ID == "" || e.authError != nil {
		e.mu.Unlock()
		return fmt.Errorf("no verified active GitHub account")
	}

	if time.Now().Before(e.nextAllowed) {
		err := fmt.Errorf("refresh paused until %s", e.nextAllowed.Local().Format(time.TimeOnly))
		e.mu.Unlock()
		return err
	}

	account, token, generation, cfg := e.account, e.token, e.generation, e.cfg
	fetchCtx, cancel := context.WithCancel(e.sessionCtx)
	stopParent := context.AfterFunc(ctx, cancel)
	defer stopParent()
	defer cancel()
	e.cancelFetch = cancel
	e.loading, e.loaded, e.total = true, 0, 0
	e.lastAttempt = time.Now()
	e.publishLocked()
	e.mu.Unlock()

	prs, err := e.source.List(fetchCtx, token, account.Login, cfg.Repositories, "open")
	if err == nil && cfg.View.Filter != "open" && cfg.View.Filter != "draft" {
		var history []model.PR
		history, err = e.source.List(fetchCtx, token, account.Login, cfg.Repositories, cfg.View.Filter)
		prs = append(prs, history...)
	}

	if err == nil {
		e.mu.Lock()
		if generation != e.generation || fetchCtx.Err() != nil {
			e.mu.Unlock()
			return context.Canceled
		}
		byID := map[string]model.PR{}
		for _, pr := range prs {
			byID[pr.ID] = pr
		}
		for id, record := range e.data.Records {
			if record.PR.State == "open" && inScope(record.PR.Repository, cfg.Repositories) {
				if _, exists := byID[id]; !exists {
					byID[id] = record.PR
				}
			}
		}
		prs = nil
		for _, pr := range byID {
			prs = append(prs, pr)
		}
		if generation == e.generation && fetchCtx.Err() == nil {
			e.total = len(prs)
			e.preview = byID
			e.publishLocked()
		}
		e.mu.Unlock()
	}

	var loaded []model.PR
	if err == nil {
		type result struct {
			pr  model.PR
			err error
		}
		jobs := make(chan model.PR, len(prs))
		results := make(chan result, len(prs))
		for _, pr := range prs {
			jobs <- pr
		}
		close(jobs)

		var workers sync.WaitGroup
		for i := 0; i < min(4, len(prs)); i++ {
			workers.Go(func() {
				for pr := range jobs {
					if fetchCtx.Err() != nil {
						return
					}

					value, detailErr := e.source.Detail(fetchCtx, token, pr)
					results <- result{pr: value, err: detailErr}
				}
			})
		}
		go func() { workers.Wait(); close(results) }()

		for result := range results {
			err = errors.Join(err, result.err)
			if result.err == nil {
				loaded = append(loaded, result.pr)
			}

			e.mu.Lock()
			if generation == e.generation {
				e.loaded++
				e.publishLocked()
			}
			e.mu.Unlock()
		}
	}

	e.mu.Lock()
	defer e.mu.Unlock()
	if generation != e.generation || fetchCtx.Err() != nil {
		if generation == e.generation {
			e.loading = false
			e.preview = map[string]model.PR{}
			e.publishLocked()
		}
		return context.Canceled
	}

	e.loading = false
	e.preview = map[string]model.PR{}
	data, deliveries := Reconcile(account, e.data, loaded, cfg.Repositories, time.Now(), err == nil, e.cfg.Notifications)
	if saveErr := e.store.Save(account, data, deliveries); saveErr != nil {
		err = errors.Join(err, saveErr)
	} else {
		e.data = data
	}

	e.lastError = err
	if err == nil {
		e.updatedAt = time.Now()
	} else {
		var limited *github.RateLimitError
		if errors.As(err, &limited) {
			e.nextAllowed = limited.Until
		} else {
			e.nextAllowed = time.Now().Add(time.Duration(cfg.Refresh.Interval))
		}
	}
	e.publishLocked()
	return err
}

// MarkRead acknowledges only events already present in the displayed snapshot.
func (e *Engine) MarkRead(id string, through uint64) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	record, exists := e.data.Records[id]
	if !exists {
		return fmt.Errorf("PR is not loaded yet")
	}

	data := e.data
	data.Records = maps.Clone(e.data.Records)
	data.Records[id] = Acknowledge(record, through)
	if err := e.store.Save(e.account, data, nil); err != nil {
		return err
	}

	e.data = data
	e.publishLocked()
	return nil
}

// MarkUnread preserves the manual mark until a later explicit acknowledgement.
func (e *Engine) MarkUnread(id string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	record, exists := e.data.Records[id]
	if !exists {
		return fmt.Errorf("PR is not loaded yet")
	}

	record.Sequence++
	record.ManualSequence = record.Sequence
	record.ManualUnread = true
	data := e.data
	data.Records = maps.Clone(e.data.Records)
	data.Records[id] = record
	if err := e.store.Save(e.account, data, nil); err != nil {
		return err
	}

	e.data = data
	e.publishLocked()
	return nil
}

// Rerun rechecks active credentials before sending any CI mutation.
func (e *Engine) Rerun(ctx context.Context, id string, failedOnly bool) (github.Result, error) {
	token, err := e.source.Credentials(ctx)
	if err != nil {
		return github.Result{}, err
	}

	e.mu.Lock()
	if token != e.token || e.authError != nil {
		e.mu.Unlock()
		_ = e.CheckAccount(ctx)
		return github.Result{}, fmt.Errorf("active account changed; select the PR again")
	}

	record, exists := e.data.Records[id]
	sessionCtx, generation := e.sessionCtx, e.generation
	e.mu.Unlock()
	if !exists || !record.PR.Complete {
		return github.Result{}, fmt.Errorf("PR is not fully loaded")
	}

	requestCtx, cancel := context.WithCancel(sessionCtx)
	defer cancel()
	stop := context.AfterFunc(ctx, cancel)
	defer stop()
	result, err := e.source.Rerun(requestCtx, token, record.PR, failedOnly)
	e.mu.Lock()
	changed := generation != e.generation
	e.mu.Unlock()
	if changed {
		return result, fmt.Errorf("account changed during CI operation")
	}

	e.RequestRefresh()
	return result, err
}

// Run keeps account detection active even when PR polling is disabled.
func (e *Engine) Run(ctx context.Context) {
	var loops sync.WaitGroup
	loops.Go(func() {
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		for {
			_ = e.CheckAccount(ctx)
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	})
	loops.Go(func() {
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()
		for {
			explicit := false
			select {
			case <-ctx.Done():
				return
			case <-e.requests:
				explicit = true
			case <-ticker.C:
			}

			e.mu.Lock()
			due := e.cfg.Refresh.Enabled && time.Since(e.lastAttempt) >= time.Duration(e.cfg.Refresh.Interval)
			e.mu.Unlock()
			if explicit || due {
				_ = e.Refresh(ctx)
			}
		}
	})
	<-ctx.Done()
	e.mu.Lock()
	if e.cancelSession != nil {
		e.cancelSession()
	}
	e.mu.Unlock()
	loops.Wait()
}
