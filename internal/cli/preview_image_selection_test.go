package cli

import (
	"image"
	"image/color"
	"image/draw"
	"io"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/heartleo/zlib/internal/preview"
)

func selectionTestSource() coverSource {
	page := image.NewRGBA(image.Rect(11, 17, 91, 137))
	draw.Draw(page, page.Bounds(), image.White, image.Point{}, draw.Src)
	return coverSource{
		url: "page", documentPage: true, area: image.Rect(3, 4, 13, 10), img: page,
		words: []preview.Word{
			{Text: "top", X0: .125, Y0: .25, X1: .375, Y1: .5},
			{Text: "bottom", X0: .65, Y0: .75, X1: .95, Y1: .95},
		},
	}
}

func TestPDFSelectionHitsAndHighlightsDisplayedPixels(t *testing.T) {
	for _, protocol := range []string{"Kitty", "Sixel"} {
		t.Run(protocol, func(t *testing.T) {
			caps := coverCapabilities{kitty: protocol == "Kitty", sixel: protocol == "Sixel", cellWidth: 4, cellHeight: 6}
			s := selectionTestSource()
			s.selectedFirst, s.selectedEnd = 0, 1
			f := encodeCoverFrame(s, caps)
			if f.x != 5 || f.y != 4 || f.wordAtCell(6, 6) != 0 || f.wordAtCell(4, 6) != -1 || f.wordAtCell(6, 7) != -1 {
				t.Fatal("the fitted word, outer margin or blank line was mapped incorrectly")
			}
			checkTint := func(f *coverFrame, selected, blank image.Point) {
				t.Helper()
				img := decodePDFTestFrame(t, f)
				c := color.RGBAModel.Convert(img.At(selected.X, selected.Y)).(color.RGBA)
				if c.A != 255 || c.B <= c.R || c.G <= c.R {
					t.Errorf("selected pixel %+v has color %+v", selected, c)
				}
				if color.RGBAModel.Convert(img.At(blank.X, blank.Y)) != (color.RGBA{255, 255, 255, 255}) {
					t.Errorf("blank pixel %+v changed", blank)
				}
			}
			checkTint(f, image.Pt(6, 12), image.Pt(16, 24))
			s.zoomPercent, s.panX, s.panY = 200, 100, 100
			s.selectedFirst, s.selectedEnd = 1, 2
			f = encodeCoverFrame(s, caps)
			if !f.wordRects[0].Empty() || f.wordAtCell(f.x+6, f.y+3) != 1 || f.wordAtCell(f.x+1, f.y+1) != -1 {
				t.Fatal("zoom and pan must clip the top word and move the bottom word")
			}
			checkTint(f, image.Pt(26, 21), image.Pt(4, 8))
		})
	}
}

func TestPDFNativeHighlightKeepsCachedDocumentClean(t *testing.T) {
	s := selectionTestSource()
	s.area = image.Rect(0, 0, 80, 120)
	s.selectedFirst, s.selectedEnd = 0, 2
	f := encodeCoverFrame(s, coverCapabilities{kitty: true, cellWidth: 1, cellHeight: 1})
	if f.pixelWidth != 80 || f.pixelHeight != 120 {
		t.Fatal("expected native pixels")
	}
	if got := color.RGBAModel.Convert(s.img.At(30, 62)); got != (color.RGBA{255, 255, 255, 255}) {
		t.Fatalf("selection mutated the source page: %v", got)
	}
}

type selectionMousePage struct {
	source coverSource
	last   tea.Msg
}

func (p *selectionMousePage) coverSlots() []coverSource { return []coverSource{p.source} }
func (p *selectionMousePage) Init() tea.Cmd             { return nil }
func (p *selectionMousePage) View() tea.View            { return tea.View{} }
func (p *selectionMousePage) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	p.last = msg
	return p, nil
}

func TestPDFPointerUsesFrameActuallyWritten(t *testing.T) {
	for _, inShell := range []bool{false, true} {
		p := &selectionMousePage{source: selectionTestSource()}
		var inner tea.Model = p
		if inShell {
			inner = shellModel{kind: "preview", page: p}
		}
		out := &coverOutput{target: io.Discard}
		m := coverGraphicsModel{inner: inner, output: out, caps: coverCapabilities{kitty: true, cellWidth: 4, cellHeight: 6}, supported: true}
		fit := encodeCoverFrame(p.source, m.caps)
		out.set(fit)
		if _, err := out.Write([]byte(coverWake)); err != nil {
			t.Fatal(err)
		}
		// The model has a new zoomed frame; the terminal still shows fit.
		p.source.zoomPercent, p.source.panX, p.source.panY = 200, 100, 100
		m.frame = encodeCoverFrame(p.source, m.caps)
		m.key = m.sourceKey()
		click := tea.MouseClickMsg{X: 6, Y: 6, Button: tea.MouseLeft}
		m.Update(click)
		hit, ok := p.last.(previewImagePointerMsg)
		if !ok || hit.word != 0 || hit.mouse != click {
			t.Fatalf("shell=%v: got %#v while old frame was visible", inShell, p.last)
		}
		out.set(m.frame)
		if _, err := out.Write([]byte(coverWake)); err != nil {
			t.Fatal(err)
		}
		release := tea.MouseReleaseMsg{X: 6, Y: 6, Button: tea.MouseLeft}
		m.Update(release)
		if hit, ok := p.last.(previewImagePointerMsg); !ok || hit.word != -1 || hit.mouse != release {
			t.Fatalf("shell=%v: the released blank cell should have no word", inShell)
		}
	}
}

func TestPDFDragShowsProgressAndRestoresCleanViewport(t *testing.T) {
	p := &selectionMousePage{source: selectionTestSource()}
	m := coverGraphicsModel{inner: p, output: &coverOutput{}, caps: coverCapabilities{kitty: true, cellWidth: 4, cellHeight: 6}, supported: true}
	m.Update(nil)
	clean := encodeCoverFrame(p.source, m.caps)
	m.Update(coverEncodedMsg{m.generation, clean})
	p.source.selecting, p.source.selectedEnd = true, 1
	m.Update(nil)
	click := coverEncodedMsg{m.generation, encodeCoverFrame(p.source, m.caps)}
	p.source.selectedEnd = 2
	m.Update(nil)
	m.Update(click)
	if m.frame != click.frame || !m.encoding || m.encodingKey.selectedEnd != 2 {
		t.Fatal("a fast drag must display its intermediate highlight while encoding the latest range")
	}
	lastDrag := coverEncodedMsg{m.generation, encodeCoverFrame(p.source, m.caps)}
	p.source.selecting, p.source.selectedEnd = false, 0
	m.Update(nil)
	m.Update(lastDrag)
	if m.frame != clean || m.encoding {
		t.Fatal("a completed stale drag must not replace the restored clean viewport")
	}
}
