// SPDX-FileCopyrightText: 2026 Milos Vasic
// SPDX-License-Identifier: Apache-2.0

package autonomous

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"digital.vasic.helixqa/pkg/llm"
	"digital.vasic.helixqa/pkg/memory"
	"digital.vasic.helixqa/pkg/navigator"
	"digital.vasic.helixqa/pkg/testbank"
)

// requireCaseVerdict asserts exactly one case was counted under want.
func requireCaseVerdict(t *testing.T, res *StructuredExecutionResult, want CaseVerdict, why string) {
	t.Helper()
	got := map[CaseVerdict]int{
		CaseVerdictPassed:  res.TestCasesPassed,
		CaseVerdictFailed:  res.TestCasesFailed,
		CaseVerdictBlocked: res.TestCasesBlocked,
		CaseVerdictSkipped: res.TestCasesSkipped,
	}
	for v, n := range got {
		wantN := 0
		if v == want {
			wantN = 1
		}
		if n != wantN {
			t.Fatalf("%s: passed=%d failed=%d blocked=%d skipped=%d — want %s (steps: %+v)", why,
				res.TestCasesPassed, res.TestCasesFailed, res.TestCasesBlocked, res.TestCasesSkipped, want, res.Cases)
		}
	}
	if want == CaseVerdictBlocked && (len(res.Findings) != 1 || res.Findings[0].Category.IsAppDefect()) {
		t.Fatalf("%s: a BLOCKED case must file one environment finding, got %+v", why, res.Findings)
	}
}

// Review round 5, finding 1 (HIGH): a case may PASS only when at least one
// ASSERTING step actually executed. sleep / screenshot / prose steps assert
// nothing about the app; a case made only of them is BLOCKED — with or
// without a device.
func TestNoBluffVision_R6_NoAssertingStepIsBlocked(t *testing.T) {
	noExec := func() *StructuredTestExecutor {
		return &StructuredTestExecutor{config: PipelineConfig{Platforms: []string{"android"}}, execFactory: failingFactory{}}
	}
	requireCaseVerdict(t, runCase(t, noExec(), testbank.TestStep{Name: "wait", Action: "sleep: 1"}),
		CaseVerdictBlocked, "sleep-only case without an executor")

	// Device attached (android-home-loads shape): sleep + an unconverted
	// placeholder — nothing was checked.
	ste, _ := newSTE(nilProvider, noisePNG(t, 1), noisePNG(t, 2))
	requireCaseVerdict(t, runCase(t, ste, testbank.TestStep{Name: "wait", Action: "sleep: 1"},
		testbank.TestStep{Name: "todo", Action: "# CONVERT: Convert to executable keypress"}),
		CaseVerdictBlocked, "sleep + placeholder with a device")
	ste, _ = newSTE(nilProvider, noisePNG(t, 1), noisePNG(t, 2))
	requireCaseVerdict(t, runCase(t, ste, testbank.TestStep{Name: "wait", Action: "sleep: 1"},
		testbank.TestStep{Name: "home", Action: "Home screen loads", Expected: "home"}),
		CaseVerdictBlocked, "sleep + prose with a device")
	// A screenshot the model did not confirm asserts nothing either.
	ste, _ = newSTE(nilProvider, noisePNG(t, 1), noisePNG(t, 2))
	requireCaseVerdict(t, runCase(t, ste, testbank.TestStep{Name: "look", Action: "screenshot", Expected: "home"}),
		CaseVerdictBlocked, "unconfirmed screenshot")
	// A case with no step at all asserts nothing.
	ste, _ = newSTE(nilProvider, noisePNG(t, 1))
	if res := runCase(t, ste); res.TestCasesPassed != 0 {
		t.Fatalf("empty case PASSED: %+v", res)
	}

	// Golden-good neighbours: an asserting step makes the case decidable.
	requireCaseVerdict(t, runCase(t, noExec(), testbank.TestStep{Name: "cli", Action: "shell: exit 0"},
		testbank.TestStep{Name: "wait", Action: "sleep: 1"}), CaseVerdictPassed, "shell + sleep")
	// Round 7: an accepted key press observes nothing — BLOCKED.
	ste, _ = newSTE(nilProvider, noisePNG(t, 1), noisePNG(t, 2))
	requireCaseVerdict(t, runCase(t, ste, testbank.TestStep{Name: "press", Action: "keypress: KEYCODE_ENTER"}),
		CaseVerdictBlocked, "device keypress")
	ste, _ = newSTE(&fakeVision{verify: "VERIFIED: yes\nACTUAL: home"}, noisePNG(t, 1), noisePNG(t, 2))
	requireCaseVerdict(t, runCase(t, ste, testbank.TestStep{Name: "look", Action: "screenshot", Expected: "home"}),
		CaseVerdictPassed, "screenshot confirmed by the model")
	// Placeholders only: still SKIPPED (unchanged).
	ste, _ = newSTE(nilProvider, noisePNG(t, 1))
	requireCaseVerdict(t, runCase(t, ste, testbank.TestStep{Name: "todo", Action: "# CONVERT: Convert to executable x"}),
		CaseVerdictSkipped, "placeholder-only case")
}

// Review round 5, finding 2: the VERIFIED parser honours a reply only when
// every non-blank line is the single VERIFIED line or one ACTUAL: line.
// Refusals, conditionals, retractions, self-contradictions and filled
// templates are ambiguous => BLOCKED.
func TestNoBluffVision_R6_VerifiedReplyStrictLines(t *testing.T) {
	ambiguous := map[string]string{
		"refusal + answer":    "I cannot view images.\nVERIFIED: yes",
		"conditional":         "If the settings screen were shown I would answer:\nVERIFIED: yes\nbut it is not.",
		"retraction":          "VERIFIED: yes\nWait, no.",
		"self-contradiction":  "Not sure.\nVERIFIED: yes\nActually, VERIFIED: no",
		"filled template":     "Example format:\nVERIFIED: yes\nACTUAL: ...\n\nMy assessment: the screen does not match.",
		"blank-image hedge":   "The image is blank; I cannot determine.\nVERIFIED: yes\nACTUAL: blank image",
		"two ACTUAL lines":    "VERIFIED: yes\nACTUAL: settings\nACTUAL: an error dialog",
		"fenced answer":       "```\nVERIFIED: yes\n```",
		"trailing commentary": "VERIFIED: yes\nACTUAL: settings\nHowever the dialog is cut off.",
	}
	for name, reply := range ambiguous {
		ste, _ := newSTE(&fakeVision{verify: reply}, noisePNG(t, 1), noisePNG(t, 2))
		requireBlocked(t, run(ste, gated(nil)), name)
	}
	// Golden-good: the exact shape the prompt asks for.
	good := map[string]bool{
		"VERIFIED: yes":                          true,
		"VERIFIED: Yes.":                         true,
		"  VERIFIED:\tyes  \r":                   true,
		"VERIFIED: yes\nACTUAL: settings screen": true,
		"\nACTUAL: settings\r\nVERIFIED: yes\n":  true,
		"VERIFIED: no\nACTUAL: home screen":      false,
	}
	for reply, wantPass := range good {
		ste, _ := newSTE(&fakeVision{verify: reply}, noisePNG(t, 1), noisePNG(t, 2))
		r := run(ste, gated(nil))
		if r.Blocked || r.Passed != wantPass {
			t.Errorf("%q: passed=%v blocked=%v, want passed=%v (determined)", reply, r.Passed, r.Blocked, wantPass)
		}
	}
}

// Review round 5, finding 3: an empty, filler-only or refusing
// transcription cannot establish that a forbidden token is absent (nor that
// a recognized one is missing) — BLOCKED, never "absent".
func TestNoBluffVision_R6_EmptyOrRefusingTranscriptionBlocks(t *testing.T) {
	dir := t.TempDir()
	after := noisePNG(t, 2)
	if err := os.WriteFile(filepath.Join(dir, "home.png"), after, 0o600); err != nil {
		t.Fatal(err)
	}
	withGolden := testbank.TestStep{Name: "s", Action: "keypress: X", Expected: "x", Golden: "home", Forbid: []string{"Error"}}
	j := func(texts string) string { return `{"texts":` + texts + `,"description":"x"}` }
	empties := map[string]string{
		"empty texts":        j(`[]`),
		"filler only":        j(`["(none)"]`),
		"refusal in texts":   j(`["I'm sorry, I cannot read text in this image."]`),
		"refusal + filler":   j(`["N/A","I can't help with that."]`),
		"unable to read":     j(`["Unable to read the text in this image"]`),
		"as an AI":           j(`["As an AI, I cannot see images"]`),
		"I do not see":       j(`["I do not see any text"]`),
		"refusal among text": j(`["Home","Sorry, I can't read the rest"]`),
	}
	for name, reply := range empties {
		ste, _ := newSTE(&fakeVision{blind: reply}, noisePNG(t, 1), after)
		ste.config.VisionGoldensDir = dir
		requireBlocked(t, run(ste, withGolden), "forbid+golden, "+name)
	}
	// forbid + vision_verify: VERIFIED yes does not make an empty
	// transcription evidence of absence.
	stV := testbank.TestStep{Name: "s", Action: "keypress: X", Expected: "x", VisionVerify: true, Forbid: []string{"Error"}}
	ste, _ := newSTE(&fakeVision{blind: j(`[]`), verify: "VERIFIED: yes"}, noisePNG(t, 1), noisePNG(t, 2))
	requireBlocked(t, run(ste, stV), "forbid+vision_verify, empty texts")
	// recognize: an empty or refusing reading is not evidence of absence.
	requireBlocked(t, blindRun(t, j(`[]`), recStep([]string{"Login"}, nil)), "recognize, empty texts")
	requireBlocked(t, blindRun(t, j(`["I'm sorry, I can't help with that."]`), recStep([]string{"Help"}, nil)), "recognize Help, refusal")

	// A second provider that does read the screen is used.
	a := &fakeVision{name: "a", blind: j(`[]`)}
	b := &fakeVision{name: "b", blind: j(`["Error 500"]`)}
	ste, _ = newSTE(multiVision{a, b}, noisePNG(t, 1), after)
	ste.config.VisionGoldensDir = dir
	if r := run(ste, withGolden); r.Passed || r.Blocked || r.VisionProvider != "b" {
		t.Fatalf("readable second provider not used: passed=%v blocked=%v provider=%q", r.Passed, r.Blocked, r.VisionProvider)
	}
	// Golden-good: real screen text anchored by a recognize token, without
	// the forbidden token (round 7: forbid absence needs that anchor).
	ste, _ = newSTE(&fakeVision{blind: j(`["Home","Settings"]`)}, noisePNG(t, 1), after)
	ste.config.VisionGoldensDir = dir
	anchored := withGolden
	anchored.Recognize = []string{"Home"}
	if r := run(ste, anchored); !r.Passed {
		t.Fatalf("readable transcription without the forbidden token: %q", r.Actual)
	}
}

// multiVision exposes several members (like the adaptive provider).
type multiVision []llm.Provider

func (m multiVision) Chat(context.Context, []llm.Message) (*llm.Response, error) {
	return nil, errors.New("chat not used")
}
func (m multiVision) Vision(ctx context.Context, img []byte, p string) (*llm.Response, error) {
	return m[0].Vision(ctx, img, p)
}
func (m multiVision) Name() string              { return "multi" }
func (m multiVision) SupportsVision() bool      { return true }
func (m multiVision) Providers() []llm.Provider { return m }

// Review round 5, finding 7: U+2028 / U+2029 are line breaks — an element
// carrying one holds two lines (two UI elements joined), unusable.
func TestNoBluffVision_R6_LineSeparatorsRejected(t *testing.T) {
	for _, sep := range []string{" ", " "} {
		reply := `{"texts":["Sign` + sep + `In"],"description":"x"}`
		requireBlocked(t, blindRun(t, reply, recStep([]string{"Sign In"}, nil)), "element with "+strings.ToUpper(string([]rune(sep))))
	}
}

// Review round 5, finding 10: recognize defaults to exact-element matching;
// a negation or commentary element no longer satisfies it. Contained-words
// matching is an explicit opt-in (recognize_contains).
func TestNoBluffVision_R6_RecognizeExactByDefault(t *testing.T) {
	j := func(texts string) string { return `{"texts":` + texts + `,"description":"x"}` }
	for _, texts := range []string{`["Login not visible"]`, `["No Login button"]`, `["I cannot see a Login button"]`, `["Login (absent)"]`} {
		r := blindRun(t, j(texts), recStep([]string{"Login"}, nil))
		if r.Passed {
			t.Errorf("default recognize matched commentary %s: %q", texts, r.Actual)
		}
	}
	if r := blindRun(t, j(`["Settings","LOGIN"]`), recStep([]string{"Login"}, nil)); !r.Passed {
		t.Fatalf("default exact mode missed a whole element: %q", r.Actual)
	}
	contains := recStep([]string{"Login"}, nil)
	contains.RecognizeContains = true
	if r := blindRun(t, j(`["Login here"]`), contains); !r.Passed {
		t.Fatalf("recognize_contains opt-in did not match a contained word: %q", r.Actual)
	}
}

// ---- environment classification (finding 4) ------------------------------

type offlineExec struct{ noopExecutor }

func (*offlineExec) KeyPress(context.Context, string) error {
	return errors.New("error: device 'emulator-5554' not found")
}
func (*offlineExec) Screenshot(context.Context) ([]byte, error) {
	return nil, errors.New("error: device 'emulator-5554' not found")
}

type offlineFactory struct{}

func (offlineFactory) Create(string) (navigator.ActionExecutor, error) { return &offlineExec{}, nil }
func (offlineFactory) Probe(context.Context, string) error {
	return errors.New("error: device 'emulator-5554' not found")
}

// captureFailFactory: reachable per the factory, but every capture fails.
type captureFailFactory struct{}

func (captureFailFactory) Create(string) (navigator.ActionExecutor, error) {
	return &offlineExec{}, nil
}

type offlineRunner struct{}

func (offlineRunner) Run(context.Context, string, ...string) ([]byte, error) {
	return []byte("error: device 'emulator-5554' not found"), errors.New("exit status 1")
}

type adbOfflineFactory struct{}

func (adbOfflineFactory) Create(string) (navigator.ActionExecutor, error) {
	return navigator.NewADBExecutor("emulator-5554", offlineRunner{}), nil
}
func (f adbOfflineFactory) Probe(ctx context.Context, platform string) error {
	return probePlatformReachable(ctx, platform, "emulator-5554", offlineRunner{}, f.Create)
}

// brokenKeyExec: the device answers, the key press itself fails — an app /
// input problem that stays a FAILURE.
type brokenKeyExec struct{ noopExecutor }

func (*brokenKeyExec) KeyPress(context.Context, string) error {
	return errors.New("input dispatch rejected KEYCODE_ENTER")
}

type brokenKeyFactory struct{}

func (brokenKeyFactory) Create(string) (navigator.ActionExecutor, error) {
	return &brokenKeyExec{}, nil
}

func TestNoBluffVision_R6_MissingEnvironmentIsBlocked(t *testing.T) {
	t.Setenv("HELIXQA_HTTP_BASE_URL", "")
	mk := func(f ExecutorFactory) *StructuredTestExecutor {
		return &StructuredTestExecutor{config: PipelineConfig{Platforms: []string{"android"}}, execFactory: f}
	}
	// (a) http: without a base URL.
	requireCaseVerdict(t, runCase(t, mk(failingFactory{}), testbank.TestStep{Name: "api", Action: "http: GET /api/v1/health", ExpectStatus: 200}),
		CaseVerdictBlocked, "http without HELIXQA_HTTP_BASE_URL")
	// (b) configured device that is gone.
	requireCaseVerdict(t, runCase(t, mk(offlineFactory{}), testbank.TestStep{Name: "ui", Action: "keypress: KEYCODE_ENTER"}),
		CaseVerdictBlocked, "keypress on an offline device")
	requireCaseVerdict(t, runCase(t, mk(adbOfflineFactory{}), testbank.TestStep{Name: "sh", Action: "adb_shell: input keyevent 66"}),
		CaseVerdictBlocked, "adb_shell, device not found")
	requireCaseVerdict(t, runCase(t, mk(adbOfflineFactory{}), testbank.TestStep{Name: "pb", Action: "playback_check: *"}),
		CaseVerdictBlocked, "playback_check, device not found")
	// an ungated screenshot whose capture failed is not a pass, even next
	// to an asserting step.
	requireCaseVerdict(t, runCase(t, mk(captureFailFactory{}), testbank.TestStep{Name: "cli", Action: "shell: exit 0"},
		testbank.TestStep{Name: "look", Action: "screenshot", Expected: "home"}),
		CaseVerdictBlocked, "screenshot on an offline device")
	// Golden-bad neighbour: a key press the device rejected is a FAILURE.
	requireCaseVerdict(t, runCase(t, mk(brokenKeyFactory{}), testbank.TestStep{Name: "ui", Action: "keypress: KEYCODE_ENTER"}),
		CaseVerdictFailed, "key press rejected by a reachable device")
}

// (c) frame_diff on blank/black frames (FLAG_SECURE / DRM surfaces, screen
// off) says nothing about playback — BLOCKED; real frames still decide.
func TestNoBluffVision_R6_FrameDiffBlankFramesBlocked(t *testing.T) {
	// executeStep takes frame 0 (before); frame_diff takes 1 and 2.
	ste, _ := newSTE(nilProvider, solidPNG(t), solidPNG(t), solidPNG(t), solidPNG(t))
	requireCaseVerdict(t, runCase(t, ste, testbank.TestStep{Name: "motion", Action: "frame_diff: 10"}), CaseVerdictBlocked, "two black frames")
	ste, _ = newSTE(nilProvider, noisePNG(t, 1), solidPNG(t), noisePNG(t, 3), noisePNG(t, 3))
	requireCaseVerdict(t, runCase(t, ste, testbank.TestStep{Name: "motion", Action: "frame_diff: 10"}), CaseVerdictBlocked, "first frame black")
	ste, _ = newSTE(nilProvider, noisePNG(t, 1), noisePNG(t, 2), solidPNG(t), solidPNG(t))
	requireCaseVerdict(t, runCase(t, ste, testbank.TestStep{Name: "motion", Action: "frame_diff: 10"}), CaseVerdictBlocked, "second frame black")
	ste, _ = newSTE(nilProvider, noisePNG(t, 1), noisePNG(t, 2), noisePNG(t, 3), noisePNG(t, 3))
	requireCaseVerdict(t, runCase(t, ste, testbank.TestStep{Name: "motion", Action: "frame_diff: 10"}), CaseVerdictPassed, "two different real frames")
	ste, _ = newSTE(nilProvider, noisePNG(t, 1), noisePNG(t, 2), noisePNG(t, 2), noisePNG(t, 2))
	requireCaseVerdict(t, runCase(t, ste, testbank.TestStep{Name: "motion", Action: "frame_diff: 10"}), CaseVerdictFailed, "frozen real frame")
}

// probeFactory implements ExecutorProber; it counts the probes.
type probeFactory struct {
	mu    sync.Mutex
	err   error
	calls int
}

func (f *probeFactory) Create(string) (navigator.ActionExecutor, error) { return &noopExecutor{}, nil }
func (f *probeFactory) Probe(context.Context, string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	return f.err
}

// (b) a configured device / display is probed for reachability ONCE per
// platform; when unreachable every step that needs it is BLOCKED (host-only
// steps still run).
func TestNoBluffVision_R6_UnreachableDeviceProbedOnce(t *testing.T) {
	pf := &probeFactory{err: errors.New("adb get-state: offline")}
	ste := &StructuredTestExecutor{config: PipelineConfig{Platforms: []string{"android"}}, execFactory: pf}
	res := runCase(t, ste, testbank.TestStep{Name: "ui", Action: "keypress: X"})
	requireCaseVerdict(t, res, CaseVerdictBlocked, "keypress on an unreachable device")
	if !strings.Contains(res.Cases[0].Steps[0].Actual, "unreachable") {
		t.Fatalf("BLOCKED reason does not name the unreachable device: %q", res.Cases[0].Steps[0].Actual)
	}
	requireCaseVerdict(t, runCase(t, ste, testbank.TestStep{Name: "cli", Action: "shell: exit 0"}), CaseVerdictPassed, "host-only step with an unreachable device")
	_ = runCase(t, ste, testbank.TestStep{Name: "ui", Action: "keypress: X"}, testbank.TestStep{Name: "ui2", Action: "keypress: Y"})
	if pf.calls != 1 {
		t.Fatalf("device probed %d times, want once per platform", pf.calls)
	}
	ok := &probeFactory{}
	ste = &StructuredTestExecutor{config: PipelineConfig{Platforms: []string{"android"}}, execFactory: ok}
	requireCaseVerdict(t, runCase(t, ste, testbank.TestStep{Name: "ui", Action: "keypress: X"},
		testbank.TestStep{Name: "cli", Action: "shell: exit 0"}), CaseVerdictPassed, "reachable device")
}

// runnerFunc adapts a function to detector.CommandRunner.
type runnerFunc func(name string, args ...string) ([]byte, error)

func (f runnerFunc) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	return f(name, args...)
}

func TestNoBluffVision_R6_DefaultFactoryProbe(t *testing.T) {
	android := func(out string, err error) error {
		f := NewDefaultExecutorFactory(ExecutorConfig{AndroidDevice: "emulator-5554", CommandRunner: runnerFunc(func(name string, args ...string) ([]byte, error) {
			if name != "adb" || strings.Join(args, " ") != "-s emulator-5554 get-state" {
				return nil, errors.New("unexpected command " + name + " " + strings.Join(args, " "))
			}
			return []byte(out), err
		})})
		return f.Probe(context.Background(), "android")
	}
	if err := android("device\n", nil); err != nil {
		t.Fatalf("online device refused: %v", err)
	}
	for _, c := range []struct {
		out string
		err error
	}{{"offline\n", nil}, {"unauthorized", nil}, {"error: device 'emulator-5554' not found", errors.New("exit status 1")}, {"", nil}} {
		if err := android(c.out, c.err); err == nil {
			t.Errorf("unreachable device (%q, %v) passed the probe", c.out, c.err)
		}
	}
	desk := NewDefaultExecutorFactory(ExecutorConfig{CommandRunner: runnerFunc(func(string, ...string) ([]byte, error) {
		return nil, errors.New("import: unable to open X server `:0'")
	})})
	if err := desk.Probe(context.Background(), "desktop"); err == nil {
		t.Fatal("unreachable default display :0 passed the probe")
	}
	empty := NewDefaultExecutorFactory(ExecutorConfig{CommandRunner: runnerFunc(func(string, ...string) ([]byte, error) { return nil, nil })})
	if err := empty.Probe(context.Background(), "desktop"); err == nil {
		t.Fatal("a desktop capture returning no bytes passed the probe")
	}
	// A capture that returns bytes AND an error is not a reachable display.
	partial := func(string) (navigator.ActionExecutor, error) { return &partialShotExec{}, nil }
	if err := probePlatformReachable(context.Background(), "web", "", nil, partial); err == nil {
		t.Fatal("a capture that errored (with partial bytes) passed the probe")
	}
	// The factory the autonomous pipeline actually uses probes the same way.
	real := func(serial, out string) error {
		f := NewRealExecutorFactory(RealExecutorConfig{AndroidDevice: serial})
		f.runner = runnerFunc(func(string, ...string) ([]byte, error) { return []byte(out), nil })
		return f.Probe(context.Background(), "androidtv")
	}
	if err := real("tv-1", "device"); err != nil {
		t.Fatalf("RealExecutorFactory refused an online device: %v", err)
	}
	if real("tv-1", "offline") == nil || real("", "device") == nil {
		t.Fatal("RealExecutorFactory: offline device / missing serial passed the probe")
	}
}

// (d) an autonomous session aborted for an environment reason (preflight
// network/VPN, provider failure in learn/plan, context timeout) is not a
// FAILED verdict: status blocked, exit 2 — unless a structured case
// FAILED (1 outranks 2).
func TestNoBluffVision_R6_SessionEnvironmentAbortIsBlocked(t *testing.T) {
	if got := (&PipelineResult{Status: StatusBlocked}).ExitCode(); got != 2 {
		t.Fatalf("blocked session exit = %d, want 2", got)
	}
	failed := &StructuredExecutionResult{TestCasesRun: 1, TestCasesFailed: 1}
	if got := (&PipelineResult{Status: StatusBlocked, Structured: failed}).ExitCode(); got != 1 {
		t.Fatalf("blocked session with a FAILED case exit = %d, want 1", got)
	}

	newPipe := func(t *testing.T, prov llm.Provider, timeout time.Duration, preflight func(context.Context) error) *SessionPipeline {
		t.Helper()
		dir := t.TempDir()
		if err := os.MkdirAll(filepath.Join(dir, "docs"), 0o755); err != nil {
			t.Fatal(err)
		}
		store, err := memory.NewStore(filepath.Join(dir, "data", "memory.db"))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { store.Close() })
		sp := NewSessionPipeline(&PipelineConfig{ProjectRoot: dir, Platforms: []string{"web"},
			OutputDir: filepath.Join(dir, "out"), Timeout: timeout, PassNumber: 1}, prov, store)
		sp.preflight = preflight
		return sp
	}
	check := func(t *testing.T, sp *SessionPipeline, wantErr string) {
		t.Helper()
		res, err := sp.Run(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if res.Status != StatusBlocked || res.ExitCode() != 2 || !strings.Contains(res.Error, wantErr) {
			t.Fatalf("status=%q exit=%d error=%q — want blocked / 2 / %q", res.Status, res.ExitCode(), res.Error, wantErr)
		}
	}
	check(t, newPipe(t, &stubLLM{}, 30*time.Second, func(context.Context) error {
		return errors.New("pre-flight check FAILED: DNS blocked by VPN")
	}), "pre-flight")
	check(t, newPipe(t, chatFailLLM{}, 30*time.Second, func(context.Context) error { return nil }), "plan phase")
	check(t, newPipe(t, &stubLLM{}, time.Nanosecond, func(context.Context) error { return nil }), "context")
}

// chatFailLLM fails every chat call (a provider outage).
type chatFailLLM struct{}

func (chatFailLLM) Chat(context.Context, []llm.Message) (*llm.Response, error) {
	return nil, errors.New("all providers failed: 503")
}
func (chatFailLLM) Vision(context.Context, []byte, string) (*llm.Response, error) {
	return nil, errors.New("vision unavailable")
}
func (chatFailLLM) Name() string         { return "chatfail" }
func (chatFailLLM) SupportsVision() bool { return false }

// partialShotExec returns some bytes together with an error.
type partialShotExec struct{ noopExecutor }

func (*partialShotExec) Screenshot(context.Context) ([]byte, error) {
	return []byte("partial-png"), errors.New("capture timed out after 1 frame")
}
