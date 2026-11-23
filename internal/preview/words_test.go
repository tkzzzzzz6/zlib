package preview

import (
	"fmt"
	"image"
	"math"
	"os/exec"
	"strconv"
	"strings"
	"testing"
)

func TestWordLayerUsesLinesAndClipsToTheRaster(t *testing.T) {
	const source = `<html><body><doc><page width="200" height="100">
	<flow><block><line><word xMin="-10" yMin="2" xMax="20" yMax="10">A&amp;B</word>
	<word xMin="25" yMin="2" xMax="60" yMax="10">x&#x202e;y</word></line></block></flow>
	<flow><block><line><word xMin="70" yMin="15" xMax="250" yMax="110">Next</word>
	<word xMin="201" yMin="2" xMax="250" yMax="10">outside</word>
	<word xMin="NaN" yMin="2" xMax="50" yMax="10">invalid</word></line></block></flow></page></doc></body></html>`
	layer, err := readWordLayer([]byte(source))
	if err != nil {
		t.Fatal(err)
	}
	layer.fit(image.Rect(0, 0, 1400, 700))
	layer.fit(image.Rect(0, 0, 1400, 700)) // cached returns keep normalized coordinates
	if len(layer.words) != 3 || layer.text != "A&B xy\nNext" {
		t.Fatalf("words = %+v, text = %q", layer.words, layer.text)
	}
	if got := layer.words[0]; got != (Word{Text: "A&B", X0: 0, Y0: .02, X1: .1, Y1: .1, Line: 0}) {
		t.Fatalf("first = %+v", got)
	}
	if got := layer.words[2]; got.X1 != 1 || got.Y1 != 1 || got.Line != 1 {
		t.Fatalf("last = %+v", got)
	}
	for _, bad := range []string{"", "<html/>", `<page width="inf" height="12"/>`, `<page width="0" height="12"/>`, `<page width="12" height="12">`} {
		if _, err := readWordLayer([]byte(bad)); err == nil {
			t.Errorf("accepted bad layer %q", bad)
		}
	}
}

// Every rotation draws the same four words, each in a different color. The
// MediaBox has a nonzero origin and the smaller CropBox has another origin.
func wordGeometryPDF(t *testing.T) string {
	t.Helper()
	stream := "BT /F1 14 Tf 30 210 Td 1 0 0 rg (First ) Tj 0 1 0 rg (word) Tj 0 -40 Td 0 0 1 rg (Next ) Tj 1 0 1 rg (line) Tj ET"
	objects := []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Count 4 /Kids [4 0 R 5 0 R 6 0 R 7 0 R] /MediaBox [10 10 210 260] /CropBox [20 40 170 240] >>",
		"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>",
	}
	for _, angle := range []int{0, 90, 180, 270} {
		objects = append(objects, fmt.Sprintf("<< /Type /Page /Parent 2 0 R /Rotate %d /Resources << /Font << /F1 3 0 R >> >> /Contents 8 0 R >>", angle))
	}
	objects = append(objects, fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(stream), stream))
	return writePDFFixture(t, objects)
}

func TestPopplerWordBoxesMatchRenderedGlyphsAtEveryRotation(t *testing.T) {
	for _, name := range []string{"pdfinfo", "pdftoppm", "pdftotext"} {
		if _, err := exec.LookPath(name); err != nil {
			t.Skipf("%s is optional", name)
		}
	}
	file := wordGeometryPDF(t)
	doc := NewDocument(file)
	for index := range 4 {
		t.Run(strconv.Itoa(90*index), func(t *testing.T) {
			p, err := doc.Load(t.Context(), index, true)
			if err != nil || len(p.Words) != 4 || p.WordsError != "" {
				t.Fatalf("words = %+v, word error = %q, load error = %v", p.Words, p.WordsError, err)
			}
			raster := p.Image.(*image.RGBA)
			ink := coloredWordBounds(raster)
			lines := make(map[int][]string)
			for _, w := range p.Words {
				lines[w.Line] = append(lines[w.Line], w.Text)
				box := ink[w.Text]
				if box.Empty() {
					t.Fatalf("missing visible word %q", w.Text)
				}
				cx := float64(box.Min.X+box.Max.X) / (2 * float64(raster.Bounds().Dx()))
				cy := float64(box.Min.Y+box.Max.Y) / (2 * float64(raster.Bounds().Dy()))
				if math.Abs(cx-(w.X0+w.X1)/2) > .014 || math.Abs(cy-(w.Y0+w.Y1)/2) > .014 {
					t.Fatalf("%s box %+v misses its colored glyphs at (%.3f,%.3f)", w.Text, w, cx, cy)
				}
			}
			seen := map[string]bool{strings.Join(lines[0], " "): true, strings.Join(lines[1], " "): true}
			if len(lines) != 2 || !seen["First word"] || !seen["Next line"] {
				t.Fatalf("wrong reading order: %+v", lines)
			}
			// A later plain load still returns exactly pdftotext -layout.
			text, err := doc.Load(t.Context(), index, false)
			if err != nil {
				t.Fatal(err)
			}
			page := strconv.Itoa(index + 1)
			data, err := runTool(t.Context(), 512<<10, "pdftotext", "-f", page, "-l", page, "-layout", "-enc", "UTF-8", file, "-")
			if err != nil || text.Text != terminalText(string(data)) || len(text.Words) != 0 {
				t.Fatalf("text-only layout mismatch: %q, %v", text.Text, err)
			}
		})
	}
}

func coloredWordBounds(img *image.RGBA) map[string]image.Rectangle {
	boxes := make(map[string]image.Rectangle)
	for y := 0; y < img.Bounds().Dy(); y++ {
		for x := 0; x < img.Bounds().Dx(); x++ {
			i := y*img.Stride + x*4
			r, g, b := img.Pix[i], img.Pix[i+1], img.Pix[i+2]
			var word string
			switch {
			case r > 220 && g < 35 && b < 35:
				word = "First"
			case g > 220 && r < 35 && b < 35:
				word = "word"
			case b > 220 && r < 35 && g < 35:
				word = "Next"
			case r > 220 && b > 220 && g < 35:
				word = "line"
			default:
				continue
			}
			boxes[word] = boxes[word].Union(image.Rect(x, y, x+1, y+1))
		}
	}
	return boxes
}
