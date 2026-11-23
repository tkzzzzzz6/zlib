package cli

import (
	"fmt"
	"path/filepath"
	"strings"

	keys "charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// spaceAction also determines whether Space belongs in the reading footer.
func (m previewPageModel) spaceAction() string {
	if m.index+1 >= m.page.Count {
		if m.nextTextScreen() {
			return "Advance one screen."
		}
		return ""
	}
	unit := "page"
	if len(m.page.Chapters) > 0 {
		unit = "chapter"
	}
	if m.nextTextScreen() {
		return "Advance one screen, then turn to the next " + unit + "."
	}
	return "Turn to the next " + unit + "."
}

func (m previewPageModel) previewHints() []keys.Binding {
	var nav, shortNav keys.Binding
	var other []keys.Binding
	switch {
	case m.contents:
		nav, shortNav = uiKey("wheel/↑/↓", "select"), uiKey("↑/↓", "select")
		other = append(other, uiKey("enter", "read"), uiKey("g", "jump"))
	case m.page.Count > 0:
		unit := "pages"
		if len(m.page.Chapters) > 0 {
			unit = "chapters"
		}
		nav, shortNav = uiKey("wheel/←/→", unit), uiKey("←/→", unit)
		if !m.busy {
			if m.showingImage() {
				if m.hasSelection() {
					other = append(other, uiKey("y", "copy"))
				} else if len(m.page.Words) > 0 {
					other = append(other, uiKey("drag", "select"))
				}
				other = append(other, uiKey("Ctrl+wheel", "zoom"))
			} else if len(m.lines) > m.visibleRows() {
				other = append(other, uiKey("↑/↓", "scroll"))
			}
			if len(m.page.Chapters) > 0 {
				other = append(other, uiKey("c", "contents"))
			}
			if m.spaceAction() != "" {
				other = append(other, uiKey("space", "next"))
			}
			if m.showingImage() && m.zoomPercent > 100 {
				other = append(other, uiKey("↑/↓", "pan"))
			}
			if m.showingImage() && m.zoomPercent != 100 {
				other = append(other, uiKey("0", "fit"))
			}
			other = append(other, uiKey("g", "jump"))
		}
		other = append(other, uiKey("o", "open"))
	default:
		nav, shortNav = uiKey("o", "open"), uiKey("o", "open")
	}
	// Reserve the navigation, help and return controls before adding shortcuts
	// for the active mode. Bubbles otherwise drops the trailing hints.
	width := m.width
	if width == 0 {
		width = 80
	}
	help, back := uiKey("?", "keys"), uiKey("esc", "back")
	cells := func(b keys.Binding) int {
		h := b.Help()
		return ansi.StringWidth(h.Key) + 1 + ansi.StringWidth(h.Desc)
	}
	used := cells(nav) + cells(help) + cells(back) + 6
	if used > width {
		nav = shortNav
		used = cells(nav) + cells(help) + cells(back) + 6
	}
	if used > width {
		return []keys.Binding{uiKey(shortNav.Help().Key, ""), uiKey("?", ""), uiKey("esc", "")}
	}
	hints := []keys.Binding{nav}
	for _, hint := range other {
		cost := cells(hint) + 3
		if used+cost <= width {
			hints = append(hints, hint)
			used += cost
		}
	}
	return append(hints, help, back)
}

func (m previewPageModel) helpLines() []string {
	var rows [][2]string
	switch {
	case m.contents:
		rows = append(rows,
			[2]string{"↑/↓, k/j or wheel", "Select a chapter."},
			[2]string{"PgUp/PgDn", "Move through the chapter list by a screen."},
			[2]string{"Home/End", "Select the first or last chapter."},
			[2]string{"Enter", "Read the selected chapter."},
			[2]string{"g", "Jump to a chapter by number."},
			[2]string{"c or Esc", "Return to the current chapter."})
	case m.page.Count > 0:
		unit := "page"
		if len(m.page.Chapters) > 0 {
			unit = "chapter"
		}
		rows = append(rows, [2]string{"←/→, h/l or wheel", "Turn to the previous or next " + unit + "."})
		if !m.busy {
			if advance := m.spaceAction(); advance != "" {
				rows = append(rows, [2]string{"Space", advance})
			}
			if m.showingImage() {
				if len(m.page.Words) > 0 {
					rows = append(rows,
						[2]string{"Left drag", "Select words on the page."},
						[2]string{"y or Ctrl+C", "Copy the selected text."})
				}
				rows = append(rows,
					[2]string{"+/- or Ctrl+wheel", "Zoom in or out."},
					[2]string{"0", "Fit the full page."})
				if m.zoomPercent > 100 {
					rows = append(rows,
						[2]string{"↑/↓", "Pan vertically."},
						[2]string{"Shift+←/→", "Pan horizontally."})
				}
			} else {
				if len(m.lines) > m.visibleRows() {
					rows = append(rows,
						[2]string{"↑/↓ or k/j", "Scroll through the text."},
						[2]string{"PgUp/PgDn", "Scroll one screen."},
						[2]string{"Home/End", "Go to the start or end of the text."})
				}
			}
			rows = append(rows, [2]string{"g", "Jump to a " + unit + " by number."})
			if len(m.page.Chapters) > 0 {
				rows = append(rows, [2]string{"c", "Choose a chapter from the list."})
			}
		}
	}
	if !m.contents {
		rows = append(rows, [2]string{"o", "Open the book in the system reader."}, [2]string{"Esc or q", "Leave the preview."})
	}

	var lines []string
	for _, row := range rows {
		line := fmt.Sprintf("%-18s  %s", row[0], row[1])
		if m.width < 55 {
			line = row[0] + "  " + row[1]
		}
		lines = append(lines, strings.Split(ansi.Wrap(line, max(1, m.width), ""), "\n")...)
	}
	return lines
}

func (m previewPageModel) helpRowCount() int { return len(m.helpLines()) }

func (m previewPageModel) helpView() tea.View {
	lines := m.helpLines()
	n := m.visibleRows()
	start := min(max(0, m.helpOffset), max(0, len(lines)-n))
	stop := min(len(lines), start+n)
	hints := []keys.Binding{uiKey("?/esc", "back")}
	status := ""
	if len(lines) > n {
		hints = append(hints, uiKey("↑/↓", "scroll"))
		status = fmt.Sprintf("Lines %d–%d of %d", start+1, stop, len(lines))
	}
	mode := "Book preview"
	switch {
	case m.contents:
		mode = "Chapter list"
	case len(m.page.Chapters) > 0:
		mode = "EPUB text"
	case m.showingImage():
		mode = "PDF image"
	case strings.EqualFold(filepath.Ext(m.path), ".pdf"):
		mode = "PDF text"
	}
	return m.previewView("Preview shortcuts", mode, strings.Join(lines[start:stop], "\n"), status, hints...)
}
