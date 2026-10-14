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
