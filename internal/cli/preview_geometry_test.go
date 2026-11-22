package cli

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/png"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi/sixel"
)

func TestPDFFrameCacheChangesWithViewport(t *testing.T) {
	p := newPreviewPage("book.pdf")
	defer p.cancel()
	p.graphics, p.busy = true, false
	p.page.Image = image.NewGray(image.Rect(0, 0, 20, 40))
	renderer := coverGraphicsModel{inner: p, caps: coverCapabilities{kitty: true, cellWidth: 8, cellHeight: 16}}
	seen := make(map[coverRenderKey]bool)
	for _, state := range []struct{ zoom, x, y int }{{100, 0, 0}, {400, 0, 0}, {400, 100, 0}, {400, 100, 100}} {
		p.zoomPercent, p.panX, p.panY = state.zoom, state.x, state.y
		renderer.inner = p
		key := renderer.sourceKey()
		if key.layout == "" || seen[key] {
			t.Fatalf("viewport %+v failed to invalidate the graphics frame", state)
		}
		seen[key] = true
	}
}

func TestPDFFrameZoomAndPan(t *testing.T) {
	// Four distinct quadrants let the encoded output identify the visible
	// corner, even when the source image starts at a nonzero origin.
	page := image.NewRGBA(image.Rect(12, 16, 28, 48))
	for y := 16; y < 48; y++ {
		for x := 12; x < 28; x++ {
			c := color.RGBA{A: 255}
			if x >= 20 {
				c.R = 255
			}
			if y >= 32 {
				c.B = 255
			}
			page.SetRGBA(x, y, c)
		}
	}
	area := image.Rect(2, 3, 10, 11)
	for _, protocol := range []string{"Kitty", "Sixel"} {
		t.Run(protocol, func(t *testing.T) {
			caps := coverCapabilities{kitty: protocol == "Kitty", sixel: protocol == "Sixel", cellWidth: 2, cellHeight: 2}
			s := coverSource{img: page, area: area, documentPage: true}
			for _, tc := range []struct{ zoom, width, height int }{{0, 8, 16}, {50, 4, 8}, {200, 16, 16}} {
				s.zoomPercent = tc.zoom
				f := encodeCoverFrame(s, caps)
				if f == nil || f.pixelWidth != tc.width || f.pixelHeight != tc.height {
					t.Fatalf("%d%%: expected %dx%d bitmap", tc.zoom, tc.width, tc.height)
				}
				if f.x != area.Min.X+(area.Dx()-f.cols)/2 || f.y != area.Min.Y+(area.Dy()-f.rows)/2 {
					t.Fatalf("%d%%: image is not centered in the preview", tc.zoom)
				}
			}
			s.zoomPercent = 400
			for _, x := range []int{-100, 100} {
				for _, y := range []int{-100, 100} {
					s.panX, s.panY = x, y
					f := encodeCoverFrame(s, caps)
					if f == nil || f.cols != area.Dx() || f.rows != area.Dy() || f.x != area.Min.X || f.y != area.Min.Y {
						t.Fatal("zoomed bitmap must stay within the preview")
					}
					decoded := decodePDFTestFrame(t, f)
					r, _, b, _ := decoded.At(8, 8).RGBA()
					if (r > 0x8000) != (x > 0) || (b > 0x8000) != (y > 0) {
						t.Fatalf("pan (%d,%d) selected the wrong quadrant", x, y)
					}
				}
			}
		})
	}
}

func TestPDFBilinearViewportMatchesReference(t *testing.T) {
	src := image.NewRGBA(image.Rect(9, 13, 100, 146))
	for y := src.Rect.Min.Y; y < src.Rect.Max.Y; y++ {
		for x := src.Rect.Min.X; x < src.Rect.Max.X; x++ {
			src.SetRGBA(x, y, color.RGBA{uint8(7 * x), uint8(11 * y), uint8(x*y + 3), 255})
		}
	}
	for _, zoom := range []int{50, 125, 400} {
		page := coverSource{img: src, zoomPercent: zoom, panX: 75, panY: -25}
		got, w, h := page.pageViewport(60, 88, 80, 88)
		x, spanX, _ := pageAxis(src.Rect.Dx(), 60, 80, zoom, page.panX)
		y, spanY, _ := pageAxis(src.Rect.Dy(), 88, 88, zoom, page.panY)
		crop := src.SubImage(image.Rect(x, y, x+spanX, y+spanY).Add(src.Rect.Min))
		want := resizeCover(crop, w, h)
		for row := range h {
			for col := range w {
				a := color.RGBAModel.Convert(got.At(col, row)).(color.RGBA)
				b := color.RGBAModel.Convert(want.At(col, row)).(color.RGBA)
				for _, delta := range []int{int(a.R) - int(b.R), int(a.G) - int(b.G), int(a.B) - int(b.B)} {
					if delta < -1 || delta > 1 {
						t.Fatalf("at %d%% (%d,%d): bilinear pixel %v vs %v", zoom, col, row, a, b)
					}
				}
			}
		}
	}
}

func TestPDFSixelNativeScale(t *testing.T) {
	// At native scale the encoder reads the viewport directly, without
	// passing through resizeCover's zero-origin bitmap.
	for _, tc := range []struct {
		name         string
		origin       image.Point
		height, zoom int
	}{
		{"bottom half", image.Point{}, 16, 200},
		{"whole page with offset origin", image.Pt(12, 16), 32, 100},
	} {
		t.Run(tc.name, func(t *testing.T) {
			page := image.NewGray(image.Rect(0, 0, 16, 32).Add(tc.origin))
			for y := 16; y < 32; y++ {
				for x := 0; x < 16; x++ {
					page.SetGray(tc.origin.X+x, tc.origin.Y+y, color.Gray{Y: 255})
				}
			}
			src := coverSource{img: page, area: image.Rect(0, 0, 16, tc.height), documentPage: true, zoomPercent: tc.zoom, panY: 100}
			frame := encodeCoverFrame(src, coverCapabilities{sixel: true, cellWidth: 1, cellHeight: 1})
			if frame == nil {
				t.Fatal("expected a Sixel frame")
			}
			decoded := decodePDFTestFrame(t, frame)
			red, _, _, alpha := decoded.At(8, tc.height-1).RGBA()
			if red != 0xffff || alpha != 0xffff {
				t.Fatalf("expected an opaque white pixel at the bottom of the viewport, got red=%d alpha=%d", red, alpha)
			}
		})
	}
}

func decodePDFTestFrame(t *testing.T, f *coverFrame) image.Image {
	t.Helper()
	if f.protocol == "Sixel" {
		data := strings.TrimSuffix(strings.TrimPrefix(f.data, "\x1bP0;1q"), "\x1b\\")
		img, err := (&sixel.Decoder{}).Decode(strings.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		return img
	}
	// The small fixture fits in one Kitty chunk.
	_, chunk, _ := strings.Cut(f.data, ";")
	data, err := base64.StdEncoding.DecodeString(strings.TrimSuffix(chunk, "\x1b\\"))
	if err != nil {
		t.Fatal(err)
	}
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	return img
}
