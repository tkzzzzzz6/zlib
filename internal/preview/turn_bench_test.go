package preview

import (
	"context"
	"fmt"
	"os"
	"slices"
	"testing"
	"time"
)

type capturedPDFTool struct {
	start, end time.Time
	data       []byte
	calls      int
}

func (c *capturedPDFTool) run(ctx context.Context, limit int, name string, args ...string) ([]byte, error) {
	c.start = time.Now()
	data, err := runTool(ctx, limit, name, args...)
	c.end = time.Now()
	c.calls++
	c.data = data
	return data, err
}

// BenchmarkPDFUncachedTurn measures graphical page 2 on a fresh document with
// page 1 open and a warm filesystem. Pass the absolute path to a local PDF
// with at least two pages, since Go runs tests in the package directory:
//
//	ZLIB_BENCH_PDF="$PWD/sample.pdf" go test ./internal/preview -run '^$' -bench '^BenchmarkPDFUncachedTurn$' -benchtime=20x -count=1
//
// The Poppler intervals include process startup and stdout transfer and run
// concurrently. Go-only decode and layout are separate replays on the exact
// captured bytes. Do not sum these metrics; ready measures the actual wall
// time until Document.Load returns. Terminal rendering starts after ready.
func BenchmarkPDFUncachedTurn(b *testing.B) {
	b.StopTimer()
	path := os.Getenv("ZLIB_BENCH_PDF")
	if path == "" {
		b.Skip("set ZLIB_BENCH_PDF to a local PDF with at least two pages")
	}
	ctx := b.Context()
	// Warm page 2 once, including Poppler and the OS file cache. Its decoded
	// page is discarded and cannot be reused by a measured document.
	warm, err := Load(ctx, path, 1, true)
	if err != nil {
		b.Fatalf("cannot preview the second PDF page: %v", err)
	}
	if warm.Count < 2 || warm.Index != 1 || warm.Image == nil {
		b.Fatal("a PDF with a renderable second page and Poppler's pdftoppm are required")
	}
	if warm.WordsError != "" {
		b.Fatal(warm.WordsError)
	}
	metrics := make(map[string][]time.Duration)
	record := func(stage string, d time.Duration) { metrics[stage] = append(metrics[stage], d) }
	for range b.N {
		doc := NewDocument(path)
		if _, err := doc.Load(ctx, 0, true); err != nil {
			b.Fatal(err)
		}
		var raster, bbox capturedPDFTool
		doc.run = func(ctx context.Context, limit int, name string, args ...string) ([]byte, error) {
			switch {
			case name == "pdftoppm":
				return raster.run(ctx, limit, name, args...)
			case slices.Contains(args, "-bbox-layout"):
				return bbox.run(ctx, limit, name, args...)
			default:
				return nil, fmt.Errorf("unexpected PDF tool on the page turn: %s", name)
			}
		}
		b.StartTimer()
		start := time.Now()
		page, err := doc.Load(ctx, 1, true)
		ready := time.Since(start)
		b.StopTimer()
		if err != nil || page.Image == nil || page.WordsError != "" || raster.calls != 1 || bbox.calls != 1 {
			b.Fatalf("page turn failed: %v %s (raster calls %d, bbox calls %d)", err, page.WordsError, raster.calls, bbox.calls)
		}
		record("ready", ready)
		record("pdftoppm", raster.end.Sub(raster.start))
		record("pdftotext", bbox.end.Sub(bbox.start))
		// Intersection of the two subprocess/pipe intervals in this run.
		lastStart := max(raster.start.Sub(start), bbox.start.Sub(start))
		firstEnd := min(raster.end.Sub(start), bbox.end.Sub(start))
		record("tool-overlap", max(0, firstEnd-lastStart))

		start = time.Now()
		img, err := decodePage(raster.data)
		record("decode-replay", time.Since(start))
		if err != nil || img.Bounds() != page.Image.Bounds() {
			b.Fatalf("raster replay mismatch: %v", err)
		}
		start = time.Now()
		layer, err := readWordLayer(bbox.data)
		if err != nil {
			b.Fatal(err)
		}
		layer.fit(img.Bounds())
		record("layout-replay", time.Since(start))
		if len(layer.words) != len(page.Words) {
			b.Fatal("word layer replay mismatch")
		}
	}
	for stage, samples := range metrics {
		slices.Sort(samples)
		for _, p := range []int{50, 95} {
			rank := (p*len(samples) + 99) / 100
			b.ReportMetric(float64(samples[rank-1])/float64(time.Millisecond), fmt.Sprintf("%s-p%d-ms", stage, p))
		}
	}
}
