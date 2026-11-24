package autonomous

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"digital.vasic.helixqa/pkg/analysis"
)

// HXC-267 — the second half of the HXC-239 silence.
//
// HXC-239 taught validateAPIData to RAISE a finding when a 200 login
// reply yields no usable bearer. That raise sits INSIDE
//
//	if jErr := json.Unmarshal(body, &decoded); jErr == nil { … }
//
// with no else, so the split it actually covers is PARSE / NO-PARSE,
// not token-present / token-absent. Measured cell-for-cell on the
// pre-fix artifact (§11.4.6 — run, not reasoned):
//
//	""                 → Unmarshal errors → SILENT
//	<html>503</html>   → Unmarshal errors → SILENT
//	null               → parses           → finding raised
//	{}                 → parses           → finding raised
//
// A 200 whose body does not decode into a JSON object therefore fails
// exactly as it did before HXC-239: no token, no finding, no log
// line. The pipeline carries on unauthenticated and the resulting
// 401s are filed as faults in the API under test. The practical
// trigger is a proxy or gateway in front of the real service
// answering an HTML error page with status 200 — the report then
// blames the wrong system.
//
// §11.4.115 polarity, same deviation and rationale documented on
// redModeOn (shared with the HXC-239 guard in this package): the
// STANDING default is GREEN (defect-absent) so `go test ./...` stays
// green on the fixed artifact and the §11.4.135 standing guard
// actually runs; RED is opt-in via RED_MODE=1 and asserts the
// pre-fix SILENCE.

// pipelineUndecodableBodyFindingTitle is the exact Title
// validateAPIData raises when login returns 200 but the body cannot
// be decoded into a JSON object. Held as a constant so a reworded
// production string breaks this guard loudly instead of letting it
// pass vacuously.
const pipelineUndecodableBodyFindingTitle = "API login returned 200 but the " +
	"response body could not be decoded"

// undecodableLoginServer answers the login POST with an operator-chosen
// body and Content-Type, and — like the live HelixCode authMiddleware
// and like unusableTokenAPIServer — answers 401 to every
// unauthenticated request thereafter.
func undecodableLoginServer(
	t *testing.T, loginBody []byte, contentType string,
) (*httptest.Server, *apiCallRecorder) {
	t.Helper()
	rec := &apiCallRecorder{}
	srv := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			rec.record(r.URL.Path, r.Header.Get("Authorization"))
			if r.Method == http.MethodPost &&
				r.URL.Path == "/api/v1/auth/login" {
				// Set explicitly (even when empty is wanted) so the
				// case-set controls the header rather than Go's
				// content sniffing.
				w.Header().Set("Content-Type", contentType)
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write(loginBody)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"status":"error",` +
				`"message":"Invalid or expired token"}`))
		}))
	return srv, rec
}

// undecodableCase is one cell of the §11.4.146 STEP-3 extend set: a
// login body shape, the outcome it must produce, and the marker that
// must NOT reach any finding.
type undecodableCase struct {
	name        string
	body        []byte
	contentType string
	// wantUndecodable is true when this body must raise the HXC-267
	// finding, false when it decodes and must instead reach the
	// HXC-239 no-bearer finding.
	wantUndecodable bool
	// wantCause is a substring the description must carry so the
	// diagnosis names the shape actually received.
	wantCause string
	// secret is a value present in the BODY that must never appear in
	// any finding (§11.4.10). Empty when the body carries none.
	secret string
}

// proxyErrorPage renders the practical trigger: a gateway answering
// HTTP 200 with an HTML error page. The page carries a bearer in a
// debug field, which is exactly why the body may not be echoed.
func proxyErrorPage() []byte {
	return []byte("<html><head><title>503 Service Unavailable</title>" +
		"</head><body><h1>503 Service Unavailable</h1>" +
		"<!-- upstream=api-01 auth=" + helixCodeJWT + " -->" +
		"</body></html>")
}

// truncatedLoginBody is a real login reply cut off mid-token — the
// shape a connection reset or a length-capped proxy produces.
func truncatedLoginBody() []byte {
	return []byte(`{"status":"success","token":"` +
		helixCodeJWT[:40])
}

func undecodableCases() []undecodableCase {
	return []undecodableCase{
		{
			name:            "empty_body",
			body:            []byte(``),
			contentType:     "application/json",
			wantUndecodable: true,
			wantCause:       "empty",
		},
		{
			name:            "html_error_page_with_status_200",
			body:            proxyErrorPage(),
			contentType:     "text/html; charset=utf-8",
			wantUndecodable: true,
			wantCause:       "not JSON",
			secret:          helixCodeJWT,
		},
		{
			name:            "truncated_json",
			body:            truncatedLoginBody(),
			contentType:     "application/json",
			wantUndecodable: true,
			wantCause:       "truncated or malformed JSON object",
			secret:          helixCodeJWT[:40],
		},
		{
			name:            "non_utf8_bytes",
			body:            []byte{0xff, 0xfe, 0x00, 0x01, 0x80, 0x90},
			contentType:     "application/octet-stream",
			wantUndecodable: true,
			wantCause:       "not JSON",
		},
		{
			name: "very_large_body",
			body: append(
				[]byte("<!doctype html><html><body>"),
				bytes.Repeat([]byte("padding-"), 32*1024)...,
			),
			contentType:     "text/html",
			wantUndecodable: true,
			wantCause:       "not JSON",
		},
		{
			name:            "valid_json_bare_string",
			body:            []byte(`"hxc267-bare-json-string"`),
			contentType:     "application/json",
			wantUndecodable: true,
			wantCause:       "JSON string",
			secret:          "hxc267-bare-json-string",
		},
		{
			name:            "valid_json_number",
			body:            []byte(`1234567890123`),
			contentType:     "application/json",
			wantUndecodable: true,
			wantCause:       "JSON number",
			secret:          "1234567890123",
		},
		{
			name:            "valid_json_array",
			body:            []byte(`[{"token":"` + helixCodeJWT + `"}]`),
			contentType:     "application/json",
			wantUndecodable: true,
			wantCause:       "JSON array",
			secret:          helixCodeJWT,
		},
		{
			name:            "valid_json_boolean",
			body:            []byte(`true`),
			contentType:     "application/json",
			wantUndecodable: true,
			wantCause:       "JSON boolean",
		},
		// The two that already worked before HXC-267: both decode
		// into a map (null decodes to a nil map), so both reach the
		// HXC-239 raise and must keep doing so. Their presence here is
		// the proof the fix did not move the parse-succeeds path.
		{
			name:            "json_null_decodes_and_reaches_no_bearer",
			body:            []byte(`null`),
			contentType:     "application/json",
			wantUndecodable: false,
		},
		{
			name:            "json_empty_object_decodes_and_reaches_no_bearer",
			body:            []byte(`{}`),
			contentType:     "application/json",
			wantUndecodable: false,
		},
	}
}

// TestHXC267_PipelineRaisesFindingOnUndecodableLoginBody is the
// standing guard for the else-path raise in validateAPIData.
//
// Without it, deleting the `findings = append(...)` in the
// parse-failure branch leaves the entire pkg/autonomous suite green —
// which makes the raise decoration rather than a gate (repo CLAUDE.md
// §6: every gate ships with a §1.1 mutation proving it catches the
// regression it claims).
func TestHXC267_PipelineRaisesFindingOnUndecodableLoginBody(t *testing.T) {
	for _, tc := range undecodableCases() {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			srv, rec := undecodableLoginServer(
				t, tc.body, tc.contentType,
			)
			defer srv.Close()

			sp := &SessionPipeline{
				config: dataValidationTestConfig(t, srv.URL),
			}
			findings := sp.validateAPIData(context.Background())

			var undecodable, noBearer []analysis.AnalysisFinding
			var downstream401 []string
			for _, f := range findings {
				switch {
				case f.Title == pipelineUndecodableBodyFindingTitle:
					undecodable = append(undecodable, f)
				case f.Title == pipelineNoBearerFindingTitle:
					noBearer = append(noBearer, f)
				case strings.Contains(f.Title, "returned 401"):
					downstream401 = append(downstream401, f.Title)
				}
			}

			// ── Asserted in BOTH polarities ──────────────────
			// True before and after the fix, and exactly what the
			// fix does NOT change: a body the pipeline cannot read
			// does not stop it. Every later request still goes out
			// unauthenticated and its 401 is still filed as an API
			// defect.
			calls := rec.snapshot()
			require.NotEmpty(t, calls,
				"the pipeline must actually hit the server")
			sawLogin := false
			for _, c := range calls {
				if c.path == "/api/v1/auth/login" {
					sawLogin = true
					continue
				}
				assert.Empty(t, c.auth,
					"after an unreadable login body the pipeline still "+
						"calls %s UNAUTHENTICATED — if this ever carries "+
						"a bearer, the pipeline.go comment describing the "+
						"residual behaviour is stale", c.path)
			}
			assert.True(t, sawLogin, "login must have been attempted")
			assert.Contains(t, downstream401,
				"API error: entities/stats returned 401",
				"the downstream 401 is still filed as an API defect")
			assert.Contains(t, downstream401,
				"API error: media/search returned 401",
				"the downstream 401 is still filed as an API defect")

			// §11.4.10 in both polarities: whatever the pipeline
			// says about a body it could not read, it must not
			// repeat the body. Asserted across EVERY finding, not
			// just the new one, so a leak cannot hide in a
			// neighbouring entry.
			if tc.secret != "" {
				for _, f := range findings {
					assert.NotContains(t, f.Description, tc.secret,
						"an unreadable body must never be echoed into a "+
							"finding (%q)", f.Title)
					assert.NotContains(t, f.Title, tc.secret,
						"an unreadable body must never be echoed into a "+
							"finding title")
				}
			}

			if !tc.wantUndecodable {
				// Decodes: the HXC-239 path owns it, in BOTH
				// polarities — this body shape was never silent, and
				// the HXC-267 fix must not have moved it.
				require.Len(t, noBearer, 1,
					"a decodable body must still reach the HXC-239 "+
						"no-bearer finding, got %d (findings=%d)",
					len(noBearer), len(findings))
				require.Empty(t, undecodable,
					"a body that DECODES must never be reported as "+
						"undecodable")
				return
			}

			if redModeOn() {
				// Defect-present assertion: the parse failure is
				// SILENT, so the only trace of an unreadable login
				// reply is a pile of 401s blamed on the API.
				require.Empty(t, undecodable,
					"RED_MODE=1: expected the pre-fix SILENT parse "+
						"failure (no finding raised), got %d. If this "+
						"passes on a defect-present artifact the guard "+
						"is blind (§11.4.115).", len(undecodable))
				require.Empty(t, noBearer,
					"RED_MODE=1: the pre-fix code never reaches the "+
						"no-bearer raise for an unparseable body — it is "+
						"inside the parse-OK branch")
				return
			}

			// ── GREEN guard: the unreadable body is surfaced ──
			require.Len(t, undecodable, 1,
				"expected exactly one %q finding, got %d (findings=%d)",
				pipelineUndecodableBodyFindingTitle,
				len(undecodable), len(findings))
			f := undecodable[0]

			// Same shape as the HXC-239 sibling so a reader sees one
			// coherent diagnosis, not two dialects.
			assert.Equal(t, analysis.CategoryFunctional, f.Category)
			assert.Equal(t, analysis.SeverityHigh, f.Severity)
			assert.Equal(t, "api", f.Platform)

			// Diagnosable (§11.4.6): names what was attempted …
			wantTried := fmt.Sprintf("%q", append(
				[]string{defaultTokenField},
				defaultTokenFieldFallbacks...,
			))
			assert.Contains(t, f.Description, wantTried,
				"the finding must name every candidate path tried, "+
					"exactly as the no-bearer sibling does")
			// … and the shape actually received.
			assert.Contains(t, f.Description, tc.wantCause,
				"the finding must classify the body shape it could "+
					"not decode")
			// … and the header that most often explains it.
			if tc.contentType != "" {
				assert.Contains(t, f.Description, tc.contentType,
					"the finding must name the response Content-Type — "+
						"the single field that identifies a proxy error "+
						"page served with status 200")
			}
			// … and that the body was deliberately withheld, so a
			// reader does not mistake the omission for a truncated
			// report.
			assert.Contains(t, f.Description, "withheld",
				"the finding must say the body was withheld on purpose")

			// … and carries the rendered diagnosis VERBATIM. The
			// wording itself is pinned in
			// TestHXC267_UndecodableDescriptionIsExactlyPinned; this
			// pins the other half of the same seam — that what
			// reaches a report is exactly what the renderer produced,
			// with nothing appended on the pipeline side, where the
			// substring assertions above would equally not notice.
			var decoded map[string]any
			jErr := json.Unmarshal(tc.body, &decoded)
			require.Error(t, jErr,
				"fixture must be a body that genuinely fails to decode")
			assert.Equal(t,
				undecodableLoginBodyDescription(
					jErr, tc.body, tc.contentType,
				),
				f.Description,
				"the finding must carry the rendered diagnosis "+
					"verbatim, byte-for-byte")

			// The undecodable body is the ONLY diagnosis: it never
			// reaches the no-bearer raise, so the report carries one
			// cause, not two.
			assert.Empty(t, noBearer,
				"an undecodable body must not ALSO raise the no-bearer "+
					"finding — one body, one diagnosis")
		})
	}
}

// TestHXC267_LoginBodyKindClassifiesWithoutEchoingContent pins the
// classifier the finding leans on: it must map a body to a CLOSED
// vocabulary and must never return any byte of the body itself.
//
// This is the §11.4.107(10) self-validation of the analyzer — a
// golden-good/golden-bad pair over the classifier, so a future
// "improvement" that inlines the offending bytes into the label
// (Go's own encoding/json does exactly this: UnmarshalTypeError.Value
// is "number "+<the literal digits>) fails here rather than shipping
// a leak.
func TestHXC267_LoginBodyKindClassifiesWithoutEchoingContent(t *testing.T) {
	const marker = "hxc267-secret-marker"
	vocabulary := map[string]bool{
		"empty":                              true,
		"truncated or malformed JSON object": true,
		"JSON array":                         true,
		"JSON string":                        true,
		"JSON number":                        true,
		"JSON boolean":                       true,
		"not JSON":                           true,
	}

	cases := []struct {
		name string
		body []byte
		want string
	}{
		{"empty", []byte(``), "empty"},
		{"whitespace_only", []byte("  \n\t "), "empty"},
		{"open_object", []byte(`{"a":`), "truncated or malformed JSON object"},
		{"array", []byte(`[1,2]`), "JSON array"},
		{"string", []byte(`"` + marker + `"`), "JSON string"},
		{"number", []byte(`1234567890`), "JSON number"},
		{"negative_number", []byte(`-42`), "JSON number"},
		{"boolean_true", []byte(`true`), "JSON boolean"},
		{"boolean_false", []byte(`false`), "JSON boolean"},
		{"html", []byte(`<html>` + marker + `</html>`), "not JSON"},
		{"binary", []byte{0xff, 0xfe, 0x00}, "not JSON"},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got := undecodableBodyKind(tc.body)
			assert.Equal(t, tc.want, got)
			require.True(t, vocabulary[got],
				"undecodableBodyKind must return a value from the closed "+
					"vocabulary, got %q — an open-ended label is how "+
					"body bytes leak into a report", got)
			assert.NotContains(t, got, marker,
				"the classifier must never return any byte of the body")
		})
	}
}

// TestHXC267_UndecodableDescriptionWithholdsBody is the golden-bad
// half of the analyzer self-validation: a body built ENTIRELY out of
// a marker must produce a description that carries none of it, at
// every length and every shape.
//
// Scope note (§11.4.6): the withholding applies to the BODY. The
// response Content-Type is a HEADER the server chose to advertise,
// not content, and it is the single field that identifies a proxy
// error page served with status 200 — so it is reported, bounded in
// length so a pathological header cannot bloat a report.
func TestHXC267_UndecodableDescriptionWithholdsBody(t *testing.T) {
	const marker = "hxc267-bearer-eyJhbGciOiJIUzI1NiJ9-do-not-echo"

	bodies := [][]byte{
		[]byte(marker),
		[]byte(`{"token":"` + marker + `"`),
		[]byte(`"` + marker + `"`),
		[]byte(`[` + marker + `]`),
		[]byte("<html>" + marker + "</html>"),
		bytes.Repeat([]byte(marker), 1024),
	}

	for i, body := range bodies {
		body := body
		t.Run(fmt.Sprintf("body_%d", i), func(t *testing.T) {
			var decoded map[string]any
			jErr := json.Unmarshal(body, &decoded)
			require.Error(t, jErr,
				"fixture must be a body that genuinely fails to decode")

			desc := undecodableLoginBodyDescription(
				jErr, body, "application/json",
			)
			assert.NotContains(t, desc, marker,
				"the body must never be echoed into the description")
			assert.Contains(t, desc, "withheld")
			assert.Contains(t, desc,
				fmt.Sprintf("%d bytes", len(body)),
				"the length is the safe stand-in for the content")
		})
	}
}

// TestHXC267_UndecodableDescriptionBoundsContentType keeps the one
// server-controlled string the description DOES carry from bloating
// a report.
func TestHXC267_UndecodableDescriptionBoundsContentType(t *testing.T) {
	var decoded map[string]any
	jErr := json.Unmarshal([]byte("<html></html>"), &decoded)
	require.Error(t, jErr)

	huge := "text/html; charset=" + strings.Repeat("x", 4096)
	desc := undecodableLoginBodyDescription(
		jErr, []byte("<html></html>"), huge,
	)
	assert.Less(t, len(desc), 600,
		"a pathological Content-Type must not bloat the finding")
	assert.Contains(t, desc, "text/html",
		"the useful prefix of the header must survive the bound")
}

// hxc267DescriptionFormat is the test's OWN copy of the diagnosis
// wording, held here so the expectation is rendered INDEPENDENTLY of
// the code under test. Building it by calling
// undecodableLoginBodyDescription would agree with every mutation of
// that function, which is the failure this guard exists to close.
const hxc267DescriptionFormat = "Login returned HTTP 200 but the response body could not " +
	"be decoded as a JSON object (%s), so no bearer token " +
	"could be looked up — tried %q. Response Content-Type " +
	"%q, body length %d bytes. The body itself is withheld " +
	"on purpose: a body this pipeline cannot read may still " +
	"carry a credential. Every request after this one goes " +
	"out UNAUTHENTICATED, so any 401 below originates here, " +
	"not in the API under test."

// deepNestedObject returns an object closed by matching braces but
// nested past encoding/json's limit (10000): structurally complete,
// and still undecodable. Half of the proof that reaching the '{' arm
// does not imply the object "never closed".
func deepNestedObject() []byte {
	const depth = 12000
	return []byte(
		strings.Repeat(`{"a":`, depth) + "1" +
			strings.Repeat("}", depth),
	)
}

// hxc267WantDescription renders the description the production code
// must produce, from CLOSED inputs: a shape label from
// undecodableBodyKind's fixed vocabulary, the already-normalised
// Content-Type, and a length. The candidate token paths are read from
// the production vars on purpose — the pin is over the WORDING, while
// the candidate list has its own assertion in the pipeline guard.
func hxc267WantDescription(
	cause, contentType string, bodyLen int,
) string {
	return fmt.Sprintf(
		hxc267DescriptionFormat,
		cause,
		append(
			[]string{defaultTokenField},
			defaultTokenFieldFallbacks...,
		),
		contentType, bodyLen,
	)
}

// TestHXC267_UndecodableDescriptionIsExactlyPinned pins the
// description byte-for-byte.
//
// Why exact rather than more substrings: every other assertion on
// this description is a substring Contains, which passes on any
// SUPERSET, and every §11.4.10 leak assertion needs the COMPLETE
// secret — the shortest fixture secret is 13 bytes. A mutation
// prefixing the description with 12 bytes of the body, label left
// correct, therefore satisfied both families and survived the entire
// suite (measured on this tree, not supposed). No substring family
// closes that hole; it only moves the threshold — 12 bytes caught, 4
// bytes not. assert.Equal closes it by construction: any deviation
// fails, additive, substitutive or truncating, at any size.
//
// This gives the description seam the property the classifier already
// has. TestHXC267_LoginBodyKindClassifiesWithoutEchoingContent pins
// undecodableBodyKind with assert.Equal plus closed-vocabulary membership,
// which is exactly why additive and substitutive deviations both die
// there. The cost — a reworded description breaks this guard — is
// this file's declared philosophy, already stated on
// pipelineUndecodableBodyFindingTitle: break loudly rather than pass
// vacuously. For a seam whose whole job is to state precisely what
// does and does not reach a committed report, a silent rewording is
// the thing worth catching.
func TestHXC267_UndecodableDescriptionIsExactlyPinned(t *testing.T) {
	hugeCT := "text/html; charset=" + strings.Repeat("x", 4096)

	cases := []struct {
		name        string
		body        []byte
		contentType string
		// wantContentType is what the description must render after
		// the empty→<none> substitution and the length bound.
		wantContentType string
		// wantKind is the literal label from undecodableBodyKind's closed
		// vocabulary, written out rather than computed so this pin
		// does not inherit a classifier mutation.
		wantKind string
		// wantOffset records whether this body fails with a
		// json.SyntaxError — the ONLY error carrying the numeric
		// Offset the description appends.
		wantOffset bool
	}{
		{
			name:            "empty_body",
			body:            []byte(``),
			contentType:     "application/json",
			wantContentType: "application/json",
			wantKind:        "empty",
			wantOffset:      true,
		},
		{
			name:            "html_error_page",
			body:            proxyErrorPage(),
			contentType:     "text/html; charset=utf-8",
			wantContentType: "text/html; charset=utf-8",
			wantKind:        "not JSON",
			wantOffset:      true,
		},
		{
			name:            "truncated_json",
			body:            truncatedLoginBody(),
			contentType:     "application/json",
			wantContentType: "application/json",
			wantKind:        "truncated or malformed JSON object",
			wantOffset:      true,
		},
		// The two counterexamples that made the '{' arm's old
		// comment ("a well-formed object would have decoded, so
		// reaching here means the object never closed") false.
		// Both are CLOSED and brace-balanced, both land on that
		// arm. Keeping them as live cells means the reworded
		// comment is guarded rather than merely asserted.
		//
		// First: json.Valid-true, rejected because 1e999 overflows
		// float64 — an UnmarshalTypeError, so no offset exists.
		{
			name:            "closed_object_overflow_number",
			body:            []byte(`{"token":1e999}`),
			contentType:     "application/json",
			wantContentType: "application/json",
			wantKind:        "truncated or malformed JSON object",
			wantOffset:      false,
		},
		// Second: 12000 matching braces, structurally complete,
		// rejected only for nesting past the decoder's limit.
		{
			name:            "closed_object_past_depth_limit",
			body:            deepNestedObject(),
			contentType:     "application/json",
			wantContentType: "application/json",
			wantKind:        "truncated or malformed JSON object",
			wantOffset:      true,
		},
		{
			name:            "json_boolean_no_offset",
			body:            []byte(`true`),
			contentType:     "application/json",
			wantContentType: "application/json",
			wantKind:        "JSON boolean",
			wantOffset:      false,
		},
		{
			name:            "absent_content_type_renders_none",
			body:            []byte(`<html></html>`),
			contentType:     "",
			wantContentType: "<none>",
			wantKind:        "not JSON",
			wantOffset:      true,
		},
		{
			name:            "pathological_content_type_is_bounded",
			body:            []byte(`<html></html>`),
			contentType:     hugeCT,
			wantContentType: hugeCT[:80] + "…",
			wantKind:        "not JSON",
			wantOffset:      true,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			if tc.name == "pathological_content_type_is_bounded" {
				require.Equal(t, 80, undecodableBodyContentTypeMax,
					"the expectation above hardcodes an 80-byte bound; "+
						"if the production bound moved, move it here too "+
						"rather than reading the constant back (that "+
						"would make the pin agree with any bound)")
			}

			var decoded map[string]any
			jErr := json.Unmarshal(tc.body, &decoded)
			require.Error(t, jErr,
				"fixture must be a body that genuinely fails to decode")

			// Build the expected cause independently: the same
			// SyntaxError.Offset the production code is allowed to
			// read, extracted here rather than borrowed from it.
			cause := tc.wantKind
			var syn *json.SyntaxError
			gotSyntaxErr := errors.As(jErr, &syn)
			require.Equal(t, tc.wantOffset, gotSyntaxErr,
				"fixture must fail with the error class this cell "+
					"claims — the offset half of the pin is only "+
					"meaningful if the two classes are really distinct")
			if gotSyntaxErr {
				cause = fmt.Sprintf(
					"%s, decoding gave up at byte offset %d",
					cause, syn.Offset,
				)
			}

			got := undecodableLoginBodyDescription(
				jErr, tc.body, tc.contentType,
			)

			assert.Equal(t,
				hxc267WantDescription(
					cause, tc.wantContentType, len(tc.body),
				),
				got,
				"the description must match byte-for-byte — a "+
					"substring guard passes on supersets, so anything "+
					"appended, prefixed or substituted (a slice of the "+
					"body, most of all) has to fail HERE")

			// The one field the doc comment says IS consulted.
			// Disabling the offset append leaves every other
			// assertion in this file green, so it needs its own.
			if tc.wantOffset {
				assert.Contains(t, got, "byte offset",
					"a json.SyntaxError carries the numeric Offset the "+
						"description promises to report; dropping the "+
						"append silently loses the only positional "+
						"information the diagnosis has")
			} else {
				assert.NotContains(t, got, "byte offset",
					"no offset exists for this error class, so claiming "+
						"one would be invented — this negative control "+
						"is what proves the assertion above discriminates")
			}
		})
	}
}
