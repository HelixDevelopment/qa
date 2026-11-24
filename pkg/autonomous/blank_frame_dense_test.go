// SPDX-FileCopyrightText: 2026 Milos Vasic
// SPDX-License-Identifier: Apache-2.0

package autonomous

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"testing"
)

func pngOf(t *testing.T, w, h int, draw func(*image.RGBA)) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{255, 247, 245, 255})
		}
	}
	if draw != nil {
		draw(img)
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// A sparse page (a few dark glyph-like strokes that miss every point of the coarse 9x9 grid) is content, not blank.
func TestIsBlankScreenshot_SparsePageIsNotBlank(t *testing.T) {
	data := pngOf(t, 1920, 937, func(img *image.RGBA) {
		// a thin dark stroke between grid columns/rows (grid points are at multiples of 192 and 93.7)
		for x := 40; x < 300; x++ {
			for y := 130; y < 134; y++ {
				img.Set(x, y, color.RGBA{20, 20, 20, 255})
			}
		}
		// a second block, large enough for the dense pass to hit it
		for x := 500; x < 800; x++ {
			for y := 300; y < 340; y++ {
				img.Set(x, y, color.RGBA{20, 20, 20, 255})
			}
		}
	})
	if IsBlankScreenshot(data) {
		t.Fatal("a page with visible content was judged blank")
	}
}

func TestIsBlankScreenshot_UniformStaysBlank(t *testing.T) {
	if !IsBlankScreenshot(pngOf(t, 1920, 937, nil)) {
		t.Fatal("a uniform frame must stay blank")
	}
}
