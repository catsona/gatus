package tui

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"git.pegasko.art/catsona/gstatus/internal/config"
	"git.pegasko.art/catsona/gstatus/internal/model"
	"github.com/charmbracelet/x/ansi"
)

func TestControlsStayOnTheirSideOfThePRList(t *testing.T) {
	for _, scenario := range []struct {
		name          string
		width, height int
		longKeys      bool
	}{
		{"wide", 140, 24, false},
		{"medium", 80, 20, false},
		{"narrow", 40, 20, false},
		{"short", 40, 10, false},
		{"long shortcuts", 40, 10, true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			size := [2]int{scenario.width, scenario.height}
			cfg := config.Default()
			if scenario.longKeys {
				for index, action := range controls {
					cfg.Keys[action] = []string{fmt.Sprintf("ctrl+alt+shift+super+f%d", index+1)}
				}
			}
			m, _, _ := testModel(t, cfg)
			m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
			view, regions := m.layout()
			firstRow, lastRow := size[1], -1
			for _, region := range regions {
				if region.action == "row" {
					firstRow = min(firstRow, region.y)
					lastRow = max(lastRow, region.y)
				}
			}
			if lastRow < 0 {
				t.Fatalf("no PR rows fit: %s", ansi.Strip(view))
			}

			global := []string{"search", "filter", "sort", "toggle_auto_refresh", "refresh_interval", "refresh", "settings", "help", "quit"}
			selected := []string{"open", "copy_url", "mark_read", "mark_unread", "refresh_selected", "rerun_failed", "rerun_all", "cancel_ci"}
			seen := map[string]bool{}
			for _, region := range regions {
				if slices.Contains(global, region.action) && region.y >= firstRow {
					t.Errorf("global control %s is below the header", region.action)
				}
				if slices.Contains(selected, region.action) && region.y <= lastRow {
					t.Errorf("PR control %s is above the footer", region.action)
				}
				if region.y >= size[1] || region.x+region.width > size[0] {
					t.Errorf("control is outside the terminal: %+v", region)
				}
				seen[region.action] = true
			}
			for _, action := range append(global, selected...) {
				if !seen[action] {
					t.Errorf("missing clickable control %s", action)
				}
			}
		})
	}
}

func TestFooterMouseActionsUseTheSelectedPR(t *testing.T) {
	for _, action := range []string{"refresh_selected", "cancel_ci", "copy_url", "quit"} {
		t.Run(action, func(t *testing.T) {
			m, backend, desktop := testModel(t, config.Default())
			press(m, 'j')
			_, regions := m.layout()
			index := slices.IndexFunc(regions, func(region hitRegion) bool { return region.action == action })
			if index < 0 {
				t.Fatalf("missing mouse control %s", action)
			}
			region := regions[index]
			_, command := m.Update(tea.MouseClickMsg{X: region.x, Y: region.y, Button: tea.MouseLeft})
			if command == nil {
				t.Fatal("click did not start an action")
			}
			message := command()
			if action == "quit" {
				if _, ok := message.(tea.QuitMsg); !ok {
					t.Fatalf("quit returned %T", message)
				}
				return
			}
			m.Update(message)
			if action == "refresh_selected" && !slices.Equal(backend.refreshed, []string{"a"}) {
				t.Fatalf("wrong refresh target: %v", backend.refreshed)
			}
			if action == "cancel_ci" && !slices.Equal(backend.cancelled, []string{"a"}) {
				t.Fatalf("wrong cancellation target: %v", backend.cancelled)
			}
			if action == "copy_url" && !strings.HasSuffix(desktop.copied, "/1") {
				t.Fatalf("wrong copied PR: %s", desktop.copied)
			}
			if len(backend.marked) != 0 || backend.refreshAll != 0 {
				t.Fatal("PR action acknowledged the PR or requested a full refresh")
			}
		})
	}
}

func TestShortTerminalKeepsMenusAndSearchVisible(t *testing.T) {
	for _, action := range []string{"filter", "help", "search"} {
		t.Run(action, func(t *testing.T) {
			cfg := config.Default()
			for index, control := range controls {
				cfg.Keys[control] = []string{fmt.Sprintf("ctrl+alt+shift+super+f%d", index+1)}
			}
			m, _, _ := testModel(t, cfg)
			m.Update(tea.WindowSizeMsg{Width: 40, Height: 10})
			_, regions := m.layout()
			index := slices.IndexFunc(regions, func(region hitRegion) bool { return region.action == action })
			if index < 0 {
				t.Fatalf("missing control %s", action)
			}
			region := regions[index]
			m.Update(tea.MouseClickMsg{X: region.x, Y: region.y, Button: tea.MouseLeft})
			view, regions := m.layout()
			text := ansi.Strip(view)
			if action == "search" {
				if !strings.Contains(text, "> ") || !strings.Contains(text, "enter save") {
					t.Fatalf("search input was hidden: %s", text)
				}
			} else if !slices.ContainsFunc(regions, func(region hitRegion) bool { return region.action == "menu" && region.index == m.menuIndex }) {
				t.Fatalf("selected menu option was hidden: %s", text)
			}

			if slices.ContainsFunc(regions, func(region hitRegion) bool { return region.action == "cancel_ci" }) {
				t.Fatal("PR actions are still clickable while a menu or editor is open")
			}
		})
	}
}

func TestTabReachesFooterAndEnterRunsItsAction(t *testing.T) {
	m, backend, _ := testModel(t, config.Default())
	for steps := 0; steps <= len(controls); steps++ {
		m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
		if m.focus >= 0 && controls[m.focus] == "cancel_ci" {
			_, command := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
			if command == nil {
				t.Fatal("focused cancellation did not start")
			}
			m.Update(command())
			if !slices.Equal(backend.cancelled, []string{"b"}) {
				t.Fatalf("wrong keyboard cancellation target: %v", backend.cancelled)
			}
			return
		}
	}
	t.Fatal("Tab never reached the cancellation button")
}

func TestSelectionStaysVisibleWhenControlBarsWrap(t *testing.T) {
	m, backend, _ := testModel(t, config.Default())
	for index := 0; index < 30; index++ {
		backend.snapshot.Records = append(backend.snapshot.Records, model.Record{PR: model.PR{
			ID: fmt.Sprint(index), Title: fmt.Sprintf("Pull request %d", index),
			State: "open", Complete: true, UpdatedAt: time.Unix(int64(index+10), 0),
		}})
	}
	m.Update(backend.snapshot)
	for _, size := range [][2]int{{80, 12}, {40, 10}, {140, 24}} {
		m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		m.selectedID, m.offset = "29", 0
		m.normalizeSelection()
		press(m, 's')
		press(m, 'j')
		m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
		_, sortedRegions := m.layout()
		if !slices.ContainsFunc(sortedRegions, func(region hitRegion) bool { return region.action == "row" && region.id == m.selectedID }) {
			t.Fatalf("applying a sort hid the selected PR at %v", size)
		}
		for step := 0; step < 35; step++ {
			press(m, 'j')
			_, regions := m.layout()
			if !slices.ContainsFunc(regions, func(region hitRegion) bool { return region.action == "row" && region.id == m.selectedID }) {
				t.Fatalf("selected PR %s is outside the visible rows at %v", m.selectedID, size)
			}
		}

		press(m, '?')
		press(m, 'k')
		m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
		_, regions := m.layout()
		if !slices.ContainsFunc(regions, func(region hitRegion) bool { return region.action == "row" && region.id == m.selectedID }) {
			t.Fatalf("closing help hid the selected PR at %v", size)
		}
	}
}
