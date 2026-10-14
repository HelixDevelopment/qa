package testbank

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func writeNCBank(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "b.yaml")
	require.NoError(t, os.WriteFile(p, []byte(body), 0o600))
	return p
}

const notContainsBankHead = `version: "1"
name: nc
test_cases:
  - id: NC-001
    name: not-contains
    steps:
      - name: s
        action: "http: GET /x"
        expect_status: 200
`

func TestLoadFile_ExpectBodyNotContainsRoundTrips(t *testing.T) {
	bf, err := LoadFile(writeNCBank(t, notContainsBankHead+"        expect_body_not_contains: \"01SECRET\"\n"))
	require.NoError(t, err)
	st := bf.TestCases[0].Steps[0]
	require.NotNil(t, st.ExpectBodyNotContains)
	assert.Equal(t, "01SECRET", *st.ExpectBodyNotContains)
}

// A vacuous negative assertion must be a LOAD error, not a silent pass.
func TestLoadFile_ExpectBodyNotContainsEmptyIsLoadError(t *testing.T) {
	_, err := LoadFile(writeNCBank(t, notContainsBankHead+"        expect_body_not_contains: \"\"\n"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "expect_body_not_contains")
}

func TestLoadFile_ExpectBodyNotContainsAbsentIsFine(t *testing.T) {
	bf, err := LoadFile(writeNCBank(t, notContainsBankHead))
	require.NoError(t, err)
	assert.Nil(t, bf.TestCases[0].Steps[0].ExpectBodyNotContains)
}

// Only the HTTP executor consumes expect_body_not_contains. On any other
// action kind the assertion would be silently ignored, so a bank asserting
// nothing would still load and pass — refuse it at load time.
func TestLoadFile_ExpectBodyNotContainsOnNonHTTPActionIsLoadError(t *testing.T) {
	_, err := LoadFile(writeNCBank(t, `version: "1"
name: nc
test_cases:
  - id: NC-002
    name: wrong-action-kind
    steps:
      - name: s
        action: "shell: echo hi"
        expect_status: 200
        expect_body_not_contains: "01SECRET"
`))
	require.Error(t, err, "a non-http action must not be allowed to carry a negative body assertion")
	assert.Contains(t, err.Error(), "expect_body_not_contains")
	assert.Contains(t, err.Error(), "http")
}

// A negative assertion alone passes on a 500 with a clean body, an empty body
// or an error envelope. It must be paired with a positive assertion about the
// same response.
func TestLoadFile_ExpectBodyNotContainsNeedsAPositiveAssertion(t *testing.T) {
	_, err := LoadFile(writeNCBank(t, `version: "1"
name: nc
test_cases:
  - id: NC-003
    name: negative-only
    steps:
      - name: s
        action: "http: GET /x"
        expect_body_not_contains: "01SECRET"
`))
	require.Error(t, err, "a negative-only step can pass vacuously")
	assert.Contains(t, err.Error(), "positive")
}

func TestLoadFile_ExpectBodyNotContainsAcceptsEachPositiveCompanion(t *testing.T) {
	for name, extra := range map[string]string{
		"status":        "        expect_status: 200\n",
		"body_contains": "        expect_body_contains: \"ok\"\n",
		"json_path":     "        expect_json_path: \"$.status\"\n",
	} {
		t.Run(name, func(t *testing.T) {
			_, err := LoadFile(writeNCBank(t, `version: "1"
name: nc
test_cases:
  - id: NC-004
    name: paired
    steps:
      - name: s
        action: "http: GET /x"
`+extra+"        expect_body_not_contains: \"01SECRET\"\n"))
			require.NoError(t, err)
		})
	}
}
