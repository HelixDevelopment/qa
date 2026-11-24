// SPDX-FileCopyrightText: 2026 Milos Vasic
// SPDX-License-Identifier: Apache-2.0

package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGoogleProvider_Name(t *testing.T) {
	p := NewGoogleProvider(ProviderConfig{
		Name:   ProviderGoogle,
		APIKey: "test-key",
	})
	assert.Equal(t, ProviderGoogle, p.Name())
}

func TestGoogleProvider_SupportsVision(t *testing.T) {
	p := NewGoogleProvider(ProviderConfig{
		Name:   ProviderGoogle,
		APIKey: "test-key",
	})
	assert.True(t, p.SupportsVision())
}

func TestGoogleProvider_DefaultModel(t *testing.T) {
	p := NewGoogleProvider(ProviderConfig{
		Name:   ProviderGoogle,
		APIKey: "test-key",
	})
	gp := p.(*googleProvider)
	assert.Equal(t, geminiDefaultModel, gp.model)
}

func TestGoogleProvider_CustomModel(t *testing.T) {
	p := NewGoogleProvider(ProviderConfig{
		Name:   ProviderGoogle,
		APIKey: "test-key",
		Model:  "gemini-1.5-pro",
	})
	gp := p.(*googleProvider)
	assert.Equal(t, "gemini-1.5-pro", gp.model)
}

func TestGoogleProvider_Chat(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, http.MethodPost, r.Method)
			assert.True(t, strings.Contains(
				r.URL.Path,
				"generateContent",
			))
			// Verify API key in header (not URL).
			assert.Equal(t,
				"test-api-key",
				r.Header.Get("x-goog-api-key"),
			)
			assert.Empty(t, r.URL.Query().Get("key"),
				"key must NOT be in URL query params")
			assert.Equal(t,
				"application/json",
				r.Header.Get("Content-Type"),
			)

			// Verify request body structure.
			var req geminiRequest
			err := json.NewDecoder(r.Body).Decode(&req)
			require.NoError(t, err)
			require.NotEmpty(t, req.Contents)

			resp := geminiResponse{
				Candidates: []geminiCandidate{
					{
						Content: geminiContent{
							Parts: []geminiPart{
								{Text: "QA analysis complete."},
							},
						},
					},
				},
				UsageMetadata: &geminiUsage{
					PromptTokenCount:     42,
					CandidatesTokenCount: 15,
				},
			}
			w.Header().Set(
				"Content-Type", "application/json",
			)
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(resp)
		},
	))
	defer srv.Close()

	// Override the URL format to point to test server.
	// We create the provider, then swap out the internal
	// URL via a Chat call that uses the test server.
	p := &googleProvider{
		apiKey: "test-api-key",
		model:  "gemini-test",
		client: srv.Client(),
	}

	// We need to override doRequest to use the test server.
	// The simplest approach: test the full HTTP round-trip
	// by pointing at the test server URL.
	origFmt := geminiGenerateURLFmt
	_ = origFmt

	// Create a custom provider that uses the test server.
	testProvider := &googleProvider{
		apiKey: "test-api-key",
		model:  "gemini-test",
		client: srv.Client(),
	}
	_ = p

	// Override the URL by using a request to the test srv.
	messages := []Message{
		{Role: RoleSystem, Content: "You are a QA agent."},
		{Role: RoleUser, Content: "Analyze this."},
	}

	// Build request manually to test server.
	var contents []geminiContent
	for _, m := range messages {
		role := m.Role
		if role == RoleSystem {
			role = "user"
		}
		if role == RoleAssistant {
			role = "model"
		}
		contents = append(contents, geminiContent{
			Role:  role,
			Parts: []geminiPart{{Text: m.Content}},
		})
	}

	req := geminiRequest{Contents: contents}
	resp, err := testProvider.doRequestURL(
		context.Background(),
		req,
		srv.URL+"/v1beta/models/gemini-test:generateContent",
	)
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, "QA analysis complete.", resp.Content)
	assert.Equal(t, 42, resp.InputTokens)
	assert.Equal(t, 15, resp.OutputTokens)
}

func TestGoogleProvider_Chat_Error(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = fmt.Fprint(w,
				`{"error":{"message":"Rate limit exceeded"}}`,
			)
		},
	))
	defer srv.Close()

	p := &googleProvider{
		apiKey: "test-key",
		model:  "gemini-test",
		client: srv.Client(),
	}

	req := geminiRequest{
		Contents: []geminiContent{
			{
				Role:  "user",
				Parts: []geminiPart{{Text: "Hello"}},
			},
		},
	}
	resp, err := p.doRequestURL(
		context.Background(),
		req,
		srv.URL+"/v1beta/models/gemini-test:generateContent",
	)
	assert.Error(t, err)
	assert.Nil(t, resp)
	assert.Contains(t, err.Error(), "429")
}

func TestGoogleProvider_Vision(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, http.MethodPost, r.Method)

			var req geminiRequest
			err := json.NewDecoder(r.Body).Decode(&req)
			require.NoError(t, err)
			require.Len(t, req.Contents, 1)
			require.Len(t, req.Contents[0].Parts, 2)
			// First part should be inline_data (image).
			assert.NotNil(t,
				req.Contents[0].Parts[0].InlineData,
			)
			assert.Equal(t,
				"image/png",
				req.Contents[0].Parts[0].InlineData.MIMEType,
			)
			// Second part should be text prompt.
			assert.NotEmpty(t,
				req.Contents[0].Parts[1].Text,
			)

			resp := geminiResponse{
				Candidates: []geminiCandidate{
					{
						Content: geminiContent{
							Parts: []geminiPart{
								{Text: "I see a settings screen."},
							},
						},
					},
				},
				UsageMetadata: &geminiUsage{
					PromptTokenCount:     200,
					CandidatesTokenCount: 30,
				},
			}
			w.Header().Set(
				"Content-Type", "application/json",
			)
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(resp)
		},
	))
	defer srv.Close()

	p := &googleProvider{
		apiKey: "test-key",
		model:  "gemini-test",
		client: srv.Client(),
	}

	imageBytes := []byte{0x89, 0x50, 0x4E, 0x47} // fake PNG

	// Build vision request manually.
	req := geminiRequest{
		Contents: []geminiContent{
			{
				Role: "user",
				Parts: []geminiPart{
					{
						InlineData: &geminiInline{
							MIMEType: "image/png",
							Data:     "iVBORw0KGgo=",
						},
					},
					{
						Text: "What do you see?",
					},
				},
			},
		},
	}
	_ = imageBytes

	resp, err := p.doRequestURL(
		context.Background(),
		req,
		srv.URL+"/v1beta/models/gemini-test:generateContent",
	)
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, "I see a settings screen.", resp.Content)
	assert.Equal(t, 200, resp.InputTokens)
	assert.Equal(t, 30, resp.OutputTokens)
}

func TestGoogleProvider_EmptyResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, _ *http.Request) {
			resp := geminiResponse{
				Candidates: []geminiCandidate{},
			}
			w.Header().Set(
				"Content-Type", "application/json",
			)
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(resp)
		},
	))
	defer srv.Close()

	p := &googleProvider{
		apiKey: "test-key",
		model:  "gemini-test",
		client: srv.Client(),
	}

	req := geminiRequest{
		Contents: []geminiContent{
			{
				Role:  "user",
				Parts: []geminiPart{{Text: "Hello"}},
			},
		},
	}
	resp, err := p.doRequestURL(
		context.Background(),
		req,
		srv.URL+"/v1beta/models/gemini-test:generateContent",
	)
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, "", resp.Content)
}

func TestRedactKeyFromError_Nil(t *testing.T) {
	assert.Nil(t, redactKeyFromError(nil, "secret"))
}

func TestRedactKeyFromError_EmptyKey(t *testing.T) {
	err := fmt.Errorf("some error")
	assert.Equal(t, err, redactKeyFromError(err, ""))
}

func TestRedactKeyFromError_RedactsKey(t *testing.T) {
	key := "AIzaSyFAKEFAKEFAKEFAKEFAKEFAKEFAKEFAKE0"
	err := fmt.Errorf(
		"Post https://example.com/api?key=%s: timeout",
		key,
	)
	redacted := redactKeyFromError(err, key)
	assert.NotContains(t, redacted.Error(), key,
		"API key must not appear in redacted error")
	assert.Contains(t, redacted.Error(), "REDACTED")
	assert.Contains(t, redacted.Error(), "timeout")
}

func TestRedactKeyFromError_NoMatch(t *testing.T) {
	err := fmt.Errorf("connection refused")
	redacted := redactKeyFromError(err, "secret-key")
	assert.Equal(t,
		"connection refused", redacted.Error(),
	)
}
func TestGoogleProvider_ErrorDoesNotLeakKey(t *testing.T) {
	// Simulate a connection error. The error message from
	// http.Client.Do includes the full URL. With the key
	// now in the header (not the URL), it must never appear.
	p := &googleProvider{
		apiKey: "AIzaSyTestKeyThatMustNotLeak",
		model:  "gemini-test",
		client: &http.Client{},
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Immediately cancel to force an error.

	req := geminiRequest{
		Contents: []geminiContent{
			{Parts: []geminiPart{{Text: "test"}}},
		},
	}
	url := fmt.Sprintf(geminiGenerateURLFmt, p.model)
	_, err := p.doRequestURL(ctx, req, url)
	require.Error(t, err)
	assert.NotContains(t, err.Error(),
		"AIzaSyTestKeyThatMustNotLeak",
		"API key MUST NOT appear in error messages",
	)
	assert.NotContains(t, err.Error(), "key=",
		"No key= parameter should exist in URL")
}
