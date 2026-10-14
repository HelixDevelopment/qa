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
