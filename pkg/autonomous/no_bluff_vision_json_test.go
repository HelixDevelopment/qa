// SPDX-FileCopyrightText: 2026 Milos Vasic
// SPDX-License-Identifier: Apache-2.0

// no_bluff_vision_json_test.go pins the round-4 redesign of recognize /
// forbid: the vision model returns a STRICT JSON object
// {"texts": [...], "description": "..."} and tokens are matched ONLY against
// whole words of individual `texts` elements — never against the
// description or any prose. Every reply that is not exactly one such object
// is unusable (next provider / BLOCKED). There are no heading heuristics.

package autonomous

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"digital.vasic.helixqa/pkg/llm"
	"digital.vasic.helixqa/pkg/testbank"
)

func recStep(rec, forb []string) testbank.TestStep {
	return testbank.TestStep{Name: "s", Action: "keypress: X", Expected: "x", Recognize: rec, Forbid: forb}
}

// The reviewer's 16 bypass forms (round 3) in the old free-text shape, plus
// structural variants: none is a JSON object, so all are BLOCKED.
func TestNoBluffVision_BlindReplyMustBeStrictJSON(t *testing.T) {
	notJSON := map[string]string{
		"A01 inline Note:":         "TEXT:\nSettings\nNote: the Login button is not visible\nDESCRIPTION: x",
		"A02 bold note":            "TEXT:\nSettings\n**Note:** no Login link is shown\nDESCRIPTION: x",
		"A03 parenthetical":        "TEXT:\nSettings\n(The Login button is not shown)\nDESCRIPTION: x",
		"A05 bullet heading":       "TEXT:\nSettings\n- Notes:\nthe Login button is missing\nDESCRIPTION: x",
		"A06 numbered heading":     "TEXT:\nSettings\n1. Observations:\nno Login button\nDESCRIPTION: x",
		"A07 Step 1:":              "TEXT:\nSettings\nStep 1:\nLogin is absent\nDESCRIPTION: x",
		"A08 Model's notes:":       "TEXT:\nSettings\nModel's notes:\nLogin not visible\nDESCRIPTION: x",
		"A09 Cyrillic heading":     "TEXT:\nSettings\nПримечание:\nLogin not visible\nDESCRIPTION: x",
		"A10 reworded filler":      "TEXT:\nThere is no visible text on the Login screen.\nDESCRIPTION: blank",
		"A11 >41-char heading":     "TEXT:\nSettings\nAdditional observations about the current screen state:\nLogin missing\nDESCRIPTION: x",
		"A12 > Note":               "TEXT:\nSettings\n> Note\nLogin not shown\nDESCRIPTION: x",
		"A13 *Notes*":              "TEXT:\nSettings\n*Notes*\nLogin not shown\nDESCRIPTION: x",
		"A14 <b>":                  "TEXT:\nSettings\n<b>Notes</b>\nLogin not shown\nDESCRIPTION: x",
		"A15 emoji heading":        "TEXT:\nSettings\n📝 Notes:\nLogin not shown\nDESCRIPTION: x",
		"A16 setext heading":       "TEXT:\nSettings\nNotes\n=====\nLogin not shown\nDESCRIPTION: x",
		"C2 (no search bar)":       "TEXT:\nLogin\n(no search bar visible)\nDESCRIPTION: x",
		"prose before object":      "Here you go: {\"texts\":[\"Login\"],\"description\":\"x\"}",
		"prose after object":       "{\"texts\":[\"Login\"],\"description\":\"x\"} Note: Login is not shown",
		"two objects":              "{\"texts\":[\"Login\"],\"description\":\"x\"}{\"texts\":[],\"description\":\"y\"}",
		"extra key":                "{\"texts\":[\"Login\"],\"description\":\"x\",\"notes\":\"Login missing\"}",
		"missing description":      "{\"texts\":[\"Login\"]}",
		"missing texts":            "{\"description\":\"Login\"}",
		"duplicate texts key":      "{\"texts\":[],\"texts\":[\"Login\"],\"description\":\"x\"}",
		"case-variant key":         "{\"Texts\":[\"Login\"],\"description\":\"x\"}",
		"texts is a string":        "{\"texts\":\"Login\",\"description\":\"x\"}",
		"texts has a number":       "{\"texts\":[\"Login\",1],\"description\":\"x\"}",
		"texts has null":           "{\"texts\":[\"Login\",null],\"description\":\"x\"}",
		"texts has an object":      "{\"texts\":[{\"t\":\"Login\"}],\"description\":\"x\"}",
		"description is a number":  "{\"texts\":[\"Login\"],\"description\":7}",
		"top-level array":          "[\"Login\"]",
		"BOM before object":        "\ufeff{\"texts\":[\"Login\"],\"description\":\"x\"}",
		"fence with prose outside": "Sure!\n```json\n{\"texts\":[\"Login\"],\"description\":\"x\"}\n```",
		"two fences":               "```json\n{\"texts\":[\"Login\"],\"description\":\"x\"}\n```\n```\nmore\n```",
		"empty":                    "   ",
	}
	for name, reply := range notJSON {
		ste, _ := newSTE(&fakeVision{blind: reply}, noisePNG(t, 1), noisePNG(t, 2))
		r := run(ste, recStep([]string{"Login"}, nil))
		if r.Passed || !r.Blocked {
			t.Errorf("%s: passed=%v blocked=%v actual=%q — a non-JSON / malformed reply must be BLOCKED", name, r.Passed, r.Blocked, r.Actual)
		}
	}

	good := map[string]string{
		"plain":        `{"texts":["Login","Password"],"description":"a login form"}`,
		"key order":    `{"description":"a login form","texts":["Password","Login"]}`,
		"single fence": "```json\n{\"texts\":[\"Login\"],\"description\":\"x\"}\n```",
		"bare fence":   "```\n{\"texts\":[\"Login\"],\"description\":\"x\"}\n```",
	}
	for name, reply := range good {
		ste, _ := newSTE(&fakeVision{blind: reply}, noisePNG(t, 1), noisePNG(t, 2))
		if r := run(ste, recStep([]string{"Login"}, nil)); !r.Passed {
			t.Errorf("%s: well-formed reply did not pass: %q", name, r.Actual)
		}
	}
}

// Prose — the description — never satisfies recognize nor trips forbid.
// The reviewer's commentary forms placed where a JSON reply puts prose.
func TestNoBluffVision_DescriptionProseNeverMatches(t *testing.T) {
	cases := []struct {
		name, reply string
		rec, forb   []string
		pass        bool
	}{
		{"negated recognize in description", `{"texts":["Settings"],"description":"The Login button is not shown"}`, []string{"Login"}, nil, false},
		{"(no search bar visible) as prose", `{"texts":["Settings"],"description":"(no search bar visible)"}`, []string{"search bar"}, nil, false},
		{"(No error messages visible) as prose", `{"texts":["Settings"],"description":"(No error messages visible)"}`, []string{"Settings"}, []string{"Error"}, true},
		{"forbid in texts fails", `{"texts":["Settings","Error 500"],"description":"banner"}`, []string{"Settings"}, []string{"Error"}, false},
		{"empty texts", `{"texts":[],"description":"Login screen"}`, []string{"Login"}, nil, false},
		{"filler-only texts", `{"texts":["(none)","N/A"],"description":"blank"}`, []string{"Settings"}, []string{"none"}, false},
	}
	for _, c := range cases {
		ste, _ := newSTE(&fakeVision{blind: c.reply}, noisePNG(t, 1), noisePNG(t, 2))
		r := run(ste, recStep(c.rec, c.forb))
		if r.Passed != c.pass || r.Blocked {
			t.Errorf("%s: passed=%v blocked=%v (want pass=%v, not blocked) actual=%q", c.name, r.Passed, r.Blocked, c.pass, r.Actual)
		}
	}
}

// Element-level whole-word policy, including the documented residual: a
// commentary string the model puts INSIDE `texts` is treated as screen text
// (it contains the token as whole words). That is a model lying inside its
// transcription and is not detectable here; golden screenshots + pHash are
// the second oracle.
func TestNoBluffVision_TextsElementWholeWordPolicy(t *testing.T) {
	cases := []struct {
		texts         []string
		rec, forb     []string
		wantMissing   int
		wantForbidden int
	}{
		{[]string{"(Loading…)"}, []string{"Load"}, nil, 1, 0},
		{[]string{"Login button"}, []string{"Login"}, nil, 0, 0},
		{[]string{"Logins"}, []string{"Login"}, nil, 1, 0},
		{[]string{"LOGIN"}, []string{"login"}, nil, 0, 0},
		{[]string{"Log", "In"}, []string{"Log In"}, nil, 1, 0},
		{[]string{"Sign-In!"}, []string{"sign in"}, nil, 0, 0},
		{[]string{"N/A", "(none)"}, []string{"n a"}, []string{"none"}, 1, 0},
		{[]string{"Terrorist-free", "ErrorsList"}, nil, []string{"Error"}, 0, 0},
		// residual, documented: commentary inside texts is matched
		{[]string{"(no search bar visible)"}, []string{"search bar"}, nil, 0, 0},
		{[]string{"(No error messages visible)"}, nil, []string{"error"}, 0, 1},
	}
	for i, c := range cases {
		missing, forbidden := matchTokensInTexts(c.texts, c.rec, c.forb)
		if len(missing) != c.wantMissing || len(forbidden) != c.wantForbidden {
			t.Errorf("case %d %v rec=%v forb=%v: missing=%v forbidden=%v (want %d/%d)",
				i, c.texts, c.rec, c.forb, missing, forbidden, c.wantMissing, c.wantForbidden)
		}
	}
	// Conservative side of the same residual: commentary inside texts that
	// contains a forbidden word as a whole word trips forbid (a FAIL, never
	// a pass).
}

// The blind prompt carries no token and no expectation, asks for the JSON
// object, and the local endpoint gets a JSON-schema response_format.
func TestNoBluffVision_BlindRequestIsStructured(t *testing.T) {
	var mu sync.Mutex
	var bodies []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		m := map[string]any{}
		_ = json.Unmarshal(raw, &m)
		mu.Lock()
		bodies = append(bodies, m)
		mu.Unlock()
		reply, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"message": map[string]any{
			"role": "assistant", "content": `{"texts":["Login"],"description":"a form"}`}}}})
		_, _ = w.Write(reply)
	}))
	defer srv.Close()
	cfg, ok := llm.VisionEndpointConfigFromEnv(func(k string) string {
		if k == llm.EnvVisionEndpoint {
			return srv.URL + "/v1"
		}
		return ""
	})
	if !ok {
		t.Fatal("no endpoint config")
	}
	ste, _ := newSTE(llm.NewOpenAIProvider(cfg), noisePNG(t, 1), noisePNG(t, 2))
	r := run(ste, recStep([]string{"Login"}, []string{"Error"}))
	if !r.Passed {
		t.Fatalf("structured endpoint reply did not pass: %q", r.Actual)
	}
	if len(bodies) != 1 {
		t.Fatalf("want 1 request, got %d", len(bodies))
	}
	rf, _ := bodies[0]["response_format"].(map[string]any)
	if rf == nil || rf["type"] != "json_object" || rf["schema"] == nil {
		t.Fatalf("endpoint request lacks the JSON response_format: %v", bodies[0]["response_format"])
	}
	schema, _ := json.Marshal(rf["schema"])
	for _, want := range []string{`"texts"`, `"description"`, `"additionalProperties":false`} {
		if !strings.Contains(string(schema), want) {
			t.Fatalf("schema lacks %s: %s", want, schema)
		}
	}
	prompt, _ := json.Marshal(bodies[0]["messages"])
	for _, leak := range []string{"Login", "Error"} {
		if strings.Contains(string(prompt), leak) {
			t.Fatalf("blind prompt leaks token %q", leak)
		}
	}
	if !strings.Contains(string(prompt), "texts") || !strings.Contains(string(prompt), "JSON") {
		t.Fatalf("blind prompt does not ask for the JSON object: %s", prompt)
	}
}
