package preview

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	maxPageSide = 1400
	// Each decoded image has at most maxPageSide squared pixels. Retain
	// only three so a long reading session has a fixed memory ceiling.
	recentPDFPages = 3
)

type toolRunner func(ctx context.Context, limit int, name string, args ...string) ([]byte, error)

type pdfDocument struct {
	count int
	pages []pdfPage // most recently used first
}

type pdfPage struct {
	index  int
	image  image.Image
	text   string
	textOK bool // a scan can have a successful but empty text extraction
}

func pdfPageCount(ctx context.Context, path string, run toolRunner) (int, error) {
	info, err := run(ctx, 256<<10, "pdfinfo", "-enc", "UTF-8", path)
	if err != nil {
		return 0, err
	}
	count := 0
	for line := range strings.SplitSeq(string(info), "\n") {
		if value, ok := strings.CutPrefix(line, "Pages:"); ok {
			count, _ = strconv.Atoi(strings.TrimSpace(value))
		}
	}
	if count < 1 {
		return 0, errors.New("pdfinfo could not determine the page count")
	}
	return count, nil
}

func (d *pdfDocument) load(ctx context.Context, path string, index int, wantImage bool, run toolRunner) (Page, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if d.count == 0 {
		count, err := pdfPageCount(ctx, path, run)
		if err != nil {
			return Page{}, err
		}
		d.count = count
	}
	if index < 0 || index >= d.count {
		return Page{}, fmt.Errorf("choose a page from 1 to %d", d.count)
	}
	entry := pdfPage{index: index}
	pos := slices.IndexFunc(d.pages, func(p pdfPage) bool { return p.index == index })
	if pos >= 0 {
		entry = d.pages[pos]
	}
	number := strconv.Itoa(index + 1)
	var imageErr, textErr error
	var render sync.WaitGroup
	if wantImage && entry.image == nil {
		render.Go(func() {
			var data []byte
			// Poppler writes a lossless binary PPM by default. The pipe
			// costs less than PNG compression and decompression per turn.
			data, imageErr = run(ctx, maxPPMBytes, "pdftoppm", "-f", number, "-l", number, "-singlefile", "-scale-to", strconv.Itoa(maxPageSide), path)
			if imageErr == nil {
				entry.image, imageErr = decodePage(data)
			}
		})
	}
	if !entry.textOK {
		var data []byte
		data, textErr = run(ctx, 512<<10, "pdftotext", "-f", number, "-l", number, "-layout", "-enc", "UTF-8", path, "-")
		if textErr == nil {
			entry.text, entry.textOK = terminalText(string(data)), true
		}
	}
	render.Wait()
	if err := ctx.Err(); err != nil {
		return Page{}, err
	}
	page := Page{Title: terminalText(filepath.Base(path)), Index: index, Count: d.count, Text: entry.text}
	if wantImage {
		page.Image = entry.image
	}
	if !entry.textOK && page.Image == nil {
		return Page{}, errors.Join(imageErr, textErr)
	}
	if pos < 0 {
		if len(d.pages) < recentPDFPages {
			d.pages = append(d.pages, pdfPage{})
		}
		pos = len(d.pages) - 1
	}
	copy(d.pages[1:pos+1], d.pages[:pos])
	d.pages[0] = entry
	if textErr != nil {
		page.Text = textErr.Error()
		return page, nil
	}
	if page.Text == "" {
		page.Text = "This page has no extractable text. Open the PDF in a reader or use a graphics-capable terminal with Poppler's pdftoppm."
		if imageErr != nil {
			page.Text += "\n" + imageErr.Error()
		}
	}
	return page, nil
}

// toolOutput closes down the child as soon as it exceeds the output budget.
type toolOutput struct {
	buf    bytes.Buffer
	limit  int
	cancel context.CancelFunc
}

func (o *toolOutput) Write(p []byte) (int, error) {
	if len(p) > o.limit-o.buf.Len() {
		o.cancel()
		return 0, errors.New("output exceeds preview limit")
	}
	return o.buf.Write(p)
}

func runTool(ctx context.Context, limit int, name string, args ...string) ([]byte, error) {
	bin, err := exec.LookPath(name)
	if err != nil {
		return nil, fmt.Errorf("install Poppler and put %s on PATH to preview this PDF", name)
	}
	child, cancel := context.WithCancel(ctx)
	defer cancel()
	cmd := exec.CommandContext(child, bin, args...)
	cmd.Env = append(cmd.Environ(), "LC_ALL=C")
	cmd.WaitDelay = time.Second
	out := &toolOutput{limit: limit, cancel: cancel}
	cmd.Stdout = out
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if child.Err() != nil {
			return nil, fmt.Errorf("%s output exceeds the preview limit", name)
		}
		return nil, fmt.Errorf("%s could not read this PDF; try opening it in a PDF reader: %w", name, err)
	}
	return out.buf.Bytes(), nil
}
