// SPDX-FileCopyrightText: 2026 Milos Vasic
// SPDX-License-Identifier: Apache-2.0

package autonomous

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"digital.vasic.helixqa/pkg/testbank"
)

// blindDescribePrompt asks for an expectation-BLIND reading of the frame as
// a JSON object. It carries neither the step's expectation nor its
// recognize/forbid tokens, so the model cannot parrot them back.
const blindDescribePrompt = "Read this screenshot. Reply with ONLY a JSON object and nothing else, " +
	"exactly of this shape:\n" +
	`{"texts": ["..."], "description": "..."}` + "\n" +
	`"texts": every piece of text that is literally written on the screen, copied verbatim, ` +
	"one array element per UI element. Put nothing in \"texts\" that is not written on the " +
	"screen: no notes, no explanations, no statements about what is missing. If no text is " +
	"visible, use an empty array.\n" +
	`"description": one paragraph describing the layout and state of the screen.` + "\n" +
	"Report only what is visible; do not infer or guess."

// blindTranscriptSchema is the JSON schema of that reply, sent as
// response_format to the local endpoint (grammar-constrained there).
var blindTranscriptSchema = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"texts":       map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
		"description": map[string]any{"type": "string"},
	},
	"required":             []any{"texts", "description"},
	"additionalProperties": false,
}

// maxBlindTexts bounds the number of transcription elements accepted.
const maxBlindTexts = 2000

var errBlindReply = errors.New("blind reply is not exactly one {\"texts\": [string...], \"description\": string} JSON object")

// parseBlindReply strict-parses a blind reply. The reply must be exactly one
// JSON object with exactly the keys "texts" (array of strings) and
// "description" (string) — case-sensitive, each once, nothing before or
// after it. The only tolerated wrapper is a single markdown code fence that
// is the whole reply ("```json\n…\n```" or "```\n…\n```"): text outside a
// fence is impossible because the fence must start and end the trimmed
// reply, and the content inside must still be the strict object, so the
// fence cannot carry prose into "texts".
func parseBlindReply(reply string) (texts []string, description string, err error) {
	s := strings.TrimSpace(reply)
	if strings.HasPrefix(s, "```") {
		inner, ok := stripSingleFence(s)
		if !ok {
			return nil, "", fmt.Errorf("%w: malformed or multiple code fences", errBlindReply)
		}
		s = strings.TrimSpace(inner)
	}
	if !strings.HasPrefix(s, "{") {
		return nil, "", fmt.Errorf("%w: does not start with '{'", errBlindReply)
	}
	dec := json.NewDecoder(strings.NewReader(s))
	if tok, terr := dec.Token(); terr != nil || tok != json.Delim('{') {
		return nil, "", fmt.Errorf("%w: not an object", errBlindReply)
	}
	seen := map[string]bool{}
	for dec.More() {
		kt, terr := dec.Token()
		key, isStr := kt.(string)
		if terr != nil || !isStr {
			return nil, "", fmt.Errorf("%w: bad key", errBlindReply)
		}
		if key != "texts" && key != "description" {
			return nil, "", fmt.Errorf("%w: unexpected key %q", errBlindReply, key)
		}
		if seen[key] {
			return nil, "", fmt.Errorf("%w: duplicate key %q", errBlindReply, key)
		}
		seen[key] = true
		var raw json.RawMessage
		if derr := dec.Decode(&raw); derr != nil {
			return nil, "", fmt.Errorf("%w: %v", errBlindReply, derr)
		}
		switch key {
		case "texts":
			texts, err = strictStringArray(raw)
			if err != nil {
				return nil, "", err
			}
		case "description":
			if len(raw) == 0 || raw[0] != '"' || json.Unmarshal(raw, &description) != nil {
				return nil, "", fmt.Errorf("%w: \"description\" is not a string", errBlindReply)
			}
		}
	}
	if tok, terr := dec.Token(); terr != nil || tok != json.Delim('}') {
		return nil, "", fmt.Errorf("%w: object not closed", errBlindReply)
	}
	if _, terr := dec.Token(); terr != io.EOF {
		return nil, "", fmt.Errorf("%w: trailing content after the object", errBlindReply)
	}
	if !seen["texts"] || !seen["description"] {
		return nil, "", fmt.Errorf("%w: missing \"texts\" or \"description\"", errBlindReply)
	}
	return texts, description, nil
}

func strictStringArray(raw json.RawMessage) ([]string, error) {
	if len(raw) == 0 || raw[0] != '[' {
		return nil, fmt.Errorf("%w: \"texts\" is not an array", errBlindReply)
	}
	var elems []json.RawMessage
	if err := json.Unmarshal(raw, &elems); err != nil {
		return nil, fmt.Errorf("%w: %v", errBlindReply, err)
	}
	if len(elems) > maxBlindTexts {
		return nil, fmt.Errorf("%w: more than %d \"texts\" elements", errBlindReply, maxBlindTexts)
	}
	out := make([]string, 0, len(elems))
	for _, e := range elems {
		var v string
		if len(e) == 0 || e[0] != '"' || json.Unmarshal(e, &v) != nil {
			return nil, fmt.Errorf("%w: a \"texts\" element is not a string", errBlindReply)
		}
		out = append(out, v)
	}
	return out, nil
}

// stripSingleFence returns the content of a reply that is exactly one
// fenced block: opening "```" or "```json" line, content, closing "```".
func stripSingleFence(s string) (string, bool) {
	nl := strings.IndexByte(s, '\n')
	if nl < 0 {
		return "", false
	}
	opener := strings.TrimSpace(s[:nl])
	if opener != "```" && !strings.EqualFold(opener, "```json") {
		return "", false
	}
	rest := s[nl+1:]
	if !strings.HasSuffix(rest, "```") {
		return "", false
	}
	inner := strings.TrimSuffix(rest, "```")
	if strings.Contains(inner, "```") {
		return "", false
	}
	return inner, true
}

// matchTokensInTexts matches recognize/forbid tokens against individual
// `texts` elements only: a token matches when some single element contains
// it as whole normalised words (testbank.VisionTokenKey). Elements are never
// joined (a phrase cannot be assembled across elements), filler elements
// ("(none)", "N/A", "No text visible") are dropped, and nothing outside
// `texts` — the description, any prose — is ever consulted.
func matchTokensInTexts(texts []string, recognize, forbid []string) (missing, forbidden []string) {
	keys := make([]string, 0, len(texts))
	for _, t := range texts {
		k := testbank.VisionTokenKey(t)
		if k == "" || testbank.IsVisionFiller(k) {
			continue
		}
		keys = append(keys, " "+k+" ")
	}
	has := func(tok string) bool {
		for _, k := range keys {
			if strings.Contains(k, " "+tok+" ") {
				return true
			}
		}
		return false
	}
	for _, tok := range recognize {
		k := testbank.VisionTokenKey(tok)
		if k == "" || !has(k) {
			missing = append(missing, k)
		}
	}
	for _, tok := range forbid {
		k := testbank.VisionTokenKey(tok)
		if k != "" && has(k) {
			forbidden = append(forbidden, k)
		}
	}
	return missing, forbidden
}
