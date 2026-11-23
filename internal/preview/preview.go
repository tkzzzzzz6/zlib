// Package preview reads a page of a local PDF or a chapter of a local EPUB.
package preview

import (
	"context"
	"fmt"
	"image"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/heartleo/zlib/internal/ebookmeta"
)

// Page holds one preview. Index is zero-based; Chapters is populated for EPUB.
type Page struct {
	Title, Text  string
	Index, Count int
	Image        image.Image
	Chapters     []string
	Words        []Word
	WordsError   string // empty when extraction succeeds, including on scanned pages
}

// Word is a selectable word in reading order. Its box uses fractions of the
// displayed raster measured from the top left. Line is zero-based.
type Word struct {
	Text           string
	X0, Y0, X1, Y1 float64
	Line           int
}

// EmptyChapter reports whether the selected EPUB spine item has no readable text.
func (p Page) EmptyChapter() bool { return len(p.Chapters) > 0 && p.Text == "" }

// Load reads one page without retaining state between calls.
func Load(ctx context.Context, path string, index int, wantImage bool) (Page, error) {
	return NewDocument(path).Load(ctx, index, wantImage)
}

// Document retains PDF metadata and recent pages for one open preview. The
// gate allows a cancelled page turn to finish releasing its processes before
// the next one starts.
type Document struct {
	path string
	gate chan struct{}
	run  toolRunner
	info os.FileInfo
	pdf  pdfDocument
}

func NewDocument(path string) *Document {
	return &Document{path: path, gate: make(chan struct{}, 1), run: runTool}
}

func sameFile(a, b os.FileInfo) bool {
	return a != nil && b != nil && a.Size() == b.Size() && a.ModTime().Equal(b.ModTime()) && os.SameFile(a, b)
}

// Load returns the requested page using the current file at the preview path.
func (d *Document) Load(ctx context.Context, index int, wantImage bool) (Page, error) {
	select {
	case d.gate <- struct{}{}:
		defer func() { <-d.gate }()
	case <-ctx.Done():
		return Page{}, ctx.Err()
	}
	if err := ctx.Err(); err != nil {
		return Page{}, err
	}
	path, err := filepath.Abs(d.path)
	if err != nil {
		return Page{}, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return Page{}, err
	}
	if !info.Mode().IsRegular() {
		return Page{}, fmt.Errorf("preview requires a regular file")
	}
	if sameFile(info, d.info) {
		return d.loadPDF(ctx, path, index, wantImage)
	}
	d.info, d.pdf = nil, pdfDocument{}
	f, err := os.Open(path)
	if err != nil {
		return Page{}, err
	}
	defer f.Close()
	format, err := ebookmeta.Detect(f, info.Size())
	if err != nil {
		return Page{}, fmt.Errorf("preview supports PDF and EPUB: %w", err)
	}
	switch format {
	case ebookmeta.FormatPDF:
		d.info = info
		return d.loadPDF(ctx, path, index, wantImage)
	case ebookmeta.FormatEPUB:
		chapters, body, err := ebookmeta.ReadEPUBChapter(ctx, f, info.Size(), index)
		if err != nil {
			return Page{}, err
		}
		for i, s := range chapters {
			chapters[i] = strings.Join(strings.Fields(terminalText(s)), " ")
		}
		return Page{Title: chapters[index], Text: terminalText(body), Index: index, Count: len(chapters), Chapters: chapters}, nil
	default:
		return Page{}, fmt.Errorf("preview supports PDF and EPUB; this file is %s", format)
	}
}

func (d *Document) loadPDF(ctx context.Context, path string, index int, wantImage bool) (Page, error) {
	page, err := d.pdf.load(ctx, path, index, wantImage, d.run)
	if err != nil {
		return Page{}, err
	}
	// Each Poppler process opens the file by name. Discard a mixture of
	// revisions if the file was replaced while those processes ran.
	current, err := os.Stat(path)
	if !sameFile(d.info, current) {
		d.info, d.pdf = nil, pdfDocument{}
		if err != nil {
			return Page{}, err
		}
		return Page{}, fmt.Errorf("PDF changed during preview; open the page again")
	}
	return page, nil
}

func terminalText(s string) string {
	return strings.TrimSpace(strings.Map(func(r rune) rune {
		if r != '\n' && r != '\t' && (unicode.IsControl(r) || unicode.Is(unicode.Bidi_Control, r)) {
			return -1
		}
		return r
	}, s))
}
