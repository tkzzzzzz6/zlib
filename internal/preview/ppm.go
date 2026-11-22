package preview

import (
	"bytes"
	"errors"
	"image"
)

const (
	maxPPMHeader = 1024
	maxPPMBytes  = maxPPMHeader + 3*maxPageSide*maxPageSide
)

// decodePage accepts a single 8-bit RGB image from pdftoppm. Its P6 header
// ends with one whitespace separator; any byte can occur in the raster.
func decodePage(data []byte) (image.Image, error) {
	header := data[:min(len(data), maxPPMHeader)]
	if len(header) < 3 || !bytes.HasPrefix(header, []byte("P6")) || !ppmSpace(header[2]) {
		return nil, errors.New("invalid PDF page image: expected a binary PPM")
	}
	pos := 2
	// The three header numbers may be separated by whitespace and comments.
	number := func() int {
		for pos < len(header) {
			if header[pos] == '#' {
				for pos < len(header) && header[pos] != '\n' && header[pos] != '\r' {
					pos++
				}
			} else if ppmSpace(header[pos]) {
				pos++
			} else {
				break
			}
		}
		n := 0
		for pos < len(header) && header[pos] >= '0' && header[pos] <= '9' {
			n = n*10 + int(header[pos]-'0')
			pos++
			if n > maxPageSide {
				return -1
			}
		}
		return n
	}
	w, h, depth := number(), number(), number()
	if w <= 0 || h <= 0 || depth != 255 || pos >= len(header) || !ppmSpace(header[pos]) {
		return nil, errors.New("invalid PDF page dimensions or PPM header")
	}
	rgb := data[pos+1:]
	want := 3 * w * h
	// Accept CRLF writers as well as a lone CR followed by a pixel of 0x0a.
	if header[pos] == '\r' && len(rgb) == want+1 && rgb[0] == '\n' {
		rgb = rgb[1:]
	}
	if len(rgb) != want {
		return nil, errors.New("invalid PDF page raster size")
	}
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for i, j := 0, 0; i < len(rgb); i, j = i+3, j+4 {
		img.Pix[j], img.Pix[j+1], img.Pix[j+2], img.Pix[j+3] = rgb[i], rgb[i+1], rgb[i+2], 255
	}
	return img, nil
}

func ppmSpace(b byte) bool {
	return b == ' ' || (b >= '\t' && b <= '\r')
}
