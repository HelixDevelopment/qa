// SPDX-FileCopyrightText: 2026 Milos Vasic
// SPDX-License-Identifier: Apache-2.0

package autonomous

import (
	"fmt"
	"time"

	"digital.vasic.helixqa/pkg/analysis"
	"digital.vasic.helixqa/pkg/memory"
)

// FindingsBridge connects the analysis pipeline to the memory store and
// optionally the issues directory on disk. It translates AnalysisFinding
// values produced by vision analysis into persisted memory.Finding records
// and Markdown issue files.
type FindingsBridge struct {
	store     *memory.Store
	issuesDir string
	sessionID string
}

// NewFindingsBridge creates a FindingsBridge backed by the given store.
// issuesDir is optional: when non-empty, each finding is also written to
// that directory as a Markdown file. sessionID is attached to every
// finding that the bridge persists.
func NewFindingsBridge(
	store *memory.Store,
	issuesDir string,
	sessionID string,
) *FindingsBridge {
	return &FindingsBridge{
		store:     store,
		issuesDir: issuesDir,
		sessionID: sessionID,
	}
}

// Process persists each AnalysisFinding to the store and, when an
// issuesDir is configured, writes a Markdown file per finding. It
// deduplicates findings by title — if an open finding with the same
// title already exists, the new occurrence is skipped. Related
// findings in the same category+platform are grouped by appending
// a "Related Issues" section to the Markdown.
//
// Returns the slice of HELIX-NNN identifiers that were created
// (not including duplicates that were skipped).
//
// Process is a no-op (returns nil, nil) when the store is nil or the
// findings slice is empty.
func (b *FindingsBridge) Process(
	findings []analysis.AnalysisFinding,
) ([]string, error) {
	if b.store == nil || len(findings) == 0 {
		return nil, nil
	}

	today := time.Now().UTC().Format("2006-01-02")
	ids := make([]string, 0, len(findings))

	// Track titles we've already created in THIS batch
	// to catch intra-batch duplicates too.
	seenTitles := make(map[string]bool)
	var skipped int

	for _, af := range findings {
		// Environment findings (e.g. a BLOCKED vision-gated case) say the
		// run could not judge; they are not app defects and are never
		// ticketed as one.
		if !af.Category.IsAppDefect() {
			continue
		}
		// ── Dedup: skip if same title already exists ──
		if seenTitles[af.Title] {
			skipped++
			continue
		}
		dup, err := b.store.FindDuplicateByTitle(af.Title)
		if err == nil && dup != nil {
			skipped++
			seenTitles[af.Title] = true
			continue
		}
		seenTitles[af.Title] = true

		id, err := b.store.NextFindingID()
		if err != nil {
			fmt.Printf(
				"  warning: could not generate "+
					"finding ID: %v\n", err,
			)
			continue
		}

		mf := memory.Finding{
			ID:                 id,
			SessionID:          b.sessionID,
			Severity:           string(af.Severity),
			Category:           string(af.Category),
			Title:              af.Title,
			Description:        af.Description,
			ReproSteps:         af.ReproSteps,
			EvidencePaths:      af.Evidence,
			Platform:           af.Platform,
			Screen:             af.Screen,
			Status:             "open",
			FoundDate:          today,
			AcceptanceCriteria: af.AcceptanceCriteria,
		}

		if err := b.store.CreateFinding(mf); err != nil {
			fmt.Printf(
				"  warning: could not create "+
					"finding %s: %v\n", id, err,
			)
			continue
		}

		// ── Group: append related issues section ─────
		if b.issuesDir != "" {
			related, _ := b.store.FindRelatedByCategory(
				string(af.Category), af.Platform,
			)
			if len(related) > 1 {
				mf.Description += "\n\n## Related Issues\n\n"
				for _, r := range related {
					if r.ID != id {
						mf.Description += fmt.Sprintf(
							"- %s: %s\n",
							r.ID, r.Title,
						)
					}
				}
			}
			if _, err := mf.WriteToDir(
				b.issuesDir,
			); err != nil {
				fmt.Printf(
					"  warning: could not write "+
						"finding %s to disk: %v\n",
					id, err,
				)
			}
		}

		ids = append(ids, id)
	}

	if skipped > 0 {
		fmt.Printf(
			"  [dedup] skipped %d duplicate findings\n",
			skipped,
		)
	}

	return ids, nil
}
