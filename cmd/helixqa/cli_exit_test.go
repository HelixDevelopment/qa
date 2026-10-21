// SPDX-FileCopyrightText: 2026 Milos Vasic
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"digital.vasic.helixqa/pkg/autonomous"
	"digital.vasic.helixqa/pkg/orchestrator"
	"digital.vasic.helixqa/pkg/reporter"
)

// TestMain doubles as the CLI child process for TestCLIExit_*: when
// HELIXQA_CLI_EXIT_CHILD=1 it runs the real main() with the arguments from
// HELIXQA_CLI_EXIT_ARGS, so the exit status observed by the parent is the
// one the shipped binary produces (os.Exit wiring included).
func TestMain(m *testing.M) {
	if os.Getenv("HELIXQA_CLI_EXIT_CHILD") == "1" {
		var args []string
		if err := json.Unmarshal([]byte(os.Getenv("HELIXQA_CLI_EXIT_ARGS")), &args); err != nil {
			os.Exit(99)
		}
		os.Args = append([]string{"helixqa"}, args...)
		if os.Getenv("HELIXQA_CLI_EXIT_PANIC") == "1" {
			realMainFn = func() { panic("injected panic (CLI exit wiring test)") }
		}
		main()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func runCLIChild(t *testing.T, panicMode bool, args ...string) int {
	t.Helper()
	enc, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^$")
	cmd.Env = append(os.Environ(), "HELIXQA_CLI_EXIT_CHILD=1", "HELIXQA_CLI_EXIT_ARGS="+string(enc))
	if panicMode {
		cmd.Env = append(cmd.Env, "HELIXQA_CLI_EXIT_PANIC=1")
	}
	out, err := cmd.CombinedOutput()
	var ee *exec.ExitError
	switch {
	case err == nil:
		return 0
	case errors.As(err, &ee):
		t.Logf("child %v exit %d; output tail: %s", args, ee.ExitCode(), tail(out, 400))
		return ee.ExitCode()
	default:
		t.Fatalf("child did not run: %v", err)
		return -1
	}
}

func tail(b []byte, n int) string {
	if len(b) > n {
		return string(b[len(b)-n:])
	}
	return string(b)
}

func writeCLIBank(t *testing.T, dir, shell string) string {
	t.Helper()
	p := filepath.Join(dir, "bank.yaml")
	body := "version: \"1.0\"\nname: cli\ntest_cases:\n  - id: CLI-1\n    name: case\n    platforms: [desktop]\n    steps:\n" +
		"      - name: cli\n        action: \"shell: " + shell + "\"\n"
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// Review round 5, finding 8: the exit wiring of the real binary — the code
// the parent observes, not the value a helper returns.
func TestCLIExit_RunWiring(t *testing.T) {
	dir := t.TempDir()
	common := []string{"--platform", "desktop", "--validate=false", "--record=false", "--tickets=false"}
	run := func(bank, out string) int {
		return runCLIChild(t, false, append([]string{"run", "--banks", bank, "--output", out}, common...)...)
	}
	if got := run(filepath.Join(dir, "missing.yaml"), filepath.Join(dir, "o1")); got != 2 {
		t.Errorf("unloadable bank: exit %d, want 2 (nothing executed)", got)
	}
	for _, sub := range []string{"p", "f"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	pass := writeCLIBank(t, filepath.Join(dir, "p"), "true")
	if got := run(pass, filepath.Join(dir, "o2")); got != 0 {
		t.Errorf("passing bank: exit %d, want 0", got)
	}
	fail := writeCLIBank(t, filepath.Join(dir, "f"), "exit 3")
	if got := run(fail, filepath.Join(dir, "o3")); got != 1 {
		t.Errorf("failing bank: exit %d, want 1", got)
	}
	// The case FAILED, then the report could not be written: 1 outranks
	// the post-execution error.
	blocked := filepath.Join(dir, "o4")
	if err := os.MkdirAll(filepath.Join(blocked, "qa-report.md"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := run(fail, blocked); got != 1 {
		t.Errorf("failing bank + report write error: exit %d, want 1", got)
	}
	// A passing case whose report cannot be written is incomplete: 2.
	blocked2 := filepath.Join(dir, "o5")
	if err := os.MkdirAll(filepath.Join(blocked2, "qa-report.md"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := run(pass, blocked2); got != 2 {
		t.Errorf("passing bank + report write error: exit %d, want 2", got)
	}
}

func TestCLIExit_PanicIs70(t *testing.T) {
	if got := runCLIChild(t, true, "version"); got != exitCodePanic || exitCodePanic != 70 {
		t.Fatalf("panic in realMain: exit %d, want 70", got)
	}
}

// Finding 5: a FAILED verdict outranks an error raised after execution; any
// other error leaves the run incomplete (2).
func TestRunExitCode_FailedOutranksPostExecutionError(t *testing.T) {
	failed := &orchestrator.Result{Report: &reporter.QAReport{TotalChallenges: 1, FailedChallenges: 1}}
	if got := runExitCode(failed, errors.New("write report: is a directory")); got != 1 {
		t.Fatalf("failed run + post-execution error = %d, want 1", got)
	}
	passed := &orchestrator.Result{Report: &reporter.QAReport{TotalChallenges: 1, PassedChallenges: 1}}
	if got := runExitCode(passed, errors.New("write report: is a directory")); got != 2 {
		t.Fatalf("passed run + post-execution error = %d, want 2", got)
	}
	if got := runExitCode(&orchestrator.Result{}, errors.New("context canceled")); got != 2 {
		t.Fatalf("no report + error = %d, want 2", got)
	}
}

// Review round 6, finding 10 (N08): a crash or ANR in the report outranks a
// post-execution error just like a failed challenge.
func TestRunExitCode_CrashOrANROutranksPostExecutionError(t *testing.T) {
	for name, q := range map[string]*reporter.QAReport{
		"crash": {TotalChallenges: 1, PassedChallenges: 1, TotalCrashes: 1},
		"anr":   {TotalChallenges: 1, PassedChallenges: 1, TotalANRs: 1},
	} {
		if got := runExitCode(&orchestrator.Result{Report: q}, errors.New("write report: is a directory")); got != 1 {
			t.Errorf("%s + post-execution error = %d, want 1", name, got)
		}
	}
}

// Review round 6, finding 17: `helixqa autonomous` with a report that could
// not be written is incomplete (2) unless the session already has a
// finding (1); a written report leaves the session's own code.
func TestAutonomousExitCode_ReportWriteError(t *testing.T) {
	pass := &autonomous.PipelineResult{Status: autonomous.StatusComplete}
	failed := &autonomous.PipelineResult{Status: autonomous.StatusComplete,
		Structured: &autonomous.StructuredExecutionResult{TestCasesRun: 1, TestCasesFailed: 1}}
	werr := errors.New("write pipeline-report.json: read-only file system")
	cases := []struct {
		name string
		r    *autonomous.PipelineResult
		err  error
		want int
	}{
		{"pass, report written", pass, nil, 0},
		{"pass, report write failed", pass, werr, 2},
		{"failed case, report write failed", failed, werr, 1},
		{"blocked session, report written", &autonomous.PipelineResult{Status: autonomous.StatusBlocked}, nil, 2},
	}
	for _, c := range cases {
		if got := autonomousExitCode(c.r, c.err); got != c.want {
			t.Errorf("%s: exit %d, want %d", c.name, got, c.want)
		}
	}
}

// Review round 6, finding 1, through the real binary: `shell: exit 3`
// FAILS, then --timeout cuts `shell: sleep 4` — exit 1, the failure kept.
func TestCLIExit_TimeoutKeepsFailure(t *testing.T) {
	dir := t.TempDir()
	bank := filepath.Join(dir, "bank.yaml")
	body := "version: \"1.0\"\nname: cli\ntest_cases:\n" +
		"  - id: CT-1\n    name: fails\n    platforms: [desktop]\n    steps:\n      - name: a\n        action: \"shell: exit 3\"\n" +
		"  - id: CT-2\n    name: slow\n    platforms: [desktop]\n    steps:\n      - name: a\n        action: \"shell: sleep 4\"\n" +
		"  - id: CT-3\n    name: never\n    platforms: [desktop]\n    steps:\n      - name: a\n        action: \"shell: true\"\n"
	if err := os.WriteFile(bank, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	got := runCLIChild(t, false, "run", "--banks", bank, "--output", filepath.Join(dir, "out"),
		"--platform", "desktop", "--validate=false", "--record=false", "--tickets=false", "--speed", "fast", "--timeout", "2s")
	if got != 1 {
		t.Fatalf("failure then timeout: exit %d, want 1", got)
	}
}

// Review round 7, finding 1, through the real binary: SIGINT two seconds
// into `shell: sleep 38; true` — the interrupted challenge is not a
// failure: exit 2, promptly, and no `sleep` outlives the process.
func TestCLIExit_SigintDuringShellStep(t *testing.T) {
	dir := t.TempDir()
	bank := filepath.Join(dir, "bank.yaml")
	body := "version: \"1.0\"\nname: cli\ntest_cases:\n" +
		"  - id: SI-1\n    name: one\n    platforms: [desktop]\n    steps:\n      - name: a\n        action: \"shell: true\"\n" +
		"  - id: SI-2\n    name: slow\n    platforms: [desktop]\n    steps:\n      - name: a\n        action: \"shell: sleep 38; true\"\n" +
		"  - id: SI-3\n    name: three\n    platforms: [desktop]\n    steps:\n      - name: a\n        action: \"shell: true\"\n"
	if err := os.WriteFile(bank, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	args := []string{"run", "--banks", bank, "--output", filepath.Join(dir, "out"), "--platform", "desktop",
		"--validate=false", "--record=false", "--tickets=false", "--speed", "fast"}
	enc, _ := json.Marshal(args)
	cmd := exec.Command(os.Args[0], "-test.run=^$")
	cmd.Env = append(os.Environ(), "HELIXQA_CLI_EXIT_CHILD=1", "HELIXQA_CLI_EXIT_ARGS="+string(enc))
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(2 * time.Second)
	start := time.Now()
	_ = cmd.Process.Signal(os.Interrupt)
	err := cmd.Wait()
	var ee *exec.ExitError
	code := 0
	if errors.As(err, &ee) {
		code = ee.ExitCode()
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Fatalf("exit took %v after SIGINT", d)
	}
	if code != 2 {
		t.Fatalf("SIGINT during a shell step: exit %d, want 2", code)
	}
}

// Review round 8, finding 1, through the real binary: a FAILED step whose
// output contains the interruption marker text, then SIGINT — exit 1.
func TestCLIExit_MarkerTextInOutputThenSigint(t *testing.T) {
	dir := t.TempDir()
	bank := filepath.Join(dir, "bank.yaml")
	body := "version: \"1.0\"\nname: cli\ntest_cases:\n" +
		"  - id: MK-1\n    name: m\n    platforms: [desktop]\n    steps:\n" +
		"      - name: a\n        action: \"shell: echo interrupted-by-run-cancellation; exit 3\"\n" +
		"      - name: b\n        action: \"shell: sleep 38.9; true\"\n"
	if err := os.WriteFile(bank, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	args := []string{"run", "--banks", bank, "--output", filepath.Join(dir, "out"), "--platform", "desktop",
		"--validate=false", "--record=false", "--tickets=false", "--speed", "fast"}
	enc, _ := json.Marshal(args)
	cmd := exec.Command(os.Args[0], "-test.run=^$")
	cmd.Env = append(os.Environ(), "HELIXQA_CLI_EXIT_CHILD=1", "HELIXQA_CLI_EXIT_ARGS="+string(enc))
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(2 * time.Second)
	_ = cmd.Process.Signal(os.Interrupt)
	err := cmd.Wait()
	var ee *exec.ExitError
	code := 0
	if errors.As(err, &ee) {
		code = ee.ExitCode()
	}
	if code != 1 {
		t.Fatalf("FAILED step with the marker text in its output, then SIGINT: exit %d, want 1", code)
	}
}
