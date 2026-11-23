package cli

import (
	"image"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/heartleo/zlib/internal/preview"
)

func TestPreviewHintsFollowModeAndViewport(t *testing.T) {
	p := newPreviewPage("example.pdf")
	defer p.cancel()
	p.busy, p.graphics = false, true
	p.page = preview.Page{Count: 3, Image: image.NewGray(image.Rect(0, 0, 10, 10)), Text: "One line"}
	p.wrap()
	footer := func() string {
		rows := strings.Split(ansi.Strip(p.View().Content), "\n")
		return rows[len(rows)-1]
	}
	check := func(want, hidden []string) {
		t.Helper()
		line := footer()
		for _, s := range want {
			if !strings.Contains(line, s) {
				t.Errorf("footer missing %q in %q", s, line)
			}
		}
		for _, s := range hidden {
			if strings.Contains(line, s) {
				t.Errorf("unexpected %q in %q", s, line)
			}
		}
	}
	check([]string{"pages", "zoom", "space next", "? keys", "esc back"}, []string{"scroll", "chapters", "pan"})
	p.zoomPercent = 125
	check([]string{"pan", "zoom"}, []string{"scroll"})
	p.textOnly = true
	check([]string{"text/image", "pages"}, []string{"scroll", "zoom", "pan"})
	p.page.Text = strings.Repeat("long text\n", 30)
	p.wrap()
	check([]string{"scroll", "text/image"}, []string{"zoom"})
	p.path = "example.epub"
	p.page.Chapters = []string{"One", "Two", "Three"}
	check([]string{"scroll", "←/→ chapters", "c contents", "space next"}, []string{"text/image", "zoom"})
	for _, width := range []int{20, 30, 40, 60, 80} {
		p.width = width
		check([]string{"←/→", "?", "esc"}, []string{"…"})
	}
	p.contents = true
	check([]string{"select", "enter", "?", "esc"}, []string{"zoom", "turn", "scroll"})
}

func TestPreviewHelpListsActionsForTheActiveReader(t *testing.T) {
	p := newPreviewPage("example.pdf")
	defer p.cancel()
	p.graphics, p.busy, p.help = true, false, true
	p.page = preview.Page{Count: 2, Image: image.NewGray(image.Rect(0, 0, 10, 10))}
	check := func(want, hidden []string) {
		t.Helper()
		view := ansi.Strip(p.View().Content)
		for _, s := range want {
			if !strings.Contains(view, s) {
				t.Errorf("missing %q in %q", s, view)
			}
		}
		for _, s := range hidden {
			if strings.Contains(view, s) {
				t.Errorf("unexpected %q in %q", s, view)
			}
		}
	}
	check([]string{"PDF image", "Space", "Jump to a page", "Ctrl+wheel", "Fit the full page", "system reader"}, []string{"chapter", "Scroll through", "Pan vertically"})
	p.zoomPercent = 125
	check([]string{"Pan vertically", "Pan horizontally"}, nil)
	p.textOnly = true
	check([]string{"PDF text", "Space", "Jump to a page", "Switch between"}, []string{"Pan vertically", "Ctrl+wheel", "Scroll through"})
	p.path = "example.epub"
	p.page.Chapters = []string{"First", "Second"}
	p.page.Text = strings.Repeat("x\n", 30)
	p.wrap()
	check([]string{"EPUB text", "Jump to a chapter", "Choose a chapter", "Scroll through", "←/→, h/l", "↑/↓ or k/j"}, []string{"PDF", "Ctrl+wheel", "Switch between"})
	p.contents = true
	check([]string{"Chapter list", "Select a chapter", "Read the selected chapter"}, []string{"Space", "system reader"})
}

func TestPreviewHelpKeepsWrappedRowsReachable(t *testing.T) {
	p := newPreviewPage("example.epub")
	defer p.cancel()
	p.graphics, p.busy = false, false
	p.width, p.height = 32, 10
	p.page = preview.Page{Count: 2, Chapters: []string{"One", "Two"}}
	p.wrap()
	if p.helpRowCount() <= p.visibleRows() {
		t.Fatal("expected scrolling help")
	}
	p.helpOffset = p.helpRowCount()
	got := ansi.Strip(p.helpView().Content)
	if !strings.Contains(got, "Leave the preview.") || !strings.Contains(got, "scroll") || !strings.Contains(got, "back") {
		t.Fatalf("unreachable final shortcuts: %q", got)
	}
}

func TestPreviewSpaceHintsMatchRemainingTextAndPages(t *testing.T) {
	p := newPreviewPage("sample.epub")
	defer p.cancel()
	p.busy = false
	p.index = 1
	p.page = preview.Page{Index: 1, Count: 2, Chapters: []string{"First", "Last"}, Text: strings.Repeat("body\n", 40)}
	p.wrap()

	check := func(want string) {
		t.Helper()
		lines := strings.Split(ansi.Strip(p.View().Content), "\n")
		footer := lines[len(lines)-1]
		if strings.Contains(footer, "space next") != (want != "") {
			t.Fatalf("Space availability mismatch in footer %q; expected action %q", footer, want)
		}
		action := ""
		for _, line := range p.helpLines() {
			if strings.HasPrefix(line, "Space ") {
				action = strings.TrimSpace(strings.TrimPrefix(line, "Space"))
			}
		}
		if action != want {
			t.Fatalf("Space help = %q; want %q", action, want)
		}
	}

	check("Advance one screen.")
	p.offset = len(p.lines) - p.visibleRows()
	check("")
	p.index, p.page.Index = 0, 0
	check("Turn to the next chapter.")
	p.offset = 0
	check("Advance one screen, then turn to the next chapter.")

	p.index, p.page.Index, p.page.Text = 1, 1, ""
	p.height = 7 // Empty-chapter actions wrap beyond the visible body.
	p.wrap()
	check("")

	p.path = "sample.pdf"
	p.graphics = true
	p.height = 24
	p.page = preview.Page{Index: 1, Count: 2, Image: image.NewGray(image.Rect(0, 0, 10, 10)), Text: strings.Repeat("body\n", 40)}
	p.wrap()
	check("")
	p.textOnly = true
	check("Advance one screen.")
}
