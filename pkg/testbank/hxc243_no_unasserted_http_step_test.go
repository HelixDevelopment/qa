// SPDX-FileCopyrightText: 2026 Milos Vasic
// SPDX-License-Identifier: Apache-2.0

package testbank

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// TestHXC243_EveryHTTPStepDeclaresAnExpectation is the standing
// regression guard for HXC-243 ("Some of our own test suites cannot
// fail, so their passes mean nothing").
//
// HXC-243 (§11.4.135 / §11.4.146): every `http:` action step in every
// bank file under banks/ MUST carry an explicit expectation
// (ExpectStatus, ExpectBodyContains, or ExpectJSONPath) OR be marked
// `_skip: true` with a non-empty `_skip_reason` explaining why no
// meaningful expectation can be expressed (Article XI §11.5 / §11.4.3).
// A step with none of those is structurally incapable of failing: it
// will report PASS regardless of what the server answers, which is
// worse than no test at all (it manufactures false confidence).
//
// This walks the directory tree RECURSIVELY, DIRECTLY via LoadFile on
// every *.yaml and *.json file found at any depth — deliberately NOT
// via LoadDir, which silently skips a JSON file whenever a same-named
// YAML sibling exists (loader.go lines ~107-118) and does not recurse
// into subdirectories at all. That skip is correct for `bin/helixqa
// run --banks banks/` (a directory scan), but every JSON bank remains
// independently reachable via `bin/helixqa http --bank
// banks/<name>.json` (cmd/helixqa/http.go calls mgr.LoadFile
// directly on a single path, bypassing the directory-scan skip). The
// demonstrated HXC-243 bluff — a collection aimed at a known-broken
// service reporting both its checks as passing — used exactly that
// reachable-but-directory-skipped path, so the guard must cover every
// file on disk, not just the subset LoadDir would pick.
//
// Recursion is load-bearing, not decorative: banks/ contains
// subdirectory banks today (banks/yole-concrete/*.yaml,
// banks/helix_vpn/*.{json,yaml}) that a top-level-only os.ReadDir
// walk never visits. The companion assertion below (mustHaveWalked)
// proves the walk REACHED each of those files — visitation, not
// inspection. For banks/helix_vpn/*.{json,yaml} that is the same
// thing: both are genuinely loaded and inspected (8 cases, 32 steps)
// and genuinely carry zero http: steps. It is NOT the same thing for
// banks/yole-concrete/yole-android-smoke.yaml: that file uses a
// different, schema-foreign top-level key (`cases:`, for
// cmd/helixqa-concrete-runner) rather than this loader's `test_cases:`,
// so LoadFile succeeds but decodes 0 cases / 0 steps — nothing was
// inspected there, and its "zero http: steps" is an artifact of
// schema-foreignness, not a finding. Forward protection still holds
// (adding a `test_cases:` block to that file would be caught), and
// the guard's own contract already promises "every bank file under
// banks/", so an http: step added in a subdirectory later would
// regress this exact Critical class silently under the old walk.
func TestHXC243_EveryHTTPStepDeclaresAnExpectation(t *testing.T) {
	dir := filepath.Join("..", "..", "banks")
	violations, filesWalked := collectUnassertedHTTPSteps(t, dir)
	if len(violations) > 0 {
		sort.Strings(violations)
		t.Fatalf(
			"HXC-243 regression: %d http: step(s) declare NO expectation "+
				"(no expect_status / expect_body_contains / expect_json_path) "+
				"and are not marked _skip with a reason. A check with none of "+
				"these will report PASS no matter what the server answers.\n%s",
			len(violations), strings.Join(violations, "\n"),
		)
	}

	// Companion assertion (recorded per the remedy this guard shipped
	// with): the walk actually descended into every known
	// subdirectory bank, so "0 violations" above is not silently true
	// because those files were never visited. If a new subdirectory
	// bank is added, it MUST show up here too — this list is not
	// exhaustive by construction, it is a floor.
	mustHaveWalked := []string{
		filepath.Join(dir, "yole-concrete", "yole-android-smoke.yaml"),
		filepath.Join(dir, "helix_vpn", "helix_vpn_bank.json"),
		filepath.Join(dir, "helix_vpn", "helix_vpn_bank.yaml"),
	}
	for _, want := range mustHaveWalked {
		if !filesWalked[want] {
			t.Fatalf(
				"HXC-243 recursion regression: expected subdirectory bank %q "+
					"to be visited by the walk, but it was not — the walker is "+
					"not actually recursing into banks/ subdirectories, so any "+
					"http: step added there would silently escape this guard. "+
					"…or this bank was intentionally renamed or removed — "+
					"update `mustHaveWalked`.",
				want,
			)
		}
	}
}

// collectUnassertedHTTPSteps is the shared walker used by both the
// real-corpus assertion above and the paired-mutation RED guard
// below, so the two can never silently drift out of agreement about
// what counts as a violation. It recurses into every subdirectory
// under dir (filepath.WalkDir), matching the promise of "every bank
// file under banks/" rather than only the top level. filesWalked
// records every bank file path actually visited, keyed by its
// filepath.Join'd path, so a caller can assert the recursion reached
// specific known subdirectory files rather than trusting silence.
func collectUnassertedHTTPSteps(t *testing.T, dir string) (violations []string, filesWalked map[string]bool) {
	t.Helper()
	filesWalked = map[string]bool{}
	err := filepath.WalkDir(dir, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(entry.Name()))
		if ext != ".yaml" && ext != ".yml" && ext != ".json" {
			return nil
		}
		filesWalked[path] = true
		bf, err := LoadFile(path)
		if err != nil {
			return fmt.Errorf("load bank file %s: %w", path, err)
		}
		for _, tc := range bf.TestCases {
			for _, st := range tc.Steps {
				at, _ := st.ParseAction()
				if at != ActionTypeHTTP {
					continue
				}
				if st.Skip {
					if strings.TrimSpace(st.SkipReason) == "" {
						violations = append(violations, fmt.Sprintf(
							"%s :: %s :: %q — _skip:true with an EMPTY _skip_reason",
							path, tc.ID, st.Name,
						))
					}
					continue
				}
				has := st.ExpectStatus != 0 ||
					st.ExpectBodyContains != "" ||
					st.ExpectBodyNotContains != nil ||
					st.ExpectJSONPath != ""
				if !has {
					violations = append(violations, fmt.Sprintf(
						"%s :: %s :: %q (action=%q) — no expectation and not skipped",
						path, tc.ID, st.Name, st.Action,
					))
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk bank dir %s: %v", dir, err)
	}
	return violations, filesWalked
}

// TestHXC243_GuardCatchesAnUnassertedHTTPStep is the paired-mutation
// proof (§1.1) that the guard above is not itself a bluff: it plants
// exactly the defect class HXC-243 describes — an http: step with no
// expect_status/expect_body_contains/expect_json_path and no _skip —
// in a throwaway bank, and asserts the walker used by the real guard
// reports it. A guard whose own mutation cannot make it fail would be
// decoration, not a check.
func TestHXC243_GuardCatchesAnUnassertedHTTPStep(t *testing.T) {
	dir := t.TempDir()
	bankPath := filepath.Join(dir, "planted_unasserted.yaml")
	content := []byte(`name: hxc243-planted-mutation
test_cases:
  - id: HXC243-PLANT-001
    name: "planted case with an unasserted http step"
    platforms: [api]
    steps:
      - name: "GET something and check nothing"
        action: "http: GET /whatever"
`)
	if err := os.WriteFile(bankPath, content, 0o644); err != nil {
		t.Fatalf("write planted bank: %v", err)
	}

	violations, _ := collectUnassertedHTTPSteps(t, dir)
	if len(violations) != 1 {
		t.Fatalf("expected exactly 1 planted violation, got %d: %v", len(violations), violations)
	}
	if !strings.Contains(violations[0], "HXC243-PLANT-001") {
		t.Fatalf("violation does not name the planted case: %q", violations[0])
	}

	// GREEN check: giving the same step an explicit expectation makes
	// the violation disappear — proves the walker isn't just always
	// reporting a hit regardless of content (the opposite bluff).
	content2 := []byte(`name: hxc243-planted-mutation-fixed
test_cases:
  - id: HXC243-PLANT-001
    name: "planted case with an unasserted http step"
    platforms: [api]
    steps:
      - name: "GET something and check nothing"
        action: "http: GET /whatever"
        expect_status: 200
`)
	if err := os.WriteFile(bankPath, content2, 0o644); err != nil {
		t.Fatalf("rewrite planted bank: %v", err)
	}
	violations2, _ := collectUnassertedHTTPSteps(t, dir)
	if len(violations2) != 0 {
		t.Fatalf("expected 0 violations once expect_status is present, got %v", violations2)
	}

	// GREEN check 2: an honest _skip with a reason is ALSO accepted,
	// not just a status/body/json_path assertion.
	content3 := []byte(`name: hxc243-planted-mutation-skipped
test_cases:
  - id: HXC243-PLANT-001
    name: "planted case with an unasserted http step"
    platforms: [api]
    steps:
      - name: "GET something and check nothing"
        action: "http: GET /whatever"
        _skip: true
        _skip_reason: "cannot assert anything meaningful here — reason"
`)
	if err := os.WriteFile(bankPath, content3, 0o644); err != nil {
		t.Fatalf("rewrite planted bank (skip): %v", err)
	}
	violations3, _ := collectUnassertedHTTPSteps(t, dir)
	if len(violations3) != 0 {
		t.Fatalf("expected 0 violations for an honest _skip+reason, got %v", violations3)
	}

	// RED check: a _skip:true with an EMPTY reason is STILL a
	// violation (an unexplained skip is exactly as uninformative as
	// an unasserted pass).
	content4 := []byte(`name: hxc243-planted-mutation-bare-skip
test_cases:
  - id: HXC243-PLANT-001
    name: "planted case with an unasserted http step"
    platforms: [api]
    steps:
      - name: "GET something and check nothing"
        action: "http: GET /whatever"
        _skip: true
`)
	if err := os.WriteFile(bankPath, content4, 0o644); err != nil {
		t.Fatalf("rewrite planted bank (bare skip): %v", err)
	}
	violations4, _ := collectUnassertedHTTPSteps(t, dir)
	if len(violations4) != 1 {
		t.Fatalf("expected exactly 1 violation for a bare _skip with no reason, got %d: %v", len(violations4), violations4)
	}
	if !strings.Contains(violations4[0], "EMPTY _skip_reason") {
		t.Fatalf("violation does not name the empty-skip-reason defect: %q", violations4[0])
	}
}

// TestHXC243_GuardRecursesIntoSubdirectoryBanks is the paired-mutation
// proof for the F-2 remedy specifically: a violation planted in a
// SUBDIRECTORY (not the top level of the scanned dir) must still be
// caught. Before the filepath.WalkDir fix, collectUnassertedHTTPSteps
// used a single os.ReadDir pass with `entry.IsDir() -> continue`, so
// this exact case — an unasserted http: step living one level below
// banks/, matching the real banks/yole-concrete/ and banks/helix_vpn/
// layout — would have been silently invisible to the guard.
func TestHXC243_GuardRecursesIntoSubdirectoryBanks(t *testing.T) {
	dir := t.TempDir()
	subdir := filepath.Join(dir, "some-subdir")
	if err := os.MkdirAll(subdir, 0o755); err != nil {
		t.Fatalf("mkdir subdir: %v", err)
	}
	bankPath := filepath.Join(subdir, "planted_unasserted_nested.yaml")
	content := []byte(`name: hxc243-planted-mutation-nested
test_cases:
  - id: HXC243-PLANT-NESTED-001
    name: "planted case one level below the scanned dir"
    platforms: [api]
    steps:
      - name: "GET something and check nothing, from a subdirectory"
        action: "http: GET /whatever-nested"
`)
	if err := os.WriteFile(bankPath, content, 0o644); err != nil {
		t.Fatalf("write nested planted bank: %v", err)
	}

	violations, filesWalked := collectUnassertedHTTPSteps(t, dir)
	if !filesWalked[bankPath] {
		t.Fatalf(
			"RED: expected the walk to visit the subdirectory bank %q, but "+
				"filesWalked does not contain it — the walker is not recursing, "+
				"which is exactly the F-2 defect (a top-level-only walk against "+
				"a guard whose contract promises \"every bank file under "+
				"banks/\").", bankPath,
		)
	}
	if len(violations) != 1 {
		t.Fatalf("expected exactly 1 violation from the nested bank, got %d: %v", len(violations), violations)
	}
	if !strings.Contains(violations[0], "HXC243-PLANT-NESTED-001") {
		t.Fatalf("violation does not name the nested planted case: %q", violations[0])
	}

	// GREEN check: fixing the nested step makes the violation
	// disappear too — the recursive walk is not just always-hit.
	content2 := []byte(`name: hxc243-planted-mutation-nested-fixed
test_cases:
  - id: HXC243-PLANT-NESTED-001
    name: "planted case one level below the scanned dir"
    platforms: [api]
    steps:
      - name: "GET something and check nothing, from a subdirectory"
        action: "http: GET /whatever-nested"
        expect_status: 200
`)
	if err := os.WriteFile(bankPath, content2, 0o644); err != nil {
		t.Fatalf("rewrite nested planted bank: %v", err)
	}
	violations2, filesWalked2 := collectUnassertedHTTPSteps(t, dir)
	if !filesWalked2[bankPath] {
		t.Fatalf("expected the walk to still visit %q after the fix", bankPath)
	}
	if len(violations2) != 0 {
		t.Fatalf("expected 0 violations once the nested step's expect_status is present, got %v", violations2)
	}
}
