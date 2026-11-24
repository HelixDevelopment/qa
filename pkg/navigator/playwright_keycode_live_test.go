// SPDX-FileCopyrightText: 2026 Milos Vasic
// SPDX-License-Identifier: Apache-2.0

package navigator

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// The autonomous loop speaks Android key names ("KEYCODE_DPAD_CENTER",
// "KEYCODE_ENTER", "KEYCODE_TAB", ...). Playwright's keyboard.press does not
// know them: "Unknown key" is thrown, so on a web target "select", "enter" and
// a click without coordinates never reached the page. These tests drive the
// REAL bridge and Chromium against a REAL local page and read the effect off
// the rendered pixels.

const keycodePage = `<html><body id="b" style="margin:0;background:#ffffff">
<button id="go" autofocus style="padding:60px;font-size:40px"
 onclick="document.getElementById('b').style.background='#123456'">Go</button>
</body></html>`

func keycodeServer(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, keycodePage)
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func keycodeExecutor(t *testing.T, url string) *PlaywrightExecutor {
	t.Helper()
	t.Setenv("HELIX_OUTPUT_DIR", t.TempDir())
	wd, _ := filepath.Abs(".")
	pe := NewPlaywrightExecutor(url, nil)
	pe.bridgePath = filepath.Join(wd, "..", "..", "scripts", "playwright-bridge.js")
	t.Cleanup(func() {
		_, _ = pe.runBridgeCmd(context.Background(), map[string]interface{}{"action": "close"})
	})
	return pe
}

func TestBridge_AndroidKeycodes_ReachTheWebPage(t *testing.T) {
	requireBrowserTools(t)
	for _, key := range []string{"KEYCODE_DPAD_CENTER", "KEYCODE_ENTER", "ENTER", "KEYCODE_SPACE"} {
		key := key
		t.Run(key, func(t *testing.T) {
			pe := keycodeExecutor(t, keycodeServer(t))
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			before, err := pe.Screenshot(ctx)
			require.NoError(t, err)
			require.Less(t, pageColourShare(t, before), 0.05, "control: page starts white")
			require.NoError(t, pe.KeyPress(ctx, key), "an Android key name must be understood by the web executor")
			time.Sleep(500 * time.Millisecond)
			after, err := pe.Screenshot(ctx)
			require.NoError(t, err)
			require.Greater(t, pageColourShare(t, after), 0.3,
				"the focused button must have been activated by %s (page turns #123456)", key)
		})
	}
}

func TestBridge_UnknownKey_StillRefused(t *testing.T) {
	requireBrowserTools(t)
	pe := keycodeExecutor(t, keycodeServer(t))
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	_, err := pe.Screenshot(ctx)
	require.NoError(t, err)
	require.Error(t, pe.KeyPress(ctx, "KEYCODE_NOT_A_REAL_KEY"),
		"a key that maps to nothing must stay an error, never a silent success")
}

func TestPlaywrightKeyName_Table(t *testing.T) {
	for in, want := range map[string]string{
		"KEYCODE_DPAD_CENTER": "Enter", "KEYCODE_ENTER": "Enter", "ENTER": "Enter",
		"KEYCODE_TAB": "Tab", "KEYCODE_DPAD_UP": "ArrowUp", "KEYCODE_DPAD_DOWN": "ArrowDown",
		"KEYCODE_DPAD_LEFT": "ArrowLeft", "KEYCODE_DPAD_RIGHT": "ArrowRight",
		"KEYCODE_SPACE": "Space", "KEYCODE_ESCAPE": "Escape", "KEYCODE_BACK": "Escape",
		"KEYCODE_DEL": "Backspace", "KEYCODE_FORWARD_DEL": "Delete",
		"KEYCODE_PAGE_DOWN": "PageDown", "KEYCODE_PAGE_UP": "PageUp",
		"KEYCODE_MOVE_HOME": "Home", "KEYCODE_MOVE_END": "End",
		"Enter": "Enter", "ArrowDown": "ArrowDown", "a": "a",
	} {
		got, ok := playwrightKeyName(in)
		require.True(t, ok, in)
		require.Equal(t, want, got, in)
	}
	_, ok := playwrightKeyName("KEYCODE_NOT_A_REAL_KEY")
	require.False(t, ok)
}
