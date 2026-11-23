package cli

import (
	"image"
	"image/color"

	"golang.org/x/image/draw"
)

const (
	minPageZoom = 50
	maxPageZoom = 400
)

// The same source crop and output dimensions drive rendering, selection and
// mouse hit testing, including when an earlier zoom is still on screen.
type pageProjection struct {
	full, crop image.Rectangle
	size       image.Point
}

func (s coverSource) pageViewport(fitW, fitH, maxW, maxH int) pageProjection {
	zoom := s.zoomPercent
	if zoom == 0 {
		zoom = 100
	}
	zoom = min(max(zoom, minPageZoom), maxPageZoom)
	b := s.img.Bounds()
	x, width, outW := pageAxis(b.Dx(), fitW, maxW, zoom, s.panX)
	y, height, outH := pageAxis(b.Dy(), fitH, maxH, zoom, s.panY)
	crop := image.Rect(x, y, x+width, y+height).Add(b.Min)
	return pageProjection{full: b, crop: crop, size: image.Pt(outW, outH)}
}

func (p pageProjection) raster(img image.Image) image.Image {
	if p.crop.Size() == p.size {
		if p.crop == p.full && p.full.Min == (image.Point{}) {
			return img
		}
		return pageRegion{Image: img, rect: p.crop}
	}
	dst := image.NewRGBA(image.Rectangle{Max: p.size})
	// Sampling the original image and source rectangle lets the scaler use
	// its concrete pixel buffers, including on a cropped or offset page.
	draw.ApproxBiLinear.Scale(dst, dst.Bounds(), img, p.crop, draw.Src, nil)
	return dst
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

// The Sixel encoder samples native-scale pages from (0,0).
func (p pageRegion) Bounds() image.Rectangle { return p.rect.Sub(p.rect.Min) }

func (p pageRegion) At(x, y int) color.Color {
	return p.Image.At(p.rect.Min.X+x, p.rect.Min.Y+y)
}

// Poppler returns RGB even for text-only pages. A single grayscale channel
// represents an opaque, achromatic viewport exactly and reduces encoder work.
func compactGrayPage(img image.Image) image.Image {
	rgba, ok := img.(*image.RGBA)
	if !ok {
		return img
	}
	b := rgba.Bounds()
	for y := range b.Dy() {
		row := rgba.Pix[y*rgba.Stride : y*rgba.Stride+b.Dx()*4]
		for x := 0; x < len(row); x += 4 {
			if row[x] != row[x+1] || row[x] != row[x+2] || row[x+3] != 255 {
				return img
			}
		}
	}
	gray := image.NewGray(b)
	for y := range b.Dy() {
		row := rgba.Pix[y*rgba.Stride : y*rgba.Stride+b.Dx()*4]
		for x := range b.Dx() {
			gray.Pix[y*gray.Stride+x] = row[x*4]
		}
	}
	return gray
}
