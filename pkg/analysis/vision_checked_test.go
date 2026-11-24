// SPDX-FileCopyrightText: 2026 Milos Vasic
// SPDX-License-Identifier: Apache-2.0

package analysis

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"digital.vasic.helixqa/pkg/llm"
)

type replyProvider struct{ reply string }

func (p *replyProvider) Chat(context.Context, []llm.Message) (*llm.Response, error) {
	return &llm.Response{Content: p.reply}, nil
}
func (p *replyProvider) Vision(context.Context, []byte, string) (*llm.Response, error) {
	return &llm.Response{Content: p.reply}, nil
}
func (p *replyProvider) Name() string         { return "reply" }
func (p *replyProvider) SupportsVision() bool { return true }

// A frame counts as analysed only when the reply is a parsable findings array.
func TestAnalyzeScreenshotChecked_Parsed(t *testing.T) {
	cases := map[string]bool{
		"":                      false,
		"I cannot view images.": false,
		"[ {not json ]":         false,
		"[]":                    true, // the model judged the frame clean
		"```json\n[]\n```":      true,
		`[{"category":"visual","severity":"low","title":"t","description":"d"}]`: true,
		// A refusal or commentary that merely CONTAINS a literal "[]" is not a
		// findings array: it must not count the frame as analysed.
		"I cannot analyse this image, so I return [] as the findings.": false,
		"I'm unable to view images. []":                                false,
		"No issues found. []":                                          false,
		"See notes [1] and [2] below.":                                 false,
		"[] I cannot see the screenshot":                               false,
		`[{}]`:                                                         false, // an empty object is not a finding
		`[1, 2]`:                                                       false,
		// Prose around a NON-empty array of real findings is still usable.
		"Here are the findings:\n```json\n" +
			`[{"category":"visual","severity":"low","title":"t","description":"d"}]` + "\n```": true,
		"  \n[]\n  ": true,
	}
	for reply, want := range cases {
		v := NewVisionAnalyzer(&replyProvider{reply: reply})
		_, parsed, err := v.AnalyzeScreenshotChecked(context.Background(), []byte("x"), "s", "web")
		require.NoError(t, err)
		assert.Equal(t, want, parsed, "reply %q", reply)
	}
}
