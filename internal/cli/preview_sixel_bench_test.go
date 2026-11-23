package cli

import (
	"image"
	"os"
	"testing"

	"github.com/heartleo/zlib/internal/preview"
)

// Benchmark a complete Sixel frame from a decoded page. Load the local PDF and
// choose the same six-word, cross-line selection outside the timed loop.
func BenchmarkPDFSixelSelection(b *testing.B) {
	path := os.Getenv("ZLIB_BENCH_PDF")
	if path == "" {
		b.Skip("set ZLIB_BENCH_PDF to a local PDF with multiple lines of text on page one")
	}
	page, err := preview.Load(b.Context(), path, 0, true)
	if err != nil {
		b.Fatal(err)
	}
	if page.Image == nil {
		b.Fatal("page one needs a renderable PDF image and Poppler's pdftoppm")
	}
	if page.WordsError != "" {
		b.Fatal(page.WordsError)
	}
	// Start near the first third of the page and leave three words on either
	// side of a line break, as in a drag across the end of a line.
	line := 0
	for i := 3; i+3 <= len(page.Words); i++ {
		if page.Words[i].Line != page.Words[i-1].Line {
			line = i
			if i >= len(page.Words)/3 {
				break
			}
		}
	}
	if line == 0 {
		b.Fatal("page one needs a line break with at least three words on either side")
	}
	src := coverSource{
		documentPage: true, img: page.Image, words: page.Words,
		area: image.Rect(1, 3, 149, 45), zoomPercent: 100,
	}
	caps := coverCapabilities{sixel: true, cellWidth: 12, cellHeight: 24}
	for _, selection := range []struct {
		name       string
		first, end int
	}{
		{"clean", 0, 0},
		{"one-word", line - 3, line - 2},
		{"six-words", line - 3, line + 3},
	} {
		b.Run(selection.name, func(b *testing.B) {
			s := src
			s.selectedFirst, s.selectedEnd = selection.first, selection.end
			b.ReportAllocs()
			var frame *coverFrame
			for b.Loop() {
				frame = encodeCoverFrame(s, caps)
			}
			if frame == nil {
				b.Fatal("no Sixel frame")
			}
			b.ReportMetric(float64(len(frame.data)), "wire-B/op")
		})
	}
}
