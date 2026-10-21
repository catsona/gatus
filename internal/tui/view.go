package tui

import (
	"fmt"
	"strings"
	"time"
	"unicode"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"git.pegasko.art/catsona/gstatus/internal/model"
	"github.com/charmbracelet/x/ansi"
)

var (
	accent    = lipgloss.NewStyle().Foreground(lipgloss.Color("#78DCE8"))
	muted     = lipgloss.NewStyle().Foreground(lipgloss.Color("#8B949E"))
	bad       = lipgloss.NewStyle().Foreground(lipgloss.Color("#FF6B81"))
	good      = lipgloss.NewStyle().Foreground(lipgloss.Color("#A6E22E"))
	waiting   = lipgloss.NewStyle().Foreground(lipgloss.Color("#FFD866"))
	highlight = lipgloss.NewStyle().Background(lipgloss.Color("#263449")).Foreground(lipgloss.Color("#F8F8F2"))
)

type hitRegion struct {
	x, y, width int
	action, id  string
	index       int
}

// View renders the same layout used by mouse hit testing.
func (m *Model) View() tea.View {
	content, _ := m.layout()
	view := tea.NewView(content)
	view.AltScreen, view.MouseMode = true, tea.MouseModeCellMotion
	return view
}

func (m *Model) layout() (string, []hitRegion) {
	if m.width < 40 || m.height < 10 {
		return clip("Resize terminal: need 40 columns × 10 rows", max(0, m.width)), nil
	}

	login := "connecting to gh…"
	if m.snapshot.Account.Login != "" {
		login = "@" + m.snapshot.Account.Login
	}
	label := "gstatus"
	if m.settings {
		label += " / Settings"
	}
	lines := []string{accent.Bold(true).Render(label) + "  " + clean(login)}
	rows := m.visible()
	summary := fmt.Sprintf("%d visible · %d monitored · last update %s", len(rows), len(m.snapshot.Records), age(m.snapshot.UpdatedAt))
	if m.snapshot.Loading {
		summary = fmt.Sprintf("Updating %d/%d · %s", m.snapshot.Loaded, m.snapshot.Total, summary)
	}
	lines = append(lines, muted.Render(summary))
	var regions []hitRegion

	if m.settings {
		text := "[" + m.hint("back") + "] Back"
		regions = append(regions, hitRegion{x: 0, y: len(lines), width: lipgloss.Width(text), action: "back"})
		lines = append(lines, accent.Render(text))
	} else {
		line, x := "", 0
		for index, action := range controls {
			text := "[" + m.hint(action) + "] " + m.controlLabel(action)
			text = clip(text, m.width)
			width := lipgloss.Width(text)
			if x > 0 && x+width+1 > m.width {
				lines = append(lines, line)
				line, x = "", 0
			}
			if x > 0 {
				line += " "
				x++
			}
			regions = append(regions, hitRegion{x: x, y: len(lines), width: width, action: action})
			if m.focus == index {
				line += highlight.Render(text)
			} else {
				line += accent.Render(text)
			}
			x += width
		}
		lines = append(lines, line)
	}

	if m.edit != "" {
		if m.settings {
			lines = append(lines, accent.Render("Edit: "+strings.ReplaceAll(m.edit, "_", " ")))
		}
		if m.edit == "template" {
			lines = append(lines, strings.Split(m.area.View(), "\n")...)
		} else {
			lines = append(lines, m.input.View())
		}
	}

	switch {
	case m.menu != "":
		lines = append(lines, muted.Bold(true).Render(strings.ToUpper(m.menu[:1])+m.menu[1:]))
		options := m.menuOptions()
		available := max(0, m.height-len(lines)-2)
		start := max(0, m.menuIndex-available+1)
		for index := start; index < len(options) && index < start+available; index++ {
			text := "  " + options[index].label
			if index == m.menuIndex {
				text = highlight.Render("> " + options[index].label)
			}
			regions = append(regions, hitRegion{x: 0, y: len(lines), width: m.width, action: "menu", index: index})
			lines = append(lines, text)
		}

	case m.settings:
		lines = append(lines, muted.Bold(true).Render("Setting                         Value"))
		available := max(0, m.height-len(lines)-2)
		settings := m.settingRows()
		for index := m.settingOffset; index < len(settings) && index < m.settingOffset+available; index++ {
			row := settings[index]
			value := row.value
			if value == "" {
				value = "(not set)"
			}
			labelWidth := min(32, m.width/2)
			text := cell(row.label, labelWidth) + " " + clean(value)
			if index == m.settingIndex {
				text = highlight.Width(m.width).Render(clip(text, m.width))
			}
			regions = append(regions, hitRegion{x: 0, y: len(lines), width: m.width, action: "setting", index: index})
			lines = append(lines, text)
		}

	default:
		columns := m.columns()
		var heading []string
		for _, column := range columns {
			heading = append(heading, cell(column.label, column.width))
		}
		lines = append(lines, muted.Bold(true).Render(strings.Join(heading, " ")))
		available := max(0, m.height-len(lines)-2)
		for index := m.offset; index < len(rows) && index < m.offset+available; index++ {
			record := rows[index]
			var values []string
			for _, column := range columns {
				value := cell(column.value(record), column.width)
				if column.label == "CI" {
					value = ciStyle(record.PR.CIStatus).Render(value)
				}
				values = append(values, value)
			}
			text := strings.Join(values, " ")
			if record.PR.ID == m.selectedID {
				text = highlight.Render(text)
			} else if record.UnreadCount() == 0 {
				text = muted.Render(text)
			} else {
				text = lipgloss.NewStyle().Bold(true).Render(text)
			}
			regions = append(regions, hitRegion{x: 0, y: len(lines), width: m.width, action: "row", id: record.PR.ID})
			lines = append(lines, text)
		}
		if len(rows) == 0 && available > 0 {
			lines = append(lines, muted.Render("No PRs match. Change the search or state filter."))
		}
	}

	for len(lines) < m.height-2 {
		lines = append(lines, "")
	}
	if len(lines) > m.height-2 {
		lines = lines[:m.height-2]
	}

	status := m.message
	if m.noticeError != "" {
		status = "Webhook: " + m.noticeError
	}
	if m.snapshot.Error != "" {
		status = m.snapshot.Error
	}
	if m.configError != "" {
		status = "Config: " + m.configError
	}
	if m.conflict {
		status = "Config changed on disk. [l] load external / [w] overwrite this field"
	}
	lines = append(lines, waiting.Render(clean(status)))
	footer := fmt.Sprintf("%s web  %s copy  %s read  %s unread  %s failed CI  %s all CI  %s help  %s quit", m.hint("open"), m.hint("copy_url"), m.hint("mark_read"), m.hint("mark_unread"), m.hint("rerun_failed"), m.hint("rerun_all"), m.hint("help"), m.hint("quit"))
	if m.settings {
		footer = "↑↓ select · enter/space edit or toggle · esc back · changes apply immediately"
	}
	if m.edit != "" {
		footer = "enter save · esc cancel"
		if m.edit == "template" {
			footer = "ctrl+s save · enter new line · esc cancel"
		}
	}
	lines = append(lines, muted.Render(footer))
	for index := range lines {
		lines[index] = clip(lines[index], m.width)
	}
	return strings.Join(lines, "\n"), regions
}

type column struct {
	label string
	width int
	value func(model.Record) string
}

func (m *Model) columns() []column {
	columns := []column{
		{"", 2, func(record model.Record) string {
			for _, pair := range []struct{ key, label string }{{"thread.reply", "↩"}, {"review.submitted", "R"}, {"comment.created", "C"}} {
				if record.Unread[pair.key] > 0 {
					return pair.label
				}
			}
			if record.UnreadCount() > 0 {
				return "●"
			}
			return ""
		}},
		{"PR", 7, func(record model.Record) string { return fmt.Sprintf("#%d", record.PR.Number) }},
		{"Title", 0, func(record model.Record) string {
			title := record.PR.Title
			if m.width < 100 && record.PR.Repository != "" {
				title = record.PR.Repository + ": " + title
			}
			if m.width < 130 {
				if record.PR.Draft {
					title = "[draft] " + title
				} else if record.PR.State != "open" {
					title = "[" + record.PR.State + "] " + title
				}
			}
			return title
		}},
		{"CI", 9, func(record model.Record) string { return ciLabel(record.PR.CIStatus, record.PR.Complete) }},
	}
	if m.width >= 65 {
		columns = append(columns, column{"New", 12, newEvents})
	}
	if m.width >= 80 {
		columns = append(columns, column{"Review", 12, func(record model.Record) string { return record.PR.ReviewStatus }})
	}
	if m.width >= 100 {
		columns = append(columns, column{"Repository", 18, func(record model.Record) string { return record.PR.Repository }})
	}
	if m.width >= 130 {
		columns = append(columns,
			column{"State", 8, func(record model.Record) string {
				if record.PR.Draft {
					return "draft"
				}
				return record.PR.State
			}},
			column{"Activity", 7, func(record model.Record) string {
				when := record.PR.UpdatedAt
				for _, value := range []time.Time{record.PR.LastCI, record.PR.LastReview, record.PR.LastMessage} {
					if value.After(when) {
						when = value
					}
				}
				return age(when)
			}})
	}
	fixed := len(columns) - 1
	for _, column := range columns {
		fixed += column.width
	}
	columns[2].width = max(1, m.width-fixed)
	return columns
}

func newEvents(record model.Record) string {
	var parts []string
	for _, pair := range []struct{ key, label string }{{"thread.reply", "↩"}, {"comment.created", "C"}, {"review.submitted", "R"}} {
		if count := record.Unread[pair.key]; count > 0 {
			parts = append(parts, fmt.Sprintf("%s%d", pair.label, count))
		}
	}
	if record.Unread["ci.success"]+record.Unread["ci.failure"] > 0 {
		parts = append(parts, "CI")
	}
	return strings.Join(parts, " ")
}

func (m *Model) hint(action string) string {
	if keys := m.cfg.Keys[action]; len(keys) > 0 {
		return keys[0]
	}
	return "-"
}

func (m *Model) controlLabel(action string) string {
	switch action {
	case "search":
		if m.cfg.View.Search != "" {
			return "Search:" + clean(m.cfg.View.Search)
		}
		return "Search"
	case "filter":
		return m.cfg.View.Filter
	case "sort":
		direction := "↑"
		if m.cfg.View.Descending {
			direction = "↓"
		}
		return m.cfg.View.Sort + direction
	case "toggle_auto_refresh":
		if m.cfg.Refresh.Enabled {
			return "Auto:on"
		}
		return "Auto:off"
	case "refresh_interval":
		return time.Duration(m.cfg.Refresh.Interval).String()
	case "refresh":
		return "Refresh all"
	case "refresh_selected":
		return "Refresh PR"
	case "cancel_ci":
		return "Cancel CI"
	default:
		return "Settings"
	}
}

func ciLabel(value string, complete bool) string {
	if !complete {
		return "…"
	}
	labels := map[string]string{"success": "OK", "failure": "FAIL", "failing": "FAIL/RUN", "pending": "WAIT", "running": "RUN", "cancelled": "CANCEL", "skipped": "SKIP", "none": "-", "unknown": "?"}
	if label, exists := labels[value]; exists {
		return label
	}
	return "?"
}

func ciStyle(value string) lipgloss.Style {
	switch value {
	case "success":
		return good
	case "failure", "failing":
		return bad
	case "pending", "running":
		return waiting
	default:
		return muted
	}
}

func age(when time.Time) string {
	if when.IsZero() {
		return "-"
	}
	delta := time.Since(when)
	switch {
	case delta < time.Minute:
		return "now"
	case delta < time.Hour:
		return fmt.Sprintf("%dm", int(delta.Minutes()))
	case delta < 24*time.Hour:
		return fmt.Sprintf("%dh", int(delta.Hours()))
	default:
		return fmt.Sprintf("%dd", int(delta.Hours()/24))
	}
}

func clean(value string) string {
	return strings.Map(func(char rune) rune {
		if unicode.IsControl(char) {
			return ' '
		}
		return char
	}, ansi.Strip(value))
}

func clip(value string, width int) string {
	if width <= 0 {
		return ""
	}
	return ansi.Truncate(value, width, "…")
}

func cell(value string, width int) string {
	value = clip(clean(value), width)
	return value + strings.Repeat(" ", max(0, width-lipgloss.Width(value)))
}
