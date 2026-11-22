package cli

import (
	"image"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/heartleo/zlib/internal/preview"
)

func TestPreviewPageOpensLocalEPUBAndScrolls(t *testing.T) {
	isolateConfigHome(t)
	file := writeEPUB(t, "Pocket Guide", strings.Repeat("<p>Sample body paragraph.</p>", 50))
	m := newPreviewPage(file)
	defer m.cancel()
	next, _ := m.Update(m.Init()())
	m = next.(previewPageModel)
	if !strings.Contains(ansi.Strip(m.View().Content), "Sample body paragraph.") {
		t.Fatalf("preview: %s", m.View().Content)
	}
	next, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyPgDown})
	m = next.(previewPageModel)
	if m.offset == 0 {
		t.Fatal("text did not scroll")
	}
	next, _ = m.Update(key("c"))
	m = next.(previewPageModel)
	if !m.contents || !strings.Contains(m.View().Content, "Chapters") {
		t.Fatal("missing chapter list")
	}
	next, cmd := m.Update(key("esc"))
	if cmd != nil || next.(previewPageModel).contents {
		t.Fatal("Esc should dismiss chapter list first")
	}
	_, cmd = next.Update(key("esc"))
	if _, ok := cmd().(tea.QuitMsg); !ok || m.ctx.Err() == nil {
		t.Fatal("preview did not close")
	}
}

func TestPreviewPageNavigationDropsLateResponses(t *testing.T) {
	m := newPreviewPage("book.epub")
	defer m.cancel()
	m.page = preview.Page{Index: 0, Count: 3, Title: "First", Chapters: []string{"First", "Second", "Third"}}
	m.busy = false
	next, _ := m.Update(key("c"))
	next, _ = next.Update(tea.KeyPressMsg{Code: tea.KeyEnd})
	next, cmd := next.Update(key("enter"))
	p := next.(previewPageModel)
	defer p.cancel()
	if p.index != 2 || p.contents || cmd == nil {
		t.Fatal("chapter jump failed")
	}
	next, _ = p.Update(tea.KeyPressMsg{Code: tea.KeyLeft})
	p = next.(previewPageModel)
	defer p.cancel()
	stale := previewLoadedMsg{generation: p.generation - 1, page: preview.Page{Index: 2, Count: 3, Title: "Late"}}
	next, _ = p.Update(stale)
	p = next.(previewPageModel)
	if !p.busy || p.index != 1 || p.page.Title == "Late" {
		t.Fatal("stale result replaced selected chapter")
	}
}

func TestPreviewPDFGraphicFits80ColumnsAndTogglesText(t *testing.T) {
	p := newPreviewPage("book.pdf")
	defer p.cancel()
	p.graphics, p.busy = true, false
	p.page = preview.Page{Image: image.NewGray(image.Rect(0, 0, 30, 40)), Text: "PDF text", Count: 2}
	p.wrap()
	outer := coverGraphicsModel{inner: shellModel{page: p, kind: "preview"}, caps: coverCapabilities{kitty: true, cellWidth: 8, cellHeight: 16}}
	if outer.sourceKey().layout == "" || outer.source().bounds() != image.Rect(1, 3, 79, 21) {
		t.Fatal("PDF did not reserve the 80x24 preview body")
	}
	next, _ := p.Update(key("x"))
	p = next.(previewPageModel)
	if len(p.coverSlots()) != 0 || !strings.Contains(p.View().Content, "PDF text") {
		t.Fatal("text toggle failed")
	}
	next, _ = p.Update(previewGraphicsMsg(false))
	if next.(previewPageModel).graphics {
		t.Fatal("capability loss ignored")
	}
}

func TestPDFWheelBurstSkipsSupersededLoads(t *testing.T) {
	m := newPreviewPage("missing.pdf")
	m.page, m.busy = preview.Page{Count: 8}, false
	defer m.cancel()
	var cmds []tea.Cmd
	for range 5 {
		next, cmd := m.Update(tea.MouseWheelMsg{Button: tea.MouseWheelDown})
		m, cmds = next.(previewPageModel), append(cmds, cmd)
	}
	defer m.cancel()
	// Only the first turn and settled target can reach a loader. In-flight
	// turns that were replaced before the settling window must emit nothing.
	for _, cmd := range cmds[1 : len(cmds)-1] {
		if msg := cmd(); msg != nil {
			t.Fatalf("superseded wheel turn emitted %T", msg)
		}
	}
	last := cmds[len(cmds)-1]().(previewLoadedMsg)
	if m.index != 5 || last.generation != m.generation || last.err == nil {
		t.Fatal("the settled wheel target was not loaded")
	}
}

func TestPreviewGraphicsProbeLetsActiveTextLoadFinish(t *testing.T) {
	p := newPreviewPage("sample.pdf")
	defer p.cancel()
	p.graphics, p.busy = false, true
	initial := p.ctx
	next, cmd := p.Update(previewGraphicsMsg(true))
	p = next.(previewPageModel)
	if cmd != nil || !p.upgradeImage || initial.Err() != nil {
		t.Fatal("capability probe restarted active extraction")
	}
	next, cmd = p.Update(previewLoadedMsg{page: preview.Page{Count: 4, Text: "extracted"}})
	p = next.(previewPageModel)
	defer p.cancel()
	if cmd == nil || !p.graphics || p.upgradeImage || p.page.Text != "extracted" || !p.busy {
		t.Fatal("finished extraction did not upgrade to graphics")
	}
}
