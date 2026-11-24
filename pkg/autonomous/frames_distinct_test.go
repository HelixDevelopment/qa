// SPDX-FileCopyrightText: 2026 Milos Vasic
// SPDX-License-Identifier: Apache-2.0

package autonomous

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Observed live against the real job_seeker console (private-CA TLS, local
// vision model): the execute phase "ran" 5 tests, every test's frame was the
// byte-identical login page (sha256 identical), and the session printed
// "Complete: 5/5 tests, 100.0% coverage, 5 frames analysed" and exited 0. A
// frame that never changes proves the tests never drove the application, so
// the figure is a bluff. These tests pin the distinct-frame accounting and the
// UNDETERMINED verdict that replaces it.

func writeFrames(t *testing.T, contents ...string) []string {
	t.Helper()
	dir := t.TempDir()
	var paths []string
	for i, c := range contents {
		p := filepath.Join(dir, "f"+string(rune('a'+i))+".png")
		require.NoError(t, os.WriteFile(p, []byte(c), 0o644))
		paths = append(paths, p)
	}
	return paths
}

func TestDistinctFrameCount(t *testing.T) {
	assert.Equal(t, 1, distinctFrameCount(writeFrames(t, "x", "x", "x", "x", "x")))
	assert.Equal(t, 2, distinctFrameCount(writeFrames(t, "x", "y", "x")))
	assert.Equal(t, 3, distinctFrameCount(writeFrames(t, "x", "y", "z")))
	assert.Equal(t, 0, distinctFrameCount(nil))
	// an entry that is not a readable file (structured-phase platform
	// placeholders are appended to the same list) is not a frame.
	assert.Equal(t, 1, distinctFrameCount(append(writeFrames(t, "x", "x"), "web", "/no/such/file")))
}

func TestUndeterminedNoInteraction_Table(t *testing.T) {
	mk := func(f func(*PipelineResult)) *PipelineResult {
		r := &PipelineResult{Status: StatusComplete, TestsRun: 5, TestsPlanned: 5,
			FramesCaptured: 5, FramesAnalysed: 5, FramesDistinct: 5}
		f(r)
		return r
	}
	reason := undeterminedNoInteraction(mk(func(r *PipelineResult) { r.FramesDistinct = 1 }))
	assert.NotEmpty(t, reason, "5 frames, 1 distinct: the application was not driven")
	assert.True(t, strings.HasPrefix(reason, noInteractionReasonPrefix))
	assert.Contains(t, reason, "5 frame(s) captured")
	assert.Contains(t, reason, "1 distinct")

	assert.Empty(t, undeterminedNoInteraction(mk(func(r *PipelineResult) {})), "control: distinct frames")
	assert.Empty(t, undeterminedNoInteraction(mk(func(r *PipelineResult) { r.FramesDistinct = 2 })),
		"two distinct frames show the screen changed")
	assert.Empty(t, undeterminedNoInteraction(mk(func(r *PipelineResult) {
		r.FramesDistinct = 1
		r.TestsRun = 1
		r.FramesCaptured = 1
		r.FramesAnalysed = 1
	})), "a single test with a single frame makes no repeated-frame claim")
	assert.Empty(t, undeterminedNoInteraction(mk(func(r *PipelineResult) { r.FramesDistinct = 1; r.FramesAnalysed = 0 })),
		"zero analysed frames has its own verdict (undeterminedNoFrames)")
	assert.Empty(t, undeterminedNoInteraction(mk(func(r *PipelineResult) {
		r.FramesDistinct = 1
		r.Structured = &StructuredExecutionResult{TestCasesRun: 3}
	})), "a session whose structured banks ran cases is judged by those verdicts")
	assert.Empty(t, undeterminedNoInteraction(mk(func(r *PipelineResult) {
		r.FramesDistinct = 1
		r.Status = StatusBlocked
	})), "an already blocked session keeps its own reason")
}

func TestSessionSummaryLine_NoInteraction(t *testing.T) {
	r := &PipelineResult{Status: StatusBlocked, TestsRun: 5, TestsPlanned: 5, FramesCaptured: 5, FramesAnalysed: 5, FramesDistinct: 1}
	r.Error = undeterminedNoInteraction(&PipelineResult{Status: StatusComplete, TestsRun: 5, TestsPlanned: 5, FramesCaptured: 5, FramesAnalysed: 5, FramesDistinct: 1})
	line := sessionSummaryLine(r)
	assert.Contains(t, line, "UNDETERMINED (exit 2)")
	assert.NotContains(t, line, "Complete:")
	assert.Equal(t, 2, r.ExitCode())
}

func TestApplyFrameVerdicts(t *testing.T) {
	r := &PipelineResult{Status: StatusComplete, TestsRun: 5, TestsPlanned: 5, FramesCaptured: 5,
		FramesAnalysed: 5, FramesDistinct: 1, CoveragePct: 100}
	applyFrameVerdicts(r)
	assert.Equal(t, StatusBlocked, r.Status)
	assert.Equal(t, 2, r.ExitCode())
	assert.InDelta(t, 0.0, r.CoveragePct, 0.001, "no coverage percentage over a screen that never changed")
	assert.True(t, strings.HasPrefix(r.Error, noInteractionReasonPrefix))

	ok := &PipelineResult{Status: StatusComplete, TestsRun: 5, TestsPlanned: 5, FramesCaptured: 5,
		FramesAnalysed: 5, FramesDistinct: 4, CoveragePct: 100}
	applyFrameVerdicts(ok)
	assert.Equal(t, StatusComplete, ok.Status, "control: a session whose screens changed stays complete")
	assert.Equal(t, 0, ok.ExitCode())
	assert.InDelta(t, 100.0, ok.CoveragePct, 0.001)

	none := &PipelineResult{Status: StatusComplete, TestsRun: 5, TestsPlanned: 5, FramesBlank: 5, CoveragePct: 100}
	applyFrameVerdicts(none)
	assert.True(t, strings.HasPrefix(none.Error, noFrameReasonPrefix), "the zero-frame verdict is unchanged")
}
