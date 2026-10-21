// SPDX-FileCopyrightText: 2026 Milos Vasic
// SPDX-License-Identifier: Apache-2.0

package autonomous

import (
	"context"
	"path/filepath"
	"testing"

	"digital.vasic.helixqa/pkg/analysis"
	"digital.vasic.helixqa/pkg/memory"
	"digital.vasic.helixqa/pkg/testbank"
)

// Review round 2, finding 5 (MEDIUM): a BLOCKED case is the QA run's own
// inability to judge (e.g. vision provider outage), not an app defect. Its
// finding is category "environment": never reproduced as a bug, never
// ticketed, never counted as an issue found — while a real functional
// finding still is.
func TestVisionFindings_BlockedIsEnvironmentNotADefect(t *testing.T) {
	ste, _ := newSTE(nilProvider, noisePNG(t, 1), noisePNG(t, 2))
	res := &StructuredExecutionResult{}
	tc := testbank.TestCase{ID: "TC-ENV", Name: "env", Priority: testbank.PriorityCritical,
		Steps: []testbank.TestStep{gated(nil)}}
	if err := ste.executeTestCase(context.Background(), tc, res); err != nil {
		t.Fatal(err)
	}
	if len(res.Findings) != 1 {
		t.Fatalf("want 1 blocked finding, got %d", len(res.Findings))
	}
	blocked := res.Findings[0]
	if blocked.Category != analysis.CategoryEnvironment || blocked.Category.IsAppDefect() {
		t.Fatalf("blocked finding category %q (app defect=%v), want environment / not a defect",
			blocked.Category, blocked.Category.IsAppDefect())
	}
	defect := analysis.AnalysisFinding{Category: analysis.CategoryFunctional, Severity: analysis.SeverityCritical,
		Title: "Login button does nothing", Platform: "web"}
	if !defect.Category.IsAppDefect() {
		t.Fatal("functional finding must be an app defect")
	}

	if bugs := findingsToReproBugs([]analysis.AnalysisFinding{blocked, defect}); len(bugs) != 1 {
		t.Fatalf("reproducer got %d bugs, want only the real defect", len(bugs))
	}
	if got := appDefectFindings([]analysis.AnalysisFinding{blocked, defect}); len(got) != 1 || got[0].Title != defect.Title {
		t.Fatalf("appDefectFindings = %+v", got)
	}

	store, err := memory.NewStore(filepath.Join(t.TempDir(), "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	bridge := NewFindingsBridge(store, t.TempDir(), "sess-env")
	ids, err := bridge.Process([]analysis.AnalysisFinding{blocked})
	if err != nil || len(ids) != 0 {
		t.Fatalf("environment finding ticketed: ids=%v err=%v", ids, err)
	}
	ids, err = bridge.Process([]analysis.AnalysisFinding{defect})
	if err != nil || len(ids) != 1 {
		t.Fatalf("real defect not ticketed: ids=%v err=%v", ids, err)
	}
}
