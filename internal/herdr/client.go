package herdr

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const tokenName = "gatus_pr"

// Request identifies the workspace that should be focused or created.
type Request struct {
	Root  string
	Label string
	PRKey string
}

// Opener focuses an existing workspace or creates one for a pull request.
type Opener interface {
	Open(context.Context, Request) error
}

// Workspace is a live Herdr workspace with fields suitable for selection.
type Workspace struct {
	ID    string
	Label string
	Root  string
}

// Service exposes the Herdr operations used by the TUI.
type Service interface {
	List(context.Context) ([]Workspace, error)
	Focus(context.Context, string) error
	Create(context.Context, Request) (Workspace, error)
}

// Client invokes the public Herdr CLI.
type Client struct {
	Executable string
}

type workspace struct {
	ID     string            `json:"workspace_id"`
	Label  string            `json:"label"`
	CWD    string            `json:"cwd"`
	Root   string            `json:"root"`
	Tokens map[string]string `json:"tokens"`
}

type listResult struct {
	Result struct {
		Workspaces []workspace `json:"workspaces"`
	} `json:"result"`
}

type createResult struct {
	Result struct {
		Workspace workspace `json:"workspace"`
	} `json:"result"`
}

type commandError struct {
	err    error
	stderr string
}

// List returns the live Herdr workspaces.
func (c Client) List(parent context.Context) ([]Workspace, error) {
	ctx, cancel := context.WithTimeout(parent, 10*time.Second)
	defer cancel()

	var result listResult
	if err := c.runJSON(ctx, &result, "workspace", "list"); err != nil {
		return nil, err
	}

	workspaces := make([]Workspace, 0, len(result.Result.Workspaces))
	for _, value := range result.Result.Workspaces {
		workspaces = append(workspaces, displayWorkspace(value))
	}
	return workspaces, nil
}

// Focus makes a live Herdr workspace active.
func (c Client) Focus(parent context.Context, id string) error {
	ctx, cancel := context.WithTimeout(parent, 10*time.Second)
	defer cancel()

	return c.run(ctx, "workspace", "focus", id)
}

// Create starts and focuses a new Herdr workspace.
func (c Client) Create(parent context.Context, request Request) (Workspace, error) {
	ctx, cancel := context.WithTimeout(parent, 10*time.Second)
	defer cancel()

	root, err := expandRoot(request.Root)
	if err != nil {
		return Workspace{}, err
	}

	var result createResult
	if err := c.runJSON(ctx, &result, "workspace", "create", "--cwd", root, "--label", request.Label, "--focus"); err != nil {
		return Workspace{}, err
	}
	if result.Result.Workspace.ID == "" {
		return Workspace{}, errors.New("Herdr create response did not contain a workspace ID")
	}

	workspace := displayWorkspace(result.Result.Workspace)
	if workspace.Label == "" {
		workspace.Label = request.Label
	}
	if workspace.Root == "" {
		workspace.Root = root
	}
	return workspace, nil
}

func (e *commandError) Error() string {
	message := strings.TrimSpace(e.stderr)
	if message == "" {
		return e.err.Error()
	}
	return fmt.Sprintf("%v: %s", e.err, message)
}

func (e *commandError) Unwrap() error {
	return e.err
}

// Open focuses a tagged workspace or creates and tags a new workspace.
func (c Client) Open(parent context.Context, request Request) error {
	ctx, cancel := context.WithTimeout(parent, 10*time.Second)
	defer cancel()

	root, err := expandRoot(request.Root)
	if err != nil {
		return err
	}

	var listed listResult
	if err := c.runJSON(ctx, &listed, "workspace", "list"); err != nil {
		return err
	}

	for _, candidate := range listed.Result.Workspaces {
		if candidate.Tokens[tokenName] != request.PRKey {
			continue
		}

		if err := c.run(ctx, "workspace", "focus", candidate.ID); err == nil {
			return nil
		} else if !workspaceNotFound(err) {
			return err
		}

		break
	}

	var created createResult
	if err := c.runJSON(ctx, &created, "workspace", "create", "--cwd", root, "--label", request.Label, "--focus"); err != nil {
		return err
	}
	if created.Result.Workspace.ID == "" {
		return errors.New("Herdr create response did not contain a workspace ID")
	}

	if err := c.run(ctx, "workspace", "report-metadata", created.Result.Workspace.ID, "--source", "gatus", "--token", tokenName+"="+request.PRKey); err != nil {
		return err
	}

	return nil
}

func displayWorkspace(value workspace) Workspace {
	root := value.Root
	if root == "" {
		root = value.CWD
	}
	return Workspace{ID: value.ID, Label: value.Label, Root: root}
}

func (c Client) runJSON(ctx context.Context, target any, args ...string) error {
	output, err := c.runOutput(ctx, args...)
	if err != nil {
		return err
	}

	if err := json.Unmarshal(output, target); err != nil {
		return fmt.Errorf("decode Herdr response: %w", err)
	}

	return nil
}

func (c Client) run(ctx context.Context, args ...string) error {
	_, err := c.runOutput(ctx, args...)
	return err
}

func (c Client) runOutput(ctx context.Context, args ...string) ([]byte, error) {
	executable := c.Executable
	if executable == "" {
		executable = "herdr"
	}

	var stderr bytes.Buffer
	command := exec.CommandContext(ctx, executable, args...)
	command.Stderr = &stderr
	output, err := command.Output()
	if err != nil {
		return nil, &commandError{err: err, stderr: stderr.String()}
	}

	return output, nil
}

func workspaceNotFound(err error) bool {
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "workspace_not_found") || strings.Contains(message, "workspace not found")
}

// IsWorkspaceNotFound reports whether Herdr could not find a workspace.
func IsWorkspaceNotFound(err error) bool {
	return err != nil && workspaceNotFound(err)
}

func expandRoot(root string) (string, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return "", errors.New("Herdr root must be set")
	}

	if root == "~" || strings.HasPrefix(root, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("find home directory: %w", err)
		}
		if root == "~" {
			return home, nil
		}
		return filepath.Join(home, strings.TrimPrefix(root, "~/")), nil
	}

	return filepath.Clean(root), nil
}
