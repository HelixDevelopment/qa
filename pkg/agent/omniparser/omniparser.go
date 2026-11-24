// SPDX-FileCopyrightText: 2026 Milos Vasic
// SPDX-License-Identifier: Apache-2.0

// Package omniparser is the HelixQA client for OmniParser v2
// (Microsoft Research, 2024) — the GUI element detector that turns a
// raw screenshot into a grid of bounding-boxed interactable elements.
//
// OmniParser complements UI-TARS:
//   - UI-TARS picks an action (click/type/scroll) given a screenshot
//     + instruction.
//   - OmniParser identifies WHERE the interactable elements live.
//
// Combining both lets the grounding layer cross-check VLM coordinate
// hypotheses against a detector that is specifically trained to find
// buttons, text fields, icons, and the like.
//
// The deployed OmniParser runs as a Python sidecar (the
// cmd/helixqa-omniparser/ container per OpenClawing4-Phase2-Kickoff.md
// §8). This package speaks its REST wire format over plain
// net/http — no Python, no gRPC, no CGO in the Go host.
//
// Wire format:
//
//	POST {endpoint}/parse
//	multipart/form-data:
//	  image = <PNG bytes>  (field name: "image")
//	→ 200 OK {
//	  "width": 1920,
//	  "height": 1080,
//	  "elements": [
//	    {
//	      "bbox": [x1, y1, x2, y2],
//	      "type": "button",
//	      "text": "Sign in",
//	      "content": "Sign in",
//	      "interactive": true,
//	      "confidence": 0.94
//	    }, ...
//	  ]
//	}
package omniparser

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

// Client is the OmniParser REST client.
type Client struct {
	// Endpoint is the base URL of the OmniParser sidecar, e.g.
	// "http://host-a.example.com:7860". Required.
	Endpoint string

	// ParsePath is the relative URL of the parse endpoint. Default:
	// "/parse".
	ParsePath string

	// HTTPClient is the underlying transport. Default: 30s timeout.
	HTTPClient *http.Client
}

// New returns a Client bound to the given endpoint.
func New(endpoint string) *Client {
	return &Client{Endpoint: endpoint}
}

// Sentinel errors.
var (
	ErrEmptyEndpoint = errors.New("helixqa/agent/omniparser: Endpoint not set")
	ErrNilImage      = errors.New("helixqa/agent/omniparser: nil screenshot")
	ErrInvalidBBox   = errors.New("helixqa/agent/omniparser: element bbox is not [x1, y1, x2, y2]")
)

// ---------------------------------------------------------------------------
// Wire / result types
// ---------------------------------------------------------------------------

// Element is one detected UI element, normalized into HelixQA's coord
// conventions. OmniParser's wire Type field is passed through verbatim
// so downstream consumers can apply their own ARIA / class maps.
type Element struct {
	BBox        image.Rectangle `json:"bbox"`
	Type        string          `json:"type"`
	Text        string          `json:"text,omitempty"`
	Content     string          `json:"content,omitempty"`
	Interactive bool            `json:"interactive"`
	Confidence  float64         `json:"confidence"`
}

// Result is the full OmniParser output for a single screenshot.
type Result struct {
	Width    int       `json:"width"`
	Height   int       `json:"height"`
	Elements []Element `json:"elements"`
}

// Center returns the pixel center of the element's bbox — the point
// UI-TARS's click action should target.
func (e Element) Center() image.Point {
	return image.Point{
		X: (e.BBox.Min.X + e.BBox.Max.X) / 2,
		Y: (e.BBox.Min.Y + e.BBox.Max.Y) / 2,
	}
}

// FindInteractiveContaining returns the smallest interactive element
// whose bbox contains the given point, or (zero, false) if no
// interactive element covers the point. Used by the grounding layer
// to cross-check a VLM-proposed click coordinate against the
// OmniParser element grid.
func (r Result) FindInteractiveContaining(p image.Point) (Element, bool) {
	var best Element
	var bestArea int
	found := false
	for _, e := range r.Elements {
		if !e.Interactive {
			continue
		}
		if !pointIn(e.BBox, p) {
			continue
		}
		area := bboxArea(e.BBox)
		if !found || area < bestArea {
			best = e
			bestArea = area
			found = true
		}
	}
	return best, found
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

// ---------------------------------------------------------------------------
// Intermediate wire types — keep them private so the exported Element /
// Result types stay orthogonal from server-side quirks.
// ---------------------------------------------------------------------------

type wireResult struct {
	Width    int           `json:"width"`
	Height   int           `json:"height"`
	Elements []wireElement `json:"elements"`
}

type wireElement struct {
	BBox        []int   `json:"bbox"` // [x1, y1, x2, y2]
	Type        string  `json:"type"`
	Text        string  `json:"text"`
	Content     string  `json:"content"`
	Interactive bool    `json:"interactive"`
	Confidence  float64 `json:"confidence"`
}

// ---------------------------------------------------------------------------
// Parse — the main entry point.
// ---------------------------------------------------------------------------

// Parse sends the screenshot to OmniParser and returns the parsed
// element grid. Respects ctx cancellation.
func (c *Client) Parse(ctx context.Context, screenshot image.Image) (Result, error) {
	if c.Endpoint == "" {
		return Result{}, ErrEmptyEndpoint
	}
	if screenshot == nil {
		return Result{}, ErrNilImage
	}

	body, contentType, err := buildMultipartPNG(screenshot)
	if err != nil {
		return Result{}, fmt.Errorf("omniparser: encode: %w", err)
	}

	path := c.ParsePath
	if path == "" {
		path = "/parse"
	}
	url := c.Endpoint + path

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, body)
	if err != nil {
		return Result{}, fmt.Errorf("omniparser: new request: %w", err)
	}
	req.Header.Set("Content-Type", contentType)

	client := c.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}

	resp, err := client.Do(req)
	if err != nil {
		return Result{}, fmt.Errorf("omniparser: call: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return Result{}, fmt.Errorf("omniparser: HTTP %d: %s", resp.StatusCode, string(b))
	}

	var wire wireResult
	if err := json.NewDecoder(resp.Body).Decode(&wire); err != nil {
		return Result{}, fmt.Errorf("omniparser: decode: %w", err)
	}
	return convertWireResult(wire)
}

// buildMultipartPNG encodes img and wraps it in a multipart form body
// with field name "image". Returns (body, content-type header value).
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

// convertWireResult normalizes the server-side wire shape into the
// exported Result / Element types. Validates bbox shape and drops
// elements that fail validation (with a wrapped ErrInvalidBBox).
func convertWireResult(w wireResult) (Result, error) {
	out := Result{Width: w.Width, Height: w.Height, Elements: make([]Element, 0, len(w.Elements))}
	for i, we := range w.Elements {
		if len(we.BBox) != 4 {
			return Result{}, fmt.Errorf("%w: element %d has bbox %v", ErrInvalidBBox, i, we.BBox)
		}
		out.Elements = append(out.Elements, Element{
			BBox:        image.Rect(we.BBox[0], we.BBox[1], we.BBox[2], we.BBox[3]),
			Type:        we.Type,
			Text:        we.Text,
			Content:     we.Content,
			Interactive: we.Interactive,
			Confidence:  we.Confidence,
		})
	}
	return out, nil
}
