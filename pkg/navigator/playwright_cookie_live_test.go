// SPDX-FileCopyrightText: 2026 Milos Vasic
// SPDX-License-Identifier: Apache-2.0

package navigator

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// A console behind a passkey login cannot be driven by the autonomous loop: it
// has no credential and no authenticator, so it only ever sees the sign-in
// page. HELIX_WEB_COOKIE_FILE lets the operator hand the browser an
// ALREADY-ESTABLISHED session (cookies obtained by their own login, e.g. a
// passkey ceremony with a software authenticator). These tests drive the real
// bridge and Chromium against a real local server that renders a different
// page when its session cookie is present.

func sessionServer(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		bg := "#ffffff" // anonymous
		if c, err := r.Cookie("sid"); err == nil && c.Value == "session-ok" {
			bg = "#123456" // authenticated
		}
		fmt.Fprintf(w, `<html><body style="margin:0;background:%s"><h1>page</h1></body></html>`, bg)
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func writeCookieFile(t *testing.T, mode os.FileMode, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "cookies.json")
	require.NoError(t, os.WriteFile(p, []byte(body), mode))
	require.NoError(t, os.Chmod(p, mode))
	return p
}

func cookieShot(t *testing.T, url string, env map[string]string) ([]byte, error) {
	t.Helper()
	for k, v := range env {
		t.Setenv(k, v)
	}
	pe := keycodeExecutor(t, url)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	return pe.Screenshot(ctx)
}

const goodCookies = `[{"name":"sid","value":"session-ok","path":"/"}]`

func TestBridge_NoCookieFile_SeesAnonymousPage(t *testing.T) {
	requireBrowserTools(t)
	shot, err := cookieShot(t, sessionServer(t), nil)
	require.NoError(t, err)
	require.Less(t, pageColourShare(t, shot), 0.05, "control: without a session the page is the anonymous (white) one")
}

func TestBridge_CookieFile_EstablishesSession(t *testing.T) {
	requireBrowserTools(t)
	shot, err := cookieShot(t, sessionServer(t), map[string]string{
		"HELIX_WEB_COOKIE_FILE": writeCookieFile(t, 0o600, goodCookies)})
	require.NoError(t, err)
	require.Greater(t, pageColourShare(t, shot), 0.4, "the supplied session cookie must reach the browser")
}

func TestBridge_CookieFile_LooseModeRefused(t *testing.T) {
	requireBrowserTools(t)
	_, err := cookieShot(t, sessionServer(t), map[string]string{
		"HELIX_WEB_COOKIE_FILE": writeCookieFile(t, 0o644, goodCookies)})
	require.Error(t, err, "a credential file readable by group/other must be refused, never used")
	require.Contains(t, err.Error(), "HELIX_WEB_COOKIE_FILE")
}

func TestBridge_CookieFile_MalformedRefused(t *testing.T) {
	requireBrowserTools(t)
	for name, body := range map[string]string{
		"not json":       `nope`,
		"not an array":   `{"name":"sid","value":"x"}`,
		"empty array":    `[]`,
		"no value":       `[{"name":"sid"}]`,
		"no name":        `[{"value":"x"}]`,
		"foreign domain": `[{"name":"sid","value":"x","domain":"evil.example"}]`,
	} {
		body := body
		t.Run(name, func(t *testing.T) {
			_, err := cookieShot(t, sessionServer(t), map[string]string{
				"HELIX_WEB_COOKIE_FILE": writeCookieFile(t, 0o600, body)})
			require.Error(t, err)
			require.Contains(t, err.Error(), "HELIX_WEB_COOKIE_FILE")
		})
	}
}

func TestBridge_CookieFile_Missing_Refused(t *testing.T) {
	requireBrowserTools(t)
	_, err := cookieShot(t, sessionServer(t), map[string]string{
		"HELIX_WEB_COOKIE_FILE": filepath.Join(t.TempDir(), "nope.json")})
	require.Error(t, err)
	require.Contains(t, err.Error(), "HELIX_WEB_COOKIE_FILE")
}

// The value of a cookie is a credential: it must never appear in an error.
func TestBridge_CookieFile_ValueNeverInError(t *testing.T) {
	requireBrowserTools(t)
	_, err := cookieShot(t, sessionServer(t), map[string]string{
		"HELIX_WEB_COOKIE_FILE": writeCookieFile(t, 0o600, `[{"name":"sid","value":"TOPSECRETVALUE","domain":"evil.example"}]`)})
	require.Error(t, err)
	require.NotContains(t, err.Error(), "TOPSECRETVALUE")
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
