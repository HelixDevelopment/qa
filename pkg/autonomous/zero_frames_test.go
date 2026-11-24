// SPDX-FileCopyrightText: 2026 Milos Vasic
// SPDX-License-Identifier: Apache-2.0

package autonomous

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"digital.vasic.helixqa/pkg/analysis"
	"digital.vasic.helixqa/pkg/memory"
)

// A session in which NO frame was analysed (here: the provider has no vision,
// and no web target exists so nothing could be captured either) makes no claim
// about the application. It used to print "N/N tests, 100% coverage" and exit
// 0; the three-valued contract says it is UNDETERMINED: exit 2, with the
// numbers that prove it in the report.
func TestSessionPipeline_ZeroFramesAnalysed_IsUndetermined(t *testing.T) {
	tmpDir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(tmpDir, "docs"), 0o755))
	store, err := memory.NewStore(filepath.Join(tmpDir, "data", "memory.db"))
	require.NoError(t, err)
	defer store.Close()

	cfg := &PipelineConfig{
		ProjectRoot: tmpDir,
		Platforms:   []string{"web"},
		OutputDir:   filepath.Join(tmpDir, "output"),
		Timeout:     30 * time.Second,
		PassNumber:  1,
	}
	p := NewSessionPipeline(cfg, &stubLLM{}, store)
	result, err := p.Run(context.Background())
	require.NoError(t, err)
	require.NotNil(t, result)

	assert.Equal(t, StatusBlocked, result.Status, "zero analysed frames must not be a complete session")
	assert.Equal(t, 2, result.ExitCode(), "zero analysed frames is could-not-determine (2), never 0")
	assert.Contains(t, result.Error, "no frame was analysed")
	assert.InDelta(t, 0.0, result.CoveragePct, 0.001, "coverage over zero analysed frames must not be reported as a percentage")
}

func TestUndeterminedNoFrames_Table(t *testing.T) {
	complete := func(f func(*PipelineResult)) *PipelineResult {
		r := &PipelineResult{Status: StatusComplete, TestsRun: 5, TestsPlanned: 5}
		f(r)
		return r
	}
	assert.NotEmpty(t, undeterminedNoFrames(complete(func(r *PipelineResult) { r.FramesBlank = 32 })),
		"32 blank frames and 0 analysed is UNDETERMINED")
	assert.Empty(t, undeterminedNoFrames(complete(func(r *PipelineResult) { r.FramesCaptured = 3; r.FramesAnalysed = 3 })),
		"control: analysed frames keep a complete session")
	assert.Empty(t, undeterminedNoFrames(complete(func(r *PipelineResult) {
		r.Structured = &StructuredExecutionResult{TestCasesRun: 2}
	})), "bank cases that ran carry their own verdicts")
	assert.Empty(t, undeterminedNoFrames(&PipelineResult{Status: StatusComplete}), "no test ran: nothing was claimed")
}

// The structured phase on a project with no bank directory is a SKIP with a
// clear message and no StructuredError (which would make the exit code 2 for a
// reason that is not a failure); an explicitly requested missing directory is
// an error.
func TestStructuredPhase_MissingBankDir(t *testing.T) {
	tmp := t.TempDir()
	missing := filepath.Join(tmp, "challenges", "helixqa-banks")
	run := func(explicit bool) *PipelineResult {
		sp := &SessionPipeline{config: &PipelineConfig{ProjectRoot: tmp, BanksDir: missing, BanksExplicit: explicit}}
		r := &PipelineResult{}
		sp.runStructuredPhase(context.Background(), nil, nil, r, func(analysis.AnalysisFinding) {}, func(string, []byte) {})
		return r
	}
	def := run(false)
	assert.Empty(t, def.StructuredError)
	assert.Contains(t, def.StructuredSkipped, "did NOT run")
	assert.Nil(t, def.Structured)
	exp := run(true)
	assert.Contains(t, exp.StructuredError, "requested with -banks")
	assert.Empty(t, exp.StructuredSkipped)
	assert.Equal(t, 2, exp.ExitCode())
}

// captureStdout runs fn and returns what it printed to os.Stdout.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	require.NoError(t, err)
	os.Stdout = w
	done := make(chan string)
	go func() {
		b, _ := io.ReadAll(r)
		done <- string(b)
	}()
	fn()
	_ = w.Close()
	os.Stdout = old
	return <-done
}

// Host-display recording (ffmpeg x11grab) must not start unless the caller
// opted in. A plain session used to grab the operator's real screen.
func TestSessionPipeline_HostVideoIsOptIn(t *testing.T) {
	// No ffmpeg on PATH: the opt-in run ATTEMPTS the recorder (and fails)
	// without ever touching a real display.
	t.Setenv("PATH", t.TempDir())
	run := func(record bool) string {
		tmpDir := t.TempDir()
		require.NoError(t, os.MkdirAll(filepath.Join(tmpDir, "docs"), 0o755))
		store, err := memory.NewStore(filepath.Join(tmpDir, "data", "memory.db"))
		require.NoError(t, err)
		defer store.Close()
		cfg := &PipelineConfig{ProjectRoot: tmpDir, Platforms: []string{"web"},
			OutputDir: filepath.Join(tmpDir, "output"), Timeout: 30 * time.Second, PassNumber: 1, RecordVideo: record}
		return captureStdout(t, func() {
			_, err = NewSessionPipeline(cfg, &stubLLM{}, store).Run(context.Background())
		})
	}
	off := run(false)
	assert.NotContains(t, off, "ffmpeg recording started")
	assert.Contains(t, off, "host-display video recording OFF")
	on := run(true)
	assert.Contains(t, on, "video recording failed for web: ffmpeg not found", "control: with the opt-in the recorder is attempted")
	assert.NotContains(t, on, "host-display video recording OFF")
}

// A session in which SOME frames were analysed and others got an unusable
// model reply (empty / refusal / non-JSON) judged only part of what it
// captured. It must say PARTIAL with the counts and must not exit 0.
func TestPartialFrames_NotPlainComplete(t *testing.T) {
	r := &PipelineResult{
		Status: StatusComplete, TestsRun: 5, TestsPlanned: 5, CoveragePct: 100,
		FramesCaptured: 5, FramesAnalysed: 3, FramesUnusable: 2,
	}
	line := sessionSummaryLine(r)
	assert.Contains(t, line, "PARTIAL")
	assert.Contains(t, line, "3 of 5")
	assert.Contains(t, line, "2 frame(s) got an unusable")
	assert.NotContains(t, line, "] Complete:", "must not read as a plain Complete")
	assert.Equal(t, 2, r.ExitCode(), "part of the session could not be judged: could-not-determine, never 0")
	assert.Contains(t, partialFrames(r), "2 of 5")

	// Controls: all frames usable stays a plain Complete with exit 0.
	ok := &PipelineResult{Status: StatusComplete, TestsRun: 5, TestsPlanned: 5, FramesCaptured: 5, FramesAnalysed: 5}
	assert.Contains(t, sessionSummaryLine(ok), "] Complete:")
	assert.Equal(t, 0, ok.ExitCode())
	assert.Empty(t, partialFrames(ok))

	// A failed session stays failed (1 outranks 2).
	f := &PipelineResult{Status: StatusFailed, FramesAnalysed: 1, FramesUnusable: 1}
	assert.Equal(t, 1, f.ExitCode())
}
