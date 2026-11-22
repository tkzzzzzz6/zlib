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
}

// Load reads only the selected local document. PDF graphics are optional.
func Load(ctx context.Context, path string, index int, wantImage bool) (Page, error) {
	if err := ctx.Err(); err != nil {
		return Page{}, err
	}
	path, err := filepath.Abs(path)
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
		return loadPDF(ctx, path, index, wantImage, runTool)
	case ebookmeta.FormatEPUB:
		chapters, body, err := ebookmeta.ReadEPUBChapter(ctx, f, info.Size(), index)
		if err != nil {
			return Page{}, err
		}
		for i, s := range chapters {
			chapters[i] = strings.Join(strings.Fields(terminalText(s)), " ")
		}
		body = terminalText(body)
		if body == "" {
			body = "This chapter has no previewable text. Open the file in an EPUB reader to see its layout and images."
		}
		return Page{Title: chapters[index], Text: body, Index: index, Count: len(chapters), Chapters: chapters}, nil
	default:
		return Page{}, fmt.Errorf("preview supports PDF and EPUB; this file is %s", format)
	}
}

func terminalText(s string) string {
	return strings.TrimSpace(strings.Map(func(r rune) rune {
		if r != '\n' && r != '\t' && (unicode.IsControl(r) || unicode.Is(unicode.Bidi_Control, r)) {
			return -1
		}
		return r
	}, s))
}
