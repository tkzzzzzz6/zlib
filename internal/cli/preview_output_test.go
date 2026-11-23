package cli

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"strings"
	"testing"
)

func outputPDFFrame(t *testing.T, protocol string, width, height, zoom int) *coverFrame {
	t.Helper()
	picture := image.NewGray(image.Rect(0, 0, width, height))
	picture.SetGray(0, 0, color.Gray{Y: 255})
	src := coverSource{url: "page:1", documentPage: true, area: image.Rect(0, 0, 4, 4), img: picture, zoomPercent: zoom}
	caps := coverCapabilities{kitty: protocol == "Kitty", sixel: protocol == "Sixel", cellWidth: 8, cellHeight: 8}
	frame := encodeCoverFrame(src, caps)
	if frame == nil {
		t.Fatal("failed to encode PDF frame")
	}
	return frame
}

func writePDFOutput(t *testing.T, out *coverOutput, capture *bytes.Buffer, p string) string {
	t.Helper()
	capture.Reset()
	n, err := out.Write([]byte(p))
	if err != nil || n != len(p) {
		t.Fatalf("write returned (%d, %v)", n, err)
	}
	return capture.String()
}

func TestPDFOutputKittyReplacement(t *testing.T) {
	old := outputPDFFrame(t, "Kitty", 40, 50, 100)
	next := outputPDFFrame(t, "Kitty", 40, 50, 125)
	if old.id == next.id {
		t.Errorf("replacement reuses live image id %d", old.id)
	}
	var capture bytes.Buffer
	out := &coverOutput{target: &capture}
	out.set(old)
	writePDFOutput(t, out, &capture, "first")
	out.set(next)
	swap := writePDFOutput(t, out, &capture, "zoom")
	newAt, deleteAt := strings.Index(swap, next.data), strings.Index(swap, eraseCover(old))
	if newAt < 0 || deleteAt < newAt+len(next.data) {
		t.Error("old Kitty image must remain live until the replacement is fully transmitted and placed")
	}
	cleanup := writePDFOutput(t, out, &capture, "\x1b[?1049l")
	if !strings.Contains(cleanup, eraseCover(next)+"\x1b[?1049l") || strings.Contains(cleanup, next.data) {
		t.Error("exit must delete the active Kitty image before leaving the alternate screen")
	}
}

func TestPDFOutputSixelReplacement(t *testing.T) {
	var capture bytes.Buffer
	out := &coverOutput{target: &capture}
	old := outputPDFFrame(t, "Sixel", 40, 50, 125)
	next := outputPDFFrame(t, "Sixel", 40, 50, 150)
	out.set(old)
	writePDFOutput(t, out, &capture, "")
	out.set(next)
	swap := writePDFOutput(t, out, &capture, "zoom")
	if strings.Contains(swap, "    ") || !strings.Contains(swap, next.data) {
		t.Error("same-footprint Sixel replacement must paint over the old image without clearing its body")
	}
	for _, input := range []string{"\x1b7\x1b8", "\x1b[8;1Hfooter"} {
		if strings.Contains(writePDFOutput(t, out, &capture, input), next.data) {
			t.Error("unchanged page was retransmitted on a wake or footer update")
		}
	}
	if !strings.Contains(writePDFOutput(t, out, &capture, "\x1b[2J"), next.data) {
		t.Error("a cleared screen needs the Sixel image again")
	}
}

func TestPDFOutputSixelPartialCellMargins(t *testing.T) {
	// With eight-pixel cells a one-pixel change can leave both footprints
	// in the same cells. Erase the old edge pixels before painting the new size.
	var capture bytes.Buffer
	out := &coverOutput{target: &capture}
	old := outputPDFFrame(t, "Sixel", 30, 32, 100)
	next := outputPDFFrame(t, "Sixel", 30, 32, 95)
	out.set(old)
	writePDFOutput(t, out, &capture, "")
	out.set(next)
	swap := writePDFOutput(t, out, &capture, "")
	for row := 1; row <= 3; row++ {
		if !strings.Contains(swap, fmt.Sprintf("\x1b[%d;4H ", row)) {
			t.Errorf("right margin was not cleared on row %d", row)
		}
	}
	if !strings.Contains(swap, "\x1b[4;1H    ") || strings.Contains(swap, "\x1b[1;1H    ") {
		t.Error("shrink must clear the partial bottom and right edges only")
	}
}

func TestPDFOutputSixelFirstWakeBeforeTextRender(t *testing.T) {
	var capture bytes.Buffer
	out := &coverOutput{target: &capture}
	page := outputPDFFrame(t, "Sixel", 30, 32, 100)
	out.set(page)
	wake := "\x1b7\x1b8"
	if !strings.Contains(writePDFOutput(t, out, &capture, wake), page.data) {
		t.Fatal("first wake did not display the page")
	}
	if strings.Contains(writePDFOutput(t, out, &capture, wake), page.data) {
		t.Fatal("an extra wake should not retransmit the page")
	}
	// Bubble Tea flushes raw commands ahead of cell diffs. That first cell
	// diff can erase the previous text preview underneath the new raster.
	if !strings.Contains(writePDFOutput(t, out, &capture, "\x1b[2;1H\x1b[K"), page.data) {
		t.Fatal("the first renderer write cleared pixels after the wake")
	}
	if strings.Contains(writePDFOutput(t, out, &capture, "\x1b[8;1Hfooter"), page.data) {
		t.Fatal("later chrome updates should keep the reserved page body")
	}
}
