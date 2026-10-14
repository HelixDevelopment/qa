// SPDX-FileCopyrightText: 2026 Milos Vasic
// SPDX-License-Identifier: Apache-2.0

package autonomous

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"digital.vasic.helixqa/pkg/analysis"
)

// Review finding 2 (HIGH): a BLOCKED structured case must reach the user —
// the session result, the persisted report (with the frame evidence), a
// filed finding, and the process exit code (2; a FAILED case outranks it
// with 1). This drives the same runStructuredPhase that SessionPipeline.Run
// calls, with a fake screen and a provider that never answers definitely.
func TestPipelineResult_StructuredBlockedReachesExitCodeAndReport(t *testing.T) {
	dir := t.TempDir()
	banks := filepath.Join(dir, "banks")
	if err := os.MkdirAll(banks, 0o700); err != nil {
		t.Fatal(err)
	}
	bank := `version: "1"
name: nbv pipeline
test_cases:
  - id: TC-NBV-PIPE-1
    name: gated screenshot
    priority: high
    steps:
      - name: look
        action: screenshot
        expected: settings screen
        vision_verify: true
`
	if err := os.WriteFile(filepath.Join(banks, "nbv.yaml"), []byte(bank), 0o600); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "session-1")
	sp := &SessionPipeline{config: &PipelineConfig{
		ProjectRoot: dir, Platforms: []string{"desktop"}, BanksDir: banks, OutputDir: out,
	}}
	after := noisePNG(t, 2)
	ex := &screenExec{frames: [][]byte{noisePNG(t, 1), after}}
	result := &PipelineResult{Status: StatusComplete}
	var findings []analysis.AnalysisFinding
	sp.runStructuredPhase(context.Background(), screenFactory{ex},
		&fakeVision{verify: "The screen shows something."}, result,
		func(f analysis.AnalysisFinding) { findings = append(findings, f) }, nil)

	if result.Structured == nil || result.Structured.TestCasesBlocked != 1 {
		t.Fatalf("structured result not on the session result: %+v", result.Structured)
	}
	if got := result.ExitCode(); got != 2 {
		t.Fatalf("session ExitCode = %d, want 2 (a blocked case is could-not-determine)", got)
	}
	blockedFinding := false
	for _, f := range findings {
		if strings.Contains(f.Title, "BLOCKED") && strings.Contains(f.Description, "TC-NBV-PIPE-1") {
			blockedFinding = true
		}
	}
	if !blockedFinding {
		t.Fatalf("no finding filed for the blocked case: %+v", findings)
	}

	if err := sp.WriteReport(result); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(out, "pipeline-report.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), sha(after)) {
		t.Fatal("persisted report lacks the after-frame sha256")
	}
	var decoded PipelineResult
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Structured == nil || decoded.Structured.TestCasesBlocked != 1 ||
		len(decoded.Structured.Cases) != 1 || decoded.Structured.Cases[0].Verdict != CaseVerdictBlocked ||
		decoded.Structured.Cases[0].Steps[0].FramePHash == "" {
		t.Fatalf("report does not carry the blocked case evidence: %s", string(data))
	}
	if decoded.ExitCode() != 2 {
		t.Fatal("exit code not reproducible from the persisted report")
	}
}

// The process exit-code contract, evaluated on the live PipelineResult.
func TestPipelineResult_ExitCode(t *testing.T) {
	cases := []struct {
		name string
		r    PipelineResult
		want int
	}{
		{"session failed", PipelineResult{Status: StatusFailed}, 1},
		{"no structured phase ran", PipelineResult{Status: StatusComplete}, 0},
		{"structured ran nothing", PipelineResult{Status: StatusComplete, Structured: &StructuredExecutionResult{}}, 0},
		{"all passed", PipelineResult{Status: StatusComplete, Structured: &StructuredExecutionResult{TestCasesRun: 2, TestCasesPassed: 2}}, 0},
		{"one blocked among passes", PipelineResult{Status: StatusComplete, Structured: &StructuredExecutionResult{TestCasesRun: 3, TestCasesPassed: 2, TestCasesBlocked: 1}}, 2},
		{"failed outranks blocked", PipelineResult{Status: StatusComplete, Structured: &StructuredExecutionResult{TestCasesRun: 3, TestCasesFailed: 1, TestCasesBlocked: 1, TestCasesPassed: 1}}, 1},
		{"all skipped", PipelineResult{Status: StatusComplete, Structured: &StructuredExecutionResult{TestCasesRun: 2, TestCasesSkipped: 2}}, 2},
		{"banks could not load", PipelineResult{Status: StatusComplete, StructuredError: "load banks: no such dir"}, 2},
	}
	for _, c := range cases {
		if got := c.r.ExitCode(); got != c.want {
			t.Errorf("%s: ExitCode = %d, want %d", c.name, got, c.want)
		}
	}
}
