package cli

import (
	"image"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/heartleo/zlib/internal/preview"
)

func TestPreviewPanelsPreservePDFPosition(t *testing.T) {
	for _, shortcut := range []string{"?", "g"} {
		t.Run(shortcut, func(t *testing.T) {
			p := newPreviewPage("book.pdf")
			defer p.cancel()
			p.busy, p.graphics = false, true
			p.page = preview.Page{Count: 8, Index: 3, Image: image.NewGray(image.Rect(0, 0, 4, 4))}
			p.index, p.offset, p.zoomPercent, p.panX, p.panY = 3, 2, 200, 50, -25
			next, _ := p.Update(key(shortcut))
			if len(next.(previewPageModel).coverSlots()) != 0 {
				t.Fatal("PDF graphics cover the panel")
			}
			for _, event := range []tea.Msg{
				tea.MouseWheelMsg{Button: tea.MouseWheelDown},
				tea.MouseWheelMsg{Button: tea.MouseWheelUp, Mod: tea.ModCtrl},
				tea.KeyPressMsg{Code: tea.KeyRight}, key("o"),
			} {
				next, _ = next.Update(event)
			}
			next, cmd := next.Update(key("esc"))
			p = next.(previewPageModel)
			if cmd != nil || len(p.coverSlots()) != 1 || p.index != 3 || p.offset != 2 || p.zoomPercent != 200 || p.panX != 50 || p.panY != -25 || p.ctx.Err() != nil {
				t.Fatal("closing the panel changed the reading position or left the reader")
			}
		})
	}
}

func TestPreviewJumpValidatesOneBasedChapter(t *testing.T) {
	p := newPreviewPage("book.epub")
	defer p.cancel()
	p.busy = false
	p.page = preview.Page{Count: 3, Chapters: []string{"First", "Middle", "Last"}}
	for _, input := range []string{"", "0", "4", "2.5", "-1", "1a", strings.Repeat("9", 35)} {
		next, _ := p.Update(key("g"))
		if !strings.Contains(ansi.Strip(next.View().Content), "Go to chapter") {
			t.Fatal("expected a chapter prompt")
		}
		next, _ = next.Update(tea.PasteMsg{Content: input})
		next, cmd := next.Update(key("enter"))
		if cmd != nil || !strings.Contains(ansi.Strip(next.View().Content), "Enter a number from 1 to 3") {
			t.Fatalf("invalid jump %q should show the valid range", input)
		}
	}
	next, _ := p.Update(key("c"))
	next, _ = next.Update(key("g"))
	next, _ = next.Update(key("esc"))
	if !next.(previewPageModel).contents {
		t.Fatal("cancelling a jump from the chapter list lost the selection")
	}
	next, _ = next.Update(key("g"))
	next, _ = next.Update(key("2"))
	next, _ = next.Update(tea.KeyPressMsg{Code: tea.KeyBackspace})
	next, _ = next.Update(key("3"))
	next, cmd := next.Update(key("enter"))
	got := next.(previewPageModel)
	defer got.cancel()
	if cmd == nil || !got.busy || got.index != 2 || got.contents {
		t.Fatal("jumping to chapter 3 must load index 2")
	}
}

func TestPreviewPanelsEscRestoresBookshelf(t *testing.T) {
	isolateConfigHome(t)
	m, err := newStandaloneShellCommand("/library")
	if err != nil {
		t.Fatal(err)
	}
	path := writeTestEPUB(t)
	m, cmd := shellStep(m, shellEvent{m.generation, previewRequestedMsg{path}})
	m, _ = shellStep(m, cmd())
	for _, shortcut := range []string{"?", "g"} {
		m, _ = shellStep(m, key(shortcut))
		m, cmd = shellStep(m, key("esc"))
		if cmd != nil || m.kind != "preview" {
			t.Fatalf("Esc in %s should close only the panel", shortcut)
		}
	}
	m, cmd = shellStep(m, key("esc"))
	m, _ = shellStep(m, cmd())
	if m.kind != "library" {
		t.Fatal("reader Esc after the panel closes must restore the bookshelf")
	}
}
