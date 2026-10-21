package config

import (
	"context"
	"crypto/sha256"
	"os"
	"path/filepath"
	"time"

	"github.com/fsnotify/fsnotify"
)

// Change carries a validated replacement or an error that leaves current settings intact.
type Change struct {
	Config Config
	Hash   [32]byte
	Err    error
}

// Watch handles atomic replacements and falls back to polling if OS notifications fail.
func Watch(ctx context.Context, path string) <-chan Change {
	changes := make(chan Change, 1)
	go func() {
		defer close(changes)
		watcher, err := fsnotify.NewWatcher()
		var events <-chan fsnotify.Event
		var failures <-chan error
		if err == nil {
			defer watcher.Close()
			if watcher.Add(filepath.Dir(path)) == nil {
				events, failures = watcher.Events, watcher.Errors
			}
		}

		var previous [32]byte
		previousError := "unread"
		check := func() {
			data, readErr := os.ReadFile(path)
			hash := sha256.Sum256(data)
			var cfg Config
			if readErr == nil {
				cfg, readErr = Parse(data)
			}

			message := ""
			if readErr != nil {
				message = readErr.Error()
			}
			if hash == previous && message == previousError {
				return
			}
			previous, previousError = hash, message
			change := Change{Config: cfg, Hash: hash, Err: readErr}
			select {
			case changes <- change:
			default:
				select {
				case <-changes:
				default:
				}
				select {
				case changes <- change:
				default:
				}
			}
		}

		fallback := time.NewTicker(2 * time.Second)
		defer fallback.Stop()
		debounce := time.NewTimer(time.Hour)
		debounce.Stop()
		defer debounce.Stop()
		check()
		for {
			select {
			case <-ctx.Done():
				return
			case _, ok := <-events:
				if !ok {
					events = nil
					continue
				}
				debounce.Reset(200 * time.Millisecond)
			case _, ok := <-failures:
				if !ok {
					failures = nil
				}
			case <-debounce.C:
				check()
			case <-fallback.C:
				check()
			}
		}
	}()

	return changes
}
