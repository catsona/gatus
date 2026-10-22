package tui

import (
	"slices"

	"charm.land/lipgloss/v2"
)

func (m *Model) controlBar(actions []string, y int, compact bool) ([]string, []hitRegion) {
	var lines []string
	var regions []hitRegion
	line, x := "", 0
	for _, action := range actions {
		text := "[" + clean(m.hint(action)) + "]"
		if !compact {
			text += " " + m.controlLabel(action)
		} else {
			limit := max(3, (m.width+1)/4-1)
			if action == "sort_direction" {
				label := m.controlLabel(action)
				text = clip(text, max(1, limit-1-lipgloss.Width(label))) + " " + label
			} else {
				text = clip(text, limit)
			}
		}
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

		regions = append(regions, hitRegion{x: x, y: y + len(lines), width: width, action: action})
		if m.focus == slices.Index(controls, action) {
			line += highlight.Render(text)
		} else {
			line += accent.Render(text)
		}
		x += width
	}
	return append(lines, line), regions
}

func (m *Model) controlBars() ([]string, []string, []hitRegion) {
	top, topRegions := m.controlBar(globalControls, 2, false)
	bottom, bottomRegions := m.controlBar(prControls, 0, false)

	switch {
	case m.edit == "template":
		bottom = []string{muted.Render("ctrl+s save · enter new line · esc cancel")}
		bottomRegions = nil
	case m.edit != "":
		bottom = []string{muted.Render("enter save · esc cancel")}
		bottomRegions = nil
	case m.settings:
		bottom = []string{muted.Render("↑↓ select · enter/space edit or toggle · esc back · changes apply immediately")}
		bottomRegions = nil
	case m.menu != "":
		bottom = []string{muted.Render("↑↓ select · enter choose · esc back")}
		bottomRegions = nil
	}

	editorRows := 0
	if m.edit != "" {
		editorRows = 1
	}
	if !m.settings && len(top)+len(bottom)+5+editorRows > m.height {
		top, topRegions = m.controlBar(globalControls, 2, true)
		if m.edit == "" && m.menu == "" {
			bottom, bottomRegions = m.controlBar(prControls, 0, true)
		}
	}

	for index := range bottomRegions {
		bottomRegions[index].y += m.height - len(bottom)
	}
	return top, bottom, append(topRegions, bottomRegions...)
}

func (m *Model) tablePage() int {
	top, bottom, _ := m.controlBars()
	editorRows := 0
	if m.edit != "" {
		editorRows = 1
	}
	return max(0, m.height-2-len(top)-1-editorRows-1-len(bottom))
}
