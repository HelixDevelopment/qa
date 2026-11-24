// SPDX-FileCopyrightText: 2026 Milos Vasic
// SPDX-License-Identifier: Apache-2.0

package navigator

import (
	"context"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Two bridge-lifecycle defects found driving a real authenticated console:
//  1. a launch refused at the cookie-install step left its Chromium running (orphaned, untracked);
//  2. the CDP port was fixed at 9223, so any other process on that port was connected to (and a
//     pattern kill could terminate a browser this bridge never started).

// browserProcsFor returns the pids of live processes whose command line carries needle.
func browserProcsFor(t *testing.T, needle string) []string {
	t.Helper()
	ents, err := os.ReadDir("/proc")
	require.NoError(t, err)
	var pids []string
	for _, e := range ents {
		if !e.IsDir() || e.Name()[0] < '0' || e.Name()[0] > '9' {
			continue
		}
		b, err := os.ReadFile(filepath.Join("/proc", e.Name(), "cmdline"))
		if err != nil {
			continue
		}
		cmd := strings.ReplaceAll(string(b), "\x00", " ")
		if strings.Contains(cmd, "remote-debugging-port") && strings.Contains(cmd, needle) {
			pids = append(pids, e.Name())
		}
	}
	return pids
}

// A launch whose cookies the browser refuses must not leave a browser behind.
func TestBridge_CookieInstallFailure_LeavesNoBrowser(t *testing.T) {
	requireBrowserTools(t)
	url := sessionServer(t)
	// passes the file validation (any value is allowed for expires), refused by the browser: expires must be a number
	bad := writeCookieFile(t, 0o600, `[{"name":"sid","value":"x","path":"/","expires":"not-a-number"}]`)
	_, err := cookieShot(t, url, map[string]string{"HELIX_WEB_COOKIE_FILE": bad})
	require.Error(t, err, "the launch must be refused")
	require.Contains(t, err.Error(), "could not install the cookies")
	require.Eventually(t, func() bool { return len(browserProcsFor(t, url)) == 0 }, 10*time.Second, 200*time.Millisecond,
		"a refused launch left a Chromium running: %v", browserProcsFor(t, url))
}

// The bridge must not use, or talk to, whatever else listens on the old fixed port 9223.
func TestBridge_CDPPort_IsNotTheFixedPort(t *testing.T) {
	requireBrowserTools(t)
	ln, err := net.Listen("tcp", "127.0.0.1:9223")
	if err != nil {
		t.Skipf("SKIP (topology): port 9223 is in use on this host (%v); cannot stage a stranger there", err)
	}
	var hits int32
	stranger := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"Browser":"stranger"}`))
	})}
	go func() { _ = stranger.Serve(ln) }()
	t.Cleanup(func() { _ = stranger.Close() })

	pe := keycodeExecutor(t, sessionServer(t))
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	shot, err := pe.Screenshot(ctx)
	require.NoError(t, err)
	require.NotEmpty(t, shot)
	require.Zero(t, atomic.LoadInt32(&hits), "the bridge talked to the process listening on 9223")
}
