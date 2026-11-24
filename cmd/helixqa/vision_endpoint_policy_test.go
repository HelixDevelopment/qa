// SPDX-FileCopyrightText: 2026 Milos Vasic
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
)

// HELIX_VISION_ENDPOINT receives every captured frame. The banner says
// "Hosted providers: off" by default, so a non-private endpoint must be
// refused unless -allow-cloud-providers is given. (Before the fix it was
// registered and used silently.)
func TestAutonomous_VisionEndpointHosted_RefusedWithoutOptIn(t *testing.T) {
	for _, ep := range []string{
		"https://vision.example.com/v1/chat/completions",
		"http://203.0.113.9:8082/v1",         // public (TEST-NET-3) address
		"http://127.0.0.1.evil.example:1/v1", // loopback-looking name
		"http://127.0.0.1:1@evil.example/v1", // userinfo trick
	} {
		env, home := autonomousEnv(t, map[string]string{"HELIX_VISION_ENDPOINT": ep})
		out, code := runAutonomousChild(t, env, "autonomous", "-project", home, "-platforms", "web",
			"-timeout", "20s", "-curiosity=false", "-output", filepath.Join(home, "out"), "-env", filepath.Join(home, "none.env"))
		assert.NotEqual(t, 0, code, ep)
		assert.NotContains(t, out, "registered provider: vision-endpoint", ep)
		assert.Contains(t, out, "HELIX_VISION_ENDPOINT", ep)
		assert.NotContains(t, out, "evil.example", "the URL is never echoed: "+ep)
		assert.NotContains(t, out, "203.0.113.9", "the URL is never echoed: "+ep)
	}
}

func TestAutonomous_VisionEndpointHosted_AllowedWithOptInAndLabelled(t *testing.T) {
	env, home := autonomousEnv(t, map[string]string{"HELIX_VISION_ENDPOINT": "http://203.0.113.9:1/v1"})
	out, _ := runAutonomousChild(t, env, "autonomous", "-project", home, "-platforms", "web",
		"-timeout", "20s", "-curiosity=false", "-allow-cloud-providers",
		"-output", filepath.Join(home, "out"), "-env", filepath.Join(home, "none.env"))
	assert.Contains(t, out, "Hosted providers: ON (explicit opt-in)")
	assert.Contains(t, out, "NOT a loopback/private host")
}

func TestAutonomous_VisionEndpointPrivate_AllowedByDefault(t *testing.T) {
	for _, ep := range []string{"http://127.0.0.1:1/v1", "http://localhost:1/v1", "http://192.168.1.50:1/v1", "http://[::1]:1/v1"} {
		env, home := autonomousEnv(t, map[string]string{"HELIX_VISION_ENDPOINT": ep})
		out, _ := runAutonomousChild(t, env, "autonomous", "-project", home, "-platforms", "web",
			"-timeout", "20s", "-curiosity=false", "-output", filepath.Join(home, "out"), "-env", filepath.Join(home, "none.env"))
		assert.Contains(t, out, "registered provider: vision-endpoint", ep)
	}
}
