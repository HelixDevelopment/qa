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
	"time"
)

// captureServer records the decoded JSON body of every request and replies
// with a fixed OpenAI-shaped answer.
func captureServer(t *testing.T, bodies *[]map[string]any) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		m := map[string]any{}
		_ = json.Unmarshal(raw, &m)
		*bodies = append(*bodies, m)
		_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"VERIFIED: yes"}}]}`)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// Finding 7: the vision endpoint is asked deterministically (temperature 0,
// fixed seed), and an unset HELIX_VISION_MODEL omits "model" (server
// default) instead of sending a cloud model name the local server lacks.
// Other OpenAI-compatible providers are unchanged.
func TestVisionEndpoint_DeterministicRequest(t *testing.T) {
	var bodies []map[string]any
	srv := captureServer(t, &bodies)

	noModel, _ := VisionEndpointConfigFromEnv(envMap(map[string]string{EnvVisionEndpoint: srv.URL + "/v1"}))
	if _, err := NewOpenAIProvider(noModel).Vision(context.Background(), []byte("x"), "q"); err != nil {
		t.Fatal(err)
	}
	b := bodies[len(bodies)-1]
	if _, has := b["model"]; has {
		t.Fatalf("unset HELIX_VISION_MODEL must omit \"model\", request sent %v", b["model"])
	}
	if b["temperature"] != float64(0) {
		t.Fatalf("temperature = %v, want 0", b["temperature"])
	}
	if b["seed"] == nil {
		t.Fatal("no seed sent")
	}
	seed1 := b["seed"]

	withModel, _ := VisionEndpointConfigFromEnv(envMap(map[string]string{EnvVisionEndpoint: srv.URL + "/v1", EnvVisionModel: "vision"}))
	if _, err := NewOpenAIProvider(withModel).Vision(context.Background(), []byte("x"), "q"); err != nil {
		t.Fatal(err)
	}
	b = bodies[len(bodies)-1]
	if b["model"] != "vision" || b["seed"] != seed1 {
		t.Fatalf("model/seed: %v %v", b["model"], b["seed"])
	}

	// A cloud provider keeps its historic request shape.
	cloud := NewOpenAIProvider(ProviderConfig{Name: ProviderOpenAI, APIKey: "k", BaseURL: srv.URL})
	if _, err := cloud.Vision(context.Background(), []byte("x"), "q"); err != nil {
		t.Fatal(err)
	}
	b = bodies[len(bodies)-1]
	if _, has := b["temperature"]; has {
		t.Fatalf("non-endpoint provider request changed: temperature %v", b["temperature"])
	}
	if b["model"] != openaiDefaultModel {
		t.Fatalf("cloud default model lost: %v", b["model"])
	}
}

// Finding 8: nothing secret-bearing in the URL reaches the log line —
// userinfo (even when the URL does not parse), query and fragment.
func TestDescribeVisionEndpoint_RedactsUnparsableQueryFragment(t *testing.T) {
	for _, u := range []string{
		"http://u:s3cr3t%zz@localhost:8082/v1",
		"http://localhost:8082/v1?api_key=s3cr3t",
		"http://localhost:8082/v1#token=s3cr3t",
		"http://u:s3cr3t@localhost:8082/v1?x=1",
		"::s3cr3t::not a url",
	} {
		line := DescribeVisionEndpoint(ProviderConfig{Name: ProviderVisionEndpoint, BaseURL: u})
		if strings.Contains(line, "s3cr3t") {
			t.Fatalf("description of %q leaks the secret: %s", u, line)
		}
	}
	if line := DescribeVisionEndpoint(ProviderConfig{BaseURL: "http://localhost:8082/v1"}); !strings.Contains(line, "localhost:8082/v1") {
		t.Fatalf("plain URL not shown: %s", line)
	}
}

// Finding 8: an error reply's body is capped in the error text, and an
// oversized success body is refused instead of read without limit.
func TestVisionEndpoint_ResponseBodyCapped(t *testing.T) {
	big := strings.Repeat("A", 1<<20) + "s3cr3t-tail"
	errSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, big)
	}))
	defer errSrv.Close()
	_, err := NewOpenAIProvider(ProviderConfig{Name: ProviderVisionEndpoint, BaseURL: errSrv.URL + "/v1"}).Vision(context.Background(), []byte("x"), "q")
	if err == nil {
		t.Fatal("500 not an error")
	}
	if len(err.Error()) > 2048 || strings.Contains(err.Error(), "s3cr3t-tail") {
		t.Fatalf("error carries the uncapped body (%d bytes)", len(err.Error()))
	}

	huge := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"`)
		chunk := strings.Repeat("B", 1<<20)
		for i := 0; i < 9; i++ {
			_, _ = io.WriteString(w, chunk)
		}
		_, _ = io.WriteString(w, `"}}]}`)
	}))
	defer huge.Close()
	if _, err := NewOpenAIProvider(ProviderConfig{Name: ProviderVisionEndpoint, BaseURL: huge.URL + "/v1"}).Vision(context.Background(), []byte("x"), "q"); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("oversized body not refused: %v", err)
	}
}

// Finding 6: the operator-configured endpoint outranks every registry
// provider for vision work — it was configured on purpose for these eyes.
func TestScoreVisionProvider_EndpointPreferred(t *testing.T) {
	ep := NewOpenAIProvider(ProviderConfig{Name: ProviderVisionEndpoint, BaseURL: "http://127.0.0.1:1/v1"})
	others := []Provider{
		NewOpenAIProvider(ProviderConfig{Name: "astica", APIKey: "k"}),
		NewOpenAIProvider(ProviderConfig{Name: ProviderOpenAI, APIKey: "k"}),
		NewOpenAIProvider(ProviderConfig{Name: "qwen", APIKey: "k"}),
	}
	ranked := rankVisionProviders(append(others, ep))
	if len(ranked) == 0 || ranked[0].Name() != ProviderVisionEndpoint {
		names := []string{}
		for _, p := range ranked {
			names = append(names, p.Name())
		}
		t.Fatalf("endpoint not ranked first: %v", names)
	}
}

// Review round 2, finding 4 (MEDIUM): a secret in HELIX_VISION_ENDPOINT
// (userinfo, query, fragment) is refused at config load with an error that
// never echoes it; the two-value helper reports "not configured".
func TestVisionEndpointConfig_RefusesSecretBearingURL(t *testing.T) {
	bad := []string{
		"http://alice:pw@127.0.0.1:8082/v1",
		"http://alice@127.0.0.1:8082/v1",
		"http://127.0.0.1:8082/v1?api_key=SEKRET123",
		"http://127.0.0.1:8082/v1#SEKRET123",
		"http://u:SEKRET123%zz@127.0.0.1:8082/v1",
		"ftp://127.0.0.1:8082/v1",
		"127.0.0.1:8082/v1",
	}
	for _, u := range bad {
		env := envMap(map[string]string{EnvVisionEndpoint: u})
		_, ok, err := VisionEndpointConfigFromEnvChecked(env)
		if err == nil || ok {
			t.Errorf("%q accepted (ok=%v err=%v)", u, ok, err)
			continue
		}
		for _, secret := range []string{"SEKRET123", "alice", "pw@"} {
			if strings.Contains(err.Error(), secret) {
				t.Errorf("error for %q echoes %q: %v", u, secret, err)
			}
		}
		if _, ok2 := VisionEndpointConfigFromEnv(env); ok2 {
			t.Errorf("%q: two-value helper reports configured", u)
		}
	}
	cfg, ok, err := VisionEndpointConfigFromEnvChecked(envMap(map[string]string{EnvVisionEndpoint: "http://127.0.0.1:8082/v1/"}))
	if err != nil || !ok || cfg.BaseURL != "http://127.0.0.1:8082/v1" {
		t.Fatalf("plain endpoint: %+v %v %v", cfg, ok, err)
	}
	if _, ok, err := VisionEndpointConfigFromEnvChecked(envMap(nil)); ok || err != nil {
		t.Fatalf("unset endpoint: ok=%v err=%v", ok, err)
	}
}

// Finding 4: a transport error never carries URL userinfo or query into the
// error text (it becomes a BLOCKED reason, a finding and the report), and the
// request path is built on the URL path, not appended after a query.
func TestVisionEndpoint_TransportErrorScrubbedAndPathBuilt(t *testing.T) {
	p := NewOpenAIProvider(ProviderConfig{Name: ProviderVisionEndpoint, BaseURL: "http://alice:pw@127.0.0.1:1/v1?api_key=SEKRET123"})
	_, err := p.Vision(context.Background(), []byte("x"), "q")
	if err == nil {
		t.Fatal("dead endpoint: no error")
	}
	for _, secret := range []string{"SEKRET123", "alice", ":pw"} {
		if strings.Contains(err.Error(), secret) {
			t.Fatalf("transport error leaks %q: %v", secret, err)
		}
	}
	var gotPath, gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotQuery = r.URL.Path, r.URL.RawQuery
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"VERIFIED: yes"}}]}`)
	}))
	defer srv.Close()
	if _, err := NewOpenAIProvider(ProviderConfig{Name: ProviderOpenAI, APIKey: "k", BaseURL: srv.URL + "/v1?x=1"}).Vision(context.Background(), []byte("x"), "q"); err != nil {
		t.Fatal(err)
	}
	if gotPath != "/v1/chat/completions" || gotQuery != "x=1" {
		t.Fatalf("request URL built wrongly: path=%q query=%q", gotPath, gotQuery)
	}
}

// Review round 2, finding 6: the read itself stops at the cap. The server
// sends a little more than the cap and then STALLS without closing: a capped
// read returns "exceeds" at once; an uncapped read would block until the
// short deadline. Memory stays bounded (~9 MiB) either way — the fixture never
// streams without end.
func TestVisionEndpoint_ResponseReadStopsAtCap(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"`)
		chunk := []byte(strings.Repeat("C", 64<<10))
		for sent := 0; sent <= openaiMaxResponseBytes+(1<<20); sent += len(chunk) {
			if _, err := w.Write(chunk); err != nil {
				return
			}
		}
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		select { // stall: never finish the body
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()
	defer close(release)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	start := time.Now()
	_, err := NewOpenAIProvider(ProviderConfig{Name: ProviderVisionEndpoint, BaseURL: srv.URL + "/v1"}).Vision(ctx, []byte("x"), "q")
	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("over-cap stalled body not refused at the cap: %v", err)
	}
	if time.Since(start) > 4*time.Second {
		t.Fatalf("refusal took %v — the read did not stop at the cap", time.Since(start))
	}
}

// Review round 2, finding 7: the enhanced wrapper's VisionVia keeps its cost
// accounting and refuses on a cancelled context without calling the member.
func TestVisionEndpoint_EnhancedVisionViaKeepsAccounting(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"VERIFIED: yes"}}],"usage":{"prompt_tokens":3,"completion_tokens":2}}`)
	}))
	defer srv.Close()
	eap, err := NewEnhancedAdaptiveProvider([]ProviderConfig{{Name: ProviderVisionEndpoint, BaseURL: srv.URL + "/v1"}})
	if err != nil {
		t.Fatal(err)
	}
	ct := NewCostTracker()
	eap.SetCostTracker(ct)
	if _, err := eap.VisionVia(context.Background(), eap.Providers()[0], []byte("x"), "q"); err != nil {
		t.Fatal(err)
	}
	if ct.Summary().TotalCalls != 1 || calls != 1 {
		t.Fatalf("VisionVia: cost calls=%d server calls=%d, want 1/1", ct.Summary().TotalCalls, calls)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := eap.VisionVia(ctx, eap.Providers()[0], []byte("x"), "q"); err == nil || calls != 1 {
		t.Fatalf("cancelled ctx: err=%v server calls=%d", err, calls)
	}
}

// Round 4: the JSON-schema response_format goes ONLY to the operator's local
// endpoint; cloud OpenAI-compatible providers keep their request shape.
func TestVisionEndpoint_ResponseSchemaOnlyForEndpoint(t *testing.T) {
	var bodies []map[string]any
	srv := captureServer(t, &bodies)
	ctx := WithResponseSchema(context.Background(), map[string]any{"type": "object"})
	if _, err := NewOpenAIProvider(ProviderConfig{Name: ProviderVisionEndpoint, BaseURL: srv.URL + "/v1"}).Vision(ctx, []byte("x"), "q"); err != nil {
		t.Fatal(err)
	}
	if rf, _ := bodies[len(bodies)-1]["response_format"].(map[string]any); rf == nil || rf["type"] != "json_object" {
		t.Fatalf("endpoint lacks response_format: %v", bodies[len(bodies)-1]["response_format"])
	}
	for _, name := range []string{ProviderOpenAI, "qwen", "openrouter"} {
		if _, err := NewOpenAIProvider(ProviderConfig{Name: name, APIKey: "k", BaseURL: srv.URL}).Vision(ctx, []byte("x"), "q"); err != nil {
			t.Fatal(err)
		}
		if _, has := bodies[len(bodies)-1]["response_format"]; has {
			t.Fatalf("%s request carries response_format — cloud request shape changed", name)
		}
	}
	// No schema in the context: the endpoint request is unchanged too.
	if _, err := NewOpenAIProvider(ProviderConfig{Name: ProviderVisionEndpoint, BaseURL: srv.URL + "/v1"}).Vision(context.Background(), []byte("x"), "q"); err != nil {
		t.Fatal(err)
	}
	if _, has := bodies[len(bodies)-1]["response_format"]; has {
		t.Fatal("response_format sent without a requested schema")
	}
}
