// SPDX-FileCopyrightText: 2026 Milos Vasic
// SPDX-License-Identifier: Apache-2.0

package llm

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"digital.vasic.helixqa/pkg/llmctl"
)

var llmctlTestPNG = []byte{
	0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 0x00, 0x00, 0x00, 0x0d, 0x49, 0x48, 0x44, 0x52,
	0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01, 0x08, 0x06, 0x00, 0x00, 0x00, 0x1f, 0x15, 0xc4,
	0x89, 0x00, 0x00, 0x00, 0x0d, 0x49, 0x44, 0x41, 0x54, 0x78, 0x9c, 0x63, 0xf8, 0xcf, 0xc0, 0xf0,
	0x1f, 0x00, 0x05, 0x00, 0x01, 0xff, 0x89, 0x99, 0x3d, 0x1d, 0x00, 0x00, 0x00, 0x00, 0x49, 0x45,
	0x4e, 0x44, 0xae, 0x42, 0x60, 0x82,
}

func llmctlServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health":
			_, _ = w.Write([]byte(`{"status":"ok"}`))
		case "/v1/models":
			_, _ = w.Write([]byte(`{"data":[{"id":"vision-pinned.gguf"}]}`))
		case "/v1/chat/completions":
			var req map[string]any
			_ = json.NewDecoder(r.Body).Decode(&req)
			text := "chat-ok"
			if raw, _ := json.Marshal(req["messages"]); strings.Contains(string(raw), "image_url") {
				text = "vision-ok"
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"model":   "vision-pinned.gguf",
				"choices": []any{map[string]any{"message": map[string]any{"content": text}}},
				"usage":   map[string]any{"prompt_tokens": 3, "completion_tokens": 1},
			})
		default:
			http.NotFound(w, r)
		}
	}))
}

func TestLlmctlProvider_InterfaceAndConfig(t *testing.T) {
	cfg := ProviderConfig{Name: ProviderLlmctl}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("llmctl needs neither api_key nor base_url: %v", err)
	}
	var p Provider = NewLlmctlProvider(cfg)
	if p.Name() != ProviderLlmctl || !p.SupportsVision() {
		t.Fatalf("name=%s vision=%v", p.Name(), p.SupportsVision())
	}
	if ProviderEnvKeys[ProviderLlmctl] == "" {
		t.Fatal("llmctl must have an env key entry")
	}
	if IsOpenAICompatible(ProviderLlmctl) {
		t.Fatal("llmctl is served by its own provider, not the OpenAI registry path")
	}
}

func TestLlmctlProvider_VisionAndChat(t *testing.T) {
	srv := llmctlServer(t)
	defer srv.Close()
	p := NewLlmctlProvider(ProviderConfig{Name: ProviderLlmctl, BaseURL: srv.URL, Model: "vision-pinned.gguf"})
	r, err := p.Vision(context.Background(), llmctlTestPNG, "describe")
	if err != nil || r.Content != "vision-ok" || r.Model != "vision-pinned.gguf" || r.InputTokens != 3 {
		t.Fatalf("vision: %+v %v", r, err)
	}
	c, err := p.Chat(context.Background(), []Message{{Role: RoleUser, Content: "hi"}})
	if err != nil || c.Content != "chat-ok" {
		t.Fatalf("chat: %+v %v", c, err)
	}
	if _, err := p.Chat(context.Background(), []Message{{Role: RoleUser}}); err == nil {
		t.Fatal("empty message must be refused")
	}
}

func TestLlmctlProvider_FailureLegs(t *testing.T) {
	srv := llmctlServer(t)
	defer srv.Close()
	// pinned model mismatch is surfaced, not silently accepted
	p := NewLlmctlProvider(ProviderConfig{Name: ProviderLlmctl, BaseURL: srv.URL, Model: "other.gguf"})
	if _, err := p.Vision(context.Background(), llmctlTestPNG, "x"); !errors.Is(err, llmctl.ErrModelMismatch) {
		t.Fatalf("want ErrModelMismatch, got %v", err)
	}
	// server down -> ErrNotRunning (so AdaptiveProvider can fall through)
	l, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := l.Addr().String()
	l.Close()
	p = NewLlmctlProvider(ProviderConfig{Name: ProviderLlmctl, BaseURL: "http://" + addr})
	if _, err := p.Vision(context.Background(), llmctlTestPNG, "x"); !errors.Is(err, llmctl.ErrNotRunning) {
		t.Fatalf("want ErrNotRunning, got %v", err)
	}
}
