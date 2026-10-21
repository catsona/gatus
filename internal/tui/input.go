package tui

import (
	"errors"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"git.pegasko.art/catsona/gstatus/internal/config"
	"git.pegasko.art/catsona/gstatus/internal/desktop"
	"git.pegasko.art/catsona/gstatus/internal/github"
)

var (
	globalControls = []string{"search", "filter", "sort", "toggle_auto_refresh", "refresh_interval", "refresh", "settings", "help", "quit"}
	prControls     = []string{"open", "copy_url", "mark_read", "mark_unread", "refresh_selected", "rerun_failed", "rerun_all", "cancel_ci"}
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
			m.menu = ""
		case "up":
			m.menuIndex = max(0, m.menuIndex-1)
		case "down":
			m.menuIndex = min(len(m.menuOptions())-1, m.menuIndex+1)
		case "open":
			m.applyMenu()
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
		m.settings, m.menu = false, ""
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
			m.applyMenu()
		case "back":
			m.settings, m.menu = false, ""
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
	default:
		var result []menuOption
		for _, action := range []string{"open", "copy_url", "mark_read", "mark_unread", "rerun_all", "rerun_failed", "cancel_ci", "search", "filter", "sort", "refresh_selected", "refresh", "toggle_auto_refresh", "refresh_interval", "settings", "quit"} {
			result = append(result, menuOption{value: action, label: strings.Join(m.cfg.Keys[action], ", ") + "  " + actionLabel(action)})
		}
		return result
	}
}

func (m *Model) applyMenu() {
	if m.menu == "help" {
		m.menu = ""
		return
	}

	options := m.menuOptions()
	if m.menuIndex < 0 || m.menuIndex >= len(options) {
		return
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
}
