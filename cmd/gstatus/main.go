package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"git.pegasko.art/catsona/gstatus/internal/app"
)

var version = "dev"

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "gstatus:", err)
		os.Exit(1)
	}
}

func run(args []string, output io.Writer) error {
	if len(args) == 1 {
		switch args[0] {
		case "--version":
			_, err := fmt.Fprintf(output, "gstatus %s\n", version)
			return err

		case "--help", "-h":
			_, err := fmt.Fprintln(output, "Usage: gstatus [--version | --help]")
			return err
		}
	}

	if len(args) != 0 {
		return fmt.Errorf("unsupported arguments: %v", args)
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	err = app.Run(ctx, filepath.Join(home, ".gstatus"), output)
	if errors.Is(err, context.Canceled) {
		return nil
	}
	return err
}
