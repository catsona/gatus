package tui

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"git.pegasko.art/catsona/gstatus/internal/config"
	"git.pegasko.art/catsona/gstatus/internal/desktop"
	"git.pegasko.art/catsona/gstatus/internal/github"
	"git.pegasko.art/catsona/gstatus/internal/herdr"
	"git.pegasko.art/catsona/gstatus/internal/model"
)

var (
	globalControls = []string{"search", "filter", "sort", "sort_direction", "toggle_auto_refresh", "refresh_interval", "refresh", "settings", "help", "quit"}
	prControls     = []string{"open", "open_herdr", "attach_herdr", "create_herdr", "copy_url", "mark_read", "mark_unread", "refresh_selected", "rerun_failed", "rerun_all", "cancel_ci"}
	controls       = append(append([]string{}, globalControls...), prControls...)
)

func (m *Model) action(key tea.KeyPressMsg) string {
	for action, bindings := range m.cfg.Keys {
		for _, binding := range bindings {
			canonical := config.CanonicalKey(binding)
			if canonical == config.CanonicalKey(key.String()) || canonical == config.CanonicalKey(key.Keystroke()) {
				return action
			}
		}
	}
	return ""
}

func (m *Model) handleKey(key tea.KeyPressMsg) tea.Cmd {
	defer m.normalizeSelection()

	value := key.String()
	if value == "ctrl+c" {
		return tea.Quit
	}

	if m.conflict {
		switch value {
		case "w":
			m.saveEditor(true)
		case "l", "esc":
			m.closeEditor()
		}
		return nil
	}

	if m.edit != "" {
		switch value {
		case "esc":
			m.closeEditor()
			return nil
		case "ctrl+s", "enter":
			if m.edit != "template" || value == "ctrl+s" {
				m.saveEditor(false)
				return nil
			}
		}
		cmd := m.updateEditor(key)
		m.normalizeSelection()
		return cmd
	}

	action := m.action(key)
	if action == "quit" {
		return tea.Quit
	}

	if m.menu != "" {
		switch action {
		case "back":
			m.closeMenu()
		case "up":
			m.menuIndex = max(0, m.menuIndex-1)
		case "down":
			m.menuIndex = min(len(m.menuOptions())-1, m.menuIndex+1)
		case "open":
			return m.applyMenu()
		}
		return nil
	}

	if m.settings && action != "up" && action != "down" && action != "back" && action != "open" && action != "help" && action != "settings" {
		if value == "space" {
			return m.activateSetting()
		}
		return nil
	}

	switch action {
	case "up":
		m.move(-1)
	case "down":
		m.move(1)
	case "focus_next":
		m.focus++
		if m.focus >= len(controls) {
			m.focus = -1
		}
	case "focus_previous":
		m.focus--
		if m.focus < -1 {
			m.focus = len(controls) - 1
		}
	case "back":
		m.settings = false
		m.closeMenu()
	case "open":
		if m.settings {
			return m.activateSetting()
		}
		if m.focus >= 0 {
			return m.perform(controls[m.focus])
		}
		return m.perform("open")
	default:
		if value == "space" && m.settings {
			return m.activateSetting()
		}
		if value == "left" || value == "right" {
			step := 1
			if value == "left" {
				step = -1
			}
			m.focus = (m.focus + step + len(controls)) % len(controls)
			return nil
		}
		if action != "" {
			return m.perform(action)
		}
	}
	return nil
}

func (m *Model) perform(action string) tea.Cmd {
	switch action {
	case "quit":
		return tea.Quit
	case "refresh":
		m.backend.RequestRefresh()
		m.setMessage("Refresh requested")
	case "search":
		return m.startEditor("search", m.cfg.View.Search)
	case "refresh_interval":
		return m.startEditor("interval", time.Duration(m.cfg.Refresh.Interval).String())
	case "sort_direction":
		cfg := m.cfg
		cfg.View.Descending = !cfg.View.Descending
		m.saveConfig(cfg, false)
	case "filter", "sort", "help":
		m.menu, m.menuIndex = action, 0
		current := m.cfg.View.Filter
		if action == "sort" {
			current = m.cfg.View.Sort
		}
		for index, option := range m.menuOptions() {
			if option.value == current {
				m.menuIndex = index
			}
		}
	case "toggle_auto_refresh":
		cfg := m.cfg
		cfg.Refresh.Enabled = !cfg.Refresh.Enabled
		m.saveConfig(cfg, false)
	case "settings":
		m.settings, m.menu = true, ""
	case "open_herdr":
		return m.openHerdrSpace()
	case "attach_herdr":
		return m.listHerdrSpaces()
	case "create_herdr":
		return m.createHerdrSpace()
	case "mark_read", "mark_unread":
		record, ok := m.selected()
		if !ok {
			return nil
		}
		var err error
		if action == "mark_read" {
			err = m.backend.MarkRead(record.PR.ID, record.Sequence)
		} else {
			err = m.backend.MarkUnread(record.PR.ID)
		}
		if err != nil {
			m.setMessage(err.Error())
		}
	case "open", "copy_url", "rerun_all", "rerun_failed", "cancel_ci", "refresh_selected":
		record, ok := m.selected()
		if !ok {
			return nil
		}

		backend, system, ctx := m.backend, m.desktop, m.ctx
		id, address, sequence, generation := record.PR.ID, record.PR.Url, record.Sequence, m.snapshot.Generation
		markOnOpen := m.cfg.Read.MarkOnOpen
		if strings.HasPrefix(action, "rerun_") || action == "cancel_ci" || action == "refresh_selected" {
			if m.busy[id] {
				return nil
			}
			m.busy[id] = true
			m.setMessage("Requesting CI rerun…")
			if action == "cancel_ci" {
				m.setMessage("Requesting CI cancellation…")
			}
			if action == "refresh_selected" {
				m.setMessage("Refreshing selected PR…")
			}
		}

		return func() tea.Msg {
			result := operationResult{generation: generation, id: id, sequence: sequence}
			switch action {
			case "open":
				result.err = system.Open(ctx, address)
				result.acknowledge = markOnOpen
				result.message = "Opened in browser"
			case "copy_url":
				result.err = system.Copy(ctx, address)
				result.message = "Link copied"
				if errors.Is(result.err, desktop.ErrNoClipboard) {
					result.err, result.copyFallback = nil, address
				}
			case "refresh_selected":
				result.releaseBusy = true
				result.err = backend.RefreshPR(ctx, id)
				result.message = "Selected PR refreshed"
			default:
				result.releaseBusy = true
				var rerun github.Result
				var err error
				if action == "cancel_ci" {
					rerun, err = backend.Cancel(ctx, id)
				} else {
					rerun, err = backend.Rerun(ctx, id, action == "rerun_failed")
				}
				result.err = err
				result.message = fmt.Sprintf("CI: %d rerun requests accepted", rerun.Accepted)
				if action == "cancel_ci" {
					result.message = fmt.Sprintf("CI: %d cancellation requests accepted", rerun.Accepted)
				}
				if len(rerun.Failures) > 0 {
					result.message += "; " + strings.Join(rerun.Failures, "; ")
				}
			}
			return result
		}
	}

	return nil
}

func (m *Model) listHerdrSpaces() tea.Cmd {
	record, ok := m.selected()
	if !ok {
		return nil
	}
	if !m.cfg.Herdr.Enabled {
		m.setMessage("Herdr integration is disabled")
		return nil
	}
	if m.herdr == nil || m.links == nil {
		m.setMessage("Herdr integration is unavailable")
		return nil
	}
	if m.busy[record.PR.ID] {
		return nil
	}

	m.busy[record.PR.ID] = true
	m.herdrID, m.herdrPRURL = record.PR.ID, record.PR.Url
	m.setMessage("Loading Herdr spaces…")
	service, ctx := m.herdr, m.ctx
	generation := m.snapshot.Generation
	id := record.PR.ID
	return func() tea.Msg {
		spaces, err := service.List(ctx)
		return operationResult{generation: generation, id: id, herdrMode: "list", herdrSpaces: spaces, err: err}
	}
}

func (m *Model) openHerdrSpace() tea.Cmd {
	record, ok := m.selected()
	if !ok {
		return nil
	}
	if !m.cfg.Herdr.Enabled {
		m.setMessage("Herdr integration is disabled")
		return nil
	}
	if m.herdr == nil || m.links == nil {
		m.setMessage("Herdr integration is unavailable")
		return nil
	}
	link, linked, err := m.links.HerdrLink(record.PR.Url)
	if err != nil {
		m.setMessage(err.Error())
		return nil
	}
	if !linked || link.WorkspaceID == "" {
		m.setMessage("PR is not linked to a Herdr space")
		return nil
	}
	if m.busy[record.PR.ID] {
		return nil
	}

	m.busy[record.PR.ID] = true
	m.setMessage("Opening linked Herdr space…")
	service, ctx := m.herdr, m.ctx
	generation := m.snapshot.Generation
	id := record.PR.ID
	workspaceID := link.WorkspaceID
	return func() tea.Msg {
		err := service.Focus(ctx, workspaceID)
		message := "Opened linked Herdr space"
		if err != nil && herdr.IsWorkspaceNotFound(err) {
			err = errors.New("linked Herdr space is closed")
		}
		return operationResult{generation: generation, id: id, herdrMode: "open", releaseBusy: true, message: message, err: err}
	}
}

func (m *Model) createHerdrSpace() tea.Cmd {
	record, ok := m.selected()
	if !ok {
		return nil
	}
	if !m.cfg.Herdr.Enabled {
		m.setMessage("Herdr integration is disabled")
		return nil
	}
	if m.herdr == nil || m.links == nil {
		m.setMessage("Herdr integration is unavailable")
		return nil
	}
	if m.busy[record.PR.ID] {
		return nil
	}

	m.busy[record.PR.ID] = true
	m.setMessage("Creating Herdr space…")
	request := herdr.Request{Root: m.cfg.Herdr.Root, Label: fmt.Sprintf("PR #%d - %s", record.PR.Number, record.PR.Repository)}
	service, links, ctx := m.herdr, m.links, m.ctx
	generation := m.snapshot.Generation
	id := record.PR.ID
	return func() tea.Msg {
		space, err := service.Create(ctx, request)
		if err == nil {
			err = links.SaveHerdrLink(model.HerdrLink{PRURL: record.PR.Url, WorkspaceID: space.ID, Label: space.Label, Root: space.Root})
		}
		return operationResult{generation: generation, id: id, herdrMode: "create", releaseBusy: true, herdrSpace: space, message: "Created and linked Herdr space", err: err}
	}
}

func (m *Model) handleHerdrResult(result operationResult) tea.Cmd {
	if result.herdrMode == "list" {
		if result.err != nil {
			delete(m.busy, result.id)
			m.setMessage(result.err.Error())
			return nil
		}
		m.herdrSpaces = result.herdrSpaces
		m.menu, m.menuIndex = "herdr", 0
		if link, ok, err := m.links.HerdrLink(m.herdrPRURL); err != nil {
			delete(m.busy, result.id)
			m.setMessage(err.Error())
			return nil
		} else if ok {
			for index, space := range m.herdrSpaces {
				if space.ID == link.WorkspaceID {
					m.menuIndex = index
					break
				}
			}
			if !slices.ContainsFunc(m.herdrSpaces, func(space herdr.Workspace) bool { return space.ID == link.WorkspaceID }) {
				m.setMessage("Linked Herdr space is unavailable; choose a replacement")
			}
		}
		if len(m.herdrSpaces) == 0 {
			m.setMessage("No live Herdr spaces")
		}
		return nil
	}

	delete(m.busy, result.id)
	if result.err != nil {
		m.setMessage(result.err.Error())
		return nil
	}
	m.setMessage(result.message)
	return nil
}

func (m *Model) closeMenu() {
	if m.menu == "herdr" {
		delete(m.busy, m.herdrID)
		m.herdrSpaces = nil
		m.herdrID, m.herdrPRURL = "", ""
	}
	m.menu = ""
}

func (m *Model) handleClick(mouse tea.MouseClickMsg) tea.Cmd {
	defer m.normalizeSelection()

	if m.edit != "" {
		return m.updateEditor(mouse)
	}
	if mouse.Button != tea.MouseLeft {
		return nil
	}

	_, regions := m.layout()
	for _, region := range regions {
		if mouse.Y != region.y || mouse.X < region.x || mouse.X >= region.x+region.width {
			continue
		}

		switch region.action {
		case "row":
			m.selectedID, m.focus = region.id, -1
			if m.lastClickID == region.id && time.Since(m.lastClickAt) < 350*time.Millisecond {
				m.lastClickID = ""
				return m.perform("open")
			}
			m.lastClickID, m.lastClickAt = region.id, time.Now()
		case "setting":
			m.settingIndex = region.index
			return m.activateSetting()
		case "menu":
			m.menuIndex = region.index
			return m.applyMenu()
		case "back":
			m.settings = false
			m.closeMenu()
		default:
			return m.perform(region.action)
		}
	}
	return nil
}

type menuOption struct{ value, label string }

func (m *Model) menuOptions() []menuOption {
	switch m.menu {
	case "filter":
		return []menuOption{{"all", "All"}, {"open", "Open (includes drafts)"}, {"closed", "Closed without merge"}, {"draft", "Draft"}, {"merged", "Merged"}}
	case "sort":
		return []menuOption{{"created", "Created"}, {"updated", "PR updated"}, {"ci", "Last CI"}, {"review", "Last review"}, {"message", "Last message"}, {"reverse", "Reverse order"}}
	case "herdr":
		options := make([]menuOption, 0, len(m.herdrSpaces))
		for _, space := range m.herdrSpaces {
			label := space.Label
			if label == "" {
				label = space.ID
			}
			if space.Root != "" {
				label += " · " + space.Root
			}
			options = append(options, menuOption{value: space.ID, label: label})
		}
		return options
	default:
		var result []menuOption
		for _, action := range []string{"open", "open_herdr", "attach_herdr", "create_herdr", "copy_url", "mark_read", "mark_unread", "rerun_all", "rerun_failed", "cancel_ci", "search", "filter", "sort", "sort_direction", "refresh_selected", "refresh", "toggle_auto_refresh", "refresh_interval", "settings", "quit"} {
			result = append(result, menuOption{value: action, label: strings.Join(m.cfg.Keys[action], ", ") + "  " + actionLabel(action)})
		}
		return result
	}
}

func (m *Model) applyMenu() tea.Cmd {
	if m.menu == "help" {
		m.menu = ""
		return nil
	}

	options := m.menuOptions()
	if m.menuIndex < 0 || m.menuIndex >= len(options) {
		return nil
	}
	if m.menu == "herdr" {
		space := m.herdrSpaces[m.menuIndex]
		link := model.HerdrLink{PRURL: m.herdrPRURL, WorkspaceID: space.ID, Label: space.Label, Root: space.Root}
		service, links, ctx := m.herdr, m.links, m.ctx
		generation, id := m.snapshot.Generation, m.herdrID
		m.closeMenu()
		m.setMessage("Attaching Herdr space…")
		return func() tea.Msg {
			err := service.Focus(ctx, space.ID)
			if err == nil {
				err = links.SaveHerdrLink(link)
			}
			return operationResult{generation: generation, id: id, herdrMode: "focus", releaseBusy: true, message: "Attached and focused Herdr space", err: err}
		}
	}
	cfg := m.cfg
	value := options[m.menuIndex].value
	if m.menu == "filter" {
		cfg.View.Filter = value
	} else if value == "reverse" {
		cfg.View.Descending = !cfg.View.Descending
	} else {
		cfg.View.Sort = value
	}

	if m.saveConfig(cfg, false) {
		m.menu = ""
	}
	return nil
}
