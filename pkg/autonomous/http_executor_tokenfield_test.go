package autonomous

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"digital.vasic.helixqa/pkg/analysis"
	"digital.vasic.helixqa/pkg/testbank"
)

// HXC-239 — login-token extraction against the REAL HelixCode
// login-response shape.
//
// §11.4.115 polarity switch. RED_MODE=1 asserts the DEFECT IS
// PRESENT on the pre-fix artifact; RED_MODE=0 (the standing default,
// see redModeOn) flips the same source into the GREEN regression
// guard asserting the defect is ABSENT. One source, two roles — the
// bug-catcher IS the regression guard.
//
// Captured ground truth (§11.4.6 — measured, not assumed). A real
// successful login against the live `bin/helixcode server` on
// 2026-08-11 returned HTTP 200 with these top-level keys:
//
//	["session", "status", "token", "user"]
//
// with the bearer JWT at TOP-LEVEL `token` (297 chars) and the
// session bookkeeping reference NESTED at `session.session_token`
// (44 chars, opaque, NOT a JWT). Verified against
// GET /api/v1/users/me:
//
//	Bearer <token>                 -> HTTP 200
//	Bearer <session.session_token> -> HTTP 401
//	                                  "token contains an invalid
//	                                   number of segments"
//
// So `session_token` is NOT a top-level key of a HelixCode login
// response at all. The pre-fix extractor did a FLAT top-level map
// lookup for the literal key "session_token", found nothing, and
// failed the whole auth step with:
//
//	login response missing field "session_token"
//
// NOTE this corrects the HXC-239 tracker narrative, which described
// HelixQA picking the session reference and being rejected with
// "Invalid or expired token". That is not the observed mechanism —
// the reference is never reachable, so login fails loudly before any
// authenticated request is sent. The tracker's measured 6-pass /
// 4-fail figure is reproduced exactly; only the stated cause differs.

// redModeOn reports whether the RED polarity is active.
//
// §11.4.115 polarity, with one deliberate and documented deviation:
// the STANDING default here is GREEN (defect-absent), not RED. A Go
// test committed to the standing suite must leave `go test ./...`
// green on the fixed artifact; defaulting to RED would make the
// suite permanently red and destroy its signal. RED is therefore
// opt-in via RED_MODE=1, which is exactly how the pre-fix baseline
// for this item was captured (see the run log in the HXC-239
// evidence: RED_MODE=1 on the pre-fix artifact asserted the defect
// present; the same source at RED_MODE=0 is the standing guard).
func redModeOn() bool {
	v := strings.TrimSpace(os.Getenv("RED_MODE"))
	return v == "1" || strings.EqualFold(v, "true")
}

// helixCodeJWT is a structurally-real 3-segment JWT stand-in. Value
// is synthetic — no captured credential or token is ever committed
// (§11.4.10).
const helixCodeJWT = "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9." +
	"eyJzdWIiOiJoeGMtMjM5LXByb2JlIiwiZXhwIjo0MTAyNDQ0ODAwfQ." +
	"c2lnbmF0dXJlLXBsYWNlaG9sZGVyLW5vdC1hLXJlYWwta2V5"

// helixCodeSessionRef mirrors the shape of the opaque 44-char
// session bookkeeping reference (base64-ish, no dots — decisively
// not a JWT).
const helixCodeSessionRef = "d0FSTkxJTkdub3RhandUb3BhcXVlc2Vzc2lvbnJlZjEyMw"

// helixCodeLoginBody renders the exact captured HelixCode login
// response structure. Every value is synthetic; only the KEY SHAPE
// is reproduced from the capture.
func helixCodeLoginBody() string {
	body := map[string]any{
		"status": "success",
		"token":  helixCodeJWT,
		"user": map[string]any{
			"id":           "00000000-0000-0000-0000-0000000000aa",
			"username":     "hxc239probe",
			"email":        "hxc239probe@qa.invalid",
			"display_name": "HXC-239 probe",
			"is_active":    true,
			"is_verified":  false,
			"mfa_enabled":  false,
		},
		"session": map[string]any{
			"id":            "00000000-0000-0000-0000-0000000000bb",
			"user_id":       "00000000-0000-0000-0000-0000000000aa",
			"session_token": helixCodeSessionRef,
			"client_type":   "rest_api",
			"ip_address":    "127.0.0.1",
			"user_agent":    "Go-http-client/1.1",
		},
	}
	b, err := json.Marshal(body)
	if err != nil {
		panic(err)
	}
	return string(b)
}

// helixCodeServer stands up a server that behaves like HelixCode:
// login returns the captured shape, and the protected endpoint
// accepts ONLY the top-level `token` JWT — exactly as the live
// server's authMiddleware does. seenAuth records what the executor
// actually put on the wire.
func helixCodeServer(t *testing.T, seenAuth *string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/auth/login":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(helixCodeLoginBody()))
		default:
			got := r.Header.Get("Authorization")
			if seenAuth != nil {
				*seenAuth = got
			}
			// Mirror the live server: only the JWT authenticates.
			if got != "Bearer "+helixCodeJWT {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"status":"error",` +
					`"message":"Invalid or expired token",` +
					`"error":"token contains an invalid number of segments"}`))
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"status":"success","workers":[]}`))
		}
	}))
}

// TestHXC239_LoginTokenExtraction_HelixCodeShape is the §11.4.115
// polarity test: RED on the broken artifact, GREEN on the fixed one.
func TestHXC239_LoginTokenExtraction_HelixCodeShape(t *testing.T) {
	var seenAuth string
	srv := helixCodeServer(t, &seenAuth)
	defer srv.Close()

	h := NewHTTPExecutor(srv.URL)
	h.AdminCreds = Credentials{Username: "hxc239probe", Password: "irrelevant"}

	res := h.Execute(context.Background(), "GET", "/api/v1/workers", testbank.TestStep{
		AuthMode:     "admin",
		ExpectStatus: 200,
	})

	if redModeOn() {
		// Defect-present assertion on the PRE-FIX artifact.
		require.False(t, res.Success,
			"RED_MODE=1: expected the pre-fix defect (auth step fails), got success. "+
				"If this passes on a pre-fix artifact the test is blind (§11.4.115).")
		assert.Contains(t, res.Message, `login response missing field "session_token"`,
			"RED_MODE=1: expected the flat top-level lookup to miss the nested field")
		// Characterisation (§11.4.146 STEP 1): login fails BEFORE any
		// authenticated request leaves the client, so the server never
		// sees an Authorization header at all.
		assert.Empty(t, seenAuth,
			"RED_MODE=1: defect characterisation — no authenticated request is ever sent")
		return
	}

	// GREEN guard: defect absent.
	require.True(t, res.Success,
		"RED_MODE=0: expected auth to succeed against the real HelixCode shape, got: %s",
		res.Message)
	assert.Equal(t, "Bearer "+helixCodeJWT, seenAuth,
		"RED_MODE=0: the executor must send the top-level JWT, never the nested session reference")
	assert.NotContains(t, seenAuth, helixCodeSessionRef,
		"RED_MODE=0: the opaque session bookkeeping reference must never be used as the bearer")
}

// TestHXC239_ExtendCaseSet is the §11.4.146 STEP 3 fan-out across
// the full case space of login-token extraction. Enumerated, with a
// per-case expectation — not a single guard.
//
// Every case runs in BOTH polarities: under RED_MODE=1 only the
// cases the pre-fix artifact could already satisfy are asserted
// (documented per-case via preFixOK).
func TestHXC239_ExtendCaseSet(t *testing.T) {
	cases := []struct {
		name string
		// body is the login response the fake server returns.
		body string
		// tokenField, when non-empty, overrides TokenField
		// (the --token-field CLI path).
		tokenField string
		// wantToken is the bearer the executor must extract.
		wantToken string
		// wantErrSubstr, when non-empty, means extraction MUST fail
		// and the message must contain this substring.
		wantErrSubstr string
		// preFixOK marks cases the PRE-FIX artifact already handled
		// correctly (so RED_MODE=1 asserts them too).
		preFixOK bool
	}{
		{
			name:      "helixcode shape: top-level token + nested session.session_token",
			body:      helixCodeLoginBody(),
			wantToken: helixCodeJWT,
		},
		{
			name:      "catalog-api shape: top-level session_token only (must not regress)",
			body:      `{"session_token":"jwt-abc","expires_at":"2026-04-30"}`,
			wantToken: "jwt-abc",
			preFixOK:  true,
		},
		{
			name:      "top-level token only",
			body:      `{"token":"only-token"}`,
			wantToken: "only-token",
		},
		{
			name:      "both top-level: session_token wins (catalog-api precedence preserved)",
			body:      `{"session_token":"sess-wins","token":"jwt-loses"}`,
			wantToken: "sess-wins",
			preFixOK:  true,
		},
		{
			name:      "oauth-style access_token fallback",
			body:      `{"access_token":"oauth-tok","token_type":"bearer"}`,
			wantToken: "oauth-tok",
		},
		{
			name:          "neither present: honest hard error naming every path tried",
			body:          `{"status":"success","user":{"id":"x"}}`,
			wantErrSubstr: "login response missing",
			preFixOK:      true,
		},
		{
			name:          "present but empty string is not a token",
			body:          `{"session_token":"","token":""}`,
			wantErrSubstr: "login response missing",
			preFixOK:      true,
		},
		{
			name:      "empty primary falls through to a populated fallback",
			body:      `{"session_token":"","token":"non-empty"}`,
			wantToken: "non-empty",
		},
		{
			name:          "non-string type is rejected, not coerced",
			body:          `{"session_token":12345,"token":{"nested":"obj"}}`,
			wantErrSubstr: "login response missing",
			preFixOK:      true,
		},
		{
			name:       "explicit --token-field override still honoured",
			body:       `{"session_token":"ignored","custom_tok":"picked"}`,
			tokenField: "custom_tok",
			wantToken:  "picked",
		},
		{
			name:       "explicit --token-field with a DOTTED nested path",
			body:       helixCodeLoginBody(),
			tokenField: "session.session_token",
			wantToken:  helixCodeSessionRef,
		},
		{
			name:          "dotted path into a non-object is an honest miss, not a panic",
			body:          `{"session":"a-string-not-an-object"}`,
			tokenField:    "session.session_token",
			wantErrSubstr: "login response missing",
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			if redModeOn() && !tc.preFixOK {
				t.Skipf("SKIP-OK: #HXC-239 — RED_MODE=1 asserts only pre-fix-satisfiable "+
					"cases; %q is part of the post-fix contract", tc.name)
			}
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()

			h := NewHTTPExecutor(srv.URL)
			if tc.tokenField != "" {
				h.TokenField = tc.tokenField
			}
			got, err := h.login(context.Background(),
				Credentials{Username: "u", Password: "p"})

			if tc.wantErrSubstr != "" {
				require.Error(t, err, "expected extraction to fail for %q", tc.name)
				assert.Contains(t, err.Error(), tc.wantErrSubstr)
				return
			}
			require.NoError(t, err, "expected extraction to succeed for %q", tc.name)
			assert.Equal(t, tc.wantToken, got)
		})
	}
}

// TestHXC239_PipelineLoginExtraction_SecondSite covers the SECOND,
// independent extraction site — validateAPIData in pipeline.go,
// which had its own hardcoded top-level `session_token` struct tag,
// no override, and failed SILENTLY (authToken stayed empty, every
// subsequent request went out unauthenticated, and the resulting
// 401s were reported as API defects).
func TestHXC239_PipelineLoginExtraction_SecondSite(t *testing.T) {
	if redModeOn() {
		t.Skip("SKIP-OK: #HXC-239 — RED_MODE=1; the shared extractor " +
			"this asserts is part of the post-fix contract")
	}
	var decoded map[string]any
	require.NoError(t, json.Unmarshal([]byte(helixCodeLoginBody()), &decoded))

	tok, err := extractLoginToken(decoded, defaultTokenField, defaultTokenFieldFallbacks)
	require.NoError(t, err)
	assert.Equal(t, helixCodeJWT, tok,
		"the pipeline site must resolve the same JWT the executor does")
	assert.NotEqual(t, helixCodeSessionRef, tok)
}

// TestHXC239_TokenCachingHonoursEveryCandidate verifies the
// ExpectJSONPath token-cache convenience keeps working for whichever
// candidate field a bank actually asserts on.
func TestHXC239_TokenCachingHonoursEveryCandidate(t *testing.T) {
	if redModeOn() {
		t.Skip("SKIP-OK: #HXC-239 — RED_MODE=1; multi-candidate caching " +
			"is part of the post-fix contract")
	}
	for _, field := range []string{"session_token", "token", "access_token"} {
		field := field
		t.Run(field, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(fmt.Sprintf(`{%q:"cached-%s"}`, field, field)))
			}))
			defer srv.Close()

			h := NewHTTPExecutor(srv.URL)
			res := h.Execute(context.Background(), "POST", "/api/v1/auth/login",
				testbank.TestStep{ExpectStatus: 200, ExpectJSONPath: "$." + field})
			require.True(t, res.Success, res.Message)

			h.mu.Lock()
			cached := h.tokenCache["__last_login__"]
			h.mu.Unlock()
			assert.Equal(t, "cached-"+field, cached,
				"a bank asserting $.%s must cache its token", field)
		})
	}
}

// pipelineNoBearerFindingTitle is the exact Title validateAPIData
// raises when login returns 200 but no configured candidate yields a
// usable bearer. Held as a constant so a reworded production string
// breaks this guard loudly instead of letting it pass vacuously.
const pipelineNoBearerFindingTitle = "API login returned 200 but no usable bearer token"

// apiCall is one request the pipeline actually put on the wire.
type apiCall struct {
	path string
	auth string
}

// apiCallRecorder captures the wire traffic so the guard asserts on
// OBSERVED behaviour rather than on the pipeline's own log lines
// (§11.4.5 — the log is the claim, the wire is the evidence).
type apiCallRecorder struct {
	mu    sync.Mutex
	calls []apiCall
}

func (r *apiCallRecorder) record(path, auth string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, apiCall{path: path, auth: auth})
}

func (r *apiCallRecorder) snapshot() []apiCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]apiCall(nil), r.calls...)
}

// unusableTokenLoginBody renders a login response that is a genuine
// SUCCESS — HTTP 200, status=success, a real user object — whose
// bearer nonetheless lives under a name no configured candidate
// addresses: `session_token` appears ONLY nested under `session` (the
// opaque non-JWT bookkeeping reference), and neither `token` nor
// `access_token` is present. That is the token-shape-mismatch class
// HXC-239 is about; a malformed or empty body would prove nothing.
// Every value is synthetic (§11.4.10).
func unusableTokenLoginBody() string {
	body := map[string]any{
		"status": "success",
		// The real bearer, under a name the extractor does not know.
		"auth": map[string]any{"bearer": helixCodeJWT},
		"user": map[string]any{
			"id":       "00000000-0000-0000-0000-0000000000cc",
			"username": "hxc239probe",
		},
		"session": map[string]any{
			"session_token": helixCodeSessionRef,
		},
	}
	b, err := json.Marshal(body)
	if err != nil {
		panic(err)
	}
	return string(b)
}

// unusableTokenAPIServer stands up a server that logs in fine but,
// like the live HelixCode authMiddleware, answers 401 to every
// unauthenticated request.
func unusableTokenAPIServer(t *testing.T) (*httptest.Server, *apiCallRecorder) {
	t.Helper()
	rec := &apiCallRecorder{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.record(r.URL.Path, r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPost && r.URL.Path == "/api/v1/auth/login" {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(unusableTokenLoginBody()))
			return
		}
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"status":"error",` +
			`"message":"Invalid or expired token"}`))
	}))
	return srv, rec
}

// TestHXC239_PipelineRaisesFindingOnUnusableToken is the standing
// guard for the FINDING-RAISE in validateAPIData — the half of the
// second extraction site that TestHXC239_PipelineLoginExtraction_SecondSite
// does not reach (that one asserts the shared extractor resolves the
// right token; this one asserts the pipeline REPORTS it when no token
// resolves at all). Without this guard, deleting the
// `findings = append(...)` line leaves the entire pkg/autonomous
// suite green — measured, not assumed — which makes the raise
// decoration rather than a gate (repo CLAUDE.md §6: every gate ships
// with a §1.1 mutation proving it catches the regression it claims).
//
// §11.4.115 polarity, same deviation and rationale as redModeOn
// documents: the STANDING default is GREEN (defect-absent) so
// `go test ./...` stays green on the fixed artifact; RED is opt-in
// via RED_MODE=1 and asserts the pre-fix SILENT failure.
//
// Honest boundary on the RED polarity (§11.4.6): unlike the executor
// test above, this one's RED was verified against the PAIRED MUTANT
// (the finding-raise line deleted — byte-for-byte the pre-fix
// behaviour of this code path), not against a genuine pre-fix
// checkout, because the sibling tests in this file reference
// post-fix symbols (extractLoginToken, defaultTokenField) and so the
// file as a whole does not compile against the pre-fix tree.
func TestHXC239_PipelineRaisesFindingOnUnusableToken(t *testing.T) {
	srv, rec := unusableTokenAPIServer(t)
	defer srv.Close()

	sp := &SessionPipeline{config: dataValidationTestConfig(t, srv.URL)}
	findings := sp.validateAPIData(context.Background())

	var noBearer []analysis.AnalysisFinding
	var downstream401 []string
	for _, f := range findings {
		switch {
		case f.Title == pipelineNoBearerFindingTitle:
			noBearer = append(noBearer, f)
		case strings.Contains(f.Title, "returned 401"):
			downstream401 = append(downstream401, f.Title)
		}
	}

	// Asserted in BOTH polarities, because it is TRUE in both and is
	// exactly what the fix does NOT change: a failed extraction does
	// not stop the pipeline. Every later request still goes out
	// unauthenticated and its 401 is still filed as an API defect.
	// The pipeline.go comment must keep saying only this much.
	calls := rec.snapshot()
	require.NotEmpty(t, calls, "the pipeline must actually hit the server")
	sawLogin := false
	for _, c := range calls {
		if c.path == "/api/v1/auth/login" {
			sawLogin = true
			continue
		}
		assert.Empty(t, c.auth,
			"after a failed extraction the pipeline still calls %s "+
				"UNAUTHENTICATED — if this ever carries a bearer, the "+
				"pipeline.go comment describing the residual behaviour "+
				"is stale", c.path)
	}
	assert.True(t, sawLogin, "login must have been attempted")
	assert.Contains(t, downstream401,
		"API error: entities/stats returned 401",
		"the downstream 401 is still filed as an API defect")
	assert.Contains(t, downstream401,
		"API error: media/search returned 401",
		"the downstream 401 is still filed as an API defect")

	if redModeOn() {
		// Defect-present assertion: extraction failure is SILENT, so
		// the only trace of a token-shape mismatch is a pile of 401s
		// blamed on the API.
		require.Empty(t, noBearer,
			"RED_MODE=1: expected the pre-fix SILENT extraction failure "+
				"(no finding raised), got %d. If this passes on a "+
				"defect-present artifact the guard is blind (§11.4.115).",
			len(noBearer))
		return
	}

	// GREEN guard: the mismatch is surfaced as its own finding.
	require.Len(t, noBearer, 1,
		"expected exactly one %q finding, got %d (findings=%d)",
		pipelineNoBearerFindingTitle, len(noBearer), len(findings))
	f := noBearer[0]
	assert.Equal(t, analysis.CategoryFunctional, f.Category)
	assert.Equal(t, analysis.SeverityHigh, f.Severity)
	assert.Equal(t, "api", f.Platform)

	// Diagnosable (§11.4.6): the description names every candidate
	// tried AND the keys actually present, so the mismatch is fixable
	// from the report alone without reading source.
	wantTried := fmt.Sprintf("%q",
		append([]string{defaultTokenField}, defaultTokenFieldFallbacks...))
	assert.Contains(t, f.Description, wantTried,
		"the finding must name every candidate path tried")
	assert.Contains(t, f.Description,
		fmt.Sprintf("%q", []string{"auth", "session", "status", "user"}),
		"the finding must name the top-level keys the server actually sent")

	// §11.4.10: keys only — no token or credential value may reach a
	// finding that ends up in a committed report.
	assert.NotContains(t, f.Description, helixCodeJWT,
		"a bearer value must never be written into a finding")
	assert.NotContains(t, f.Description, helixCodeSessionRef,
		"a session reference must never be written into a finding")
}
