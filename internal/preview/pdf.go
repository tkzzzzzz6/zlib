package preview

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/png"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const maxPageSide = 1400

type toolRunner func(ctx context.Context, limit int, name string, args ...string) ([]byte, error)

func loadPDF(ctx context.Context, path string, index int, wantImage bool, run toolRunner) (Page, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	info, err := run(ctx, 256<<10, "pdfinfo", "-enc", "UTF-8", path)
	if err != nil {
		return Page{}, err
	}
	count := 0
	for line := range strings.SplitSeq(string(info), "\n") {
		if value, ok := strings.CutPrefix(line, "Pages:"); ok {
			count, _ = strconv.Atoi(strings.TrimSpace(value))
		}
	}
	if count < 1 {
		return Page{}, errors.New("pdfinfo could not determine the page count")
	}
	if index < 0 || index >= count {
		return Page{}, fmt.Errorf("choose a page from 1 to %d", count)
	}
	page := Page{Title: terminalText(filepath.Base(path)), Index: index, Count: count}
	number := strconv.Itoa(index + 1)
	var imageErr error
	if wantImage {
		var data []byte
		data, imageErr = run(ctx, 10<<20, "pdftoppm", "-f", number, "-l", number, "-singlefile", "-scale-to", strconv.Itoa(maxPageSide), "-png", path)
		if imageErr == nil {
			page.Image, imageErr = decodePage(data)
		}
	}
	data, textErr := run(ctx, 512<<10, "pdftotext", "-f", number, "-l", number, "-layout", "-enc", "UTF-8", path, "-")
	if err := ctx.Err(); err != nil {
		return Page{}, err
	}
	if textErr != nil {
		if page.Image == nil {
			return Page{}, errors.Join(imageErr, textErr)
		}
		page.Text = textErr.Error()
		return page, nil
	}
	page.Text = terminalText(string(data))
	if page.Text == "" {
		page.Text = "This page has no extractable text. Open the PDF in a reader or use a graphics-capable terminal with Poppler's pdftoppm."
		if imageErr != nil {
			page.Text += "\n" + imageErr.Error()
		}
	}
	return page, nil
}

func decodePage(data []byte) (image.Image, error) {
	cfg, err := png.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("invalid PDF page image: %w", err)
	}
	if cfg.Width > maxPageSide || cfg.Height > maxPageSide {
		return nil, errors.New("PDF page image exceeds preview dimensions")
	}
	return png.Decode(bytes.NewReader(data))
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
