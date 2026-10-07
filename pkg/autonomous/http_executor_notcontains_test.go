package autonomous

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"digital.vasic.helixqa/pkg/testbank"
)

func strp(s string) *string { return &s }

func notContainsServer(body string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(body))
	}))
}

// A forbidden substring present in the body MUST fail the step. This is the
// whole point of the field: a redacted passage id appearing in a search
// response has to turn the case red.
func TestHTTPExecutor_BodyNotContainsFailsWhenForbiddenPresent(t *testing.T) {
	srv := notContainsServer(`{"hits":[{"pid":"01FORBIDDENPID"}]}`)
	defer srv.Close()

	res := NewHTTPExecutor(srv.URL).Execute(context.Background(), "GET", "/x", testbank.TestStep{
		ExpectStatus:          200,
		ExpectBodyNotContains: strp("01FORBIDDENPID"),
	})
	require.False(t, res.Success, "forbidden substring present must fail")
	assert.Contains(t, res.Message, "must not contain")
}

func TestHTTPExecutor_BodyNotContainsPassesWhenForbiddenAbsent(t *testing.T) {
	srv := notContainsServer(`{"hits":[{"pid":"01OTHER"}]}`)
	defer srv.Close()

	res := NewHTTPExecutor(srv.URL).Execute(context.Background(), "GET", "/x", testbank.TestStep{
		ExpectStatus:          200,
		ExpectBodyNotContains: strp("01FORBIDDENPID"),
	})
	require.True(t, res.Success, "absent forbidden substring must pass: %s", res.Message)
}

// An explicitly empty value would match every body, so the step could never
// pass; refuse at run time as well, with a message that says why.
func TestHTTPExecutor_BodyNotContainsEmptyIsAnError(t *testing.T) {
	srv := notContainsServer(`{}`)
	defer srv.Close()

	res := NewHTTPExecutor(srv.URL).Execute(context.Background(), "GET", "/x", testbank.TestStep{
		ExpectBodyNotContains: strp(""),
	})
	require.False(t, res.Success)
	assert.Contains(t, res.Message, "empty")
}

// A body that is cut short (the server promises more bytes than it sends) must
// FAIL the step. The executor used to discard the read error, so the negative
// assertion ran over a truncated body and passed even though the forbidden
// text might have been in the part never read.
func TestHTTPExecutor_TruncatedBodyFailsTheStepInsteadOfPassingTheNegativeAssertion(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "4096")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"hits":[]}`)) // 11 of the promised 4096 bytes
	}))
	defer srv.Close()

	res := NewHTTPExecutor(srv.URL).Execute(context.Background(), "GET", "/x", testbank.TestStep{
		ExpectStatus:          200,
		ExpectBodyNotContains: strp("01FORBIDDENPID"),
	})
	require.False(t, res.Success, "a truncated body cannot prove the forbidden text is absent")
	assert.Contains(t, res.Message, "body")
}
