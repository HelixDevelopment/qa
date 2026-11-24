// SPDX-FileCopyrightText: 2026 Milos Vasic
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"digital.vasic.helixqa/pkg/llm"
)

func TestIsLoopbackURL(t *testing.T) {
	for _, u := range []string{"http://127.0.0.1:11434", "http://localhost:11500/v1", "http://[::1]:9/x"} {
		assert.True(t, isLoopbackURL(u), u)
	}
	for _, u := range []string{"https://api.openai.com", "http://192.168.1.5:11434", "http://127.0.0.1.evil.example:1", "", "::garbage"} {
		assert.False(t, isLoopbackURL(u), u)
	}
}

func TestSelectEnvProviders(t *testing.T) {
	cands := []llm.ProviderConfig{
		{Name: "openai", APIKey: "k"},
		{Name: "anthropic", APIKey: "k"},
		{Name: llm.ProviderOllama, BaseURL: "http://127.0.0.1:11434"},
		{Name: llm.ProviderLlmctl, BaseURL: "http://10.0.0.7:8082"}, // not loopback
	}
	got, skipped := selectEnvProviders(cands, false)
	require.Len(t, got, 1)
	assert.Equal(t, llm.ProviderOllama, got[0].Name)
	assert.Equal(t, []string{"anthropic", "llmctl", "openai"}, skipped)

	all, none := selectEnvProviders(cands, true)
	assert.Len(t, all, 4)
	assert.Empty(t, none)
}

// runAutonomousChild runs the real main() with a minimal, explicit environment
// (nothing inherited) and returns combined output and exit code.
func runAutonomousChild(t *testing.T, env map[string]string, args ...string) (string, int) {
	t.Helper()
	enc, _ := json.Marshal(args)
	cmd := exec.Command(os.Args[0], "-test.run=^$")
	cmd.Env = []string{"HELIXQA_CLI_EXIT_CHILD=1", "HELIXQA_CLI_EXIT_ARGS=" + string(enc)}
	for k, v := range env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	out, err := cmd.CombinedOutput()
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return string(out), ee.ExitCode()
	}
	require.NoError(t, err)
	return string(out), 0
}

func autonomousEnv(t *testing.T, extra map[string]string) (map[string]string, string) {
	t.Helper()
	home := t.TempDir()
	bin := filepath.Join(home, "bin")
	require.NoError(t, os.MkdirAll(bin, 0o755))
	// A "claude" CLI on PATH: it must NOT be registered without opt-in.
	require.NoError(t, os.WriteFile(filepath.Join(bin, "claude"), []byte("#!/bin/sh\nexit 0\n"), 0o755))
	env := map[string]string{
		"PATH": bin + ":/usr/bin:/bin", "HOME": home,
		"OPENAI_API_KEY": "sk-test-not-a-real-key-1", "ANTHROPIC_API_KEY": "sk-test-not-a-real-key-2",
		"GROQ_API_KEY": "sk-test-not-a-real-key-3",
		// a dead LOCAL endpoint: the session cannot plan and ends quickly
		"HELIX_VISION_ENDPOINT": "http://127.0.0.1:1/v1/chat/completions",
	}
	for k, v := range extra {
		env[k] = v
	}
	return env, home
}

func TestAutonomous_DefaultIsLocalOnly(t *testing.T) {
	env, home := autonomousEnv(t, nil)
	out, _ := runAutonomousChild(t, env, "autonomous", "-project", home, "-platforms", "web",
		"-timeout", "20s", "-curiosity=false", "-output", filepath.Join(home, "out"), "-env", filepath.Join(home, "none.env"))
	assert.Contains(t, out, "Hosted providers NOT registered")
	for _, n := range []string{"anthropic", "groq", "openai"} {
		assert.Contains(t, out, n)
		assert.NotContains(t, out, "registered provider: "+n)
	}
	assert.Contains(t, out, "found but NOT registered")
	assert.NotContains(t, out, "Bridged CLI:      claude (")
	assert.Contains(t, out, "registered provider: vision-endpoint")
	assert.Contains(t, out, "Host video:       off")
	for _, secret := range []string{"sk-test-not-a-real-key-1", "sk-test-not-a-real-key-2", "sk-test-not-a-real-key-3"} {
		assert.NotContains(t, out, secret)
	}
	assert.NotContains(t, out, "ffmpeg recording started")
}

func TestAutonomous_OptInRegistersHosted(t *testing.T) {
	env, home := autonomousEnv(t, nil)
	out, _ := runAutonomousChild(t, env, "autonomous", "-project", home, "-platforms", "web",
		"-timeout", "20s", "-curiosity=false", "-allow-cloud-providers", "-allow-bridged-cli",
		"-output", filepath.Join(home, "out"), "-env", filepath.Join(home, "none.env"))
	assert.Contains(t, out, "Hosted providers: ON (explicit opt-in)")
	assert.NotContains(t, out, "Hosted providers NOT registered")
	assert.Contains(t, out, "Bridged CLI:      claude (")
}

func TestAutonomous_NoLocalProviderNoOptIn_Exit2(t *testing.T) {
	env, home := autonomousEnv(t, map[string]string{"HELIX_VISION_ENDPOINT": ""})
	out, code := runAutonomousChild(t, env, "autonomous", "-project", home, "-platforms", "web",
		"-timeout", "20s", "-output", filepath.Join(home, "out"), "-env", filepath.Join(home, "none.env"))
	assert.Equal(t, 2, code)
	assert.Contains(t, out, "no LLM provider is registered")
	assert.True(t, strings.Contains(out, "-allow-cloud-providers"))
}
