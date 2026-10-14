// SPDX-FileCopyrightText: 2026 Milos Vasic
// SPDX-License-Identifier: Apache-2.0

// no_bluff_vision_review_test.go extends the no_bluff_vision gate with the
// findings of the independent review of 8627c0d/4ccbd03. Each test is a
// golden-good / golden-bad pair for one finding and is paired with a
// mutant in challenges/scripts/no_bluff_vision_mutation_challenge.sh.

package autonomous

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"math/rand"
	"strings"
	"testing"

	"digital.vasic.helixqa/pkg/llm"
	"digital.vasic.helixqa/pkg/navigator"
	"digital.vasic.helixqa/pkg/testbank"
)

func noisePNGSized(t *testing.T, seed int64, w, h int) []byte {
	t.Helper()
	r := rand.New(rand.NewSource(seed))
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{uint8(r.Intn(256)), uint8(r.Intn(256)), uint8(r.Intn(256)), 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// Finding 5 (MEDIUM): expect_change alone proves only that SOMETHING
// changed (a crash dialog counts) — it is not a positive content check, so
// the step is labelled UNVERIFIED-by-vision, never VISION-VERIFIED.
func TestNoBluffVision_ExpectChangeAloneIsNotVerified(t *testing.T) {
	ste, _ := newSTE(nilProvider, noisePNG(t, 1), noisePNG(t, 2))
	r := run(ste, testbank.TestStep{Name: "s", Action: "keypress: X", Expected: "x", ExpectChange: true})
	if !r.Passed || r.VisionStatus != VisionUnverified || !strings.Contains(r.Actual, "UNVERIFIED-by-vision") {
		t.Fatalf("expect_change-only step: passed=%v status=%q actual=%q", r.Passed, r.VisionStatus, r.Actual)
	}
	if strings.Contains(r.Actual, "VISION-VERIFIED") {
		t.Fatalf("expect_change-only step claims verification: %q", r.Actual)
	}
}

// Finding 5 (MEDIUM): frames of different dimensions are not comparable —
// "changed" cannot be decided, so the step is BLOCKED (never a claimed change).
func TestNoBluffVision_ExpectChangeIncomparableFramesBlocked(t *testing.T) {
	ste, _ := newSTE(&fakeVision{verify: "VERIFIED: yes"}, noisePNGSized(t, 1, 160, 120), noisePNGSized(t, 2, 200, 100))
	requireBlocked(t, run(ste, gated(func(s *testbank.TestStep) { s.ExpectChange = true })), "size mismatch")
}

// Finding 1 (CRITICAL): the answer must be exactly one standalone
// "VERIFIED: yes|no" line. An echoed prompt template ("VERIFIED: yes/no"),
// "UNVERIFIED: yes", prose that merely contains the phrase, or two answer
// lines are not an answer.
func TestNoBluffVision_VerifiedAnswerMustBeOneStandaloneLine(t *testing.T) {
	notAnAnswer := []string{
		"Respond with ONLY:\nVERIFIED: yes/no\nACTUAL: brief description of what's visible",
		"VERIFIED: yes/no",
		"UNVERIFIED: yes\nACTUAL: settings",
		"The expected state is VERIFIED: yes because the menu shows.",
		"VERIFIED: yes\nVERIFIED: yes\nACTUAL: duplicated",
		"VERIFIED: yes, mostly\nACTUAL: hedged",
	}
	for _, reply := range notAnAnswer {
		ste, _ := newSTE(&fakeVision{verify: reply}, noisePNG(t, 1), noisePNG(t, 2))
		requireBlocked(t, run(ste, gated(nil)), "reply "+reply)
	}
	for _, reply := range []string{
		"VERIFIED: yes\nACTUAL: settings",
		"  verified:  YES  \nACTUAL: settings",
		"ACTUAL: settings\nVERIFIED: yes.",
	} {
		ste, _ := newSTE(&fakeVision{verify: reply}, noisePNG(t, 1), noisePNG(t, 2))
		if r := run(ste, gated(nil)); !r.Passed || r.VisionStatus != VisionVerified {
			t.Fatalf("standalone yes line %q not accepted: %+v", reply, r)
		}
	}
	ste, _ := newSTE(&fakeVision{verify: "ACTUAL: home\nVERIFIED: no"}, noisePNG(t, 1), noisePNG(t, 2))
	if r := run(ste, gated(nil)); r.Passed || r.Blocked || r.VisionStatus != VisionRefuted {
		t.Fatalf("standalone no line not a FAIL: %+v", r)
	}
}

// Finding 6 (MEDIUM): an ambiguous or failing provider must not freeze
// every gated step at BLOCKED while a capable provider is configured — the
// next provider is asked; the operator's HELIX_VISION_ENDPOINT is asked
// first; and a DEFINITE answer is final (no shopping for a yes).
func TestNoBluffVision_ProviderFallThroughAndOrdering(t *testing.T) {
	// Ambiguous first provider -> the next one answers.
	a := &fakeVision{name: "astica", verify: "The screen shows a settings list with toggles."}
	b := &fakeVision{name: "qwen", verify: "VERIFIED: yes\nACTUAL: settings"}
	ste, _ := newSTE(llm.NewAdaptiveProvider(a, b), noisePNG(t, 1), noisePNG(t, 2))
	r := run(ste, gated(nil))
	if !r.Passed || r.VisionProvider != "qwen" {
		t.Fatalf("ambiguous first provider froze the step: passed=%v provider=%q actual=%q", r.Passed, r.VisionProvider, r.Actual)
	}

	// The operator endpoint is asked before registry providers.
	a = &fakeVision{name: "astica", verify: "VERIFIED: no"}
	ep := &fakeVision{name: llm.ProviderVisionEndpoint, verify: "VERIFIED: yes"}
	ste, _ = newSTE(llm.NewAdaptiveProvider(a, ep), noisePNG(t, 1), noisePNG(t, 2))
	r = run(ste, gated(nil))
	if !r.Passed || a.calls() != 0 || r.VisionProvider != llm.ProviderVisionEndpoint {
		t.Fatalf("endpoint not asked first: passed=%v astica-calls=%d provider=%q", r.Passed, a.calls(), r.VisionProvider)
	}

	// A definite "no" is final: the next provider is never asked.
	a = &fakeVision{name: "a", verify: "VERIFIED: no\nACTUAL: home"}
	b = &fakeVision{name: "b", verify: "VERIFIED: yes"}
	ste, _ = newSTE(llm.NewAdaptiveProvider(a, b), noisePNG(t, 1), noisePNG(t, 2))
	r = run(ste, gated(nil))
	if r.Passed || b.calls() != 0 || r.VisionStatus != VisionRefuted {
		t.Fatalf("definite no was shopped around: passed=%v b-calls=%d status=%q", r.Passed, b.calls(), r.VisionStatus)
	}

	// Blind transcription: a reply that is not the JSON object falls through too.
	a = &fakeVision{name: "a", blind: "A settings list."}
	b = &fakeVision{name: "b", blind: `{"texts":["Settings"],"description":"list"}`}
	ste, _ = newSTE(llm.NewAdaptiveProvider(a, b), noisePNG(t, 1), noisePNG(t, 2))
	r = run(ste, testbank.TestStep{Name: "s", Action: "keypress: X", Expected: "x", Recognize: []string{"Settings"}})
	if !r.Passed {
		t.Fatalf("non-JSON blind reply did not fall through: %+v", r)
	}

	// Every provider unusable -> BLOCKED, naming each.
	a = &fakeVision{name: "a", verify: "maybe"}
	b = &fakeVision{name: "b", verifyErr: errTest("503")}
	ste, _ = newSTE(llm.NewAdaptiveProvider(a, b), noisePNG(t, 1), noisePNG(t, 2))
	r = run(ste, gated(nil))
	requireBlocked(t, r, "all providers unusable")
	if !strings.Contains(r.Actual, "a:") || !strings.Contains(r.Actual, "b:") {
		t.Fatalf("blocked reason does not name every provider tried: %q", r.Actual)
	}
}

type errTest string

func (e errTest) Error() string { return string(e) }

// Review round 2, finding 2 (HIGH): a vision-gated step that the executor
// SKIPS (prose description action, playwright without a CDP URL) asserted
// nothing — its case must be BLOCKED, never PASSED.
func TestNoBluffVision_SkippedGatedStepBlocksCase(t *testing.T) {
	t.Setenv("HELIXQA_PLAYWRIGHT_CDP_URL", "")
	gatedSkips := map[string]testbank.TestStep{
		"description + vision_verify":     {Name: "look", Action: "Check that the settings screen is shown", Expected: "settings", VisionVerify: true},
		"playwright (no CDP) + recognize": {Name: "nav", Action: "playwright: navigate http://127.0.0.1:1/", Expected: "home", Recognize: []string{"Home"}},
		// golden-only: gated, but needs no vision MODEL (pins N13)
		"playwright (no CDP) + golden only": {Name: "nav", Action: "playwright: navigate http://127.0.0.1:1/", Expected: "home", Golden: "home"},
	}
	for name, st := range gatedSkips {
		ste, _ := newSTE(&fakeVision{verify: "VERIFIED: yes", blind: `{"texts":["Home"],"description":"x"}`}, noisePNG(t, 1), noisePNG(t, 2))
		res := &StructuredExecutionResult{}
		tc := testbank.TestCase{ID: "TC-SKG", Name: "skipped gated", Steps: []testbank.TestStep{{Name: "ok", Action: "keypress: X", Expected: "x"}, st}}
		if err := ste.executeTestCase(context.Background(), tc, res); err != nil {
			t.Fatal(err)
		}
		if res.TestCasesPassed != 0 || res.TestCasesBlocked != 1 || res.ExitCode() != 2 {
			t.Errorf("%s: passed=%d blocked=%d skipped=%d exit=%d — a skipped gated step must BLOCK the case",
				name, res.TestCasesPassed, res.TestCasesBlocked, res.TestCasesSkipped, res.ExitCode())
		}
		// Step level (round 7: a skipped step now also blocks the CASE, so
		// the step itself must say BLOCKED, not merely be skipped).
		if len(res.Cases) != 1 || len(res.Cases[0].Steps) != 2 {
			t.Fatalf("%s: case record %+v", name, res.Cases)
		}
		if gs := res.Cases[0].Steps[1]; gs.Skipped || !gs.Blocked || gs.VisionStatus != VisionBlocked {
			t.Errorf("%s: gated step skipped=%v blocked=%v status=%q — a skipped gated step is BLOCKED", name, gs.Skipped, gs.Blocked, gs.VisionStatus)
		}
	}
	// Golden-good: an UNgated prose step is still an honest SKIP.
	ste, _ := newSTE(nilProvider, noisePNG(t, 1), noisePNG(t, 2))
	res := &StructuredExecutionResult{}
	tc := testbank.TestCase{ID: "TC-SKP", Name: "plain skip", Steps: []testbank.TestStep{{Name: "p", Action: "Just prose"}}}
	_ = ste.executeTestCase(context.Background(), tc, res)
	if res.TestCasesSkipped != 1 || res.TestCasesBlocked != 0 {
		t.Fatalf("ungated prose step: %+v", res)
	}
}

// Review round 2, finding 7 (LOW): asking adaptive members one by one must
// keep the wrapper's accounting (cost, unavailable-marking) and must stop
// asking providers once the context is cancelled.
func TestNoBluffVision_ProviderLoopKeepsAccountingAndHonoursCtx(t *testing.T) {
	// Cost is recorded for the provider that answered.
	a := &fakeVision{name: "a", verify: "The screen shows a list."}
	b := &fakeVision{name: "b", verify: "VERIFIED: yes"}
	ap := llm.NewAdaptiveProvider(a, b)
	ct := llm.NewCostTracker()
	ap.SetCostTracker(ct)
	ste, _ := newSTE(ap, noisePNG(t, 1), noisePNG(t, 2))
	if r := run(ste, gated(nil)); !r.Passed {
		t.Fatalf("setup: %+v", r)
	}
	if got := ct.Summary().TotalCalls; got < 1 {
		t.Fatalf("no vision cost recorded through the executor's provider loop (TotalCalls=%d)", got)
	}

	// An auth failure marks the provider unavailable in the wrapper, so the
	// next step does not ask it again.
	a = &fakeVision{name: "a", verifyErr: errTest("openai: API error 401: invalid key")}
	b = &fakeVision{name: "b", verify: "VERIFIED: yes"}
	ap = llm.NewAdaptiveProvider(a, b)
	ste, _ = newSTE(ap, noisePNG(t, 1), noisePNG(t, 2), noisePNG(t, 3), noisePNG(t, 4))
	_ = run(ste, gated(nil))
	_ = run(ste, gated(nil))
	if a.calls() != 1 {
		t.Fatalf("provider with an auth failure was asked %d times, want 1 (wrapper unavailable-marking bypassed)", a.calls())
	}
	if _, marked := ap.GetUnavailableProviders()["a"]; !marked {
		t.Fatal("auth failure not recorded on the wrapper")
	}

	// Cancellation between providers: the first provider's call cancels the
	// context; the second provider must not be asked; the step is BLOCKED.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a = &fakeVision{name: "a", verify: "unclear", onCall: cancel}
	b = &fakeVision{name: "b", verify: "VERIFIED: yes"}
	ste, _ = newSTE(llm.NewAdaptiveProvider(a, b), noisePNG(t, 1), noisePNG(t, 2))
	r := ste.executeStep(ctx, testbank.TestCase{ID: "TC-CTX", Name: "ctx"}, gated(nil), 1)
	if b.calls() != 0 {
		t.Fatalf("provider asked after the context was cancelled (%d calls)", b.calls())
	}
	requireBlocked(t, r, "cancelled context")
}

// listProvider exposes members via Providers() but is NOT a VisionRouter,
// so the executor's own between-provider ctx check is the only guard.
type listProvider struct{ ps []llm.Provider }

func (l listProvider) Chat(context.Context, []llm.Message) (*llm.Response, error) {
	return nil, errTest("no chat")
}
func (l listProvider) Vision(context.Context, []byte, string) (*llm.Response, error) {
	return nil, errTest("listProvider.Vision must not be called directly")
}
func (l listProvider) Name() string              { return "list" }
func (l listProvider) SupportsVision() bool      { return true }
func (l listProvider) Providers() []llm.Provider { return l.ps }

func TestNoBluffVision_ProviderLoopStopsOnCancelledCtxWithoutRouter(t *testing.T) {
	for _, mode := range []string{"verify", "blind"} {
		ctx, cancel := context.WithCancel(context.Background())
		a := &fakeVision{name: "a", verify: "unclear", blind: "no headings", onCall: cancel}
		b := &fakeVision{name: "b", verify: "VERIFIED: yes", blind: `{"texts":["Login"],"description":"x"}`}
		ste, _ := newSTE(listProvider{ps: []llm.Provider{a, b}}, noisePNG(t, 1), noisePNG(t, 2))
		st := gated(nil)
		if mode == "blind" {
			st = testbank.TestStep{Name: "s", Action: "keypress: X", Expected: "x", Recognize: []string{"Login"}}
		}
		r := ste.executeStep(ctx, testbank.TestCase{ID: "TC-CTX2", Name: "ctx"}, st, 1)
		cancel()
		if b.calls() != 0 {
			t.Fatalf("%s: provider asked after the context was cancelled", mode)
		}
		requireBlocked(t, r, mode+" cancelled context")
	}
}

// Review round 2, finding 9 (MEDIUM, pre-existing): frame_diff reported
// "motion detected" when the frames could not be decoded or had different
// sizes. Non-comparable frames now FAIL the step with the reason; real
// motion still passes and identical frames still fail.
func TestFrameDiff_NonComparableFramesAreNotMotion(t *testing.T) {
	step := testbank.TestStep{Action: "frame_diff: 10"}
	cases := []struct {
		name             string
		a, b             []byte
		success, blocked bool
		mustSay          string
	}{
		{"undecodable", []byte("not an image at all"), []byte("still not an image"), false, true, "not comparable"},
		{"size mismatch", noisePNGSized(t, 1, 160, 120), noisePNGSized(t, 2, 200, 100), false, true, "not comparable"},
		{"identical", noisePNG(t, 5), noisePNG(t, 5), false, false, "identical"},
		{"real motion", noisePNG(t, 5), noisePNG(t, 6), true, false, "motion"},
	}
	for _, c := range cases {
		ex := &screenExec{frames: [][]byte{c.a, c.b}}
		res := (&StructuredTestExecutor{}).performAction(context.Background(), ex, step)
		if res.Success != c.success || res.Blocked != c.blocked || !strings.Contains(res.Message, c.mustSay) {
			t.Errorf("%s: success=%v blocked=%v msg=%q (want %v/%v, %q)", c.name, res.Success, res.Blocked, res.Message, c.success, c.blocked, c.mustSay)
		}
	}
	// Step and case level: a capture problem is BLOCKED (environment), never
	// a FAILED app-defect finding (review round 3, finding 6).
	ste, _ := newSTE(nilProvider, noisePNG(t, 1), []byte("junk"), []byte("junk2"), noisePNG(t, 2))
	res := &StructuredExecutionResult{}
	tc := testbank.TestCase{ID: "TC-FD", Name: "fd", Steps: []testbank.TestStep{{Name: "motion", Action: "frame_diff: 10"}}}
	_ = ste.executeTestCase(context.Background(), tc, res)
	if res.TestCasesBlocked != 1 || res.TestCasesFailed != 0 {
		t.Fatalf("non-comparable frame_diff case: %+v", res)
	}
	if len(res.Findings) != 1 || res.Findings[0].Category.IsAppDefect() {
		t.Fatalf("capture problem filed as an app defect: %+v", res.Findings)
	}
}

type failingFactory struct{}

func (failingFactory) Create(string) (navigator.ActionExecutor, error) {
	return nil, errTest("no device attached")
}

// Review round 3, finding 6: "executor cannot be created" is an
// environment problem — the step is BLOCKED, never a FAILED app defect.
func TestNoBluffVision_ExecutorCreationFailureBlocks(t *testing.T) {
	for _, st := range []testbank.TestStep{gated(nil), {Name: "plain", Action: "keypress: X", Expected: "x"}} {
		ste := &StructuredTestExecutor{config: PipelineConfig{Platforms: []string{"android"}}, execFactory: failingFactory{}}
		res := &StructuredExecutionResult{}
		tc := testbank.TestCase{ID: "TC-EX", Name: "ex", Steps: []testbank.TestStep{st}}
		_ = ste.executeTestCase(context.Background(), tc, res)
		if res.TestCasesBlocked != 1 || res.TestCasesFailed != 0 {
			t.Fatalf("%s: executor failure counted as %+v", st.Name, res)
		}
		if len(res.Findings) != 1 || res.Findings[0].Category.IsAppDefect() {
			t.Fatalf("%s: executor failure filed as an app defect: %+v", st.Name, res.Findings)
		}
	}
}
