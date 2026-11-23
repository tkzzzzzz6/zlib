package preview

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"image"
	"io"
	"math"
	"strconv"
	"strings"
)

const maxWordLayerBytes = 2 << 20

// TextFromWords joins a reading-order selection, preserving PDF line breaks.
func TextFromWords(words []Word) string {
	var text strings.Builder
	for i, word := range words {
		if i > 0 {
			if words[i-1].Line == word.Line {
				text.WriteByte(' ')
			} else {
				text.WriteByte('\n')
			}
		}
		text.WriteString(word.Text)
	}
	return text.String()
}

type pdfWordLayer struct {
	width, height float64
	words         []Word
	text          string
	fitted        bool
}

func coordinate(element xml.StartElement, name string) (float64, bool) {
	for _, attr := range element.Attr {
		if attr.Name.Local == name {
			v, err := strconv.ParseFloat(attr.Value, 64)
			return v, err == nil && !math.IsNaN(v) && !math.IsInf(v, 0)
		}
	}
	return 0, false
}

// readWordLayer consumes pdftotext -bbox-layout XHTML. Each line element gets
// its own ID, even if Poppler starts a new flow or column.
func readWordLayer(data []byte) (*pdfWordLayer, error) {
	if len(data) > maxWordLayerBytes {
		return nil, errors.New("PDF text layer exceeds the preview limit")
	}
	dec := xml.NewDecoder(bytes.NewReader(data))
	var layer pdfWordLayer
	line := -1
	inPage, inLine := false, false
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("invalid PDF text layer: %w", err)
		}
		switch el := tok.(type) {
		case xml.StartElement:
			switch el.Name.Local {
			case "page":
				if layer.width > 0 {
					return nil, errors.New("expected one PDF page of text")
				}
				w, wok := coordinate(el, "width")
				h, hok := coordinate(el, "height")
				if !wok || !hok || w <= 0 || h <= 0 {
					return nil, errors.New("invalid PDF text layer dimensions")
				}
				layer.width, layer.height, inPage = w, h, true
			case "line":
				if inPage {
					line++
					inLine = true
				}
			case "word":
				if !inPage || !inLine {
					continue
				}
				var content string
				if err := dec.DecodeElement(&content, &el); err != nil {
					return nil, fmt.Errorf("invalid PDF word: %w", err)
				}
				text := strings.Join(strings.Fields(terminalText(content)), " ")
				x0, a := coordinate(el, "xMin")
				y0, b := coordinate(el, "yMin")
				x1, c := coordinate(el, "xMax")
				y1, d := coordinate(el, "yMax")
				if text != "" && a && b && c && d && x1 > x0 && y1 > y0 {
					layer.words = append(layer.words, Word{Text: text, X0: x0, Y0: y0, X1: x1, Y1: y1, Line: line})
				}
			}
		case xml.EndElement:
			switch el.Name.Local {
			case "line":
				inLine = false
			case "page":
				inPage = false
			}
		}
	}
	if layer.width == 0 {
		return nil, errors.New("missing PDF text layer dimensions")
	}
	return &layer, nil
}

func (l *pdfWordLayer) fit(raster image.Rectangle) {
	if l.fitted {
		return
	}
	l.fitted = true
	w, h := l.width, l.height
	// Poppler's word boxes already follow page rotation. Versions that
	// report unrotated MediaBox dimensions need the axes swapped for 90/270.
	if (w < h && raster.Dx() > raster.Dy()) || (w > h && raster.Dx() < raster.Dy()) {
		w, h = h, w
	}
	visible := l.words[:0]
	for _, word := range l.words {
		word.X0, word.X1 = max(0, word.X0/w), min(1, word.X1/w)
		word.Y0, word.Y1 = max(0, word.Y0/h), min(1, word.Y1/h)
		if word.X0 >= word.X1 || word.Y0 >= word.Y1 {
			continue
		}
		visible = append(visible, word)
	}
	l.words, l.text = visible, TextFromWords(visible)
}
