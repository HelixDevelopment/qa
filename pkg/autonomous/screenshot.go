// SPDX-FileCopyrightText: 2026 Milos Vasic
// SPDX-License-Identifier: Apache-2.0

package autonomous

import (
	"bytes"
	"fmt"
	"image"
	"image/png"

	// Register JPEG decoder so image.Decode can handle
	// JPEG screenshots returned by some ADB versions.
	_ "image/jpeg"
)

// IsBlankScreenshot checks if a screenshot is blank/uniform color.
// It returns true if the image is all white, all black, or uniform color.
func IsBlankScreenshot(data []byte) bool {
	if len(data) < 1000 {
		// Too small to contain meaningful content
		return true
	}

	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		// Can't decode, assume it's blank to be safe
		return true
	}

	bounds := img.Bounds()
	width := bounds.Dx()
	height := bounds.Dy()

	if width < 10 || height < 10 {
		return true
	}

	// A coarse 9x9 grid first (cheap, catches widgets on a dark login
	// screen). A text-light, background-dominated page (a settings or empty
	// state page whose few glyphs miss all 81 points) then gets a dense
	// 128x128 pass: judging such a page "blank" would BLOCK a verdict on a
	// frame that plainly shows content.
	return sampledRange(img, 9) < 20 && sampledRange(img, 128) < 20
}

// sampledRange returns the largest per-channel range (max - min) over a
// gridN x gridN lattice of sample points.
func sampledRange(img image.Image, gridN int) uint32 {
	bounds := img.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	var rMin, gMin, bMin uint32 = 255, 255, 255
	var rMax, gMax, bMax uint32
	for iy := 1; iy <= gridN; iy++ {
		for ix := 1; ix <= gridN; ix++ {
			x := bounds.Min.X + width*ix/(gridN+1)
			y := bounds.Min.Y + height*iy/(gridN+1)
			r, g, b, _ := img.At(x, y).RGBA()
			r, g, b = r>>8, g>>8, b>>8
			rMin, rMax = min(rMin, r), max(rMax, r)
			gMin, gMax = min(gMin, g), max(gMax, g)
			bMin, bMax = min(bMin, b), max(bMax, b)
		}
	}
	maxRange := rMax - rMin
	if gMax-gMin > maxRange {
		maxRange = gMax - gMin
	}
	if bMax-bMin > maxRange {
		maxRange = bMax - bMin
	}
	return maxRange
}

// absDiff returns absolute difference
func absDiff(a, b uint32) uint32 {
	if a > b {
		return a - b
	}
	return b - a
}

// maxScreenshotWidth is the maximum width (in pixels) for
// screenshots sent to the LLM vision API. Larger images
// are downscaled proportionally using nearest-neighbour
// sampling. 480px keeps file size under ~50KB for fast
// CPU-based inference while retaining UI readability.
const maxScreenshotWidth = 480

// resizeScreenshot downscales a PNG image to at most
// maxScreenshotWidth pixels wide, preserving aspect ratio.
// If the image is already small enough or cannot be
// decoded, the original bytes are returned unchanged.
func resizeScreenshot(data []byte) []byte {
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return data
	}

	bounds := img.Bounds()
	origW := bounds.Dx()
	origH := bounds.Dy()

	if origW <= maxScreenshotWidth {
		return data
	}

	// Compute new dimensions preserving aspect ratio.
	newW := maxScreenshotWidth
	newH := origH * newW / origW

	// Nearest-neighbour downscale — fast and sufficient
	// for LLM vision which does not need anti-aliasing.
	dst := image.NewRGBA(image.Rect(0, 0, newW, newH))
	for y := 0; y < newH; y++ {
		srcY := y * origH / newH
		for x := 0; x < newW; x++ {
			srcX := x * origW / newW
			dst.Set(x, y, img.At(
				bounds.Min.X+srcX,
				bounds.Min.Y+srcY,
			))
		}
	}

	var buf bytes.Buffer
	if err := png.Encode(&buf, dst); err != nil {
		return data
	}

	fmt.Printf(
		"    [resize] %dx%d -> %dx%d (%dKB -> %dKB)\n",
		origW, origH, newW, newH,
		len(data)/1024, buf.Len()/1024,
	)
	return buf.Bytes()
}
