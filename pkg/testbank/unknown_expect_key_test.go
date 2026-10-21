package testbank

import (
	"log"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Operator decision 2026-10-08: an assertion key this build does not define
// must be a LOAD ERROR. Before, yaml.Unmarshal dropped unknown keys, so a
// stale binary (or a typo) turned a step that claimed to assert something
// into one that asserted nothing and still PASSED — the false pass
// WK-SEARCH-010 showed with a binary that predated expect_body_not_contains.

const unknownExpectHead = `version: "1"
name: ue
test_cases:
  - id: UE-001
    name: unknown-expect-key
    steps:
      - name: s
        action: "http: GET /x"
        expect_status: 200
`

func TestLoadFile_UnknownExpectKeyIsLoadError(t *testing.T) {
	_, err := LoadFile(writeNCBank(t, unknownExpectHead+"        expect_body_never_contains: \"x\"\n"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "expect_body_never_contains")
	assert.Contains(t, err.Error(), "unknown assertion key")
}

// A mistyped key that merely STARTS like a known one must not slip through.
func TestLoadFile_TypoedExpectKeyIsLoadError(t *testing.T) {
	_, err := LoadFile(writeNCBank(t, unknownExpectHead+"        expect_staus: 200\n"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "expect_staus")
}

// Every expect* key the struct defines must still load (the known set is
// derived from the struct's tags, so it cannot drift from the schema).
func TestLoadFile_EveryKnownExpectKeyStillLoads(t *testing.T) {
	typ := reflect.TypeOf(TestStep{})
	seen := 0
	for i := 0; i < typ.NumField(); i++ {
		name := strings.Split(typ.Field(i).Tag.Get("yaml"), ",")[0]
		if !strings.HasPrefix(name, "expect") {
			continue
		}
		seen++
		assert.True(t, knownStepKeys()[name], "known set must contain %s", name)
	}
	require.Greater(t, seen, 0, "the struct must define at least one expect* key, or this test is vacuous")
}

// Non-assertion unknown keys stay legal: measured against the real corpus a
// global KnownFields(true) rejects 43 of the 145 banks (fix_reference,
// executor_agent, requires ...). The rule is deliberately narrow.
func TestLoadFile_UnknownNonExpectKeyStaysLegal(t *testing.T) {
	bf, err := LoadFile(writeNCBank(t, unknownExpectHead+"        fix_reference: \"docs/x.md\"\n"))
	require.NoError(t, err)
	assert.Len(t, bf.TestCases, 1)
}

// "expected:" is a real field (the human description); a prefix match on
// "expect" must not confuse it with an assertion key.
func TestLoadFile_ExpectedProseFieldIsNotAnAssertionKey(t *testing.T) {
	bf, err := LoadFile(writeNCBank(t, unknownExpectHead+"        expected: \"returns 200\"\n"))
	require.NoError(t, err)
	assert.Equal(t, "returns 200", bf.TestCases[0].Steps[0].Expected)
}

// The same rule applies to JSON banks, which json.Unmarshal also silently
// truncates.
func TestLoadFile_UnknownExpectKeyInJSONBankIsLoadError(t *testing.T) {
	p := filepath.Join(t.TempDir(), "b.json")
	require.NoError(t, os.WriteFile(p, []byte(`{"version":"1","name":"ue","test_cases":[{"id":"UE-J1","name":"j","steps":[{"name":"s","action":"http: GET /x","expect_status":200,"expect_nope":"x"}]}]}`), 0o600))
	_, err := LoadFile(p)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "expect_nope")
}

// A key that is unknown but sits outside a step (e.g. an "expect_x" metadata
// key at the case level) is not an assertion key and must not be rejected by
// this rule.
func TestLoadFile_ExpectKeyOutsideStepsIsNotChecked(t *testing.T) {
	bf, err := LoadFile(writeNCBank(t, `version: "1"
name: ue
expect_note: "bank-level metadata"
test_cases:
  - id: UE-002
    name: outside-steps
    steps:
      - name: s
        action: "http: GET /x"
        expect_status: 200
`))
	require.NoError(t, err)
	assert.Len(t, bf.TestCases, 1)
}

// A directory scan reports a bank that declares no test cases BY NAME and
// says why; when the file carries a top-level "cases:" key (the
// yole-android-smoke shape: the loader reads "test_cases:") the reason
// must name that near-miss, because that is the actionable fact.
func TestLoadDirVerbose_ZeroCaseFileNamesTheCasesKeyTypo(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "good.yaml")
	bad := filepath.Join(dir, "typo.yaml")
	require.NoError(t, os.WriteFile(good, []byte(unknownExpectHead), 0o600))
	require.NoError(t, os.WriteFile(bad, []byte("version: \"1\"\nname: typo\ncases:\n  - id: T-1\n    name: n\n    steps:\n      - name: s\n        action: \"http: GET /x\"\n        expect_status: 200\n"), 0o600))

	res, err := LoadDirVerbose(dir)
	require.NoError(t, err, "a zero-case file must never abort the scan of the other banks")
	require.Len(t, res.Banks, 1, "the good bank still loads")

	var found *DeclinedFile
	for i := range res.Declined {
		if res.Declined[i].Path == bad {
			found = &res.Declined[i]
		}
	}
	require.NotNil(t, found, "the zero-case file must be reported, not dropped silently")
	assert.Contains(t, found.Reason, "test_cases")
	assert.Contains(t, found.Reason, "cases:")
}

// A zero-case file WITHOUT the near-miss key keeps the existing generic
// reason (no false hint).
func TestLoadDirVerbose_ZeroCaseFileWithoutTypoKeepsGenericReason(t *testing.T) {
	dir := t.TempDir()
	stray := filepath.Join(dir, "stray.yaml")
	require.NoError(t, os.WriteFile(stray, []byte("version: \"1\"\nname: stray\n"), 0o600))
	res, err := LoadDirVerbose(dir)
	require.NoError(t, err)
	require.Len(t, res.Declined, 1)
	assert.Contains(t, res.Declined[0].Reason, "declares 0 test cases")
	assert.NotContains(t, res.Declined[0].Reason, "top-level \"cases:\"")
}

// captureTwinLog swaps twinLogger for a buffer for the duration of a test.
func captureTwinLog(t *testing.T) *strings.Builder {
	t.Helper()
	var sb strings.Builder
	old := twinLogger
	twinLogger = log.New(&sb, "", 0)
	t.Cleanup(func() { twinLogger = old })
	return &sb
}

// A key NEAR a real assertion key is still a load error: expect_exit and
// expect_output_contains are now real keys, but expect_exit_code is not.
func TestLoadFile_NearMissOfARealAssertionKeyIsStillLoadError(t *testing.T) {
	_, err := LoadFile(writeNCBank(t, unknownExpectHead+"        expect_exit_code: 0\n"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "expect_exit_code")
}

// Pins the strict rule to reality over EVERY bank file: all of them must load
// (no key is silently dropped and none needs an allowlist), and the walk must
// have seen the real corpus or this test is vacuous. The former legacy
// allowlist for expect_exit / expect_output_contains is gone because both
// keys are now evaluated; a new unknown expect* key in any bank fails here.
func TestEveryBankInTheCorpusLoadsUnderTheStrictRule(t *testing.T) {
	logs := captureTwinLog(t)
	root := filepath.Join("..", "..", "banks")
	loaded := 0
	err := filepath.Walk(root, func(p string, info os.FileInfo, werr error) error {
		if werr != nil || info.IsDir() {
			return werr
		}
		switch strings.ToLower(filepath.Ext(p)) {
		case ".yaml", ".yml", ".json":
		default:
			return nil
		}
		if _, lerr := LoadFile(p); lerr != nil {
			t.Errorf("bank %s must load under the strict rule: %v", p, lerr)
		}
		loaded++
		return nil
	})
	require.NoError(t, err)
	require.Greater(t, loaded, 100, "the walk must see the real corpus, or this test is vacuous")
	assert.NotContains(t, logs.String(), "assert NOTHING",
		"no bank may carry an assertion key that nothing evaluates")
}
