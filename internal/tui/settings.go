package tui

import (
	"crypto/sha256"
	"fmt"
	"maps"
	"os"
	"slices"
	"sort"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"git.pegasko.art/catsona/gstatus/internal/config"
	"go.yaml.in/yaml/v3"
)

type settingRow struct {
	id, label, value string
	toggle           bool
}

func (m *Model) settingRows() []settingRow {
	repositories := strings.Join(m.cfg.Repositories, ", ")
	if repositories == "" {
		repositories = "all repositories"
	}
	rows := []settingRow{
		{"repositories", "Repositories", repositories, false},
		{"mark_on_open", "Mark read when opening", checked(m.cfg.Read.MarkOnOpen), true},
		{"notify_enabled", "Webhook notifications", checked(m.cfg.Notifications.Enabled), true},
		{"webhook_url", "Webhook URL", m.cfg.Notifications.WebhookURL, false},
		{"template", "Message template", m.cfg.Notifications.Template, false},
		{"herdr_enabled", "Herdr spaces", checked(m.cfg.Herdr.Enabled), true},
		{"herdr_root", "Herdr root", m.cfg.Herdr.Root, false},
	}

	for _, event := range config.Default().Notifications.Events {
		rows = append(rows, settingRow{"event:" + event, "Notify: " + eventLabel(event), checked(slices.Contains(m.cfg.Notifications.Events, event)), true})
	}

	var actions []string
	for action := range m.cfg.Keys {
		actions = append(actions, action)
	}
	sort.Strings(actions)
	for _, action := range actions {
		rows = append(rows, settingRow{"key:" + action, "Key: " + actionLabel(action), strings.Join(m.cfg.Keys[action], ", "), false})
	}
	return rows
}

func checked(value bool) string {
	if value {
		return "[x]"
	}
	return "[ ]"
}

func (m *Model) activateSetting() tea.Cmd {
	rows := m.settingRows()
	if m.settingIndex < 0 || m.settingIndex >= len(rows) {
		return nil
	}
	row := rows[m.settingIndex]
	cfg := m.editableConfig()
	if row.toggle {
		switch row.id {
		case "mark_on_open":
			cfg.Read.MarkOnOpen = !cfg.Read.MarkOnOpen
		case "herdr_enabled":
			cfg.Herdr.Enabled = !cfg.Herdr.Enabled
		case "notify_enabled":
			cfg.Notifications.Enabled = !cfg.Notifications.Enabled
		default:
			event := strings.TrimPrefix(row.id, "event:")
			index := slices.Index(cfg.Notifications.Events, event)
			if index < 0 {
				cfg.Notifications.Events = append(cfg.Notifications.Events, event)
			} else {
				cfg.Notifications.Events = slices.Delete(cfg.Notifications.Events, index, index+1)
			}
		}
		m.saveConfig(cfg, false)
		return nil
	}

	value := row.value
	if row.id == "repositories" {
		value = strings.Join(m.cfg.Repositories, ", ")
	}
	return m.startEditor(row.id, value)
}

func (m *Model) editableConfig() config.Config {
	cfg := m.cfg
	cfg.Keys = maps.Clone(m.cfg.Keys)
	cfg.Notifications.Events = slices.Clone(m.cfg.Notifications.Events)
	return cfg
}

func (m *Model) startEditor(field, value string) tea.Cmd {
	m.edit, m.editHash, m.conflict = field, m.fileHash, false
	if field == "template" {
		m.area.SetValue(value)
		return m.area.Focus()
	}

	m.input.SetValue(value)
	m.input.CursorEnd()
	return m.input.Focus()
}

func (m *Model) closeEditor() {
	m.edit, m.conflict = "", false
	m.input.Blur()
	m.area.Blur()
	m.normalizeSelection()
}

func (m *Model) updateEditor(message tea.Msg) tea.Cmd {
	var cmd tea.Cmd
	if m.edit == "template" {
		m.area, cmd = m.area.Update(message)
	} else {
		m.input, cmd = m.input.Update(message)
	}
	return cmd
}

func splitValues(value string) []string {
	return strings.FieldsFunc(value, func(char rune) bool { return char == ',' || char == '\n' || char == ' ' })
}

func (m *Model) saveEditor(force bool) {
	data, err := os.ReadFile(m.path)
	if err != nil {
		m.setMessage(err.Error())
		return
	}
	if !force && sha256.Sum256(data) != m.editHash {
		m.conflict = true
		return
	}

	cfg, err := config.Parse(data)
	if err != nil {
		m.setMessage(err.Error())
		return
	}
	value := m.input.Value()
	switch m.edit {
	case "search":
		cfg.View.Search = value
	case "interval":
		interval, err := time.ParseDuration(strings.TrimSpace(value))
		if err != nil {
			m.setMessage("Use a duration such as 30s or 1m")
			return
		}
		cfg.Refresh.Interval = config.Duration(interval)
	case "repositories":
		cfg.Repositories = splitValues(value)
	case "herdr_root":
		cfg.Herdr.Root = strings.TrimSpace(value)
	case "webhook_url":
		cfg.Notifications.WebhookURL = strings.TrimSpace(value)
	case "template":
		cfg.Notifications.Template = m.area.Value()
	default:
		cfg.Keys[strings.TrimPrefix(m.edit, "key:")] = splitValues(value)
	}

	if m.saveConfig(cfg, true) {
		m.closeEditor()
	}
}

func (m *Model) saveConfig(cfg config.Config, force bool) bool {
	if !force {
		data, err := os.ReadFile(m.path)
		if err != nil || sha256.Sum256(data) != m.fileHash {
			m.setMessage("Config changed on disk; wait for reload before saving")
			return false
		}
	}

	if err := config.Save(m.path, cfg); err != nil {
		m.setMessage(err.Error())
		return false
	}

	data, err := yaml.Marshal(cfg)
	if err != nil {
		m.setMessage(fmt.Sprintf("encode settings: %v", err))
		return false
	}
	m.cfg, m.fileHash, m.configError = cfg, sha256.Sum256(data), ""
	m.backend.SetConfig(cfg)
	m.normalizeSelection()
	m.setMessage("Settings saved")
	return true
}

func actionLabel(action string) string {
	return strings.ReplaceAll(action, "_", " ")
}

func eventLabel(event string) string {
	labels := map[string]string{
		"comment.created": "comments", "thread.reply": "replies", "review.submitted": "reviews",
		"ci.success": "CI passed", "ci.failure": "CI failed", "pr.state_changed": "PR state", "pr.created": "new PR",
	}
	return labels[event]
}
