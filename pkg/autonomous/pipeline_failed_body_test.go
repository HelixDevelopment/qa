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
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"digital.vasic.helixqa/pkg/analysis"
)

// HXC-270 — the FAILURE half of the discipline HXC-267 fixed on the
// success half.
//
// HXC-267 taught the 200-but-undecodable sign-in path to report only
// a shape label, a byte count and a decode position — never a body
// byte. Fifteen lines below it, the non-200 sign-in path recorded
// `string(body)` verbatim as a finding description, and so did the
// entity-statistics and media-search paths. The shared HTTP helper
// did the same in a RETURNED error, which propagates up the call
// chain into every caller's message.
//
// An unsuccessful reply is still a reply. A sign-in route that echoes
// a submitted token, renders a session cookie into its body, or
// quotes an Authorization header back in diagnostic output puts that
// value into a durable record that travels into shared logs and
// automation.
//
// MEASURED SITE COUNT — SEVEN, not the five the item was filed with
// and not the three its prose named. The item enumerated the three
// `string(body)` descriptions in validateAPIData plus two in the
// shared HTTP helper. It did not count the two decode complaints
// sitting ten lines below two of them: `fmt.Printf("… JSON parse
// failed: %v", jErr)` renders a body byte too, because encoding/json
// embeds input in its error values. Measured on this toolchain
// against the exact decode targets those two sites use:
//
//	{"total_entities":1e999270270} → `cannot unmarshal number
//	  1e999270270 into Go struct field .total_entities of type
//	  int` — the whole literal, not one byte (both decode targets
//	  are anonymous structs, so the struct-name segment before the
//	  dot renders empty)
//	{"total":1e999270270}          → the same, via .total
//	<html>…</html>                 → `invalid character '<' …`
//	0xff…                          → `'ÿ'`
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

// hxc270TextMarker is the distinctive value planted in a failure body
// that must never reach a finding, a log line or an error string.
//
// §11.4.10: synthetic throughout — this is a recognisable nonsense
// string, never a captured credential or a real token.
const hxc270TextMarker = "HXC270-SYNTHETIC-LEAK-MARKER-9d41"

// hxc270NumberMarker is the marker for the two decode-complaint
// sites, where the leak vehicle is json.UnmarshalTypeError.Value
// rather than a raw body copy.
//
// It MUST be a JSON number literal the decode target cannot
// represent: that is the measured path on which encoding/json embeds
// the full literal in its error text. A text marker cannot exercise
// these sites — a json.SyntaxError quotes exactly ONE character,
// which is too little to distinguish from ordinary prose.
const hxc270NumberMarker = "1e999270270"

// hxc270Route is one route's canned reply.
type hxc270Route struct {
	status      int
	body        string
	contentType string
}

// hxc270OKLogin is the reply that lets a case reach the
// entity-statistics and media-search routes authenticated, so a case
// targeting those sites is not short-circuited at sign-in.
func hxc270OKLogin() hxc270Route {
	return hxc270Route{
		status:      http.StatusOK,
		body:        `{"token":"` + helixCodeJWT + `"}`,
		contentType: "application/json",
	}
}

// hxc270OKStats and hxc270OKSearch are healthy replies, used for the
// routes a case is not exercising. Both report non-zero totals so the
// "returned zero entities" / "zero results" findings stay out of the
// way of the assertion set.
func hxc270OKStats() hxc270Route {
	return hxc270Route{
		status:      http.StatusOK,
		body:        `{"total_entities":5,"by_type":{"video":5}}`,
		contentType: "application/json",
	}
}

func hxc270OKSearch() hxc270Route {
	return hxc270Route{
		status:      http.StatusOK,
		body:        `{"items":[{"id":"a"}],"total":1}`,
		contentType: "application/json",
	}
}

// hxc270Server answers each of the three routes validateAPIData
// visits with an operator-chosen status, body and Content-Type.
func hxc270Server(
	t *testing.T, login, stats, search hxc270Route,
) *httptest.Server {
	t.Helper()
	write := func(w http.ResponseWriter, r hxc270Route) {
		w.Header().Set("Content-Type", r.contentType)
		w.WriteHeader(r.status)
		_, _ = w.Write([]byte(r.body))
	}
	return httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			switch {
			case r.URL.Path == "/api/v1/auth/login":
				write(w, login)
			case r.URL.Path == "/api/v1/entities/stats":
				write(w, stats)
			case r.URL.Path == "/api/v1/media/search":
				write(w, search)
			default:
				w.WriteHeader(http.StatusNotFound)
			}
		}))
}

// hxc270CaptureStdout runs fn with os.Stdout redirected to a pipe and
// returns everything written to it.
//
// validateAPIData reports two of the seven sites through fmt.Printf
// rather than through a finding, so a guard that inspects only the
// returned findings would call those two sites clean while they leak.
// No test in this package calls t.Parallel(), so swapping the process
// stdout for the duration of one subtest cannot steal another's
// output. The reader goroutine drains concurrently, so a body larger
// than the pipe buffer cannot deadlock the writer.
func hxc270CaptureStdout(t *testing.T, fn func()) string {
	t.Helper()
	orig := os.Stdout
	r, w, err := os.Pipe()
	require.NoError(t, err, "stdout capture needs a pipe")
	os.Stdout = w

	drained := make(chan string, 1)
	go func() {
		var buf bytes.Buffer
		_, _ = io.Copy(&buf, r)
		drained <- buf.String()
	}()

	func() {
		defer func() {
			os.Stdout = orig
			_ = w.Close()
		}()
		fn()
	}()

	out := <-drained
	_ = r.Close()
	return out
}

// hxc270PipelineCase is one cell of the validateAPIData half of the
// measured site set.
type hxc270PipelineCase struct {
	name string
	// site is the site number in the seven-site enumeration, carried
	// so a failure message names which one regressed.
	site   int
	login  hxc270Route
	stats  hxc270Route
	search hxc270Route
	// marker must appear in NO finding and in NO log line.
	marker string
}

// hxc270PipelineCases covers the five measured sites inside
// validateAPIData: three verbatim `string(body)` descriptions and two
// `%v`-on-decode-error log lines.
func hxc270PipelineCases() []hxc270PipelineCase {
	failure := func(marker string) hxc270Route {
		return hxc270Route{
			status: http.StatusInternalServerError,
			// The shape of a real leak: a service dumping request
			// context into its own error body. The marker stands
			// where a bearer, a cookie or an echoed password would.
			body: `{"error":"internal",` +
				`"authorization":"Bearer ` + marker + `"}`,
			contentType: "application/json",
		}
	}
	return []hxc270PipelineCase{
		{
			name:   "site1_signin_failure_body",
			site:   1,
			login:  failure(hxc270TextMarker),
			stats:  hxc270OKStats(),
			search: hxc270OKSearch(),
			marker: hxc270TextMarker,
		},
		{
			name:   "site2_entity_stats_failure_body",
			site:   2,
			login:  hxc270OKLogin(),
			stats:  failure(hxc270TextMarker),
			search: hxc270OKSearch(),
			marker: hxc270TextMarker,
		},
		{
			name:   "site3_media_search_failure_body",
			site:   3,
			login:  hxc270OKLogin(),
			stats:  hxc270OKStats(),
			search: failure(hxc270TextMarker),
			marker: hxc270TextMarker,
		},
		{
			name:  "site4_entity_stats_decode_complaint",
			site:  4,
			login: hxc270OKLogin(),
			stats: hxc270Route{
				status: http.StatusOK,
				body: `{"total_entities":` +
					hxc270NumberMarker + `}`,
				contentType: "application/json",
			},
			search: hxc270OKSearch(),
			marker: hxc270NumberMarker,
		},
		{
			name:  "site5_media_search_decode_complaint",
			site:  5,
			login: hxc270OKLogin(),
			stats: hxc270OKStats(),
			search: hxc270Route{
				status: http.StatusOK,
				body: `{"total":` +
					hxc270NumberMarker + `}`,
				contentType: "application/json",
			},
			marker: hxc270NumberMarker,
		},
	}
}

// TestHXC270_FailedReplyBodyNeverReachesAFindingOrLog is the standing
// §11.4.135 guard for the five measured sites inside validateAPIData.
//
// Without it, restoring `Description: string(body)` at any of the
// three finding sites, or `%v` on the decode error at either log
// site, leaves the whole pkg/autonomous suite green — which would
// make the withholding decoration rather than a gate (repo CLAUDE.md
// §6: every gate ships with a §1.1 mutation proving it catches the
// regression it claims).
func TestHXC270_FailedReplyBodyNeverReachesAFindingOrLog(t *testing.T) {
	for _, tc := range hxc270PipelineCases() {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			srv := hxc270Server(t, tc.login, tc.stats, tc.search)
			defer srv.Close()

			sp := &SessionPipeline{
				config: dataValidationTestConfig(t, srv.URL),
			}

			var findings []analysis.AnalysisFinding
			stdout := hxc270CaptureStdout(t, func() {
				findings = sp.validateAPIData(context.Background())
			})

			// Asserted in BOTH polarities: the pipeline must
			// actually have exercised the route under test.
			// Without this a case that silently stopped reaching
			// the server would "pass" the leak assertion by
			// producing no output at all — the vacuous-guard
			// failure mode.
			require.Contains(t, stdout, "[data-validation]",
				"site %d: the pipeline must have run and "+
					"reported something", tc.site)

			if redModeOn() {
				// RED_MODE=1 reproduces the defect on the
				// PRE-FIX artifact: the marker planted in the
				// failure body comes back out. On the FIXED
				// artifact this polarity fails, which is the
				// point — it is the baseline capture, not a
				// standing assertion (§11.4.115).
				leaked := strings.Contains(stdout, tc.marker)
				for _, f := range findings {
					if strings.Contains(f.Description, tc.marker) ||
						strings.Contains(f.Title, tc.marker) {
						leaked = true
					}
				}
				assert.True(t, leaked,
					"RED_MODE=1: site %d must reproduce the "+
						"pre-fix echo — the marker planted in "+
						"the failure body must come back out in "+
						"a finding or a log line", tc.site)
				return
			}

			// ── GREEN: the standing guard ───────────────────
			// Asserted across EVERY finding, not just the one
			// the case targets, so a leak cannot hide in a
			// neighbouring entry.
			for _, f := range findings {
				assert.NotContains(t, f.Description, tc.marker,
					"site %d: a failed reply body must never be "+
						"echoed into a finding description (%q)",
					tc.site, f.Title)
				assert.NotContains(t, f.Title, tc.marker,
					"site %d: a failed reply body must never be "+
						"echoed into a finding title", tc.site)
			}
			assert.NotContains(t, stdout, tc.marker,
				"site %d: a failed reply body must never be "+
					"echoed into the session log — stdout is "+
					"captured and archived", tc.site)

			// Reporting NOTHING fails in the other direction, so
			// the diagnosis a reader needs must still be there.
			// Both classes of site keep a shape label; the three
			// finding sites additionally keep the byte length.
			assert.Contains(t, stdout+hxc270JoinFindings(findings),
				"JSON", "site %d: the withheld body must still "+
					"be described by shape", tc.site)
		})
	}
}

// hxc270JoinFindings flattens findings into one string so an
// assertion can look across all of them at once.
func hxc270JoinFindings(findings []analysis.AnalysisFinding) string {
	var b strings.Builder
	for _, f := range findings {
		b.WriteString(f.Title)
		b.WriteString("\n")
		b.WriteString(f.Description)
		b.WriteString("\n")
	}
	return b.String()
}

// TestHXC270_LoginHelperNeverEchoesReplyBodyIntoItsError is the
// standing guard for the two measured sites in the shared HTTP
// helper — the ones that PROPAGATE, since loginWithRetry's error
// travels up through applyAuth into an ActionResult message.
func TestHXC270_LoginHelperNeverEchoesReplyBodyIntoItsError(t *testing.T) {
	cases := []struct {
		name   string
		site   int
		login  hxc270Route
		marker string
	}{
		{
			name: "site6_login_failure_body_in_error",
			site: 6,
			login: hxc270Route{
				status: http.StatusInternalServerError,
				body: `{"error":"internal",` +
					`"authorization":"Bearer ` +
					hxc270TextMarker + `"}`,
				contentType: "application/json",
			},
			marker: hxc270TextMarker,
		},
		{
			name: "site7_login_decode_complaint_in_error",
			site: 7,
			login: hxc270Route{
				status: http.StatusOK,
				// Brace-balanced and json.Valid, yet
				// undecodable into map[string]any — the
				// measured path on which the decoder embeds
				// the whole literal in its error text.
				body:        `{"token":` + hxc270NumberMarker + `}`,
				contentType: "application/json",
			},
			marker: hxc270NumberMarker,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			srv := hxc270Server(
				t, tc.login, hxc270OKStats(), hxc270OKSearch(),
			)
			defer srv.Close()

			h := NewHTTPExecutor(srv.URL)
			tok, err := h.login(
				context.Background(),
				Credentials{
					Username: "admin",
					Password: "admin123",
				},
			)

			// Asserted in BOTH polarities: this is a FAILURE
			// path, so it must actually have failed. A case that
			// started succeeding would pass the leak assertion
			// vacuously.
			require.Error(t, err,
				"site %d: this reply must fail the login", tc.site)
			require.Empty(t, tok,
				"site %d: a failed login must yield no bearer",
				tc.site)

			if redModeOn() {
				assert.Contains(t, err.Error(), tc.marker,
					"RED_MODE=1: site %d must reproduce the "+
						"pre-fix echo — the marker planted in "+
						"the reply body must come back out in "+
						"the returned error", tc.site)
				return
			}

			assert.NotContains(t, err.Error(), tc.marker,
				"site %d: a failed sign-in reply body must never "+
					"be echoed into a returned error — this "+
					"error propagates up the call chain",
				tc.site)

			// Still diagnostic: the caller must be able to tell
			// what came back without seeing what came back.
			assert.Contains(t, err.Error(), "withheld",
				"site %d: the error must say the body was "+
					"withheld, so a reader does not mistake the "+
					"omission for a truncated message", tc.site)
			assert.Contains(t, err.Error(), "bytes",
				"site %d: the error must still carry the reply's "+
					"length", tc.site)
		})
	}
}

// hxc270FailedReplyDescriptionFormat is a TEST-LOCAL copy of the
// production format string.
//
// It is deliberately a second copy rather than a reference to
// failedReplyDescriptionFormat: an expectation rendered by calling
// the code under test agrees with ANY mutation of that code, so it
// would pin nothing. HXC-267 hit exactly this and resolved it the
// same way. Substring assertions cannot close the class either —
// Contains passes on supersets, and NotContains needs the complete
// secret, so a prefix echo of a longer secret defeats both.
const hxc270FailedReplyDescriptionFormat = "%s returned HTTP %d. Reply body " +
	"shape %q, length %d bytes, Content-Type %q. The body itself is " +
	"withheld on purpose: a failed reply may still carry a credential " +
	"— a sign-in route that echoes the submitted token, a session " +
	"cookie rendered into the body, or an authorization header quoted " +
	"back in diagnostic output. Status, shape, length and Content-Type " +
	"identify a wrong-system reply such as a proxy error page without " +
	"repeating what the server sent."

// TestHXC270_FailedReplyDescriptionIsExactlyPinned pins the exact
// bytes of the failure-path description.
//
// The leak guard above proves the body does not come out; this proves
// the replacement still says something useful. A fix that reported
// nothing would satisfy every NotContains assertion in this file and
// be just as broken, in the other direction.
//
// Every expected value is a literal written out here — the shape
// labels and the bounded Content-Type are NOT obtained by calling
// replyBodyShape or boundedContentType, which are themselves under
// test.
func TestHXC270_FailedReplyDescriptionIsExactlyPinned(t *testing.T) {
	longCT := strings.Repeat("x", undecodableBodyContentTypeMax+40)

	cases := []struct {
		name        string
		label       string
		status      int
		body        []byte
		contentType string
		wantShape   string
		wantCT      string
	}{
		{
			name:        "html_error_page",
			label:       "GET /api/v1/entities/stats",
			status:      503,
			body:        []byte("<html>503</html>"),
			contentType: "text/html; charset=utf-8",
			wantShape:   "not JSON",
			wantCT:      "text/html; charset=utf-8",
		},
		{
			name:        "json_object_error",
			label:       "POST /api/v1/auth/login",
			status:      401,
			body:        []byte(`{"error":"bad creds"}`),
			contentType: "application/json",
			wantShape:   "JSON object",
			wantCT:      "application/json",
		},
		{
			name:        "empty_body",
			label:       "GET /api/v1/media/search",
			status:      500,
			body:        []byte(""),
			contentType: "",
			wantShape:   "empty",
			wantCT:      "<none>",
		},
		{
			name:        "json_array_error",
			label:       "GET /x",
			status:      422,
			body:        []byte(`[{"field":"name"}]`),
			contentType: "application/json",
			wantShape:   "JSON array",
			wantCT:      "application/json",
		},
		{
			// The Content-Type bound is load-bearing: it is the
			// one server-controlled string this description
			// carries, so an unbounded header would reopen a
			// (smaller) version of the same problem.
			name:        "content_type_is_bounded",
			label:       "GET /x",
			status:      500,
			body:        []byte("!"),
			contentType: longCT,
			wantShape:   "not JSON",
			wantCT: strings.Repeat(
				"x", undecodableBodyContentTypeMax,
			) + "…",
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			want := fmt.Sprintf(
				hxc270FailedReplyDescriptionFormat,
				tc.label, tc.status, tc.wantShape,
				len(tc.body), tc.wantCT,
			)
			got := failedReplyDescription(
				tc.label, tc.status, tc.body, tc.contentType,
			)
			assert.Equal(t, want, got,
				"the failure-path description is pinned byte for "+
					"byte; if this changed on purpose, update the "+
					"test-local format copy in the same commit")
			// Skipped for the empty body: every string contains
			// "", so this assertion is degenerate there — it
			// would pass unconditionally and read as coverage.
			if len(tc.body) > 0 {
				assert.NotContains(t, got, string(tc.body),
					"the description must never contain the body")
			}
		})
	}
}

// TestHXC270_DecodeFailureDetailNeverReadsErrorText pins the
// replacement for the decode-complaint wrapping: a position and a
// shape, never a quoted character.
//
// Each case asserts BOTH that the expected non-echoing detail is
// produced AND that the decoder's own error text — recomputed
// test-locally, never obtained from the code under test — does not
// appear in it.
func TestHXC270_DecodeFailureDetailNeverReadsErrorText(t *testing.T) {
	type statsShape struct {
		Total  int            `json:"total_entities"`
		ByType map[string]int `json:"by_type"`
	}

	cases := []struct {
		name string
		body []byte
		// decodeInto selects the target, because WHICH error the
		// decoder produces (and therefore which bytes it embeds)
		// depends on it.
		intoStats bool
		want      string
		// leaked is the substring the raw error text carries and
		// the detail must not.
		leaked string
	}{
		{
			name:   "html_syntax_error_quotes_a_character",
			body:   []byte("<html>503</html>"),
			want:   "not JSON, decoding gave up at byte offset 1",
			leaked: "invalid character",
		},
		{
			name:   "invalid_utf8_quotes_a_character",
			body:   []byte{0xff, 0xfe, 0x00},
			want:   "not JSON, decoding gave up at byte offset 1",
			leaked: "invalid character",
		},
		{
			name:      "unrepresentable_number_carries_the_literal",
			body:      []byte(`{"total_entities":` + hxc270NumberMarker + `}`),
			intoStats: true,
			// No offset: this is an UnmarshalTypeError, not a
			// SyntaxError, so there is no Offset to report and the
			// detail is the shape alone. That asymmetry is real
			// and worth pinning — it is the negative control
			// proving the offset clause discriminates rather than
			// firing unconditionally.
			want:   "truncated or malformed JSON object",
			leaked: hxc270NumberMarker,
		},
		{
			name:   "empty_body",
			body:   []byte(""),
			want:   "empty, decoding gave up at byte offset 0",
			leaked: "unexpected end",
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			var jErr error
			if tc.intoStats {
				var v statsShape
				jErr = json.Unmarshal(tc.body, &v)
			} else {
				var v map[string]any
				jErr = json.Unmarshal(tc.body, &v)
			}
			require.Error(t, jErr,
				"the fixture must actually fail to decode")

			got := decodeFailureDetail(jErr, tc.body)
			assert.Equal(t, tc.want, got,
				"the decode-failure detail is pinned exactly")
			assert.NotContains(t, got, tc.leaked,
				"the detail must never carry the decoder's own "+
					"error text — that text quotes input bytes")
			assert.NotContains(t, got, jErr.Error(),
				"the detail must not embed the raw decode error")
		})
	}
}
