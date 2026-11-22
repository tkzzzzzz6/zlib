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
	seen := make(map[string]bool)
	for _, state := range []struct{ zoom, x, y int }{{100, 0, 0}, {400, 0, 0}, {400, 100, 0}, {400, 100, 100}} {
		p.zoomPercent, p.panX, p.panY = state.zoom, state.x, state.y
		renderer.inner = p
		key := renderer.sourceKey()
		if key == "" || seen[key] {
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
