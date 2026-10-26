package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/catsona/gatus/internal/config"
	"github.com/catsona/gatus/internal/desktop"
	"github.com/catsona/gatus/internal/github"
	"github.com/catsona/gatus/internal/herdr"
	"github.com/catsona/gatus/internal/monitor"
	"github.com/catsona/gatus/internal/notify"
	"github.com/catsona/gatus/internal/state"
	"github.com/catsona/gatus/internal/tui"
	"github.com/charmbracelet/x/term"
)

// Run owns service lifetimes; all workers stop before the database is closed.
func Run(ctx context.Context, directory string, output io.Writer) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !term.IsTerminal(os.Stdin.Fd()) {
		return fmt.Errorf("run gatus in an interactive terminal")
	}

	executable, err := exec.LookPath("gh")
	if err != nil {
		return fmt.Errorf("GitHub CLI is required; install gh and run gh auth login")
	}
	version, err := exec.CommandContext(ctx, executable, "version").Output()
	if err != nil {
		return fmt.Errorf("check gh version: %w", err)
	}
	var major, minor, patch int
	fields := strings.Fields(string(version))
	if len(fields) < 3 {
		return fmt.Errorf("cannot read gh version")
	}
	if _, err := fmt.Sscanf(fields[2], "%d.%d.%d", &major, &minor, &patch); err != nil || major < 2 || (major == 2 && minor < 96) {
		return fmt.Errorf("gh 2.96.0 or later is required")
	}

	path := filepath.Join(directory, "config.yaml")
	cfg, err := loadConfig(ctx, path, output)
	if err != nil {
		return err
	}
	store, err := state.Open(filepath.Join(directory, "state.db"))
	if err != nil {
		return err
	}
	defer store.Close()

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	engine := monitor.New(&github.Client{Executable: executable}, store, cfg)
	worker := &notify.Worker{Store: store}
	notices := make(chan string, 1)
	report := func(err error) {
		message := ""
		if err != nil {
			message = err.Error()
		}
		select {
		case notices <- message:
		default:
			select {
			case <-notices:
			default:
			}
			select {
			case notices <- message:
			default:
			}
		}
	}

	var services sync.WaitGroup
	services.Go(func() { engine.Run(ctx) })
	services.Go(func() { worker.Run(ctx, engine.Session, report) })
	model := tui.New(tui.Options{
		Context: ctx, ConfigPath: path, Config: cfg, Backend: engine,
		Desktop: desktop.Native{}, Herdr: herdr.Client{Executable: "herdr"}, Links: store,
		Changes: config.Watch(ctx, path), Notices: notices,
	})
	_, err = tea.NewProgram(model, tea.WithContext(ctx), tea.WithOutput(output)).Run()
	wasCancelled := ctx.Err() != nil
	cancel()
	services.Wait()
	if errors.Is(err, tea.ErrProgramKilled) && wasCancelled {
		return nil
	}
	return err
}

func loadConfig(ctx context.Context, path string, output io.Writer) (config.Config, error) {
	cfg, err := config.Load(path)
	if os.IsNotExist(err) {
		cfg = config.Default()
		if err := config.Save(path, cfg); err != nil {
			return config.Config{}, err
		}
		return cfg, nil
	}
	if err == nil {
		return cfg, nil
	}

	lastError := ""
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		if err != nil && err.Error() != lastError {
			fmt.Fprintf(output, "Config %s: %v\nWaiting for a valid config; Ctrl+C to quit.\n", path, err)
			lastError = err.Error()
		}
		select {
		case <-ctx.Done():
			return config.Config{}, ctx.Err()
		case <-ticker.C:
			cfg, err = config.Load(path)
			if err == nil {
				return cfg, nil
			}
		}
	}
}
