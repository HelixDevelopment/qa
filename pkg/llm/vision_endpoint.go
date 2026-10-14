// SPDX-FileCopyrightText: 2026 Milos Vasic
// SPDX-License-Identifier: Apache-2.0

package llm

import (
	"fmt"
	"net/url"
	"strings"
)

// ProviderVisionEndpoint names an operator-supplied OpenAI-compatible
// vision endpoint — typically a local llama-server (for example an
// llmctl `vision` instance on http://127.0.0.1:8082/v1 or `vision-pro`
// on :8083/v1). It is served by the OpenAI-style provider via BaseURL.
const ProviderVisionEndpoint = "vision-endpoint"

// Environment variables that configure the vision endpoint. Only
// EnvVisionEndpoint is required; the API key is optional (a local
// llama-server started without --api-key needs none) and is never logged.
const (
	EnvVisionEndpoint = "HELIX_VISION_ENDPOINT"
	EnvVisionModel    = "HELIX_VISION_MODEL"
	EnvVisionAPIKey   = "HELIX_VISION_API_KEY"
)

// VisionEndpointConfigFromEnv builds the ProviderConfig for the
// OpenAI-compatible vision endpoint from getenv (os.Getenv in
// production). It returns ok=false when HELIX_VISION_ENDPOINT is unset
// or blank — there is no default URL, so an unconfigured endpoint can
// never look configured.
func VisionEndpointConfigFromEnv(getenv func(string) string) (ProviderConfig, bool) {
	base := strings.TrimSpace(getenv(EnvVisionEndpoint))
	if base == "" {
		return ProviderConfig{}, false
	}
	return ProviderConfig{
		Name:    ProviderVisionEndpoint,
		BaseURL: strings.TrimRight(base, "/"),
		Model:   strings.TrimSpace(getenv(EnvVisionModel)),
		APIKey:  strings.TrimSpace(getenv(EnvVisionAPIKey)),
	}, true
}

// DescribeVisionEndpoint renders a log-safe one-line description of the
// endpoint config: URL with any userinfo redacted, the model, and whether
// an API key is set — never the key itself.
func DescribeVisionEndpoint(cfg ProviderConfig) string {
	shown := cfg.BaseURL
	if u, err := url.Parse(cfg.BaseURL); err == nil && u.User != nil {
		u.User = nil
		shown = u.String() + " (userinfo redacted)"
	}
	model := cfg.Model
	if model == "" {
		model = "(server default)"
	}
	key := "unset"
	if cfg.APIKey != "" {
		key = "set"
	}
	return fmt.Sprintf("%s (model: %s, api key: %s)", shown, model, key)
}
