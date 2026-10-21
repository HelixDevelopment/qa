// SPDX-FileCopyrightText: 2026 Milos Vasic
// SPDX-License-Identifier: Apache-2.0

package llm

import (
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"net/url"
	"regexp"
	"strings"
	"unicode/utf16"
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
// redacts the echo forms of each secret listed at redactSecret in the FULL
// body FIRST, and only THEN truncates to openaiErrorBodySnippet bytes, so a
// secret straddling the cut cannot leak as a prefix. Forms not listed there
// (mixed or partial escaping, other encodings) are NOT detected.
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

// redactSecret replaces, case-insensitively, these echo forms of secret:
//
//   - raw; URL query- and path-escaped; every byte percent-encoded;
//   - hex; JSON \uXXXX escapes of every character;
//   - base64 (standard / URL alphabet, padded / raw) of the secret alone;
//   - base64 of the secret INSIDE a larger encoded blob (an echoed
//     "Authorization: Bearer <key>" header, a JSON document): for each of
//     the three byte alignments the run of base64 characters that depends
//     only on secret bytes is redacted (at most 2 secret bytes at each edge
//     remain in their partial characters).
//
// NOT covered: mixed or partial escaping, a partial echo of the key (a
// prefix of it), base64 wrapped into lines (MIME 76 columns) or of another
// text encoding (UTF-16), HTML entities, base32 / compressed / reversed
// forms. Secrets shorter than minRedactableSecret are left alone (they
// would match ordinary text).
func redactSecret(text, secret string) string {
	if len(secret) < minRedactableSecret {
		return text
	}
	raw := []byte(secret)
	forms := []string{
		secret,
		url.QueryEscape(secret),
		url.PathEscape(secret),
		percentEncodeAll(raw),
		hex.EncodeToString(raw),
		jsonUnicodeEscapes(secret),
		base64.StdEncoding.EncodeToString(raw),
		base64.URLEncoding.EncodeToString(raw),
		base64.RawStdEncoding.EncodeToString(raw),
		base64.RawURLEncoding.EncodeToString(raw),
	}
	forms = append(forms, embeddedBase64Cores(raw)...)
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

// embeddedBase64Cores returns, for each byte alignment of secret inside a
// larger base64-encoded blob (0, 1 or 2 bytes before it, modulo 3), the
// base64 characters (standard and URL alphabet) that encode only secret
// bytes: the secret bytes from the first 3-byte group boundary, cut to whole
// groups. Those characters appear verbatim in any encoding of a blob that
// carries the secret at that alignment.
func embeddedBase64Cores(raw []byte) []string {
	var out []string
	for lead := 0; lead < 3; lead++ {
		skip := (3 - lead) % 3
		if len(raw) <= skip {
			continue
		}
		core := raw[skip:]
		core = core[:len(core)/3*3]
		if len(core) < 6 {
			continue
		}
		out = append(out, base64.StdEncoding.EncodeToString(core), base64.URLEncoding.EncodeToString(core))
	}
	return out
}

// percentEncodeAll encodes every byte as %XX.
func percentEncodeAll(raw []byte) string {
	var b strings.Builder
	for _, c := range raw {
		fmt.Fprintf(&b, "%%%02X", c)
	}
	return b.String()
}

// jsonUnicodeEscapes writes every character as a JSON \uXXXX escape
// (UTF-16 surrogate pairs above the BMP).
func jsonUnicodeEscapes(s string) string {
	var b strings.Builder
	for _, u := range utf16.Encode([]rune(s)) {
		fmt.Fprintf(&b, `\u%04x`, u)
	}
	return b.String()
}
