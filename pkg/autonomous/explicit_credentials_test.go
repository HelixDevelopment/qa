// SPDX-FileCopyrightText: 2026 Milos Vasic
// SPDX-License-Identifier: Apache-2.0

package autonomous

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"digital.vasic.helixqa/pkg/learning"
	"digital.vasic.helixqa/pkg/testbank"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Credentials are strictly explicit: the executor has no built-in admin pair.
// Before the fix an empty AdminCreds fell back to admin/admin123 and POSTed it
// to the target's login endpoint.
func TestHTTPExecutor_AdminAuth_NoBuiltInCredentials(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"session_token":"t"}`))
	}))
	defer srv.Close()

	h := NewHTTPExecutor(srv.URL)
	res := h.Execute(context.Background(), "GET", "/api/v1/admin/x", testbank.TestStep{
		AuthMode: "admin", ExpectStatus: 200,
	})
	assert.False(t, res.Success, "auth:admin without supplied credentials must not pass")
	assert.Zero(t, atomic.LoadInt32(&hits), "no request (login or otherwise) may reach the target without explicit credentials")
	assert.Contains(t, res.Message, "credentials")
}

func TestHTTPExecutor_AdminAuth_ExplicitCredentialsStillWork(t *testing.T) {
	var loginBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/v1/auth/login" {
			b := make([]byte, 512)
			n, _ := r.Body.Read(b)
			loginBody = string(b[:n])
			_, _ = w.Write([]byte(`{"session_token":"t"}`))
			return
		}
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	h := NewHTTPExecutor(srv.URL)
	h.AdminCreds = Credentials{Username: "u-supplied", Password: "p-supplied"}
	res := h.Execute(context.Background(), "GET", "/health", testbank.TestStep{AuthMode: "admin", ExpectStatus: 200})
	require.True(t, res.Success, res.Message)
	assert.Contains(t, loginBody, "u-supplied")
	assert.Contains(t, loginBody, "p-supplied")
}

// The LLM navigation prompts must never carry a credential.
func TestNavigationPrompts_CarryNoCredentials(t *testing.T) {
	for name, p := range map[string]string{"tv": navigationPromptTemplate, "web": webNavigationPromptTemplate} {
		low := strings.ToLower(p)
		assert.NotContains(t, low, "admin123", name)
		assert.NotContains(t, low, "admin/admin", name)
		assert.NotContains(t, low, `type "admin"`, name)
		assert.Contains(t, p, QAUsernamePlaceholder, name)
		assert.Contains(t, p, QAPasswordPlaceholder, name)
	}
}

// Credentials discovered in the project's .env must not be written into the
// prompt that is sent to an LLM provider: names only.
func TestBuildKBContext_NeverEmbedsCredentialValues(t *testing.T) {
	kb := &learning.KnowledgeBase{Credentials: map[string]string{
		"ADMIN_USERNAME": "envuser-xyz", "ADMIN_PASSWORD": "envpass-xyz",
	}}
	ctx := buildKBContext(kb)
	assert.NotContains(t, ctx, "envuser-xyz")
	assert.NotContains(t, ctx, "envpass-xyz")
	assert.NotContains(t, ctx, "USE THESE CREDENTIALS")
	assert.Contains(t, ctx, "ADMIN_PASSWORD", "names may be listed so the model knows credentials exist")
}

// Placeholders are substituted by the harness only with the operator opt-in.
func TestResolveCredentialPlaceholders(t *testing.T) {
	t.Setenv("HQA_T_USER", "qa-user")
	t.Setenv("HQA_T_PASS", "qa-pass")
	optIn := &SessionPipeline{config: &PipelineConfig{
		DataValidation: true, DataValidationUserEnv: "HQA_T_USER", DataValidationPassEnv: "HQA_T_PASS",
	}}
	a, err := optIn.resolveCredentialPlaceholders(llmAction{Type: "type", Value: QAPasswordPlaceholder})
	require.NoError(t, err)
	assert.Equal(t, "qa-pass", a.Value)
	a, err = optIn.resolveCredentialPlaceholders(llmAction{Type: "type", Value: QAUsernamePlaceholder})
	require.NoError(t, err)
	assert.Equal(t, "qa-user", a.Value)

	// Non-placeholder text passes through untouched.
	a, err = optIn.resolveCredentialPlaceholders(llmAction{Type: "type", Value: "search term"})
	require.NoError(t, err)
	assert.Equal(t, "search term", a.Value)

	// No opt-in: refused, nothing typed.
	off := &SessionPipeline{config: &PipelineConfig{DataValidationUserEnv: "HQA_T_USER", DataValidationPassEnv: "HQA_T_PASS"}}
	_, err = off.resolveCredentialPlaceholders(llmAction{Type: "type", Value: QAPasswordPlaceholder})
	assert.Error(t, err)

	// Opted in but variable unset: refused.
	t.Setenv("HQA_T_PASS", "")
	_, err = optIn.resolveCredentialPlaceholders(llmAction{Type: "type", Value: QAPasswordPlaceholder})
	assert.Error(t, err)
}
