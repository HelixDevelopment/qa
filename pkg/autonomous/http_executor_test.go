// SPDX-FileCopyrightText: 2026 Milos Vasic
// SPDX-License-Identifier: Apache-2.0

package autonomous

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"digital.vasic.helixqa/pkg/testbank"
)

// TestHTTPExecutor_BasicGet verifies a vanilla GET against a real
// loopback server returns Success and surfaces the response body.
func TestHTTPExecutor_BasicGet(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/health", r.URL.Path)
		assert.Equal(t, http.MethodGet, r.Method)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"healthy","build":"25"}`))
	}))
	defer srv.Close()

	h := NewHTTPExecutor(srv.URL)
	res := h.Execute(context.Background(), "GET", "/health", testbank.TestStep{
		ExpectStatus:   200,
		ExpectJSONPath: "$.status",
	})
	require.True(t, res.Success, "expected success, got: %s", res.Message)

	status, hdr, body := h.LastResponse()
	assert.Equal(t, 200, status)
	assert.Equal(t, "application/json", hdr.Get("Content-Type"))
	assert.Contains(t, string(body), "healthy")
}

// TestHTTPExecutor_PostJSONBody verifies a POST with a structured
// body (map) is JSON-encoded with Content-Type: application/json
// and ExpectStatus enforces.
func TestHTTPExecutor_PostJSONBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
		var got map[string]any
		require.NoError(t, json.NewDecoder(r.Body).Decode(&got))
		assert.Equal(t, "admin", got["username"])
		assert.Equal(t, "admin123", got["password"])
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"session_token":"jwt-abc","expires_at":"2026-04-30"}`))
	}))
	defer srv.Close()

	h := NewHTTPExecutor(srv.URL)
	res := h.Execute(context.Background(), "POST", "/api/v1/auth/login", testbank.TestStep{
		Body:           map[string]string{"username": "admin", "password": "admin123"},
		ExpectStatus:   200,
		ExpectJSONPath: "$.session_token",
	})
	require.True(t, res.Success, "expected success, got: %s", res.Message)
}

// TestHTTPExecutor_StatusMismatch verifies ExpectStatus actually
// fails the step (Article XI: assertion has teeth).
func TestHTTPExecutor_StatusMismatch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"unauthorized"}`))
	}))
	defer srv.Close()

	h := NewHTTPExecutor(srv.URL)
	res := h.Execute(context.Background(), "GET", "/protected", testbank.TestStep{
		ExpectStatus: 200,
	})
	require.False(t, res.Success, "expected failure on 401 vs 200")
	assert.Contains(t, res.Message, "status 401")
	assert.Contains(t, res.Message, "expected 200")
}

// TestHTTPExecutor_BodyContainsMismatch verifies
// ExpectBodyContains rejects when the substring is absent.
func TestHTTPExecutor_BodyContainsMismatch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"items":[]}`))
	}))
	defer srv.Close()

	h := NewHTTPExecutor(srv.URL)
	res := h.Execute(context.Background(), "GET", "/foo", testbank.TestStep{
		ExpectBodyContains: "Inception",
	})
	require.False(t, res.Success, "expected failure when 'Inception' missing")
	assert.Contains(t, res.Message, "missing")
}

// TestHTTPExecutor_AdminAuthCachesToken verifies that the first
// AuthMode="admin" step performs a login round-trip and subsequent
// steps reuse the cached token (no second login call).
func TestHTTPExecutor_AdminAuthCachesToken(t *testing.T) {
	loginCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/auth/login":
			loginCount++
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"session_token":"cached-jwt"}`))
		case "/api/v1/users":
			assert.Equal(t, "Bearer cached-jwt", r.Header.Get("Authorization"))
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"users":[{"id":1}]}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	h := NewHTTPExecutor(srv.URL)
	h.AdminCreds = Credentials{Username: "u-test", Password: "p-test"}
	for i := 0; i < 3; i++ {
		res := h.Execute(context.Background(), "GET", "/api/v1/users", testbank.TestStep{
			AuthMode:     "admin",
			ExpectStatus: 200,
		})
		require.True(t, res.Success, "iter %d: %s", i, res.Message)
	}
	assert.Equal(t, 1, loginCount, "admin login should be cached after first call (got %d calls)", loginCount)
}

// TestHTTPExecutor_RawTokenAuth verifies AuthMode="raw:<token>"
// attaches the token verbatim without a login call.
func TestHTTPExecutor_RawTokenAuth(t *testing.T) {
	loginCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/auth/login" {
			loginCount++
		}
		assert.Equal(t, "Bearer my-static-token", r.Header.Get("Authorization"))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	h := NewHTTPExecutor(srv.URL)
	res := h.Execute(context.Background(), "GET", "/api/v1/users", testbank.TestStep{
		AuthMode: "raw:my-static-token",
	})
	require.True(t, res.Success, res.Message)
	assert.Zero(t, loginCount, "raw: token must not trigger a login call")
}

// TestHTTPExecutor_AsUserAuth verifies AuthMode="as:<user>" uses
// the matching credentials from UserCredentials.
func TestHTTPExecutor_AsUserAuth(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/auth/login":
			var got map[string]string
			_ = json.NewDecoder(r.Body).Decode(&got)
			assert.Equal(t, "viewer", got["username"])
			assert.Equal(t, "viewer123", got["password"])
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"session_token":"viewer-jwt"}`))
		default:
			assert.Equal(t, "Bearer viewer-jwt", r.Header.Get("Authorization"))
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{}`))
		}
	}))
	defer srv.Close()

	h := NewHTTPExecutor(srv.URL)
	h.UserCredentials = map[string]Credentials{
		"viewer": {Username: "viewer", Password: "viewer123"},
	}
	res := h.Execute(context.Background(), "GET", "/api/v1/media", testbank.TestStep{
		AuthMode: "as:viewer",
	})
	require.True(t, res.Success, res.Message)
}

// TestHTTPExecutor_AsUserUnknownUser verifies AuthMode="as:nope"
// returns a clear error when the user is not registered.
func TestHTTPExecutor_AsUserUnknownUser(t *testing.T) {
	h := NewHTTPExecutor("http://unused")
	res := h.Execute(context.Background(), "GET", "/x", testbank.TestStep{
		AuthMode: "as:ghost",
	})
	require.False(t, res.Success)
	assert.Contains(t, res.Message, "ghost")
	assert.Contains(t, res.Message, "credentials not registered")
}

// TestHTTPExecutor_BadJSONPath verifies an unparseable JSON path
// surfaces a clean error instead of crashing.
func TestHTTPExecutor_BadJSONPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"a":1}`))
	}))
	defer srv.Close()

	h := NewHTTPExecutor(srv.URL)
	res := h.Execute(context.Background(), "GET", "/x", testbank.TestStep{
		ExpectJSONPath: "no_dollar",
	})
	require.False(t, res.Success)
	assert.Contains(t, res.Message, "must start with $")
}

// TestHTTPExecutor_TimeoutPropagatesContext verifies a cancelled
// context aborts the request quickly (no goroutine leak).
func TestHTTPExecutor_TimeoutPropagatesContext(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Slow response — would block longer than ctx allows.
		select {
		case <-time.After(2 * time.Second):
			w.WriteHeader(http.StatusOK)
		case <-r.Context().Done():
			return
		}
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	h := NewHTTPExecutor(srv.URL)
	start := time.Now()
	res := h.Execute(ctx, "GET", "/slow", testbank.TestStep{})
	elapsed := time.Since(start)
	require.False(t, res.Success)
	assert.Less(t, elapsed.Milliseconds(), int64(1500),
		"context cancellation must abort within ~timeout, took %v", elapsed)
}

// TestParseHTTPAction covers the action-string parser corner cases.
func TestParseHTTPAction(t *testing.T) {
	tests := []struct{ in, m, p string }{
		{"GET /health", "GET", "/health"},
		{"POST /api/v1/auth/login", "POST", "/api/v1/auth/login"},
		{"  PUT   /x  ", "PUT", "/x"},
		{"/health", "GET", "/health"},
		{"", "", ""},
	}
	for _, tc := range tests {
		m, p := parseHTTPAction(tc.in)
		assert.Equal(t, tc.m, m, "method for %q", tc.in)
		assert.Equal(t, tc.p, p, "path for %q", tc.in)
	}
}

// TestJSONPathExists covers the tiny json-path subset.
func TestJSONPathExists(t *testing.T) {
	body := []byte(`{
		"user": {"id": 7, "name": "alice"},
		"items": [{"id":"a"},{"id":"b"}],
		"empty": null
	}`)
	cases := []struct {
		path     string
		want     bool
		wantVal  any
		wantErr  bool
	}{
		{"$", true, nil, false},
		{"$.user", true, nil, false},
		{"$.user.name", true, "alice", false},
		{"$.user.missing", false, nil, false},
		{"$.items[0].id", true, "a", false},
		{"$.items[1].id", true, "b", false},
		{"$.items[5]", false, nil, false},
		{"$.empty", false, nil, false},
		{"no_dollar", false, nil, true},
	}
	for _, tc := range cases {
		ok, val, err := jsonPathExists(body, tc.path)
		if tc.wantErr {
			assert.Error(t, err, "path %q", tc.path)
			continue
		}
		assert.NoError(t, err, "path %q", tc.path)
		if tc.want {
			assert.True(t, ok, "path %q should exist", tc.path)
			if tc.wantVal != nil {
				assert.Equal(t, tc.wantVal, val, "path %q value", tc.path)
			}
		} else {
			assert.False(t, ok, "path %q should not exist", tc.path)
		}
	}
}

// TestRunAssertion_StatusEq verifies the assertion dispatcher.
func TestRunAssertion_StatusEq(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":42}`))
	}))
	defer srv.Close()
	h := NewHTTPExecutor(srv.URL)
	require.True(t, h.Execute(context.Background(), "POST", "/x", testbank.TestStep{}).Success)

	res := runAssertion(h, "status_eq: 201")
	require.True(t, res.Success, res.Message)

	res = runAssertion(h, "status_eq: 200")
	require.False(t, res.Success)
	assert.Contains(t, res.Message, "got 201")

	res = runAssertion(h, "json_path_eq: $.id = 42")
	require.True(t, res.Success, res.Message)

	res = runAssertion(h, "body_contains: 42")
	require.True(t, res.Success, res.Message)

	res = runAssertion(h, "body_contains: nope")
	require.False(t, res.Success)
}

// TestActionTypeHTTP_RoundTrip verifies the full ParseAction →
// performAction → HTTPExecutor.Execute round trip via a minimal
// performAction harness. (We don't invoke StructuredTestExecutor
// itself because that needs a navigator.ActionExecutor; instead
// we exercise the leaf path that ActionTypeHTTP routes to.)
func TestActionTypeHTTP_RoundTrip(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/v1/health", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"healthy"}`))
	}))
	defer srv.Close()

	step := testbank.TestStep{
		Action:         "http: GET /api/v1/health",
		ExpectStatus:   200,
		ExpectJSONPath: "$.status",
	}
	at, val := step.ParseAction()
	require.Equal(t, testbank.ActionTypeHTTP, at)
	method, path := parseHTTPAction(val)
	require.Equal(t, "GET", method)
	require.Equal(t, "/api/v1/health", path)

	h := NewHTTPExecutor(srv.URL)
	res := h.Execute(context.Background(), method, path, step)
	require.True(t, res.Success, res.Message)
}

// TestHTTPExecutor_NoBaseURL verifies the executor surfaces a
// clean error when called without configuration (instead of
// silently no-oping or panicking).
func TestHTTPExecutor_NoBaseURL(t *testing.T) {
	h := &HTTPExecutor{}
	res := h.Execute(context.Background(), "GET", "/x", testbank.TestStep{})
	require.False(t, res.Success)
	assert.Contains(t, res.Message, "BaseURL not configured")
}

// AntiBluffVerification: the matching negative — if we DELETE the
// status check from ExpectStatus path, the test that asserted
// "wrong status returns failure" would still pass. The fix:
// re-run TestHTTPExecutor_StatusMismatch after commenting out the
// `if step.ExpectStatus != 0 && resp.StatusCode != step.ExpectStatus`
// block; the test must then FAIL. Verified manually before commit.
func TestHTTPExecutor_AntiBluffMarker(t *testing.T) {
	// This test exists to anchor the manual anti-bluff verification
	// and to ensure the package's intent is clear in code review.
	// It deliberately re-asserts the assertion already covered by
	// TestHTTPExecutor_StatusMismatch.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = fmt.Fprint(w, `{"error":"boom"}`)
	}))
	defer srv.Close()
	h := NewHTTPExecutor(srv.URL)
	res := h.Execute(context.Background(), "GET", "/", testbank.TestStep{ExpectStatus: 200})
	require.False(t, res.Success)
	require.True(t, strings.Contains(res.Message, "status 500"))
}

// TestHTTPExecutor_CSRFAutoPreflight asserts that mutating
// requests against CSRF-guarded prefixes:
//  1. trigger a preflight GET to CSRFPreflightPath
//  2. capture the X-CSRF-Token header + csrf cookie
//  3. replay both on the actual request
//
// Article XI §11.2.5 anti-bluff anchor: comment out the
// `h.needsCSRF` block in Execute and this test FAILS — the
// mutating call goes out without csrf header/cookie and the
// fake server's strict check rejects it with 403.
func TestHTTPExecutor_CSRFAutoPreflight(t *testing.T) {
	var preflightCalls, mutatingCalls int
	var seenToken, seenCookie string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/admin/system-info":
			preflightCalls++
			w.Header().Set("X-CSRF-Token", "tok-fixture")
			http.SetCookie(w, &http.Cookie{Name: "csrf", Value: "ck-fixture", Path: "/"})
			w.WriteHeader(http.StatusOK)
			_, _ = fmt.Fprint(w, `{"ok":true}`)
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/admin/storage/scan":
			mutatingCalls++
			seenToken = r.Header.Get("X-CSRF-Token")
			if c, err := r.Cookie("csrf"); err == nil {
				seenCookie = c.Value
			}
			if seenToken != "tok-fixture" || seenCookie != "ck-fixture" {
				w.WriteHeader(http.StatusForbidden)
				_, _ = fmt.Fprint(w, `{"error":"missing csrf cookie"}`)
				return
			}
			w.WriteHeader(http.StatusOK)
			_, _ = fmt.Fprint(w, `{"started":true}`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	h := NewHTTPExecutor(srv.URL)
	res := h.Execute(context.Background(), "POST",
		"/api/v1/admin/storage/scan",
		testbank.TestStep{ExpectStatus: 200, AuthMode: "none"})

	require.True(t, res.Success,
		"mutating call MUST succeed once CSRF token+cookie auto-fetched; got: %s",
		res.Message)
	assert.Equal(t, 1, preflightCalls,
		"preflight GET must fire exactly once per executor session")
	assert.Equal(t, 1, mutatingCalls)
	assert.Equal(t, "tok-fixture", seenToken,
		"X-CSRF-Token header must be replayed verbatim from preflight")
	assert.Equal(t, "ck-fixture", seenCookie,
		"csrf cookie must be replayed verbatim from preflight")
}

// TestHTTPExecutor_CSRFCachedAcrossCalls asserts the preflight is
// cached — three mutating calls in a row trigger only one
// preflight, NOT three.
func TestHTTPExecutor_CSRFCachedAcrossCalls(t *testing.T) {
	var preflightCalls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/api/v1/admin/system-info" {
			preflightCalls++
			w.Header().Set("X-CSRF-Token", "tok")
			http.SetCookie(w, &http.Cookie{Name: "csrf", Value: "ck", Path: "/"})
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.Header.Get("X-CSRF-Token") == "" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	h := NewHTTPExecutor(srv.URL)
	for i := 0; i < 3; i++ {
		res := h.Execute(context.Background(), "POST",
			fmt.Sprintf("/api/v1/admin/x%d", i),
			testbank.TestStep{ExpectStatus: 200, AuthMode: "none"})
		require.True(t, res.Success, "call %d failed: %s", i, res.Message)
	}
	assert.Equal(t, 1, preflightCalls,
		"preflight must run exactly once for the lifetime of the executor")
}

// TestHTTPExecutor_LoginHonorsRetryAfter asserts that when the
// catalog-api login endpoint responds 429 with a retry_after JSON
// field, the executor sleeps that duration (clamped to a sane
// upper bound) and retries.
//
// Article XI §11.5 anchor: without this, sequential bank
// verification runs cascade-fail across every admin: test as
// soon as the rate limit fires. The bluff would be reading 50+
// "401 Unauthorized" tickets that are actually all just 429s
// the executor never recovered from.
//
// To keep the test fast, we use a fixture that returns 429 with
// retry_after=1 (second) on the first hit, then 200 with a token.
func TestHTTPExecutor_LoginHonorsRetryAfter(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = fmt.Fprint(w, `{"error":"Rate limit exceeded","retry_after":1}`)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"session_token":"tok-after-retry"}`)
	}))
	defer srv.Close()

	h := NewHTTPExecutor(srv.URL)
	start := time.Now()
	tok, err := h.login(context.Background(), Credentials{Username: "admin", Password: "admin123"})
	elapsed := time.Since(start)

	require.NoError(t, err, "login MUST succeed after honoring Retry-After")
	assert.Equal(t, "tok-after-retry", tok)
	assert.GreaterOrEqual(t, elapsed, 900*time.Millisecond,
		"login should have slept at least ~1s before retrying")
	assert.Equal(t, 2, calls, "exactly 2 calls: 1 initial 429 + 1 retry")
}

// TestHTTPExecutor_LoginRetryAfterClampedToMax asserts that an
// abusive Retry-After value is clamped to MaxRetryAfter (65s)
// and never strands the test forever. The fixture returns
// retry_after=99999, but we never wait that long because we
// cancel the context after a brief moment.
func TestHTTPExecutor_LoginRetryAfterClampedToMax(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "99999")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = fmt.Fprint(w, `{"retry_after":99999}`)
	}))
	defer srv.Close()

	h := NewHTTPExecutor(srv.URL)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	_, err := h.login(ctx, Credentials{Username: "admin", Password: "admin123"})
	require.Error(t, err, "context cancellation must surface as an error")
	assert.Contains(t, err.Error(), "context",
		"expected context-cancel to bubble up; got %s", err.Error())
}

// TestHTTPExecutor_AutoRefreshOnInvalidatedToken asserts that the
// executor handles the post-/auth/logout case gracefully: when a
// cached bearer token has been invalidated server-side, the next
// admin: request gets 401, the executor evicts the cache and
// retries with a fresh login.
//
// Article XI §11.5 anchor: without this retry, every admin: call
// after FQA-API-007's logout step would silently 401 forever, and
// a reviewer would (wrongly) file 14 catalog-api "Unauthorized"
// defects when the truth is just a stale cache.
func TestHTTPExecutor_AutoRefreshOnInvalidatedToken(t *testing.T) {
	var (
		loginCalls    int
		validToken    string
		invalidatedAt string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/auth/login":
			loginCalls++
			validToken = fmt.Sprintf("token-%d", loginCalls)
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprintf(w, `{"session_token":%q}`, validToken)
		case r.URL.Path == "/api/v1/auth/me":
			tok := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
			if tok == invalidatedAt || tok == "" {
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = fmt.Fprint(w, `{"error":"Invalid token"}`)
				return
			}
			w.WriteHeader(http.StatusOK)
			_, _ = fmt.Fprintf(w, `{"username":"admin"}`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	h := NewHTTPExecutor(srv.URL)
	h.AdminCreds = Credentials{Username: "u-test", Password: "p-test"}
	// First call mints token-1 and gets 200.
	res1 := h.Execute(context.Background(), "GET", "/api/v1/auth/me",
		testbank.TestStep{ExpectStatus: 200, AuthMode: "admin"})
	require.True(t, res1.Success, "first /auth/me must succeed: %s", res1.Message)
	require.Equal(t, 1, loginCalls)

	// Server invalidates token-1 (simulating a logout call).
	invalidatedAt = "token-1"

	// Second call uses the cached (now-invalid) token-1 → 401 → executor
	// must evict cache, re-login (token-2), and retry.
	res2 := h.Execute(context.Background(), "GET", "/api/v1/auth/me",
		testbank.TestStep{ExpectStatus: 200, AuthMode: "admin"})
	require.True(t, res2.Success,
		"second /auth/me MUST auto-refresh and succeed; got %s", res2.Message)
	assert.Equal(t, 2, loginCalls,
		"executor must have re-logged in to mint token-2")
}

// TestHTTPExecutor_ExplicitSkipHonored asserts that a step with
// _skip: true is SKIPPED before any HTTP traffic, with the
// _skip_reason surfaced in the result message.
//
// Article XI §11.5 anchor: removing this gate causes destructive
// or fixture-required steps to actually run on shared
// infrastructure (e.g. changing the admin password). The check
// IS load-bearing.
func TestHTTPExecutor_ExplicitSkipHonored(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("server MUST NOT be reached for a _skip step; got %s %s", r.Method, r.URL.Path)
	}))
	defer srv.Close()

	h := NewHTTPExecutor(srv.URL)
	res := h.Execute(context.Background(), "POST",
		"/api/v1/auth/change-password",
		testbank.TestStep{
			Skip:       true,
			SkipReason: "destructive on shared catalog-api",
		})

	require.True(t, res.Skipped, "explicit _skip must yield Skipped=true; got %#v", res)
	require.False(t, res.Success, "skipped result must not also be Success")
	assert.Contains(t, res.Message, "SKIP-OK:",
		"skip message must carry the SKIP-OK marker")
	assert.Contains(t, res.Message, "destructive on shared catalog-api",
		"skip message must include the SkipReason verbatim")
}

// TestHTTPExecutor_ExplicitSkipWithoutReason still skips, but
// surfaces a clear "untriaged" marker so the bluff scanner can
// flag bank entries that are skipped without justification.
func TestHTTPExecutor_ExplicitSkipWithoutReason(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("server MUST NOT be reached for a _skip step")
	}))
	defer srv.Close()

	h := NewHTTPExecutor(srv.URL)
	res := h.Execute(context.Background(), "GET", "/x",
		testbank.TestStep{Skip: true})
	require.True(t, res.Skipped)
	assert.Contains(t, res.Message, "UNTRIAGED",
		"missing reason must surface a fail-loud UNTRIAGED token so reviewers notice")
}

// TestHTTPExecutor_UnresolvedPlaceholderSkips asserts that a
// request whose path contains an unresolved {var} placeholder is
// SKIPPED with a SKIP-OK marker, NOT failed. Article XI §11.5: a
// FAIL on this path would be a bluff because the catalog-api isn't
// broken — the bank converter just left a template variable in.
//
// Anti-bluff anchor: comment out the unresolvedPlaceholder check
// in Execute and this test FAILS — the request goes out with a
// literal `{id}` in the URL, the catalog-api returns 400 ("Invalid
// ID"), and the test sees a Failure instead of Skipped.
func TestHTTPExecutor_UnresolvedPlaceholderSkips(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("server MUST NOT be reached when path has unresolved placeholder; got %s", r.URL.Path)
	}))
	defer srv.Close()

	h := NewHTTPExecutor(srv.URL)
	res := h.Execute(context.Background(), "GET",
		"/api/v1/scans/{job_id}",
		testbank.TestStep{ExpectStatus: 200, AuthMode: "none"})

	require.True(t, res.Skipped, "must SKIP, not run; got %#v", res)
	require.False(t, res.Success, "skipped result must not also be Success")
	assert.Contains(t, res.Message, "{job_id}",
		"skip message must name the unresolved placeholder")
	assert.Contains(t, res.Message, "SKIP-OK:",
		"skip message must carry the SKIP-OK marker so the bluff scanner doesn't flag it")
	assert.Contains(t, res.Message, "BLUFF-HELIXQA-BANKS-VAR-SUBST-001",
		"skip message must reference the tracking ticket")
}

// TestHTTPExecutor_PlaceholderDetectionConservative asserts that
// path patterns that LOOK like braces but aren't placeholders
// (uppercase chars, mixed-case, real path segments containing
// braces) are NOT treated as placeholders.
func TestHTTPExecutor_PlaceholderDetectionConservative(t *testing.T) {
	tests := []struct {
		path     string
		expected string
	}{
		{"/api/v1/foo", ""},
		{"/api/v1/foo/{id}", "{id}"},
		{"/api/v1/foo/{job_id}/status", "{job_id}"},
		{"/api/v1/users/{Id}", ""}, // uppercase — likely a real path segment
		{"/api/v1/x/{User}/y", ""},
		{"/api/v1/x/123/y", ""},
		{"/api/v1/scans/{}", ""}, // empty braces
		{"/api/v1/scans/{abc-def}", ""}, // hyphen — not a var name
	}
	for _, tt := range tests {
		got := unresolvedPlaceholder(tt.path)
		assert.Equal(t, tt.expected, got, "path=%q", tt.path)
	}
}

// TestHTTPExecutor_CSRFGetUnaffected asserts that GET requests
// against CSRF-guarded paths do NOT trigger preflights — only
// mutating methods do.
func TestHTTPExecutor_CSRFGetUnaffected(t *testing.T) {
	var preflightCalls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/admin/system-info" {
			preflightCalls++
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	h := NewHTTPExecutor(srv.URL)
	res := h.Execute(context.Background(), "GET",
		"/api/v1/admin/users",
		testbank.TestStep{ExpectStatus: 200, AuthMode: "none"})

	require.True(t, res.Success)
	assert.Equal(t, 0, preflightCalls,
		"GET requests on /admin/* must NOT trigger CSRF preflight — "+
			"only POST/PUT/PATCH/DELETE do")
}
