package tui

import (
	"fmt"
	"image/color"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/catsona/gstatus/internal/config"
	"github.com/catsona/gstatus/internal/monitor"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
)

func TestSelectionBackgroundCoversEveryCellAfterColoredStatuses(t *testing.T) {
	for _, width := range []int{40, 80, 160} {
		for _, refresh := range []monitor.RefreshState{monitor.RefreshIdle, monitor.RefreshQueued, monitor.RefreshRunning} {
			t.Run(fmt.Sprintf("width=%d/refresh=%s", width, refresh), func(t *testing.T) {
				cfg := config.Default()
				cfg.View.Filter = "all"
				m, backend, _ := testModel(t, cfg)
				backend.snapshot.Records[1].PR.State = "merged"
				backend.snapshot.Records[1].PR.CIStatus = "success"
				backend.snapshot.Records[1].PR.ReviewStatus = "approved"
				backend.snapshot.RefreshStates = map[string]monitor.RefreshState{"b": refresh}
				m.Update(backend.snapshot)
				m.Update(tea.WindowSizeMsg{Width: width, Height: 24})

				view, regions := m.layout()
				lines := uv.NewStyledString(view).Lines(ansi.GraphemeWidth)
				selectedRows := 0
				for _, region := range regions {
					if region.action != "row" {
						continue
					}
					line := lines[region.y]
					if region.id != "b" {
						for x, cell := range line {
							if cell.Style.Bg != nil {
								t.Fatalf("selection background leaked into unselected PR at column %d", x)
							}
						}
						continue
					}

					selectedRows++
					if len(line) != width || line[0].Style.Bg == nil {
						t.Fatalf("selected row has no full-width background: cells=%d", len(line))
					}
					background := color.NRGBAModel.Convert(line[0].Style.Bg)
					for x, cell := range line {
						if cell.Style.Bg == nil || color.NRGBAModel.Convert(cell.Style.Bg) != background {
							t.Fatalf("selection background stops at column %d (%q)", x, cell.Content)
						}
					}
				}
				if selectedRows != 1 {
					t.Fatal("selected PR was not rendered")
				}
			})
		}
	}
}

func TestSelectedRowKeepsStatusForegroundColors(t *testing.T) {
	for _, width := range []int{80, 160} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			cfg := config.Default()
			cfg.View.Filter = "all"
			m, backend, _ := testModel(t, cfg)
			backend.snapshot.Records[1].PR.State = "merged"
			backend.snapshot.Records[1].PR.CIStatus = "success"
			backend.snapshot.Records[1].PR.ReviewStatus = "approved"
			m.Update(backend.snapshot)
			m.Update(tea.WindowSizeMsg{Width: width, Height: 24})

			view, regions := m.layout()
			lines := strings.Split(view, "\n")
			for _, region := range regions {
				if region.action != "row" || region.id != "b" {
					continue
				}
				line := uv.NewStyledString(lines[region.y]).Lines(ansi.GraphemeWidth)[0]
				plain := ansi.Strip(lines[region.y])
				foreground := func(text string) color.Color {
					t.Helper()
					index := strings.Index(plain, text)
					if index < 0 {
						t.Fatalf("missing selected row text %q", text)
					}
					value := line[ansi.StringWidth(plain[:index])].Style.Fg
					if value == nil {
						t.Fatalf("missing foreground on %q", text)
					}
					return color.NRGBAModel.Convert(value)
				}
				title, ci, review, state := foreground("Beta"), foreground("OK"), foreground("approved"), foreground("merged")
				if ci == title || review != ci || state == title || state == ci {
					t.Fatal("selection lost CI, review or PR state colors")
				}
				return
			}
			t.Fatal("selected PR was not rendered")
		})
	}
}

func TestBranchStatusIsVisibleWithoutBreakingSelection(t *testing.T) {
	for _, width := range []int{40, 80, 160} {
		for _, scenario := range []struct{ status, label, compact string }{
			{"conflict", "conflict", "!!"},
			{"ok", "ok", "ok"},
			{"unknown", "unknown", "?"},
			{"", "unknown", "?"},
		} {
			t.Run(fmt.Sprintf("%d/%s", width, scenario.status), func(t *testing.T) {
				m, backend, _ := testModel(t, config.Default())
				backend.snapshot.Records[1].PR.BranchStatus = scenario.status
				m.Update(backend.snapshot)
				m.Update(tea.WindowSizeMsg{Width: width, Height: 24})
				view, regions := m.layout()
				lines := uv.NewStyledString(view).Lines(ansi.GraphemeWidth)
				label, heading := scenario.label, "Branch"
				if width < 65 {
					label, heading = scenario.compact, "Br"
				}
				plain := strings.Split(ansi.Strip(view), "\n")
				column := -1
				for _, line := range plain {
					if index := strings.Index(line, heading); index >= 0 {
						column = ansi.StringWidth(line[:index])
						break
					}
				}
				if column < 0 {
					t.Fatal("branch status column is missing")
				}
				for _, region := range regions {
					if region.action != "row" || region.id != "b" {
						continue
					}
					var text strings.Builder
					for _, cell := range lines[region.y][column:] {
						text.WriteString(cell.Content)
					}
					if !strings.HasPrefix(text.String(), label) || lines[region.y][column].Style.Bg == nil {
						t.Fatalf("selected branch status missing: %s", text.String())
					}
					return
				}
				t.Fatal("selected PR was not rendered")
			})
		}
	}
}
