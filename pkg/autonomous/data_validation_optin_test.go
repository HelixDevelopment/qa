// SPDX-FileCopyrightText: 2026 Milos Vasic
// SPDX-License-Identifier: Apache-2.0

package autonomous

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// dvRecorder is a real local listener (not a mock of HelixQA code): it records
// every request the data-validation step sends to the target.
type dvRecorder struct {
	mu   sync.Mutex
	reqs []string // "METHOD path body"
}

func (r *dvRecorder) snapshot() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.reqs...)
}

func dvServer(t *testing.T) (*httptest.Server, *dvRecorder) {
	t.Helper()
	rec := &dvRecorder{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		rec.mu.Lock()
		rec.reqs = append(rec.reqs, r.Method+" "+r.URL.Path+" "+string(b))
		rec.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/v1/auth/login" {
			_, _ = w.Write([]byte(`{"token":"t0k"}`))
			return
		}
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(srv.Close)
	return srv, rec
}

// dataValidationTestConfig returns a config with the step explicitly enabled
// and credentials supplied through the named environment variables.
func dataValidationTestConfig(t *testing.T, webURL string) *PipelineConfig {
	t.Helper()
	t.Setenv("HQA_TEST_DV_USER", "qa-user")
	t.Setenv("HQA_TEST_DV_PASS", "qa-pass")
	return &PipelineConfig{
		WebURL:                webURL,
		DataValidation:        true,
		DataValidationUserEnv: "HQA_TEST_DV_USER",
		DataValidationPassEnv: "HQA_TEST_DV_PASS",
	}
}

// SECURITY: by default the step must send NOTHING to the target. Before the
// fix it POSTed {"username":"admin","password":"admin123"} to /api/v1/auth/login
// of whatever HELIX_WEB_URL pointed at (default-credential probing).
func TestDataValidation_DefaultOff_SendsNoRequest(t *testing.T) {
	srv, rec := dvServer(t)
	sp := &SessionPipeline{config: &PipelineConfig{WebURL: srv.URL}}
	_ = sp.validateAPIData(context.Background())
	assert.Empty(t, rec.snapshot(), "no request may reach the target unless data-validation is opted in")
}

// Credentials present in the environment do not turn the step on by themselves.
func TestDataValidation_CredentialsAloneDoNotEnableIt(t *testing.T) {
	srv, rec := dvServer(t)
	cfg := dataValidationTestConfig(t, srv.URL)
	cfg.DataValidation = false
	sp := &SessionPipeline{config: cfg}
	_ = sp.validateAPIData(context.Background())
	assert.Empty(t, rec.snapshot(), "credentials in the environment without the opt-in must send nothing")
}

// Opted in but with no credential variable set: still no request (and never a
// fallback to a built-in credential).
func TestDataValidation_OptInWithoutCredentials_SendsNoRequest(t *testing.T) {
	srv, rec := dvServer(t)
	t.Setenv("HQA_TEST_DV_USER", "")
	t.Setenv("HQA_TEST_DV_PASS", "")
	sp := &SessionPipeline{config: &PipelineConfig{
		WebURL: srv.URL, DataValidation: true,
		DataValidationUserEnv: "HQA_TEST_DV_USER", DataValidationPassEnv: "HQA_TEST_DV_PASS",
	}}
	_ = sp.validateAPIData(context.Background())
	assert.Empty(t, rec.snapshot())
	sp2 := &SessionPipeline{config: &PipelineConfig{WebURL: srv.URL, DataValidation: true}}
	_ = sp2.validateAPIData(context.Background())
	assert.Empty(t, rec.snapshot(), "no credential variable NAMES configured: nothing is sent")
}

// Opted in with explicit credentials: the login carries exactly those, never
// the old built-in pair.
func TestDataValidation_OptIn_UsesSuppliedCredentials(t *testing.T) {
	srv, rec := dvServer(t)
	sp := &SessionPipeline{config: dataValidationTestConfig(t, srv.URL)}
	_ = sp.validateAPIData(context.Background())
	reqs := rec.snapshot()
	require.NotEmpty(t, reqs)
	login := reqs[0]
	assert.True(t, strings.HasPrefix(login, "POST /api/v1/auth/login "), login)
	assert.Contains(t, login, `"username":"qa-user"`)
	assert.Contains(t, login, `"password":"qa-pass"`)
	for _, r := range reqs {
		assert.NotContains(t, r, "admin123")
	}
}
