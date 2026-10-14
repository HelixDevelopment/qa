// SPDX-FileCopyrightText: 2026 Milos Vasic
// SPDX-License-Identifier: Apache-2.0

package llm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func envMap(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

// TestVisionEndpointConfigFromEnv maps HELIX_VISION_ENDPOINT /
// HELIX_VISION_MODEL / HELIX_VISION_API_KEY onto an OpenAI-compatible
// ProviderConfig. Golden-bad: an unset endpoint yields NO config (never a
// default URL that would make a dead endpoint look configured).
func TestVisionEndpointConfigFromEnv(t *testing.T) {
	if _, ok := VisionEndpointConfigFromEnv(envMap(nil)); ok {
		t.Fatal("no HELIX_VISION_ENDPOINT must yield no config")
	}
	if _, ok := VisionEndpointConfigFromEnv(envMap(map[string]string{EnvVisionEndpoint: "   "})); ok {
		t.Fatal("blank HELIX_VISION_ENDPOINT must yield no config")
	}
	cfg, ok := VisionEndpointConfigFromEnv(envMap(map[string]string{
		EnvVisionEndpoint: "http://localhost:8082/v1/",
		EnvVisionModel:    "vision",
		EnvVisionAPIKey:   "sk-test-secret",
	}))
	if !ok {
		t.Fatal("endpoint set but no config produced")
	}
	if cfg.Name != ProviderVisionEndpoint {
		t.Fatalf("Name = %q", cfg.Name)
	}
	if cfg.BaseURL != "http://localhost:8082/v1" {
		t.Fatalf("BaseURL = %q (trailing slash must be trimmed so /v1 suffix detection works)", cfg.BaseURL)
	}
	if cfg.Model != "vision" || cfg.APIKey != "sk-test-secret" {
		t.Fatalf("model/key not mapped: %+v", cfg)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	// A key is OPTIONAL for a local llama-server.
	noKey, _ := VisionEndpointConfigFromEnv(envMap(map[string]string{EnvVisionEndpoint: "http://h:8083/v1"}))
	if err := noKey.Validate(); err != nil {
		t.Fatalf("keyless local endpoint must validate: %v", err)
	}
	if err := (ProviderConfig{Name: ProviderVisionEndpoint}).Validate(); err == nil {
		t.Fatal("vision-endpoint without base_url must NOT validate")
	}
}

// TestDescribeVisionEndpoint_NeverLeaksKey: the startup line printed for
// the endpoint names the URL and says whether a key is set — it never
// prints the key, nor URL userinfo.
func TestDescribeVisionEndpoint_NeverLeaksKey(t *testing.T) {
	cfg := ProviderConfig{
		Name:    ProviderVisionEndpoint,
		BaseURL: "http://user:hunter2@localhost:8082/v1",
		Model:   "vision",
		APIKey:  "sk-test-secret",
	}
	line := DescribeVisionEndpoint(cfg)
	for _, secret := range []string{"sk-test-secret", "hunter2"} {
		if strings.Contains(line, secret) {
			t.Fatalf("description leaks a secret %q: %s", secret, line)
		}
	}
	if !strings.Contains(line, "localhost:8082/v1") || !strings.Contains(line, "api key: set") {
		t.Fatalf("description lacks endpoint or key presence: %s", line)
	}
	cfg.APIKey = ""
	if !strings.Contains(DescribeVisionEndpoint(cfg), "api key: unset") {
		t.Fatal("keyless description must say unset")
	}
}

// TestVisionEndpoint_RealHTTPRoundTrip drives the provider built from the
// env config against an httptest server speaking the OpenAI chat
// completions protocol (what llama-server exposes on /v1). It asserts the
// request really carries the image as a data URI, the model, and the
// bearer key only when one is configured — and that the provider
// reports vision support and returns the server's text verbatim.
func TestVisionEndpoint_RealHTTPRoundTrip(t *testing.T) {
	type seen struct {
		path, auth, model string
		hasImage          bool
		hasSystem         bool
	}
	var got seen
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req struct {
			Model    string `json:"model"`
			Messages []struct {
				Role    string `json:"role"`
				Content []struct {
					Type     string `json:"type"`
					ImageURL *struct {
						URL string `json:"url"`
					} `json:"image_url"`
				} `json:"content"`
			} `json:"messages"`
		}
		_ = json.Unmarshal(body, &req)
		got.path = r.URL.Path
		got.auth = r.Header.Get("Authorization")
		got.model = req.Model
		for _, m := range req.Messages {
			if m.Role == RoleSystem {
				got.hasSystem = true
			}
			for _, c := range m.Content {
				if c.ImageURL != nil && strings.HasPrefix(c.ImageURL.URL, "data:image/png;base64,") {
					got.hasImage = true
				}
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"model":"vision","choices":[{"message":{"role":"assistant","content":"VERIFIED: yes\nACTUAL: login form"}}],"usage":{"prompt_tokens":5,"completion_tokens":7}}`)
	}))
	defer srv.Close()

	cfg, ok := VisionEndpointConfigFromEnv(envMap(map[string]string{
		EnvVisionEndpoint: srv.URL + "/v1",
		EnvVisionModel:    "vision",
	}))
	if !ok {
		t.Fatal("no config")
	}
	p := NewOpenAIProvider(cfg)
	if !p.SupportsVision() {
		t.Fatal("vision-endpoint provider must report SupportsVision")
	}
	resp, err := p.Vision(context.Background(), []byte("\x89PNGfake"), "is this a login form?")
	if err != nil {
		t.Fatalf("Vision: %v", err)
	}
	if resp.Content != "VERIFIED: yes\nACTUAL: login form" {
		t.Fatalf("content = %q", resp.Content)
	}
	if got.path != "/v1/chat/completions" {
		t.Fatalf("path = %q", got.path)
	}
	if got.model != "vision" || !got.hasImage {
		t.Fatalf("request lacked model/image: %+v", got)
	}
	if got.auth != "" {
		t.Fatalf("keyless endpoint must not send Authorization, got %q", got.auth)
	}
	if got.hasSystem {
		t.Fatal("vision-endpoint must not inject the llamacpp navigation JSON system prompt (it would break VERIFIED replies)")
	}

	// With a key: sent as Bearer.
	cfg.APIKey = "sk-test-secret"
	if _, err := NewOpenAIProvider(cfg).Vision(context.Background(), []byte("x"), "q"); err != nil {
		t.Fatal(err)
	}
	if got.auth != "Bearer sk-test-secret" {
		t.Fatalf("Authorization = %q", got.auth)
	}

	// Registered through the adaptive factory without an API key.
	ap, err := NewEnhancedAdaptiveProvider([]ProviderConfig{{Name: ProviderVisionEndpoint, BaseURL: srv.URL + "/v1"}})
	if err != nil {
		t.Fatalf("enhanced adaptive refused the endpoint: %v", err)
	}
	if len(ap.providers) != 1 || ap.providers[0].Name() != ProviderVisionEndpoint {
		t.Fatalf("endpoint not registered: %d providers", len(ap.providers))
	}
	if _, err := NewAdaptiveFromConfigs([]ProviderConfig{{Name: ProviderVisionEndpoint, BaseURL: srv.URL + "/v1"}}); err != nil {
		t.Fatalf("adaptive factory refused the endpoint: %v", err)
	}
}

// TestVisionEndpoint_ServerDownIsAnError — the negative control: a dead
// endpoint must surface an error, never an empty "successful" reply.
func TestVisionEndpoint_ServerDownIsAnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := srv.URL
	srv.Close()
	cfg, _ := VisionEndpointConfigFromEnv(envMap(map[string]string{EnvVisionEndpoint: url + "/v1"}))
	resp, err := NewOpenAIProvider(cfg).Vision(context.Background(), []byte("x"), "q")
	if err == nil {
		t.Fatalf("dead endpoint returned no error (resp=%+v)", resp)
	}
}
