// SPDX-FileCopyrightText: 2026 Milos Vasic
// SPDX-License-Identifier: Apache-2.0

package autonomous

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"digital.vasic.helixqa/pkg/testbank"
)

// hxc243Check identifies one http: bank step that HXC-243 gave an
// explicit expect_status to, because the check previously declared
// no expectation at all and would report PASS for any HTTP response.
type hxc243Check struct {
	File         string // bank file under banks/
	CaseID       string
	StepName     string
	ExpectStatus int
}

// hxc243AssignedChecks is generated in
// hxc243_falsifiability_checks_generated_test.go.

// hxc243LoginPath is the executor's default login endpoint (see
// NewHTTPExecutor). Every check table row was generated against
// banks that never override -login-path, so this constant is the
// correct login path for every row here.
const hxc243LoginPath = "/api/v1/auth/login"

// hxc243GoldenBadServer stands in for "a service already proven to
// be returning an error for every request" — the exact HXC-243
// demonstrated scenario. When needsAuxLogin is true, the login
// endpoint is left healthy (so the auth PRECURSOR the checked step
// depends on succeeds and the failure we observe is attributable to
// the check's OWN assertion, not to an unrelated auth failure);
// every other path — including the login path itself when
// needsAuxLogin is false, i.e. when the checked step's own target IS
// the login endpoint — answers 500 with an error body.
func hxc243GoldenBadServer(needsAuxLogin bool) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if needsAuxLogin && r.Method == http.MethodPost && r.URL.Path == hxc243LoginPath {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"token":"hxc243-golden-bad-aux-login-token"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"status":"error","message":"HXC-243 golden-bad: simulated broken service — every request answers 500"}`))
	}))
}

// hxc243GoldenGoodServer is the mirror-image self-consistency check:
// when the checked endpoint DOES answer with exactly the status the
// bank now expects, Execute() must report Success. This is the
// opposite-direction guard the ticket does not name explicitly but
// which the same anti-bluff discipline demands — an assertion that
// can NEVER pass would be just as misleading as one that can never
// fail (a permanently-red check that nobody can trust either).
func hxc243GoldenGoodServer(needsAuxLogin bool, targetStatus int) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if needsAuxLogin && r.Method == http.MethodPost && r.URL.Path == hxc243LoginPath {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"token":"hxc243-golden-good-aux-login-token"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(targetStatus)
		_, _ = w.Write([]byte(`{"status":"ok","hxc243":"golden-good","token":"hxc243-golden-good-token"}`))
	}))
}

// findHXC243Step locates the exact step a table row refers to inside
// the CURRENT bank file content (never a cached/stale copy), so this
// test is always exercising what actually ships, not a snapshot of
// what the fix intended.
func findHXC243Step(t *testing.T, chk hxc243Check) *testbank.TestStep {
	t.Helper()
	bankPath := filepath.Join("..", "..", "banks", chk.File)
	bf, err := testbank.LoadFile(bankPath)
	require.NoError(t, err, "load bank file %s", bankPath)

	for i := range bf.TestCases {
		tc := &bf.TestCases[i]
		if tc.ID != chk.CaseID {
			continue
		}
		for j := range tc.Steps {
			st := &tc.Steps[j]
			at, _ := st.ParseAction()
			if at != testbank.ActionTypeHTTP {
				continue
			}
			if st.Name == chk.StepName && st.ExpectStatus == chk.ExpectStatus {
				return st
			}
		}
	}
	return nil
}

// TestHXC243_PreFixStepsWouldHaveBluffPassed is the direct, faithful
// reproduction of the exact scenario HXC-243 demonstrated: "one
// collection was deliberately aimed at the wrong service, one
// already proven to be returning an error for every request, and it
// reported both of its checks as passing."
//
// The two steps below are reconstructed VERBATIM from
// banks/admin-operations.json at git HEAD (i.e. before this fix —
// see `git show HEAD:banks/admin-operations.json`, test cases
// ADM-026 "Request health check" and ADM-029 "Check metrics"): both
// are plain GET /api/v1/health and GET /metrics steps with NO
// expect_status / expect_body_contains / expect_json_path and no
// auth requirement — the two simplest, most natural checks anyone
// would run first to sanity-test whether a service is alive, and
// exactly the shape of check the ticket's own demonstrated scenario
// describes.
//
// This test proves the BEFORE/AFTER pair directly: the pre-fix step
// literals report Success=true against a server that answers every
// request with 500 (the bug, reproduced on demand rather than taken
// on faith); the CURRENT (post-fix, expect_status:200) versions of
// the very same two steps, loaded live from banks/admin-operations.json,
// report Success=false against the identical broken-service stub.
func TestHXC243_PreFixStepsWouldHaveBluffPassed(t *testing.T) {
	preFixSteps := []testbank.TestStep{
		{Name: "Request health check", Action: "http: GET /api/v1/health"},
		{Name: "Check metrics", Action: "http: GET /metrics"},
	}

	badSrv := hxc243GoldenBadServer(false)
	defer badSrv.Close()

	for _, step := range preFixSteps {
		method, path := parseHTTPAction(strings.TrimSpace(strings.TrimPrefix(step.Action, "http:")))
		h := NewHTTPExecutor(badSrv.URL)
		res := h.Execute(context.Background(), method, path, step)
		// Round 8 (review round 7, finding 6) closed the bug CLASS at the
		// executor: an http: step with no expect_status now expects a 2xx,
		// so even the pre-fix step literals FAIL against the broken service.
		assert.False(t, res.Success,
			"pre-fix step literal (no expectation) against a service answering "+
				"every request with 500 must FAIL (default 2xx expectation), got "+
				"Success=true (message=%q)", res.Message)
	}

	for _, cid := range []string{"ADM-026", "ADM-029"} {
		chk := hxc243Check{File: "admin-operations.json", CaseID: cid}
		bf, err := testbank.LoadFile(filepath.Join("..", "..", "banks", chk.File))
		require.NoError(t, err)
		var step *testbank.TestStep
		for i := range bf.TestCases {
			if bf.TestCases[i].ID != cid {
				continue
			}
			for j := range bf.TestCases[i].Steps {
				at, _ := bf.TestCases[i].Steps[j].ParseAction()
				if at == testbank.ActionTypeHTTP {
					step = &bf.TestCases[i].Steps[j]
				}
			}
		}
		require.NotNil(t, step, "current http step not found for %s", cid)
		require.NotZero(t, step.ExpectStatus, "%s should now carry an explicit expect_status", cid)

		method, path := parseHTTPAction(strings.TrimSpace(strings.TrimPrefix(step.Action, "http:")))
		h := NewHTTPExecutor(badSrv.URL)
		res := h.Execute(context.Background(), method, path, *step)
		assert.False(t, res.Success,
			"AFTER (current banks/admin-operations.json, case %s): expected the "+
				"fix to make this check FAIL against the identical broken-service "+
				"stub, but it reported Success=true — the fix did not close the "+
				"reported bug for this check.", cid)
	}
}

// TestHXC243_EveryAssignedCheckIsFalsifiableAndSatisfiable is the
// "half two" proof HXC-243 demands: NOT a sample — every one of the
// 119 http: steps this fix gave an explicit expect_status to is
// driven, through the REAL production HTTPExecutor (the same code
// path `bin/helixqa http` uses), against (a) a golden-bad stub that
// behaves like a service already proven to be returning an error for
// every request — the demonstrated HXC-243 scenario — and MUST
// report FAIL, and (b) a golden-good stub that answers with exactly
// the expected status — MUST report PASS, proving the assertion is
// not itself internally broken / permanently unsatisfiable.
//
// No real provider is contacted and no project service is started or
// restarted (§11.4.101 / operator constraint): both stubs are local
// httptest.Server instances that exist only for the duration of each
// subtest.
func TestHXC243_EveryAssignedCheckIsFalsifiableAndSatisfiable(t *testing.T) {
	require.NotEmpty(t, hxc243AssignedChecks, "the generated check table must not be empty")

	for _, chk := range hxc243AssignedChecks {
		chk := chk
		t.Run(chk.File+"/"+chk.CaseID+"/"+chk.StepName, func(t *testing.T) {
			step := findHXC243Step(t, chk)
			require.NotNil(t, step, "step not found in the CURRENT bank content — the check table is stale relative to banks/%s", chk.File)

			method, path := parseHTTPAction(strings.TrimSpace(strings.TrimPrefix(step.Action, "http:")))
			require.NotEmpty(t, method, "could not parse method from action %q", step.Action)

			// Some of these steps target a path with an unresolved
			// `{id}`-style template placeholder (e.g. GET
			// /api/v1/entities/{id}) that the bank converter left
			// behind. The executor ALREADY refuses to send those as a
			// literal URL — Execute() detects the placeholder and
			// reports Skipped (citing #BLUFF-HELIXQA-BANKS-VAR-SUBST-001)
			// BEFORE it ever reaches the assertion logic this fix
			// added. That is a pre-existing, already-honest harness
			// behaviour (a real, undetected placeholder in a URL would
			// be a worse bug than an honest skip), not something
			// HXC-243 introduced or can silently paper over — so
			// these steps get their OWN proof: both stubs below must
			// agree the step is Skipped (deterministic, not
			// content-dependent), which is the correct thing to prove
			// about a check that does not currently reach the live
			// HTTP layer at all. They are reported separately, never
			// folded into "falsifiable and satisfiable".
			if placeholder := unresolvedPlaceholder(path); placeholder != "" {
				badSrv := hxc243GoldenBadServer(false)
				defer badSrv.Close()
				hBad := NewHTTPExecutor(badSrv.URL)
				resBad := hBad.Execute(context.Background(), method, path, *step)

				goodSrv := hxc243GoldenGoodServer(false, chk.ExpectStatus)
				defer goodSrv.Close()
				hGood := NewHTTPExecutor(goodSrv.URL)
				resGood := hGood.Execute(context.Background(), method, path, *step)

				assert.True(t, resBad.Skipped && resGood.Skipped,
					"PLACEHOLDER-SKIP: expected this step to be honestly Skipped "+
						"(unresolved %s in the path) regardless of server response — "+
						"got badSkipped=%v goodSkipped=%v. If either ran for real "+
						"against a literal %q URL, that is a DIFFERENT, worse bug "+
						"(a real request to an unresolved template path) than the "+
						"one this test set out to prove.",
					placeholder, resBad.Skipped, resGood.Skipped, path)
				t.Logf("PLACEHOLDER-SKIP (not falsifiable via HTTP today): %s — "+
					"expect_status=%d is present and correct but Execute() never "+
					"reaches it because of the unresolved %s placeholder; this is "+
					"the harness's own pre-existing #BLUFF-HELIXQA-BANKS-VAR-SUBST-001 "+
					"limitation, not a regression from this fix. message=%q",
					path, chk.ExpectStatus, placeholder, resBad.Message)
				return
			}

			isLoginAction := method == http.MethodPost && path == hxc243LoginPath
			needsAuxLogin := !isLoginAction && strings.EqualFold(step.AuthMode, "admin")

			// --- golden-bad: MUST FAIL ---
			badSrv := hxc243GoldenBadServer(needsAuxLogin)
			defer badSrv.Close()
			hBad := NewHTTPExecutor(badSrv.URL)
			hBad.AdminCreds = Credentials{Username: "hxc243-golden-bad", Password: "irrelevant"}
			resBad := hBad.Execute(context.Background(), method, path, *step)
			assert.False(t, resBad.Success,
				"GOLDEN-BAD: expected this check to FAIL against a service that "+
					"answers every request with an error, but it reported PASS. "+
					"This reproduces the exact HXC-243 bluff — a check that cannot "+
					"fail carries no information. message=%q", resBad.Message)

			// --- golden-good: MUST PASS (assertion is satisfiable) ---
			goodSrv := hxc243GoldenGoodServer(needsAuxLogin, chk.ExpectStatus)
			defer goodSrv.Close()
			hGood := NewHTTPExecutor(goodSrv.URL)
			hGood.AdminCreds = Credentials{Username: "hxc243-golden-good", Password: "irrelevant"}
			resGood := hGood.Execute(context.Background(), method, path, *step)
			assert.True(t, resGood.Success,
				"GOLDEN-GOOD: expected this check to PASS when the service answers "+
					"with exactly the expected status %d, but it reported FAIL — the "+
					"assertion is internally broken and could never pass for ANY "+
					"response, which is its own anti-bluff defect (a permanently-red "+
					"check is exactly as untrustworthy as a permanently-green one). "+
					"message=%q", chk.ExpectStatus, resGood.Message)
		})
	}
}
