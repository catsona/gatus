package tui

import (
	"context"
	"crypto/sha256"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"git.pegasko.art/catsona/gstatus/internal/config"
	"git.pegasko.art/catsona/gstatus/internal/github"
	"git.pegasko.art/catsona/gstatus/internal/model"
	"git.pegasko.art/catsona/gstatus/internal/monitor"
	"github.com/charmbracelet/x/ansi"
)

type backendFixture struct {
	snapshot  monitor.Snapshot
	marked    []uint64
	cfg       config.Config
	cancelled []string
}

func (b *backendFixture) Updates() <-chan monitor.Snapshot {
	return nil
}

func (b *backendFixture) Snapshot() monitor.Snapshot {
	return b.snapshot
}

func (b *backendFixture) SetConfig(cfg config.Config) {
	b.cfg = cfg
}

func (b *backendFixture) RequestRefresh() {}

func (b *backendFixture) MarkRead(_ string, through uint64) error {
	b.marked = append(b.marked, through)
	return nil
}

func (b *backendFixture) MarkUnread(string) error {
	return nil
}

func (b *backendFixture) Rerun(context.Context, string, bool) (github.Result, error) {
	return github.Result{Accepted: 1}, nil
}

func (b *backendFixture) Cancel(_ context.Context, id string) (github.Result, error) {
	b.cancelled = append(b.cancelled, id)
	return github.Result{Accepted: 1}, nil
}

type desktopFixture struct{ opened, copied string }

func (d *desktopFixture) Open(_ context.Context, value string) error {
	d.opened = value
	return nil
}

func (d *desktopFixture) Copy(_ context.Context, value string) error {
	d.copied = value
	return nil
}

func testModel(t *testing.T, cfg config.Config) (*Model, *backendFixture, *desktopFixture) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := config.Save(path, cfg); err != nil {
		t.Fatal(err)
	}
	backend := &backendFixture{snapshot: monitor.Snapshot{Account: model.Account{Login: "alice"}, Records: []model.Record{
		{PR: model.PR{ID: "a", Title: "Alpha", Url: "https://github.com/acme/app/pull/1", State: "open", Complete: true, UpdatedAt: time.Unix(1, 0)}, Sequence: 2},
		{PR: model.PR{ID: "b", Title: "Beta", Url: "https://github.com/acme/app/pull/2", State: "open", Complete: true, UpdatedAt: time.Unix(2, 0)}, Sequence: 3},
	}}}
	desktop := &desktopFixture{}
	m := New(Options{Context: context.Background(), ConfigPath: path, Config: cfg, Backend: backend, Desktop: desktop})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	return m, backend, desktop
}

func press(m *Model, key rune) tea.Cmd {
	_, cmd := m.Update(tea.KeyPressMsg{Code: key, Text: string(key)})
	return cmd
}

func typeText(m *Model, text string) {
	for _, key := range text {
		press(m, key)
	}
}

func TestSelectionSurvivesResortAndCopyDoesNotAcknowledge(t *testing.T) {
	m, backend, desktop := testModel(t, config.Default())
	press(m, 'j')
	backend.snapshot.Records[0].PR.UpdatedAt = time.Unix(3, 0)
	m.Update(backend.snapshot)
	cmd := press(m, 'y')
	if cmd != nil {
		m.Update(cmd())
	}
	if desktop.copied != "https://github.com/acme/app/pull/1" || len(backend.marked) != 0 {
		t.Fatalf("selection or copy acknowledgement: %q %+v", desktop.copied, backend.marked)
	}

	cmd = press(m, 'o')
	if cmd != nil {
		m.Update(cmd())
	}
	if desktop.opened != desktop.copied || len(backend.marked) != 1 || backend.marked[0] != 2 {
		t.Fatalf("open acknowledgement: %q %+v", desktop.opened, backend.marked)
	}
}

func TestSearchAndConfigurableNavigation(t *testing.T) {
	cfg := config.Default()
	cfg.Keys["down"] = []string{"n"}
	m, _, desktop := testModel(t, cfg)
	press(m, 'n')
	cmd := press(m, 'y')
	if cmd != nil {
		m.Update(cmd())
	}
	if !strings.HasSuffix(desktop.copied, "/1") {
		t.Fatal("configured key did not move selection")
	}

	press(m, '/')
	typeText(m, "Beta")
	if rows := m.visible(); len(rows) != 1 || rows[0].PR.ID != "b" {
		t.Fatalf("search results: %+v", rows)
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	loaded, err := config.Load(m.path)
	if err != nil || loaded.View.Search != "Beta" {
		t.Fatalf("search was not persisted: %+v %v", loaded, err)
	}
}

func TestResponsiveViewAndMouseSelection(t *testing.T) {
	m, _, desktop := testModel(t, config.Default())
	for _, width := range []int{120, 80, 50, 40, 20} {
		m.Update(tea.WindowSizeMsg{Width: width, Height: 20})
		lines := strings.Split(m.View().Content, "\n")
		if len(lines) > 20 {
			t.Fatalf("view exceeds terminal height at width %d", width)
		}
		for _, line := range lines {
			if lipgloss.Width(line) > width {
				t.Fatalf("view overflows width %d: %q", width, line)
			}
		}
	}

	m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	for y, line := range strings.Split(ansi.Strip(m.View().Content), "\n") {
		if strings.Contains(line, "Alpha") {
			m.Update(tea.MouseClickMsg{X: 2, Y: y, Button: tea.MouseLeft})
			break
		}
	}
	cmd := press(m, 'y')
	if cmd != nil {
		m.Update(cmd())
	}
	if !strings.HasSuffix(desktop.copied, "/1") {
		t.Fatalf("mouse selected wrong PR: %s", desktop.copied)
	}
}

func TestSettingsDetectExternalEditBeforeSaving(t *testing.T) {
	m, _, _ := testModel(t, config.Default())
	press(m, ',')
	for i := 0; i < 3; i++ {
		press(m, 'j')
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	typeText(m, "https://example.org/hook")

	changed := config.Default()
	changed.Read.MarkOnOpen = false
	if err := config.Save(m.path, changed); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(m.path)
	m.Update(config.Change{Config: changed, Hash: sha256.Sum256(data)})
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	loaded, _ := config.Load(m.path)
	if loaded.Notifications.WebhookURL != "" || loaded.Read.MarkOnOpen {
		t.Fatal("external edit was overwritten silently")
	}
	if !strings.Contains(strings.ToLower(ansi.Strip(m.View().Content)), "changed on disk") {
		t.Fatal("conflict was not shown")
	}

	press(m, 'w')
	loaded, err := config.Load(m.path)
	if err != nil || loaded.Read.MarkOnOpen || loaded.Notifications.WebhookURL != "https://example.org/hook" {
		t.Fatalf("explicit field overwrite lost unrelated settings: %+v %v", loaded, err)
	}
}

func TestSettingsDoNotRunCIHotkeys(t *testing.T) {
	m, _, _ := testModel(t, config.Default())
	press(m, ',')
	if cmd := press(m, 'R'); cmd != nil {
		t.Fatal("a main-screen CI action was started from settings")
	}
}

func TestCancelCapturesSelectedPRAndPreventsDuplicateRequests(t *testing.T) {
	m, backend, _ := testModel(t, config.Default())
	press(m, 'j')
	command := press(m, 'x')
	if command == nil {
		t.Fatal("cancel shortcut did not start an operation")
	}
	if repeated := press(m, 'x'); repeated != nil {
		t.Fatal("duplicate cancellation started while the first was pending")
	}
	press(m, 'k')
	m.Update(command())
	if len(backend.cancelled) != 1 || backend.cancelled[0] != "a" || len(backend.marked) != 0 {
		t.Fatalf("cancel used the wrong selection or acknowledged it: %+v %+v", backend.cancelled, backend.marked)
	}
}

func TestOpeningMenuKeepsTheCurrentFilter(t *testing.T) {
	m, _, _ := testModel(t, config.Default())
	press(m, 'f')
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	loaded, err := config.Load(m.path)
	if err != nil || loaded.View.Filter != "open" {
		t.Fatalf("opening the state menu changed the filter: %+v %v", loaded, err)
	}
}

func TestBrowserObeysDisabledAcknowledgement(t *testing.T) {
	cfg := config.Default()
	cfg.Read.MarkOnOpen = false
	m, backend, _ := testModel(t, cfg)
	cmd := press(m, 'o')
	if cmd != nil {
		m.Update(cmd())
	}
	if len(backend.marked) != 0 {
		t.Fatal("opening marked a PR despite the disabled setting")
	}
}
