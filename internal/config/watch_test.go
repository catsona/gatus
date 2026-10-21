package config

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWatchReloadsAtomicReplacementAndReportsInvalidFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := Save(path, Default()); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	changes := Watch(ctx, path)
	cfg := Default()
	cfg.Refresh.Interval = Duration(10 * time.Second)
	if err := Save(path, cfg); err != nil {
		t.Fatal(err)
	}

	timeout := time.NewTimer(4 * time.Second)
	defer timeout.Stop()
	for {
		select {
		case change := <-changes:
			if change.Err == nil && change.Config.Refresh.Interval == cfg.Refresh.Interval {
				goto invalid
			}
		case <-timeout.C:
			t.Fatal("atomic replacement was not reloaded")
		}
	}

invalid:
	if err := os.WriteFile(path, []byte("invalid_field: true\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for {
		select {
		case change := <-changes:
			if change.Err != nil {
				return
			}
		case <-timeout.C:
			t.Fatal("invalid replacement was not reported")
		}
	}
}
