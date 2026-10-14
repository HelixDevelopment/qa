// SPDX-FileCopyrightText: 2026 Milos Vasic
// SPDX-License-Identifier: Apache-2.0

package autonomous

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/png"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"digital.vasic.helixqa/pkg/memory"
	"digital.vasic.helixqa/pkg/navigator"
	"digital.vasic.helixqa/pkg/testbank"
)

// runCaseCtx runs one case under ctx.
func runCaseCtx(t *testing.T, ctx context.Context, ste *StructuredTestExecutor, steps ...testbank.TestStep) *StructuredExecutionResult {
	t.Helper()
	res := &StructuredExecutionResult{}
	tc := testbank.TestCase{ID: "TC-R7", Name: "r7", Steps: steps}
	if err := ste.executeTestCase(ctx, tc, res); err != nil {
		t.Fatal(err)
	}
	return res
}

func lastActual(res *StructuredExecutionResult) string {
	if len(res.Cases) == 0 || len(res.Cases[0].Steps) == 0 {
		return ""
	}
	s := res.Cases[0].Steps
	return s[len(s)-1].Actual
}

// rangePNG renders grey-level noise whose value lies in [lo, hi].
func rangePNG(t *testing.T, seed int64, lo, hi int) []byte {
	t.Helper()
	r := rand.New(rand.NewSource(seed))
	img := image.NewRGBA(image.Rect(0, 0, 160, 120))
	for y := 0; y < 120; y++ {
		for x := 0; x < 160; x++ {
			v := uint8(lo + r.Intn(hi-lo+1))
			img.Set(x, y, color.RGBA{v, v, v, 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// Review round 6, finding 4 (HIGH): an accepted tap / key press asserts
// nothing about the app. A case PASSes only when a step OBSERVED app state
// (a vision-confirmed screen, http / assert / shell / playback_check /
// frame_diff), and a case with a skipped non-executable step is never
// PASSED.
func TestNoBluffVision_R7_PassNeedsAnObservingStep(t *testing.T) {
	dev := func() *StructuredTestExecutor { s, _ := newSTE(nilProvider, noisePNG(t, 1), noisePNG(t, 2)); return s }
	for name, steps := range map[string][]testbank.TestStep{
		"tap only (android-login-success shape)": {{Name: "tap", Action: "tap: 960,540"}},
		"key presses only":                       {{Name: "k1", Action: "keypress: KEYCODE_DPAD_DOWN"}, {Name: "k2", Action: "keypress: KEYCODE_ENTER"}},
		"text + swipe":                           {{Name: "t", Action: "text: admin"}, {Name: "s", Action: "swipe: 1,2,3,4"}},
		"tap + skipped TODO verify":              {{Name: "tap", Action: "tap: 960,540"}, {Name: "verify", Action: "# TODO: Convert to executable - verify session"}},
		"shell + skipped prose step":             {{Name: "cli", Action: "shell: exit 0"}, {Name: "verify", Action: "Verify session is active"}},
	} {
		requireCaseVerdict(t, runCase(t, dev(), steps...), CaseVerdictBlocked, name)
	}
	// Golden-good: a step that observes app state.
	requireCaseVerdict(t, runCase(t, dev(), testbank.TestStep{Name: "cli", Action: "shell: exit 0"}), CaseVerdictPassed, "shell")
	ste, _ := newSTE(&fakeVision{verify: "VERIFIED: yes\nACTUAL: home"}, noisePNG(t, 1), noisePNG(t, 2))
	requireCaseVerdict(t, runCase(t, ste, testbank.TestStep{Name: "tap", Action: "tap: 1,2", Expected: "home"}), CaseVerdictPassed, "tap confirmed by the model")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) }))
	defer srv.Close()
	hs := dev()
	hs.config.HTTPBaseURL = srv.URL
	requireCaseVerdict(t, runCase(t, hs, testbank.TestStep{Name: "tap", Action: "tap: 1,2"}, testbank.TestStep{Name: "api", Action: "http: GET /health", ExpectStatus: 200}),
		CaseVerdictPassed, "tap + http observation")
	fd, _ := newSTE(nilProvider, noisePNG(t, 1), noisePNG(t, 2), noisePNG(t, 3), noisePNG(t, 4))
	requireCaseVerdict(t, runCase(t, fd, testbank.TestStep{Name: "motion", Action: "frame_diff: 10"}), CaseVerdictPassed, "frame_diff observes motion")
}

// Review round 6, finding 3 (HIGH): a session context that ends before or
// during a step leaves its outcome undetermined — BLOCKED, never a FAILED
// app defect.
func TestNoBluffVision_R7_ContextEndBlocks(t *testing.T) {
	expired, cancel := context.WithCancel(context.Background())
	cancel()
	ste, _ := newSTE(nilProvider, noisePNG(t, 1), noisePNG(t, 2))
	res := runCaseCtx(t, expired, ste, testbank.TestStep{Name: "cli", Action: "shell: true"})
	requireCaseVerdict(t, res, CaseVerdictBlocked, "shell: true with an expired context")
	if !strings.Contains(lastActual(res), "context ended before this step") {
		t.Fatalf("BLOCKED reason does not name the ended context: %q", lastActual(res))
	}
	// A step whose action ignores the context (frame_diff on a fake
	// device) is not run at all once the session ended.
	fd, _ := newSTE(nilProvider, noisePNG(t, 1), noisePNG(t, 2), noisePNG(t, 3), noisePNG(t, 4))
	requireCaseVerdict(t, runCaseCtx(t, expired, fd, testbank.TestStep{Name: "motion", Action: "frame_diff: 10"}), CaseVerdictBlocked, "frame_diff after the session ended")
	// Ends DURING the action.
	short := func() context.Context {
		c, cl := context.WithTimeout(context.Background(), 300*time.Millisecond)
		t.Cleanup(cl)
		return c
	}
	ste, _ = newSTE(nilProvider, noisePNG(t, 1), noisePNG(t, 2))
	requireCaseVerdict(t, runCaseCtx(t, short(), ste, testbank.TestStep{Name: "cli", Action: "shell: sleep 3"}), CaseVerdictBlocked, "shell killed by the session timeout")
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(3 * time.Second):
		case <-r.Context().Done():
		}
		w.WriteHeader(200)
	}))
	defer slow.Close()
	ste, _ = newSTE(nilProvider, noisePNG(t, 1), noisePNG(t, 2))
	ste.config.HTTPBaseURL = slow.URL
	requireCaseVerdict(t, runCaseCtx(t, short(), ste, testbank.TestStep{Name: "api", Action: "http: GET /health", ExpectStatus: 200}), CaseVerdictBlocked, "http cut by the session timeout")
	// Golden-bad: a shell step's OWN timeout is still a failure.
	ste, _ = newSTE(nilProvider, noisePNG(t, 1), noisePNG(t, 2))
	requireCaseVerdict(t, runCase(t, ste, testbank.TestStep{Name: "cli", Action: "shell: exit 4"}), CaseVerdictFailed, "failing shell, live context")
}

// fakeTools installs fake adb / xdotool / import binaries on PATH. The
// device starts reachable; the first UI ACTION prints msg, fails, and
// leaves the device in state next (so the re-probe fails).
func fakeTools(t *testing.T, msg, next string) {
	t.Helper()
	dir := t.TempDir()
	pngPath := filepath.Join(dir, "frame.png")
	if err := os.WriteFile(pngPath, noisePNG(t, 5), 0o600); err != nil {
		t.Fatal(err)
	}
	msgFile := filepath.Join(dir, "msg")
	if err := os.WriteFile(msgFile, []byte(msg+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	state := filepath.Join(dir, "state")
	if err := os.WriteFile(state, []byte("device\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	adb := `#!/bin/sh
st=$(cat "` + state + `")
case "$*" in
  *get-state*) if [ "$st" = device ]; then echo "* daemon started successfully"; echo device; exit 0; fi
               if [ "$st" = unauthorized ]; then echo "error: device unauthorized." >&2; exit 1; fi
               echo "error: device 'emulator-5554' not found" >&2; exit 1 ;;
  *screencap*) if [ "$st" = device ]; then cat "` + pngPath + `"; exit 0; fi
               echo "error: device 'emulator-5554' not found" >&2; exit 1 ;;
  *dumpsys*)   echo "error: device 'emulator-5554' not found" >&2; exit 1 ;;
  *) cat "` + msgFile + `" >&2; echo "` + next + `" > "` + state + `"; exit 1 ;;
esac
`
	x := `#!/bin/sh
cat "` + msgFile + `" >&2; echo "` + next + `" > "` + state + `"; exit 1
`
	imp := `#!/bin/sh
st=$(cat "` + state + `")
if [ "$st" = device ]; then cat "` + pngPath + `"; exit 0; fi
echo "import: unable to open X server" >&2; exit 1
`
	for name, body := range map[string]string{"adb": adb, "xdotool": x, "import": imp} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// Review round 6, finding 2 (HIGH): the REAL executors report only
// "exit status 1", so no message matching can see a vanished device.
// After any device-action error the reachability probe is re-run
// (uncached); when it fails the step is BLOCKED.
func TestNoBluffVision_R7_DeviceGoneMidSessionRealExecutors(t *testing.T) {
	for _, action := range []string{"keypress: KEYCODE_ENTER", "tap: 100,200", "text: hello", "swipe: 1,2,3,4", "adb_shell: input keyevent 66"} {
		fakeTools(t, "error: device 'emulator-5554' not found", "gone")
		f := NewRealExecutorFactory(RealExecutorConfig{AndroidDevice: "emulator-5554"})
		ste := &StructuredTestExecutor{config: PipelineConfig{Platforms: []string{"android"}}, execFactory: f}
		res := runCase(t, ste, testbank.TestStep{Name: "ui", Action: action})
		requireCaseVerdict(t, res, CaseVerdictBlocked, "real ADBExecutor, device vanished, "+action)
		if !strings.Contains(lastActual(res), "unreachable") {
			t.Fatalf("%s: BLOCKED reason does not name the unreachable device: %q", action, lastActual(res))
		}
	}
	fakeTools(t, "error: device unauthorized.", "unauthorized")
	ste := &StructuredTestExecutor{config: PipelineConfig{Platforms: []string{"android"}},
		execFactory: NewDefaultExecutorFactory(ExecutorConfig{AndroidDevice: "emulator-5554"})}
	requireCaseVerdict(t, runCase(t, ste, testbank.TestStep{Name: "ui", Action: "keypress: KEYCODE_ENTER"}), CaseVerdictBlocked, "real ADBExecutor, device unauthorized")
	fakeTools(t, "Error: Can't open display: :0", "gone")
	ste = &StructuredTestExecutor{config: PipelineConfig{Platforms: []string{"desktop"}},
		execFactory: NewRealExecutorFactory(RealExecutorConfig{})}
	requireCaseVerdict(t, runCase(t, ste, testbank.TestStep{Name: "ui", Action: "keypress: Return"}), CaseVerdictBlocked, "real X11Executor, display gone")

	// Finding 11: an adb_shell command whose OWN output says "device not
	// found" on a device that is still reachable is a determined failure.
	fakeTools(t, `{"error":"storage device not found"}`, "device")
	ste = &StructuredTestExecutor{config: PipelineConfig{Platforms: []string{"android"}},
		execFactory: NewRealExecutorFactory(RealExecutorConfig{AndroidDevice: "emulator-5554"})}
	requireCaseVerdict(t, runCase(t, ste, testbank.TestStep{Name: "sh", Action: "adb_shell: cat /sdcard/x"}), CaseVerdictFailed, "adb_shell app output on a reachable device")
}

// perPlatformProber: unreachable only for the listed platforms.
type perPlatformProber struct {
	down  map[string]bool
	calls map[string]int
}

func (p *perPlatformProber) Create(string) (navigator.ActionExecutor, error) {
	return &noopExecutor{}, nil
}
func (p *perPlatformProber) Probe(_ context.Context, platform string) error {
	p.calls[platform]++
	if p.down[platform] {
		return errors.New(platform + " unreachable")
	}
	return nil
}

// Review round 6, findings 10 (N21) and 12: the probe result is kept per
// platform, and a FAILED probe is not cached forever.
func TestNoBluffVision_R7_ProbeCachePerPlatformAndFailureExpires(t *testing.T) {
	p := &perPlatformProber{down: map[string]bool{"android": true}, calls: map[string]int{}}
	ste := &StructuredTestExecutor{config: PipelineConfig{Platforms: []string{"android", "desktop"}}, execFactory: p}
	requireCaseVerdict(t, runCase(t, ste, testbank.TestStep{Name: "a", Action: "keypress: X", Platform: "android"}), CaseVerdictBlocked, "android unreachable")
	requireCaseVerdict(t, runCase(t, ste, testbank.TestStep{Name: "d", Action: "keypress: X", Platform: "desktop"},
		testbank.TestStep{Name: "cli", Action: "shell: exit 0"}), CaseVerdictPassed, "desktop reachable")

	old := failedProbeTTL
	failedProbeTTL = 0
	defer func() { failedProbeTTL = old }()
	p = &perPlatformProber{down: map[string]bool{"android": true}, calls: map[string]int{}}
	ste = &StructuredTestExecutor{config: PipelineConfig{Platforms: []string{"android"}}, execFactory: p}
	requireCaseVerdict(t, runCase(t, ste, testbank.TestStep{Name: "a", Action: "keypress: X"}), CaseVerdictBlocked, "android down")
	p.down["android"] = false
	requireCaseVerdict(t, runCase(t, ste, testbank.TestStep{Name: "a", Action: "keypress: X"}, testbank.TestStep{Name: "cli", Action: "shell: exit 0"}),
		CaseVerdictPassed, "android back: the failed probe expired")
	// A successful probe stays cached.
	ok := &perPlatformProber{calls: map[string]int{}}
	ste = &StructuredTestExecutor{config: PipelineConfig{Platforms: []string{"android"}}, execFactory: ok}
	_ = runCase(t, ste, testbank.TestStep{Name: "a", Action: "keypress: X"}, testbank.TestStep{Name: "b", Action: "keypress: Y"})
	if ok.calls["android"] != 1 {
		t.Fatalf("reachable platform probed %d times", ok.calls["android"])
	}
}

// Finding 12: `adb get-state` output is judged on its last non-empty line
// (a "* daemon started successfully" line must not make a device
// unreachable).
func TestNoBluffVision_R7_ProbeReadsLastLine(t *testing.T) {
	for out, wantOK := range map[string]bool{
		"* daemon not running; starting now at tcp:5037\n* daemon started successfully\ndevice\n": true,
		"adb: warning: stdin not a tty\ndevice":                                                   true,
		"device\noffline\n":                                                                       false,
		"\n\n":                                                                                    false,
	} {
		o := out
		f := NewDefaultExecutorFactory(ExecutorConfig{AndroidDevice: "e", CommandRunner: runnerFunc(func(string, ...string) ([]byte, error) { return []byte(o), nil })})
		if err := f.Probe(context.Background(), "android"); (err == nil) != wantOK {
			t.Errorf("get-state %q: err=%v, want reachable=%v", out, err, wantOK)
		}
	}
}

// Review round 6, finding 13: frame_diff BLOCKs only on black frames; a
// uniform grey slate or a dark low-contrast scene is judged.
func TestNoBluffVision_R7_FrameDiffJudgesNonBlackLowContrast(t *testing.T) {
	grey := rangePNG(t, 1, 128, 128)
	ste, _ := newSTE(nilProvider, noisePNG(t, 1), grey, grey, grey)
	requireCaseVerdict(t, runCase(t, ste, testbank.TestStep{Name: "m", Action: "frame_diff: 10"}), CaseVerdictFailed, "frozen grey slate")
	ste, _ = newSTE(nilProvider, noisePNG(t, 1), rangePNG(t, 2, 20, 38), rangePNG(t, 3, 20, 38), noisePNG(t, 4))
	requireCaseVerdict(t, runCase(t, ste, testbank.TestStep{Name: "m", Action: "frame_diff: 10"}), CaseVerdictPassed, "moving dark low-contrast scene")
	ste, _ = newSTE(nilProvider, noisePNG(t, 1), rangePNG(t, 2, 0, 10), rangePNG(t, 3, 0, 10), noisePNG(t, 4))
	requireCaseVerdict(t, runCase(t, ste, testbank.TestStep{Name: "m", Action: "frame_diff: 10"}), CaseVerdictBlocked, "near-black frames")
}

// Review round 6, finding 14: assert: reads the last http: response of
// THIS case; without an http: step in the case it is BLOCKED (bank
// defect), not an app failure.
func TestNoBluffVision_R7_AssertNeedsHTTPInSameCase(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) }))
	defer srv.Close()
	ste, _ := newSTE(nilProvider, noisePNG(t, 1), noisePNG(t, 2))
	ste.config.HTTPBaseURL = srv.URL
	requireCaseVerdict(t, runCase(t, ste, testbank.TestStep{Name: "api", Action: "http: GET /x", ExpectStatus: 200},
		testbank.TestStep{Name: "chk", Action: "assert: status_eq: 200"}), CaseVerdictPassed, "http + assert in one case")
	requireCaseVerdict(t, runCase(t, ste, testbank.TestStep{Name: "chk", Action: "assert: status_eq: 200"}), CaseVerdictBlocked, "assert reusing another case's response")
	fresh, _ := newSTE(nilProvider, noisePNG(t, 1), noisePNG(t, 2))
	requireCaseVerdict(t, runCase(t, fresh, testbank.TestStep{Name: "chk", Action: "assert: status_eq: 200"}), CaseVerdictBlocked, "assert with no http at all")
}

// Review round 6, findings 5 + 6 + 7: a forbidden token PRESENT fails the
// step whatever else the reading holds; an ABSENCE is credited only when
// the same reading matched a positive anchor (recognize) and is not a
// refusal; a recognize token found only inside a longer element is
// BLOCKED with a pointer to recognize_contains.
func TestNoBluffVision_R7_PresenceFirstAbsenceNeedsAnchor(t *testing.T) {
	dir := t.TempDir()
	after := noisePNG(t, 2)
	if err := os.WriteFile(filepath.Join(dir, "home.png"), after, 0o600); err != nil {
		t.Fatal(err)
	}
	j := func(texts string) string { return `{"texts":` + texts + `,"description":"x"}` }
	runR := func(reply string, st testbank.TestStep) TestStepResult {
		ste, _ := newSTE(&fakeVision{blind: j(reply)}, noisePNG(t, 1), after)
		ste.config.VisionGoldensDir = dir
		return run(ste, st)
	}
	forbidG := func(tok ...string) testbank.TestStep {
		return testbank.TestStep{Name: "s", Action: "keypress: X", Expected: "x", Golden: "home", Forbid: tok}
	}
	recF := func(rec, forb string) testbank.TestStep { return recStep([]string{rec}, []string{forb}) }
	fails := map[string]struct {
		texts string
		st    testbank.TestStep
	}{
		"app banner that reads like a refusal": {`["Unable to process image"]`, forbidG("Unable to process image")},
		"forbidden next to a refusal":          {`["Error 500","Sorry, I can't read the rest"]`, forbidG("Error")},
		"FAQ text + real error":                {`["I can't see my library","Error: sync failed"]`, forbidG("Error")},
		"forbidden in an all-refusal reading":  {`["I'm sorry, an Error occurred"]`, recF("Login", "Error")},
	}
	for name, c := range fails {
		if r := runR(c.texts, c.st); r.Passed || r.Blocked {
			t.Errorf("%s: passed=%v blocked=%v, want FAILED (forbidden present): %q", name, r.Passed, r.Blocked, r.Actual)
		}
	}
	blocked := map[string]struct {
		texts string
		st    testbank.TestStep
	}{
		"forbid absent, no positive anchor": {`["Home","Settings"]`, forbidG("Error")},
		"refusal outside the phrase list":   {`["Lo siento, no puedo ver la imagen"]`, forbidG("Error")},
		"anchor missing":                    {`["Home"]`, recF("Login", "Error")},
		"all-refusal reading, recognize":    {`["I apologize, but I cannot analyze this image."]`, recStep([]string{"Login"}, nil)},
		"'The image ...' remark":            {`["The image is too dark to read"]`, recStep([]string{"Login"}, nil)},
		"contained, not equal":              {`["Sign In to Catalogizer"]`, recStep([]string{"Sign In"}, nil)},
	}
	for name, c := range blocked {
		r := runR(c.texts, c.st)
		if name == "anchor missing" {
			// recognize truly absent from a real reading: determined failure.
			if r.Passed || r.Blocked {
				t.Errorf("%s: want FAILED, got passed=%v blocked=%v", name, r.Passed, r.Blocked)
			}
			continue
		}
		requireBlocked(t, r, name)
		if name == "contained, not equal" && !strings.Contains(r.Actual, "recognize_contains") {
			t.Errorf("contained-not-equal reason does not point to recognize_contains: %q", r.Actual)
		}
	}
	// Golden-good.
	if r := runR(`["Home","Settings"]`, recF("Home", "Error")); !r.Passed {
		t.Errorf("anchor matched, forbidden absent: %q", r.Actual)
	}
	if r := runR(`["As an AI developer tool, Catalogizer indexes media","Settings"]`, recStep([]string{"Settings"}, nil)); !r.Passed {
		t.Errorf("real app text that starts like a model remark, next to real text: %q", r.Actual)
	}
}

// Review round 6, finding 8: the ACTUAL line is scanned too — a refusal or
// a second verdict there makes the reply ambiguous.
func TestNoBluffVision_R7_ActualLineScanned(t *testing.T) {
	for _, reply := range []string{
		"VERIFIED: yes\nACTUAL: I cannot see the image",
		"VERIFIED: yes\nACTUAL: VERIFIED: no",
		"VERIFIED: yes\nACTUAL: (unable to view images; assuming yes)",
		"VERIFIED: yes\nACTUAL: settings; verified: no, an error dialog",
	} {
		ste, _ := newSTE(&fakeVision{verify: reply}, noisePNG(t, 1), noisePNG(t, 2))
		requireBlocked(t, run(ste, gated(nil)), reply)
	}
	ste, _ := newSTE(&fakeVision{verify: "VERIFIED: yes\nACTUAL: settings screen, a Verified badge is shown"}, noisePNG(t, 1), noisePNG(t, 2))
	if r := run(ste, gated(nil)); !r.Passed {
		t.Fatalf("plain ACTUAL mentioning a 'Verified' badge refused: %q", r.Actual)
	}
}

// Review round 6, finding 10 (N11): a learn-phase error (project docs
// unreadable) blocks the session (exit 2), never FAILS it.
func TestNoBluffVision_R7_LearnErrorBlocks(t *testing.T) {
	dir := t.TempDir()
	docs := filepath.Join(dir, "docs")
	if err := os.MkdirAll(docs, 0o755); err != nil {
		t.Fatal(err)
	}
	secret := filepath.Join(docs, "secret.md")
	if err := os.WriteFile(secret, []byte("x"), 0o000); err != nil {
		t.Fatal(err)
	}
	if _, err := os.ReadFile(secret); err == nil {
		t.Fatal("fixture broken: a mode-000 file is readable (running as root?)")
	}
	store, err := memory.NewStore(filepath.Join(dir, "data", "memory.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	sp := NewSessionPipeline(&PipelineConfig{ProjectRoot: dir, Platforms: []string{"web"},
		OutputDir: filepath.Join(dir, "out"), Timeout: 30 * time.Second, PassNumber: 1}, &stubLLM{}, store)
	sp.preflight = func(context.Context) error { return nil }
	res, err := sp.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != StatusBlocked || res.ExitCode() != 2 || !strings.Contains(res.Error, "learn phase") {
		t.Fatalf("status=%q exit=%d error=%q — want blocked / 2 / learn phase", res.Status, res.ExitCode(), res.Error)
	}
}
