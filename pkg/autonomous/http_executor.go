// SPDX-FileCopyrightText: 2026 Milos Vasic
// SPDX-License-Identifier: Apache-2.0

package autonomous

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"digital.vasic.helixqa/pkg/testbank"
)

// HTTPExecutor performs HTTP requests for ActionTypeHTTP test
// steps and asserts on the response. It is generic — it has no
// knowledge of any specific API surface; the caller supplies a
// BaseURL and per-step TestStep fields specify method, path,
// body, headers, expected status, and JSON-path / body-contains
// assertions.
//
// One HTTPExecutor instance per test session: it caches admin
// session tokens in tokenCache so repeated AuthMode="admin" steps
// don't trigger N login round-trips.
//
// Added 2026-04-29 to close the BLUFF-HELIXQA-BANKS-REWRITE-001
// gap. Before this, HelixQA banks for HTTP-flavoured surfaces
// (full-qa-api.json, full-qa-web.json, atmosphere.json) had to
// use ActionTypeDescription with prose actions like
// "POST /api/v1/auth/login with body {…}" that the executor
// could not run. This executor makes those banks structurally
// executable per Article XI §11.5.
type HTTPExecutor struct {
	// BaseURL is the root URL prepended to every step's path
	// (e.g. http://thinker.local:8092). Required.
	BaseURL string
	// HTTPClient is the underlying *http.Client. Defaults to a
	// 30-second-timeout client if nil.
	HTTPClient *http.Client
	// AdminCreds holds the admin login credentials used by
	// AuthMode="admin" steps. Only populated when at least one
	// step's AuthMode requires login. Empty struct means
	// "admin/admin123" defaults are used.
	AdminCreds Credentials
	// UserCredentials maps username → credentials for AuthMode
	// "as:<user>" steps. Empty by default.
	UserCredentials map[string]Credentials
	// LoginPath is the auth-login endpoint, default
	// "/api/v1/auth/login".
	LoginPath string
	// TokenField is the PRIMARY JSON key in the login response that
	// contains the bearer token, default "session_token" (the
	// catalog-api shape). Dotted paths address nested objects, e.g.
	// "session.session_token".
	TokenField string
	// TokenFieldFallbacks are additional candidate keys/paths tried
	// in order when TokenField is absent or empty. Default
	// {"token", "access_token"}.
	//
	// HXC-239: a single hardcoded field cannot serve every target.
	// catalog-api returns the bearer at top-level `session_token`;
	// HelixCode returns the bearer JWT at top-level `token` and puts
	// an opaque, non-JWT session bookkeeping reference at NESTED
	// `session.session_token` (measured against a live server —
	// the JWT authenticates, the session reference is rejected with
	// "token contains an invalid number of segments"). Because
	// `session_token` is not a top-level key of a HelixCode login
	// response at all, the pre-HXC-239 flat lookup failed every
	// authenticated bank with `login response missing field
	// "session_token"`.
	//
	// The fallback list is strictly additive: `session_token` is
	// still tried FIRST, so catalog-api behaviour is unchanged, and
	// targets that name the bearer differently now work out of the
	// box instead of requiring --token-field.
	TokenFieldFallbacks []string

	// CSRFPreflightPath, when non-empty, is a safe GET endpoint that
	// the executor calls before any mutating request (POST/PUT/PATCH/
	// DELETE) targeting CSRFGuardedPaths. The catalog-api convention
	// (root_middleware/csrf): GET/HEAD/OPTIONS mints a fresh token
	// returned in the X-CSRF-Token header AND a `csrf` cookie; POST/
	// PUT/DELETE then require both to match. Default
	// "/api/v1/admin/system-info" — picks any /admin/* GET because
	// the same guard runs there.
	CSRFPreflightPath string
	// CSRFGuardedPathPrefixes lists request-path prefixes that are
	// behind the CSRF guard. Default {"/api/v1/admin/"}.
	CSRFGuardedPathPrefixes []string
	// CSRFCookieNames is the ordered list of cookie names the guard
	// might use. The first match wins. catalog-api uses
	// `__Host-csrf` (with the __Host- prefix) over HTTPS and the
	// httptest fixtures use bare "csrf" over HTTP, so the default
	// list contains both.
	CSRFCookieNames []string
	// CSRFHeaderName is the request header name expected by the
	// guard. Default "X-CSRF-Token".
	CSRFHeaderName string

	mu           sync.Mutex
	tokenCache   map[string]string // creds-key → bearer token
	lastResponse []byte            // for ActionTypeAssert follow-ups
	lastStatus   int
	lastHeaders  http.Header
	// csrfToken / csrfCookieName / csrfCookieValue carry the most
	// recent CSRF pair from a preflight GET, reused across mutating
	// calls. csrfCookieName preserves whichever of CSRFCookieNames
	// was actually present in the preflight response, so the
	// replay sets exactly the same cookie name the server expects.
	csrfToken       string
	csrfCookieName  string
	csrfCookieValue string
}

// Credentials is a username + password pair.
type Credentials struct {
	Username string
	Password string
}

// NewHTTPExecutor constructs an HTTPExecutor with sensible
// defaults. baseURL is required; admin defaults to admin/admin123
// if zero.
func NewHTTPExecutor(baseURL string) *HTTPExecutor {
	return &HTTPExecutor{
		BaseURL:                 strings.TrimRight(baseURL, "/"),
		HTTPClient:              &http.Client{Timeout: 30 * time.Second},
		LoginPath:               "/api/v1/auth/login",
		TokenField:              defaultTokenField,
		TokenFieldFallbacks:     append([]string(nil), defaultTokenFieldFallbacks...),
		tokenCache:              map[string]string{},
		CSRFPreflightPath:       "/api/v1/admin/system-info",
		CSRFGuardedPathPrefixes: []string{"/api/v1/admin/"},
		// catalog-api's CSRF guard (root_middleware/csrf.go) sets a
		// `__Host-csrf` cookie (the __Host- prefix requires Secure +
		// no Domain attribute, so the browser binds the cookie to
		// the exact origin). The test fixture in
		// http_executor_test.go uses the bare "csrf" name because
		// httptest.NewServer is HTTP — `__Host-` cookies are
		// rejected over HTTP. Both must be matched, so try the
		// fully-prefixed name first and fall back to "csrf".
		CSRFCookieNames: []string{"__Host-csrf", "csrf"},
		CSRFHeaderName:  "X-CSRF-Token",
	}
}

// Execute runs an ActionTypeHTTP step against BaseURL and applies
// any expectStatus / expectJSONPath / expectBodyContains
// assertions declared on the step. Returns ActionResult so the
// dispatch in performAction can use it the same way other
// executors do.
//
// The caller is responsible for parsing the action value
// ("METHOD PATH") via testbank.TestStep.ParseAction(); this method
// just consumes the (method, path, step) trio.
func (h *HTTPExecutor) Execute(
	ctx context.Context,
	method, path string,
	step testbank.TestStep,
) ActionResult {
	if h.BaseURL == "" {
		return ActionResult{Success: false, Message: "http: BaseURL not configured (set HELIXQA_HTTP_BASE_URL)"}
	}
	// Article XI §11.5: explicit step-level skip honored first.
	// A bank entry can declare _skip: true with _skip_reason to
	// document a deliberate non-execution (destructive operation
	// on shared infrastructure, missing fixture, converter
	// limitation, etc.). This is strictly more honest than letting
	// the request go out and producing a confusing PASS/FAIL.
	if step.Skip {
		reason := step.SkipReason
		if reason == "" {
			reason = "step marked _skip without reason — treat as SKIP-OK: #UNTRIAGED"
		}
		return ActionResult{
			Skipped: true,
			Message: fmt.Sprintf("http: step skipped — SKIP-OK: %s", reason),
		}
	}
	// Article XI §11.5: detect unresolved `{var}` placeholders left
	// over from the bank converter. The bank entry's prose describes
	// "GET /scans/{job_id}" expecting the converter to substitute
	// {job_id} from a prior step's response, but the runtime doesn't
	// yet support response capture / template expansion. Marking
	// these SKIPPED (with explicit reason) is honest — the test
	// can't run yet and a FAIL would be a bluff because the
	// catalog-api isn't actually broken; the harness just lacks the
	// feature.
	if placeholder := unresolvedPlaceholder(path); placeholder != "" {
		return ActionResult{
			Skipped: true,
			Message: fmt.Sprintf("http: unresolved placeholder %s in path — SKIP-OK: #BLUFF-HELIXQA-BANKS-VAR-SUBST-001 (executor lacks response capture / variable expansion; bank converter must hardcode an ID or runtime must implement extract:/template support)", placeholder),
		}
	}
	method = strings.ToUpper(strings.TrimSpace(method))
	if method == "" {
		return ActionResult{Success: false, Message: "http: method missing (use 'http: POST /path' format)"}
	}
	path = strings.TrimSpace(path)
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	url := h.BaseURL + path

	// Build body
	var bodyReader io.Reader
	contentType := ""
	if step.Body != nil {
		switch v := step.Body.(type) {
		case string:
			bodyReader = strings.NewReader(v)
		case []byte:
			bodyReader = bytes.NewReader(v)
		default:
			b, err := json.Marshal(v)
			if err != nil {
				return ActionResult{Success: false, Message: fmt.Sprintf("http: body marshal failed: %v", err)}
			}
			bodyReader = bytes.NewReader(b)
			contentType = "application/json"
		}
	}

	req, err := http.NewRequestWithContext(ctx, method, url, bodyReader)
	if err != nil {
		return ActionResult{Success: false, Message: fmt.Sprintf("http: build request failed: %v", err)}
	}
	if contentType != "" && req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", contentType)
	}
	if req.Header.Get("Accept") == "" {
		req.Header.Set("Accept", "application/json")
	}
	for k, v := range step.Headers {
		req.Header.Set(k, v)
	}

	// Auth
	if err := h.applyAuth(ctx, req, step.AuthMode); err != nil {
		return ActionResult{Success: false, Message: fmt.Sprintf("http: auth failed: %v", err)}
	}

	// Article XI §11.5: catalog-api's admin group sits behind a
	// double-submit-cookie CSRF guard (root_middleware/csrf.go). For
	// any mutating method targeting a CSRF-guarded prefix, do a
	// preflight GET to mint a token, capture cookie + header, and
	// replay both on the real call. Without this, every admin POST/
	// PUT/DELETE in the bank fails with 403 "missing csrf cookie".
	// Caught by FQA-API-047, FQA-API-243, FQA-API-252, FQA-API-253.
	if h.needsCSRF(method, path) {
		if err := h.ensureCSRF(ctx, step.AuthMode); err != nil {
			return ActionResult{Success: false, Message: fmt.Sprintf("http: csrf preflight failed: %v", err)}
		}
		h.mu.Lock()
		tok, ckName, ckVal := h.csrfToken, h.csrfCookieName, h.csrfCookieValue
		h.mu.Unlock()
		if tok != "" {
			req.Header.Set(h.CSRFHeaderName, tok)
		}
		if ckName != "" && ckVal != "" {
			req.AddCookie(&http.Cookie{Name: ckName, Value: ckVal})
		}
	}

	// Execute
	resp, err := h.HTTPClient.Do(req)
	if err != nil {
		return ActionResult{Success: false, Message: fmt.Sprintf("http: request failed: %v", err)}
	}
	body, readErr := io.ReadAll(resp.Body)
	resp.Body.Close()
	if readErr != nil {
		// A body cut short cannot prove anything about what it did not
		// contain: a negative assertion over a truncated body would pass
		// even if the forbidden text was in the unread part.
		return ActionResult{Success: false, Message: fmt.Sprintf(
			"http: %s %s → reading the response body failed after %d byte(s): %v",
			method, path, len(body), readErr)}
	}

	// Article XI §11.5: 401 on a request that USED a cached
	// bearer token usually means the cached token was invalidated
	// (e.g. by a previous /auth/logout step in the bank, or
	// catalog-api session expiry). Evict the cache entry and
	// retry the request once with a fresh login. Without this,
	// every admin: call after FQA-API-007 (logout) silently fails
	// with 401 — the bluff scanner would file 14 "phantom"
	// catalog-api defects when the truth is just a stale cache.
	if resp.StatusCode == http.StatusUnauthorized && step.AuthMode != "" &&
		!strings.EqualFold(step.AuthMode, "none") &&
		!strings.HasPrefix(step.AuthMode, "raw:") {
		h.invalidateCachedToken(step.AuthMode)
		// Rebuild the request body reader (the original was consumed).
		var retryBody io.Reader
		if step.Body != nil {
			switch v := step.Body.(type) {
			case string:
				retryBody = strings.NewReader(v)
			case []byte:
				retryBody = bytes.NewReader(v)
			default:
				if b, err := json.Marshal(v); err == nil {
					retryBody = bytes.NewReader(b)
				}
			}
		}
		retryReq, retryErr := http.NewRequestWithContext(ctx, method, url, retryBody)
		if retryErr == nil {
			if contentType != "" {
				retryReq.Header.Set("Content-Type", contentType)
			}
			retryReq.Header.Set("Accept", "application/json")
			for k, v := range step.Headers {
				retryReq.Header.Set(k, v)
			}
			if err := h.applyAuth(ctx, retryReq, step.AuthMode); err == nil {
				if h.needsCSRF(method, path) {
					h.mu.Lock()
					tok, ckName, ckVal := h.csrfToken, h.csrfCookieName, h.csrfCookieValue
					h.mu.Unlock()
					if tok != "" {
						retryReq.Header.Set(h.CSRFHeaderName, tok)
					}
					if ckName != "" && ckVal != "" {
						retryReq.AddCookie(&http.Cookie{Name: ckName, Value: ckVal})
					}
				}
				if retryResp, retryErr := h.HTTPClient.Do(retryReq); retryErr == nil {
					retryBytes, retryReadErr := io.ReadAll(retryResp.Body)
					retryResp.Body.Close()
					if retryReadErr != nil {
						return ActionResult{Success: false, Message: fmt.Sprintf(
							"http: %s %s → reading the retried response body failed after %d byte(s): %v",
							method, path, len(retryBytes), retryReadErr)}
					}
					resp = retryResp
					body = retryBytes
				}
			}
		}
	}

	h.mu.Lock()
	h.lastResponse = body
	h.lastStatus = resp.StatusCode
	h.lastHeaders = resp.Header
	h.mu.Unlock()

	// Assertions
	if step.ExpectStatus != 0 && resp.StatusCode != step.ExpectStatus {
		return ActionResult{
			Success: false,
			Message: fmt.Sprintf("http: %s %s → status %d, expected %d (body: %s)",
				method, path, resp.StatusCode, step.ExpectStatus, truncateOutput(body, 200)),
		}
	}
	if step.ExpectBodyContains != "" && !strings.Contains(string(body), step.ExpectBodyContains) {
		return ActionResult{
			Success: false,
			Message: fmt.Sprintf("http: response body missing %q (body: %s)",
				step.ExpectBodyContains, truncateOutput(body, 200)),
		}
	}
	if step.ExpectBodyNotContains != nil {
		if *step.ExpectBodyNotContains == "" {
			return ActionResult{
				Success: false,
				Message: "http: expect_body_not_contains is empty — it would match every body, so the assertion is vacuous",
			}
		}
		if strings.Contains(string(body), *step.ExpectBodyNotContains) {
			return ActionResult{
				Success: false,
				Message: fmt.Sprintf("http: response body must not contain %q but does (body: %s)",
					*step.ExpectBodyNotContains, truncateOutput(body, 200)),
			}
		}
	}
	if step.ExpectJSONPath != "" {
		ok, val, err := jsonPathExists(body, step.ExpectJSONPath)
		if err != nil {
			return ActionResult{Success: false, Message: fmt.Sprintf("http: json_path %q parse error: %v", step.ExpectJSONPath, err)}
		}
		if !ok {
			return ActionResult{Success: false, Message: fmt.Sprintf("http: json_path %q not found in response", step.ExpectJSONPath)}
		}
		// Cache token if the asserted path names ANY configured token
		// candidate — convenience for chained tests. HXC-239: this
		// used to match only the single TokenField, so a bank
		// asserting $.token against a server whose bearer is named
		// `token` silently cached nothing.
		if h.isTokenJSONPath(step.ExpectJSONPath) {
			if s, ok2 := val.(string); ok2 && s != "" {
				h.mu.Lock()
				h.tokenCache["__last_login__"] = s
				h.mu.Unlock()
			}
		}
	}

	return ActionResult{
		Success: true,
		Message: fmt.Sprintf("http: %s %s → %d (%dB)", method, path, resp.StatusCode, len(body)),
	}
}

// LastResponse returns the most recent response captured by
// Execute, for chained assertions or debugging. Safe for
// concurrent use.
func (h *HTTPExecutor) LastResponse() (status int, headers http.Header, body []byte) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.lastStatus, h.lastHeaders, h.lastResponse
}

func (h *HTTPExecutor) applyAuth(ctx context.Context, req *http.Request, mode string) error {
	mode = strings.TrimSpace(mode)
	if mode == "" || strings.EqualFold(mode, "none") {
		return nil
	}
	if strings.HasPrefix(mode, "raw:") {
		req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(mode[len("raw:"):]))
		return nil
	}

	creds, credsKey, err := h.resolveCreds(mode)
	if err != nil {
		return err
	}

	h.mu.Lock()
	cached, ok := h.tokenCache[credsKey]
	h.mu.Unlock()
	if ok && cached != "" {
		req.Header.Set("Authorization", "Bearer "+cached)
		return nil
	}

	tok, err := h.login(ctx, creds)
	if err != nil {
		return err
	}
	h.mu.Lock()
	h.tokenCache[credsKey] = tok
	h.mu.Unlock()
	req.Header.Set("Authorization", "Bearer "+tok)
	return nil
}

// resolveCreds maps an AuthMode string ("admin" / "as:<user>") to a
// (Credentials, cache-key) pair. Extracted so applyAuth and the
// 401-retry path in Execute share one source of truth.
func (h *HTTPExecutor) resolveCreds(mode string) (Credentials, string, error) {
	credsKey := mode
	switch {
	case strings.EqualFold(mode, "admin"):
		creds := h.AdminCreds
		if creds.Username == "" {
			creds = Credentials{Username: "admin", Password: "admin123"}
		}
		return creds, credsKey, nil
	case strings.HasPrefix(mode, "as:"):
		user := strings.TrimSpace(mode[len("as:"):])
		creds, ok := h.UserCredentials[user]
		if !ok {
			return Credentials{}, "", fmt.Errorf("auth as:%s — credentials not registered", user)
		}
		return creds, credsKey, nil
	}
	return Credentials{}, "", fmt.Errorf("unknown AuthMode %q (expected: none|admin|as:<user>|raw:<token>)", mode)
}

// invalidateCachedToken evicts the cached bearer for the given
// AuthMode. Used after a 401 on a cached-token request — the
// catalog-api invalidated the session (e.g. /auth/logout was just
// called) and the cache entry is now dead. The next applyAuth call
// will re-login. Article XI §11.5: silently keeping a stale token
// in the cache causes ~all subsequent admin: requests to fail with
// 401, which a reviewer would mistake for a catalog-api defect
// instead of an executor cache-staleness bug.
func (h *HTTPExecutor) invalidateCachedToken(mode string) {
	mode = strings.TrimSpace(mode)
	if mode == "" || strings.EqualFold(mode, "none") || strings.HasPrefix(mode, "raw:") {
		return
	}
	h.mu.Lock()
	delete(h.tokenCache, mode)
	h.mu.Unlock()
}

// HXC-239 — login-token field resolution.
//
// defaultTokenField stays "session_token" so the catalog-api banks
// (which really do return the bearer at that top-level key) are
// untouched. defaultTokenFieldFallbacks adds the other names real
// servers use, tried only when the primary is absent or empty.
const defaultTokenField = "session_token"

var defaultTokenFieldFallbacks = []string{"token", "access_token"}

// lookupJSONString resolves a dotted path ("a.b.c") against a
// decoded JSON object and returns the value only when it is a
// NON-EMPTY STRING. A missing key, a traversal through a non-object,
// a non-string leaf, and an empty string are all reported as "not
// found" — never coerced into a token (§11.4.6: a token we cannot
// positively identify is not a token).
func lookupJSONString(decoded map[string]any, path string) (string, bool) {
	path = strings.TrimSpace(path)
	if path == "" || decoded == nil {
		return "", false
	}
	// A literal top-level key wins over dotted interpretation, so a
	// server that genuinely names a key "a.b" still resolves.
	if v, ok := decoded[path]; ok {
		if s, isStr := v.(string); isStr && s != "" {
			return s, true
		}
		if !strings.Contains(path, ".") {
			return "", false
		}
	}
	segments := strings.Split(path, ".")
	var cur any = decoded
	for _, seg := range segments {
		obj, ok := cur.(map[string]any)
		if !ok {
			return "", false
		}
		cur, ok = obj[seg]
		if !ok {
			return "", false
		}
	}
	s, ok := cur.(string)
	if !ok || s == "" {
		return "", false
	}
	return s, true
}

// extractLoginToken pulls the bearer token out of a decoded login
// response, trying primary first and then each fallback in order.
//
// The error names EVERY path tried, so a mismatch against a new
// target is diagnosable from the failure message alone rather than
// requiring a source read.
func extractLoginToken(
	decoded map[string]any,
	primary string,
	fallbacks []string,
) (string, error) {
	tried := make([]string, 0, len(fallbacks)+1)
	for _, candidate := range append([]string{primary}, fallbacks...) {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			continue
		}
		// Preserve order while skipping duplicates so the error
		// message reads cleanly when primary repeats a fallback.
		dup := false
		for _, seen := range tried {
			if seen == candidate {
				dup = true
				break
			}
		}
		if dup {
			continue
		}
		tried = append(tried, candidate)
		if tok, ok := lookupJSONString(decoded, candidate); ok {
			return tok, nil
		}
	}
	return "", fmt.Errorf(
		missingTokenFieldFormat, tried, boundedKeyCensus(decoded))
}

// missingTokenFieldFormat is the error raised when a 200 login reply
// decodes into an object that carries no recognisable bearer under
// any configured candidate — the third of the three ways this
// function fails. Held as a package constant so the guard can pin it
// against a test-local copy rather than by calling the code under
// test, which would agree with any mutation of it (§1.1).
const missingTokenFieldFormat = "login response missing token field — " +
	"tried %q (top-level keys present: %q)"

// loginReplyKeyCensusMax bounds HOW MANY reply keys are listed, and
// loginReplyKeyMax bounds each listed key's rendered length.
//
// HXC-278: the census had NO bound in either dimension. Both numbers
// exist for the reason undecodableBodyContentTypeMax exists ten
// definitions away — a pathological reply must not be able to bloat a
// committed report — and loginReplyKeyMax deliberately carries the
// SAME value, so this package has one number for "how much
// server-chosen text may enter a report" rather than two that drift.
//
// The count is 12 against a measured baseline: the HXC-239 capture of
// a real HelixCode login response has FOUR top-level keys
// (["session", "status", "token", "user"]), so 12 is three times the
// observed width — a verbose but honest reply is reported in full,
// and only a reply far outside that range is summarised.
const (
	loginReplyKeyCensusMax = 12
	loginReplyKeyMax       = undecodableBodyContentTypeMax
)

// loginReplyKeyCensusTruncFormat is the honest tail appended when the
// census omitted keys, so a reader never mistakes a bounded list for
// the whole object. Pinned test-locally alongside the format above.
const loginReplyKeyCensusTruncFormat = "…and %d more of %d"

// credentialShapedKeyPlaceholder stands in for a key whose NAME looks
// like credential material. Fixed text, carrying no byte of the
// original (§11.4.10).
const credentialShapedKeyPlaceholder = "<redacted:credential-shaped>"

// credentialShapedKeyMinLen is the length below which a RUN of
// credential-alphabet bytes is never treated as an opaque credential
// blob.
//
// It reads against the run rather than the whole key (see
// looksCredentialShaped, round-1 review F2). That is the stricter of
// the two readings: a key shorter than this still cannot contain a run
// this long, so nothing that was masked before is unmasked now, while
// a long key that merely wraps a credential in prose no longer
// shelters it.
//
// Measured, not guessed (§11.4.6): the longest field name in the
// HXC-239 capture of a real login response is 13 bytes
// ("session_token"), so 32 leaves better than 2x headroom over the
// longest name a real server was observed to use.
const credentialShapedKeyMinLen = 32

// boundedKeyCensus renders a decoded reply's top-level key names for
// the diagnostic in extractLoginToken, bounded in count and in
// per-key length, with anything credential-SHAPED masked.
//
// HXC-278 — why "keys only, never values" was not enough here. That
// rationale (HXC-239's, on sortedKeys) holds for a cooperating peer:
// a field name describes data rather than carrying it. It does not
// hold on THIS route. The keys come from whatever server actually
// answered, and a 200 that decodes but carries no recognisable token
// is precisely the wrong-service signature — the one failure mode
// where "some other system replied" is the leading hypothesis, so it
// is the worst place to trust the peer's choice of names. A JSON
// object may be keyed by anything, including the credential itself:
// `{"<jwt>": {…}}` is an ordinary map-keyed-by-token shape whose key
// IS the token.
//
// Deliberately NOT reduced to silence: an operator debugging a failed
// sign-in needs to know what the reply did contain, and reporting
// nothing would fail in the other direction just as HXC-270 records
// for the failure-path description. Both directions are guarded.
func boundedKeyCensus(decoded map[string]any) []string {
	keys := sortedKeys(decoded)
	shown := keys
	if len(shown) > loginReplyKeyCensusMax {
		shown = shown[:loginReplyKeyCensusMax]
	}
	census := make([]string, 0, len(shown)+1)
	for _, k := range shown {
		census = append(census, boundedKeyName(k))
	}
	if len(keys) > len(shown) {
		census = append(census, fmt.Sprintf(
			loginReplyKeyCensusTruncFormat,
			len(keys)-len(shown), len(keys),
		))
	}
	return census
}

// boundedKeyName masks a credential-shaped key and clips whatever
// survives to loginReplyKeyMax bytes.
//
// The clip is by BYTES and can split a multi-byte rune, exactly as
// boundedContentType's is: harmless here for the same reason, since
// the caller renders the result with %q, which escapes an invalid
// trailing fragment rather than emitting it raw.
//
// boundedContentType is not reused despite the identical clip: its
// empty-input answer ("<none>") is wrong for a key, and its comment
// records that HXC-267 pinned its output byte for byte, so widening
// it would put that fix's pinned descriptions at risk for no gain.
func boundedKeyName(key string) string {
	if looksCredentialShaped(key) {
		return credentialShapedKeyPlaceholder
	}
	if len(key) > loginReplyKeyMax {
		return key[:loginReplyKeyMax] + "…"
	}
	return key
}

// looksCredentialShaped reports whether a string CONTAINS credential
// material rather than being an ordinary field name.
//
// The shapes are the ones a credential actually takes when it lands
// in a key position: a JWT, an Authorization header value copied
// verbatim, a PEM block, a long opaque base64/base32/base64url blob
// such as a session reference or an API key, and a long hex digest.
//
// WHAT IT KEYS ON. Two whole-string rules — an Authorization value and
// a PEM header — then a scan of every maximal RUN of credential-
// alphabet bytes (see credentialRuns), each judged by
// runIsCredentialShaped. Scanning runs rather than the whole string is
// round-1 review finding F2: the bearer and PEM rules were already
// substring matches while the encoded-blob rules were whole-string, so
// a credential with any prose around it — `"note <jwt>"` — evaded
// every one of the latter and emitted up to loginReplyKeyMax of its
// bytes. The length floor now applies to the RUN, which is the
// stricter reading of the same threshold: a short key still cannot
// contain a long run, while a long prose key no longer shelters one.
//
// HONEST BOUNDARY (§11.4.6). This is a heuristic. It is biased toward
// masking, and unlike the first revision of this comment that claim is
// now true in both directions rather than asserted while the encoded-
// blob rules leaked. Stated precisely, in both directions:
//
// It DELIBERATELY DOES NOT CATCH: any credential whose every run is
// shorter than credentialShapedKeyMinLen, because ordinary field names
// live at those lengths and masking them would destroy the diagnostic
// this census exists to provide; an unbroken run carrying only ONE
// character class OUTSIDE THE HEX ALPHABET, such as an all-letter
// base32 draw that happened to contain no digit, which is
// indistinguishable from a long identifier (a single-class run
// confined to hex digits — all-digit, or all-letter within a-f/A-F —
// is NOT this gap: the hex rule below exists precisely to catch it,
// round-2 review finding F-R2-1); and anything rendered in an
// alphabet outside base64/base32/hex, or broken up so that no
// surviving run reaches the floor.
//
// It DELIBERATELY OVER-CATCHES: a name of at least
// credentialShapedKeyMinLen bytes mixing three of the four character
// classes — "OAuth2AccessTokenExpiresInSeconds", say — which is
// indistinguishable from an opaque token by shape alone; a dotted path
// of that length with three or more segments, which the JWT rule reads
// as a JWT; and the prose around an embedded credential, since a key
// that contains one is replaced WHOLE rather than in part, so no
// reader is invited to reconstruct what sat between the surviving
// fragments. Each costs one name out of a census that still reports
// every other key and still says a redaction happened; the opposite
// error puts a credential in a durable record.
//
// There is no first-party credential-shape detector in this repository
// to extend (§11.4.74: the only redactor, pkg/llm's
// redactKeyFromError, masks a literal the caller already knows, which
// is exactly what we do not have here), so this is written rather than
// reused, and it is self-validated by golden-good/golden-bad fixtures
// per §11.4.107(10) so the detector itself cannot bluff.
func looksCredentialShaped(s string) bool {
	// An Authorization header value used as a key. Requires at least
	// one byte after the scheme, so the bare word cannot trip it.
	if i := strings.Index(strings.ToLower(s), "bearer "); i >= 0 &&
		len(s) > i+len("bearer ") {
		return true
	}
	// A PEM block — a private key pasted somewhere it should not be.
	if strings.Contains(s, "-----BEGIN") {
		return true
	}
	for _, run := range credentialRuns(s) {
		if runIsCredentialShaped(run) {
			return true
		}
	}
	return false
}

// credentialRuns splits s into the maximal runs of bytes that could
// belong to an encoded credential: the base64 / base64url alphabet
// plus '.', which is included so a JWT's dot-separated segments stay
// in ONE run instead of being cut into three sub-threshold pieces.
//
// Everything else — whitespace, punctuation, non-ASCII — is a
// boundary, which is what lets an embedded credential be found
// (round-1 review F2) without the surrounding prose diluting the shape
// tests applied to the run itself.
func credentialRuns(s string) []string {
	var runs []string
	start := -1
	for i := 0; i < len(s); i++ {
		if isCredentialRunByte(s[i]) {
			if start < 0 {
				start = i
			}
			continue
		}
		if start >= 0 {
			runs = append(runs, s[start:i])
			start = -1
		}
	}
	if start >= 0 {
		runs = append(runs, s[start:])
	}
	return runs
}

// isCredentialRunByte reports whether c may appear inside an encoded
// credential — the isCredentialAlphabet set plus the JWT separator.
//
// Defined in terms of isCredentialAlphabetByte rather than repeating
// the alphabet, so the run scanner and the whole-string test cannot
// drift apart.
func isCredentialRunByte(c byte) bool {
	return isCredentialAlphabetByte(c) || c == '.'
}

// runIsCredentialShaped judges ONE maximal run from credentialRuns.
func runIsCredentialShaped(run string) bool {
	if len(run) < credentialShapedKeyMinLen {
		return false
	}
	// A JWT: three or more non-empty base64url segments. Checked
	// before the unbroken-blob rules because the dots disqualify it
	// from all of them.
	if segments := strings.Split(run, "."); len(segments) >= 3 {
		jwt := true
		for _, seg := range segments {
			if seg == "" || !isCredentialAlphabet(seg) {
				jwt = false
				break
			}
		}
		if jwt {
			return true
		}
	}
	// Every remaining rule describes an UNBROKEN blob, so a run still
	// carrying a dot is not one of them.
	if !isCredentialAlphabet(run) {
		return false
	}
	// A long hex digest. Kept separate from the class-count rule
	// because an all-letter hex draw ("abcdef…") is a single class and
	// would slip both of the rules below.
	if isHexRun(run) {
		return true
	}
	// A long opaque blob mixing three of the four classes: the
	// base64/base64url shape, whose padding and URL-safe substitutions
	// put it over the count.
	if charClassCount(run) >= 3 {
		return true
	}
	// A long opaque blob of exactly TWO classes — one letter case plus
	// digits, unbroken.
	//
	// Round-1 review finding F1. This rule was absent, and the
	// three-class rule above was justified by "a token mixes case with
	// digits". That premise is FALSE: base32 is A-Z plus 2-7, one
	// letter case and digits, and base32 is what TOTP secrets and many
	// API keys are rendered in — so a base32 blob counted two classes,
	// declined every rule, and came back out of the census verbatim.
	// The reviewer demonstrated it end to end.
	//
	// Requiring the run to be UNBROKEN — no padding, no separator — is
	// what keeps ordinary names out: snake_case is lowercase plus '_'
	// and CONSTANT_CASE is uppercase plus '_', so both carry a
	// separator byte and neither qualifies however long it grows.
	return isSingleCaseDigitRun(run)
}

// isSingleCaseDigitRun reports whether s is letters of exactly ONE
// case plus digits, with both present and no separator or padding —
// the base32 / bare-alphanumeric-token shape.
//
// Both halves are load-bearing. Requiring a digit keeps a long
// all-letter identifier (an unbroken run of one class) reportable;
// requiring exactly one case leaves the mixed-case blob to the
// three-class rule above, which already answers it.
func isSingleCaseDigitRun(s string) bool {
	var lower, upper, digit bool
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z':
			lower = true
		case c >= 'A' && c <= 'Z':
			upper = true
		case c >= '0' && c <= '9':
			digit = true
		default:
			// Padding or a separator: not an unbroken run.
			return false
		}
	}
	return digit && lower != upper
}

// isCredentialAlphabet reports whether every byte of s is drawn from
// the base64 / base64url alphabet, including both padding and the
// URL-safe substitutions.
func isCredentialAlphabet(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if !isCredentialAlphabetByte(s[i]) {
			return false
		}
	}
	return true
}

// isCredentialAlphabetByte is the per-byte half of
// isCredentialAlphabet, factored out so isCredentialRunByte can extend
// the same set by one character without restating it.
func isCredentialAlphabetByte(c byte) bool {
	switch {
	case c >= 'a' && c <= 'z',
		c >= 'A' && c <= 'Z',
		c >= '0' && c <= '9',
		c == '+', c == '/', c == '=', c == '_', c == '-':
		return true
	}
	return false
}

// isHexRun reports whether every byte of s is a hex digit.
func isHexRun(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= '0' && c <= '9',
			c >= 'a' && c <= 'f',
			c >= 'A' && c <= 'F':
		default:
			return false
		}
	}
	return true
}

// charClassCount counts how many of {lowercase, uppercase, digit,
// base64 symbol} appear in s.
func charClassCount(s string) int {
	var lower, upper, digit, symbol bool
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z':
			lower = true
		case c >= 'A' && c <= 'Z':
			upper = true
		case c >= '0' && c <= '9':
			digit = true
		case c == '+', c == '/', c == '=', c == '_', c == '-':
			symbol = true
		}
	}
	n := 0
	for _, present := range []bool{lower, upper, digit, symbol} {
		if present {
			n++
		}
	}
	return n
}

// sortedKeys lists a decoded object's top-level keys in deterministic
// order (§11.4.50). Keys only — never values.
//
// HXC-278 note: "keys only" is necessary but NOT sufficient, and this
// function is no longer the diagnostic's last step. Every caller must
// go through boundedKeyCensus, which bounds and masks what this
// returns; see that function for why a key name is not automatically
// safe to report.
func sortedKeys(decoded map[string]any) []string {
	keys := make([]string, 0, len(decoded))
	for k := range decoded {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// isTokenJSONPath reports whether an ExpectJSONPath addresses one of
// the configured token candidates.
func (h *HTTPExecutor) isTokenJSONPath(jsonPath string) bool {
	if jsonPath == "" {
		return false
	}
	for _, candidate := range append([]string{h.TokenField}, h.TokenFieldFallbacks...) {
		if candidate != "" && jsonPath == "$."+candidate {
			return true
		}
	}
	return false
}

func (h *HTTPExecutor) login(ctx context.Context, creds Credentials) (string, error) {
	return h.loginWithRetry(ctx, creds, 0)
}

// loginWithRetry performs a login, honoring catalog-api's
// rate-limiter Retry-After header on 429 responses.
//
// Article XI §11.5: Without this, sequential bank verification
// runs hit the login rate limit, the cached admin token gets
// invalidated mid-suite (e.g. by a logout step), the auto-refresh
// path issues a fresh login that gets 429'd, and ALL subsequent
// admin: tests cascade-fail. The 60-second wait is bounded by
// MaxRetryAfter so a misbehaving rate-limiter can't stall a test
// run indefinitely.
//
// Retry depth caps at 1 — a single retry is enough to wait out
// a typical 60-second window. Anything more is a sign the test
// is firing logins faster than the rate limit allows, which is a
// bank-design issue.
func (h *HTTPExecutor) loginWithRetry(ctx context.Context, creds Credentials, depth int) (string, error) {
	const maxRetryAfter = 65 * time.Second
	body, err := json.Marshal(map[string]string{
		"username": creds.Username,
		"password": creds.Password,
	})
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, h.BaseURL+h.LoginPath, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := h.HTTPClient.Do(req)
	if err != nil {
		return "", err
	}
	body2, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	if resp.StatusCode == http.StatusTooManyRequests && depth == 0 {
		retryAfter := parseRetryAfter(resp, body2)
		if retryAfter > maxRetryAfter {
			retryAfter = maxRetryAfter
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(retryAfter):
		}
		return h.loginWithRetry(ctx, creds, depth+1)
	}

	// HXC-270 sites 6 and 7 of 7 — the two in the shared HTTP helper,
	// and the ones that PROPAGATE: this error travels up through
	// applyAuth into an ActionResult message, so a body echoed here
	// is copied outward by every caller on the chain.
	//
	// Site 6 used to be `body=%s` with truncateOutput(body2, 200):
	// 200 bytes of a failed SIGN-IN reply, verbatim, in a returned
	// error. Site 7 used to be `%w` on the decode error, which reads
	// as harmless plumbing and is the same leak — measured against
	// THIS decode target (map[string]any), an HTML body renders
	// `invalid character '<' …` and `{"token":1e999}` renders the
	// literal `1e999`.
	//
	// The %w wrapping is dropped rather than preserved because the
	// wrapped error's text IS the leak; there is no way to keep the
	// chain unwrappable-but-quiet. Verified before removing that
	// nothing in this module calls errors.Is/errors.As on a login
	// error — the only errors.As in non-test code is the one
	// decodeFailureDetail uses internally.
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf(
			"login failed status=%d (reply body withheld — a failed "+
				"sign-in reply may carry a credential; shape %q, "+
				"length %d bytes, Content-Type %q)",
			resp.StatusCode, replyBodyShape(body2), len(body2),
			boundedContentType(resp.Header.Get("Content-Type")),
		)
	}
	var decoded map[string]any
	if err := json.Unmarshal(body2, &decoded); err != nil {
		return "", fmt.Errorf(
			"login response decode failed: %s (reply body withheld "+
				"— it may carry a credential; length %d bytes, "+
				"Content-Type %q)",
			decodeFailureDetail(err, body2), len(body2),
			boundedContentType(resp.Header.Get("Content-Type")),
		)
	}
	return extractLoginToken(decoded, h.TokenField, h.TokenFieldFallbacks)
}

// parseRetryAfter extracts the wait duration from a 429
// response. Honors RFC7231: Retry-After header (seconds), and
// falls back to a JSON `retry_after` field in the body, then to
// a 60-second default.
func parseRetryAfter(resp *http.Response, body []byte) time.Duration {
	if v := resp.Header.Get("Retry-After"); v != "" {
		if secs, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && secs > 0 {
			return time.Duration(secs) * time.Second
		}
	}
	var decoded map[string]any
	if err := json.Unmarshal(body, &decoded); err == nil {
		if v, ok := decoded["retry_after"]; ok {
			switch t := v.(type) {
			case float64:
				if t > 0 {
					return time.Duration(t) * time.Second
				}
			case string:
				if secs, err := strconv.Atoi(t); err == nil && secs > 0 {
					return time.Duration(secs) * time.Second
				}
			}
		}
	}
	return 60 * time.Second
}

// jsonPathExists evaluates a tiny subset of JSON-path expressions
// against body — enough to cover the expectations in HelixQA
// banks: $.foo, $.foo.bar, $.foo[0].bar. Returns
// (found, resolvedValue, err). It deliberately does NOT pull in
// a full JSONPath library — the bank's expectations are simple
// dot/bracket walks and adding a dependency for that would inflate
// the surface area.
func jsonPathExists(body []byte, path string) (bool, any, error) {
	path = strings.TrimSpace(path)
	if !strings.HasPrefix(path, "$") {
		return false, nil, fmt.Errorf("path must start with $")
	}
	rest := path[1:] // drop leading $
	var root any
	if err := json.Unmarshal(body, &root); err != nil {
		return false, nil, fmt.Errorf("body is not JSON: %w", err)
	}
	cur := root
	for rest != "" {
		switch {
		case strings.HasPrefix(rest, "."):
			rest = rest[1:]
			// read until next . or [
			end := strings.IndexAny(rest, ".[")
			var key string
			if end < 0 {
				key, rest = rest, ""
			} else {
				key, rest = rest[:end], rest[end:]
			}
			obj, ok := cur.(map[string]any)
			if !ok {
				return false, nil, nil
			}
			cur, ok = obj[key]
			if !ok {
				return false, nil, nil
			}
		case strings.HasPrefix(rest, "["):
			end := strings.Index(rest, "]")
			if end < 0 {
				return false, nil, fmt.Errorf("unterminated [ in path")
			}
			idx := strings.TrimSpace(rest[1:end])
			rest = rest[end+1:]
			arr, ok := cur.([]any)
			if !ok {
				return false, nil, nil
			}
			var n int
			if _, err := fmt.Sscanf(idx, "%d", &n); err != nil {
				return false, nil, fmt.Errorf("invalid array index %q: %w", idx, err)
			}
			if n < 0 || n >= len(arr) {
				return false, nil, nil
			}
			cur = arr[n]
		default:
			return false, nil, fmt.Errorf("unexpected token in path at %q", rest)
		}
	}
	return cur != nil, cur, nil
}

// parseHTTPAction splits a "METHOD /path" action value into
// method and path, tolerating extra whitespace.
func parseHTTPAction(value string) (method, path string) {
	parts := strings.Fields(strings.TrimSpace(value))
	if len(parts) >= 2 {
		return parts[0], parts[1]
	}
	if len(parts) == 1 {
		return "GET", parts[0]
	}
	return "", ""
}

// unresolvedPlaceholder returns the first `{var}`-style template
// placeholder in s that LOOKS unresolved (i.e. the var name
// matches a known capture-style identifier, not a real path
// segment). Returns "" if no placeholder is found. The check is
// deliberately conservative — a real path segment like
// `{"key":"v"}` in a query string is NOT a placeholder, only
// `/{job_id}` / `/{id}/` / `={smb_root}` patterns count.
//
// We accept the simple heuristic: anything matching `{[a-z_]+}`
// that the bank converter likely emitted as a placeholder.
// Substring tokens like `{` inside JSON query bodies don't reach
// this function — `path` is the URL path component only.
func unresolvedPlaceholder(path string) string {
	open := strings.IndexByte(path, '{')
	if open < 0 {
		return ""
	}
	close := strings.IndexByte(path[open:], '}')
	if close < 0 {
		return ""
	}
	frag := path[open : open+close+1]
	// Empty braces "{}" or single char are not placeholders.
	inner := frag[1 : len(frag)-1]
	if len(inner) == 0 {
		return ""
	}
	// Only treat lowercase + underscore + digits ID-ish names as
	// placeholders. Uppercase, hyphens, dots, etc. are real path
	// segments, not converter placeholders.
	for _, r := range inner {
		if !(r == '_' || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')) {
			return ""
		}
	}
	return frag
}

// needsCSRF reports whether the given (method, path) is behind the
// CSRF guard and therefore requires a token+cookie pair on the
// request.
func (h *HTTPExecutor) needsCSRF(method, path string) bool {
	if h.CSRFPreflightPath == "" {
		return false
	}
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
	default:
		return false
	}
	for _, prefix := range h.CSRFGuardedPathPrefixes {
		if strings.HasPrefix(path, prefix) {
			return true
		}
	}
	return false
}

// ensureCSRF performs a preflight GET to CSRFPreflightPath and
// captures the X-CSRF-Token header + csrf cookie. Cached for the
// lifetime of the executor — most CSRF guards mint long-lived
// tokens, and a session of bank tests is short enough that we
// don't need refresh logic. Idempotent: returns early if a token
// is already cached.
func (h *HTTPExecutor) ensureCSRF(ctx context.Context, authMode string) error {
	h.mu.Lock()
	have := h.csrfToken != "" && h.csrfCookieValue != ""
	h.mu.Unlock()
	if have {
		return nil
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, h.BaseURL+h.CSRFPreflightPath, nil)
	if err != nil {
		return fmt.Errorf("build preflight: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	if err := h.applyAuth(ctx, req, authMode); err != nil {
		return fmt.Errorf("preflight auth: %w", err)
	}
	resp, err := h.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("preflight fetch: %w", err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)

	tok := resp.Header.Get(h.CSRFHeaderName)
	var (
		cookieName  string
		cookieValue string
	)
	// Walk the configured candidate names in order — first match
	// wins. This handles deployments where the guard uses the
	// __Host- prefix (HTTPS) versus a bare cookie name (HTTP test
	// fixture).
	for _, name := range h.CSRFCookieNames {
		for _, c := range resp.Cookies() {
			if c.Name == name {
				cookieName = c.Name
				cookieValue = c.Value
				break
			}
		}
		if cookieName != "" {
			break
		}
	}
	if tok == "" && cookieValue == "" {
		// Guard not active for this deployment (e.g. NewCSRF
		// returned an err and main.go disabled the guard with a
		// warning). Treat as success — the actual call will go
		// through unguarded.
		return nil
	}
	h.mu.Lock()
	h.csrfToken = tok
	h.csrfCookieName = cookieName
	h.csrfCookieValue = cookieValue
	h.mu.Unlock()
	return nil
}
