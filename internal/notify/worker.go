package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/catsona/gatus/internal/config"
	"github.com/catsona/gatus/internal/model"
	"github.com/catsona/gatus/internal/state"
)

// Worker delivers one due event per tick using the current configuration.
type Worker struct {
	Store          *state.Store
	Client         *http.Client
	pruneSignature string
}

// Tick cancels disabled types globally and processes the active queue; calls must be serial.
func (w *Worker) Tick(ctx context.Context, account model.Account, cfg config.Notifications, now time.Time) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	types := cfg.Events
	if !cfg.Enabled {
		types = nil
	}
	signature := fmt.Sprintf("%t:%q", cfg.Enabled, types)
	if signature != w.pruneSignature {
		if err := w.Store.PruneDeliveries(types); err != nil {
			return err
		}
		w.pruneSignature = signature
	}

	if !cfg.Enabled || account.ID == "" {
		return nil
	}

	queue, err := w.Store.Pending(account)
	if err != nil {
		return err
	}

	var previous error
	for _, delivery := range queue {
		if delivery.Failed {
			previous = errors.Join(previous, errors.New(delivery.LastError))
			continue
		}

		if now.Before(delivery.NextAttempt) {
			continue
		}

		return errors.Join(previous, w.attempt(ctx, delivery, cfg, now))
	}

	return previous
}

func (w *Worker) attempt(ctx context.Context, delivery model.Delivery, cfg config.Notifications, now time.Time) error {
	text, err := Render(cfg.Template, delivery)
	if err != nil {
		delivery.Failed, delivery.LastError = true, "notification template: "+err.Error()
		return errors.Join(err, w.Store.PutDelivery(delivery))
	}

	body, err := json.Marshal(struct {
		Text string `json:"text"`
	}{Text: text})
	if err != nil {
		return err
	}

	requestCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(requestCtx, http.MethodPost, cfg.WebhookURL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")

	client := w.Client
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		}}
	}

	response, sendErr := client.Do(request)
	if ctx.Err() != nil {
		if response != nil {
			_ = response.Body.Close()
		}
		return ctx.Err()
	}

	delivery.Attempts++
	retry := true
	var retryAfter time.Time
	if sendErr == nil {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 64<<10))
		_ = response.Body.Close()
		if response.StatusCode >= 200 && response.StatusCode < 300 {
			return w.Store.RemoveDelivery(delivery.Account, delivery.ID)
		}

		sendErr = fmt.Errorf("webhook: HTTP %d", response.StatusCode)
		retry = response.StatusCode == 408 || response.StatusCode == 429 || response.StatusCode >= 500
		if seconds, err := strconv.Atoi(response.Header.Get("Retry-After")); err == nil {
			retryAfter = now.Add(time.Duration(seconds) * time.Second)
		} else if value, err := http.ParseTime(response.Header.Get("Retry-After")); err == nil {
			retryAfter = value
		}
	} else {
		var requestError *url.Error
		if errors.As(sendErr, &requestError) {
			sendErr = fmt.Errorf("webhook: %v", requestError.Err)
		}
	}

	delivery.LastError = sendErr.Error()
	delivery.Failed = !retry || delivery.Attempts >= 5
	if !delivery.Failed {
		delays := []time.Duration{2 * time.Second, 5 * time.Second, 15 * time.Second, 60 * time.Second}
		delivery.NextAttempt = now.Add(delays[delivery.Attempts-1])
		if retryAfter.After(delivery.NextAttempt) {
			delivery.NextAttempt = retryAfter
		}
	}

	return errors.Join(sendErr, w.Store.PutDelivery(delivery))
}

// Run pauses automatically with the account session and reports delivery errors to the UI.
func (w *Worker) Run(ctx context.Context, current func() (model.Account, context.Context, config.Config), report func(error)) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			account, session, cfg := current()
			err := w.Tick(session, account, cfg.Notifications, time.Now())
			if !errors.Is(err, context.Canceled) && report != nil {
				report(err)
			}
		}
	}
}
