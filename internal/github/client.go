package github

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Client invokes gh with captured credentials; it is safe for concurrent readers.
type Client struct {
	Executable string
	once       sync.Once
	slots      chan struct{}
	mu         sync.Mutex
	cache      map[string]cachedResponse
}

type cachedResponse struct {
	etag string
	body []byte
}

// RateLimitError tells the scheduler when GitHub permits another request.
type RateLimitError struct {
	Until time.Time
}

func (e *RateLimitError) Error() string {
	return "GitHub rate limit; retry after " + e.Until.Local().Format(time.TimeOnly)
}

func (c *Client) initialize() {
	c.once.Do(func() {
		c.slots = make(chan struct{}, 4)
		c.cache = map[string]cachedResponse{}
	})
}

func (c *Client) output(ctx context.Context, token string, args ...string) ([]byte, error) {
	c.initialize()

	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	if len(args) == 0 || args[0] != "auth" {
		select {
		case c.slots <- struct{}{}:
			defer func() { <-c.slots }()
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}

	executable := c.Executable
	if executable == "" {
		executable = "gh"
	}

	command := exec.CommandContext(ctx, executable, args...)
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		if name == "GH_DEBUG" || name == "DEBUG" || name == "GH_PAGER" || name == "GH_PROMPT_DISABLED" || name == "GH_TELEMETRY" {
			continue
		}

		if token != "" && (name == "GH_TOKEN" || name == "GITHUB_TOKEN" || name == "GH_HOST" || name == "GH_REPO") {
			continue
		}

		command.Env = append(command.Env, entry)
	}

	command.Env = append(command.Env, "GH_PROMPT_DISABLED=1", "GH_PAGER=cat", "GH_TELEMETRY=false")
	if token != "" {
		command.Env = append(command.Env, "GH_TOKEN="+token, "GH_HOST=github.com")
	}

	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		message := strings.TrimSpace(stderr.String())
		if token != "" {
			message = strings.ReplaceAll(message, token, "[redacted]")
		}

		if message == "" {
			message = err.Error()
		}

		if len(message) > 4096 {
			message = message[:4096]
		}

		return stdout.Bytes(), fmt.Errorf("gh: %s", message)
	}

	return stdout.Bytes(), nil
}

func (c *Client) api(ctx context.Context, token, endpoint string, target any) error {
	c.initialize()
	key := fmt.Sprintf("%x:%s", sha256.Sum256([]byte(token)), endpoint)
	c.mu.Lock()
	cached := c.cache[key]
	c.mu.Unlock()

	args := []string{"api", "--hostname", "github.com", "--method", "GET", "--include"}
	if cached.etag != "" {
		args = append(args, "--header", "If-None-Match: "+cached.etag)
	}
	args = append(args, endpoint)

	output, commandErr := c.output(ctx, token, args...)
	response, err := http.ReadResponse(bufio.NewReader(bytes.NewReader(output)), nil)
	if err != nil {
		if commandErr != nil {
			return commandErr
		}

		return fmt.Errorf("invalid gh API response: %w", err)
	}
	defer response.Body.Close()

	if response.StatusCode == http.StatusNotModified && cached.body != nil {
		return json.Unmarshal(cached.body, target)
	}

	if response.StatusCode == 429 || (response.StatusCode == 403 && response.Header.Get("X-RateLimit-Remaining") == "0") {
		until := time.Now().Add(time.Minute)
		if reset, err := strconv.ParseInt(response.Header.Get("X-RateLimit-Reset"), 10, 64); err == nil {
			until = time.Unix(reset, 0)
		}

		if seconds, err := strconv.Atoi(response.Header.Get("Retry-After")); err == nil {
			until = time.Now().Add(time.Duration(seconds) * time.Second)
		}

		return &RateLimitError{Until: until}
	}

	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("GitHub %s: %s", endpoint, response.Status)
	}

	if commandErr != nil {
		return commandErr
	}

	body, err := io.ReadAll(io.LimitReader(response.Body, 10<<20))
	if err != nil {
		return err
	}

	if err := json.Unmarshal(body, target); err != nil {
		return fmt.Errorf("decode GitHub response: %w", err)
	}

	if etag := response.Header.Get("ETag"); etag != "" {
		c.mu.Lock()
		if len(c.cache) >= 2048 {
			clear(c.cache)
		}
		c.cache[key] = cachedResponse{etag: etag, body: body}
		c.mu.Unlock()
	}

	return nil
}

func pages[T any](ctx context.Context, c *Client, token, endpoint string) ([]T, error) {
	var result []T
	separator := "?"
	if strings.Contains(endpoint, "?") {
		separator = "&"
	}

	for page := 1; ; page++ {
		var values []T
		address := fmt.Sprintf("%s%sper_page=100&page=%d", endpoint, separator, page)
		if err := c.api(ctx, token, address, &values); err != nil {
			return nil, err
		}

		result = append(result, values...)
		if len(values) < 100 {
			return result, nil
		}
	}
}
