// SPDX-FileCopyrightText: 2026 Milos Vasic
// SPDX-License-Identifier: Apache-2.0

package llm

import (
	"encoding/base64"
	"io"
	"net/url"
	"regexp"
	"strings"
)

// readCappedBody is the ONE place every provider reads a reply body: it
// reads at most openaiMaxResponseBytes+1 bytes and reports whether the
// reply was larger than the cap (tooBig). It never buffers an unbounded
// body.
func readCappedBody(r io.Reader) (body []byte, tooBig bool, err error) {
	body, err = io.ReadAll(io.LimitReader(r, openaiMaxResponseBytes+1))
	if err != nil {
		return nil, false, err
	}
	return body, len(body) > openaiMaxResponseBytes, nil
}

// minRedactableSecret is the shortest secret that is redacted; shorter
// values would match ordinary text.
const minRedactableSecret = 8

// errorBodyText turns an error reply into text that is safe to put into an
// error (which reaches logs, BLOCKED reasons, findings and reports): it
// redacts every echo form of each secret in the FULL body FIRST — raw, in
// any letter case, URL query- and path-escaped, and base64 (standard / URL,
// padded / raw) — and only THEN truncates to openaiErrorBodySnippet bytes,
// so a secret straddling the cut cannot leak as a prefix.
func errorBodyText(body []byte, secrets ...string) string {
	text := string(body)
	for _, s := range secrets {
		text = redactSecret(text, s)
	}
	if len(text) > openaiErrorBodySnippet {
		text = strings.ToValidUTF8(text[:openaiErrorBodySnippet], "") + "…(truncated)"
	}
	return text
}

func redactSecret(text, secret string) string {
	if len(secret) < minRedactableSecret {
		return text
	}
	raw := []byte(secret)
	forms := []string{
		secret,
		url.QueryEscape(secret),
		url.PathEscape(secret),
		base64.StdEncoding.EncodeToString(raw),
		base64.URLEncoding.EncodeToString(raw),
		base64.RawStdEncoding.EncodeToString(raw),
		base64.RawURLEncoding.EncodeToString(raw),
	}
	seen := map[string]bool{}
	for _, f := range forms {
		if f == "" || seen[f] {
			continue
		}
		seen[f] = true
		text = regexp.MustCompile(`(?i)`+regexp.QuoteMeta(f)).ReplaceAllString(text, "[REDACTED]")
	}
	return text
}
