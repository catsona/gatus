package app

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCancelledStartupDoesNotCreateState(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "gatus")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var output bytes.Buffer
	if err := Run(ctx, directory, &output); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled startup: %v", err)
	}
	if _, err := os.Stat(directory); !os.IsNotExist(err) {
		t.Fatal("cancelled startup created application data")
	}
}

func TestInvalidStartupConfigIsNotOverwritten(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	original := []byte("unknown_field: true\n")
	if err := os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	var output bytes.Buffer
	if _, err := loadConfig(ctx, path, &output); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("invalid config did not wait for repair: %v", err)
	}
	data, _ := os.ReadFile(path)
	if !bytes.Equal(data, original) || !bytes.Contains(output.Bytes(), []byte("Waiting")) {
		t.Fatal("invalid startup settings were overwritten or not reported")
	}
}

func TestPTYHelper(t *testing.T) {
	if os.Getenv("GATUS_PTY_HELPER") != "1" {
		t.Skip("requires the PTY smoke harness")
	}

	if err := Run(context.Background(), os.Getenv("GATUS_TEST_DIR"), os.Stdout); err != nil {
		t.Fatal(err)
	}
}
