package cli

import (
	"image"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/heartleo/zlib/internal/preview"
)

func TestPreviewCtrlWheelZoomAndFit(t *testing.T) {
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
	wheelUp := tea.MouseWheelMsg{Button: tea.MouseWheelUp, Mod: tea.ModCtrl}
	wheelDown := tea.MouseWheelMsg{Button: tea.MouseWheelDown, Mod: tea.ModCtrl}
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
	if view := p.View(); view.MouseMode != tea.MouseModeCellMotion || !strings.Contains(view.Content, "First line") {
		t.Fatal("text mode must receive page turns and show the extracted text")
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

func TestPreviewWheelTurnsPagesAndChapters(t *testing.T) {
	for _, mode := range []string{"pdf image", "epub", "pdf text"} {
		t.Run(mode, func(t *testing.T) {
			p := newPreviewPage("sample.pdf")
			defer p.cancel()
			p.graphics, p.busy = true, false
			p.page = preview.Page{Count: 3, Image: image.NewGray(image.Rect(0, 0, 40, 80))}
			var cmd tea.Cmd
			switch mode {
			case "epub":
				p.path, p.page.Image, p.page.Chapters = "sample.epub", nil, []string{"A", "B", "C"}
			case "pdf text":
				p.graphics, p.page.Image = false, nil
			}
			if p.View().MouseMode == tea.MouseModeNone {
				t.Fatal("preview did not request wheel events")
			}
			wheel := func(button tea.MouseButton, mod tea.KeyMod) {
				next, c := p.Update(tea.MouseWheelMsg{Button: button, Mod: mod})
				p, cmd = next.(previewPageModel), c
			}
			wheel(tea.MouseWheelUp, 0)
			if cmd != nil || p.index != 0 || p.zoomPercent != 100 {
				t.Fatal("wheel crossed the first page or changed zoom")
			}
			wheel(tea.MouseWheelDown, tea.ModShift)
			if cmd != nil || p.index != 0 {
				t.Fatal("unexpected modifier turned page")
			}
			wheel(tea.MouseWheelDown, 0)
			if cmd == nil || p.index != 1 || !p.busy || p.zoomPercent != 100 {
				t.Fatal("wheel down did not request the next page at the same zoom")
			}
			wheel(tea.MouseWheelDown, 0)
			if cmd == nil || p.index != 2 || p.View().MouseMode == tea.MouseModeNone {
				t.Fatal("consecutive wheel events must navigate from the latest requested page")
			}
			wheel(tea.MouseWheelDown, 0)
			if cmd != nil || p.index != 2 {
				t.Fatal("wheel crossed the last page")
			}
			wheel(tea.MouseWheelUp, 0)
			if cmd == nil || p.index != 1 {
				t.Fatal("wheel up did not request the previous page")
			}
			p.cancel()
		})
	}
}

func TestPreviewWheelNavigatesOpenChapterList(t *testing.T) {
	p := newPreviewPage("sample.epub")
	defer p.cancel()
	p.page = preview.Page{Count: 3, Chapters: []string{"A", "B", "C"}}
	p.busy, p.contents = false, true
	if p.View().MouseMode == tea.MouseModeNone {
		t.Fatal("chapter list did not request wheel events")
	}
	next, cmd := p.Update(tea.MouseWheelMsg{Button: tea.MouseWheelDown})
	p = next.(previewPageModel)
	if cmd != nil || p.chapter != 1 || !p.contents || p.index != 0 {
		t.Fatal("wheel in the chapter list should move the selection only")
	}
}

func TestShellPreviewForwardsWheelAndModifiers(t *testing.T) {
	p := newPreviewPage("sample.pdf")
	defer p.cancel()
	p.graphics, p.busy = true, false
	p.page = preview.Page{Count: 2, Image: image.NewGray(image.Rect(0, 0, 40, 80))}
	m := shellModel{kind: "preview", page: p, width: 80, height: 24}
	view := m.View()
	if view.MouseMode != tea.MouseModeCellMotion {
		t.Fatal("shell did not request wheel events")
	}
	for _, hint := range []string{"wheel/←/→ pages", "Ctrl+wheel/+/- zoom", "? keys", "esc back"} {
		if !strings.Contains(ansi.Strip(view.Content), hint) {
			t.Fatalf("80-column preview omitted %q", hint)
		}
	}
	m, cmd := shellStep(m, tea.MouseWheelMsg{Button: tea.MouseWheelUp, Mod: tea.ModCtrl})
	if cmd != nil || m.page.(previewPageModel).zoomPercent != 125 {
		t.Fatal("shell did not forward Ctrl+wheel to the current image")
	}
	m, cmd = shellStep(m, tea.MouseWheelMsg{Button: tea.MouseWheelDown})
	p = m.page.(previewPageModel)
	defer p.cancel()
	if cmd == nil || p.index != 1 || p.zoomPercent != 125 {
		t.Fatal("shell did not forward the plain wheel as a page turn")
	}
}
