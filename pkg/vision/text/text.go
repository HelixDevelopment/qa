// SPDX-FileCopyrightText: 2026 Milos Vasic
// SPDX-License-Identifier: Apache-2.0

// Package text is the HelixQA client for the text-detection sidecar
// (cmd/helixqa-text/, future — a Python process running EAST /
// MSER / PP-OCR). Same HTTP-over-sidecar pattern as
// pkg/agent/omniparser and pkg/nexus/observe/axtree/darwin. The Go
// host stays CGO-free while the Python sidecar carries the CNN
// dependencies.
//
// Wire format (multipart/form-data upload, JSON response):
//
//	POST {endpoint}/detect
//	multipart: "image" = <PNG bytes>
//	→ 200 OK {
//	  "width":  1920,
//	  "height": 1080,
//	  "regions": [
//	    {
//	      "bbox":       [x1, y1, x2, y2],
//	      "text":       "Sign In",   // optional — present when OCR is enabled
//	      "confidence": 0.94
//	    },
//	    ...
//	  ]
//	}
package text

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/png"
	"io"
	"mime/multipart"
	"net/http"
	"time"
)

// Detector is the cross-package contract for text-region
// enumeration. Satisfied by *Client.
type Detector interface {
	Detect(ctx context.Context, screenshot image.Image) (Result, error)
}

// Client is the HTTP client for the text-detection sidecar.
type Client struct {
	// Endpoint is the base URL of the sidecar, e.g.
	// "http://host-a.example.com:7870". Required.
	Endpoint string

	// DetectPath is the relative URL of the detect endpoint.
	// Default: "/detect".
	DetectPath string

	// HTTPClient is the transport. Default 30 s timeout — EAST on
	// CPU runs in a few seconds per 1080p frame.
	HTTPClient *http.Client
}

// New returns a Client bound to the given endpoint.
func New(endpoint string) *Client {
	return &Client{Endpoint: endpoint}
}

// Sentinel errors.
var (
	ErrEmptyEndpoint = errors.New("helixqa/vision/text: Endpoint not set")
	ErrNilImage      = errors.New("helixqa/vision/text: nil screenshot")
	ErrInvalidBBox   = errors.New("helixqa/vision/text: region bbox is not [x1, y1, x2, y2]")
)

// ---------------------------------------------------------------------------
// Result types
// ---------------------------------------------------------------------------

// Region is one detected text bounding box.
type Region struct {
	BBox       image.Rectangle `json:"bbox"`
	Text       string          `json:"text,omitempty"`
	Confidence float64         `json:"confidence"`
}

// Result aggregates every detection the sidecar reports for a
// single screenshot.
type Result struct {
	Width   int      `json:"width"`
	Height  int      `json:"height"`
	Regions []Region `json:"regions"`
}

// Center returns the pixel center of a region's bbox — the natural
// click target for regions that represent clickable text.
func (r Region) Center() image.Point {
	return image.Point{
		X: (r.BBox.Min.X + r.BBox.Max.X) / 2,
		Y: (r.BBox.Min.Y + r.BBox.Max.Y) / 2,
	}
}

// FindContaining returns the smallest region whose bbox contains
// the given point, or (zero, false) if none covers it. Used by
// grounding layers that want to match a click coordinate to the
// text label it targets.
func (r Result) FindContaining(p image.Point) (Region, bool) {
	var best Region
	var bestArea int
	found := false
	for _, rg := range r.Regions {
		if !pointIn(rg.BBox, p) {
			continue
		}
		area := bboxArea(rg.BBox)
		if !found || area < bestArea {
			best = rg
			bestArea = area
			found = true
		}
	}
	return best, found
}

// ---------------------------------------------------------------------------
// Private wire types
// ---------------------------------------------------------------------------

type wireResult struct {
	Width   int          `json:"width"`
	Height  int          `json:"height"`
	Regions []wireRegion `json:"regions"`
}

type wireRegion struct {
	BBox       []int   `json:"bbox"` // [x1, y1, x2, y2]
	Text       string  `json:"text"`
	Confidence float64 `json:"confidence"`
}

// ---------------------------------------------------------------------------
// Detect — the main entry point
// ---------------------------------------------------------------------------

// Detect sends the screenshot to the sidecar and returns the parsed
// Result. Respects ctx cancellation.
func (c *Client) Detect(ctx context.Context, screenshot image.Image) (Result, error) {
	if c.Endpoint == "" {
		return Result{}, ErrEmptyEndpoint
	}
	if screenshot == nil {
		return Result{}, ErrNilImage
	}

	body, contentType, err := buildMultipartPNG(screenshot)
	if err != nil {
		return Result{}, fmt.Errorf("text: encode: %w", err)
	}

	path := c.DetectPath
	if path == "" {
		path = "/detect"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.Endpoint+path, body)
	if err != nil {
		return Result{}, fmt.Errorf("text: new request: %w", err)
	}
	req.Header.Set("Content-Type", contentType)

	client := c.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return Result{}, fmt.Errorf("text: call: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return Result{}, fmt.Errorf("text: HTTP %d: %s", resp.StatusCode, string(b))
	}

	var wire wireResult
	if err := json.NewDecoder(resp.Body).Decode(&wire); err != nil {
		return Result{}, fmt.Errorf("text: decode: %w", err)
	}
	return convertWireResult(wire)
}

func buildMultipartPNG(img image.Image) (io.Reader, string, error) {
	var pngBuf bytes.Buffer
	if err := png.Encode(&pngBuf, img); err != nil {
		return nil, "", err
	}
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	field, err := w.CreateFormFile("image", "screenshot.png")
	if err != nil {
		return nil, "", err
	}
	if _, err := field.Write(pngBuf.Bytes()); err != nil {
		return nil, "", err
	}
	if err := w.Close(); err != nil {
		return nil, "", err
	}
	return &body, w.FormDataContentType(), nil
}

func convertWireResult(w wireResult) (Result, error) {
	out := Result{
		Width:   w.Width,
		Height:  w.Height,
		Regions: make([]Region, 0, len(w.Regions)),
	}
	for i, wr := range w.Regions {
		if len(wr.BBox) != 4 {
			return Result{}, fmt.Errorf("%w: region %d has bbox %v", ErrInvalidBBox, i, wr.BBox)
		}
		out.Regions = append(out.Regions, Region{
			BBox:       image.Rect(wr.BBox[0], wr.BBox[1], wr.BBox[2], wr.BBox[3]),
			Text:       wr.Text,
			Confidence: wr.Confidence,
		})
	}
	return out, nil
}

func pointIn(r image.Rectangle, p image.Point) bool {
	return p.X >= r.Min.X && p.X < r.Max.X && p.Y >= r.Min.Y && p.Y < r.Max.Y
}

func bboxArea(r image.Rectangle) int {
	if r.Max.X <= r.Min.X || r.Max.Y <= r.Min.Y {
		return 0
	}
	return (r.Max.X - r.Min.X) * (r.Max.Y - r.Min.Y)
}

// Compile-time guard.
var _ Detector = (*Client)(nil)
