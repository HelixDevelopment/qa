// SPDX-FileCopyrightText: 2026 Milos Vasic
// SPDX-License-Identifier: Apache-2.0

package testbank

import (
	"strings"
	"testing"
)

// Review round 7, finding 4: declared dependencies order the run; ID is
// only the tie-break; cases in (or behind) a cycle are reported, not run.
func TestDependencyOrder_R8(t *testing.T) {
	order, cyclic := DependencyOrder(
		[]string{"a-check", "m-mid", "z-setup", "b-free"},
		map[string][]string{"a-check": {"z-setup"}, "m-mid": {"a-check"}},
	)
	if got := strings.Join(order, ","); got != "b-free,z-setup,a-check,m-mid" {
		t.Fatalf("order %s, want b-free,z-setup,a-check,m-mid", got)
	}
	if len(cyclic) != 0 {
		t.Fatalf("no cycle, got %v", cyclic)
	}
	order, cyclic = DependencyOrder(
		[]string{"x", "y", "w", "after"},
		map[string][]string{"x": {"y"}, "y": {"x"}, "after": {"x"}, "w": {"missing-elsewhere"}},
	)
	if strings.Join(order, ",") != "w" || strings.Join(cyclic, ",") != "after,x,y" {
		t.Fatalf("order %v cyclic %v — want [w] and [after x y]", order, cyclic)
	}
}

// Review round 7, finding 9: Arabic tatweel (kashida, U+0640) is a
// stretching glyph, not a letter of the word.
func TestVisionFields_R8_TatweelDropped(t *testing.T) {
	if a, b := VisionTokenKey("خطـــأ"), VisionTokenKey("خطأ"); a != b {
		t.Fatalf("tatweel kept: %q vs %q", a, b)
	}
}
