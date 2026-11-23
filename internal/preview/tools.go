package preview

import (
	"context"
	"errors"
	"iter"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
)

// bboxAlternatives searches only after the normal pdftotext has failed.
// Skip the executable already tried, duplicate entries, relative paths and
// the working directory (including aliases of it).
func bboxAlternatives(tried string) iter.Seq[string] {
	return func(yield func(string) bool) {
		cwd, err := os.Stat(".")
		if err != nil {
			return
		}
		var seen []os.FileInfo
		if tried != "" {
			if info, err := os.Stat(tried); err == nil {
				seen = append(seen, info)
			}
		}
		for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
			if !filepath.IsAbs(dir) {
				continue
			}
			bin, err := exec.LookPath(filepath.Join(dir, "pdftotext"))
			if err != nil {
				continue
			}
			parent, err := os.Stat(dir)
			if err != nil || os.SameFile(cwd, parent) {
				continue
			}
			info, err := os.Stat(bin)
			if err != nil || slices.ContainsFunc(seen, func(f os.FileInfo) bool { return os.SameFile(f, info) }) {
				continue
			}
			seen = append(seen, info)
			if !yield(bin) {
				return
			}
		}
	}
}

func usageExit(err error) bool {
	var status interface{ ExitCode() int }
	return errors.As(err, &status) && status.ExitCode() == 99
}

func tryAnotherWordTool(err error) bool {
	return usageExit(err) || errors.Is(err, exec.ErrNotFound) || errors.Is(err, exec.ErrDot) || errors.Is(err, os.ErrNotExist)
}

func (d *pdfDocument) wordLayer(ctx context.Context, page, path string, run toolRunner) (*pdfWordLayer, error) {
	const name = "pdftotext"
	first := d.wordTool
	if first == "" {
		first = name
	}
	extract := func(tool string) (*pdfWordLayer, error) {
		data, err := run(ctx, maxWordLayerBytes, tool, "-f", page, "-l", page, "-bbox-layout", "-enc", "UTF-8", path, "-")
		if err != nil {
			return nil, err
		}
		return readWordLayer(data)
	}
	layer, err := extract(first)
	if !tryAnotherWordTool(err) {
		return layer, err
	}
	// Xpdf ships a tool with this name that rejects Poppler's bbox option.
	// Try the actual page so a successful probe supplies the requested layer.
	var tried string
	if usageExit(err) {
		tried, _ = exec.LookPath(first)
	}
	d.wordTool = ""
	for candidate := range bboxAlternatives(tried) {
		layer, err = extract(candidate)
		if err == nil {
			d.wordTool = candidate
			return layer, nil
		}
		if !tryAnotherWordTool(err) {
			return nil, err
		}
	}
	return nil, errors.New("install Poppler's pdftotext with -bbox-layout support and put it on PATH to select PDF text")
}
