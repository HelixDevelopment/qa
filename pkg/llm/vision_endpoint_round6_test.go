// SPDX-FileCopyrightText: 2026 Milos Vasic
// SPDX-License-Identifier: Apache-2.0

package llm

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const r6Key = "sk-review5-AbCdEfGhIjKlMnOpQrStUvWxYz012" // 40 bytes

func pctEveryByte(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		fmt.Fprintf(&b, "%%%02X", s[i])
	}
	return b.String()
}

func jsonUEscapes(s string) string {
	var b strings.Builder
	for _, r := range s {
		fmt.Fprintf(&b, `\u%04x`, r)
	}
	return b.String()
}

// leaksKey reports whether 12 consecutive characters of the key survive in
// any decoding the reviewer applied (raw text, base64 / hex decoding of
// every token, JSON \u unescaping, percent unescaping).
func leaksKey(out, key string) bool {
	views := []string{out}
	for _, tok := range strings.FieldsFunc(out, func(r rune) bool { return strings.ContainsRune(` "{}:,|`, r) }) {
		for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.URLEncoding, base64.RawStdEncoding, base64.RawURLEncoding} {
			if d, err := enc.DecodeString(tok); err == nil {
				views = append(views, string(d))
			}
		}
		if d, err := hex.DecodeString(tok); err == nil {
			views = append(views, string(d))
		}
	}
	for _, v := range views {
		for i := 0; i+12 <= len(key); i++ {
			if strings.Contains(v, key[i:i+12]) {
				return true
			}
		}
	}
	return strings.Contains(strings.ToLower(out), strings.ToLower(pctEveryByte(key)[:36])) ||
		strings.Contains(strings.ToLower(out), strings.ToLower(jsonUEscapes(key)[:36]))
}

// Review round 5, finding 9: the redaction covers the echo forms a server
// actually produces besides the bare key: base64 of "Bearer <key>" (an
// echoed Authorization header), the key inside a larger base64 blob at any
// byte alignment, every byte percent-encoded, hex, and JSON \u escapes.
func TestProviders_R6_RedactsEchoForms(t *testing.T) {
	bodies := map[string]string{
		"base64 Bearer header":       base64.StdEncoding.EncodeToString([]byte("Bearer " + r6Key)),
		"base64 blob, offset 0":      base64.StdEncoding.EncodeToString([]byte("abc" + r6Key + "xyz")),
		"base64 blob, offset 1":      base64.StdEncoding.EncodeToString([]byte("a" + r6Key + "xyz")),
		"base64 blob, offset 2":      base64.StdEncoding.EncodeToString([]byte("ab" + r6Key + "xyz")),
		"base64url blob, offset 1":   base64.RawURLEncoding.EncodeToString([]byte("{\"k\":\"" + r6Key + "\"}")),
		"percent-encoded every byte": pctEveryByte(r6Key),
		"lower-case percent":         strings.ToLower(pctEveryByte(r6Key)),
		"hex":                        hex.EncodeToString([]byte(r6Key)),
		"upper hex":                  strings.ToUpper(hex.EncodeToString([]byte(r6Key))),
		"json \\u escapes":           jsonUEscapes(r6Key),
	}
	for name, echo := range bodies {
		if !leaksKey(`{"echo":"`+echo+`"}`, r6Key) {
			t.Fatalf("%s: the probe does not see the unredacted key — fixture broken", name)
		}
		out := errorBodyText([]byte(`{"echo":"`+echo+`"}`), r6Key)
		if leaksKey(out, r6Key) {
			t.Errorf("%s: key recoverable from %q", name, out)
		}
		if !strings.Contains(out, "[REDACTED]") {
			t.Errorf("%s: redaction not visible: %q", name, out)
		}
	}
	// Ordinary text near the key's alphabet is not mangled.
	if out := errorBodyText([]byte(`{"error":"invalid model gpt-4o"}`), r6Key); out != `{"error":"invalid model gpt-4o"}` {
		t.Errorf("unrelated body altered: %q", out)
	}
}

// Finding 9: astica's non-success "status" field is server text and is
// redacted (and capped) before it reaches an error.
func TestProviders_R6_AsticaStatusRedacted(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{"status":"error: token `+r6Key+` rejected `+strings.Repeat("z", 2000)+`"}`)
	}))
	defer srv.Close()
	p := NewAsticaProvider(ProviderConfig{Name: "astica", APIKey: r6Key, BaseURL: srv.URL})
	_, err := p.Vision(context.Background(), []byte("x"), "q")
	if err == nil {
		t.Fatal("non-success status was not an error")
	}
	if leaksKey(err.Error(), r6Key) || !strings.Contains(err.Error(), "[REDACTED]") {
		t.Fatalf("astica status not redacted: %q", err.Error())
	}
	if len(err.Error()) > 1024 {
		t.Fatalf("astica status not capped (%d bytes)", len(err.Error()))
	}
}
