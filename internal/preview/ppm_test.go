package preview

import (
	"bytes"
	"image"
	"strings"
	"testing"
)

func TestPPMSeparatesHeaderFromEveryPossibleFirstPixel(t *testing.T) {
	for _, header := range []string{
		"P6\n2 1\n255\n",
		"P6\t# dimensions\n 2\r\n1\n# maxval\n255\r\n",
		"P6 2 1 255\r",
	} {
		for first := range 256 {
			data := append([]byte(header), byte(first), 0, 17, 255, 35, 128)
			got, err := decodePage(data)
			if err != nil {
				t.Fatalf("%q first byte %d: %v", header, first, err)
			}
			rgba, ok := got.(*image.RGBA)
			want := []byte{byte(first), 0, 17, 255, 255, 35, 128, 255}
			if !ok || rgba.Bounds() != image.Rect(0, 0, 2, 1) || !bytes.Equal(rgba.Pix, want) {
				t.Fatalf("%q first byte %d: unexpected RGBA pixels", header, first)
			}
		}
	}
}

func TestPPMRejectsTruncatedOversizeAndMalformedPages(t *testing.T) {
	for _, s := range []string{
		"", "P3 1 1 255\n123", "P61 1 255\n123", "P6 1x1 255\n123",
		"P6 0 1 255\n", "P6 1 -1 255\n", "P6 1401 1 255\n",
		"P6 1 99999999999999999 255\n", "P6 1 1 65535\n123456",
		"P6 1 1 255", "P6 1 1 255\n12", "P6 1 1 255\n1234",
		"P6\n#" + strings.Repeat("a", maxPPMHeader) + "\n1 1 255\n123",
	} {
		if _, err := decodePage([]byte(s)); err == nil {
			t.Fatalf("accepted invalid PPM %q", s)
		}
	}
}
