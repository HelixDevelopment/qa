// SPDX-FileCopyrightText: 2026 Milos Vasic
// SPDX-License-Identifier: Apache-2.0

package autonomous

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"digital.vasic.helixqa/pkg/analysis"
	"digital.vasic.helixqa/pkg/testbank"
)

// HXC-278 — the THIRD failure route out of extractLoginToken, and the
// one nobody asked about.
//
// The same function fails three distinct ways, and each was closed
// separately because each leaks a different thing:
//
//	200 + undecodable body        → body echoed.  Closed by HXC-267.
//	non-200, or decode error      → body echoed.  Closed by HXC-270.
//	200 + decodable + no token    → KEYS echoed.  This item.
//
// The surviving route renders sortedKeys(decoded) — the reply's field
// NAMES — %q-quoted and UNBOUNDED in both count and per-key length.
//
// WHY "KEYS ONLY, NEVER VALUES" IS NOT SUFFICIENT HERE. The rationale
// HXC-239 shipped with sortedKeys is usually sound: a field name
// describes data rather than carrying it. Two things undermine it on
// exactly this route:
//
//  1. The keys come from WHATEVER SERVER ACTUALLY ANSWERED. A request
//     landing on the wrong system, or a service rendering diagnostic
//     material under unusual names, puts that material into a durable
//     record. A 200 that decodes but carries no recognisable token is
//     PRECISELY the wrong-service signature — it is the one route
//     where "some other system replied" is the leading hypothesis, so
//     it is the worst possible place to trust the peer's naming.
//  2. The list had NO BOUND AT ALL — not on key count, not on
//     individual key length — sitting beside undecodableBodyContentTypeMax,
//     a bound this same area already applies to a different piece of
//     server-controlled reply data for exactly this concern.
//
// A JSON object may be keyed by anything, including the credential
// itself: `{"<jwt>": {...}}` is a perfectly ordinary map-keyed-by-token
// shape, and its key IS the token.
//
// MEASURED SITE COUNT — FOUR sinks fed by ONE render site, not the
// three the review named. The review named the returned error, the
// finding Description and the session stdout line. Measured by
// following the render site's error value to every terminal sink:
//
// Line numbers below are POST-FIX (this tree). They are a reading
// aid, not the contract — the contract is the assertions.
//
//	render  http_executor.go:554-555  the %q of the key census
//	  ├─ propagates: loginWithRetry:919 → login:827 → applyAuth:418
//	  │    ├─ SINK 1  http_executor.go:247   ActionResult.Message
//	  │    │            "http: auth failed: %v"  → result.Actual
//	  │    │            → finding.Description (structured_executor.go:222)
//	  │    └─ SINK 2  http_executor.go:1105 → :259
//	  │              ensureCSRF wraps %w → "http: csrf preflight failed"
//	  └─ called directly by pipeline.go:3309
//	       ├─ SINK 3  pipeline.go:3319-3323  session stdout %v
//	       └─ SINK 4  pipeline.go:3330       finding Description
//
// http_executor.go:315 also receives the error and is NOT a sink: the
// 401-retry path guards on `err == nil` and discards it.
//
// SINK 2 is on the path but is not SERIALLY reachable, and this file
// says so rather than shipping a case that would pass vacuously:
// within one Execute call, applyAuth at :246 runs BEFORE ensureCSRF at
// :258 and returns early on error, so a login that fails here never
// reaches the CSRF preflight on that goroutine.
//
// "Serially" is the precise word, and the round-1 review was right to
// ask for it. The executor guards its shared state with h.mu and
// documents LastResponse as safe for concurrent use, so two Execute
// calls may be in flight at once, and then the sink IS reachable —
// traced, not assumed (§11.4.6):
//
//	applyAuth caches the bearer under the trimmed AuthMode
//	  (resolveCreds sets credsKey = mode), so goroutine A can satisfy
//	  :246 from cache without logging in at all;
//	goroutine B takes a 401 and calls invalidateCachedToken at :291,
//	  which deletes that same key under h.mu;
//	goroutine A then reaches :258, and ensureCSRF's own applyAuth now
//	  misses the cache and logs in for real — landing on this error.
//
// So SINK 2 is unreachable only in the single-goroutine shape a
// deterministic test would drive (§11.4.50), not in principle.
//
// No assertion gap follows, and the reason is compositional rather
// than incidental: SINK 2 does not render the census itself. ensureCSRF
// wraps applyAuth's error with %w ("preflight auth: %w") and Execute
// renders that wrapper at :259 — so it carries the SAME error value
// SINK 1 renders. Whatever bytes reach one reach the other, and pinning
// the wrapped value pins both. The login-error case below is that pin.
//
// NOT a sink, and deliberately left alone: the `tried` list rendered
// beside the census in the same error. It comes from TokenField /
// TokenFieldFallbacks — operator configuration, not the reply — so it
// is not reply-derived name material and is outside this item.
//
// Session stdout is a durable sink: it is captured, archived and
// attached to reports, which is why this guard asserts on it and not
// only on findings.
//
// §11.4.115 polarity, same deviation and rationale documented on
// redModeOn: the STANDING default is GREEN (defect-absent) so
// `go test ./...` stays green on the fixed artifact and the §11.4.135
// standing guard actually runs; RED is opt-in via RED_MODE=1 and
// asserts the pre-fix ECHO.

// hxc278JWTKeyMarker is a credential-shaped value planted in the KEY
// position of an otherwise-ordinary login reply.
//
// It is a structurally-real 3-segment base64url JWT because that is
// the shape the fix must recognise; it is SYNTHETIC nonsense
// throughout — no captured credential or real token is ever committed
// (§11.4.10). Distinct from helixCodeJWT so a failure message names
// which fixture leaked.
const hxc278JWTKeyMarker = "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9." +
	"eyJzdWIiOiJoeGMyNzhTeW50aGV0aWNMZWFrTWFya2VyIn0." +
	"aHhjMjc4U3ludGhldGljTGVha01hcmtlck5vdEFSZWFsS2V5"

// hxc278OpaqueKeyMarker is the second credential shape: a long opaque
// mixed-case base64-ish blob with no dots, the shape an session
// reference or an API key takes. Synthetic (§11.4.10).
const hxc278OpaqueKeyMarker = "hXc278SyntheticOpaqueSessionRef0000AbCdEf1234"

// hxc278BearerKeyMarker is the third shape: an Authorization header
// value used verbatim as a key, which is how a service that dumps its
// own request context into a reply object renders it. Synthetic.
const hxc278BearerKeyMarker = "Bearer hxc278-synthetic-bearer-marker-9d41"

// hxc278Base32KeyMarker and hxc278LowerDigitKeyMarker are the fourth
// and fifth shapes: TWO-character-class opaque blobs.
//
// Round-1 review finding F1. The first fix's class-count rule required
// THREE of {lower, upper, digit, symbol} and justified it with "a
// token mixes case with digits". That premise is false for base32,
// whose alphabet is A-Z plus 2-7 — no lowercase at all — and base32 is
// what TOTP secrets and a great many API keys are rendered in. A
// base32 blob therefore counted TWO classes, slipped the rule, and
// came back out of the census verbatim: the HXC-278 defect itself,
// alive for this shape. The lowercase variant is the mirror case,
// since base32 decoders are case-insensitive and plenty of systems
// emit lowercase.
//
// Both are REAL base32 of explicit synthetic text, matching the
// convention the markers above follow (decode them and you get the
// name back), so a reader can confirm at a glance that no captured
// credential is committed (§11.4.10):
//
//	NB4GG… → "hxc278SyntheticTotpLeakMarker"
//	nb4gg… → "hxc278SyntheticLowerBlobMarker"
//
// They decode to DIFFERENT text on purpose, so a failure message names
// which of the two shapes leaked rather than leaving it ambiguous.
const (
	hxc278Base32KeyMarker     = "NB4GGMRXHBJXS3TUNBSXI2LDKRXXI4CMMVQWWTLBOJVWK4Q"
	hxc278LowerDigitKeyMarker = "nb4ggmrxhbjxs3tunbsxi2ldjrxxozlsijwg6ysnmfzgwzls"
)

// hxc278EmbeddedKeyMarker is the sixth shape: a credential EMBEDDED in
// an otherwise-prose key rather than occupying the whole key.
//
// Round-1 review finding F2. The bearer and PEM rules were substring
// matches while the JWT, blob and hex rules were whole-string, so a
// credential with any prose around it evaded all three — the key here
// is not itself base64, so every whole-string rule declined it and up
// to loginReplyKeyMax bytes of the JWT emitted. The asymmetry was the
// defect; the shape is ordinary, being what a reply keyed by an
// annotated identifier looks like.
const hxc278EmbeddedKeyMarker = "note " + hxc278JWTKeyMarker

// hxc278NoTokenLoginBody renders a 200 login reply that DECODES into
// a JSON object and carries NO recognisable bearer under any
// configured candidate — the exact precondition for the third failure
// route — with marker planted in the KEY position.
//
// The ordinary keys around it are the ones the HXC-239 capture
// measured on a real HelixCode login response, minus `token`: they
// are what the diagnostic must still report.
func hxc278NoTokenLoginBody(t *testing.T, marker string) string {
	t.Helper()
	body := map[string]any{
		marker:    map[string]any{"issued": true},
		"status":  "success",
		"user":    "hxc278probe",
		"session": map[string]any{"id": "hxc278"},
	}
	b, err := json.Marshal(body)
	require.NoError(t, err, "the fixture body must marshal")
	// Guard the fixture itself: a body that accidentally carried a
	// recognised candidate would resolve a token and never reach the
	// route under test.
	for _, candidate := range append(
		[]string{defaultTokenField}, defaultTokenFieldFallbacks...,
	) {
		_, present := body[candidate]
		require.False(t, present,
			"fixture must not carry candidate %q, or extraction "+
				"would succeed and skip the route under test",
			candidate)
	}
	return string(b)
}

// hxc278LoginRoute is the 200-but-tokenless reply, reusing the
// three-route server HXC-270 already stood up rather than a parallel
// fixture that could drift from it.
func hxc278LoginRoute(t *testing.T, marker string) hxc270Route {
	t.Helper()
	return hxc270Route{
		status:      http.StatusOK,
		body:        hxc278NoTokenLoginBody(t, marker),
		contentType: "application/json",
	}
}

// hxc278Markers is the credential-shape set every site is driven
// with, so a fix that recognises only the JWT shape is caught.
//
// The base32 shape (round-1 review F1) is driven end to end here and
// not left to the oracle's unit fixtures alone: it is the shape that
// survived the first fix, so the guard that it no longer reaches a
// durable sink has to run against the real sinks, not only against the
// detector in isolation.
func hxc278Markers() []struct {
	name   string
	marker string
} {
	return []struct {
		name   string
		marker string
	}{
		{name: "jwt_shaped_key", marker: hxc278JWTKeyMarker},
		{name: "opaque_blob_key", marker: hxc278OpaqueKeyMarker},
		{name: "bearer_header_key", marker: hxc278BearerKeyMarker},
		{name: "base32_two_class_key", marker: hxc278Base32KeyMarker},
	}
}

// TestHXC278_ReplyKeyNamesNeverLeakIntoTheLoginError is the standing
// §11.4.135 guard for the RENDER site and, through it, for SINK 1 and
// SINK 2 — the two that render this same propagating error value.
//
// Driving h.login directly targets the error value both sinks copy,
// so the assertion holds for both without a SINK 2 case that could
// not be reached (see the header note on Execute's early return).
func TestHXC278_ReplyKeyNamesNeverLeakIntoTheLoginError(t *testing.T) {
	for _, m := range hxc278Markers() {
		m := m
		t.Run(m.name, func(t *testing.T) {
			srv := hxc270Server(
				t, hxc278LoginRoute(t, m.marker),
				hxc270OKStats(), hxc270OKSearch(),
			)
			defer srv.Close()

			h := NewHTTPExecutor(srv.URL)
			tok, err := h.login(
				context.Background(),
				Credentials{Username: "admin", Password: "admin123"},
			)

			// Asserted in BOTH polarities: this is the no-token
			// route, so it must actually have failed extraction. A
			// case that started resolving a token would pass the
			// leak assertion vacuously.
			require.Error(t, err,
				"render site: a tokenless 200 must fail extraction")
			require.Empty(t, tok,
				"render site: a failed extraction must yield no bearer")
			require.Contains(t, err.Error(), "missing token field",
				"render site: the case must reach the third failure "+
					"route, not one of the two already closed")

			if redModeOn() {
				// RED_MODE=1 reproduces the defect on the PRE-FIX
				// artifact: the credential-shaped key planted in
				// the reply comes back out verbatim. On the FIXED
				// artifact this polarity fails, which is the point
				// — it is the baseline capture, not a standing
				// assertion (§11.4.115).
				assert.Contains(t, err.Error(), m.marker,
					"RED_MODE=1: the render site must reproduce the "+
						"pre-fix echo — a credential-shaped key must "+
						"come back out in the returned error")
				return
			}

			// ── GREEN: the standing guard ───────────────────────
			assert.NotContains(t, err.Error(), m.marker,
				"a credential-shaped reply KEY must never be echoed "+
					"into the returned error — this error propagates "+
					"through applyAuth into ActionResult.Message and "+
					"from there into a finding description")
		})
	}
}

// TestHXC278_ReplyKeyNamesNeverLeakIntoAnActionResult covers SINK 1
// end to end: the ActionResult.Message an Execute step produces, which
// structured_executor.go:222 copies verbatim into a finding
// description.
//
// The render-site guard above asserts on the error value; this one
// asserts the value actually arrives masked at the durable sink, so a
// fix applied to the wrong layer cannot pass.
func TestHXC278_ReplyKeyNamesNeverLeakIntoAnActionResult(t *testing.T) {
	for _, m := range hxc278Markers() {
		m := m
		t.Run(m.name, func(t *testing.T) {
			srv := hxc270Server(
				t, hxc278LoginRoute(t, m.marker),
				hxc270OKStats(), hxc270OKSearch(),
			)
			defer srv.Close()

			h := NewHTTPExecutor(srv.URL)
			res := h.Execute(
				context.Background(), http.MethodGet,
				"/api/v1/entities/stats",
				testbank.TestStep{AuthMode: "admin"},
			)

			require.False(t, res.Success,
				"sink 1: a step whose auth cannot resolve must fail")
			require.Contains(t, res.Message, "auth failed",
				"sink 1: the case must have failed at applyAuth, "+
					"which is the sink under test")

			if redModeOn() {
				assert.Contains(t, res.Message, m.marker,
					"RED_MODE=1: sink 1 must reproduce the pre-fix "+
						"echo — the credential-shaped key must reach "+
						"ActionResult.Message")
				return
			}

			assert.NotContains(t, res.Message, m.marker,
				"sink 1: a credential-shaped reply KEY must never "+
					"reach ActionResult.Message — it is copied into a "+
					"finding description verbatim")
		})
	}
}

// TestHXC278_ReplyKeyNamesNeverLeakIntoAFindingOrLog covers SINK 3
// and SINK 4 — the pipeline's own extraction site, which does not go
// through the HTTP executor at all and so would survive a fix applied
// only to the executor.
func TestHXC278_ReplyKeyNamesNeverLeakIntoAFindingOrLog(t *testing.T) {
	for _, m := range hxc278Markers() {
		m := m
		t.Run(m.name, func(t *testing.T) {
			srv := hxc270Server(
				t, hxc278LoginRoute(t, m.marker),
				hxc270OKStats(), hxc270OKSearch(),
			)
			defer srv.Close()

			sp := &SessionPipeline{
				config: dataValidationTestConfig(t, srv.URL),
			}

			var findings []analysis.AnalysisFinding
			stdout := hxc270CaptureStdout(t, func() {
				findings = sp.validateAPIData(context.Background())
			})

			// Asserted in BOTH polarities: the pipeline must have
			// reached the extraction site, or both sinks are silent
			// and the leak assertion passes on nothing.
			require.Contains(t, stdout, "no bearer token found",
				"sinks 3+4: the pipeline must have reached the "+
					"third failure route")

			joined := hxc270JoinFindings(findings)

			if redModeOn() {
				assert.True(t,
					strings.Contains(stdout, m.marker) ||
						strings.Contains(joined, m.marker),
					"RED_MODE=1: sinks 3+4 must reproduce the pre-fix "+
						"echo — the credential-shaped key must reach "+
						"the session log or a finding")
				return
			}

			assert.NotContains(t, stdout, m.marker,
				"sink 3: a credential-shaped reply KEY must never "+
					"reach the session log — stdout is captured and "+
					"archived")
			for _, f := range findings {
				assert.NotContains(t, f.Description, m.marker,
					"sink 4: a credential-shaped reply KEY must never "+
						"reach a finding description (%q)", f.Title)
				assert.NotContains(t, f.Title, m.marker,
					"sink 4: a credential-shaped reply KEY must never "+
						"reach a finding title")
			}
		})
	}
}

// hxc278MissingTokenFieldFormat, hxc278CensusTruncFormat and
// hxc278CredentialPlaceholder are TEST-LOCAL copies of the production
// strings.
//
// They are deliberately second copies rather than references to
// missingTokenFieldFormat / loginReplyKeyCensusTruncFormat /
// credentialShapedKeyPlaceholder: an expectation rendered by reading
// the code under test agrees with ANY mutation of that code, so it
// would pin nothing. HXC-267 and HXC-270 both hit this and resolved
// it the same way.
const hxc278MissingTokenFieldFormat = "login response missing token field — " +
	"tried %q (top-level keys present: %q)"

const hxc278CensusTruncFormat = "…and %d more of %d"

const hxc278CredentialPlaceholder = "<redacted:credential-shaped>"

// TestHXC278_BoundsArePinned pins the two bounds and the masking
// threshold against literals.
//
// Read back from the constants they would agree with any bound,
// including no bound at all, which is the defect this item closes.
func TestHXC278_BoundsArePinned(t *testing.T) {
	if redModeOn() {
		t.Skip("SKIP-OK: #HXC-278 — RED_MODE=1; the bounds asserted " +
			"here are part of the post-fix contract")
	}
	require.Equal(t, 12, loginReplyKeyCensusMax,
		"the census lists at most 12 keys; if that moved on purpose, "+
			"move this literal in the same commit")
	require.Equal(t, 80, loginReplyKeyMax,
		"each listed key is clipped at 80 bytes, matching the bound "+
			"this package already applies to Content-Type")
	require.Equal(t, 80, undecodableBodyContentTypeMax,
		"loginReplyKeyMax is defined as this constant; the pin above "+
			"is only meaningful while this one is 80")
	require.Equal(t, 32, credentialShapedKeyMinLen,
		"a key shorter than 32 bytes is never treated as an opaque "+
			"credential blob")
}

// TestHXC278_CredentialShapeOracleIsSelfValidated is the
// §11.4.107(10) self-validation of the detector.
//
// The leak guards above are only as good as looksCredentialShaped: a
// detector that answered false for everything would satisfy nothing,
// and one that answered true for everything would satisfy every
// NotContains assertion in this file while destroying the diagnostic.
// Both fixture sets are therefore asserted, so the analyzer itself
// cannot bluff in either direction.
func TestHXC278_CredentialShapeOracleIsSelfValidated(t *testing.T) {
	if redModeOn() {
		t.Skip("SKIP-OK: #HXC-278 — RED_MODE=1; the shape oracle is " +
			"part of the post-fix contract")
	}

	// GOLDEN-GOOD: real field names, every one of which must pass
	// through unmasked. The first block is the measured HXC-239
	// capture of a live HelixCode login response.
	good := []string{
		"session", "status", "token", "user",
		"id", "user_id", "session_token", "client_type",
		"ip_address", "user_agent", "display_name",
		"is_active", "is_verified", "mfa_enabled",
		"access_token", "retry_after", "total_entities", "by_type",
		// A dotted path, which lookupJSONString genuinely supports
		// as a literal key.
		"a.b.c",
		// Long, but snake_case: lowercase plus separator, and no
		// digit — so it is neither three classes nor an unbroken
		// single-case-plus-digit run.
		"extremely_long_but_legitimate_field_name",
		// CONSTANT_CASE, the uppercase mirror of the line above. It
		// is the name the round-1 F1 rule had to be written NOT to
		// swallow: uppercase plus separator, still no digit.
		"EXTREMELY_LONG_BUT_LEGITIMATE_CONSTANT_NAME",
		// An unbroken run of letters with no digit at all. Also the
		// exact key the census pin clips rather than masks, listed
		// here so the two tests cannot disagree about it.
		strings.Repeat("k", 100),
		strings.Repeat("K", 100),
		// hxc278Base32KeyMarker's shape one byte BELOW
		// credentialShapedKeyMinLen. Pins the threshold from the
		// other side: the same bytes at 32 are golden-bad below, so
		// this pair is what makes the length rule falsifiable rather
		// than decorative.
		"NB4GGMRXHBZXS3TUNBSXI2LDGMZGEMZ",
		// ── round-3 review F-R3-1: the unbroken-alphabet gate is
		// silently deletable ────────────────────────────────────────
		// `if !isCredentialAlphabet(run) { return false }` in
		// runIsCredentialShaped has a pure OVER-CATCH failure mode:
		// deleting it cannot unmask anything (it only ever narrows
		// what reaches the hex/class-count/single-case-digit rules
		// below it), so no golden-BAD fixture is structurally capable
		// of killing that mutant — only a golden-GOOD one can, and
		// nothing above occupied the relevant shape ("a.b.c" is
		// floor-rejected at 5 bytes, well short of
		// credentialShapedKeyMinLen).
		//
		// This is a dotted, ordinary-looking field name at or above
		// the length floor with exactly TWO non-empty segments (so
		// the JWT rule's `len(segments) >= 3` does not apply) and
		// THREE character classes once the dot is set aside (lower,
		// upper, digit — the dot itself is not a counted symbol; see
		// charClassCount, which only counts +/=_- ). Measured, not
		// assumed (§11.4.6): 34 bytes, 2 segments, 3 classes,
		// isCredentialAlphabet=false (the dot is outside the base64
		// alphabet), isHexRun=false, isSingleCaseDigitRun=false (its
		// default branch rejects the dot outright). On pristine code
		// the gate above rejects the run before any of the
		// class-counting rules run, so this returns false; delete the
		// gate and the run falls through to `charClassCount(run) >= 3`,
		// which counts 3 and returns true. Verified against this tree
		// before and after the deletion, not taken on the reviewer's
		// word alone.
		//
		// Pinning it here is what keeps the gate from being "silently
		// deletable": without a case that only the gate protects, a
		// refactor removing it would start masking legitimate dotted
		// key names — eroding the exact diagnostic the usefulness
		// half of this file (TestHXC278_MissingTokenErrorStaysDiagnostic)
		// exists to protect — while every other test here stayed
		// green.
		"OAuth2.AccessTokenExpiresInSeconds",
		"", // the empty key: degenerate, never credential material
	}
	for _, k := range good {
		assert.False(t, looksCredentialShaped(k),
			"golden-good: %q is an ordinary field name and must NOT "+
				"be masked — masking it would destroy the diagnostic "+
				"this census exists to provide", k)
	}

	// GOLDEN-BAD: credential material in the key position. Every one
	// must be flagged; a detector that misses any of these leaks it.
	bad := []string{
		hxc278JWTKeyMarker,
		hxc278OpaqueKeyMarker,
		hxc278BearerKeyMarker,
		// The JWT and opaque session reference the HXC-239 capture
		// measured, reused here so the two items' fixtures agree on
		// what a credential looks like.
		helixCodeJWT,
		helixCodeSessionRef,
		// A hex digest: only two character classes (lowercase and
		// digit), which is why the detector needs a hex rule separate
		// from the class count. 64 chars — a SHA-256 in hex.
		"3b1f4c9a2e7d05684fa3c1b9e0d7a2461f8c35d0ae9b6742c1d80f3a5b6e97c4",
		strings.Repeat("a1b2c3d4", 8),
		// An Authorization header value copied verbatim.
		"Bearer abc123def456",
		"bearer " + hxc278OpaqueKeyMarker,
		// A PEM block header.
		"-----BEGIN RSA PRIVATE KEY-----",
		// ── round-1 review F1: TWO-class blobs ──────────────────
		// base32 (upper+digit) and its lowercase mirror. Both were
		// two character classes, so the first fix's three-class rule
		// declined them and they leaked verbatim. Non-hex, so the hex
		// rule did not catch them either.
		hxc278Base32KeyMarker,
		hxc278LowerDigitKeyMarker,
		// The same shape at EXACTLY credentialShapedKeyMinLen, paired
		// with its 31-byte golden-good sibling above.
		"NB4GGMRXHBZXS3TUNBSXI2LDGMZGEMZS",
		// ── round-1 review F2: EMBEDDED credential ──────────────
		// A credential with prose around it. The space put the key
		// outside the base64 alphabet, so every whole-string rule
		// declined it while the credential sat in the middle intact.
		hxc278EmbeddedKeyMarker,
		"session=" + hxc278OpaqueKeyMarker + " (stale)",
		// ── round-2 review F-R2-1: hex-branch-ONLY shapes ───────
		// isSingleCaseDigitRun (added by round-1 F1, above) happens
		// to ALSO catch both hex fixtures above — the SHA-256 digest
		// and the a1b2c3d4 repeat — because both are lowercase
		// letters plus digits. That coincidence meant a mutation
		// that deleted the hex rule outright left every test in this
		// file passing: nothing here exercised a run the hex rule
		// alone protects. These three are runs isHexRun catches and
		// neither of the other two rules can, each for a different
		// reason:
		//
		//	all-digit: no letter of either case, so
		//	  isSingleCaseDigitRun's "digit && lower != upper" is
		//	  false (lower and upper are both false, so they are
		//	  equal), and charClassCount counts one class.
		//	all-hex-letter, one case: no digit at all, so
		//	  isSingleCaseDigitRun declines regardless of case, and
		//	  charClassCount again counts one class.
		//	all-hex-letter, mixed case: still no digit, so
		//	  isSingleCaseDigitRun declines the same way, and
		//	  charClassCount now counts TWO classes — lower and
		//	  upper — still short of its three-class floor.
		//
		// None decodes to readable text in the convention the named
		// markers above follow: a digit-only alphabet cannot encode
		// a letter, and an alphabet confined to hex letters (a-f /
		// A-F) can only decode to byte values 0xAA-0xFF — outside
		// printable ASCII — so these are clearly-labelled synthetic
		// digit/letter constants rather than decode-and-read
		// fixtures. Each is exactly credentialShapedKeyMinLen bytes.
		strings.Repeat("48501927", 4),      // all-digit, no letters
		"deadbeefcafebabefeedfacebaadface", // all-hex-letter, one case
		"DeadBeefCafeBabeFeedFaceBaadFace", // all-hex-letter, mixed case
		// ── round-4 review F-R4-1: dot-merging in credentialRuns is
		// silently deletable ─────────────────────────────────────────
		// isCredentialRunByte extends isCredentialAlphabetByte with
		// `|| c == '.'` so a JWT's dot-separated segments stay in ONE
		// run instead of being cut into three sub-threshold pieces —
		// but nothing above pinned that purpose: every JWT-shaped
		// fixture in this file (hxc278JWTKeyMarker, helixCodeJWT,
		// hxc278EmbeddedKeyMarker) carries the 36-byte first segment
		// "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9", which the
		// three-class rule (charClassCount(run) >= 3) catches ON THAT
		// SEGMENT ALONE even with the dot removed from
		// isCredentialRunByte — so deleting the merge left the whole
		// suite green.
		//
		// A compact JWT whose EVERY segment is under
		// credentialShapedKeyMinLen is the shape only the merge
		// protects — entirely realistic, e.g. an alg-none-family
		// token. With the dot merging the three segments into one
		// 40-byte run, the JWT rule (len(segments) >= 3, each segment
		// isCredentialAlphabet) matches and the whole run is masked.
		// Delete `|| c == '.'` and the run instead splits at each dot
		// into three runs of 19, 8 and 11 bytes — every one below the
		// credentialShapedKeyMinLen floor — so runIsCredentialShaped
		// returns false for all three and looksCredentialShaped
		// returns false for the whole key: a verbatim leak of a real
		// JWT. Decodes (§11.4.10 convention) to {"alg":"none"} /
		// "hxc278" / "shortSig". Measured, not assumed (§11.4.6):
		// verified against this tree both with and without the
		// deletion — only this fixture fails under the mutant, and it
		// passes on pristine code.
		"eyJhbGciOiJub25lIn0.aHhjMjc4.c2hvcnRTaWc",
	}
	for _, k := range bad {
		assert.True(t, looksCredentialShaped(k),
			"golden-bad: %q is credential-shaped and MUST be masked", k)
	}
}

// TestHXC278_KeyCensusIsExactlyPinned pins the census output byte for
// byte.
//
// The leak guards prove credential-shaped keys do not come out; this
// proves the replacement still says something useful. A fix that
// reported nothing — an empty census, or every key masked — would
// satisfy every NotContains assertion in this file and be just as
// broken, in the other direction.
//
// Every expected value is a literal written out here; none is
// obtained by calling boundedKeyCensus, boundedKeyName or
// looksCredentialShaped, which are the code under test.
func TestHXC278_KeyCensusIsExactlyPinned(t *testing.T) {
	if redModeOn() {
		t.Skip("SKIP-OK: #HXC-278 — RED_MODE=1; the bounded census is " +
			"part of the post-fix contract")
	}

	longOrdinaryKey := strings.Repeat("k", 100)

	manyKeys := map[string]any{}
	for i := 0; i < 20; i++ {
		manyKeys[fmt.Sprintf("k%02d", i)] = i
	}

	cases := []struct {
		name    string
		decoded map[string]any
		want    []string
	}{
		{
			name: "ordinary_keys_pass_through_sorted",
			decoded: map[string]any{
				"status": "success", "user": "u", "session": "s",
			},
			want: []string{"session", "status", "user"},
		},
		{
			name: "jwt_shaped_key_is_masked_neighbours_survive",
			decoded: map[string]any{
				hxc278JWTKeyMarker: 1, "status": "success", "user": "u",
			},
			// The marker starts with 'e' and so sorts before both.
			want: []string{
				hxc278CredentialPlaceholder, "status", "user",
			},
		},
		{
			name: "opaque_blob_key_is_masked",
			decoded: map[string]any{
				hxc278OpaqueKeyMarker: 1, "status": "success",
			},
			// Starts with 'h', before 's'.
			want: []string{hxc278CredentialPlaceholder, "status"},
		},
		{
			name: "bearer_header_key_is_masked",
			decoded: map[string]any{
				hxc278BearerKeyMarker: 1, "status": "success",
			},
			// Leading 'B' is uppercase, which sorts before 's'.
			want: []string{hxc278CredentialPlaceholder, "status"},
		},
		{
			name: "long_ordinary_key_is_clipped_not_masked",
			decoded: map[string]any{
				longOrdinaryKey: 1, "status": "success",
			},
			// 'k' < 's'. Clipped at 80 bytes with an explicit
			// ellipsis, NOT replaced — it is not credential-shaped.
			want: []string{
				strings.Repeat("k", 80) + "…", "status",
			},
		},
		// ── round-1 review F3: the clip boundary ────────────────
		// The three cases either side of loginReplyKeyMax. Without
		// them the clip's comparison was untested at its own edge:
		// `>` → `>=` in boundedKeyName survived the entire suite,
		// because a 100-byte key clips identically under both and
		// nothing sat at 79, 80 or 81. The 80-byte case is the one
		// that kills that mutant — under `>=` it gains a spurious
		// ellipsis — and its neighbours pin that the boundary did
		// not simply move by one.
		{
			name: "key_one_byte_below_the_clip_is_untouched",
			decoded: map[string]any{
				strings.Repeat("k", 79): 1, "status": "success",
			},
			want: []string{strings.Repeat("k", 79), "status"},
		},
		{
			name: "key_exactly_at_the_clip_is_untouched",
			decoded: map[string]any{
				strings.Repeat("k", 80): 1, "status": "success",
			},
			// EXACTLY loginReplyKeyMax bytes: nothing is dropped, so
			// nothing may claim to have been. An ellipsis here would
			// assert a truncation that did not happen.
			want: []string{strings.Repeat("k", 80), "status"},
		},
		{
			name: "key_one_byte_above_the_clip_is_clipped",
			decoded: map[string]any{
				strings.Repeat("k", 81): 1, "status": "success",
			},
			want: []string{strings.Repeat("k", 80) + "…", "status"},
		},
		// ── round-1 review F1 + F2 at the census ────────────────
		{
			name: "base32_two_class_key_is_masked",
			decoded: map[string]any{
				hxc278Base32KeyMarker: 1, "status": "success",
			},
			// Leading 'N' is uppercase, which sorts before 's'.
			want: []string{hxc278CredentialPlaceholder, "status"},
		},
		{
			name: "embedded_credential_masks_the_whole_key",
			decoded: map[string]any{
				hxc278EmbeddedKeyMarker: 1, "status": "success",
			},
			// 'n' < 's'. The WHOLE key is replaced, not just the
			// credential inside it: a partial mask would still emit
			// the surrounding bytes and invite a reader to
			// reconstruct what sat between them.
			want: []string{hxc278CredentialPlaceholder, "status"},
		},
		{
			name:    "count_overflow_is_truncated_honestly",
			decoded: manyKeys,
			want: []string{
				"k00", "k01", "k02", "k03", "k04", "k05",
				"k06", "k07", "k08", "k09", "k10", "k11",
				fmt.Sprintf(hxc278CensusTruncFormat, 8, 20),
			},
		},
		{
			name:    "empty_object_reports_an_empty_census",
			decoded: map[string]any{},
			want:    []string{},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got := boundedKeyCensus(tc.decoded)
			assert.Equal(t, tc.want, got,
				"the census is pinned element by element; if this "+
					"changed on purpose, update the literals here in "+
					"the same commit")
			assert.LessOrEqual(t, len(got), 12+1,
				"the census must never exceed the count bound plus "+
					"its truncation note")
			for _, k := range got {
				assert.LessOrEqual(t, len(k), 100,
					"no single census entry may be unbounded (%q)", k)
			}
		})
	}
}

// TestHXC278_MissingTokenErrorStaysDiagnostic is the USEFULNESS half
// of the guard, asserted at every measured sink.
//
// A redaction fix that reported nothing passes every leak assertion
// in this file while destroying the diagnostic an operator debugging
// a failed sign-in actually needs. This is the assertion that catches
// that, so the guard covers both directions and not only the one the
// item was afraid of.
func TestHXC278_MissingTokenErrorStaysDiagnostic(t *testing.T) {
	if redModeOn() {
		t.Skip("SKIP-OK: #HXC-278 — RED_MODE=1; a bounded-but-still-" +
			"useful census is part of the post-fix contract")
	}

	srv := hxc270Server(
		t, hxc278LoginRoute(t, hxc278JWTKeyMarker),
		hxc270OKStats(), hxc270OKSearch(),
	)
	defer srv.Close()

	// The ordinary keys that sit beside the planted marker in the
	// fixture. Every one must survive to every sink.
	ordinary := []string{"session", "status", "user"}

	t.Run("render_site_and_propagating_error", func(t *testing.T) {
		h := NewHTTPExecutor(srv.URL)
		_, err := h.login(
			context.Background(),
			Credentials{Username: "admin", Password: "admin123"},
		)
		require.Error(t, err)

		// Pinned against the TEST-LOCAL format copy, rendered with
		// the exact census this fixture must produce — not by asking
		// the code under test what it produced.
		want := fmt.Sprintf(
			hxc278MissingTokenFieldFormat,
			[]string{"session_token", "token", "access_token"},
			[]string{
				hxc278CredentialPlaceholder,
				"session", "status", "user",
			},
		)
		assert.Equal(t, want, err.Error(),
			"the missing-token error is pinned byte for byte: it must "+
				"still name every candidate tried and every ordinary "+
				"key present, with only the credential-shaped one "+
				"masked")
	})

	t.Run("action_result_sink", func(t *testing.T) {
		h := NewHTTPExecutor(srv.URL)
		res := h.Execute(
			context.Background(), http.MethodGet,
			"/api/v1/entities/stats",
			testbank.TestStep{AuthMode: "admin"},
		)
		require.False(t, res.Success)
		for _, k := range ordinary {
			assert.Contains(t, res.Message, k,
				"sink 1 must still report ordinary key %q — a census "+
					"that masked everything would be useless", k)
		}
		assert.Contains(t, res.Message, hxc278CredentialPlaceholder,
			"sink 1 must say a redaction happened, so a reader does "+
				"not mistake the omission for the object's real shape")
	})

	t.Run("pipeline_log_and_finding_sinks", func(t *testing.T) {
		sp := &SessionPipeline{
			config: dataValidationTestConfig(t, srv.URL),
		}
		var findings []analysis.AnalysisFinding
		stdout := hxc270CaptureStdout(t, func() {
			findings = sp.validateAPIData(context.Background())
		})
		joined := hxc270JoinFindings(findings)

		require.NotEmpty(t, findings,
			"sinks 3+4: the pipeline must still raise the "+
				"no-usable-token finding")
		for _, k := range ordinary {
			assert.Contains(t, stdout, k,
				"sink 3 must still report ordinary key %q", k)
			assert.Contains(t, joined, k,
				"sink 4 must still report ordinary key %q", k)
		}
		assert.Contains(t, stdout, hxc278CredentialPlaceholder,
			"sink 3 must say a redaction happened")
		assert.Contains(t, joined, hxc278CredentialPlaceholder,
			"sink 4 must say a redaction happened")
	})
}

// TestHXC278_CensusTruncationIsVisibleAtASink proves the count bound
// announces itself where a reader would see it, not only in the unit
// pin above.
func TestHXC278_CensusTruncationIsVisibleAtASink(t *testing.T) {
	if redModeOn() {
		t.Skip("SKIP-OK: #HXC-278 — RED_MODE=1; the count bound is " +
			"part of the post-fix contract")
	}

	wide := map[string]any{}
	for i := 0; i < 40; i++ {
		wide[fmt.Sprintf("field_%02d", i)] = i
	}
	body, err := json.Marshal(wide)
	require.NoError(t, err)

	srv := hxc270Server(t, hxc270Route{
		status:      http.StatusOK,
		body:        string(body),
		contentType: "application/json",
	}, hxc270OKStats(), hxc270OKSearch())
	defer srv.Close()

	h := NewHTTPExecutor(srv.URL)
	_, loginErr := h.login(
		context.Background(),
		Credentials{Username: "admin", Password: "admin123"},
	)
	require.Error(t, loginErr)

	assert.Contains(t, loginErr.Error(),
		fmt.Sprintf(hxc278CensusTruncFormat, 28, 40),
		"a 40-key reply must be reported as 12 keys plus an honest "+
			"count of what was omitted — silence about the omission "+
			"would misrepresent the object's shape")
	assert.Contains(t, loginErr.Error(), "field_00",
		"truncation must keep the first keys, not drop them all")
	assert.NotContains(t, loginErr.Error(), "field_39",
		"truncation must actually bound the list")
}
