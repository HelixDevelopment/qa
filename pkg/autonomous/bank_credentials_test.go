// SPDX-FileCopyrightText: 2026 Milos Vasic
// SPDX-License-Identifier: Apache-2.0

package autonomous

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"digital.vasic.helixqa/pkg/testbank"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// No shipped bank, guide or script may carry the old built-in credential.
func TestShippedFiles_CarryNoDefaultCredential(t *testing.T) {
	root := filepath.Join("..", "..")
	var offenders []string
	for _, dir := range []string{"banks", "website", "scripts", "cmd"} {
		_ = filepath.Walk(filepath.Join(root, dir), func(p string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() || strings.HasSuffix(p, "_test.go") {
				return nil
			}
			b, rerr := os.ReadFile(p)
			if rerr != nil {
				return nil
			}
			if strings.Contains(string(b), "admin123") {
				offenders = append(offenders, p)
			}
			return nil
		})
	}
	assert.Empty(t, offenders, "files still carry the default credential")
}

func bodyRecorder(t *testing.T) (*httptest.Server, func() []string) {
	t.Helper()
	var mu sync.Mutex
	var got []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		got = append(got, r.Method+" "+r.URL.Path+" "+string(b))
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(srv.Close)
	return srv, func() []string { mu.Lock(); defer mu.Unlock(); return append([]string(nil), got...) }
}

// Bank bodies reference credentials by variable NAME; the runner resolves them
// from the environment at request time.
func TestHTTPExecutor_BodyTestVarsResolvedFromEnv(t *testing.T) {
	t.Setenv("HELIX_TEST_USER", "bank-user")
	t.Setenv("HELIX_TEST_PASSWORD", `p"w`)
	srv, reqs := bodyRecorder(t)
	h := NewHTTPExecutor(srv.URL)
	res := h.Execute(context.Background(), "POST", "/api/v1/auth/login", testbank.TestStep{
		Body:         map[string]any{"username": "${HELIX_TEST_USER}", "password": "${HELIX_TEST_PASSWORD}"},
		ExpectStatus: 200,
	})
	require.True(t, res.Success, res.Message)
	got := reqs()
	require.Len(t, got, 1)
	assert.Contains(t, got[0], `"username":"bank-user"`)
	assert.Contains(t, got[0], `"password":"p\"w"`)
	assert.NotContains(t, got[0], "${")
}

func TestHTTPExecutor_BodyTestVarsUnset_BlocksAndSendsNothing(t *testing.T) {
	t.Setenv("HELIX_TEST_USER", "")
	t.Setenv("HELIX_TEST_PASSWORD", "")
	srv, reqs := bodyRecorder(t)
	h := NewHTTPExecutor(srv.URL)
	res := h.Execute(context.Background(), "POST", "/api/v1/auth/login", testbank.TestStep{
		Body:         map[string]string{"username": "${HELIX_TEST_USER}", "password": "${HELIX_TEST_PASSWORD}"},
		ExpectStatus: 200,
	})
	assert.False(t, res.Success)
	assert.True(t, res.Blocked, "an unset credential variable is 'could not determine', never a pass or an app fail")
	assert.Empty(t, reqs(), "nothing may be sent with an unresolved credential variable")
	assert.Contains(t, res.Message, "HELIX_TEST_PASSWORD")
}

func TestExpandTestVars(t *testing.T) {
	t.Setenv("HELIX_TEST_USER", "u1")
	t.Setenv("HELIX_TEST_PASSWORD", "")
	out, missing := expandTestVars("a ${HELIX_TEST_USER} b ${HELIX_TEST_PASSWORD} c ${HOME}")
	assert.Equal(t, "a u1 b ${HELIX_TEST_PASSWORD} c ${HOME}", out, "only HELIX_TEST_* names are ever expanded")
	assert.Equal(t, []string{"HELIX_TEST_PASSWORD"}, missing)
}
