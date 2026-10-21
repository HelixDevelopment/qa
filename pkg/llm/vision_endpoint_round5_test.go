// SPDX-FileCopyrightText: 2026 Milos Vasic
// SPDX-License-Identifier: Apache-2.0

package llm

import (
	"context"
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const r5Key = "sk-Ab/Cd+Ef=0123456789XYZ"

// keyForms are the shapes in which a server can echo a key back.
func keyForms(k string) []string {
	return []string{
		k, strings.ToUpper(k), strings.ToLower(k),
		url.QueryEscape(k), url.PathEscape(k),
		base64.StdEncoding.EncodeToString([]byte(k)),
		base64.URLEncoding.EncodeToString([]byte(k)),
		base64.RawStdEncoding.EncodeToString([]byte(k)),
	}
}

// Review round 4, finding 3: every provider reads an error reply through ONE
// shared helper: the read is capped, the FULL body is redacted first (raw,
// any letter case, URL-encoded, base64), THEN truncated. A key near the
// truncation point must not leak as a prefix.
func TestProviders_ErrorBodyRedactedBeforeTruncation(t *testing.T) {
	forms := keyForms(r5Key)
	var bodyMode int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		pad := strings.Repeat("p", 490)
		switch atomic.LoadInt32(&bodyMode) {
		case 0: // key straddles the 512-byte truncation point
			_, _ = io.WriteString(w, pad+r5Key+" tail")
		default: // every echo form, early in the body
			_, _ = io.WriteString(w, "echo: "+strings.Join(forms, " | "))
		}
	}))
	defer srv.Close()
	providers := map[string]Provider{
		"openai-style": NewOpenAIProvider(ProviderConfig{Name: ProviderVisionEndpoint, BaseURL: srv.URL + "/v1", APIKey: r5Key}),
		"anthropic":    NewAnthropicProvider(ProviderConfig{Name: ProviderAnthropic, APIKey: r5Key, BaseURL: srv.URL}),
		"astica":       NewAsticaProvider(ProviderConfig{Name: "astica", APIKey: r5Key, BaseURL: srv.URL}),
		"google":       NewGoogleProvider(ProviderConfig{Name: ProviderGoogle, APIKey: r5Key, BaseURL: srv.URL}),
	}
	for _, mode := range []int32{0, 1} {
		atomic.StoreInt32(&bodyMode, mode)
		for name, p := range providers {
			_, err := p.Vision(context.Background(), []byte("x"), "q")
			if err == nil {
				t.Fatalf("%s: 400 not an error", name)
			}
			msg := err.Error()
			for _, f := range forms {
				if strings.Contains(msg, f) {
					t.Errorf("%s mode %d: error carries the key in form %q", name, mode, f)
				}
			}
			if strings.Contains(msg, r5Key[:12]) {
				t.Errorf("%s mode %d: error carries a key prefix: %q", name, mode, msg)
			}
			if len(msg) > 2048 {
				t.Errorf("%s mode %d: error text not truncated (%d bytes)", name, mode, len(msg))
			}
			if !strings.Contains(msg, "[REDACTED]") {
				t.Errorf("%s mode %d: redaction not visible: %q", name, mode, msg)
			}
		}
	}
}

// Finding 3: every provider caps the read — an over-cap body that then
// stalls is refused at once, not read until a deadline.
func TestProviders_ResponseReadCapped(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		chunk := []byte(strings.Repeat("C", 64<<10))
		for sent := 0; sent <= openaiMaxResponseBytes+(1<<20); sent += len(chunk) {
			if _, err := w.Write(chunk); err != nil {
				return
			}
		}
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()
	defer close(release)
	for name, p := range map[string]Provider{
		"anthropic": NewAnthropicProvider(ProviderConfig{Name: ProviderAnthropic, APIKey: "k", BaseURL: srv.URL}),
		"astica":    NewAsticaProvider(ProviderConfig{Name: "astica", APIKey: "k", BaseURL: srv.URL}),
		"google":    NewGoogleProvider(ProviderConfig{Name: ProviderGoogle, APIKey: "k", BaseURL: srv.URL}),
		"ollama":    NewOllamaProvider(ProviderConfig{Name: ProviderOllama, BaseURL: srv.URL}),
	} {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		start := time.Now()
		_, err := p.Vision(ctx, []byte("x"), "q")
		cancel()
		if err == nil || !strings.Contains(err.Error(), "exceeds") || time.Since(start) > 4*time.Second {
			t.Errorf("%s: over-cap stalled body not refused at the cap: err=%v after %v", name, err, time.Since(start))
		}
	}
}

// Pins R10: AdaptiveProvider.VisionVia returns a cancelled context's error
// unchanged and does not call the member.
func TestAdaptiveVisionVia_CancelledCtx(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"ok"}}]}`)
	}))
	defer srv.Close()
	member := NewOpenAIProvider(ProviderConfig{Name: ProviderVisionEndpoint, BaseURL: srv.URL + "/v1"})
	ap := NewAdaptiveProvider(member)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := ap.VisionVia(ctx, member, []byte("x"), "q"); err != context.Canceled || atomic.LoadInt32(&calls) != 0 {
		t.Fatalf("cancelled ctx: err=%v calls=%d, want context.Canceled and 0", err, calls)
	}
}

// Pins R27/R28: a bare '#' (empty fragment) or bare '?' (empty query) is
// refused too.
func TestVisionEndpointConfig_RefusesEmptyQueryAndFragment(t *testing.T) {
	for _, u := range []string{"http://127.0.0.1:8082/v1#", "http://127.0.0.1:8082/v1?"} {
		if _, ok, err := VisionEndpointConfigFromEnvChecked(envMap(map[string]string{EnvVisionEndpoint: u})); err == nil || ok {
			t.Errorf("%q accepted", u)
		}
	}
}

// Pins R20: Google's transport errors are scrubbed.
func TestGoogle_TransportErrorScrubbed(t *testing.T) {
	p := NewGoogleProvider(ProviderConfig{Name: ProviderGoogle, APIKey: "k", BaseURL: "http://alice:pw@127.0.0.1:1/x?key=SEKRET123"})
	_, err := p.Vision(context.Background(), []byte("x"), "q")
	if err == nil {
		t.Fatal("dead endpoint: no error")
	}
	for _, secret := range []string{"SEKRET123", "alice", ":pw"} {
		if strings.Contains(err.Error(), secret) {
			t.Fatalf("google transport error leaks %q: %v", secret, err)
		}
	}
}
