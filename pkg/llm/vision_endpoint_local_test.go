// SPDX-FileCopyrightText: 2026 Milos Vasic
// SPDX-License-Identifier: Apache-2.0

package llm

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestIsLocalEndpointURL(t *testing.T) {
	local := []string{
		"http://127.0.0.1:8082/v1", "http://localhost:8082", "https://LOCALHOST/v1",
		"http://[::1]:8082/v1", "http://10.1.2.3:1", "http://172.16.0.9:1", "http://172.31.255.1:1",
		"http://192.168.1.50:1", "http://169.254.10.10:1", "http://[fd12:3456::1]:1", "http://[fe80::1]:1",
		"http://gpu-box.local:8082/v1", "http://llm.localhost:1",
	}
	for _, u := range local {
		assert.True(t, IsLocalEndpointURL(u), u)
	}
	hosted := []string{
		"", "::garbage", "ftp://127.0.0.1/x", "http://", "/v1/chat",
		"https://api.openai.com/v1", "http://203.0.113.9:1", "http://172.32.0.1:1", "http://8.8.8.8",
		"http://127.0.0.1.evil.example:1/v1", // loopback-looking name, parsed host is evil.example
		"http://127.0.0.1:1@evil.example/v1", // user info smuggling a loopback look-alike
		"http://localhost@evil.example/v1",   // same, with a name
		"http://user:pw@127.0.0.1:8082/v1",   // user info is refused even on a loopback host
		"http://evil.example/127.0.0.1",      // loopback in the path
		"http://evil.example/?h=127.0.0.1",   // loopback in the query
		"http://localhost.evil.example/v1",   // name that STARTS with localhost
		"http://[::ffff:8.8.8.8]:1",          // v4-mapped public address
		"http://2130706433/v1",               // integer form: not parsed as an IP, refused (safe)
		"http://0.0.0.0:1",                   // unspecified, not a named private range
		"http://evil.example:80#@127.0.0.1",  // fragment trick
	}
	for _, u := range hosted {
		assert.False(t, IsLocalEndpointURL(u), u)
	}
}
