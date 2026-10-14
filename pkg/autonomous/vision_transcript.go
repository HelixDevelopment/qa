// SPDX-FileCopyrightText: 2026 Milos Vasic
// SPDX-License-Identifier: Apache-2.0

package autonomous

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

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

// maxBlindTextLen bounds one transcription element (runes).
const maxBlindTextLen = 200

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
	if !utf8.ValidString(reply) {
		return nil, "", fmt.Errorf("%w: reply is not valid UTF-8", errBlindReply)
	}
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
		if utf8.RuneCountInString(v) > maxBlindTextLen {
			return nil, fmt.Errorf("%w: a \"texts\" element is longer than %d characters", errBlindReply, maxBlindTextLen)
		}
		if strings.IndexFunc(v, unicode.IsControl) >= 0 {
			return nil, fmt.Errorf("%w: a \"texts\" element contains a line break or control character (one element per UI element)", errBlindReply)
		}
		if strings.IndexFunc(v, isLineOrParagraphSeparator) >= 0 {
			return nil, fmt.Errorf("%w: a \"texts\" element contains a line / paragraph separator U+2028 / U+2029 (one element per UI element)", errBlindReply)
		}
		out = append(out, v)
	}
	return out, nil
}

// isLineOrParagraphSeparator reports U+2028 / U+2029 (Unicode Zl / Zp):
// line breaks that unicode.IsControl does not cover.
func isLineOrParagraphSeparator(r rune) bool {
	return unicode.In(r, unicode.Zl, unicode.Zp)
}

// modelStatementPattern matches a normalised (testbank.VisionTokenKey)
// text that is the MODEL talking — a refusal or a remark about the image —
// rather than text written on the screen. It is a phrase heuristic; how it
// is used is structural (see transcriptUsable).
const modelStatementPattern = `(i m sorry|i am sorry|sorry i|sorry but|i apologi[sz]e|unfortunately i|as an ai|as a language model|` +
	`i (cannot|can t|can not|could not|couldn t|am unable to|m unable to|am not able to|m not able to|do not|don t|won t be able to|will not be able to) ` +
	`(see|read|view|determine|identify|make out|help|assist|process|analy[sz]e|interpret|transcribe|tell|access the image)|` +
	`(unable to|cannot|can t) (read|see|view|determine|identify|make out|process|analy[sz]e|interpret|transcribe) ` +
	`((the|this|any) )?(images?|screenshots?|text|screens?|pictures?)|` +
	`(the|this) (image|screenshot|picture) (is|shows|appears|contains|does))( |$)`

// modelStatementRe matches a whole element that STARTS as a model statement.
var modelStatementRe = regexp.MustCompile(`^` + modelStatementPattern)

// transcriptUsable decides — by STRUCTURE — whether a strict-parsed
// transcription can carry a claim of ABSENCE. It cannot when it holds no
// screen text (empty, or "no text" filler only), or when EVERY remaining
// element is the model talking (a refusal or a remark about the image): such
// a reading did not transcribe the screen. A model-statement-looking element
// NEXT TO other screen text is treated as screen text (real app copy such as
// "Unable to process image" or "As an AI developer tool …"). The caller
// checks forbidden-token PRESENCE before this rule.
func transcriptUsable(texts []string) error {
	screenText, statements := 0, 0
	var first string
	for _, t := range texts {
		k := testbank.VisionTokenKey(t)
		if k == "" || testbank.IsVisionFiller(k) {
			continue
		}
		screenText++
		if modelStatementRe.MatchString(k) {
			if statements == 0 {
				first = t
			}
			statements++
		}
	}
	if screenText == 0 {
		return errors.New("transcription holds no screen text (empty or filler only) — whether a token is present or absent cannot be determined")
	}
	if statements == screenText {
		return fmt.Errorf("every transcription element is a refusal / remark by the model (e.g. %q), not screen text — the reading is unusable", first)
	}
	return nil
}

// containedOnly returns the (normalised) tokens that appear as whole words
// INSIDE some texts element although they equal none.
func containedOnly(texts, tokens []string) []string {
	var out []string
	for _, k := range tokens {
		if k == "" {
			continue
		}
		if _, f := matchTokensInTexts(texts, nil, []string{k}, false); len(f) > 0 {
			out = append(out, k)
		}
	}
	return out
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
func matchTokensInTexts(texts []string, recognize, forbid []string, exact bool) (missing, forbidden []string) {
	keys := make([]string, 0, len(texts))
	for _, t := range texts {
		k := testbank.VisionTokenKey(t)
		if k == "" || testbank.IsVisionFiller(k) {
			continue
		}
		keys = append(keys, " "+k+" ")
	}
	has := func(tok string) bool {
		sub := needsSubstringMatch(tok)
		for _, k := range keys {
			if sub && strings.Contains(k, tok) {
				// Scripts written without spaces between words: a token
				// inside a longer run of text IS in it.
				return true
			}
			if strings.Contains(k, " "+tok+" ") {
				return true
			}
		}
		return false
	}
	equals := func(tok string) bool {
		for _, k := range keys {
			if k == " "+tok+" " {
				return true
			}
		}
		return false
	}
	for _, tok := range recognize {
		k := testbank.VisionTokenKey(tok)
		found := has(k)
		if exact {
			// exact-element mode: the token must BE one whole element.
			found = equals(k)
		}
		if k == "" || !found {
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

// noSpaceScripts are scripts written without spaces between words, where
// whole-word matching would never find a token inside a longer string.
var noSpaceScripts = []*unicode.RangeTable{unicode.Han, unicode.Hiragana, unicode.Katakana,
	unicode.Thai, unicode.Lao, unicode.Khmer, unicode.Myanmar}

// needsSubstringMatch reports a (normalised) token that contains a letter of
// a no-space script: it is matched as a substring, not as whole words.
func needsSubstringMatch(tok string) bool {
	for _, r := range tok {
		if unicode.In(r, noSpaceScripts...) {
			return true
		}
	}
	return false
}

// allModelStatements reports a reading whose every non-filler element is the
// model talking (see transcriptUsable); false when it holds none.
func allModelStatements(texts []string) bool {
	n := 0
	for _, t := range texts {
		k := testbank.VisionTokenKey(t)
		if k == "" || testbank.IsVisionFiller(k) {
			continue
		}
		if !modelStatementRe.MatchString(k) {
			return false
		}
		n++
	}
	return n > 0
}

// forbiddenPresent returns the forbid tokens present in the reading. In a
// reading where every element is the model talking, a forbidden word inside
// a refusal sentence ("I cannot read the Error text") is not on the screen:
// there only an element EQUAL to the token counts.
func forbiddenPresent(texts, forbid []string) []string {
	if !allModelStatements(texts) {
		_, f := matchTokensInTexts(texts, nil, forbid, true)
		return f
	}
	var out []string
	for _, tok := range forbid {
		if missing, _ := matchTokensInTexts(texts, []string{tok}, nil, true); len(missing) == 0 {
			out = append(out, testbank.VisionTokenKey(tok))
		}
	}
	return out
}
