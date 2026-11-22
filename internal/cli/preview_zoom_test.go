package cli

import (
	"image"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/heartleo/zlib/internal/preview"
)

func TestPreviewWheelZoomAndFit(t *testing.T) {
	p := newPreviewPage("pages.pdf")
	defer p.cancel()
	p.busy, p.graphics = false, true
	p.page = preview.Page{Count: 2, Image: image.NewGray(image.Rect(0, 0, 40, 80)), Text: "First line"}
	p.wrap()
	if p.View().MouseMode != tea.MouseModeCellMotion {
		t.Fatal("image view did not request mouse reporting")
	}
	step := func(msg tea.Msg) {
		t.Helper()
		next, cmd := p.Update(msg)
		if cmd != nil {
			t.Fatal("zoom and text toggles should reuse the loaded PDF")
		}
		p = next.(previewPageModel)
	}
	wantZoom := func(label string) {
		t.Helper()
		if got := ansi.Strip(p.View().Content); !strings.Contains(got, label) {
			t.Fatalf("zoom should show %s: %s", label, got)
		}
	}
	wheelUp := tea.MouseWheelMsg{Button: tea.MouseWheelUp}
	wheelDown := tea.MouseWheelMsg{Button: tea.MouseWheelDown}
	step(wheelUp)
	wantZoom("125%")
	step(wheelDown)
	wantZoom("100%")
	for range 24 {
		step(wheelUp)
	}
	wantZoom("400%")
	for range 24 {
		step(wheelDown)
	}
	wantZoom("50%")
	step(key("+"))
	wantZoom("75%")
	step(key("-"))
	wantZoom("50%")
	step(key("0"))
	wantZoom("100%")
	step(key("x"))
	if view := p.View(); view.MouseMode != tea.MouseModeNone || !strings.Contains(view.Content, "First line") {
		t.Fatal("text mode must release the mouse and show the extracted text")
	}
	step(wheelUp)
	step(key("x"))
	wantZoom("100%")
}

func TestEnlargedPreviewPansAndTurnsPages(t *testing.T) {
	p := newPreviewPage("pages.pdf")
	defer p.cancel()
	p.graphics, p.busy = true, false
	p.page = preview.Page{Count: 2, Image: image.NewGray(image.Rect(0, 0, 100, 100))}
	input := func(k tea.KeyPressMsg) {
		next, _ := p.Update(k)
		p = next.(previewPageModel)
	}
	down := tea.KeyPressMsg{Code: tea.KeyDown}
	right := tea.KeyPressMsg{Code: tea.KeyRight, Mod: tea.ModShift}
	input(down)
	if p.panY != 0 {
		t.Fatal("a fitted page cannot pan")
	}
	input(key("+"))
	for range 8 {
		input(down)
		input(right)
	}
	slot := p.coverSlots()[0]
	if slot.zoomPercent != 125 || slot.panX != 100 || slot.panY != 100 {
		t.Fatalf("viewport zoom=%d pan=(%d,%d)", slot.zoomPercent, slot.panX, slot.panY)
	}
	next, cmd := p.Update(tea.KeyPressMsg{Code: tea.KeyRight})
	p = next.(previewPageModel)
	defer p.cancel()
	if cmd == nil || !p.busy || p.index != 1 {
		t.Fatal("right arrow should still turn pages")
	}
	nextPage := preview.Page{Index: 1, Count: 2, Image: p.page.Image}
	next, _ = p.Update(previewLoadedMsg{generation: p.generation, page: nextPage})
	p = next.(previewPageModel)
	slot = p.coverSlots()[0]
	if slot.zoomPercent != 125 || slot.panX != 0 || slot.panY != -100 {
		t.Fatal("new enlarged page should open at the top")
	}
	input(key("0"))
	slot = p.coverSlots()[0]
	if slot.zoomPercent != 100 || slot.panX != 0 || slot.panY != 0 {
		t.Fatal("fit did not restore the centered page")
	}
}

func TestPreviewWheelCaptureOnlyForPDFImages(t *testing.T) {
	for _, mode := range []string{"epub", "text", "unsupported terminal", "small window", "loading"} {
		t.Run(mode, func(t *testing.T) {
			p := newPreviewPage("sample.pdf")
			defer p.cancel()
			p.graphics, p.busy = true, false
			p.page = preview.Page{Count: 1, Image: image.NewGray(image.Rect(0, 0, 40, 80))}
			switch mode {
			case "epub":
				p.path, p.page.Image, p.page.Chapters = "sample.epub", nil, []string{"Chapter"}
			case "text":
				p.textOnly = true
			case "unsupported terminal":
				p.graphics = false
			case "small window":
				p.height = 5
			case "loading":
				p.busy = true
			}
			next, _ := p.Update(tea.MouseWheelMsg{Button: tea.MouseWheelUp})
			p = next.(previewPageModel)
			if p.View().MouseMode != tea.MouseModeNone || p.zoomPercent != 100 {
				t.Fatal("captured wheel outside graphical PDF preview")
			}
		})
	}
}
