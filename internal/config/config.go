package config

import (
	"bytes"
	"fmt"
	"io"
	"net/url"
	"regexp"
	"slices"
	"text/template"
	"time"

	"git.pegasko.art/catsona/gstatus/internal/model"
	"go.yaml.in/yaml/v3"
)

// Duration accepts Go duration strings in YAML.
type Duration time.Duration

// UnmarshalYAML rejects values that are not Go duration strings.
func (d *Duration) UnmarshalYAML(node *yaml.Node) error {
	value, err := time.ParseDuration(node.Value)
	if err != nil {
		return err
	}

	*d = Duration(value)
	return nil
}

// MarshalYAML preserves a duration's human-readable representation.
func (d Duration) MarshalYAML() (any, error) {
	return time.Duration(d).String(), nil
}

// Config is applied only after the whole document passes Validate.
type Config struct {
	Version       int                 `yaml:"version"`
	Repositories  []string            `yaml:"repositories"`
	Refresh       Refresh             `yaml:"refresh"`
	View          View                `yaml:"view"`
	Read          Read                `yaml:"read"`
	Notifications Notifications       `yaml:"notifications"`
	Keys          map[string][]string `yaml:"keys"`
}

// Refresh controls PR polling, independently of account detection.
type Refresh struct {
	Enabled  bool     `yaml:"enabled"`
	Interval Duration `yaml:"interval"`
}

// View controls the main screen without restricting background monitoring.
type View struct {
	Filter     string `yaml:"filter"`
	Sort       string `yaml:"sort"`
	Descending bool   `yaml:"descending"`
	Search     string `yaml:"search"`
}

// Read controls acknowledgement after a browser launch succeeds.
type Read struct {
	MarkOnOpen bool `yaml:"mark_on_open"`
}

// Notifications filters future events; enabling it does not replay history.
type Notifications struct {
	Enabled    bool     `yaml:"enabled"`
	WebhookURL string   `yaml:"webhook_url"`
	Events     []string `yaml:"events"`
	Template   string   `yaml:"template"`
}

// Default returns independent, editable settings with notifications disabled.
func Default() Config {
	return Config{
		Version:      1,
		Repositories: []string{},
		Refresh:      Refresh{Enabled: true, Interval: Duration(30 * time.Second)},
		View:         View{Filter: "open", Sort: "updated", Descending: true},
		Read:         Read{MarkOnOpen: true},
		Notifications: Notifications{
			Events:   []string{"comment.created", "thread.reply", "review.submitted", "ci.success", "ci.failure", "pr.state_changed", "pr.created"},
			Template: "{{.PR.Url}} {{.PR.Title}} status update: {{.Event.Summary}}",
		},
		Keys: map[string][]string{
			"up": {"up", "k"}, "down": {"down", "j"},
			"focus_next": {"tab"}, "focus_previous": {"shift+tab"},
			"open": {"enter", "o"}, "copy_url": {"y"},
			"mark_read": {"m"}, "mark_unread": {"u"},
			"refresh": {"r"}, "toggle_auto_refresh": {"a"}, "refresh_interval": {"i"},
			"search": {"/"}, "filter": {"f"}, "sort": {"s"},
			"rerun_all": {"ctrl+r"}, "rerun_failed": {"R"},
			"settings": {","}, "help": {"?"}, "back": {"esc"}, "quit": {"q", "ctrl+c"},
		},
	}
}

// Parse merges omitted fields with defaults and rejects incomplete documents.
func Parse(data []byte) (Config, error) {
	cfg := Default()
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(&cfg); err != nil && err != io.EOF {
		return Config{}, fmt.Errorf("parse config: %w", err)
	}

	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return Config{}, fmt.Errorf("config must contain one YAML document")
	}

	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}

	return cfg, nil
}

// Validate checks the complete configuration and the template field contract.
func (c Config) Validate() error {
	if c.Version != 1 {
		return fmt.Errorf("unsupported config version %d", c.Version)
	}

	if time.Duration(c.Refresh.Interval) < 5*time.Second {
		return fmt.Errorf("refresh interval must be at least 5s")
	}

	repositoryName := regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)
	for _, repository := range c.Repositories {
		if !repositoryName.MatchString(repository) {
			return fmt.Errorf("repository must be owner/repo: %q", repository)
		}
	}

	if !slices.Contains([]string{"all", "open", "closed", "draft", "merged"}, c.View.Filter) {
		return fmt.Errorf("unknown PR filter %q", c.View.Filter)
	}

	if !slices.Contains([]string{"created", "updated", "ci", "review", "message"}, c.View.Sort) {
		return fmt.Errorf("unknown sort %q", c.View.Sort)
	}

	defaults := Default()
	for _, event := range c.Notifications.Events {
		if !slices.Contains(defaults.Notifications.Events, event) {
			return fmt.Errorf("unknown notification event %q", event)
		}
	}

	if c.Notifications.Enabled || c.Notifications.WebhookURL != "" {
		address, err := url.Parse(c.Notifications.WebhookURL)
		if err != nil || address.Host == "" || (address.Scheme != "http" && address.Scheme != "https") {
			return fmt.Errorf("webhook URL must be an HTTP or HTTPS URL")
		}
	}

	tmpl, err := template.New("notification").Option("missingkey=error").Parse(c.Notifications.Template)
	if err != nil {
		return fmt.Errorf("notification template: %w", err)
	}
	if err := validateTemplate(tmpl); err != nil {
		return fmt.Errorf("notification template: %w", err)
	}

	if err := tmpl.Execute(io.Discard, model.TemplateData{}); err != nil {
		return fmt.Errorf("notification template: %w", err)
	}

	assigned := map[string]string{}
	for action, keys := range c.Keys {
		if _, exists := defaults.Keys[action]; !exists {
			return fmt.Errorf("unknown action %q", action)
		}

		for _, key := range keys {
			if !validKey(key) {
				return fmt.Errorf("invalid key %q", key)
			}

			canonical := CanonicalKey(key)
			if previous, exists := assigned[canonical]; exists && previous != action {
				return fmt.Errorf("key %q is assigned to %s and %s", key, previous, action)
			}

			assigned[canonical] = action
		}
	}

	return nil
}
