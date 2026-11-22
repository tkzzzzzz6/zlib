package cli

import (
	"bytes"
	"image"
	"strconv"

	"github.com/charmbracelet/x/ansi/sixel"
)

const paperTile = 32

// Locate paper and ink locally: a photograph embedded in otherwise white
// margins should keep the terminal's full grayscale range. Count exact black
// and white pixels so highlights in a smooth gradient do not qualify.
func textTiles(pixels []byte, width, height, stride, channels int) []bool {
	cols := (width + paperTile - 1) / paperTile
	tiles := make([]bool, cols*((height+paperTile-1)/paperTile))
	for top := 0; top < height; top += paperTile {
		for left := 0; left < width; left += paperTile {
			endX, endY := min(left+paperTile, width), min(top+paperTile, height)
			paper := 0
			for y := top; y < endY; y++ {
				row := pixels[y*stride+left*channels : y*stride+endX*channels]
				for x := 0; x < len(row); x += channels {
					v := row[x]
					if (v == 0 || v == 255) && (channels == 1 || (row[x+1] == v && row[x+2] == v)) {
						paper++
					}
				}
			}
			// Dense antialiased text can leave less than half the tile as
			// solid paper. A continuous-tone area rarely has even a third.
			tiles[top/paperTile*cols+left/paperTile] = paper*3 >= (endX-left)*(endY-top)
		}
	}
	return tiles
}

// Text pages use relatively few colors but cover many terminal cells. Keep
// one palette index per pixel and emit one six-row band at a time. Leave
// photographs and transparent images to the general-purpose quantizer.
func encodePageSixel(data *bytes.Buffer, img image.Image) bool {
	if region, ok := img.(pageRegion); ok {
		sub, ok := region.Image.(interface {
			SubImage(image.Rectangle) image.Image
		})
		if !ok {
			return false
		}
		img = sub.SubImage(region.rect)
	}
	var pixels []byte
	var stride, channels int
	switch src := img.(type) {
	case *image.RGBA:
		pixels, stride, channels = src.Pix, src.Stride, 4
	case *image.Gray:
		pixels, stride, channels = src.Pix, src.Stride, 1
	default:
		return false
	}
	width, height := img.Bounds().Dx(), img.Bounds().Dy()
	tileCols := (width + paperTile - 1) / paperTile
	text := textTiles(pixels, width, height, stride, channels)
	indexes := make([]byte, width*height)
	var palette []uint32
	colors := make(map[uint32]byte)
	var counts [sixel.MaxColors]int
	var paper byte
	for y := range height {
		row := pixels[y*stride : y*stride+width*channels]
		for x := range width {
			i := x * channels
			r := int(row[i])
			g, b := r, r
			if channels == 4 {
				if row[i+3] != 255 {
					return false
				}
				g, b = int(row[i+1]), int(row[i+2])
			}
			// Sixel uses integer RGB percentages. Group only neutral shades
			// in 4% steps (at most 2% error), retaining black, white and all
			// chromatic values at the terminal's full color precision.
			if r == g && g == b && text[y/paperTile*tileCols+x/paperTile] {
				r = ((r*25 + 127) / 255) * 4
				g, b = r, r
			} else {
				r, g, b = (r*100+127)/255, (g*100+127)/255, (b*100+127)/255
			}
			key := uint32(r)<<16 | uint32(g)<<8 | uint32(b)
			index, ok := colors[key]
			if !ok {
				if len(palette) == sixel.MaxColors {
					return false
				}
				index = byte(len(palette))
				colors[key] = index
				palette = append(palette, key)
			}
			indexes[y*width+x] = index
			counts[index]++
			if counts[index] > counts[paper] {
				paper = index
			}
		}
	}
	_, _ = sixel.WriteRaster(data, 1, 1, width, height)
	for i, rgb := range palette {
		_, _ = sixel.WriteColor(data, i, 2, int(rgb>>16), int(rgb>>8&255), int(rgb&255))
	}

	// Each color owns a row of six-bit columns. Printing only through the
	// last occupied column saves the trailing blanks on every color pass.
	bands := make([]byte, len(palette)*width)
	last := make([]int, len(palette))
	for y := 0; y < height; y += 6 {
		if y > 0 {
			data.WriteByte('-')
		}
		bandHeight := min(6, height-y)
		// Lay down the paper color in a single run. The foreground rows
		// overwrite it, so there is no need to encode its jagged text edges.
		_, _ = sixel.WriteColor(data, int(paper), 0, 0, 0, 0)
		_, _ = sixel.WriteRepeat(data, width, byte((1<<bandHeight)-1)+'?')
		for dy := 0; dy < bandHeight; dy++ {
			row := indexes[(y+dy)*width : (y+dy+1)*width]
			for x, index := range row {
				if index == paper {
					continue
				}
				bands[int(index)*width+x] |= 1 << dy
				last[index] = max(last[index], x+1)
			}
		}
		for index, end := range last {
			if end == 0 {
				continue
			}
			data.WriteByte('$')
			data.WriteByte('#')
			data.WriteString(strconv.Itoa(index))
			band := bands[index*width : index*width+end]
			writeSixelRuns(data, band)
			clear(band)
			last[index] = 0
		}
	}
	data.WriteByte('$')
	return true
}

func writeSixelRuns(data *bytes.Buffer, band []byte) {
	for len(band) > 0 {
		pixel, run := band[0]+'?', 1
		for run < len(band) && band[run] == band[0] {
			run++
		}
		if run > 3 {
			data.WriteByte('!')
			data.WriteString(strconv.Itoa(run))
			data.WriteByte(pixel)
		} else {
			for range run {
				data.WriteByte(pixel)
			}
		}
		band = band[run:]
	}
}
