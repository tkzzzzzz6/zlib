package cli

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"regexp"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi/sixel"
	"github.com/heartleo/zlib/internal/preview"
)

func decodePageSixel(t *testing.T, page image.Image) image.Image {
	t.Helper()
	var encoded bytes.Buffer
	if !encodePageSixel(&encoded, page, nil) || strings.HasSuffix(encoded.String(), "-") {
		t.Fatal("expected a text frame without a trailing sixel line advance")
	}
	return decodeSixelPixels(t, encoded.String(), page.Bounds())
}

func decodeSixelPixels(t *testing.T, encoded string, bounds image.Rectangle) image.Image {
	t.Helper()
	// The library decoder consumes literal columns following an RLE command
	// as part of that command. Expand runs for exact pixel comparison.
	data := regexp.MustCompile(`![0-9]+[?-~]`).ReplaceAllStringFunc(encoded, func(match string) string {
		run, _ := sixel.DecodeRepeat([]byte(match))
		return strings.Repeat(string(run.Char), run.Count)
	})
	decoded, err := (&sixel.Decoder{}).Decode(strings.NewReader(data))
	if err != nil || decoded.Bounds() != bounds {
		t.Fatalf("unexpected decoded size or error: %v", err)
	}
	return decoded
}

func TestPDFSelectionPreservesCompactSixelPalette(t *testing.T) {
	const w, h = 256, 64
	for _, accents := range []int{182, 230} {
		t.Run(fmt.Sprintf("%d page colors", accents+26), func(t *testing.T) {
			page := image.NewRGBA(image.Rect(0, 0, w, h))
			draw.Draw(page, page.Bounds(), image.White, image.Point{}, draw.Src)
			// Two lines of antialiased ink, plus exact chromatic colors used
			// outside the selection. The full fixture consumes all 256 slots.
			for y := 0; y < 32; y++ {
				for x := 0; x < w; x += 2 {
					tone := uint8(x + y)
					page.SetRGBA(x, y, color.RGBA{tone, tone, tone, 255})
				}
			}
			for i := range accents {
				page.SetRGBA(i, 48, color.RGBA{uint8(i%16) * 16, uint8(i/16) * 16, 163, 255})
			}
			s := coverSource{img: page, area: page.Bounds(), documentPage: true, words: []preview.Word{
				{X0: 0, Y0: 0, X1: .5, Y1: .25},
				{X0: .25, Y0: .25, X1: .75, Y1: .5},
			}}
			caps := coverCapabilities{sixel: true, cellWidth: 1, cellHeight: 1}
			frame := func() image.Image {
				f := encodeCoverFrame(s, caps)
				data := strings.TrimSuffix(strings.TrimPrefix(f.data, "\x1bP0;1q"), "\x1b\\")
				// The compact encoder finishes with a carriage return. The
				// general encoder always appends a sixel line advance.
				if !strings.HasSuffix(data, "$") {
					t.Fatalf("selection [%d,%d) triggered full-page quantization", s.selectedFirst, s.selectedEnd)
				}
				return decodeSixelPixels(t, data, page.Bounds())
			}
			clean := frame()
			s.selectedEnd = 2
			highlighted := frame()
			for y := range h {
				for x := range w {
					if (y < 16 && x < 128) || (y >= 16 && y < 32 && x >= 64 && x < 192) {
						continue
					}
					if clean.At(x, y) != highlighted.At(x, y) {
						t.Fatalf("unselected pixel (%d,%d) changed", x, y)
					}
				}
			}
			ink := color.RGBAModel.Convert(highlighted.At(0, 0)).(color.RGBA)
			paper := color.RGBAModel.Convert(highlighted.At(1, 0)).(color.RGBA)
			contrast := int(paper.G) - int(ink.G)
			if paper == (color.RGBA{255, 255, 255, 255}) || (contrast > -90 && contrast < 90) {
				t.Fatalf("selection must be visible and keep the ink legible: ink=%v paper=%v", ink, paper)
			}
			if accents == 182 && (paper.B < 235 || paper.R > 205 || ink.R > 50) {
				t.Fatalf("a page with spare slots should display the blue highlight: ink=%v paper=%v", ink, paper)
			}
			if accents == 230 && contrast > -90 {
				t.Fatalf("a saturated palette without a close blue should use reverse video: ink=%v paper=%v", ink, paper)
			}
		})
	}
}

func TestPDFIndexedSelectionUsesClippedUnion(t *testing.T) {
	page := image.NewGray(image.Rect(0, 0, 10, 5))
	draw.Draw(page, page.Bounds(), image.White, image.Point{}, draw.Src)
	rects := []image.Rectangle{image.Rect(-3, -2, 4, 2), image.Rect(2, 1, 6, 3), image.Rect(6, 1, 14, 2)}
	var data bytes.Buffer
	if !encodePageSixel(&data, page, rects) {
		t.Fatal("a single-color page should keep the indexed encoder")
	}
	decoded := decodeSixelPixels(t, data.String(), page.Bounds())
	tinted := color.RGBAModel.Convert(decoded.At(0, 0)).(color.RGBA)
	if tinted.B <= tinted.R {
		t.Fatalf("expected blue selected paper, got %v", tinted)
	}
	for y := range 5 {
		for x := range 10 {
			want := color.RGBA{255, 255, 255, 255}
			for _, r := range rects {
				if image.Pt(x, y).In(r) {
					want = tinted
				}
			}
			if got := color.RGBAModel.Convert(decoded.At(x, y)).(color.RGBA); got != want {
				t.Fatalf("pixel (%d,%d): got %v, want %v", x, y, got, want)
			}
		}
	}
}

func TestPDFIndexedSelectionKeepsContrastingInk(t *testing.T) {
	// The sole spare register is an exact tint for the yellow paper. Both
	// the normal and inverse maps can then choose that same register for
	// the dark ochre ink if only the dominant color is checked.
	palette := []uint32{0x646400, 0x463200}
	for i := range 253 {
		palette = append(palette, uint32(i%101)<<16|uint32(i/101))
	}
	indexes := make([]byte, 64)
	for x := 0; x < 64; x += 4 {
		indexes[x] = 1
	}
	mapped := highlightSixelIndexes(indexes, 64, 1, palette, []image.Rectangle{image.Rect(0, 0, 64, 1)})
	if mapped != nil {
		paper, ink := mapped[indexes[1]], mapped[indexes[0]]
		if sixelColorDistance(paper, ink) < sixelColorDistance(palette[0], palette[1])/9 {
			t.Fatalf("high-contrast ink disappeared: mapped paper=%06x ink=%06x", paper, ink)
		}
	}
}

func TestPDFIndexedSelectionKeepsInkOnSecondBackground(t *testing.T) {
	// A mostly white selection also covers ochre ink on a yellow callout.
	// Both are distinct from white even if mapped to the same register.
	palette := []uint32{0x646464, 0x646400, 0x463200, 0x485223}
	for i := range 251 {
		palette = append(palette, uint32(i%101)<<16|uint32(i/101))
	}
	indexes := make([]byte, 64)
	for x := 48; x < 64; x++ {
		indexes[x] = 1
		if x%4 == 0 {
			indexes[x] = 2
		}
	}
	mapped := highlightSixelIndexes(indexes, 64, 1, palette, []image.Rectangle{image.Rect(0, 0, 64, 1)})
	if mapped != nil {
		paper, ink := mapped[indexes[49]], mapped[indexes[48]]
		if sixelColorDistance(paper, ink) < sixelColorDistance(palette[1], palette[2])/9 {
			t.Fatalf("ink on second background disappeared: mapped paper=%06x ink=%06x", paper, ink)
		}
	}
}

func TestPDFSelectedSixelKeepsGeneralFallback(t *testing.T) {
	for _, kind := range []string{"color gradient", "transparency"} {
		t.Run(kind, func(t *testing.T) {
			page := image.NewRGBA(image.Rect(0, 0, 48, 32))
			draw.Draw(page, page.Bounds(), image.White, image.Point{}, draw.Src)
			if kind == "color gradient" {
				for y := range 32 {
					for x := 24; x < 48; x++ {
						page.SetRGBA(x, y, color.RGBA{uint8(x * 5), uint8(y * 8), 124, 255})
					}
				}
			} else {
				page.SetRGBA(47, 31, color.RGBA{})
			}
			s := coverSource{img: page, area: page.Bounds(), documentPage: true, selectedEnd: 1,
				words: []preview.Word{{X0: 0, Y0: 0, X1: .5, Y1: .5}}}
			f := encodeCoverFrame(s, coverCapabilities{sixel: true, cellWidth: 1, cellHeight: 1})
			data := strings.TrimSuffix(strings.TrimPrefix(f.data, "\x1bP0;1q"), "\x1b\\")
			if !strings.HasSuffix(data, "-") {
				t.Fatal("expected the general Sixel color encoder")
			}
			decoded := decodeSixelPixels(t, data, page.Bounds())
			ink := color.RGBAModel.Convert(decoded.At(4, 4)).(color.RGBA)
			if ink.B < 230 || ink.B-ink.R < 40 || ink.A != 255 {
				t.Fatalf("selected paper lost its blue highlight: %v", ink)
			}
			if page.RGBAAt(4, 4) != (color.RGBA{255, 255, 255, 255}) {
				t.Fatal("general fallback wrote into the document's cached native raster")
			}
		})
	}
}

func TestPDFTextSixelQualityAndEdges(t *testing.T) {
	for _, height := range []int{1, 2, 3, 4, 5, 6, 7, 13} {
		t.Run(fmt.Sprint(height), func(t *testing.T) {
			page := image.NewRGBA(image.Rect(0, 0, 1026, height))
			for y := range height {
				for x := range 1024 {
					tone := uint8(255)
					if x%4 == 0 {
						tone = uint8((x/4 + y) % 256)
					}
					page.SetRGBA(x, y, color.RGBA{tone, tone, tone, 255})
				}
				page.SetRGBA(1024, y, color.RGBA{16, 94, 204, 255})
				page.SetRGBA(1025, y, color.RGBA{250, 112, 7, 255})
			}
			decoded := decodePageSixel(t, page)
			for y := range height {
				for x := range page.Bounds().Dx() {
					want := page.RGBAAt(x, y)
					got := color.RGBAModel.Convert(decoded.At(x, y)).(color.RGBA)
					if x < 1024 {
						delta := int(got.R) - int(want.R)
						if delta < -5 || delta > 5 || got.G != got.R || got.B != got.R || got.A != 255 {
							t.Fatalf("gray (%d,%d): got %v, want %v within five levels", x, y, got, want)
						}
					} else {
						expected := color.RGBAModel.Convert(sixel.FromColor(want)).(color.RGBA)
						if got != expected {
							t.Fatalf("color (%d,%d): got %v, want %v", x, y, got, expected)
						}
					}
				}
			}
		})
	}
}

func TestPDFSixelKeepsGradientOnWhitePage(t *testing.T) {
	page := image.NewGray(image.Rect(0, 0, 192, 64))
	for y := 0; y < 64; y++ {
		for x := 0; x < 192; x++ {
			tone := uint8(255)
			if x >= 64 && x < 128 {
				tone = uint8((x-64)*4 + y%4)
			}
			page.SetGray(x, y, color.Gray{Y: tone})
		}
	}
	decoded := decodePageSixel(t, page)
	for y := 0; y < 64; y++ {
		for x := 64; x < 128; x++ {
			want := color.RGBAModel.Convert(sixel.FromColor(page.GrayAt(x, y))).(color.RGBA)
			got := color.RGBAModel.Convert(decoded.At(x, y)).(color.RGBA)
			if got != want {
				t.Fatalf("gradient pixel (%d,%d): got %v, want %v", x, y, got, want)
			}
		}
	}
}

func TestPDFTextSixelFallback(t *testing.T) {
	photo := image.NewRGBA(image.Rect(0, 0, 32, 32))
	for y := 0; y < 32; y++ {
		for x := 0; x < 32; x++ {
			photo.SetRGBA(x, y, color.RGBA{uint8(x * 8), uint8(y * 8), 127, 255})
		}
	}
	for _, src := range []image.Image{photo, image.NewRGBA(image.Rect(0, 0, 4, 4))} {
		var data bytes.Buffer
		if encodePageSixel(&data, src, nil) || data.Len() != 0 {
			t.Fatal("complex colors and transparency need the general Sixel encoder")
		}
	}
}
