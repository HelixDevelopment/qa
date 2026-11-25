// SPDX-FileCopyrightText: 2026 Milos Vasic
// SPDX-License-Identifier: Apache-2.0

package navigator

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	osexec "os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// HELIX_WEB_VIDEO_DIR records the bridge's ONE page (its CDP screencast, not the host display) into a WEBM when the
// bridge's close runs. These tests drive the real bridge, Chromium, ffmpeg and a real local server whose page changes
// colour while the session is open, so a video of a frozen first frame (or none) cannot pass.

func requireFFTools(t *testing.T) {
	t.Helper()
	for _, b := range []string{"ffmpeg", "ffprobe"} {
		if _, err := osexec.LookPath(b); err != nil {
			t.Skip("SKIP topology: " + b + " not on PATH")
		}
	}
}

func changingServer(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<html><body style="margin:0;background:#ffffff"><h1>page</h1><script>
var c=['#aa0000','#00aa00','#0000aa','#aaaa00'],i=0;setInterval(function(){document.body.style.background=c[i++%4]},300);</script></body></html>`)
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

// videoFrames returns the decoded frame count of a webm, or an error when it has no video stream.
func videoFrames(t *testing.T, file string) (int, error) {
	t.Helper()
	out, err := osexec.Command("ffprobe", "-v", "error", "-count_frames", "-select_streams", "v:0",
		"-show_entries", "stream=nb_read_frames", "-of", "default=nw=1:nk=1", file).Output()
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(string(out)))
}

func recordSession(t *testing.T, env map[string]string) (dir string, closeOut string) {
	t.Helper()
	requireBrowserTools(t)
	requireFFTools(t)
	dir = t.TempDir()
	if env != nil {
		for k, v := range env {
			t.Setenv(k, strings.ReplaceAll(v, "@DIR@", dir))
		}
	}
	pe := keycodeExecutor(t, changingServer(t))
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	_, err := pe.Screenshot(ctx)
	require.NoError(t, err)
	time.Sleep(2 * time.Second) // the page changes colour four times a second-ish while recorded
	_, err = pe.Screenshot(ctx)
	require.NoError(t, err)
	b, err := pe.runBridgeCmd(ctx, map[string]interface{}{"action": "close"})
	require.NoError(t, err)
	return dir, string(b)
}

func TestBridge_VideoDir_RecordsThePageAsWebm(t *testing.T) {
	dir, closeOut := recordSession(t, map[string]string{"HELIX_WEB_VIDEO_DIR": "@DIR@"})
	files, _ := filepath.Glob(filepath.Join(dir, "web-*.webm"))
	require.Len(t, files, 1, "close must leave exactly one video; close said: %s", closeOut)
	n, err := videoFrames(t, files[0])
	require.NoError(t, err, "the file must be a decodable video")
	require.GreaterOrEqual(t, n, 3, "a page that changed colour repeatedly must produce several frames, not one frozen frame")
	require.Contains(t, closeOut, `"status":"ok"`, "close reports the finished video")
}

func TestBridge_NoVideoDir_RecordsNothing(t *testing.T) {
	_, closeOut := recordSession(t, nil)
	require.NotContains(t, closeOut, "video", "without HELIX_WEB_VIDEO_DIR no recorder runs and close reports no video")
}

func TestBridge_VideoDir_UnstartableRecorderAbortsLaunch(t *testing.T) {
	requireBrowserTools(t)
	requireFFTools(t)
	// a path under a regular file can never be created: the recorder cannot start, so the launch must fail, not run unrecorded
	f := filepath.Join(t.TempDir(), "afile")
	require.NoError(t, osexec.Command("touch", f).Run())
	t.Setenv("HELIX_WEB_VIDEO_DIR", filepath.Join(f, "sub"))
	pe := keycodeExecutor(t, changingServer(t))
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	_, err := pe.Screenshot(ctx)
	require.Error(t, err)
	require.Contains(t, err.Error(), "HELIX_WEB_VIDEO_DIR")
}
