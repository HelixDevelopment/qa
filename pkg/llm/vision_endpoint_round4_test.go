// SPDX-FileCopyrightText: 2026 Milos Vasic
// SPDX-License-Identifier: Apache-2.0

package llm

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// Review round 3, finding 7: secrets hidden in the PATH as encoded '?'/'#'
// or ';' parameters are refused too, without echoing them.
func TestVisionEndpointConfig_RefusesEncodedPathSecrets(t *testing.T) {
	for _, u := range []string{
		"http://127.0.0.1:8082/v1%3Fapi_key=SEKRET123",
		"http://127.0.0.1:8082/v1%3fapi_key=SEKRET123",
		"http://127.0.0.1:8082/v1%23SEKRET123",
		"http://127.0.0.1:8082/v1;key=SEKRET123",
	} {
		_, ok, err := VisionEndpointConfigFromEnvChecked(envMap(map[string]string{EnvVisionEndpoint: u}))
		if err == nil || ok {
			t.Errorf("%q accepted", u)
			continue
		}
		if strings.Contains(err.Error(), "SEKRET123") {
			t.Errorf("refusal echoes the secret: %v", err)
		}
	}
}

// Finding 7: a server that echoes the request (incl. the Authorization
// header) in its error body must not put the provider's own key into the
// error text.
func TestVisionEndpoint_ErrorBodyRedactsOwnKey(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, "bad auth: "+r.Header.Get("Authorization"))
	}))
	defer srv.Close()
	p := NewOpenAIProvider(ProviderConfig{Name: ProviderVisionEndpoint, BaseURL: srv.URL + "/v1", APIKey: "sk-SEKRET-123456"})
	_, err := p.Vision(context.Background(), []byte("x"), "q")
	if err == nil {
		t.Fatal("401 not an error")
	}
	if strings.Contains(err.Error(), "sk-SEKRET-123456") {
		t.Fatalf("error text carries the provider's own key: %v", err)
	}
	if !strings.Contains(err.Error(), "[REDACTED]") {
		t.Fatalf("redaction not visible: %v", err)
	}
}

// Finding 7: a base URL that already ends with /chat/completions is used as
// is, not double-appended.
func TestVisionEndpoint_BaseAlreadyChatCompletions(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.URL.Path
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"ok"}}]}`)
	}))
	defer srv.Close()
	for _, base := range []string{srv.URL + "/v1/chat/completions", srv.URL + "/v1/chat/completions/"} {
		if _, err := NewOpenAIProvider(ProviderConfig{Name: ProviderVisionEndpoint, BaseURL: base}).Vision(context.Background(), []byte("x"), "q"); err != nil {
			t.Fatal(err)
		}
		if got != "/v1/chat/completions" {
			t.Fatalf("base %q requested %q", base, got)
		}
	}
}

// Finding 7: other providers' transport errors are scrubbed too.
func TestOtherProviders_TransportErrorScrubbed(t *testing.T) {
	base := "http://alice:pw@127.0.0.1:1/v1?key=SEKRET123"
	for name, p := range map[string]Provider{
		"anthropic": NewAnthropicProvider(ProviderConfig{Name: ProviderAnthropic, APIKey: "k", BaseURL: base}),
		"ollama":    NewOllamaProvider(ProviderConfig{Name: ProviderOllama, BaseURL: base}),
		"astica":    NewAsticaProvider(ProviderConfig{Name: "astica", APIKey: "k", BaseURL: base}),
	} {
		_, err := p.Vision(context.Background(), []byte("x"), "q")
		if err == nil {
			t.Fatalf("%s: dead endpoint returned no error", name)
		}
		for _, secret := range []string{"SEKRET123", "alice", ":pw"} {
			if strings.Contains(err.Error(), secret) {
				t.Errorf("%s: transport error leaks %q: %v", name, secret, err)
			}
		}
	}
}

func resetLimiter(t *testing.T, name string) {
	t.Helper()
	del := func() {
		rateLimiterMu.Lock()
		delete(rateLimiterRegistry, name)
		rateLimiterMu.Unlock()
	}
	del()
	t.Cleanup(del)
}

// Review round 3, finding 2 (pins N19/N20/N34): the enhanced wrapper's
// VisionVia records failures, opens the circuit after the threshold (the
// member is then not called; the error is ErrCircuitOpen), and returns a
// cancelled context's error unchanged.
func TestVisionEndpoint_EnhancedVisionViaCircuitAndCtx(t *testing.T) {
	resetLimiter(t, ProviderVisionEndpoint)
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, "boom")
	}))
	defer srv.Close()
	eap, err := NewEnhancedAdaptiveProvider([]ProviderConfig{{Name: ProviderVisionEndpoint, BaseURL: srv.URL + "/v1"}})
	if err != nil {
		t.Fatal(err)
	}
	member := eap.Providers()[0]
	threshold := GetRateLimiter(ProviderVisionEndpoint).config.MaxConsecutiveFailures
	for i := 0; i < threshold; i++ {
		if _, err := eap.VisionVia(context.Background(), member, []byte("x"), "q"); err == nil {
			t.Fatal("500 not an error")
		}
	}
	_, err = eap.VisionVia(context.Background(), member, []byte("x"), "q")
	if !errors.Is(err, ErrCircuitOpen) {
		t.Fatalf("after %d failures: err=%v, want ErrCircuitOpen", threshold, err)
	}
	if got := atomic.LoadInt32(&calls); int(got) != threshold {
		t.Fatalf("member called %d times, want %d (the open circuit must stop the call)", got, threshold)
	}

	resetLimiter(t, ProviderVisionEndpoint)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := eap.VisionVia(ctx, member, []byte("x"), "q"); err != context.Canceled {
		t.Fatalf("cancelled ctx: err=%v, want context.Canceled unchanged", err)
	}
}
