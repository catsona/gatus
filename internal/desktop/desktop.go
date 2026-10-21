package desktop

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/google/shlex"
)

// ErrNoClipboard lets the terminal renderer fall back to OSC 52.
var ErrNoClipboard = errors.New("no system clipboard command is available")

// Native opens URLs and writes clipboard text without evaluating URL content as shell code.
type Native struct{}

// Open honors GH_BROWSER/BROWSER, otherwise uses the system URL handler.
func (Native) Open(ctx context.Context, address string) error {
	command := os.Getenv("GH_BROWSER")
	if command == "" {
		command = os.Getenv("BROWSER")
	}

	var args []string
	if command != "" {
		var err error
		args, err = shlex.Split(command)
		if err != nil || len(args) == 0 {
			return fmt.Errorf("invalid browser command")
		}
	} else if runtime.GOOS == "darwin" {
		args = []string{"open"}
	} else {
		args = []string{"xdg-open"}
	}

	replaced := false
	for i := range args {
		if strings.Contains(args[i], "%s") {
			args[i] = strings.ReplaceAll(args[i], "%s", address)
			replaced = true
		}
	}
	if !replaced {
		args = append(args, address)
	}

	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := exec.CommandContext(ctx, args[0], args[1:]...).Run(); err != nil {
		return fmt.Errorf("open browser: %w", err)
	}

	return nil
}

// Copy writes an exact URL to a system clipboard, or requests a terminal fallback.
func (Native) Copy(ctx context.Context, value string) error {
	candidates := [][]string{{"wl-copy"}, {"xclip", "-selection", "clipboard"}, {"xsel", "--clipboard", "--input"}}
	if runtime.GOOS == "darwin" {
		candidates = [][]string{{"pbcopy"}}
	}

	for _, args := range candidates {
		path, err := exec.LookPath(args[0])
		if err != nil {
			continue
		}

		ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		command := exec.CommandContext(ctx, path, args[1:]...)
		command.Stdin = strings.NewReader(value)
		err = command.Run()
		cancel()
		if err == nil {
			return nil
		}
	}

	return ErrNoClipboard
}
