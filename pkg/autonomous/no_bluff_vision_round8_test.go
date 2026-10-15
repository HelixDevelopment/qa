// SPDX-FileCopyrightText: 2026 Milos Vasic
// SPDX-License-Identifier: Apache-2.0

package autonomous

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"digital.vasic.helixqa/pkg/navigator"
	"digital.vasic.helixqa/pkg/testbank"
)

// Review round 7, finding 2 (HIGH): the ACTUAL scan matches only "could not
// see / view / read the image" wording — an ordinary description ("The
// screenshot shows …") is not a refusal, and a definite NO is never scanned.
func TestNoBluffVision_R8_ActualScanOnlyInabilityAndNeverOnNo(t *testing.T) {
	pass := []string{
		"VERIFIED: yes\nACTUAL: The screenshot shows the settings screen",
		"VERIFIED: yes\nACTUAL: The image shows a list of movies",
		"VERIFIED: yes\nACTUAL: This screenshot is the home page",
		"VERIFIED: yes\nACTUAL: The picture contains a login form",
		"VERIFIED: yes\nACTUAL: settings screen; I don't see any error dialogs",
		"VERIFIED: yes\nACTUAL: profile page with an Email verified: yes badge",
	}
	for _, reply := range pass {
		ste, _ := newSTE(&fakeVision{verify: reply}, noisePNG(t, 1), noisePNG(t, 2))
		if r := run(ste, gated(nil)); !r.Passed {
			t.Errorf("%q: %q — an ordinary description must not block a yes", reply, r.Actual)
		}
	}
	refuted := []string{
		"VERIFIED: no\nACTUAL: The screenshot shows a login form",
		"VERIFIED: no\nACTUAL: I cannot see the settings screen, an error dialog covers it",
	}
	for _, reply := range refuted {
		ste, _ := newSTE(&fakeVision{verify: reply}, noisePNG(t, 1), noisePNG(t, 2))
		if r := run(ste, gated(nil)); r.Passed || r.Blocked {
			t.Errorf("%q: passed=%v blocked=%v — a definite NO is REFUTED, never scanned", reply, r.Passed, r.Blocked)
		}
	}
	for _, reply := range []string{
		"VERIFIED: yes\nACTUAL: I cannot see the image",
		"VERIFIED: yes\nACTUAL: (unable to view images; assuming yes)",
		"VERIFIED: yes\nACTUAL: I could not read the screenshot",
		"VERIFIED: yes\nACTUAL: VERIFIED: no",
	} {
		ste, _ := newSTE(&fakeVision{verify: reply}, noisePNG(t, 1), noisePNG(t, 2))
		requireBlocked(t, run(ste, gated(nil)), reply)
	}
	// Simulation: a model that always says yes, with ACTUAL "<expected>" or
	// "The screenshot shows <expected>", must be judged the same way.
	for _, exp := range []string{"settings screen", "home page with recent media", "login form", "video player is playing"} {
		var verdicts []bool
		for _, actual := range []string{exp, "The screenshot shows " + exp, "The image shows " + exp} {
			ste, _ := newSTE(&fakeVision{verify: "VERIFIED: yes\nACTUAL: " + actual}, noisePNG(t, 1), noisePNG(t, 2))
			st := gated(nil)
			st.Expected = exp
			verdicts = append(verdicts, run(ste, st).Passed)
		}
		if verdicts[0] != verdicts[1] || verdicts[1] != verdicts[2] {
			t.Errorf("%q: verdicts differ by ACTUAL phrasing: %v", exp, verdicts)
		}
	}
}

// Review round 7, finding 5: a model's "yes" to an EMPTY expectation is not
// an observation — the model is not even asked.
func TestNoBluffVision_R8_EmptyExpectationNotVerified(t *testing.T) {
	fv := &fakeVision{verify: "VERIFIED: yes\nACTUAL: something"}
	ste, _ := newSTE(fv, noisePNG(t, 1), noisePNG(t, 2))
	requireCaseVerdict(t, runCase(t, ste, testbank.TestStep{Name: "k", Action: "keypress: X"},
		testbank.TestStep{Name: "w", Action: "sleep: 1"}), CaseVerdictBlocked, "keypress + sleep, no expectation")
	if fv.calls() != 0 {
		t.Fatalf("the model was asked %d time(s) to verify an empty expectation", fv.calls())
	}
	ste, _ = newSTE(&fakeVision{verify: "VERIFIED: yes"}, noisePNG(t, 1), noisePNG(t, 2))
	st := gated(nil)
	st.Expected = "  "
	requireBlocked(t, run(ste, st), "vision_verify with a blank expected")
}

// Review round 7, finding 6: an http: step with no expect_* accepts only a
// 2xx status (a 500 is a failure, not an observation that passes).
func TestNoBluffVision_R8_HTTPDefaultsTo2xx(t *testing.T) {
	code := 500
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(code) }))
	defer srv.Close()
	ste, _ := newSTE(nilProvider, noisePNG(t, 1), noisePNG(t, 2))
	ste.config.HTTPBaseURL = srv.URL
	requireCaseVerdict(t, runCase(t, ste, testbank.TestStep{Name: "api", Action: "http: GET /x"}), CaseVerdictFailed, "500 with no expect_*")
	code = 204
	requireCaseVerdict(t, runCase(t, ste, testbank.TestStep{Name: "api", Action: "http: GET /x"}), CaseVerdictPassed, "204 with no expect_*")
	code = 404
	requireCaseVerdict(t, runCase(t, ste, testbank.TestStep{Name: "api", Action: "http: GET /x", ExpectStatus: 404}), CaseVerdictPassed, "declared expect_status 404")
}

// Review round 7, finding 7: in a reading where EVERY element is the model
// talking, a forbidden word inside a refusal sentence is not on the screen;
// only an element EQUAL to the token counts as present there.
func TestNoBluffVision_R8_RefusalMentioningForbiddenWord(t *testing.T) {
	j := func(texts string) string { return `{"texts":` + texts + `,"description":"x"}` }
	st := recStep([]string{"Login"}, []string{"Error"})
	requireBlocked(t, blindRun(t, j(`["I cannot read the Error text in this image"]`), st), "refusal mentioning Error")
	requireBlocked(t, blindRun(t, j(`["I'm sorry, an Error occurred"]`), st), "apology mentioning Error")
	// Real text: presence still decides.
	if r := blindRun(t, j(`["Login","Error 500"]`), st); r.Passed || r.Blocked {
		t.Fatalf("forbidden token in real text: %q", r.Actual)
	}
}

// Review round 7, finding 8: scripts written without spaces between words
// (Han, kana, Thai, …) match by substring — a forbidden token inside a
// longer string is present, and a recognize token inside a longer element
// is contained (BLOCKED), not absent.
func TestNoBluffVision_R8_NoSpaceScripts(t *testing.T) {
	j := func(texts string) string { return `{"texts":` + texts + `,"description":"x"}` }
	for name, c := range map[string]struct{ texts, forb string }{
		"Chinese":  {`["设置","发生错误了"]`, "错误"},
		"Japanese": {`["設定","通信エラーが発生しました"]`, "エラー"},
		"Thai":     {`["ตั้งค่า","เกิดข้อผิดพลาด"]`, "ผิดพลาด"},
	} {
		rec := map[string]string{"Chinese": "设置", "Japanese": "設定", "Thai": "ตั้งค่า"}[name]
		if r := blindRun(t, j(c.texts), recStep([]string{rec}, []string{c.forb})); r.Passed || r.Blocked {
			t.Errorf("%s: forbidden %q inside a longer string not seen: %q", name, c.forb, r.Actual)
		}
	}
	r := blindRun(t, j(`["系统设置页面"]`), recStep([]string{"设置"}, nil))
	requireBlocked(t, r, "Chinese recognize contained in a longer element")
	// Latin is unchanged: whole words only.
	if missing, f := matchTokensInTexts([]string{"Errors"}, nil, []string{"Error"}, false); len(f) != 0 || len(missing) != 0 {
		t.Fatalf("Latin substring matched: %v", f)
	}
}

// Review round 7, reviewer mutant RV7-01: only playwright assertvisible /
// waitfor observe; other verbs are input.
func TestNoBluffVision_R8_PlaywrightObservingVerbs(t *testing.T) {
	for action, want := range map[string]bool{
		"playwright: assertvisible #ok":     true,
		"playwright: waitfor #ok":           true,
		"playwright: click #btn":            false,
		"playwright: navigate http://x/":    false,
		"playwright: fill #user admin":      false,
		"playwright: assertnotvisible #err": false,
	} {
		if got := stepObserved(testbank.TestStep{Action: action}, TestStepResult{Passed: true}); got != want {
			t.Errorf("%s: observed=%v, want %v", action, got, want)
		}
	}
}

// flipProber: reachable on the first probe, unreachable afterwards.
type flipProber struct {
	mu sync.Mutex
	n  int
}

func (f *flipProber) Create(string) (navigator.ActionExecutor, error) { return &noopExecutor{}, nil }
func (f *flipProber) Probe(context.Context, string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.n++
	if f.n > 1 {
		return errors.New("device gone")
	}
	return nil
}

// Review round 7, reviewer mutant RV7-02: a HOST step's own failure (shell
// exit 1) is decided by the host command — never re-probed into BLOCKED.
func TestNoBluffVision_R8_HostStepFailureNotReprobed(t *testing.T) {
	ste := &StructuredTestExecutor{config: PipelineConfig{Platforms: []string{"android"}}, execFactory: &flipProber{}}
	requireCaseVerdict(t, runCase(t, ste, testbank.TestStep{Name: "cli", Action: "shell: exit 1"}), CaseVerdictFailed, "shell exit 1 with a device that then drops")
}

// LOW: sleep: honours cancellation; `_skip: true` steps are not run (same as
// `helixqa run`); the probe-failure reason no longer claims "probed once".
func TestNoBluffVision_R8_SleepSkipAndReason(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(200*time.Millisecond, cancel)
	ste, _ := newSTE(nilProvider, noisePNG(t, 1), noisePNG(t, 2))
	start := time.Now()
	res := runCaseCtx(t, ctx, ste, testbank.TestStep{Name: "w", Action: "sleep: 5000"}, testbank.TestStep{Name: "cli", Action: "shell: exit 0"})
	if d := time.Since(start); d > 1500*time.Millisecond {
		t.Fatalf("sleep ignored the cancellation (%v)", d)
	}
	requireCaseVerdict(t, res, CaseVerdictBlocked, "sleep cut by cancellation")

	ste, _ = newSTE(nilProvider, noisePNG(t, 1), noisePNG(t, 2))
	requireCaseVerdict(t, runCase(t, ste, testbank.TestStep{Name: "cli", Action: "shell: exit 0"},
		testbank.TestStep{Name: "off", Action: "shell: exit 7", Skip: true}), CaseVerdictPassed, "_skip step not run")
	ste, _ = newSTE(nilProvider, noisePNG(t, 1), noisePNG(t, 2))
	requireCaseVerdict(t, runCase(t, ste, testbank.TestStep{Name: "cli", Action: "shell: exit 0"},
		testbank.TestStep{Name: "off", Action: "Prose step disabled", Skip: true}), CaseVerdictPassed, "_skip prose step does not block the case")

	pf := &probeFactory{err: errors.New("offline")}
	ste = &StructuredTestExecutor{config: PipelineConfig{Platforms: []string{"android"}}, execFactory: pf}
	res = runCase(t, ste, testbank.TestStep{Name: "ui", Action: "keypress: X"})
	if a := lastActual(res); strings.Contains(a, "probed once") || !strings.Contains(a, "unreachable") {
		t.Fatalf("probe-failure reason: %q", a)
	}
}

// LOW: get-state output with a warning AFTER the state line is still read.
func TestNoBluffVision_R8_ProbeStateLine(t *testing.T) {
	for out, wantOK := range map[string]bool{
		"device\nadb: warning: something odd\n":   true,
		"* daemon started successfully\ndevice\n": true,
		"offline\nadb: warning: something odd\n":  false,
		"unauthorized\n":                          false,
		"adb: warning: no state line at all\n":    false,
		"device\noffline\n":                       false,
	} {
		o := out
		f := NewDefaultExecutorFactory(ExecutorConfig{AndroidDevice: "e", CommandRunner: runnerFunc(func(string, ...string) ([]byte, error) { return []byte(o), nil })})
		if err := f.Probe(context.Background(), "android"); (err == nil) != wantOK {
			t.Errorf("get-state %q: err=%v, want reachable=%v", out, err, wantOK)
		}
	}
}

// Review round 7, finding 4: the structured phase runs cases in dependency
// order (ID as tie-break) — the same order `helixqa run` uses — and BLOCKS
// cases in a dependency cycle.
func TestNoBluffVision_R8_StructuredDependencyOrder(t *testing.T) {
	dir := t.TempDir()
	bank := "version: \"1.0\"\nname: deps\ntest_cases:\n" +
		"  - id: a-check\n    name: a\n    dependencies: [z-setup]\n    steps:\n      - name: s\n        action: \"shell: true\"\n" +
		"  - id: z-setup\n    name: z\n    steps:\n      - name: s\n        action: \"shell: true\"\n" +
		"  - id: c-loop\n    name: c\n    dependencies: [d-loop]\n    steps:\n      - name: s\n        action: \"shell: true\"\n" +
		"  - id: d-loop\n    name: d\n    dependencies: [c-loop]\n    steps:\n      - name: s\n        action: \"shell: true\"\n"
	if err := os.WriteFile(filepath.Join(dir, "deps.yaml"), []byte(bank), 0o600); err != nil {
		t.Fatal(err)
	}
	ste := &StructuredTestExecutor{config: PipelineConfig{Platforms: []string{"desktop"}, BanksDir: dir}, execFactory: failingFactory{}}
	res, err := ste.Execute(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	verdict := map[string]CaseVerdict{}
	for _, c := range res.Cases {
		ids = append(ids, c.ID)
		verdict[c.ID] = c.Verdict
	}
	if strings.Join(ids, ",") != "z-setup,a-check,c-loop,d-loop" {
		t.Fatalf("case order %v, want z-setup,a-check then the cycle", ids)
	}
	if verdict["c-loop"] != CaseVerdictBlocked || verdict["d-loop"] != CaseVerdictBlocked || verdict["a-check"] != CaseVerdictPassed {
		t.Fatalf("verdicts %v — a dependency cycle is BLOCKED, the rest runs", verdict)
	}
}

// Review round 8, findings 4 + 5: the inability scan needs a first-person
// subject (or the text's start) and ignores an app image ("the image OF …");
// it knows attached / provided / uploaded images; and a NO resting on
// "could not see the image" is BLOCKED, not filed as an app defect.
func TestNoBluffVision_R9_InabilityScanScope(t *testing.T) {
	blocked := []string{
		"I cannot see the image", "I can't view the attached image, but assuming the state is met",
		"I'm unable to see the provided screenshot", "I am not able to see the screenshot",
		"Unable to view images; assuming yes", "As an AI, I can't see pictures", "cannot see any images",
		"settings; I cannot see the image", "I cannot see the attached screenshot", "couldn't load the image",
		"I am unable to view the image", "Unable to see the screenshot",
	}
	for _, a := range blocked {
		ste, _ := newSTE(&fakeVision{verify: "VERIFIED: yes\nACTUAL: " + a}, noisePNG(t, 1), noisePNG(t, 2))
		requireBlocked(t, run(ste, gated(nil)), a)
	}
	pass := []string{
		"Settings. I cannot see the image of the user avatar as it is blank",
		"A dialog reads 'Could not load image'", "Toast: Unable to open image. Settings screen behind it",
		"Settings. Note: you cannot see the image thumbnails until they load",
		"The attached screenshot shows the settings screen",
	}
	for _, a := range pass {
		ste, _ := newSTE(&fakeVision{verify: "VERIFIED: yes\nACTUAL: " + a}, noisePNG(t, 1), noisePNG(t, 2))
		if r := run(ste, gated(nil)); !r.Passed {
			t.Errorf("%q: %q — quoted app text / app image is not an inability", a, r.Actual)
		}
	}
	ste, _ := newSTE(&fakeVision{verify: "VERIFIED: no\nACTUAL: I cannot see the image"}, noisePNG(t, 1), noisePNG(t, 2))
	res := runCase(t, ste, gated(nil))
	requireCaseVerdict(t, res, CaseVerdictBlocked, "NO resting on an inability")
	ste, _ = newSTE(&fakeVision{verify: "VERIFIED: no\nACTUAL: The screenshot shows a login form"}, noisePNG(t, 1), noisePNG(t, 2))
	requireCaseVerdict(t, runCase(t, ste, gated(nil)), CaseVerdictFailed, "definite NO with an ordinary description")
}
