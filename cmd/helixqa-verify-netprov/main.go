// SPDX-FileCopyrightText: 2026 Milos Vasic
// SPDX-License-Identifier: Apache-2.0

// Command helixqa-verify-netprov is the RUNNABLE analyzer for HelixQA's
// HelixAgent NETWORK-PROVIDER capability bank
// (`banks/helixllm_network_provider.yaml`, §11.4.169). It drives a real
// text chat/completions request against a live HelixLLM (llama.cpp
// server, e.g. Qwen3-Coder-30B) endpoint reached over a LAN/VPN address
// rather than localhost/loopback — the exact topology HelixAgent's
// network-provider capability targets (helix_agent afbeb1bb / cfa94f2f,
// env-var-parameterized `HELIX_LLM_HOST` / `HELIX_LLM_LOCAL_OPENAI_ENDPOINT`)
// — writes a machine-readable verdict artefact, and exits with a
// PASS/FAIL code the bank's RequiredEvidence content assertion gates on.
//
// It mirrors the design of cmd/helixqa-verify-vision (Bank B) but is
// TEXT-ONLY (plain OpenAI-compatible `content: string` message, not the
// multimodal content-part array) and adds a LAN/loopback discriminator:
// the verdict records whether the resolved endpoint host is a loopback
// address, and (when --require-lan is set, the bank's default) a
// loopback endpoint fails the LAN check regardless of the chat response
// content — closing the "the provider merely answers on localhost" gap
// this bank exists to prove is NOT the mechanism under test.
//
// ANTI-BLUFF (§11.4.6 / §11.4.69 / §11.4.107(10) / §11.4.123). The
// verdict is a STRICT fact-matching check over the model's genuine
// response text AND a strict non-loopback host check — never a
// lenient/partial match, never a hardcoded PASS. This is the single
// analyzer used for BOTH the golden-good fixture (must PASS) and the
// golden-bad fixtures (must FAIL: wrong facts, and separately a
// loopback-endpoint fixture proving the LAN check itself is load-bearing)
// via the same --expect-fail inversion convention already used by
// cmd/helixqa-verify-vision / helixcode-ensemble-members.yaml HXC-ENS-003.
//
// Usage:
//
//	helixqa-verify-netprov \
//	  --endpoint http://198.51.100.221:18434/v1/chat/completions \
//	  --model /models/Qwen3-Coder-30B-A3B-Instruct-Q4_K_M.gguf \
//	  --prompt "reply with only the word OK" \
//	  --expect "ok" \
//	  --require-lan \
//	  --out qa-results/helixllm_network_provider/lan_basic_001_verdict.json \
//	  [--forbid "error"] [--conduit-dir qa-results/helixllm_network_provider/conduit] \
//	  [--challenge-id NETPROV-LAN-BASIC-001] [--timeout 30s] [--max-tokens 16] \
//	  [--expect-fail]
//
// Exit codes (machine-readable for the Dispatcher, pkg/testbank/dispatch.go):
//
//	0 -> case_result==true (this case succeeded)
//	1 -> case_result==false (fact mismatch / LAN-check failure / analyzer
//	     wrongly accepted a golden-bad fixture under --expect-fail)
//	2 -> infra/usage error (endpoint unreachable, bad flags, bad URL)
//
// DECOUPLING (CONST-051(B) / §11.4.28). The endpoint URL, model id,
// prompt, and expected/forbidden fact tokens are ALL flags/bank data —
// nothing about the consuming project (HelixAgent's network-provider env
// vars, HelixLLM's coder port) is hardcoded in this tool beyond a
// documented default matching HelixLLM's on-demand-infra convention.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"digital.vasic.helixqa/pkg/conduit"
)

const (
	exitPass  = 0
	exitFail  = 1
	exitInfra = 2
)

// chatMessage is the plain (text-only) OpenAI-compatible message shape —
// deliberately distinct from cmd/helixqa-verify-vision's multimodal
// content-part array: this analyzer targets the llama.cpp server's
// standard /v1/chat/completions text endpoint, confirmed live via
// `curl -X POST http://198.51.100.221:18434/v1/chat/completions` during
// authoring (2026-07-08).
type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatRequest struct {
	Model       string        `json:"model"`
	Temperature float64       `json:"temperature"`
	MaxTokens   int           `json:"max_tokens"`
	Messages    []chatMessage `json:"messages"`
}

type chatChoice struct {
	Index        int    `json:"index"`
	FinishReason string `json:"finish_reason"`
	Message      struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	} `json:"message"`
}

type chatResponse struct {
	ID      string       `json:"id"`
	Model   string       `json:"model"`
	Choices []chatChoice `json:"choices"`
	Usage   struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage"`
}

// verdict is the machine-readable artefact the ContentAssertingResolver
// gates on (embeds the raw response + LAN-check outcome + ground-truth +
// metric — auditable, never trust-the-analyzer-blindly).
type verdict struct {
	Endpoint       string   `json:"endpoint"`
	ResolvedHost   string   `json:"resolved_host"`
	IsLoopback     bool     `json:"is_loopback"`
	RequireLAN     bool     `json:"require_lan"`
	LANCheckPass   bool     `json:"lan_check_pass"`
	Prompt         string   `json:"prompt"`
	ModelRequested string   `json:"model_requested"`
	ModelServed    string   `json:"model_served"`
	ExpectedFacts  []string `json:"expected_facts"`
	ForbiddenFacts []string `json:"forbidden_facts,omitempty"`
	Response       string   `json:"response"`
	MatchedFacts   int      `json:"matched_facts"`
	ExpectedCount  int      `json:"expected_count"`
	Hallucinated   bool     `json:"hallucinated"`
	Pass           bool     `json:"pass"`
	ExpectFail     bool     `json:"expect_fail"`
	CaseResult     bool     `json:"case_result"`
	LatencyMS      int64    `json:"latency_ms"`
	HTTPStatus     int      `json:"http_status"`
	PromptTokens   int      `json:"prompt_tokens"`
	CompletionToks int      `json:"completion_tokens"`
	Error          string   `json:"error,omitempty"`
}

func main() {
	os.Exit(run())
}

func run() int {
	var (
		endpoint    = flag.String("endpoint", envOr("HELIX_LLM_LOCAL_OPENAI_ENDPOINT", "http://localhost:18434/v1/chat/completions"), "network-provider chat/completions endpoint (LAN IP expected)")
		model       = flag.String("model", envOr("HELIX_LLM_MODEL", "/models/Qwen3-Coder-30B-A3B-Instruct-Q4_K_M.gguf"), "model id to request")
		prompt      = flag.String("prompt", "", "prompt to send (required)")
		expectCSV   = flag.String("expect", "", "comma-separated required fact tokens (case-insensitive substring match)")
		forbidCSV   = flag.String("forbid", "", "comma-separated forbidden fact tokens (presence forces hallucinated=true)")
		requireLAN  = flag.Bool("require-lan", true, "fail the LAN check (and thus pass=false) when the resolved endpoint host is loopback/localhost")
		out         = flag.String("out", "", "path to write the verdict JSON (required)")
		conduitDir  = flag.String("conduit-dir", "", "optional conduit JSONL event dir (§11.4.116)")
		challengeID = flag.String("challenge-id", "", "challenge id for conduit events (defaults to --out basename)")
		timeout     = flag.Duration("timeout", 30*time.Second, "request timeout")
		maxTokens   = flag.Int("max-tokens", 16, "max_tokens for the completion request")
		expectFail  = flag.Bool("expect-fail", false, "invert the case-level exit code — for golden-bad self-validation fixtures (mirrors cmd/helixqa-verify-vision: the RAW verdict (\"pass\") is still recorded honestly; \"case_result\" is what the bank's RequiredEvidence gates on)")
	)
	flag.Parse()

	if *prompt == "" || *out == "" {
		fmt.Fprintln(os.Stderr, "usage: helixqa-verify-netprov --prompt <text> --out <verdict.json> [--endpoint url] [--expect a,b] [--forbid c,d] [--require-lan] [--model id]")
		return exitInfra
	}
	cid := *challengeID
	if cid == "" {
		cid = strings.TrimSuffix(filepath.Base(*out), filepath.Ext(*out))
	}

	var sink conduit.Sink = conduit.NopSink()
	if *conduitDir != "" {
		w, werr := conduit.NewWriter(conduit.Config{Session: "helixllm_network_provider", Dir: *conduitDir})
		if werr == nil {
			sink = w
			defer w.Close()
		}
	}
	conduit.ChallengeStart(sink, cid, "network-provider")

	v := verdict{
		Endpoint:       *endpoint,
		Prompt:         *prompt,
		ModelRequested: *model,
		ExpectedFacts:  splitCSV(*expectCSV),
		ForbiddenFacts: splitCSV(*forbidCSV),
		RequireLAN:     *requireLAN,
		ExpectFail:     *expectFail,
	}
	v.ExpectedCount = len(v.ExpectedFacts)

	// --- LAN/loopback discriminator (the property this bank exists to prove,
	// distinct from cmd/helixqa-verify-vision which has no such check) ---
	host, isLoopback, hostErr := resolveIsLoopback(*endpoint)
	if hostErr != nil {
		return failInfra(sink, cid, &v, *out, fmt.Sprintf("parse/resolve endpoint host: %v", hostErr))
	}
	v.ResolvedHost = host
	v.IsLoopback = isLoopback
	v.LANCheckPass = !(*requireLAN && isLoopback)

	reqBody := chatRequest{
		Model:       *model,
		Temperature: 0,
		MaxTokens:   *maxTokens,
		Messages: []chatMessage{
			{Role: "user", Content: *prompt},
		},
	}
	payload, err := json.Marshal(reqBody)
	if err != nil {
		return failInfra(sink, cid, &v, *out, fmt.Sprintf("marshal request: %v", err))
	}

	client := &http.Client{Timeout: *timeout}
	start := time.Now()
	httpReq, err := http.NewRequest(http.MethodPost, *endpoint, bytes.NewReader(payload))
	if err != nil {
		return failInfra(sink, cid, &v, *out, fmt.Sprintf("build request: %v", err))
	}
	httpReq.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(httpReq)
	latency := time.Since(start)
	v.LatencyMS = latency.Milliseconds()
	if err != nil {
		return failInfra(sink, cid, &v, *out, fmt.Sprintf("http request: %v", err))
	}
	defer resp.Body.Close()
	v.HTTPStatus = resp.StatusCode

	var cr chatResponse
	if decErr := json.NewDecoder(resp.Body).Decode(&cr); decErr != nil {
		return failInfra(sink, cid, &v, *out, fmt.Sprintf("decode response: %v", decErr))
	}
	if resp.StatusCode != http.StatusOK || len(cr.Choices) == 0 {
		return failInfra(sink, cid, &v, *out, fmt.Sprintf("non-200 or empty choices (status=%d)", resp.StatusCode))
	}

	v.ModelServed = cr.Model
	v.Response = cr.Choices[0].Message.Content
	v.PromptTokens = cr.Usage.PromptTokens
	v.CompletionToks = cr.Usage.CompletionTokens

	// --- strict fact-matching (§11.4.6 / §11.4.107(10)) ---
	lowerResp := strings.ToLower(v.Response)
	matched := 0
	for _, f := range v.ExpectedFacts {
		if strings.Contains(lowerResp, strings.ToLower(f)) {
			matched++
		}
	}
	v.MatchedFacts = matched

	hallucinated := false
	for _, f := range v.ForbiddenFacts {
		if f != "" && strings.Contains(lowerResp, strings.ToLower(f)) {
			hallucinated = true
			break
		}
	}
	v.Hallucinated = hallucinated

	// Pass requires BOTH the fact-match AND the LAN check — a correct
	// answer served over loopback (when --require-lan is set) is NOT a
	// pass, because this bank exists to prove the LAN path, not merely
	// that the model can answer.
	v.Pass = v.ExpectedCount > 0 && v.MatchedFacts == v.ExpectedCount && !v.Hallucinated && v.LANCheckPass

	// --expect-fail (golden-bad self-validation, §11.4.107(10)): the RAW
	// verdict ("pass") is recorded honestly and NEVER flipped — what
	// inverts is which raw outcome counts as this CASE succeeding.
	if v.ExpectFail {
		v.CaseResult = !v.Pass
	} else {
		v.CaseResult = v.Pass
	}

	if writeErr := writeVerdict(*out, &v); writeErr != nil {
		fmt.Fprintf(os.Stderr, "ERROR: write verdict: %v\n", writeErr)
		return exitInfra
	}

	conduit.LLMCall(sink, cid, latency, map[string]any{
		"model_served":      v.ModelServed,
		"resolved_host":     v.ResolvedHost,
		"is_loopback":       v.IsLoopback,
		"prompt_tokens":     v.PromptTokens,
		"completion_tokens": v.CompletionToks,
		"latency_ms":        v.LatencyMS,
	})
	conduit.EvidenceCaptured(sink, cid, "network_provider_verdict_json", *out)

	if v.CaseResult {
		conduit.ChallengeVerdict(sink, cid, conduit.VerdictPass, "")
		fmt.Printf("PASS: %s host=%s loopback=%v matched=%d/%d hallucinated=%v expect_fail=%v raw_pass=%v response=%q\n", cid, v.ResolvedHost, v.IsLoopback, v.MatchedFacts, v.ExpectedCount, v.Hallucinated, v.ExpectFail, v.Pass, v.Response)
		return exitPass
	}
	reason := fmt.Sprintf("host=%s loopback=%v lan_check_pass=%v matched=%d/%d hallucinated=%v expect_fail=%v raw_pass=%v", v.ResolvedHost, v.IsLoopback, v.LANCheckPass, v.MatchedFacts, v.ExpectedCount, v.Hallucinated, v.ExpectFail, v.Pass)
	conduit.ChallengeVerdict(sink, cid, conduit.VerdictFail, reason)
	fmt.Printf("FAIL: %s %s response=%q\n", cid, reason, v.Response)
	return exitFail
}

func failInfra(sink conduit.Sink, cid string, v *verdict, out, reason string) int {
	v.Error = reason
	v.Pass = false
	// An infra error still respects --expect-fail: a fixture that
	// deliberately points at an unreachable endpoint to prove the RED
	// path is honestly infra-FAIL at the raw level, and case_result is
	// the (possibly-inverted) success signal — mirrors the in-band
	// fact-mismatch inversion below rather than being a separate code path.
	if v.ExpectFail {
		v.CaseResult = !v.Pass
	} else {
		v.CaseResult = v.Pass
	}
	if writeErr := writeVerdict(out, v); writeErr != nil {
		fmt.Fprintf(os.Stderr, "ERROR: write verdict (after infra error %q): %v\n", reason, writeErr)
	}
	conduit.Errorf(sink, cid, reason)
	if v.CaseResult {
		conduit.ChallengeVerdict(sink, cid, conduit.VerdictPass, reason)
		fmt.Printf("PASS (expect-fail, infra-error honestly caught): %s: %s\n", cid, reason)
		return exitPass
	}
	conduit.ChallengeVerdict(sink, cid, conduit.VerdictFail, reason)
	fmt.Fprintf(os.Stderr, "INFRA-ERROR: %s: %s\n", cid, reason)
	return exitInfra
}

// resolveIsLoopback parses the endpoint URL and reports whether its host
// is a loopback address (127.0.0.0/8, ::1, or the literal "localhost").
// It does NOT perform a DNS lookup for non-IP, non-"localhost" hostnames
// (§11.4.6 — no speculative resolution beyond what the discriminator
// needs); an IP-literal host is checked directly via net.ParseIP.
func resolveIsLoopback(endpoint string) (host string, loopback bool, err error) {
	u, err := url.Parse(endpoint)
	if err != nil {
		return "", false, fmt.Errorf("invalid endpoint URL %q: %w", endpoint, err)
	}
	h := u.Hostname()
	if h == "" {
		return "", false, fmt.Errorf("endpoint URL %q has no host", endpoint)
	}
	if strings.EqualFold(h, "localhost") {
		return h, true, nil
	}
	if ip := net.ParseIP(h); ip != nil {
		return h, ip.IsLoopback(), nil
	}
	// Non-IP, non-"localhost" hostname (e.g. a real LAN DNS name): treat
	// as non-loopback without a DNS round-trip — a genuinely misconfigured
	// hostname will surface as an HTTP connection failure downstream,
	// which is itself honestly captured (infra error), never silently
	// mis-classified as a LAN pass.
	return h, false, nil
}

func writeVerdict(path string, v *verdict) error {
	if dir := filepath.Dir(path); dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}

func splitCSV(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
