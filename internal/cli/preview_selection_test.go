package cli

import (
	"errors"
	"image"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/heartleo/zlib/internal/preview"
)

func TestPDFSelectionScanAndImageMode(t *testing.T) {
	p := newPreviewPage("book.pdf")
	defer p.cancel()
	p.graphics, p.busy = true, false
	p.page = preview.Page{Count: 1, Image: image.NewGray(image.Rect(0, 0, 32, 48))}
	if !strings.Contains(ansi.Strip(p.View().Content), "No selectable text") {
		t.Error("a scan should say why dragging cannot select text")
	}
	p.page.WordsError = "pdftotext unavailable"
	if !strings.Contains(ansi.Strip(p.View().Content), p.page.WordsError) {
		t.Error("extraction failure should explain why text selection is unavailable")
	}
	next, cmd := p.Update(key("x"))
	if cmd != nil || !next.(previewPageModel).showingImage() {
		t.Error("x should leave the graphical PDF on screen")
	}
}
func TestPDFDragCopiesInReadingOrderOnlyOnRequest(t *testing.T) {
	p := newPreviewPage("words.pdf")
	defer p.cancel()
	p.graphics, p.busy = true, false
	p.page = preview.Page{Count: 2, Image: image.NewGray(image.Rect(0, 0, 20, 30)), Words: []preview.Word{
		{Text: "Early", Line: 0}, {Text: "birds", Line: 0}, {Text: "fly.", Line: 1},
	}}
	var copied string
	p.writeClipboard = func(text string) error { copied = text; return nil }
	for _, pointer := range []previewImagePointerMsg{
		{mouse: tea.MouseClickMsg{Button: tea.MouseLeft}, word: 2},
		{mouse: tea.MouseMotionMsg{Button: tea.MouseLeft}, word: 1},
		{mouse: tea.MouseReleaseMsg{Button: tea.MouseLeft}, word: 0},
	} {
		next, cmd := p.Update(pointer)
		p = next.(previewPageModel)
		if cmd != nil || copied != "" {
			t.Fatal("mouse selection should never copy automatically")
		}
	}
	src := p.coverSlots()[0]
	if !p.hasSelection() || src.selectedFirst != 0 || src.selectedEnd != 3 {
		t.Fatalf("expected a highlighted range covering all three words")
	}
	next, cmd := p.Update(key("y"))
	if cmd == nil {
		t.Fatal("y must request a copy")
	}
	next, _ = next.Update(cmd())
	p = next.(previewPageModel)
	if copied != "Early birds\nfly." || !strings.Contains(ansi.Strip(p.View().Content), "Copied") {
		t.Fatal("copy must preserve PDF word and line order and confirm completion")
	}
	p.writeClipboard = func(string) error { return errors.New("clipboard unavailable") }
	next, cmd = p.Update(key("ctrl+c"))
	if cmd == nil {
		t.Fatal("Ctrl+C should copy the selected text")
	}
	next, _ = next.Update(cmd())
	p = next.(previewPageModel)
	if !p.hasSelection() || !strings.Contains(ansi.Strip(p.View().Content), "clipboard unavailable") {
		t.Fatal("failed copy should keep the selection and show the error")
	}
	next, _ = p.Update(previewImagePointerMsg{mouse: tea.MouseClickMsg{Button: tea.MouseLeft}, word: -1})
	p = next.(previewPageModel)
	if p.hasSelection() {
		t.Fatal("clicking blank paper should clear the highlight")
	}
	if _, cmd = p.Update(key("ctrl+c")); cmd == nil {
		t.Fatal("Ctrl+C without a selection should exit")
	} else if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatal("Ctrl+C without a selection should exit")
	}
}

func TestPDFSelectionClearsWhenViewportChanges(t *testing.T) {
	for _, tc := range []struct {
		name string
		msg  tea.Msg
	}{
		{"page", tea.KeyPressMsg{Code: tea.KeyRight}},
		{"wheel zoom", tea.MouseWheelMsg{Button: tea.MouseWheelUp, Mod: tea.ModCtrl}},
		{"pan", tea.KeyPressMsg{Code: tea.KeyDown}},
		{"resize", tea.WindowSizeMsg{Width: 72, Height: 16}},
		{"text fallback", previewGraphicsMsg(false)},
		{"help", key("?")}, {"jump", key("g")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := newPreviewPage("words.pdf")
			defer p.cancel()
			p.graphics, p.busy, p.zoomPercent = true, false, 150
			p.page = preview.Page{Count: 2, Image: image.NewGray(image.Rect(0, 0, 20, 30)), Words: []preview.Word{{Text: "word"}}}
			next, _ := p.Update(previewImagePointerMsg{mouse: tea.MouseClickMsg{Button: tea.MouseLeft}, word: 0})
			p = next.(previewPageModel)
			if !p.hasSelection() {
				t.Fatal("expected a selected word")
			}
			next, _ = p.Update(tc.msg)
			p = next.(previewPageModel)
			defer p.cancel()
			if p.hasSelection() {
				t.Fatal("transformation retained stale selection")
			}
		})
	}
}
