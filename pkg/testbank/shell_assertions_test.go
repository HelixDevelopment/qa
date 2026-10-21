package testbank

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Operator decision 2026-10-08: expect_exit and expect_output_contains are
// REAL assertions on `shell:` steps. Until now no executor evaluated them, so
// 34 step assertions in three shipped banks asserted nothing. These tests pin
// the load rules (never a silent no-op) and the single shared verdict
// function both shell executors call (so the two paths cannot drift).

func shellBank(extra string) string {
	return `version: "1"
name: sh
test_cases:
  - id: SH-001
    name: shell-assertions
    steps:
      - name: s
        action: "shell: echo hi"
` + extra
}

func intp(i int) *int       { return &i }
func strp(s string) *string { return &s }

func TestLoadFile_ShellAssertionKeysLoadOnAShellStep(t *testing.T) {
	bf, err := LoadFile(writeNCBank(t, shellBank("        expect_exit: 3\n        expect_output_contains: \"hi\"\n")))
	require.NoError(t, err)
	st := bf.TestCases[0].Steps[0]
	require.NotNil(t, st.ExpectExit)
	assert.Equal(t, 3, *st.ExpectExit)
	require.NotNil(t, st.ExpectOutputContains)
	assert.Equal(t, "hi", *st.ExpectOutputContains)
}

// expect_exit: 0 must be distinguishable from "not set" — that is why the
// field is a pointer.
func TestLoadFile_ExpectExitZeroIsAnExplicitValue(t *testing.T) {
	bf, err := LoadFile(writeNCBank(t, shellBank("        expect_exit: 0\n")))
	require.NoError(t, err)
	require.NotNil(t, bf.TestCases[0].Steps[0].ExpectExit)
	assert.Equal(t, 0, *bf.TestCases[0].Steps[0].ExpectExit)
}

func TestLoadFile_ShellAssertionKeysNoLongerWarnAsIgnored(t *testing.T) {
	logs := captureTwinLog(t)
	_, err := LoadFile(writeNCBank(t, shellBank("        expect_exit: 0\n        expect_output_contains: \"hi\"\n")))
	require.NoError(t, err)
	assert.NotContains(t, logs.String(), "assert NOTHING")
	assert.NotContains(t, logs.String(), "WARNING")
}

// A step whose action yields no exit code / combined output must not carry
// the keys: the assertion would be silently ignored.
func TestLoadFile_ExpectExitOnAnHTTPStepIsLoadError(t *testing.T) {
	_, err := LoadFile(writeNCBank(t, `version: "1"
name: sh
test_cases:
  - id: SH-002
    name: http-with-exit
    steps:
      - name: s
        action: "http: GET /x"
        expect_status: 200
        expect_exit: 0
`))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "expect_exit")
	assert.Contains(t, err.Error(), "shell:")
}

func TestLoadFile_ExpectOutputContainsOnAnADBShellStepIsLoadError(t *testing.T) {
	_, err := LoadFile(writeNCBank(t, `version: "1"
name: sh
test_cases:
  - id: SH-003
    name: adb-with-output
    steps:
      - name: s
        action: "adb_shell: getprop ro.build.version.sdk"
        expect_output_contains: "34"
`))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "expect_output_contains")
}

func TestLoadFile_EmptyExpectOutputContainsIsLoadError(t *testing.T) {
	_, err := LoadFile(writeNCBank(t, shellBank("        expect_output_contains: \"\"\n")))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "expect_output_contains")
	assert.Contains(t, err.Error(), "empty")
}

// An exit code is 0..255; anything else can never equal a real exit status
// (a signal death reports -1), so the step could never pass.
func TestLoadFile_ExpectExitOutOfRangeIsLoadError(t *testing.T) {
	for _, bad := range []string{"-1", "256", "1000"} {
		_, err := LoadFile(writeNCBank(t, shellBank("        expect_exit: "+bad+"\n")))
		require.Error(t, err, "expect_exit %s must be refused", bad)
		assert.Contains(t, err.Error(), "expect_exit")
	}
}

func TestLoadFile_JSONBankShellAssertionsLoad(t *testing.T) {
	p := filepath.Join(t.TempDir(), "b.json")
	require.NoError(t, os.WriteFile(p, []byte(`{"version":"1","name":"j","test_cases":[{"id":"J-1","name":"j","steps":[{"name":"s","action":"shell: echo hi","expect_exit":0,"expect_output_contains":"hi"}]}]}`), 0o600))
	bf, err := LoadFile(p)
	require.NoError(t, err)
	st := bf.TestCases[0].Steps[0]
	require.NotNil(t, st.ExpectExit)
	require.NotNil(t, st.ExpectOutputContains)
}

// CheckShellOutcome is THE verdict both shell executors use.
func TestCheckShellOutcome(t *testing.T) {
	cases := []struct {
		name   string
		step   TestStep
		exit   int
		out    string
		wantOK bool
		wantIn string
	}{
		{"no keys, exit 0 passes (legacy behaviour kept)", TestStep{}, 0, "x", true, ""},
		{"no keys, exit 1 fails (legacy behaviour kept)", TestStep{}, 1, "x", false, "exited 1"},
		{"expect_exit 0 matches", TestStep{ExpectExit: intp(0)}, 0, "", true, ""},
		{"expect_exit 0 vs exit 2 fails", TestStep{ExpectExit: intp(0)}, 2, "", false, "expected 0"},
		{"expect_exit 3 matches exit 3 (non-zero is a pass when asked for)", TestStep{ExpectExit: intp(3)}, 3, "", true, ""},
		{"expect_exit 3 vs exit 0 fails", TestStep{ExpectExit: intp(3)}, 0, "", false, "expected 3"},
		{"signal death never equals a real code", TestStep{ExpectExit: intp(0)}, -1, "", false, "exited -1"},
		{"output contains hit", TestStep{ExpectOutputContains: strp("needle")}, 0, "a needle b", true, ""},
		{"output contains miss", TestStep{ExpectOutputContains: strp("needle")}, 0, "haystack", false, "does not contain"},
		{"output match is case-sensitive", TestStep{ExpectOutputContains: strp("Needle")}, 0, "needle", false, "does not contain"},
		{"both keys, both satisfied", TestStep{ExpectExit: intp(4), ExpectOutputContains: strp("x")}, 4, "x", true, ""},
		{"exit ok but output missing still fails", TestStep{ExpectExit: intp(4), ExpectOutputContains: strp("x")}, 4, "y", false, "does not contain"},
		{"output ok but exit wrong still fails", TestStep{ExpectExit: intp(4), ExpectOutputContains: strp("x")}, 5, "x", false, "expected 4"},
		{"empty expect_output_contains at run time is a failure, never a pass", TestStep{ExpectOutputContains: strp("")}, 0, "anything", false, "empty"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ok, msg := c.step.CheckShellOutcome(c.exit, c.out)
			assert.Equal(t, c.wantOK, ok, msg)
			if c.wantIn != "" {
				assert.Contains(t, msg, c.wantIn)
			}
			if c.wantOK {
				assert.Empty(t, msg)
			}
		})
	}
}
