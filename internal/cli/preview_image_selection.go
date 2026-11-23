package cli

import (
	"image"
	"image/color"
	"image/draw"
	"math"

	tea "charm.land/bubbletea/v2"
	"github.com/heartleo/zlib/internal/preview"
)

type previewImagePointerMsg struct {
	mouse tea.MouseMsg
	word  int // index in page reading order, or -1 over blank space
}

func (m *coverGraphicsModel) imagePointer(msg tea.Msg) tea.Msg {
	switch e := msg.(type) {
	case tea.MouseClickMsg:
		if e.Button != tea.MouseLeft {
			return msg
		}
	case tea.MouseMotionMsg, tea.MouseReleaseMsg:
		button := e.(tea.MouseMsg).Mouse().Button
		if button != tea.MouseLeft && button != tea.MouseNone {
			return msg
		}
	default:
		return msg
	}
	source := m.source()
	if !source.documentPage || m.caps.protocol() == "" {
		return msg
	}
	mouse := msg.(tea.MouseMsg)
	hit := previewImagePointerMsg{mouse: mouse, word: -1}
	if m.frame == nil || m.output == nil {
		return hit
	}
	// Updating the model or finishing an encode may precede writing its
	// pixels. A pending zoom must not change the geometry under the pointer.
	m.output.mu.Lock()
	shown := m.output.shown
	m.output.mu.Unlock()
	if shown != nil && shown.pageKey == source.url {
		point := mouse.Mouse()
		hit.word = shown.wordAtCell(point.X, point.Y)
	}
	return hit
}

func (p pageProjection) wordRectangles(words []preview.Word) []image.Rectangle {
	if len(words) == 0 {
		return nil
	}
	rects := make([]image.Rectangle, len(words))
	xScale := float64(p.size.X) / float64(p.crop.Dx())
	yScale := float64(p.size.Y) / float64(p.crop.Dy())
	xOffset := float64(p.full.Min.X - p.crop.Min.X)
	yOffset := float64(p.full.Min.Y - p.crop.Min.Y)
	viewport := image.Rectangle{Max: p.size}
	for i, w := range words {
		left := int(math.Floor((w.X0*float64(p.full.Dx()) + xOffset) * xScale))
		top := int(math.Floor((w.Y0*float64(p.full.Dy()) + yOffset) * yScale))
		right := int(math.Ceil((w.X1*float64(p.full.Dx()) + xOffset) * xScale))
		bottom := int(math.Ceil((w.Y1*float64(p.full.Dy()) + yOffset) * yScale))
		rects[i] = image.Rect(left, top, right, bottom).Intersect(viewport)
	}
	return rects
}

// Mouse reports identify a cell rather than a pixel. A small glyph can fit
// inside one corner of a cell. Among intersecting words prefer the closest
// to the cell center, using the covered area to resolve a boundary.
func (f *coverFrame) wordAtCell(x, y int) int {
	left, top := (x-f.x)*f.cellWidth, (y-f.y)*f.cellHeight
	cell := image.Rect(left, top, left+f.cellWidth, top+f.cellHeight)
	cell = cell.Intersect(image.Rect(0, 0, f.pixelWidth, f.pixelHeight))
	if cell.Empty() {
		return -1
	}
	centerX, centerY := cell.Min.X+cell.Max.X, cell.Min.Y+cell.Max.Y
	best, distance, area := -1, math.MaxInt, 0
	for i, word := range f.wordRects {
		overlap := cell.Intersect(word)
		if overlap.Empty() {
			continue
		}
		dx := max(0, word.Min.X*2-centerX, centerX-word.Max.X*2)
		dy := max(0, word.Min.Y*2-centerY, centerY-word.Max.Y*2)
		d, a := dx*dx+dy*dy, overlap.Dx()*overlap.Dy()
		if d < distance || (d == distance && a > area) {
			best, distance, area = i, d, a
		}
	}
	return best
}

var pdfSelectionTint = image.NewUniform(color.NRGBA{R: 64, G: 136, B: 240, A: 96})

func selectedWordRects(rects []image.Rectangle, first, end int) []image.Rectangle {
	first, end = max(0, first), min(end, len(rects))
	if first >= end {
		return nil
	}
	return rects[first:end]
}

func (p pageProjection) highlight(img image.Image, selection []image.Rectangle) image.Image {
	var dst *image.RGBA
	for _, r := range selection {
		if r.Empty() {
			continue
		}
		if dst == nil {
			if p.crop.Size() != p.size {
				// The scaler allocated this buffer for this frame.
				dst = img.(*image.RGBA)
			} else {
				// Native-size data belongs to the cached document.
				dst = image.NewRGBA(image.Rectangle{Max: p.size})
				draw.Draw(dst, dst.Bounds(), img, img.Bounds().Min, draw.Src)
			}
		}
		draw.Draw(dst, r, pdfSelectionTint, image.Point{}, draw.Over)
	}
	if dst != nil {
		return dst
	}
	return img
}
