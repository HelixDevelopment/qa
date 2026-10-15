// SPDX-FileCopyrightText: 2026 Milos Vasic
// SPDX-License-Identifier: Apache-2.0

package llmctl

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// pngBytes is a real 1x1 PNG.
var pngBytes = []byte{
	0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 0x00, 0x00, 0x00, 0x0d, 0x49, 0x48, 0x44, 0x52,
	0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01, 0x08, 0x06, 0x00, 0x00, 0x00, 0x1f, 0x15, 0xc4,
	0x89, 0x00, 0x00, 0x00, 0x0d, 0x49, 0x44, 0x41, 0x54, 0x78, 0x9c, 0x63, 0xf8, 0xcf, 0xc0, 0xf0,
	0x1f, 0x00, 0x05, 0x00, 0x01, 0xff, 0x89, 0x99, 0x3d, 0x1d, 0x00, 0x00, 0x00, 0x00, 0x49, 0x45,
	0x4e, 0x44, 0xae, 0x42, 0x60, 0x82,
}

const planJSONGated = `{"tier":"below-minimum","budgets":{"ram_mb":5000,"vram_mb":9712},"profiles":{
"vision":{"port":8082,"fits":false,"tier_ok":false,"min_tier":"baseline","recommended":false,"ram_mb":2048,"vram_mb":4030},
"decide-tiny":{"port":8092,"fits":true,"tier_ok":true,"min_tier":"below-minimum","recommended":true,"ram_mb":2048,"vram_mb":1592}}}`

const planJSONNoFit = `{"tier":"baseline","budgets":{"ram_mb":1000,"vram_mb":9712},"profiles":{
"vision":{"port":8082,"fits":false,"tier_ok":true,"min_tier":"baseline","recommended":false,"ram_mb":2048,"vram_mb":4030}}}`

const planJSONRunnable = `{"tier":"baseline","budgets":{"ram_mb":9000,"vram_mb":9712},"profiles":{
"vision":{"port":8082,"fits":true,"tier_ok":true,"min_tier":"baseline","recommended":true,"ram_mb":2048,"vram_mb":4030}}}`

// fakeLlmctl writes an executable shell script that prints planJSON
// for `plan` and records every invocation's argv[1] in a log file.
func fakeLlmctl(t *testing.T, dir, planJSON string) (bin, log string) {
	t.Helper()
	bin = filepath.Join(dir, "llmctl")
	log = filepath.Join(dir, "calls.log")
	script := "#!/bin/sh\necho \"$1\" >> '" + log + "'\ncase \"$1\" in\nplan) cat <<'EOF_PLAN'\n" +
		planJSON + "\nEOF_PLAN\n;;\nstatus) echo 'profile port'; ;;\n*) echo \"unexpected $1\" >&2; exit 9;;\nesac\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin, log
}

func closedPortURL(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	l.Close()
	return "http://" + addr
}

// ---------- Detect ----------

func TestDetect_EnvWinsOverPathAndVendored(t *testing.T) {
	envDir, pathDir, vendDir := t.TempDir(), t.TempDir(), t.TempDir()
	envBin, _ := fakeLlmctl(t, envDir, planJSONRunnable)
	fakeLlmctl(t, pathDir, planJSONRunnable)
	if err := os.MkdirAll(filepath.Join(vendDir, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	fakeLlmctl(t, filepath.Join(vendDir, "bin"), planJSONRunnable)
	env := map[string]string{EnvBin: envBin, "PATH": pathDir}
	d, err := Detect(DetectOptions{Getenv: func(k string) string { return env[k] }, VendoredDir: vendDir})
	if err != nil || d.Source != SourceEnv || d.Bin != envBin {
		t.Fatalf("want env source %s, got %+v err=%v", envBin, d, err)
	}
}

func TestDetect_PathThenVendored(t *testing.T) {
	pathDir, vendDir := t.TempDir(), t.TempDir()
	pbin, _ := fakeLlmctl(t, pathDir, planJSONRunnable)
	_ = os.MkdirAll(filepath.Join(vendDir, "bin"), 0o755)
	vbin, _ := fakeLlmctl(t, filepath.Join(vendDir, "bin"), planJSONRunnable)

	env := map[string]string{"PATH": "/nonexistent:" + pathDir}
	d, err := Detect(DetectOptions{Getenv: func(k string) string { return env[k] }, VendoredDir: vendDir})
	if err != nil || d.Source != SourcePath || d.Bin != pbin {
		t.Fatalf("want path %s, got %+v err=%v", pbin, d, err)
	}
	env["PATH"] = "/nonexistent"
	d, err = Detect(DetectOptions{Getenv: func(k string) string { return env[k] }, VendoredDir: vendDir})
	if err != nil || d.Source != SourceVendored || d.Bin != vbin {
		t.Fatalf("want vendored %s, got %+v err=%v", vbin, d, err)
	}
	argv := SetupArgv(d)
	if len(argv) != 2 || argv[0] != vbin || argv[1] != "setup" {
		t.Fatalf("setup argv = %v", argv)
	}
}

func TestDetect_AbsentAndUnusableEnv(t *testing.T) {
	env := map[string]string{"PATH": "/nonexistent"}
	_, err := Detect(DetectOptions{Getenv: func(k string) string { return env[k] }})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
	// An explicit but unusable LLMCTL_BIN must NOT silently fall through.
	pathDir := t.TempDir()
	fakeLlmctl(t, pathDir, planJSONRunnable)
	env = map[string]string{EnvBin: "/nonexistent/llmctl", "PATH": pathDir}
	_, err = Detect(DetectOptions{Getenv: func(k string) string { return env[k] }})
	if !errors.Is(err, ErrNotFound) || !strings.Contains(err.Error(), EnvBin) {
		t.Fatalf("unusable env must error, got %v", err)
	}
	// non-executable file
	dir := t.TempDir()
	f := filepath.Join(dir, "llmctl")
	_ = os.WriteFile(f, []byte("x"), 0o644)
	env = map[string]string{EnvBin: f}
	if _, err = Detect(DetectOptions{Getenv: func(k string) string { return env[k] }}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("non-exec must error, got %v", err)
	}
}

// ---------- plan / read-only ----------

func TestParsePlanAndClassify(t *testing.T) {
	p, err := ParsePlan([]byte(planJSONGated))
	if err != nil {
		t.Fatal(err)
	}
	if s, why := p.Classify(VisionProfile); s != StateGated || !strings.Contains(why, "baseline") {
		t.Fatalf("want GATED/baseline, got %s %q", s, why)
	}
	p, _ = ParsePlan([]byte(planJSONNoFit))
	if s, _ := p.Classify(VisionProfile); s != StateNoFit {
		t.Fatalf("want NO_FIT, got %s", s)
	}
	p, _ = ParsePlan([]byte(planJSONRunnable))
	if s, _ := p.Classify(VisionProfile); s != "" {
		t.Fatalf("want runnable, got %s", s)
	}
	if s, _ := p.Classify("nope"); s != StateUnknown {
		t.Fatalf("want UNKNOWN, got %s", s)
	}
	if _, err := ParsePlan([]byte("not json")); err == nil {
		t.Fatal("garbage must error")
	}
	if _, err := ParsePlan([]byte(`{"profiles":{}}`)); err == nil {
		t.Fatal("empty profiles must error")
	}
}

func TestRun_ReadOnlyAllowlist(t *testing.T) {
	bin, log := fakeLlmctl(t, t.TempDir(), planJSONGated)
	for _, forbidden := range []string{"start", "auto", "enable", "switch", "models", "setup", "stop"} {
		if _, err := Run(context.Background(), bin, forbidden); !errors.Is(err, ErrSubcommandNotAllowed) {
			t.Fatalf("%s must be refused, got %v", forbidden, err)
		}
	}
	if b, _ := os.ReadFile(log); len(b) != 0 {
		t.Fatalf("a refused subcommand must not even spawn llmctl, log=%q", b)
	}
	p, err := ReadPlan(context.Background(), bin)
	if err != nil || p.Tier != "below-minimum" {
		t.Fatalf("ReadPlan: %+v %v", p, err)
	}
	b, _ := os.ReadFile(log)
	if strings.TrimSpace(string(b)) != "plan" {
		t.Fatalf("only plan may run, log=%q", b)
	}
}

// ---------- vision client ----------

func completionServer(t *testing.T, content string, captured *map[string]any) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health":
			_, _ = w.Write([]byte(`{"status":"ok"}`))
		case "/v1/models":
			_, _ = w.Write([]byte(`{"data":[{"id":"pinned-model.gguf"}]}`))
		case "/v1/chat/completions":
			if captured != nil {
				_ = json.NewDecoder(r.Body).Decode(captured)
			}
			resp := map[string]any{
				"model":   "pinned-model.gguf",
				"choices": []any{map[string]any{"message": map[string]any{"content": content}}},
				"usage":   map[string]any{"prompt_tokens": 7, "completion_tokens": 2},
			}
			_ = json.NewEncoder(w).Encode(resp)
		default:
			http.NotFound(w, r)
		}
	}))
}

func TestVision_RequestIsDeterministicAndAnswerParsed(t *testing.T) {
	var got map[string]any
	srv := completionServer(t, "  Login button  ", &got)
	defer srv.Close()
	c := NewVisionClient(VisionConfig{BaseURL: srv.URL, ExpectModel: "pinned-model.gguf"})
	a, err := c.Vision(context.Background(), pngBytes, "what is shown?")
	if err != nil {
		t.Fatal(err)
	}
	if a.Content != "Login button" || a.Model != "pinned-model.gguf" || a.InputTokens != 7 || a.OutputTokens != 2 {
		t.Fatalf("answer = %+v", a)
	}
	if got["temperature"].(float64) != 0 {
		t.Fatalf("temperature = %v", got["temperature"])
	}
	if _, ok := got["seed"]; !ok {
		t.Fatal("seed must always be sent")
	}
	raw, _ := json.Marshal(got["messages"])
	if !strings.Contains(string(raw), "data:image/png;base64,") {
		t.Fatalf("image must travel as a png data URL: %s", raw)
	}
}

func TestVision_FailureLegs(t *testing.T) {
	ctx := context.Background()

	// not running
	c := NewVisionClient(VisionConfig{BaseURL: closedPortURL(t), Timeout: 2 * time.Second})
	if _, err := c.Vision(ctx, pngBytes, "p"); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("want ErrNotRunning, got %v", err)
	}
	if st, _ := c.Health(ctx); st != StateNotRunning {
		t.Fatalf("health want NOT_RUNNING, got %s", st)
	}

	// timeout
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(3 * time.Second):
		case <-r.Context().Done():
		}
	}))
	defer slow.Close()
	c = NewVisionClient(VisionConfig{BaseURL: slow.URL, Timeout: 150 * time.Millisecond})
	if _, err := c.Vision(ctx, pngBytes, "p"); !errors.Is(err, ErrTimeout) {
		t.Fatalf("want ErrTimeout, got %v", err)
	}

	// empty answer
	empty := completionServer(t, "   ", nil)
	defer empty.Close()
	c = NewVisionClient(VisionConfig{BaseURL: empty.URL})
	if _, err := c.Vision(ctx, pngBytes, "p"); !errors.Is(err, ErrEmptyAnswer) {
		t.Fatalf("want ErrEmptyAnswer, got %v", err)
	}

	// 503 loading
	loading := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(503)
		_, _ = w.Write([]byte(`{"error":"Loading model"}`))
	}))
	defer loading.Close()
	c = NewVisionClient(VisionConfig{BaseURL: loading.URL})
	var he *HTTPError
	if _, err := c.Vision(ctx, pngBytes, "p"); !errors.As(err, &he) || he.Status != 503 {
		t.Fatalf("want HTTPError 503, got %v", err)
	}
	if st, _ := c.Health(ctx); st != StateUnhealthy {
		t.Fatalf("health want UNHEALTHY, got %s", st)
	}

	// pinned model mismatch
	srv := completionServer(t, "x", nil)
	defer srv.Close()
	c = NewVisionClient(VisionConfig{BaseURL: srv.URL, ExpectModel: "other.gguf"})
	if _, err := c.Vision(ctx, pngBytes, "p"); !errors.Is(err, ErrModelMismatch) {
		t.Fatalf("want ErrModelMismatch, got %v", err)
	}
	if st, _ := c.Health(ctx); st != StateUnhealthy {
		t.Fatalf("pinned mismatch health want UNHEALTHY, got %s", st)
	}

	// bad inputs
	c = NewVisionClient(VisionConfig{BaseURL: srv.URL})
	if _, err := c.Vision(ctx, nil, "p"); !errors.Is(err, ErrEmptyImage) {
		t.Fatalf("want ErrEmptyImage, got %v", err)
	}
	if _, err := c.Vision(ctx, []byte("just text, no image"), "p"); err == nil {
		t.Fatal("non-image payload must be refused")
	}
}

// ---------- ResolveVision ----------

func TestResolveVision_States(t *testing.T) {
	ctx := context.Background()
	closed := closedPortURL(t)

	// ABSENT
	env := map[string]string{"PATH": "/nonexistent"}
	st := ResolveVision(ctx, ResolveOptions{
		Detect: DetectOptions{Getenv: func(k string) string { return env[k] }},
		Vision: VisionConfig{BaseURL: closed, Timeout: time.Second},
	})
	if st.State != StateAbsent || !strings.Contains(st.Detail, "llmctl setup") {
		t.Fatalf("want ABSENT with setup hint, got %+v", st)
	}

	// GATED
	d := t.TempDir()
	bin, log := fakeLlmctl(t, d, planJSONGated)
	env = map[string]string{EnvBin: bin}
	opts := ResolveOptions{
		Detect: DetectOptions{Getenv: func(k string) string { return env[k] }},
		Vision: VisionConfig{BaseURL: closed, Timeout: time.Second},
	}
	st = ResolveVision(ctx, opts)
	if st.State != StateGated {
		t.Fatalf("want GATED, got %+v", st)
	}
	if b, _ := os.ReadFile(log); strings.TrimSpace(string(b)) != "plan" {
		t.Fatalf("only plan may run, log=%q", b)
	}

	// NOT_RUNNING (plan says runnable, nothing listens)
	bin2, _ := fakeLlmctl(t, t.TempDir(), planJSONRunnable)
	env[EnvBin] = bin2
	st = ResolveVision(ctx, opts)
	if st.State != StateNotRunning {
		t.Fatalf("want NOT_RUNNING, got %+v", st)
	}

	// READY wins even when the plan says GATED
	srv := completionServer(t, "ok", nil)
	defer srv.Close()
	env[EnvBin] = bin
	opts.Vision.BaseURL = srv.URL
	st = ResolveVision(ctx, opts)
	if st.State != StateReady {
		t.Fatalf("want READY, got %+v", st)
	}
}

// ---------- decide gateway ----------

func decideServer(t *testing.T, key string, status int, body string) (*httptest.Server, *int32) {
	t.Helper()
	var calls int32
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		if r.URL.Path != "/v1/systemone" || r.Header.Get("Authorization") != "Bearer "+key {
			w.WriteHeader(401)
			_, _ = w.Write([]byte(`{"message":"no","error_type":"unauthorized"}`))
			return
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	return srv, &calls
}

func caFileFor(t *testing.T, srv *httptest.Server) string {
	t.Helper()
	f := filepath.Join(t.TempDir(), "ca.pem")
	der := srv.Certificate().Raw
	if err := os.WriteFile(f, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	return f
}

func TestDecide_TypedAnswers(t *testing.T) {
	const key = "test-key-not-a-real-secret-0123456789"
	body := `{"model":"decide-tiny","answers":{"q":{"type":"choice","choice":"billing","probabilities":{"billing":0.96,"legal":0.04},"confidence":0.93,"maturity":"experimental"}},"usage":{"input_tokens":5,"output_tokens":1}}`
	srv, _ := decideServer(t, key, 200, body)
	defer srv.Close()
	c, err := NewDecideClient(DecideConfig{BaseURL: srv.URL, APIKey: key, CAFile: caFileFor(t, srv)})
	if err != nil {
		t.Fatal(err)
	}
	ch, a, err := c.Choice(context.Background(), "Routing.", "Which team?", map[string]string{"billing": "invoices", "legal": "contracts"})
	if err != nil || ch != "billing" || a.Confidence != 0.93 {
		t.Fatalf("choice=%q a=%+v err=%v", ch, a, err)
	}
	if !a.Experimental() {
		t.Fatal("experimental maturity must be reported as experimental")
	}

	srv2, _ := decideServer(t, key, 200, `{"model":"decide-nli","answers":{"q":{"type":"noul","noul":0.8}}}`)
	defer srv2.Close()
	c2, _ := NewDecideClient(DecideConfig{BaseURL: srv2.URL, APIKey: key, CAFile: caFileFor(t, srv2)})
	p, a2, err := c2.Noul(context.Background(), "s", "is it?")
	if err != nil || p != 0.8 || !a2.Experimental() {
		t.Fatalf("noul p=%v a=%+v err=%v (missing maturity must read as experimental)", p, a2, err)
	}
}

func TestDecide_FailureLegs(t *testing.T) {
	const key = "test-key-not-a-real-secret-0123456789"
	ctx := context.Background()

	for _, tc := range []struct {
		name   string
		status int
		body   string
		want   error
	}{
		{"not_ready", 503, `{"message":"no engine","error_type":"not_ready"}`, ErrNotReady},
		{"readout", 422, `{"message":"bad","error_type":"readout_failed"}`, ErrReadoutFailed},
	} {
		srv, _ := decideServer(t, key, tc.status, tc.body)
		c, _ := NewDecideClient(DecideConfig{BaseURL: srv.URL, APIKey: key, CAFile: caFileFor(t, srv)})
		_, _, err := c.Noul(ctx, "s", "q")
		srv.Close()
		if !errors.Is(err, tc.want) {
			t.Fatalf("%s: want %v got %v", tc.name, tc.want, err)
		}
	}

	// wrong key -> 401, and the key never appears in the error
	srv, _ := decideServer(t, key, 200, `{}`)
	defer srv.Close()
	c, _ := NewDecideClient(DecideConfig{BaseURL: srv.URL, APIKey: "wrong-key-value-xyz", CAFile: caFileFor(t, srv)})
	_, _, err := c.Noul(ctx, "s", "q")
	if !errors.Is(err, ErrUnauthorized) || strings.Contains(err.Error(), "wrong-key-value-xyz") {
		t.Fatalf("want ErrUnauthorized without key, got %v", err)
	}

	// answer type mismatch (gateway answered a different type)
	srvM, _ := decideServer(t, key, 200, `{"model":"m","answers":{"q":{"type":"choice","choice":"a","probabilities":{"a":1},"confidence":1}}}`)
	defer srvM.Close()
	cm, _ := NewDecideClient(DecideConfig{BaseURL: srvM.URL, APIKey: key, CAFile: caFileFor(t, srvM)})
	if _, _, err := cm.Noul(ctx, "s", "q"); !errors.Is(err, ErrBadAnswerType) {
		t.Fatalf("want ErrBadAnswerType, got %v", err)
	}

	// unknown CA: TLS verification is never disabled
	srvOK, _ := decideServer(t, key, 200, `{"model":"m","answers":{"q":{"type":"noul","noul":0.5}}}`)
	defer srvOK.Close()
	cok, _ := NewDecideClient(DecideConfig{BaseURL: srvOK.URL, APIKey: key, CAFile: caFileFor(t, srvOK)})
	if _, _, err := cok.Noul(ctx, "s", "q"); err != nil {
		t.Fatalf("control: trusted CA must succeed, got %v", err)
	}
	cn, _ := NewDecideClient(DecideConfig{BaseURL: srvOK.URL, APIKey: key, Timeout: 2 * time.Second})
	if _, _, err := cn.Noul(ctx, "s", "q"); err == nil {
		t.Fatal("untrusted certificate must fail")
	}

	// no key at all
	if _, err := NewDecideClientFromEnv(func(string) string { return "" }); !errors.Is(err, ErrNoAPIKey) {
		t.Fatalf("want ErrNoAPIKey, got %v", err)
	}
	// down gateway
	cd, _ := NewDecideClient(DecideConfig{BaseURL: strings.Replace(closedPortURL(t), "http:", "https:", 1), APIKey: key, Timeout: 2 * time.Second})
	if _, _, err := cd.Noul(ctx, "s", "q"); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("want ErrNotRunning, got %v", err)
	}
	if _, err := cd.Decide(ctx, "", "s", nil); err == nil {
		t.Fatal("zero questions must be refused")
	}
}

// ---------- oracle / determinism (11.4.269) ----------

type modelOracle struct{}

func (modelOracle) Name() string                                         { return "vision-model" }
func (modelOracle) ModelBacked() bool                                    { return true }
func (modelOracle) Confirm(context.Context, string) (bool, string, error) { return true, "model says yes", nil }

func TestConfirm_ModelAloneIsNeverAGate(t *testing.T) {
	ctx := context.Background()
	if v := Confirm(ctx, "Login", nil); v.Pass() || v.Status != VerdictUnconfirmed {
		t.Fatalf("nil oracle: %+v", v)
	}
	if v := Confirm(ctx, "Login", modelOracle{}); v.Pass() || v.Status != VerdictUnconfirmed {
		t.Fatalf("model-backed oracle must be refused: %+v", v)
	}
	ocr := OCRTextOracle{Text: "Welcome. Press LOGIN to continue", Source: "tesseract"}
	if v := Confirm(ctx, "login", ocr); !v.Pass() || v.Oracle != "ocr-text:tesseract" {
		t.Fatalf("ocr confirm: %+v", v)
	}
	// WRONG model answer: the oracle contradicts it
	if v := Confirm(ctx, "Checkout", ocr); v.Status != VerdictRejected || v.Pass() {
		t.Fatalf("wrong answer must be REJECTED: %+v", v)
	}
	// blind instrument: empty OCR text is not an absence
	if v := Confirm(ctx, "Login", OCRTextOracle{}); v.Status != VerdictUnconfirmed {
		t.Fatalf("blind oracle must be UNCONFIRMED: %+v", v)
	}
}

func TestRunDeterministic(t *testing.T) {
	ctx := context.Background()
	r, err := RunDeterministic(ctx, 3, func(context.Context) (string, error) { return "Login  button\n", nil })
	if err != nil || r.Runs != 3 || len(r.Hash) != 64 {
		t.Fatalf("identical runs: %+v %v", r, err)
	}
	var n int
	_, err = RunDeterministic(ctx, 3, func(context.Context) (string, error) { n++; return strings.Repeat("a", n), nil })
	if !errors.Is(err, ErrNonDeterministic) {
		t.Fatalf("want ErrNonDeterministic, got %v", err)
	}
	if _, err = RunDeterministic(ctx, 2, func(context.Context) (string, error) { return "x", nil }); err == nil {
		t.Fatal("n<3 must be refused")
	}
	if CanonicalHash("a  b") != CanonicalHash("a b") || CanonicalHash("a b") == CanonicalHash("a c") {
		t.Fatal("canonical hash normalisation broken")
	}
}
