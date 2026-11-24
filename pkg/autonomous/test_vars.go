// SPDX-FileCopyrightText: 2026 Milos Vasic
// SPDX-License-Identifier: Apache-2.0

package autonomous

import (
	"encoding/json"
	"os"
	"regexp"
	"sort"
)

// testVarRe matches ${HELIX_TEST_*} references in bank text. Only names with
// the HELIX_TEST_ prefix are ever expanded, so a bank can never read an
// arbitrary environment variable (HOME, API keys, ...) into a request.
var testVarRe = regexp.MustCompile(`\$\{(HELIX_TEST_[A-Z0-9_]+)\}`)

// expandTestVars resolves ${HELIX_TEST_*} references from the environment.
// Unset or empty variables are left unexpanded and returned in missing (sorted,
// unique) so the caller can refuse to send/type anything: there is no default
// value for a credential.
func expandTestVars(s string) (string, []string) {
	return expandTestVarsEsc(s, func(v string) string { return v })
}

// expandTestVarsJSON is expandTestVars for text that is already a JSON
// document: values are JSON-string-escaped so the document stays valid.
func expandTestVarsJSON(s string) (string, []string) {
	return expandTestVarsEsc(s, func(v string) string {
		b, _ := json.Marshal(v)
		return string(b[1 : len(b)-1])
	})
}

func expandTestVarsEsc(s string, esc func(string) string) (string, []string) {
	seen := map[string]bool{}
	var missing []string
	out := testVarRe.ReplaceAllStringFunc(s, func(m string) string {
		name := testVarRe.FindStringSubmatch(m)[1]
		v := os.Getenv(name)
		if v == "" {
			if !seen[name] {
				seen[name] = true
				missing = append(missing, name)
			}
			return m
		}
		return esc(v)
	})
	sort.Strings(missing)
	return out, missing
}
