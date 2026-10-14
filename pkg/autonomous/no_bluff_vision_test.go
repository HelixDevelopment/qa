// SPDX-FileCopyrightText: 2026 Milos Vasic
// SPDX-License-Identifier: Apache-2.0

// no_bluff_vision_test.go is the no_bluff_vision gate: it pins the
// invariants that make HelixQA's vision "eyes" honest.
//
//  1. A vision-gated step (vision_verify / recognize / forbid / golden /
//     expect_change) can PASS only on positive, frame-bound evidence.
//  2. No provider, a provider error, an ambiguous or empty reply, a blank
//     frame or a missing frame make a gated step BLOCKED
//     (could-not-determine, rc semantic 2) — never PASS, never a
//     "fallback to action success".
//  3. A non-gated step that was not vision-confirmed is reported
//     UNVERIFIED-by-vision, never "verified".
//  4. Every verdict carries the sha256 + pHash of the exact frame bytes
//     the model saw.
//  5. recognize/forbid are checked deterministically against an
//     expectation-BLIND description (the tokens never reach the prompt);
//     golden is a pHash distance check; expect_change rejects an
//     unchanged frame.
//
// Each invariant has a golden-good and a golden-bad fixture, and
// challenges/scripts/no_bluff_vision_mutation_challenge.sh reverts each
// fix under a go -overlay and requires these tests to go RED (§1.1).

package autonomous

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"image"
	"image/color"
	"image/png"
	"io"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"digital.vasic.helixqa/pkg/llm"
	"digital.vasic.helixqa/pkg/navigator"
	"digital.vasic.helixqa/pkg/testbank"
)

// ── fixtures ────────────────────────────────────────────────────────

// noisePNG renders a deterministic random-noise PNG: never blank,
// distinct per seed (framesDiffer=true across seeds, large pHash distance).
func noisePNG(t *testing.T, seed int64) []byte {
	t.Helper()
	r := rand.New(rand.NewSource(seed))
	img := image.NewRGBA(image.Rect(0, 0, 160, 120))
	for y := 0; y < 120; y++ {
		for x := 0; x < 160; x++ {
			img.Set(x, y, color.RGBA{uint8(r.Intn(256)), uint8(r.Intn(256)), uint8(r.Intn(256)), 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// solidPNG renders a uniform frame — the IsBlankScreenshot golden-TRUE case.
func solidPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 160, 120))
	for y := 0; y < 120; y++ {
		for x := 0; x < 160; x++ {
			img.Set(x, y, color.RGBA{0, 0, 0, 255})
		}
	}
	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	return buf.Bytes()
}

func sha(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

// screenExec returns frames[i] on the i-th Screenshot (the last frame
// repeats). executeStep takes exactly two: before-action, after-action.
type screenExec struct {
	noopExecutor
	mu     sync.Mutex
	frames [][]byte
	errAt  map[int]error
	n      int
}

func (s *screenExec) Screenshot(context.Context) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	i := s.n
	s.n++
	if err := s.errAt[i]; err != nil {
		return nil, err
	}
	if i >= len(s.frames) {
		i = len(s.frames) - 1
	}
	return s.frames[i], nil
}

type screenFactory struct{ ex *screenExec }

func (f screenFactory) Create(string) (navigator.ActionExecutor, error) { return f.ex, nil }

// fakeVision distinguishes the expectation-blind description call from
// the VERIFIED call by the prompt's requested format.
type fakeVision struct {
	mu          sync.Mutex
	name        string
	onCall      func()
	unsupported bool
	verify      string
	verifyErr   error
	blind       string
	blindErr    error
	images      [][]byte
	prompts     []string
}

func (f *fakeVision) Chat(context.Context, []llm.Message) (*llm.Response, error) {
	return nil, errors.New("chat not used")
}
func (f *fakeVision) Name() string {
	if f.name != "" {
		return f.name
	}
	return "fake-vision"
}
func (f *fakeVision) SupportsVision() bool { return !f.unsupported }
func (f *fakeVision) Vision(_ context.Context, img []byte, prompt string) (*llm.Response, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.images = append(f.images, append([]byte(nil), img...))
	f.prompts = append(f.prompts, prompt)
	if f.onCall != nil {
		f.onCall()
	}
	if strings.Contains(prompt, "VERIFIED:") {
		if f.verifyErr != nil {
			return nil, f.verifyErr
		}
		return &llm.Response{Content: f.verify}, nil
	}
	if f.blindErr != nil {
		return nil, f.blindErr
	}
	return &llm.Response{Content: f.blind}, nil
}
func (f *fakeVision) calls() int { f.mu.Lock(); defer f.mu.Unlock(); return len(f.prompts) }

func newSTE(v llm.Provider, frames ...[]byte) (*StructuredTestExecutor, *screenExec) {
	ex := &screenExec{frames: frames}
	ste := &StructuredTestExecutor{
		config:      PipelineConfig{Platforms: []string{"desktop"}},
		execFactory: screenFactory{ex},
		vision:      v,
	}
	return ste, ex
}

// nilProvider returns a typed-nil-free nil llm.Provider.
var nilProvider llm.Provider

func run(ste *StructuredTestExecutor, st testbank.TestStep) TestStepResult {
	return ste.executeStep(context.Background(), testbank.TestCase{ID: "TC-NBV", Name: "nbv"}, st, 1)
}

func gated(extra func(*testbank.TestStep)) testbank.TestStep {
	st := testbank.TestStep{Name: "press", Action: "keypress: KEYCODE_ENTER", Expected: "settings screen", VisionVerify: true}
	if extra != nil {
		extra(&st)
	}
	return st
}

func requireBlocked(t *testing.T, r TestStepResult, why string) {
	t.Helper()
	if r.Passed {
		t.Fatalf("%s: step PASSED — a could-not-determine outcome was reported as a pass (Actual=%q)", why, r.Actual)
	}
	if !r.Blocked {
		t.Fatalf("%s: step not BLOCKED (Actual=%q, status=%q)", why, r.Actual, r.VisionStatus)
	}
	if r.VisionStatus != VisionBlocked {
		t.Fatalf("%s: VisionStatus=%q, want %q", why, r.VisionStatus, VisionBlocked)
	}
	if !strings.Contains(r.Actual, "BLOCKED") {
		t.Fatalf("%s: Actual does not say BLOCKED: %q", why, r.Actual)
	}
}

// ── invariant 2: gated + no usable vision => BLOCKED ────────────────

func TestNoBluffVision_GatedWithoutProviderIsBlocked(t *testing.T) {
	ste, _ := newSTE(nilProvider, noisePNG(t, 1), noisePNG(t, 2))
	requireBlocked(t, run(ste, gated(nil)), "nil provider")

	ste, _ = newSTE(&fakeVision{unsupported: true, verify: "VERIFIED: yes"}, noisePNG(t, 1), noisePNG(t, 2))
	requireBlocked(t, run(ste, gated(nil)), "provider without vision support")
}

func TestNoBluffVision_GatedProviderErrorIsBlocked(t *testing.T) {
	ste, _ := newSTE(&fakeVision{verifyErr: errors.New("429 rate limited")}, noisePNG(t, 1), noisePNG(t, 2))
	r := run(ste, gated(nil))
	requireBlocked(t, r, "provider error")
	if strings.Contains(r.Actual, "falling back to action success") {
		t.Fatalf("gated step still falls back to action success: %q", r.Actual)
	}
}

func TestNoBluffVision_GatedAmbiguousOrEmptyReplyIsBlocked(t *testing.T) {
	for _, reply := range []string{
		"The screen shows a settings menu with several options.",
		"   ",
		"VERIFIED: yes\nVERIFIED: no", // self-contradiction is ambiguous, not a pass
	} {
		ste, _ := newSTE(&fakeVision{verify: reply}, noisePNG(t, 1), noisePNG(t, 2))
		requireBlocked(t, run(ste, gated(nil)), "reply "+strings.TrimSpace(reply))
	}
}

// ── invariant 1+4: PASS only on positive, frame-bound evidence ───────

func TestNoBluffVision_GatedVerifiedYesPassesAndBindsFrame(t *testing.T) {
	before, after := noisePNG(t, 1), noisePNG(t, 2)
	fv := &fakeVision{verify: "VERIFIED: yes\nACTUAL: settings screen with Wi-Fi toggle"}
	ste, _ := newSTE(fv, before, after)
	r := run(ste, gated(nil))
	if !r.Passed || r.Blocked {
		t.Fatalf("golden-good gated step did not pass: %+v", r)
	}
	if r.VisionStatus != VisionVerified || !r.VisionGated {
		t.Fatalf("status=%q gated=%v", r.VisionStatus, r.VisionGated)
	}
	if r.FrameSHA256 != sha(after) {
		t.Fatalf("verdict not bound to the after-frame: got %s want %s", r.FrameSHA256, sha(after))
	}
	if r.BeforeFrameSHA256 != sha(before) {
		t.Fatalf("before-frame hash not recorded: %s", r.BeforeFrameSHA256)
	}
	if len(r.FramePHash) != 16 {
		t.Fatalf("pHash not recorded as 16 hex digits: %q", r.FramePHash)
	}
	if len(fv.images) != 1 || sha(fv.images[0]) != r.FrameSHA256 {
		t.Fatalf("the model did not see exactly the bound frame")
	}
	if !strings.Contains(r.Actual, r.FrameSHA256[:12]) {
		t.Fatalf("Actual does not cite the frame hash: %q", r.Actual)
	}
}

func TestNoBluffVision_GatedVerifiedNoFails(t *testing.T) {
	ste, _ := newSTE(&fakeVision{verify: "VERIFIED: no\nACTUAL: home screen"}, noisePNG(t, 1), noisePNG(t, 2))
	r := run(ste, gated(nil))
	if r.Passed || r.Blocked || r.VisionStatus != VisionRefuted {
		t.Fatalf("refuted gated step: passed=%v blocked=%v status=%q", r.Passed, r.Blocked, r.VisionStatus)
	}
}

// ── invariant 3: non-gated without vision is UNVERIFIED ──────────────

func TestNoBluffVision_NonGatedWithoutVisionIsUnverified(t *testing.T) {
	plain := testbank.TestStep{Name: "press", Action: "keypress: KEYCODE_ENTER", Expected: "settings screen"}

	ste, _ := newSTE(nilProvider, noisePNG(t, 1), noisePNG(t, 2))
	r := run(ste, plain)
	if !r.Passed || r.Blocked {
		t.Fatalf("non-gated step must still pass on action success: %+v", r)
	}
	if r.VisionStatus != VisionUnverified || !strings.Contains(r.Actual, "UNVERIFIED-by-vision") {
		t.Fatalf("non-gated no-vision step not reported UNVERIFIED-by-vision: status=%q actual=%q", r.VisionStatus, r.Actual)
	}

	ste, _ = newSTE(&fakeVision{verifyErr: errors.New("boom")}, noisePNG(t, 1), noisePNG(t, 2))
	r = run(ste, plain)
	if !r.Passed || r.VisionStatus != VisionUnverified || !strings.Contains(r.Actual, "UNVERIFIED-by-vision") {
		t.Fatalf("provider error on non-gated step: passed=%v status=%q actual=%q", r.Passed, r.VisionStatus, r.Actual)
	}
	if strings.Contains(r.Actual, "falling back to action success") {
		t.Fatalf("legacy fallback wording survived: %q", r.Actual)
	}

	// Golden-good for the non-gated path: an explicit yes is "verified"
	// and an explicit no still fails the step (existing detection kept).
	ste, _ = newSTE(&fakeVision{verify: "VERIFIED: yes\nACTUAL: ok"}, noisePNG(t, 1), noisePNG(t, 2))
	if r = run(ste, plain); !r.Passed || r.VisionStatus != VisionVerified {
		t.Fatalf("non-gated yes: %+v", r)
	}
	ste, _ = newSTE(&fakeVision{verify: "VERIFIED: no\nACTUAL: wrong"}, noisePNG(t, 1), noisePNG(t, 2))
	if r = run(ste, plain); r.Passed || r.VisionStatus != VisionRefuted {
		t.Fatalf("non-gated no: %+v", r)
	}
}

// ── invariant 2/4: blank or missing frame is never verified ──────────

func TestNoBluffVision_BlankFrameRejected(t *testing.T) {
	fv := &fakeVision{verify: "VERIFIED: yes\nACTUAL: looks fine"}
	ste, _ := newSTE(fv, noisePNG(t, 1), solidPNG(t))
	requireBlocked(t, run(ste, gated(nil)), "blank after-frame with a yes verdict")
	if fv.calls() != 0 {
		t.Fatalf("a blank frame was sent to the model (%d calls) — its verdict could only be a bluff", fv.calls())
	}

	// Non-gated: a "yes" about a blank frame is not accepted as verified.
	fv = &fakeVision{verify: "VERIFIED: yes"}
	ste, _ = newSTE(fv, noisePNG(t, 1), solidPNG(t))
	r := run(ste, testbank.TestStep{Name: "p", Action: "keypress: X", Expected: "x"})
	if r.VisionStatus == VisionVerified {
		t.Fatalf("blank frame accepted as vision-verified")
	}
}

func TestNoBluffVision_MissingFrameBlocked(t *testing.T) {
	ste, ex := newSTE(&fakeVision{verify: "VERIFIED: yes"}, noisePNG(t, 1), noisePNG(t, 2))
	ex.errAt = map[int]error{1: errors.New("screencap failed")}
	requireBlocked(t, run(ste, gated(nil)), "after-frame capture error")
}

// ── invariant 5: expect_change ───────────────────────────────────────

func TestNoBluffVision_ExpectChangeRejectsUnchangedFrame(t *testing.T) {
	same := noisePNG(t, 7)
	ste, _ := newSTE(&fakeVision{verify: "VERIFIED: yes"}, same, same)
	r := run(ste, gated(func(s *testbank.TestStep) { s.ExpectChange = true }))
	if r.Passed || r.Blocked {
		t.Fatalf("unchanged frame with expect_change must FAIL: %+v", r)
	}
	if !strings.Contains(r.Actual, "did not change") {
		t.Fatalf("reason does not name the unchanged frame: %q", r.Actual)
	}

	// Golden-good: a real change passes.
	ste, _ = newSTE(&fakeVision{verify: "VERIFIED: yes"}, noisePNG(t, 7), noisePNG(t, 8))
	if r = run(ste, gated(func(s *testbank.TestStep) { s.ExpectChange = true })); !r.Passed {
		t.Fatalf("changed frame with expect_change did not pass: %+v", r)
	}

	// No usable before-frame: change cannot be determined.
	ste, _ = newSTE(&fakeVision{verify: "VERIFIED: yes"}, solidPNG(t), noisePNG(t, 8))
	requireBlocked(t, run(ste, gated(func(s *testbank.TestStep) { s.ExpectChange = true })), "blank before-frame")
}

// ── invariant 5: recognize / forbid ──────────────────────────────────

func TestNoBluffVision_RecognizeForbidTokens(t *testing.T) {
	step := func() testbank.TestStep {
		return testbank.TestStep{
			Name: "login", Action: "keypress: KEYCODE_ENTER", Expected: "login form",
			Recognize: []string{"Sign in", "Password"}, Forbid: []string{"Error"},
		}
	}
	yes := "VERIFIED: yes\nACTUAL: login form"

	// Golden-good: tokens present after normalisation (case, punctuation, spacing).
	fv := &fakeVision{verify: yes, blind: "TEXT:\nSIGN-IN!\nE-mail\npassword;\nContinue\nDESCRIPTION: A login form with two fields and a button."}
	ste, _ := newSTE(fv, noisePNG(t, 1), noisePNG(t, 2))
	r := run(ste, step())
	if !r.Passed {
		t.Fatalf("recognize golden-good failed: %+v", r)
	}
	// The tokens and the expectation never reach the blind prompt.
	var blindPrompt string
	for _, p := range fv.prompts {
		if !strings.Contains(p, "VERIFIED:") {
			blindPrompt = p
		}
	}
	if blindPrompt == "" {
		t.Fatal("no expectation-blind description call was made")
	}
	for _, leak := range []string{"Sign in", "Password", "Error", "login form"} {
		if strings.Contains(strings.ToLower(blindPrompt), strings.ToLower(leak)) {
			t.Fatalf("blind prompt leaks %q — the model could parrot it: %q", leak, blindPrompt)
		}
	}
	if r.VisionDescription == "" {
		t.Fatal("blind description not recorded as evidence")
	}

	// Golden-bad: missing token fails.
	fv = &fakeVision{verify: yes, blind: "TEXT:\nSign in\nUsername\nDESCRIPTION: A form with a password-free login."}
	ste, _ = newSTE(fv, noisePNG(t, 1), noisePNG(t, 2))
	if r = run(ste, step()); r.Passed || r.Blocked || !strings.Contains(r.Actual, "password") {
		t.Fatalf("missing recognize token not a FAIL naming it: %+v", r)
	}

	// Golden-bad: forbidden token fails even when VERIFIED: yes.
	fv = &fakeVision{verify: yes, blind: "TEXT:\nSign in\nPassword\nError 500\nDESCRIPTION: A form with a red banner."}
	ste, _ = newSTE(fv, noisePNG(t, 1), noisePNG(t, 2))
	if r = run(ste, step()); r.Passed || !strings.Contains(r.Actual, "error") {
		t.Fatalf("forbidden token not a FAIL: %+v", r)
	}

	// Carrier decoy: a token is matched as a word, not a substring
	// ("Errors" / "terror" must not trip forbid "Error"... but whole-word
	// "error" must).
	fv = &fakeVision{verify: yes, blind: "TEXT:\nSign in\nPassword\nTerrorist-free zone\nErrorsList\nDESCRIPTION: A list widget."}
	ste, _ = newSTE(fv, noisePNG(t, 1), noisePNG(t, 2))
	if r = run(ste, step()); !r.Passed {
		t.Fatalf("substring decoy tripped the forbid check: %+v", r)
	}

	// Blind call failing => BLOCKED, never a pass on the VERIFIED reply alone.
	fv = &fakeVision{verify: yes, blindErr: errors.New("timeout")}
	ste, _ = newSTE(fv, noisePNG(t, 1), noisePNG(t, 2))
	requireBlocked(t, run(ste, step()), "blind description error")
}

func TestNoBluffVision_NormalizeAndMatch(t *testing.T) {
	if got := normalizeVisionText("  Sign-In!\n\tPASSWORD: ok  "); got != "sign in password ok" {
		t.Fatalf("normalizeVisionText = %q", got)
	}
	missing, hit := matchVisionTokens("the sign in page, password field", []string{"Sign In", "password", "user name"}, []string{"error"})
	if len(missing) != 1 || missing[0] != "user name" || len(hit) != 0 {
		t.Fatalf("missing=%v hit=%v", missing, hit)
	}
	_, hit = matchVisionTokens("signal strength; errors none", nil, []string{"sign", "error"})
	if len(hit) != 0 {
		t.Fatalf("substring matched as a word: %v", hit)
	}
}

// ── invariant 5: golden pHash ────────────────────────────────────────

func TestNoBluffVision_GoldenPHash(t *testing.T) {
	t.Setenv(EnvGoldensDir, "") // the host env must not supply a goldens dir
	dir := t.TempDir()
	after := noisePNG(t, 2)
	if err := os.WriteFile(filepath.Join(dir, "home.png"), after, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "other.png"), noisePNG(t, 99), 0o600); err != nil {
		t.Fatal(err)
	}
	zero := 0
	goldenStep := func(id string) testbank.TestStep {
		return testbank.TestStep{Name: "home", Action: "keypress: HOME", Expected: "home", Golden: id, PHashMaxDistance: &zero}
	}

	// Golden-only steps are a deterministic oracle: no model needed.
	ste, _ := newSTE(nilProvider, noisePNG(t, 1), after)
	ste.config.VisionGoldensDir = dir
	r := run(ste, goldenStep("home"))
	if !r.Passed || r.GoldenDistance != 0 || r.VisionStatus != VisionVerified {
		t.Fatalf("identical golden did not pass at distance 0: %+v", r)
	}

	ste, _ = newSTE(nilProvider, noisePNG(t, 1), after)
	ste.config.VisionGoldensDir = dir
	r = run(ste, goldenStep("other"))
	if r.Passed || r.Blocked || r.GoldenDistance <= 0 {
		t.Fatalf("different golden must FAIL with its distance: %+v", r)
	}

	ste, _ = newSTE(nilProvider, noisePNG(t, 1), after)
	ste.config.VisionGoldensDir = dir
	requireBlocked(t, run(ste, goldenStep("absent")), "missing golden")

	ste, _ = newSTE(nilProvider, noisePNG(t, 1), after)
	requireBlocked(t, run(ste, goldenStep("home")), "no goldens dir configured")
}

// ── non-screen actions do not consult the model ──────────────────────

func TestNoBluffVision_NonScreenActionsDoNotConsultVision(t *testing.T) {
	fv := &fakeVision{verify: "VERIFIED: no\nACTUAL: unrelated screen"}
	ste, _ := newSTE(fv, noisePNG(t, 1), noisePNG(t, 2))
	r := run(ste, testbank.TestStep{Name: "cli", Action: "shell: exit 0", Expected: "exit 0"})
	if !r.Passed || fv.calls() != 0 || r.VisionStatus != VisionNotRequested {
		t.Fatalf("shell step judged by a screenshot: passed=%v calls=%d status=%q", r.Passed, fv.calls(), r.VisionStatus)
	}
	// ...unless the bank author explicitly gates it.
	fv = &fakeVision{verify: "VERIFIED: no\nACTUAL: unrelated screen"}
	ste, _ = newSTE(fv, noisePNG(t, 1), noisePNG(t, 2))
	r = run(ste, testbank.TestStep{Name: "cli", Action: "shell: exit 0", Expected: "window opened", VisionVerify: true})
	if r.Passed || fv.calls() == 0 {
		t.Fatalf("gated shell step did not consult vision: %+v", r)
	}
}

// ── case level: BLOCKED is its own verdict and exit code ─────────────

func TestNoBluffVision_CaseLevelBlockedAndExitCode(t *testing.T) {
	ste, _ := newSTE(nilProvider, noisePNG(t, 1), noisePNG(t, 2))
	res := &StructuredExecutionResult{}
	tc := testbank.TestCase{ID: "TC-B", Name: "blocked case", Steps: []testbank.TestStep{gated(nil), {Name: "next", Action: "keypress: X", Expected: "x"}}}
	if err := ste.executeTestCase(context.Background(), tc, res); err != nil {
		t.Fatal(err)
	}
	if res.TestCasesBlocked != 1 || res.TestCasesPassed != 0 || res.TestCasesFailed != 0 {
		t.Fatalf("blocked case counted wrong: %+v", res)
	}
	if len(res.Cases) != 1 || res.Cases[0].Verdict != CaseVerdictBlocked || len(res.Cases[0].Steps) != 1 {
		t.Fatalf("case record: %+v", res.Cases)
	}
	if res.Cases[0].Steps[0].FrameSHA256 == "" {
		t.Fatal("blocked step lost its frame evidence")
	}
	if got := res.ExitCode(); got != 2 {
		t.Fatalf("ExitCode = %d, want 2 (could not determine)", got)
	}
	if (&StructuredExecutionResult{TestCasesBlocked: 1, TestCasesFailed: 1}).ExitCode() != 1 {
		t.Fatal("a real failure must outrank could-not-determine")
	}
	// Survivor of mutation M16: passes elsewhere must not hide a block.
	if (&StructuredExecutionResult{TestCasesPassed: 5, TestCasesBlocked: 1}).ExitCode() != 2 {
		t.Fatal("one blocked case among passes must still be 2, never 0")
	}
	if (&StructuredExecutionResult{TestCasesPassed: 3}).ExitCode() != 0 {
		t.Fatal("all-pass must be 0")
	}
	if (&StructuredExecutionResult{}).ExitCode() != 2 {
		t.Fatal("nothing executed is not a pass")
	}
}

// ── real OpenAI-compatible endpoint (llama-server shape) ─────────────

func TestNoBluffVision_EndpointProviderEndToEnd(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"VERIFIED: yes\nACTUAL: settings"}}]}`)
	}))
	cfg, ok := llm.VisionEndpointConfigFromEnv(func(k string) string {
		if k == llm.EnvVisionEndpoint {
			return srv.URL + "/v1"
		}
		return ""
	})
	if !ok {
		t.Fatal("no endpoint config")
	}
	ste, _ := newSTE(llm.NewOpenAIProvider(cfg), noisePNG(t, 1), noisePNG(t, 2))
	if r := run(ste, gated(nil)); !r.Passed || r.VisionStatus != VisionVerified {
		t.Fatalf("live-shaped endpoint yes did not pass: %+v", r)
	}

	// Negative: the server goes down => BLOCKED, not PASS.
	srv.Close()
	ste, _ = newSTE(llm.NewOpenAIProvider(cfg), noisePNG(t, 1), noisePNG(t, 2))
	requireBlocked(t, run(ste, gated(nil)), "endpoint down")
}
