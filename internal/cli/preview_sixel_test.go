package cli

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"regexp"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi/sixel"
)

func decodePageSixel(t *testing.T, page image.Image) image.Image {
	t.Helper()
	var encoded bytes.Buffer
	if !encodePageSixel(&encoded, page) || strings.HasSuffix(encoded.String(), "-") {
		t.Fatal("expected a text frame without a trailing sixel line advance")
	}
	// The library decoder consumes literal columns following an RLE command
	// as part of that command. Expand runs for exact pixel comparison.
	data := regexp.MustCompile(`![0-9]+[?-~]`).ReplaceAllStringFunc(encoded.String(), func(match string) string {
		run, _ := sixel.DecodeRepeat([]byte(match))
		return strings.Repeat(string(run.Char), run.Count)
	})
	decoded, err := (&sixel.Decoder{}).Decode(strings.NewReader(data))
	if err != nil || decoded.Bounds() != page.Bounds() {
		t.Fatalf("unexpected decoded size or error: %v", err)
	}
	return decoded
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
		if encodePageSixel(&data, src) || data.Len() != 0 {
			t.Fatal("complex colors and transparency need the general Sixel encoder")
		}
	}
}
