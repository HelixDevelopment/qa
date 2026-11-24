// SPDX-FileCopyrightText: 2026 Milos Vasic
// SPDX-License-Identifier: Apache-2.0

package autonomous

import (
	"crypto/sha256"
	"os"
	"path/filepath"
	"testing"
)

// The frame a structured step is judged on must exist on disk with the same
// bytes (so its sha256 matches the report), and distinct frames must be counted.
func TestSaveStructuredFrameWritesTheJudgedBytes(t *testing.T) {
	dir := t.TempDir()
	a := []byte("\x89PNG frame A")
	b := []byte("\x89PNG frame B")
	pa, err := saveStructuredFrame(dir, "web", 1, a)
	if err != nil {
		t.Fatal(err)
	}
	pb, err := saveStructuredFrame(dir, "web", 2, b)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(pa)
	if sha256.Sum256(got) != sha256.Sum256(a) {
		t.Fatal("the saved frame differs from the judged bytes")
	}
	if filepath.Base(pa) != "structured-001-web.png" || filepath.Base(pb) != "structured-002-web.png" {
		t.Fatalf("unexpected names %s %s", pa, pb)
	}
	if n := distinctFrameCount([]string{pa, pb}); n != 2 {
		t.Fatalf("distinct frames = %d, want 2", n)
	}
	// the same bytes under two names are ONE distinct frame
	pc, _ := saveStructuredFrame(dir, "web", 3, a)
	if n := distinctFrameCount([]string{pa, pc}); n != 1 {
		t.Fatalf("identical frames counted as %d distinct", n)
	}
}

func TestSaveStructuredFrameRefusesEmpty(t *testing.T) {
	if _, err := saveStructuredFrame(t.TempDir(), "web", 1, nil); err == nil {
		t.Fatal("an empty frame was written")
	}
}
