// SPDX-FileCopyrightText: 2026 Milos Vasic
// SPDX-License-Identifier: Apache-2.0

package navigator

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	osexec "os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"digital.vasic.helixqa/pkg/detector"
)

// PlaywrightExecutor implements ActionExecutor for web browsers
// via a headless Chromium instance controlled by a Node.js
// Playwright bridge script. The bridge manages the browser
// lifecycle and accepts JSON commands over stdin/stdout.
type PlaywrightExecutor struct {
	browserURL string
	cmdRunner  detector.CommandRunner
	bridgePath string
	launched   bool
	mu         sync.Mutex
}

// NewPlaywrightExecutor creates a PlaywrightExecutor.
func NewPlaywrightExecutor(
	browserURL string,
	runner detector.CommandRunner,
) *PlaywrightExecutor {
	return &PlaywrightExecutor{
		browserURL: browserURL,
		cmdRunner:  runner,
	}
}

// findBridge locates the playwright-bridge.js script relative
// to the HelixQA binary or project root.
func (p *PlaywrightExecutor) findBridge() string {
	if p.bridgePath != "" {
		return p.bridgePath
	}
	// Check common locations.
	candidates := []string{
		"helix_qa/scripts/playwright-bridge.js",
		"scripts/playwright-bridge.js",
	}
	// Try relative to executable.
	if exe, err := os.Executable(); err == nil {
		dir := filepath.Dir(exe)
		candidates = append(candidates,
			filepath.Join(dir, "..", "scripts",
				"playwright-bridge.js"),
		)
	}
	// Try relative to working directory.
	if wd, err := os.Getwd(); err == nil {
		for _, c := range candidates {
			full := filepath.Join(wd, c)
			if _, err := os.Stat(full); err == nil {
				p.bridgePath = full
				return full
			}
		}
	}
	// Absolute fallback.
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			p.bridgePath = c
			return c
		}
	}
	return "scripts/playwright-bridge.js"
}

// ensureLaunched starts the browser if not already running.
// The bridge script launches headless Chromium as a detached
// process, writes the CDP endpoint to a state file, then
// exits. Subsequent action calls reconnect via CDP.
func (p *PlaywrightExecutor) ensureLaunched(
	ctx context.Context,
) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.launched {
		return nil
	}

	bridge := p.findBridge()
	// Derive API URL from browser URL (replace :3000 with :8080).
	apiURL := strings.Replace(p.browserURL, ":3000", ":8080", 1)
	if envAPI := os.Getenv("BROWSING_API_URL"); envAPI != "" {
		apiURL = envAPI
	}
	cmd := map[string]interface{}{
		"action": "launch",
		"url":    p.browserURL,
		"apiUrl": apiURL,
	}
	cmdJSON, _ := json.Marshal(cmd)

	launchCtx, cancel := context.WithTimeout(
		ctx, 90*time.Second,
	)
	defer cancel()

	proc := osexec.CommandContext(
		launchCtx, "node", bridge,
	)
	proc.Stdin = bytes.NewReader(cmdJSON)
	// Use /tmp for browser state file — always exists.
	proc.Env = append(os.Environ(),
		"HELIX_OUTPUT_DIR=/tmp",
		"NODE_PATH="+nodePath(),
	)

	out, err := proc.Output()
	if err != nil {
		if exitErr, ok := err.(*osexec.ExitError); ok {
			return fmt.Errorf(
				"launch browser: %w: %s",
				err,
				strings.TrimSpace(
					string(exitErr.Stderr),
				),
			)
		}
		return fmt.Errorf("launch browser: %w", err)
	}
	fmt.Printf(
		"  [playwright] browser launched: %s\n",
		strings.TrimSpace(string(out)),
	)
	p.launched = true
	return nil
}

// runBridgeCmd sends a JSON command to the bridge and returns
// the raw stdout output.
func (p *PlaywrightExecutor) runBridgeCmd(
	ctx context.Context,
	cmd map[string]interface{},
) ([]byte, error) {
	if err := p.ensureLaunched(ctx); err != nil {
		return nil, err
	}

	bridge := p.findBridge()
	cmdJSON, _ := json.Marshal(cmd)

	execCtx, cancel := context.WithTimeout(
		ctx, 30*time.Second,
	)
	defer cancel()

	proc := osexec.CommandContext(execCtx, "node", bridge)
	proc.Stdin = bytes.NewReader(cmdJSON)
	proc.Env = append(os.Environ(),
		"HELIX_OUTPUT_DIR=/tmp",
		"NODE_PATH="+nodePath(),
	)

	out, err := proc.Output()
	if err != nil {
		// Include stderr in the error for debugging.
		if exitErr, ok := err.(*osexec.ExitError); ok {
			return nil, fmt.Errorf(
				"bridge: %w: %s",
				err, strings.TrimSpace(
					string(exitErr.Stderr),
				),
			)
		}
		return nil, fmt.Errorf("bridge: %w", err)
	}
	return out, nil
}

// nodePath returns the best NODE_PATH for finding Playwright.
//
// Resolution order (first hit wins):
//  1. The HELIXQA_PLAYWRIGHT_NODE_PATH env var (explicit override).
//  2. The NODE_PATH env var (standard Node lookup).
//  3. Any directory matching `*web*/node_modules/playwright` under
//     the working directory or its parent — generic so any project
//     whose web front-end lives in a differently-named directory
//     works without HelixQA knowing that name.
//  4. The working directory's own `node_modules/playwright`.
func nodePath() string {
	if v := os.Getenv("HELIXQA_PLAYWRIGHT_NODE_PATH"); v != "" {
		return v
	}
	if v := os.Getenv("NODE_PATH"); v != "" {
		return v
	}
	wd, err := os.Getwd()
	if err != nil {
		return ""
	}
	// Generic pattern search — matches any sibling dir containing a
	// Playwright install, without hardcoding project-specific names.
	patterns := []string{
		filepath.Join(wd, "*web*", "node_modules"),
		filepath.Join(wd, "*frontend*", "node_modules"),
		filepath.Join(wd, "..", "*web*", "node_modules"),
		filepath.Join(wd, "..", "*frontend*", "node_modules"),
		filepath.Join(wd, "node_modules"),
	}
	for _, pattern := range patterns {
		matches, _ := filepath.Glob(pattern)
		for _, c := range matches {
			if _, err := os.Stat(filepath.Join(c, "playwright")); err == nil {
				return c
			}
		}
	}
	return ""
}

// Click dispatches a click at coordinates.
func (p *PlaywrightExecutor) Click(
	ctx context.Context, x, y int,
) error {
	_, err := p.runBridgeCmd(ctx, map[string]interface{}{
		"action": "click",
		"x":      x,
		"y":      y,
	})
	return err
}

// Type enters text via Playwright keyboard.
func (p *PlaywrightExecutor) Type(
	ctx context.Context, text string,
) error {
	_, err := p.runBridgeCmd(ctx, map[string]interface{}{
		"action": "type",
		"text":   text,
	})
	return err
}

// Clear selects all text and deletes it via Playwright
// keyboard shortcuts.
func (p *PlaywrightExecutor) Clear(
	ctx context.Context,
) error {
	// Ctrl+A selects all, then Backspace deletes.
	if err := p.KeyPress(ctx, "Control+a"); err != nil {
		return err
	}
	return p.KeyPress(ctx, "Backspace")
}

// Scroll scrolls the page in the given direction.
func (p *PlaywrightExecutor) Scroll(
	ctx context.Context, direction string, amount int,
) error {
	_, err := p.runBridgeCmd(ctx, map[string]interface{}{
		"action":    "scroll",
		"direction": direction,
		"amount":    amount,
	})
	return err
}

// LongPress performs a long press at the given coordinates.
func (p *PlaywrightExecutor) LongPress(
	ctx context.Context, x, y int,
) error {
	_, err := p.runBridgeCmd(ctx, map[string]interface{}{
		"action": "longpress",
		"x":      x,
		"y":      y,
	})
	return err
}

// Swipe performs a drag gesture.
func (p *PlaywrightExecutor) Swipe(
	ctx context.Context, fromX, fromY, toX, toY int,
) error {
	_, err := p.runBridgeCmd(ctx, map[string]interface{}{
		"action": "swipe",
		"fromX":  fromX,
		"fromY":  fromY,
		"toX":    toX,
		"toY":    toY,
	})
	return err
}

// KeyPress simulates a key press.
func (p *PlaywrightExecutor) KeyPress(
	ctx context.Context, key string,
) error {
	name, ok := playwrightKeyName(key)
	if !ok {
		return fmt.Errorf("web key press: %q has no Playwright equivalent", key)
	}
	_, err := p.runBridgeCmd(ctx, map[string]interface{}{
		"action": "key",
		"key":    name,
	})
	return err
}

// Back navigates back in the browser.
func (p *PlaywrightExecutor) Back(ctx context.Context) error {
	_, err := p.runBridgeCmd(ctx, map[string]interface{}{
		"action": "back",
	})
	return err
}

// Home navigates to the browser URL.
func (p *PlaywrightExecutor) Home(ctx context.Context) error {
	_, err := p.runBridgeCmd(ctx, map[string]interface{}{
		"action": "navigate",
		"url":    p.browserURL,
	})
	return err
}

// Screenshot captures the page as PNG.
func (p *PlaywrightExecutor) Screenshot(
	ctx context.Context,
) ([]byte, error) {
	data, err := p.runBridgeCmd(ctx, map[string]interface{}{
		"action": "screenshot",
	})
	if err != nil {
		return nil, fmt.Errorf("playwright screenshot: %w", err)
	}
	return data, nil
}

// androidToPlaywrightKey maps the Android key names the autonomous loop emits
// to the names Playwright's keyboard.press understands. Playwright throws
// "Unknown key" for a KEYCODE_* name, so without this map every "select",
// "enter", "tab" and coordinate-less "click" action failed on a web target and
// the page was never driven.
var androidToPlaywrightKey = map[string]string{
	"KEYCODE_DPAD_CENTER": "Enter", "KEYCODE_ENTER": "Enter", "ENTER": "Enter",
	"KEYCODE_NUMPAD_ENTER": "Enter",
	"KEYCODE_TAB":          "Tab", "TAB": "Tab",
	"KEYCODE_DPAD_UP": "ArrowUp", "KEYCODE_DPAD_DOWN": "ArrowDown",
	"KEYCODE_DPAD_LEFT": "ArrowLeft", "KEYCODE_DPAD_RIGHT": "ArrowRight",
	"KEYCODE_SPACE":  "Space",
	"KEYCODE_ESCAPE": "Escape", "KEYCODE_BACK": "Escape",
	"KEYCODE_DEL": "Backspace", "KEYCODE_FORWARD_DEL": "Delete",
	"KEYCODE_PAGE_UP": "PageUp", "KEYCODE_PAGE_DOWN": "PageDown",
	"KEYCODE_MOVE_HOME": "Home", "KEYCODE_MOVE_END": "End",
}

// playwrightKeyName returns the Playwright key name for k. An Android name is
// translated; a name that is not an Android KEYCODE_* is passed through as the
// caller's own Playwright name. An unmapped KEYCODE_* is reported as not ok so
// the caller fails loudly instead of sending a key the browser cannot press.
func playwrightKeyName(k string) (string, bool) {
	if v, ok := androidToPlaywrightKey[k]; ok {
		return v, true
	}
	if strings.HasPrefix(k, "KEYCODE_") {
		return "", false
	}
	return k, true
}
