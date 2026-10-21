// SPDX-FileCopyrightText: 2026 Milos Vasic
// SPDX-License-Identifier: Apache-2.0

// Package coordinate implements the coordinate-scaling algorithm
// ported from Anthropic's computer-use-demo reference
// (<tools-dir>/anthropic-quickstarts/computer-use-demo/
// computer_use_demo/tools/computer.py::scale_coordinates) and the
// UI-TARS action space
// (<tools-dir>/ui-tars/docs/action_space.md).
//
// The algorithm takes a model-produced (x, y) pair expressed in a
// canonical display resolution and maps it onto the real screen.
// Normalised (0..1) coordinates are also supported as a convenience
// for UI-TARS-style planners that emit click(0.5, 0.5).
package coordinate

import (
	"errors"
	"math"
)

// ScalingTarget is one canonical resolution the planner can think
// in. Anthropic's MAX_SCALING_TARGETS covers the common
// 4:3 / 16:9 / 16:10 ratios.
type ScalingTarget struct {
	Width  int
	Height int
}

// MaxScalingTargets is the default catalogue matching Anthropic's
// reference (`XGA`, `WXGA`, `FWXGA`). Operators who want to support
// non-standard screens can pass their own slice to ScaleCoordinates.
var MaxScalingTargets = []ScalingTarget{
	{Width: 1024, Height: 768},  // XGA      (4:3)
	{Width: 1280, Height: 800},  // WXGA     (16:10)
	{Width: 1366, Height: 768},  // FWXGA    (~16:9)
	{Width: 1280, Height: 720},  // HD 720p  (16:9)
	{Width: 1920, Height: 1080}, // FHD      (16:9)
}

// ScaleMode selects between upscaling (model→screen) and the
// inverse (screen→model).
type ScaleMode int

const (
	// ScaleAPIToScreen maps a model-space (x,y) onto the real
	// screen. Use when dispatching an LLM-produced coordinate.
	ScaleAPIToScreen ScaleMode = iota
	// ScaleScreenToAPI maps a real (x,y) back into the model's
	// canonical resolution. Use when recording observations to
	// feed back into the prompt.
	ScaleScreenToAPI
)

// Config controls ScaleCoordinates.
type Config struct {
	// Screen is the real display resolution (in pixels).
	Screen ScalingTarget
	// Targets is the catalogue of canonical resolutions. Empty =
	// MaxScalingTargets.
	Targets []ScalingTarget
	// AspectTolerance is the allowed difference between the real
	// screen's aspect ratio and a Target's. Defaults to 0.02
	// (±2 %); anything outside that window means no scaling is
	// applied and ScaleCoordinates returns the input unchanged.
	AspectTolerance float64
}

// ErrOutOfBounds is returned when a caller supplies coordinates
// that escape the screen after scaling. Prevents a model-produced
// (x, y) from spilling onto a neighbouring monitor or through the
// taskbar.
var ErrOutOfBounds = errors.New("coordinate: out of bounds")

// ScaleCoordinates maps (x, y) between the planner's canonical
// resolution and the real screen. When x and y are in 0..1 they are
// treated as normalised UI-TARS-style coordinates and multiplied
// by the real screen size directly. Returns ErrOutOfBounds when the
// scaled result escapes the screen.
func ScaleCoordinates(cfg Config, mode ScaleMode, x, y float64) (int, int, error) {
	if cfg.Screen.Width <= 0 || cfg.Screen.Height <= 0 {
		return 0, 0, errors.New("coordinate: screen dimensions required")
	}
	if cfg.AspectTolerance <= 0 {
		cfg.AspectTolerance = 0.02
	}

	// Normalised UI-TARS path.
	if 0 <= x && x <= 1 && 0 <= y && y <= 1 {
		outX := int(math.Round(x * float64(cfg.Screen.Width)))
		outY := int(math.Round(y * float64(cfg.Screen.Height)))
		if outX < 0 || outY < 0 || outX >= cfg.Screen.Width || outY >= cfg.Screen.Height {
			return 0, 0, ErrOutOfBounds
		}
		return outX, outY, nil
	}

	targets := cfg.Targets
	if len(targets) == 0 {
		targets = MaxScalingTargets
	}
	t, ok := pickTargetByAspect(cfg.Screen, targets, cfg.AspectTolerance)
	if !ok {
		// No aspect match — pass input through but clamp into
		// bounds so an out-of-range coord doesn't escape.
		xi := int(math.Round(x))
		yi := int(math.Round(y))
		if xi < 0 || yi < 0 || xi >= cfg.Screen.Width || yi >= cfg.Screen.Height {
			return 0, 0, ErrOutOfBounds
		}
		return xi, yi, nil
	}

	var xScale, yScale float64
	switch mode {
	case ScaleAPIToScreen:
		xScale = float64(cfg.Screen.Width) / float64(t.Width)
		yScale = float64(cfg.Screen.Height) / float64(t.Height)
	case ScaleScreenToAPI:
		xScale = float64(t.Width) / float64(cfg.Screen.Width)
		yScale = float64(t.Height) / float64(cfg.Screen.Height)
	default:
		return 0, 0, errors.New("coordinate: unknown ScaleMode")
	}

	outX := int(math.Round(x * xScale))
	outY := int(math.Round(y * yScale))

	if mode == ScaleAPIToScreen {
		if outX < 0 || outY < 0 || outX >= cfg.Screen.Width || outY >= cfg.Screen.Height {
			return 0, 0, ErrOutOfBounds
		}
	} else {
		if outX < 0 || outY < 0 || outX >= t.Width || outY >= t.Height {
			return 0, 0, ErrOutOfBounds
		}
	}
	return outX, outY, nil
}

// pickTargetByAspect finds the scaling target whose aspect ratio is
// closest to screen. Returns (_, false) when no target lies inside
// tolerance.
func pickTargetByAspect(screen ScalingTarget, targets []ScalingTarget, tolerance float64) (ScalingTarget, bool) {
	screenAspect := float64(screen.Width) / float64(screen.Height)
	best := ScalingTarget{}
	bestDelta := math.Inf(1)
	for _, t := range targets {
		a := float64(t.Width) / float64(t.Height)
		d := math.Abs(a - screenAspect)
		if d < bestDelta {
			best = t
			bestDelta = d
		}
	}
	if bestDelta > tolerance {
		return ScalingTarget{}, false
	}
	return best, true
}

// NormalizedToScreen is a convenience wrapper that accepts
// normalised (0..1) floats and returns real pixels directly.
func NormalizedToScreen(screen ScalingTarget, x, y float64) (int, int, error) {
	return ScaleCoordinates(Config{Screen: screen}, ScaleAPIToScreen, x, y)
}
