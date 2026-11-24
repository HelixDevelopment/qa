// SPDX-FileCopyrightText: 2026 Milos Vasic
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"net"
	"net/url"
	"sort"
	"strings"

	"digital.vasic.helixqa/pkg/llm"
)

// Provider-registration policy for `helixqa autonomous`.
//
// A session sends screenshots and page text to whatever providers it
// registers. Registering every provider whose API key happens to be in the
// shell environment, and every claude / opencode CLI found on PATH, meant a
// plain `helixqa autonomous` in an operator's normal shell could route frames
// of the application under test to ~20 hosted services without anyone asking.
// The policy is therefore explicit:
//
//   - LOCAL providers (a loopback Ollama / llmctl URL, and the operator's own
//     HELIX_VISION_ENDPOINT) are always eligible.
//   - HOSTED providers (any API-key provider, and a non-loopback Ollama /
//     llmctl URL) and bridged CLIs (claude, qwen-coder, opencode) are
//     registered ONLY when the operator opts in: -allow-cloud-providers /
//     HELIX_ALLOW_CLOUD_PROVIDERS=1, and -allow-bridged-cli /
//     HELIX_ALLOW_BRIDGED_CLI=1.
//
// Only provider NAMES are ever printed here, never a key or a URL.

// envEnabled reports whether a boolean opt-in variable is set to 1/true/yes.
func envEnabled(getenv func(string) string, name string) bool {
	switch strings.ToLower(strings.TrimSpace(getenv(name))) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// isLoopbackURL reports whether raw names a loopback host.
func isLoopbackURL(raw string) bool {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return false
	}
	h := u.Hostname()
	if strings.EqualFold(h, "localhost") {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}

// isLocalProvider reports whether a provider config talks only to this host.
func isLocalProvider(cfg llm.ProviderConfig) bool {
	switch cfg.Name {
	case llm.ProviderOllama, llm.ProviderLlmctl:
		return isLoopbackURL(cfg.BaseURL)
	}
	return false
}

// selectEnvProviders applies the policy to the env-registered providers and
// returns the allowed ones plus the NAMES of those skipped (sorted).
func selectEnvProviders(
	cands []llm.ProviderConfig, allowCloud bool,
) (allowed []llm.ProviderConfig, skipped []string) {
	for _, c := range cands {
		if allowCloud || isLocalProvider(c) {
			allowed = append(allowed, c)
			continue
		}
		skipped = append(skipped, c.Name)
	}
	sort.Strings(skipped)
	return allowed, skipped
}

// dataValidationPolicy is the resolved opt-in for the API data-validation
// step. That step LOGS IN to the application under test, so it is off unless
// asked for and it never carries a built-in credential: only the NAMES of the
// environment variables holding the login are configured, and the values are
// read at the moment of use and never printed.
type dataValidationPolicy struct {
	On       bool
	UserEnv  string
	PassEnv  string
	envValue func(string) string
}

func resolveDataValidation(
	flagOn bool, userEnv, passEnv string, getenv func(string) string,
) dataValidationPolicy {
	if userEnv == "" {
		userEnv = strings.TrimSpace(getenv("HELIX_DATA_VALIDATION_USER_ENV"))
	}
	if passEnv == "" {
		passEnv = strings.TrimSpace(getenv("HELIX_DATA_VALIDATION_PASS_ENV"))
	}
	return dataValidationPolicy{
		On:       flagOn || envEnabled(getenv, "HELIX_DATA_VALIDATION"),
		UserEnv:  userEnv,
		PassEnv:  passEnv,
		envValue: getenv,
	}
}

// describe renders the banner value: never a credential, only names and
// whether the named variables are set.
func (d dataValidationPolicy) describe() string {
	if !d.On {
		return "off (no login or API probe is sent to the target)"
	}
	set := func(n string) string {
		if n == "" {
			return "(name not configured)"
		}
		if d.envValue(n) == "" {
			return n + " (UNSET)"
		}
		return n + " (set)"
	}
	return "ON; user env " + set(d.UserEnv) + ", password env " + set(d.PassEnv)
}
