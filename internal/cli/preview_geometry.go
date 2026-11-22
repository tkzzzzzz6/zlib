package cli

import (
	"image"
	"image/color"
)

const (
	minPageZoom = 50
	maxPageZoom = 400
)

// pageViewport crops the source before scaling so a zoomed page only needs
// a bitmap as large as the visible viewport. Covers always use their full image.
func (s coverSource) pageViewport(fitW, fitH, maxW, maxH int) (image.Image, int, int) {
	zoom := s.zoomPercent
	if zoom == 0 {
		zoom = 100
	}
	zoom = min(max(zoom, minPageZoom), maxPageZoom)
	b := s.img.Bounds()
	x, width, outW := pageAxis(b.Dx(), fitW, maxW, zoom, s.panX)
	y, height, outH := pageAxis(b.Dy(), fitH, maxH, zoom, s.panY)
	crop := image.Rect(x, y, x+width, y+height).Add(b.Min)
	if crop == b && b.Min == (image.Point{}) {
		return s.img, outW, outH
	}
	return pageRegion{Image: s.img, rect: crop}, outW, outH
}

// pageAxis maps the visible output pixels to a span of the source image.
// Pan is a fraction of the available overflow, so its endpoints always reach
// the first/last source pixel regardless of page or terminal aspect ratio.
func pageAxis(source, fitted, available, zoom, pan int) (offset, span, output int) {
	scaled := max(1, (fitted*zoom+50)/100)
	if scaled <= available {
		return 0, source, scaled
	}
	span = max(1, (source*available+scaled/2)/scaled)
	percent := min(max(pan, -100), 100) + 100
	offset = ((source-span)*percent + 100) / 200
	return offset, span, available
}

type pageRegion struct {
	image.Image
	rect image.Rectangle
}

// The Sixel encoder samples from (0,0), including when no resize is needed.
func (p pageRegion) Bounds() image.Rectangle { return p.rect.Sub(p.rect.Min) }

func (p pageRegion) At(x, y int) color.Color {
	return p.Image.At(p.rect.Min.X+x, p.rect.Min.Y+y)
}
