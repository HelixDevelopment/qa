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
	cfg, ok, err := VisionEndpointConfigFromEnvChecked(getenv)
	return cfg, ok && err == nil
}

// VisionEndpointConfigFromEnvChecked is VisionEndpointConfigFromEnv with
// the validation error surfaced.
//
// The endpoint must be http(s)://host[:port][/path] with NO user info, query
// or fragment: those are where secrets get pasted, and anything in the URL
// ends up in transport errors, logs and reports. The error never echoes
// the URL. Put a key in HELIX_VISION_API_KEY instead.
func VisionEndpointConfigFromEnvChecked(getenv func(string) string) (ProviderConfig, bool, error) {
	cfg, ok := visionEndpointConfigRaw(getenv)
	if !ok {
		return cfg, false, nil
	}
	refuse := func(why string) (ProviderConfig, bool, error) {
		return ProviderConfig{}, false, fmt.Errorf("%s refused: %s — it must be "+
			"http(s)://host[:port][/path] with no user info, query or fragment "+
			"(the value is not printed; put a key in %s)", EnvVisionEndpoint, why, EnvVisionAPIKey)
	}
	u, err := url.Parse(cfg.BaseURL)
	if err != nil {
		return refuse("it does not parse as a URL")
	}
	switch {
	case u.Scheme != "http" && u.Scheme != "https":
		return refuse("scheme is not http or https")
	case u.Host == "":
		return refuse("it has no host")
	case u.User != nil:
		return refuse("it carries user info")
	case u.RawQuery != "" || u.ForceQuery:
		return refuse("it carries a query string")
	case u.Fragment != "" || strings.Contains(cfg.BaseURL, "#"):
		return refuse("it carries a fragment")
	}
	return cfg, true, nil
}

func visionEndpointConfigRaw(getenv func(string) string) (ProviderConfig, bool) {
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
// endpoint config: scheme://host/path only (userinfo, query and fragment
// redacted; an unparsable URL is not printed at all), the model, and
// whether an API key is set — never the key itself.
func DescribeVisionEndpoint(cfg ProviderConfig) string {
	shown := "(unparsable endpoint URL — not printed)"
	if u, err := url.Parse(cfg.BaseURL); err == nil && u.Host != "" {
		redacted := u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.ForceQuery
		clean := url.URL{Scheme: u.Scheme, Host: u.Host, Path: u.Path}
		shown = clean.String()
		if redacted {
			shown += " (userinfo/query/fragment redacted)"
		}
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
