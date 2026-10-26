package tui

import (
	"context"
	"crypto/sha256"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"git.pegasko.art/catsona/gstatus/internal/config"
	"git.pegasko.art/catsona/gstatus/internal/github"
	"git.pegasko.art/catsona/gstatus/internal/herdr"
	"git.pegasko.art/catsona/gstatus/internal/model"
	"git.pegasko.art/catsona/gstatus/internal/monitor"
	"github.com/charmbracelet/x/ansi"
)

type backendFixture struct {
	snapshot   monitor.Snapshot
	marked     []uint64
	cfg        config.Config
	cancelled  []string
	refreshed  []string
	refreshAll int
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

func (b *backendFixture) RequestRefresh() {
	b.refreshAll++
}

func (b *backendFixture) RefreshPR(_ context.Context, id string) error {
	b.refreshed = append(b.refreshed, id)
	return nil
}

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

type herdrFixture struct {
	requests []herdr.Request
	created  []herdr.Request
	focused  []string
	spaces   []herdr.Workspace
	err      error
	focusErr error
}

func (h *herdrFixture) List(context.Context) ([]herdr.Workspace, error) {
	return h.spaces, h.err
}

func (h *herdrFixture) Focus(_ context.Context, id string) error {
	h.focused = append(h.focused, id)
	return h.focusErr
}

func (h *herdrFixture) Create(_ context.Context, request herdr.Request) (herdr.Workspace, error) {
	h.requests = append(h.requests, request)
	h.created = append(h.created, request)
	if h.err != nil {
		return herdr.Workspace{}, h.err
	}
	return herdr.Workspace{ID: "new-space", Label: request.Label, Root: request.Root}, nil
}

type herdrLinksFixture struct {
	links map[string]model.HerdrLink
}

func (h *herdrLinksFixture) HerdrLink(prURL string) (model.HerdrLink, bool, error) {
	link, ok := h.links[prURL]
	return link, ok, nil
}

func (h *herdrLinksFixture) SaveHerdrLink(link model.HerdrLink) error {
	h.links[link.PRURL] = link
	return nil
}

func (h *herdrLinksFixture) RemoveHerdrLink(prURL string) error {
	delete(h.links, prURL)
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

func TestRowRefreshIndicatorTracksQueueRunningAndIdle(t *testing.T) {
	m, backend, _ := testModel(t, config.Default())
	snapshot := backend.snapshot
	snapshot.RefreshStates = map[string]monitor.RefreshState{"a": monitor.RefreshQueued, "b": monitor.RefreshRunning}
	m.Update(snapshot)
	firstCharacter := func(title string) rune {
		t.Helper()
		for _, line := range strings.Split(ansi.Strip(m.View().Content), "\n") {
			if strings.Contains(line, title) {
				return []rune(line)[0]
			}
		}
		t.Fatalf("PR %s is not visible", title)
		return 0
	}
	queued, running := firstCharacter("Alpha"), firstCharacter("Beta")
	m.Update(tickMsg(time.Now()))
	if next := firstCharacter("Beta"); next == running {
		t.Fatal("running refresh indicator did not animate")
	}
	snapshot.RefreshStates = nil
	m.Update(snapshot)
	idle := firstCharacter("Alpha")
	if queued == running || idle == queued || idle == running {
		t.Fatalf("refresh phases are indistinguishable: queued=%q running=%q idle=%q", queued, running, idle)
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

func TestAttachingHerdrListsAndFocusesSelectedSpace(t *testing.T) {
	cfg := config.Default()
	cfg.Herdr.Enabled = true
	cfg.Herdr.Root = "/tmp/herdr-root"
	m, _, _ := testModel(t, cfg)
	m.snapshot.Records[0].PR.Repository = "acme/app"
	m.snapshot.Records[0].PR.Number = 1
	m.Update(m.snapshot)
	service := &herdrFixture{spaces: []herdr.Workspace{{ID: "space-1", Label: "Shared", Root: "/work/shared"}}}
	links := &herdrLinksFixture{links: map[string]model.HerdrLink{}}
	m.herdr, m.links = service, links

	press(m, 'j')
	command := press(m, 'E')
	if command == nil {
		t.Fatal("Herdr shortcut did not start an operation")
	}
	m.Update(command())
	if m.menu != "herdr" || len(m.herdrSpaces) != 1 {
		t.Fatalf("Herdr list was not opened: menu=%q spaces=%+v", m.menu, m.herdrSpaces)
	}
	command = press(m, tea.KeyEnter)
	if command == nil {
		t.Fatal("Herdr selection did not start focus")
	}
	m.Update(command())

	if len(service.focused) != 1 || service.focused[0] != "space-1" {
		t.Fatalf("Herdr focus requests = %+v", service.focused)
	}
	if links.links["https://github.com/acme/app/pull/1"].WorkspaceID != "space-1" {
		t.Fatalf("Herdr link = %+v", links.links)
	}
}

func TestCreatingHerdrSpaceLinksSelectedPR(t *testing.T) {
	cfg := config.Default()
	cfg.Herdr.Enabled = true
	cfg.Herdr.Root = "/tmp/herdr-root"
	m, _, _ := testModel(t, cfg)
	service := &herdrFixture{}
	links := &herdrLinksFixture{links: map[string]model.HerdrLink{}}
	m.herdr, m.links = service, links

	_, command := m.Update(tea.KeyPressMsg{Code: 'e', Mod: tea.ModCtrl})
	if command == nil {
		t.Fatal("ctrl+e did not start Herdr create")
	}
	m.Update(command())

	if len(service.created) != 1 || links.links["https://github.com/acme/app/pull/2"].WorkspaceID != "new-space" {
		t.Fatalf("created=%+v links=%+v", service.created, links.links)
	}
}

func TestOpeningLinkedHerdrSpaceFocusesTheStoredID(t *testing.T) {
	cfg := config.Default()
	cfg.Herdr.Enabled = true
	m, _, _ := testModel(t, cfg)
	service := &herdrFixture{}
	links := &herdrLinksFixture{links: map[string]model.HerdrLink{
		"https://github.com/acme/app/pull/2": {PRURL: "https://github.com/acme/app/pull/2", WorkspaceID: "space-1"},
	}}
	m.herdr, m.links = service, links

	command := press(m, 'e')
	if command == nil {
		t.Fatal("e did not start linked Herdr focus")
	}
	m.Update(command())
	if !slices.Equal(service.focused, []string{"space-1"}) {
		t.Fatalf("linked Herdr focus = %+v", service.focused)
	}
}

func TestOpeningHerdrWithoutLinkReportsAnError(t *testing.T) {
	cfg := config.Default()
	cfg.Herdr.Enabled = true
	m, _, _ := testModel(t, cfg)
	service := &herdrFixture{}
	m.herdr, m.links = service, &herdrLinksFixture{links: map[string]model.HerdrLink{}}

	if command := press(m, 'e'); command != nil {
		t.Fatal("e opened an unlinked Herdr PR")
	}
	if !strings.Contains(strings.ToLower(m.message), "not linked") {
		t.Fatalf("unlinked Herdr message = %q", m.message)
	}
}

func TestOpeningClosedHerdrSpaceReportsClosed(t *testing.T) {
	cfg := config.Default()
	cfg.Herdr.Enabled = true
	m, _, _ := testModel(t, cfg)
	service := &herdrFixture{focusErr: errors.New("workspace_not_found")}
	links := &herdrLinksFixture{links: map[string]model.HerdrLink{
		"https://github.com/acme/app/pull/2": {PRURL: "https://github.com/acme/app/pull/2", WorkspaceID: "space-1"},
	}}
	m.herdr, m.links = service, links

	command := press(m, 'e')
	if command == nil {
		t.Fatal("e did not start linked Herdr focus")
	}
	m.Update(command())
	if !strings.Contains(strings.ToLower(m.message), "closed") {
		t.Fatalf("closed Herdr message = %q", m.message)
	}
}

func TestFailedHerdrFocusKeepsThePreviousLink(t *testing.T) {
	cfg := config.Default()
	cfg.Herdr.Enabled = true
	m, _, _ := testModel(t, cfg)
	service := &herdrFixture{spaces: []herdr.Workspace{{ID: "space-2", Label: "Replacement"}}, focusErr: errors.New("focus failed")}
	links := &herdrLinksFixture{links: map[string]model.HerdrLink{
		"https://github.com/acme/app/pull/2": {PRURL: "https://github.com/acme/app/pull/2", WorkspaceID: "space-1"},
	}}
	m.herdr, m.links = service, links

	if command := press(m, 'E'); command == nil {
		t.Fatal("Herdr attach did not start")
	} else {
		m.Update(command())
	}
	command := press(m, tea.KeyEnter)
	if command == nil {
		t.Fatal("Herdr focus did not start")
	}
	m.Update(command())

	if links.links["https://github.com/acme/app/pull/2"].WorkspaceID != "space-1" {
		t.Fatalf("failed focus replaced link: %+v", links.links)
	}
}

func TestDisabledHerdrShortcutDoesNotInvokeTheClient(t *testing.T) {
	m, _, _ := testModel(t, config.Default())
	service := &herdrFixture{}
	m.herdr = service
	m.links = &herdrLinksFixture{links: map[string]model.HerdrLink{}}

	if command := press(m, 'e'); command != nil {
		t.Fatal("disabled Herdr shortcut started an operation")
	}
	if !strings.Contains(strings.ToLower(m.message), "disabled") || len(service.requests) != 0 {
		t.Fatalf("disabled Herdr state: message=%q requests=%+v", m.message, service.requests)
	}
}

func TestHerdrShortcutPreventsDuplicateRequests(t *testing.T) {
	cfg := config.Default()
	cfg.Herdr.Enabled = true
	m, _, _ := testModel(t, cfg)
	service := &herdrFixture{}
	m.herdr = service
	m.links = &herdrLinksFixture{links: map[string]model.HerdrLink{}}

	command := press(m, 'E')
	if command == nil || press(m, 'E') != nil {
		t.Fatal("duplicate Herdr operation was accepted")
	}
	m.Update(command())
	if m.menu != "herdr" {
		t.Fatalf("Herdr list menu = %q", m.menu)
	}
}

func TestHerdrSettingsPersist(t *testing.T) {
	m, _, _ := testModel(t, config.Default())
	m.settings = true
	rows := m.settingRows()

	for index, row := range rows {
		if row.id == "herdr_enabled" {
			m.settingIndex = index
			m.activateSetting()
		}
	}
	for index, row := range m.settingRows() {
		if row.id == "herdr_root" {
			m.settingIndex = index
			m.activateSetting()
			m.input.SetValue("/tmp/herdr-root")
			m.saveEditor(false)
		}
	}

	loaded, err := config.Load(m.path)
	if err != nil || !loaded.Herdr.Enabled || loaded.Herdr.Root != "/tmp/herdr-root" {
		t.Fatalf("Herdr settings were not persisted: %+v %v", loaded.Herdr, err)
	}
}

func TestSelectedRefreshDoesNotRequestTheWholeList(t *testing.T) {
	m, backend, _ := testModel(t, config.Default())
	press(m, 'j')
	command := press(m, 'p')
	if command == nil {
		t.Fatal("selected refresh shortcut did not start an operation")
	}
	press(m, 'k')
	m.Update(command())
	if len(backend.refreshed) != 1 || backend.refreshed[0] != "a" || backend.refreshAll != 0 || len(backend.marked) != 0 {
		t.Fatalf("selected refresh fetched or acknowledged the wrong PR: %+v all=%d read=%v", backend.refreshed, backend.refreshAll, backend.marked)
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

func TestSortDirectionShortcutAndButtonPersistTheOrder(t *testing.T) {
	m, _, _ := testModel(t, config.Default())
	selected := m.selectedID
	press(m, 'S')
	rows := m.visible()
	if len(rows) != 2 || rows[0].PR.ID != "a" || rows[1].PR.ID != "b" || m.selectedID != selected {
		t.Fatalf("ascending order or selection: %+v selected=%s", rows, m.selectedID)
	}
	loaded, err := config.Load(m.path)
	if err != nil || loaded.View.Descending {
		t.Fatalf("ascending direction was not saved: %+v %v", loaded.View, err)
	}

	view, regions := m.layout()
	if !strings.Contains(ansi.Strip(view), "ASC") {
		t.Fatal("ascending direction is not visible")
	}
	clicked := false
	for _, region := range regions {
		if region.action == "sort_direction" {
			m.Update(tea.MouseClickMsg{X: region.x, Y: region.y, Button: tea.MouseLeft})
			clicked = true
			break
		}
	}
	rows = m.visible()
	loaded, err = config.Load(m.path)
	if !clicked || err != nil || !loaded.View.Descending || rows[0].PR.ID != "b" || m.selectedID != selected {
		t.Fatalf("descending order or persistence: clicked=%t cfg=%+v err=%v", clicked, loaded.View, err)
	}
	if !strings.Contains(ansi.Strip(m.View().Content), "DESC") {
		t.Fatal("descending direction is not visible")
	}
	m.Update(tea.WindowSizeMsg{Width: 40, Height: 10})
	press(m, 'S')
	if !strings.Contains(ansi.Strip(m.View().Content), "ASC") {
		t.Fatal("compact controls hide the ascending direction")
	}
}

func TestSortDirectionLeavesUnknownTimestampsLast(t *testing.T) {
	cfg := config.Default()
	cfg.View.Sort = "ci"
	m, backend, _ := testModel(t, cfg)
	backend.snapshot.Records[0].PR.LastCI = time.Unix(1, 0)
	backend.snapshot.Records[1].PR.LastCI = time.Unix(2, 0)
	backend.snapshot.Records = append(backend.snapshot.Records, model.Record{PR: model.PR{ID: "unknown", State: "open"}})
	m.Update(backend.snapshot)
	for _, want := range []string{"b", "a"} {
		rows := m.visible()
		if len(rows) != 3 || rows[0].PR.ID != want || rows[2].PR.ID != "unknown" {
			t.Fatalf("wrong timestamp order: %+v", rows)
		}
		press(m, 'S')
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
