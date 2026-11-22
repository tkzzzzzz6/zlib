package preview

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/draw"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// The first page carries text; the second paints an inline image without a
// text layer. Build the xref from byte offsets so a parser can open the file
// without falling back to its damaged-file recovery.
func twoPagePDF(t *testing.T) string {
	t.Helper()
	stream := func(data string) string {
		return fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(data), data)
	}
	objects := []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Count 2 /Kids [3 0 R 6 0 R] /MediaBox [0 0 200 250] >>",
		"<< /Type /Page /Parent 2 0 R /Resources << /Font << /F1 5 0 R >> >> /Contents 4 0 R >>",
		stream("BT /F1 14 Tf 10 220 Td (Preview sample) Tj ET"),
		"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>",
		"<< /Type /Page /Parent 2 0 R /Resources << >> /Contents 7 0 R >>",
		stream("q 20 0 0 20 10 10 cm BI /W 1 /H 1 /CS /RGB /BPC 8 /F /AHx ID FF9900> EI Q"),
	}
	var buf bytes.Buffer
	buf.WriteString("%PDF-1.4\n")
	var offsets []int
	for i, obj := range objects {
		offsets = append(offsets, buf.Len())
		fmt.Fprintf(&buf, "%d 0 obj\n%s\nendobj\n", i+1, obj)
	}
	xref := buf.Len()
	fmt.Fprintf(&buf, "xref\n0 %d\n0000000000 65535 f \n", len(objects)+1)
	for _, n := range offsets {
		fmt.Fprintf(&buf, "%010d 00000 n \n", n)
	}
	fmt.Fprintf(&buf, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(objects)+1, xref)
	p := filepath.Join(t.TempDir(), "-fixture book.pdf")
	if err := os.WriteFile(p, buf.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadActualPDFPagesWhenPopplerIsInstalled(t *testing.T) {
	for _, name := range []string{"pdfinfo", "pdftotext", "pdftoppm"} {
		if _, err := exec.LookPath(name); err != nil {
			t.Skipf("%s is optional", name)
		}
	}
	path := twoPagePDF(t)
	page, err := Load(t.Context(), path, 0, false)
	if err != nil || page.Count != 2 || page.Index != 0 || page.Text != "Preview sample" || page.Image != nil {
		t.Fatalf("text page = %+v, %v", page, err)
	}
	page, err = Load(t.Context(), path, 1, true)
	if err != nil || page.Image == nil || page.Image.Bounds().Empty() || !strings.Contains(page.Text, "no extractable text") {
		t.Fatalf("image page = %v, text = %q, err = %v", page.Image != nil, page.Text, err)
	}
	// Both Poppler transports carry the same pixels, including the inline
	// image. Compare to its lossless PNG output, channel by channel.
	pngBytes, err := runTool(t.Context(), 10<<20, "pdftoppm", "-f", "2", "-l", "2", "-singlefile", "-scale-to", fmt.Sprint(maxPageSide), "-png", path)
	if err != nil {
		t.Fatal(err)
	}
	reference, err := png.Decode(bytes.NewReader(pngBytes))
	if err != nil {
		t.Fatal(err)
	}
	want := image.NewRGBA(reference.Bounds())
	draw.Draw(want, want.Bounds(), reference, reference.Bounds().Min, draw.Src)
	got, ok := page.Image.(*image.RGBA)
	if !ok || got.Bounds() != want.Bounds() || !bytes.Equal(got.Pix, want.Pix) {
		t.Fatal("binary PPM and PNG pixels differ")
	}
	_, err = Load(t.Context(), path, 2, false)
	if err == nil || !strings.Contains(err.Error(), "1 to 2") {
		t.Fatalf("bounds error = %v", err)
	}
}

func TestPDFFallsBackWhenToolsAreMissing(t *testing.T) {
	testErr := errors.New("install missing tool")
	path := twoPagePDF(t)
	testRunner := func(ctx context.Context, limit int, name string, args ...string) ([]byte, error) {
		if !slices.Contains(args, path) {
			t.Fatalf("%s args %q omit path", name, args)
		}
		switch name {
		case "pdfinfo":
			return []byte("Pages: 3\n"), nil
		case "pdftoppm":
			return nil, testErr
		case "pdftotext":
			return []byte("safe\x1b[1m\r\u009b\u202e text\f"), nil
		}
		t.Fatalf("unexpected tool %q", name)
		return nil, nil
	}
	var doc pdfDocument
	got, err := doc.load(t.Context(), path, 2, true, testRunner)
	if err != nil || got.Text != "safe[1m text" || got.Image != nil || got.Count != 3 || got.Index != 2 {
		t.Fatalf("page = %+v, err = %v", got, err)
	}
	t.Setenv("PATH", t.TempDir())
	_, err = Load(t.Context(), path, 0, false)
	if err == nil || !strings.Contains(err.Error(), "install Poppler") {
		t.Fatalf("missing pdfinfo error = %v", err)
	}
}

func TestPreviewBoundsAndCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	out := &toolOutput{limit: 10, cancel: cancel}
	if _, err := out.Write([]byte("too much output")); err == nil || ctx.Err() == nil || out.buf.Len() != 0 {
		t.Fatal("output limit did not stop the child")
	}
	if _, err := Load(ctx, "missing", 0, false); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled load = %v", err)
	}
}
