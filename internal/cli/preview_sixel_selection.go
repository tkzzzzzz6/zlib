package cli

import (
	"image"
	"slices"

	"github.com/charmbracelet/x/ansi/sixel"
)

// Apply the highlight after indexing the clean page. The original color
// registers stay intact for links, illustrations and text outside the drag.
func highlightSixelIndexes(indexes []byte, width, height int, palette []uint32, rects []image.Rectangle) []uint32 {
	viewport := image.Rect(0, 0, width, height)
	var bounds image.Rectangle
	for _, r := range rects {
		bounds = bounds.Union(r.Intersect(viewport))
	}
	if bounds.Empty() {
		return palette
	}
	// Bound the mask to the selection. Overlapping word boxes count once.
	mask := image.NewAlpha(bounds)
	var counts [sixel.MaxColors]int
	for _, r := range rects {
		r = r.Intersect(bounds)
		for y := r.Min.Y; y < r.Max.Y; y++ {
			for x := r.Min.X; x < r.Max.X; x++ {
				pos := mask.PixOffset(x, y)
				if mask.Pix[pos] == 0 {
					mask.Pix[pos] = 1
					counts[indexes[y*width+x]]++
				}
			}
		}
	}
	var used []int
	for index := range palette {
		if counts[index] != 0 {
			used = append(used, index)
		}
	}
	// Paper and the most frequent ink shades get first use of spare slots.
	slices.SortStableFunc(used, func(a, b int) int { return counts[b] - counts[a] })
	var mapping [sixel.MaxColors]byte
	originalColors := len(palette)
	for _, index := range used {
		palette, mapping[index] = selectionColor(palette, tintSixelColor(palette[index]))
	}

	// A saturated page can lack a usable blue. Switch the selection to
	// reverse video when its dominant color cannot show a recognizable
	// tint. If even inversion lacks contrast, use the general color encoder.
	dominant := used[0]
	old, chosen := palette[dominant], palette[mapping[dominant]]
	if sixelColorDistance(chosen, tintSixelColor(old)) > 3*8*8 || sixelColorDistance(chosen, old) < 8*8 ||
		!selectionKeepsInkContrast(palette, used, mapping) {
		palette = palette[:originalColors]
		for _, index := range used {
			inverted := uint32(0x646464) - palette[index] // RGB percentages are 0..100
			palette, mapping[index] = selectionColor(palette, inverted)
		}
		if sixelColorDistance(palette[mapping[dominant]], old) < 3*12*12 || !selectionKeepsInkContrast(palette, used, mapping) {
			return nil
		}
	}
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		row := indexes[y*width+bounds.Min.X : y*width+bounds.Max.X]
		start := mask.PixOffset(bounds.Min.X, y)
		for x, selected := range mask.Pix[start : start+len(row)] {
			if selected != 0 {
				row[x] = mapping[row[x]]
			}
		}
	}
	return palette
}

func selectionKeepsInkContrast(palette []uint32, used []int, mapping [sixel.MaxColors]byte) bool {
	// A selection can cross several backgrounds. Preserve at least one
	// third of the original RGB separation for every clearly contrasting
	// pair. Distances are squared; at most 256 colors need comparison.
	for i, a := range used {
		for _, b := range used[i+1:] {
			original := sixelColorDistance(palette[a], palette[b])
			if original >= 3*20*20 && 9*sixelColorDistance(palette[mapping[a]], palette[mapping[b]]) < original {
				return false
			}
		}
	}
	return true
}

func tintSixelColor(rgb uint32) uint32 {
	// RGBA gives premultiplied channels. Blend directly in terminal RGB
	// percentages using the same overlay color as the PNG renderer.
	r, g, b, a := pdfSelectionTint.C.RGBA()
	blend := func(value, tint uint32) uint32 {
		return (value*(65535-a) + tint*100 + 32767) / 65535
	}
	return blend(rgb>>16, r)<<16 | blend(rgb>>8&255, g)<<8 | blend(rgb&255, b)
}

func sixelColorDistance(a, b uint32) int {
	r := int(a>>16) - int(b>>16)
	g := int(a>>8&255) - int(b>>8&255)
	blue := int(a&255) - int(b&255)
	return r*r + g*g + blue*blue
}

func selectionColor(palette []uint32, rgb uint32) ([]uint32, byte) {
	best, distance := 0, sixelColorDistance(palette[0], rgb)
	for i := 1; i < len(palette) && distance > 0; i++ {
		if d := sixelColorDistance(palette[i], rgb); d < distance {
			best, distance = i, d
		}
	}
	if distance > 0 && len(palette) < sixel.MaxColors {
		return append(palette, rgb), byte(len(palette))
	}
	return palette, byte(best)
}
