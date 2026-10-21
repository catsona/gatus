package tui

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"slices"
	"sort"
	"strings"
	"time"

	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"git.pegasko.art/catsona/gstatus/internal/config"
	"git.pegasko.art/catsona/gstatus/internal/github"
	"git.pegasko.art/catsona/gstatus/internal/model"
	"git.pegasko.art/catsona/gstatus/internal/monitor"
)

// Backend exposes monitor actions without coupling terminal rendering to network work.
type Backend interface {
	Updates() <-chan monitor.Snapshot
	Snapshot() monitor.Snapshot
	SetConfig(config.Config)
	RequestRefresh()
	RefreshPR(context.Context, string) error
	MarkRead(string, uint64) error
	MarkUnread(string) error
	Rerun(context.Context, string, bool) (github.Result, error)
	Cancel(context.Context, string) (github.Result, error)
}

// Desktop launches the browser and copies links without acknowledging PR events.
type Desktop interface {
	Open(context.Context, string) error
	Copy(context.Context, string) error
}

// Options connects the UI to cancellable services and the shared config file.
type Options struct {
	Context    context.Context
	ConfigPath string
	Config     config.Config
	Backend    Backend
	Desktop    Desktop
	Changes    <-chan config.Change
	Notices    <-chan string
}

// Model keeps local selection while asynchronous monitor snapshots replace table data.
type Model struct {
	ctx                         context.Context
	path                        string
	cfg                         config.Config
	backend                     Backend
	desktop                     Desktop
	changes                     <-chan config.Change
	notices                     <-chan string
	snapshot                    monitor.Snapshot
	width, height               int
	selectedID                  string
	offset                      int
	focus                       int
	settings                    bool
	settingIndex, settingOffset int
	menu                        string
	menuIndex                   int
	edit                        string
	input                       textinput.Model
	area                        textarea.Model
	fileHash, editHash          [32]byte
	conflict                    bool
	configError, noticeError    string
	message                     string
	messageAt                   time.Time
	busy                        map[string]bool
	lastClickID                 string
	lastClickAt                 time.Time
	refreshFrame                int
}

type tickMsg time.Time
type noticeMsg string

type operationResult struct {
	generation   uint64
	id           string
	sequence     uint64
	acknowledge  bool
	releaseBusy  bool
	copyFallback string
	message      string
	err          error
}

// New prepares a model without starting background services or changing user state.
func New(options Options) *Model {
	ctx := options.Context
	if ctx == nil {
		ctx = context.Background()
	}

	m := &Model{
		ctx: ctx, path: options.ConfigPath, cfg: options.Config,
		backend: options.Backend, desktop: options.Desktop, changes: options.Changes, notices: options.Notices,
		width: 80, height: 24, focus: -1, input: textinput.New(), area: textarea.New(), busy: map[string]bool{},
	}
	m.input.Prompt = "> "
	m.input.SetWidth(72)
	m.area.ShowLineNumbers = false
	m.area.SetWidth(72)
	m.area.SetHeight(4)
	data, err := os.ReadFile(m.path)
	if err == nil {
		cfg, parseErr := config.Parse(data)
		if parseErr == nil {
			m.cfg, m.fileHash = cfg, sha256.Sum256(data)
			if m.backend != nil {
				m.backend.SetConfig(cfg)
			}
		} else {
			m.configError = parseErr.Error()
		}
	}
	if m.backend != nil {
		m.snapshot = m.backend.Snapshot()
	}
	m.normalizeSelection()
	return m
}

// Init subscribes to coalesced updates; services never run on the render loop.
func (m *Model) Init() tea.Cmd {
	return tea.Batch(m.waitSnapshot(), m.waitConfig(), m.waitNotice(), nextTick())
}

func nextTick() tea.Cmd {
	return tea.Tick(125*time.Millisecond, func(now time.Time) tea.Msg { return tickMsg(now) })
}

func (m *Model) waitSnapshot() tea.Cmd {
	if m.backend == nil || m.backend.Updates() == nil {
		return nil
	}
	return func() tea.Msg {
		select {
		case snapshot := <-m.backend.Updates():
			return snapshot
		case <-m.ctx.Done():
			return nil
		}
	}
}

func (m *Model) waitConfig() tea.Cmd {
	if m.changes == nil {
		return nil
	}
	return func() tea.Msg {
		select {
		case change, ok := <-m.changes:
			if ok {
				return change
			}
		case <-m.ctx.Done():
		}
		return nil
	}
}

func (m *Model) waitNotice() tea.Cmd {
	if m.notices == nil {
		return nil
	}
	return func() tea.Msg {
		select {
		case value, ok := <-m.notices:
			if ok {
				return noticeMsg(value)
			}
		case <-m.ctx.Done():
		}
		return nil
	}
}

// Update applies UI events and accepts results only for the displayed account generation.
func (m *Model) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := message.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.input.SetWidth(max(1, msg.Width-5))
		m.area.SetWidth(max(1, msg.Width-4))
		m.area.SetHeight(max(1, min(4, msg.Height-8)))
		m.normalizeSelection()

	case monitor.Snapshot:
		if msg.Generation != m.snapshot.Generation {
			m.selectedID, m.offset = "", 0
			m.busy = map[string]bool{}
		}
		m.snapshot = msg
		m.normalizeSelection()
		return m, m.waitSnapshot()

	case config.Change:
		if msg.Err != nil {
			m.configError = msg.Err.Error()
		} else {
			m.configError = ""
			if msg.Hash != m.fileHash {
				m.cfg, m.fileHash = msg.Config, msg.Hash
				m.backend.SetConfig(m.cfg)
				m.normalizeSelection()
			}
		}
		return m, m.waitConfig()

	case noticeMsg:
		m.noticeError = string(msg)
		return m, m.waitNotice()

	case tickMsg:
		m.refreshFrame++
		if time.Since(m.messageAt) > 8*time.Second {
			m.message = ""
		}
		return m, nextTick()

	case operationResult:
		if msg.generation != m.snapshot.Generation {
			return m, nil
		}
		if msg.releaseBusy {
			delete(m.busy, msg.id)
		}
		if msg.err != nil {
			m.setMessage(msg.err.Error())
		} else {
			m.setMessage(msg.message)
			if msg.acknowledge {
				if err := m.backend.MarkRead(msg.id, msg.sequence); err != nil {
					m.setMessage(err.Error())
				}
			}
		}
		if msg.copyFallback != "" {
			m.setMessage("Link sent to terminal clipboard")
			return m, tea.SetClipboard(msg.copyFallback)
		}

	case tea.KeyPressMsg:
		return m, m.handleKey(msg)

	case tea.MouseClickMsg:
		return m, m.handleClick(msg)

	case tea.MouseWheelMsg:
		if m.edit == "" {
			step := 3
			if msg.Button == tea.MouseWheelUp {
				step = -step
			}
			m.move(step)
		}
	}

	if m.edit != "" {
		return m, m.updateEditor(message)
	}
	return m, nil
}

func (m *Model) setMessage(value string) {
	m.message, m.messageAt = value, time.Now()
}

func (m *Model) visible() []model.Record {
	query := m.cfg.View.Search
	if m.edit == "search" {
		query = m.input.Value()
	}
	query = strings.ToLower(query)
	var rows []model.Record
	for _, record := range m.snapshot.Records {
		pr := record.PR
		match := m.cfg.View.Filter == "all" || pr.State == m.cfg.View.Filter || (m.cfg.View.Filter == "draft" && pr.State == "open" && pr.Draft)
		if !match {
			continue
		}

		haystack := fmt.Sprintf("%s %s #%d %s", pr.Title, pr.Repository, pr.Number, pr.Url)
		if query != "" && !strings.Contains(strings.ToLower(haystack), query) {
			continue
		}
		rows = append(rows, record)
	}

	sort.SliceStable(rows, func(i, j int) bool {
		a, b := sortTime(rows[i].PR, m.cfg.View.Sort), sortTime(rows[j].PR, m.cfg.View.Sort)
		if a.IsZero() != b.IsZero() {
			return !a.IsZero()
		}
		if !a.Equal(b) {
			if m.cfg.View.Descending {
				return a.After(b)
			}
			return a.Before(b)
		}

		if rows[i].PR.Repository == rows[j].PR.Repository {
			return rows[i].PR.Number < rows[j].PR.Number
		}
		return rows[i].PR.Repository < rows[j].PR.Repository
	})
	return rows
}

func sortTime(pr model.PR, field string) time.Time {
	switch field {
	case "created":
		return pr.CreatedAt
	case "ci":
		return pr.LastCI
	case "review":
		return pr.LastReview
	case "message":
		return pr.LastMessage
	default:
		return pr.UpdatedAt
	}
}

func (m *Model) normalizeSelection() {
	rows := m.visible()
	if len(rows) == 0 {
		m.selectedID, m.offset = "", 0
		return
	}

	index := slices.IndexFunc(rows, func(record model.Record) bool { return record.PR.ID == m.selectedID })
	if index < 0 {
		index, m.selectedID = 0, rows[0].PR.ID
	}

	page := max(1, m.height-9)
	m.offset = max(0, min(m.offset, index))
	if index >= m.offset+page {
		m.offset = index - page + 1
	}
}

func (m *Model) move(step int) {
	if m.settings {
		m.settingIndex = max(0, min(len(m.settingRows())-1, m.settingIndex+step))
		page := max(1, m.height-7)
		m.settingOffset = max(0, min(m.settingOffset, m.settingIndex))
		if m.settingIndex >= m.settingOffset+page {
			m.settingOffset = m.settingIndex - page + 1
		}
		return
	}

	rows := m.visible()
	if len(rows) == 0 {
		return
	}
	index := slices.IndexFunc(rows, func(record model.Record) bool { return record.PR.ID == m.selectedID })
	index = max(0, min(len(rows)-1, index+step))
	m.selectedID, m.focus = rows[index].PR.ID, -1
	m.normalizeSelection()
}

func (m *Model) selected() (model.Record, bool) {
	for _, record := range m.visible() {
		if record.PR.ID == m.selectedID {
			return record, true
		}
	}
	return model.Record{}, false
}
