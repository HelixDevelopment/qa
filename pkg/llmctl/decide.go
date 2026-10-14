// SPDX-FileCopyrightText: 2026 Milos Vasic
// SPDX-License-Identifier: Apache-2.0

package llmctl

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// Environment variable NAMES used by the decision gateway client.
// Values are read at call time and never logged or put in an error.
const (
	EnvDecideURL = "LLMCTL_DECIDE_URL"
	EnvAPIKey    = "LLMCTL_API_KEY"
	EnvCAFile    = "LLMCTL_CA_FILE"
	// DefaultDecideURL is the gateway default (port 8095, HTTPS).
	DefaultDecideURL = "https://127.0.0.1:8095"
)

// Typed question kinds of the gateway.
const (
	TypeNoul   = "noul"
	TypeChoice = "choice"
	TypeScore  = "score"
)

// Errors returned by DecideClient.
var (
	ErrNoAPIKey      = errors.New("llmctl: decide gateway key not set (" + EnvAPIKey + ")")
	ErrNotReady      = errors.New("llmctl: decide gateway has no ready engine (503 not_ready)")
	ErrReadoutFailed = errors.New("llmctl: decide readout failed (422 readout_failed, not retryable)")
	ErrUnauthorized  = errors.New("llmctl: decide gateway rejected the key (401)")
	ErrBadAnswerType = errors.New("llmctl: answer type does not match the question type")
)

// Question is one typed question. Criteria maps option -> description
// for choice and score questions.
type Question struct {
	Type         string            `json:"type"`
	Instructions string            `json:"instructions"`
	Criteria     map[string]string `json:"criteria,omitempty"`
}

// DecideAnswer is one typed answer.
type DecideAnswer struct {
	Type          string             `json:"type"`
	Noul          float64            `json:"noul"`
	Choice        string             `json:"choice"`
	Score         float64            `json:"score"`
	Probabilities map[string]float64 `json:"probabilities"`
	Confidence    float64            `json:"confidence"`
	// Maturity is "experimental" for a type the profile has not
	// measured above baseline. An experimental answer is advisory:
	// Experimental() reports it so callers never gate on it.
	Maturity string `json:"maturity"`
}

// Experimental reports whether the answer is not trustworthy enough
// to be more than a hint. Anything other than an explicit measured
// maturity is treated as experimental (conservative default).
func (a DecideAnswer) Experimental() bool {
	switch strings.ToLower(a.Maturity) {
	case "measured", "calibrated", "stable":
		return false
	}
	return true
}

// DecideResponse is the gateway envelope.
type DecideResponse struct {
	Model   string                  `json:"model"`
	Answers map[string]DecideAnswer `json:"answers"`
}

// DecideConfig configures a DecideClient.
type DecideConfig struct {
	BaseURL string
	// APIKey is the bearer key. Prefer NewDecideClientFromEnv.
	APIKey string
	// CAFile is a PEM bundle to trust (the llmctl local CA).
	CAFile  string
	Timeout time.Duration
	// HTTPClient overrides the transport (tests with httptest TLS).
	HTTPClient *http.Client
}

// DecideClient is a client for the llmctl decision gateway.
type DecideClient struct {
	cfg DecideConfig
	hc  *http.Client
}

// NewDecideClientFromEnv reads LLMCTL_DECIDE_URL, LLMCTL_API_KEY and
// LLMCTL_CA_FILE (falling back to SSL_CERT_FILE) through getenv.
func NewDecideClientFromEnv(getenv func(string) string) (*DecideClient, error) {
	if getenv == nil {
		getenv = os.Getenv
	}
	ca := getenv(EnvCAFile)
	if ca == "" {
		ca = getenv("SSL_CERT_FILE")
	}
	return NewDecideClient(DecideConfig{
		BaseURL: getenv(EnvDecideURL),
		APIKey:  getenv(EnvAPIKey),
		CAFile:  ca,
	})
}

// NewDecideClient builds a client. TLS verification is never
// disabled; trust is the system pool plus the optional CAFile.
func NewDecideClient(cfg DecideConfig) (*DecideClient, error) {
	if cfg.APIKey == "" {
		return nil, ErrNoAPIKey
	}
	if cfg.BaseURL == "" {
		cfg.BaseURL = DefaultDecideURL
	}
	cfg.BaseURL = strings.TrimRight(cfg.BaseURL, "/")
	if cfg.Timeout == 0 {
		cfg.Timeout = 30 * time.Second
	}
	hc := cfg.HTTPClient
	if hc == nil {
		pool, err := x509.SystemCertPool()
		if err != nil || pool == nil {
			pool = x509.NewCertPool()
		}
		if cfg.CAFile != "" {
			pem, err := os.ReadFile(cfg.CAFile)
			if err != nil {
				return nil, fmt.Errorf("llmctl: read CA file: %w", err)
			}
			if !pool.AppendCertsFromPEM(pem) {
				return nil, errors.New("llmctl: CA file holds no certificate")
			}
		}
		hc = &http.Client{Transport: &http.Transport{
			TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12},
		}}
	}
	return &DecideClient{cfg: cfg, hc: hc}, nil
}

// Decide posts one POST /v1/systemone request.
func (c *DecideClient) Decide(ctx context.Context, model, state string, qs map[string]Question) (*DecideResponse, error) {
	if len(qs) == 0 {
		return nil, errors.New("llmctl: at least one question is required")
	}
	if model == "" {
		model = "llmctl-decide-tiny"
	}
	raw, _ := json.Marshal(map[string]any{"model": model, "state": state, "questions": qs})
	cctx, cancel := context.WithTimeout(ctx, c.cfg.Timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, http.MethodPost, c.cfg.BaseURL+"/v1/systemone", bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.cfg.APIKey)
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, redactKey(classifyNetErr(cctx, err), c.cfg.APIKey)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode != http.StatusOK {
		return nil, mapGatewayError(resp.StatusCode, body)
	}
	var out DecideResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("llmctl: parse decide response: %w", err)
	}
	for name, q := range qs {
		a, ok := out.Answers[name]
		if !ok || a.Type != q.Type {
			return nil, fmt.Errorf("%w: question %q", ErrBadAnswerType, name)
		}
	}
	return &out, nil
}

// Noul asks one yes/no question and returns the yes-probability.
func (c *DecideClient) Noul(ctx context.Context, state, instructions string) (float64, DecideAnswer, error) {
	r, err := c.Decide(ctx, "", state, map[string]Question{"q": {Type: TypeNoul, Instructions: instructions}})
	if err != nil {
		return 0, DecideAnswer{}, err
	}
	a := r.Answers["q"]
	return a.Noul, a, nil
}

// Choice asks one single-choice question.
func (c *DecideClient) Choice(ctx context.Context, state, instructions string, criteria map[string]string) (string, DecideAnswer, error) {
	r, err := c.Decide(ctx, "", state, map[string]Question{"q": {Type: TypeChoice, Instructions: instructions, Criteria: criteria}})
	if err != nil {
		return "", DecideAnswer{}, err
	}
	a := r.Answers["q"]
	return a.Choice, a, nil
}

// Score asks one scoring question.
func (c *DecideClient) Score(ctx context.Context, state, instructions string, criteria map[string]string) (float64, DecideAnswer, error) {
	r, err := c.Decide(ctx, "", state, map[string]Question{"q": {Type: TypeScore, Instructions: instructions, Criteria: criteria}})
	if err != nil {
		return 0, DecideAnswer{}, err
	}
	a := r.Answers["q"]
	return a.Score, a, nil
}

func mapGatewayError(status int, body []byte) error {
	var e struct {
		Message   string `json:"message"`
		ErrorType string `json:"error_type"`
	}
	_ = json.Unmarshal(body, &e)
	switch {
	case status == http.StatusUnauthorized:
		return ErrUnauthorized
	case status == http.StatusServiceUnavailable && e.ErrorType == "not_ready":
		return ErrNotReady
	case status == http.StatusUnprocessableEntity && e.ErrorType == "readout_failed":
		return ErrReadoutFailed
	}
	return &HTTPError{Status: status, Body: truncate(e.ErrorType+": "+e.Message, 200)}
}

// redactKey guarantees the key can never surface in an error string.
func redactKey(err error, key string) error {
	if err == nil || key == "" || !strings.Contains(err.Error(), key) {
		return err
	}
	return errors.New(strings.ReplaceAll(err.Error(), key, "[REDACTED]"))
}
