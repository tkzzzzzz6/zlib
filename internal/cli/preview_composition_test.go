package cli

import (
	"image"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/heartleo/zlib/internal/preview"
)

func TestPDFZoomKeepsLastFrameUntilLatestViewportIsReady(t *testing.T) {
	page := newPreviewPage("sample.pdf")
	defer page.cancel()
	page.graphics, page.busy = true, false
	page.page = preview.Page{Count: 2, Image: image.NewGray(image.Rect(0, 0, 20, 50))}
	m := coverGraphicsModel{inner: page, output: &coverOutput{}, caps: coverCapabilities{kitty: true, cellWidth: 8, cellHeight: 16}, supported: true}
	m.Update(nil)
	initial := encodeCoverFrame(m.source(), m.caps)
	m.Update(coverEncodedMsg{m.generation, initial})
	visible := func() *coverFrame {
		t.Helper()
		m.View()
		return m.output.want
	}
	if initial == nil || visible() != initial {
		t.Fatal("fitted page did not appear")
	}
	key := func(code rune) { m.Update(tea.KeyPressMsg{Code: code, Text: string(code)}) }
	key('+')
	firstZoom := m.generation
	if visible() != initial || !m.encoding {
		t.Fatal("zoom removed the fitted frame before the replacement was ready")
	}
	key('+')
	m.Update(coverEncodedMsg{firstZoom, &coverFrame{}})
	if visible() != initial || !m.encoding {
		t.Fatal("obsolete zoom result displaced the fitted frame or stopped the latest encode")
	}
	latest := encodeCoverFrame(m.source(), m.caps)
	m.Update(coverEncodedMsg{m.generation, latest})
	if latest == nil || visible() != latest || m.encoding {
		t.Fatal("latest zoom did not replace the fitted frame")
	}
	key('-')
	lastZoom := m.generation
	key('x')
	m.Update(coverEncodedMsg{lastZoom, initial})
	if visible() != nil {
		t.Fatal("in-flight image survived the switch to text")
	}
}

func TestPDFImageClearsTextAtBothMargins(t *testing.T) {
	const width, height = 80, 24
	page := newPreviewPage("sample.pdf")
	defer page.cancel()
	page.graphics, page.busy = true, false
	line := "C" + strings.Repeat("x", width-2) + "Z"
	page.page = preview.Page{Title: "sample.pdf", Count: 1, Image: image.NewGray(image.Rect(0, 0, 20, 50)), Text: line + "\nSecond paragraph"}
	page.wrap()
	shell := shellModel{page: page, kind: "preview", width: width, height: height}
	m := coverGraphicsModel{inner: shell, output: &coverOutput{}, caps: coverCapabilities{kitty: true, cellWidth: 8, cellHeight: 16}, generation: 1}
	m.key = m.sourceKey()
	render := func() []string { return strings.Split(ansi.Strip(m.View().Content), "\n") }
	checkMargins := func(state string) {
		t.Helper()
		rows := render()
		for i := 3; i < height-3; i++ {
			if strings.TrimSpace(rows[i]) != "" {
				t.Fatalf("%s: unexpected PDF text in image row %d: %q", state, i, rows[i])
			}
		}
		if !strings.Contains(rows[0], "Page 1 of 1") || strings.TrimSpace(rows[height-1]) == "" {
			t.Fatalf("%s: heading or controls were cleared", state)
		}
	}
	checkMargins("waiting for image")
	m.frame = encodeCoverFrame(m.source(), m.caps)
	if m.frame == nil {
		t.Fatal("page failed to encode")
	}
	checkMargins("image displayed")
	m.frame, m.encodedGeneration = nil, m.generation
	if got := render()[3]; got != line {
		t.Fatalf("encoding failure lost the text fallback: %q", got)
	}
}

type mouseCapturingPage struct{ previewPageModel }

func (m mouseCapturingPage) View() tea.View {
	v := m.previewPageModel.View()
	v.MouseMode = tea.MouseModeCellMotion
	return v
}

func TestShellPreservesChildMouseMode(t *testing.T) {
	isolateConfigHome(t)
	page := newPreviewPage("sample.pdf")
	defer page.cancel()
	m := newShellModel()
	m.kind, m.page, m.typing = "preview", mouseCapturingPage{page}, false
	for _, notice := range []string{"", "Ready"} {
		m.status = notice
		if m.View().MouseMode != tea.MouseModeCellMotion {
			t.Fatalf("child lost mouse input, notice=%q", notice)
		}
	}
	m.home()
	if m.View().MouseMode != tea.MouseModeNone {
		t.Fatal("home retained preview mouse capture")
	}
}
