package cli

import "image"

const (
	minPageZoom = 50
	maxPageZoom = 400
)

// pageViewport crops the source before scaling so a zoomed page only needs
// a bitmap as large as the visible viewport. Covers always use their full image.
func (s coverSource) pageViewport(fitW, fitH, maxW, maxH int) (image.Image, int, int) {
	if s.zoomPercent == 0 || s.zoomPercent == 100 {
		return s.img, fitW, fitH
	}
	zoom := min(max(s.zoomPercent, minPageZoom), maxPageZoom)
	b := s.img.Bounds()
	x, width, outW := pageAxis(b.Dx(), fitW, maxW, zoom, s.panX)
	y, height, outH := pageAxis(b.Dy(), fitH, maxH, zoom, s.panY)
	if width == b.Dx() && height == b.Dy() {
		return s.img, outW, outH
	}
	crop := image.Rect(x, y, x+width, y+height).Add(b.Min)
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

func (p pageRegion) Bounds() image.Rectangle { return p.rect }
