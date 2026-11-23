package ebookmeta

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/url"
	"path"
	"strings"
	"unicode"
	"unicode/utf8"
)

// ReadEPUBChapter reads the selected linear spine item and returns the chapter
// titles in reading order. All links resolve within the open archive.
func ReadEPUBChapter(ctx context.Context, src io.ReaderAt, size int64, index int) ([]string, string, error) {
	if err := ctx.Err(); err != nil {
		return nil, "", err
	}
	zr, err := zip.NewReader(src, size)
	if err != nil {
		return nil, "", err
	}
	if len(zr.File) > 32768 {
		return nil, "", errors.New("EPUB has too many archive entries to preview")
	}
	b := &epubBook{zr: zr}
	parts := make(map[string]*zip.File, len(zr.File))
	for _, f := range zr.File {
		parts[f.Name] = f
	}
	read := func(name string, limit int64) ([]byte, error) {
		f := parts[name]
		if f == nil {
			return nil, fmt.Errorf("%w: EPUB preview entry is missing", ErrMalformed)
		}
		return readZipFile(f, limit)
	}
	container, err := read("META-INF/container.xml", 1<<20)
	if err != nil {
		return nil, "", err
	}
	b.opfPath, err = containerRootfile(container)
	if err != nil {
		return nil, "", err
	}
	if b.encrypted(b.opfPath) {
		return nil, "", ErrEncrypted
	}
	opf, err := read(b.opfPath, 2<<20)
	if err != nil {
		return nil, "", err
	}
	var pkg struct {
		XMLName xml.Name `xml:"package"`
		Items   []struct {
			ID    string `xml:"id,attr"`
			Href  string `xml:"href,attr"`
			Media string `xml:"media-type,attr"`
		} `xml:"manifest>item"`
		Spine []struct {
			ID     string `xml:"idref,attr"`
			Linear string `xml:"linear,attr"`
		} `xml:"spine>itemref"`
	}
	dec := xml.NewDecoder(bytes.NewReader(opf))
	dec.CharsetReader, dec.Entity = charsetReader, xml.HTMLEntity
	if err := dec.Decode(&pkg); err != nil {
		return nil, "", fmt.Errorf("%w: EPUB package: %v", ErrMalformed, err)
	}
	if len(pkg.Spine) > 4096 {
		return nil, "", errors.New("EPUB has too many chapters to preview")
	}
	hrefs := make(map[string]string, len(pkg.Items))
	for _, item := range pkg.Items {
		if item.Media == "application/xhtml+xml" || item.Media == "text/html" {
			hrefs[item.ID] = item.Href
		}
	}
	var entries, titles []string
	for _, ref := range pkg.Spine {
		href := hrefs[ref.ID]
		if href == "" || strings.EqualFold(ref.Linear, "no") {
			continue
		}
		u, err := url.Parse(href)
		if err != nil || u.IsAbs() || u.Host != "" || strings.HasPrefix(u.Path, "/") {
			return nil, "", errors.New("EPUB chapter references a location outside the archive")
		}
		entry, err := b.resolve(href)
		if err != nil {
			return nil, "", err
		}
		entries = append(entries, entry)
		titles = append(titles, strings.TrimSuffix(path.Base(entry), path.Ext(entry)))
	}
	if len(entries) == 0 {
		return nil, "", errors.New("EPUB has no readable text chapters")
	}
	if index < 0 || index >= len(entries) {
		return nil, "", fmt.Errorf("choose a chapter from 1 to %d", len(entries))
	}
	if b.encrypted(entries[index]) {
		return nil, "", ErrEncrypted
	}
	// Some books use opaque filenames, so sample headings for the chapter
	// chooser without decompressing every chapter. Keep an aggregate budget.
	budget := int64(2 << 20)
	var body string
	for i, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, "", err
		}
		f := parts[entry]
		if i != index && (budget == 0 || f == nil) {
			continue
		}
		if f == nil {
			return nil, "", fmt.Errorf("%w: chapter %d is missing", ErrMalformed, i+1)
		}
		r, err := f.Open()
		if err != nil {
			if i != index {
				continue
			}
			return nil, "", err
		}
		limit := min(budget, 8192)
		if i == index {
			limit = 2 << 20
		}
		sample, readErr := io.ReadAll(io.LimitReader(r, limit))
		r.Close()
		truncated := f.UncompressedSize64 > uint64(limit)
		if truncated {
			// End a text token cleanly when the byte limit splits a long
			// paragraph or a multi-byte character in that paragraph.
			for range utf8.UTFMax {
				r, n := utf8.DecodeLastRune(sample)
				if r != utf8.RuneError || n != 1 {
					break
				}
				sample = sample[:len(sample)-1]
			}
			sample = append(sample, '<')
		}
		title, text, parseErr := chapterText(ctx, sample)
		if err := ctx.Err(); err != nil {
			return nil, "", err
		}
		if title != "" {
			titles[i] = title
		}
		if i != index {
			budget -= min(budget, int64(len(sample)))
			continue
		}
		if readErr != nil {
			return nil, "", readErr
		}
		if parseErr != nil && !truncated {
			return nil, "", fmt.Errorf("read EPUB chapter: %w", parseErr)
		}
		body = text
		if truncated && !strings.HasSuffix(body, chapterTruncated) {
			body += "\n" + chapterTruncated
		}
	}
	return titles, body, nil
}

const chapterTruncated = "[Chapter preview truncated]"

// chapterText preserves paragraph boundaries and spaces across inline tags.
// Neither attributes nor content such as scripts reach the visible body.
func chapterText(ctx context.Context, data []byte) (string, string, error) {
	dec := xml.NewDecoder(bytes.NewReader(data))
	dec.Strict, dec.Entity, dec.CharsetReader = false, xml.HTMLEntity, charsetReader
	var title, heading, body strings.Builder
	var inBody, inTitle, inHeading, skip int
	var pendingSpace bool
	var parseErr error
	const maxText = 128 << 10
	for body.Len() < maxText {
		if err := ctx.Err(); err != nil {
			return "", "", err
		}
		token, err := dec.Token()
		if err != nil {
			if err != io.EOF {
				parseErr = err
			}
			break
		}
		switch t := token.(type) {
		case xml.StartElement:
			name := strings.ToLower(t.Name.Local)
			switch {
			case skip > 0 || name == "script" || name == "style" || name == "svg":
				skip++
			case name == "body":
				inBody++
			case name == "title":
				inTitle++
			case name == "h1" || name == "h2":
				if heading.Len() == 0 {
					inHeading++
				}
			}
			if skip == 0 && inBody > 0 && isChapterBreak(name) {
				body.WriteByte('\n')
				pendingSpace = false
			}
		case xml.EndElement:
			name := strings.ToLower(t.Name.Local)
			switch {
			case skip > 0:
				skip--
			case name == "body":
				inBody--
			case name == "title":
				inTitle--
			case name == "h1" || name == "h2":
				inHeading = 0
			}
			if skip == 0 && inBody > 0 && isChapterBreak(name) {
				body.WriteByte('\n')
				pendingSpace = false
			}
		case xml.CharData:
			if skip > 0 {
				continue
			}
			if inTitle > 0 {
				title.Write(t)
			}
			if inHeading > 0 {
				heading.Write(t)
			}
			if inBody > 0 {
				for _, r := range string(t) {
					if body.Len() >= maxText {
						break
					}
					if unicode.IsSpace(r) {
						pendingSpace = true
						continue
					}
					if pendingSpace {
						body.WriteByte(' ')
						pendingSpace = false
					}
					body.WriteRune(r)
				}
			}
		}
	}
	// Prefer a visible heading over generic titles shared by every chapter.
	label := strings.Join(strings.Fields(heading.String()), " ")
	if label == "" {
		label = strings.Join(strings.Fields(title.String()), " ")
	}
	if runes := []rune(label); len(runes) > 160 {
		label = string(runes[:160]) + "…"
	}
	var lines []string
	for line := range strings.SplitSeq(body.String(), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			lines = append(lines, line)
		}
	}
	if body.Len() >= maxText {
		lines = append(lines, chapterTruncated)
	}
	return label, strings.Join(lines, "\n"), parseErr
}

func isChapterBreak(name string) bool {
	switch name {
	case "p", "div", "section", "article", "h1", "h2", "h3", "h4", "h5", "h6", "li", "br", "tr", "blockquote", "pre":
		return true
	}
	return false
}
