// SPDX-FileCopyrightText: 2026 Milos Vasic
// SPDX-License-Identifier: Apache-2.0

package testbank

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// jsonBankFile mirrors BankFile but accepts "challenges" as an
// alternate key for test_cases (used by comprehensive JSON banks).
type jsonBankFile struct {
	Version     string         `json:"version"`
	Name        string         `json:"name"`
	Description string         `json:"description"`
	TestCases   []TestCase     `json:"test_cases"`
	Challenges  []TestCase     `json:"challenges"`
	Metadata    map[string]any `json:"metadata,omitempty"`
}

// knownStepKeys is the set of YAML/JSON keys a TestStep defines, derived
// from the struct's tags by reflection so it cannot drift from the schema.
func knownStepKeys() map[string]bool {
	known := map[string]bool{}
	t := reflect.TypeOf(TestStep{})
	for i := 0; i < t.NumField(); i++ {
		for _, tag := range []string{"yaml", "json"} {
			if name := strings.Split(t.Field(i).Tag.Get(tag), ",")[0]; name != "" && name != "-" {
				known[name] = true
			}
		}
	}
	return known
}

// rejectUnknownExpectKeys fails a bank whose steps carry an assertion key
// (anything starting "expect") that this build does not define.
//
// WHY (operator decision 2026-10-08): yaml.Unmarshal and json.Unmarshal drop
// unknown keys silently, so a step asserting something the binary cannot
// check loaded fine and PASSED vacuously — a stale helixqa binary printed
// [ok] for WK-SEARCH-010's "pid ABSENT" step while checking nothing.
//
// WHY ONLY expect* (measured, not assumed): all 145 banks under banks/ load
// with this rule, whereas a global KnownFields(true) would reject 43 of them
// (fix_reference, executor_agent, requires, ...). Those keys are metadata,
// not assertions; a dropped assertion is the only silent-pass hazard.
//
// "expected" (the prose description) is a real field, so it is in the known
// set and is never confused with an assertion key.
func rejectUnknownExpectKeys(data []byte) error {
	var root yaml.Node
	// JSON is a YAML subset, so one node walk serves both formats. A syntax
	// error was already reported by the typed decode that ran before this.
	if yerr := yaml.Unmarshal(data, &root); yerr != nil || len(root.Content) == 0 {
		return nil
	}
	known := knownStepKeys()
	var bad []string
	var walk func(n *yaml.Node)
	walk = func(n *yaml.Node) {
		if n.Kind == yaml.MappingNode {
			for i := 0; i+1 < len(n.Content); i += 2 {
				k, v := n.Content[i], n.Content[i+1]
				if k.Value == "steps" && v.Kind == yaml.SequenceNode {
					for _, st := range v.Content {
						for j := 0; st.Kind == yaml.MappingNode && j+1 < len(st.Content); j += 2 {
							sk := st.Content[j]
							if strings.HasPrefix(sk.Value, "expect") && !known[sk.Value] {
								bad = append(bad, fmt.Sprintf("line %d: unknown assertion key %q", sk.Line, sk.Value))
							}
						}
					}
				}
				walk(v)
			}
			return
		}
		for _, c := range n.Content {
			walk(c)
		}
	}
	walk(&root)
	if len(bad) > 0 {
		return fmt.Errorf("%s (rebuild helixqa, or fix the key in the bank)", strings.Join(bad, "; "))
	}
	return nil
}

// LoadFile loads a test bank file (YAML or JSON) and returns the
// parsed BankFile.
func LoadFile(path string) (*BankFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read bank file %s: %w", path, err)
	}

	ext := strings.ToLower(filepath.Ext(path))
	var bf BankFile

	if ext == ".json" {
		var jbf jsonBankFile
		if err := json.Unmarshal(data, &jbf); err != nil {
			return nil, fmt.Errorf("parse bank file %s: %w", path, err)
		}
		bf.Version = jbf.Version
		bf.Name = jbf.Name
		bf.Description = jbf.Description
		bf.Metadata = jbf.Metadata
		bf.TestCases = jbf.TestCases
		if len(bf.TestCases) == 0 && len(jbf.Challenges) > 0 {
			bf.TestCases = jbf.Challenges
		}
	} else {
		if err := yaml.Unmarshal(data, &bf); err != nil {
			return nil, fmt.Errorf("parse bank file %s: %w", path, err)
		}
	}

	// An assertion key this build does not define must be a load error,
	// never a silent no-op (see rejectUnknownExpectKeys). Runs for YAML and
	// JSON banks alike, after the typed decode has reported real syntax errors.
	if err := rejectUnknownExpectKeys(data); err != nil {
		return nil, fmt.Errorf("parse bank file %s: %w", path, err)
	}

	// Validate all test cases + guard against intra-bank duplicate
	// ids. P9 fix (docs/nexus/remaining-work.md): a duplicate id
	// inside a single bank used to silently overwrite the prior
	// entry when loaded into maps downstream; refuse at load time.
	seen := map[string]int{}
	for i := range bf.TestCases {
		if msg := bf.TestCases[i].IsValid(); msg != "" {
			return nil, fmt.Errorf(
				"bank file %s: test case %d: %s",
				path, i, msg,
			)
		}
		id := bf.TestCases[i].ID
		if id == "" {
			continue
		}
		if prev, dup := seen[id]; dup {
			return nil, fmt.Errorf(
				"bank file %s: duplicate test case id %q at indices %d and %d",
				path, id, prev, i,
			)
		}
		seen[id] = i
	}

	return &bf, nil
}

// hasTopLevelKey reports whether the YAML/JSON document at path has the given
// key at its top level. A read or parse failure answers false: this is only
// used to enrich a message, never to decide anything.
func hasTopLevelKey(path, key string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil || len(root.Content) == 0 {
		return false
	}
	m := root.Content[0]
	for i := 0; m.Kind == yaml.MappingNode && i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return true
		}
	}
	return false
}

// DeclinedFile records a bank file LoadDir found on disk but did NOT
// load, and why. HXC-305: a directory scan must never discard a file
// without saying so — "no message, no count" is exactly what let a
// silently-preferred legacy YAML twin hide the JSON twin's ASSERTED
// http: checks for as long as it did.
//
// Direction matters here and an earlier revision of this comment had
// it backwards ("hide 125 unasserted checks"). HXC-243 is what MADE
// those checks asserted — it assigned an explicit expect_status to
// each one — so what the preferred YAML twin hid was assertion that
// already existed, not the absence of it. Measured, not recalled:
// pkg/autonomous/hxc243_falsifiability_checks_generated_test.go
// enumerates 119 such steps across 7 bank files, every row in a
// .json twin. The "125" the old comment carried is not that number
// and has no measured basis.
type DeclinedFile struct {
	// Path is the declined file, as filepath.Join(dir, entry.Name())
	// — i.e. it carries the SAME dir argument the caller passed to
	// LoadDir/LoadDirVerbose as a prefix (which may itself be
	// relative or absolute); it is not relative to dir.
	Path string
	// Reason is a human-readable explanation, always non-empty. For a
	// twin pair it states the executable-step comparison AND, since
	// merging is how case ids are preserved (see LoadDirVerbose),
	// names exactly which case ids (if any) were merged in verbatim
	// from this file, which (if any) were preferred from this file
	// per-case, and which (if any) were excluded because they
	// genuinely collide with an unrelated bank elsewhere.
	Reason string
	// ExcludedIDs names every case id that was NOT loaded because it
	// genuinely collides with an id an unrelated bank claimed first
	// (HXC-305 B3) — the ONLY legitimate way for a case present in a
	// bank file to be absent from the loaded catalog. Reason also
	// names them in prose, but this field is the structural form:
	// callers and tests MUST match on it rather than substring-scrape
	// Reason, because bank ids in this corpus are not suffix-free
	// (e.g. "AICHAT-SEC-001" ends with "SEC-001"), so a prose scrape
	// can credit an unrelated id as excluded and mask a genuine loss.
	// Empty in the overwhelmingly common case.
	ExcludedIDs []string
}

// LoadDirResult is the return value of LoadDirVerbose: the banks that
// were actually loaded, plus every file LoadDir found on disk but
// declined to load, with a reason (HXC-305 non-silent-decline
// invariant).
type LoadDirResult struct {
	Banks    []*BankFile
	Declined []DeclinedFile
}

// twinLogger receives one line per file LoadDir declines to load, so
// the decline is always observable from a directory-scan run and
// never silent. Overridable in tests; production default writes to
// os.Stderr via the standard log package.
//
// VOLUME, and why it is deliberately NOT summarised here (round 16):
// declines are the STEADY STATE of this corpus, not an exception —
// a healthy `list --banks banks/` prints 50 of these lines on every
// run, because 50 of the 195 bank files are the declined side of a
// twin pair. There is a real argument that a steady-state 50-line
// list trains operators to ignore the very channel this item exists
// to create, and that the healthy case should summarise ("50
// declined, use --verbose") while the full list is reserved for the
// abort path where it is diagnostic. That change is NOT made here,
// and round 18 corrected the reason given. This block used to cite
// §11.4.122, arguing a summary would REDUCE what an operator
// currently sees. It would not: at 0634b1b8 there is no declined
// surface at all — twinLogger, LoadDirVerbose, DeclinedFile and
// LoadDirResult have ZERO occurrences at HEAD, so `list` prints ZERO
// declined lines and this change CREATES the 50-line surface rather
// than preserving it. §11.4.122 governs removing already-shipped
// capability and has no purchase on the shape of a surface being
// introduced. The reason to ship the full list is the guarantee
// itself: HXC-305's operative statement is that a scan must never
// discard a file without saying so, and a count-only summary reports
// how many files were dropped but never WHICH — so it would not
// discharge that statement as written. The 50 lines ARE the fix.
// The trade-off above is real, but orthogonal to the silent-loss
// defect this change fixes — the harm measured in rounds 14 and 16
// was always on the ABORT path, where the list went to ZERO.
// Summarising the healthy path would need its own RED baseline, its
// own guard and its own evidence. Raised here so the trade-off is
// tracked rather than rediscovered.
var twinLogger = log.New(os.Stderr, "", log.LstdFlags)

// LoadDir loads all test bank files from a directory.
// It scans for .yaml, .yml, and .json files (non-recursive).
//
// A directory's declined files (see LoadDirVerbose) are logged via
// twinLogger so this call is never silent about what it skipped;
// callers that need the declined list structurally (to report it
// themselves, or to assert on it in a test) should call
// LoadDirVerbose directly instead.
func LoadDir(dir string) ([]*BankFile, error) {
	res, err := LoadDirVerbose(dir)
	// Logged BEFORE the error check, so an aborted scan is no more
	// silent than a successful one. Returning first discarded the
	// declines on exactly the runs that need them: round 14 measured
	// a directory with a declined twin and a failing id floor logging
	// ZERO bytes, while the same directory with a satisfied floor
	// logged the decline — a doc comment promising this call "is never
	// silent about what it skipped" describing code that was silent
	// precisely when it mattered. Manager.LoadDir applies the same
	// discipline one layer up. Both orderings are NEW in this change —
	// at 0634b1b8 Manager.LoadDir did not log at all (it returned this
	// function's error directly and manager.go never named twinLogger),
	// so neither layer had "always" done this and the ordering here is
	// not inherited from an existing convention.
	if res != nil {
		for _, d := range res.Declined {
			twinLogger.Printf("testbank: LoadDir(%s): declined %s: %s",
				dir, d.Path, d.Reason)
		}
	}
	if err != nil {
		return nil, err
	}
	return res.Banks, nil
}

// LoadDirVerbose loads all test bank files from a directory (as
// LoadDir does) and additionally reports every file found on disk but
// declined, with a reason — HXC-305.
//
// A bank commonly ships as a YAML/JSON twin pair sharing one base
// name: a legacy prose YAML (steps described in English the executor
// cannot run) and a JSON form produced by a prose-to-http conversion
// pass (steps carrying an explicit `http:`/`adb_shell:`/... action the
// executor CAN run). WHICH twin's content is preferred for a case
// shared by both is decided by CONTENT, not by format, and — HXC-305
// finding F1 — PER CASE, not per bank: for each shared case id the
// side whose OWN body has STRICTLY MORE genuinely-executable steps
// (ParseAction() != ActionTypeDescription) wins that case; ties keep
// the primary's body. A whole-bank comparison is NOT sound for this,
// because a conversion pass can improve a bank overall while making
// an individual case WORSE — de-converting an already-executable step
// back into prose. That is not hypothetical: in this project's corpus
// atmosphere.json wins the bank-level comparison (49/395 executable
// steps vs the YAML twin's 24/566) yet carries a strictly worse body
// for PA-001, PA-005 and VR-015, each of which has one executable
// `adb_shell:` step in the YAML twin and none in the JSON twin. Under
// a bank-granularity preference those three cases stop executing —
// the HXC-305 defect class (a silently-preferred twin hiding
// executable steps) reintroduced in the opposite direction. Per-case
// preference keeps all 41 cases the JSON twin genuinely improves IN
// THAT BANK (684 across the whole corpus, of 1897 shared ids) AND
// those 3 it would have demoted. The 684 figure is corpus-wide and
// an earlier revision of this comment attached it to atmosphere
// alone, which is off by 16x.
//
// The bank-level comparison still runs, but ONLY to choose the
// "primary" — the twin whose case ORDER and whose bank-level
// Name/Version/Metadata the merged result carries, and whose body
// wins a per-case tie. (That attribution is cosmetic downstream:
// Manager keys cases by case ID, not by bank identity.)
//
// The preferred twin is NOT assumed to be a full superset of the
// other's case ids — a prose-to-http conversion pass can convert only
// SOME of a bank's cases, leaving the rest behind as unconverted
// prose that exists ONLY in the other twin (HXC-305 finding B1: one
// real bank in this project's corpus converts 129 of its YAML twin's
// 179 cases; the JSON form wins on executable-step count, but the
// other 50 case ids exist only in the YAML form and MUST NOT vanish
// just because the comparison picked JSON). So the twins are MERGED:
// the primary's cases are kept in order (each body resolved per-case
// as above), and any case id present in the OTHER twin but absent
// from the primary is appended, sourced from that other twin. The
// resulting case-id set is always the UNION of both twins', never a
// subset of either — the executable-step comparison decides which
// VERSION of a case shared by both twins is kept, never whether a
// case exists at all.
//
// PARSE FAILURES are deliberately asymmetric, preserving exactly the
// guarantee each form has always carried: the non-JSON twin has
// always been REQUIRED to parse, so if it fails, the whole directory
// scan still hard-fails. A JSON twin, by contrast, was historically
// never parsed at all when a non-JSON sibling existed, so a JSON
// parse failure does NOT newly hard-fail a directory that always
// loaded before; the non-JSON twin loads (the pre-existing source of
// truth) and the failure is reported in Declined rather than
// swallowed.
//
// That asymmetry is a SYNTACTIC guarantee and nothing more, and it
// must not be over-read (HXC-305 F2, round-4 correction). An earlier
// revision of this comment justified the hard-fail as preventing "a
// corrupt/truncated YAML bank resolving quietly to its JSON
// sibling". It does not, and cannot: the branch fires only on input
// that is not valid YAML, which is a shape truncation essentially
// never produces. YAML block sequences are self-terminating, so
// cutting a bank file short at a case boundary leaves a perfectly
// valid document that simply declares fewer cases, and an empty file
// parses to a valid zero-case bank. Measured on this corpus against
// the real binary: truncating banks/atmosphere.yaml at a case
// boundary exits 0 and drops the catalog 3046 -> 3006 cases with no
// error and no mention; emptying it exits 0 and drops it to 2996,
// losing exactly the 50 ids unique to that twin, while the decline
// line it prints positively asserts "none lost". Only a mid-token
// corruption trips the parse check — and then it aborts the whole
// 195-file scan, 3046 -> 0. JSON does not share the property (it is
// bracket-delimited, so truncation is always a parse error), which is
// why the check looks effective when exercised against JSON and is
// inert on the form that actually relies on it.
//
// The reachable class is therefore closed by CONTENT invariants
// rather than by the parse check:
//
//   - ZERO-CASE (always on, no configuration). A file that parses but
//     declares no test cases at all is never a legitimate bank in
//     this corpus (measured: 0 of 195 files). For the non-JSON twin
//     of a pair it is the exact corruption shape the parse check
//     misses, so it hard-fails the scan — the guarantee the old
//     comment claimed, now actually delivered. Everywhere else (a
//     zero-case JSON twin, a standalone file, a stray non-bank YAML
//     that happens to sit in the directory) it is REPORTED in
//     Declined instead, so it can never again be indistinguishable
//     from a file that was never read, without newly hard-failing a
//     directory that has always loaded.
//
//   - ID FLOOR (opt-in per directory). Zero-case cannot catch a
//     PARTIAL truncation, which still declares plenty of cases. That
//     needs an expected id set, and the only non-brittle source for
//     one is a record of what the directory is known to hold. A
//     directory MAY therefore carry a bankIDFloorFile listing the
//     case ids it is expected to yield; every id in it must end up
//     either loaded or explicitly excluded, and a missing one is a
//     hard error naming it. It is a FLOOR, not an equality check, so
//     adding cases — the common edit — never trips it and never
//     needs a regeneration; only REMOVING a case does, which is the
//     event that should be visible in review rather than silent. A
//     directory without the file is not floor-checked at all, so an
//     arbitrary --banks path keeps loading exactly as before.
//
// A twin pair's (possibly merged) case ids can also genuinely collide
// with an UNRELATED bank elsewhere in the directory — a real,
// separate content-authoring defect, not a twin relationship
// (HXC-305 finding B3). That must not abort the entire directory
// scan: the colliding case id(s) are excluded from the twin's loaded
// bank and reported by name, while every other case in the pair — and
// every other bank in the directory — still loads. This differs from
// the cross-bank duplicate-id check for a genuinely standalone
// (non-twin) file below, which still hard-fails the whole scan, since
// a lone duplicate id between two otherwise-unrelated files has no
// merge or partial-exclusion available to it.
//
// Every declined file (twin loser, or one that failed to parse) is
// always reported in Declined, together with exactly what happened to
// its content: merged in verbatim, excluded for colliding elsewhere,
// or neither (every one of its ids was already present in the twin
// that was kept).
//
// "Always" includes an ERROR return: the result is non-nil and its
// Declined list is complete as of the abort on every failure path
// that could have declined anything. Banks, by contrast, is only the
// directory's catalog when err is nil — on an error return it holds
// just what loaded before the abort.
func LoadDirVerbose(dir string) (*LoadDirResult, error) {
	return loadDirVerbose(dir, true)
}

// loadDirVerbose is LoadDirVerbose with the id-floor check made
// optional.
//
// enforceFloor is false for exactly one caller —
// RegenerateBankIDFloor, which must be able to read the live catalog
// of a directory whose floor is CURRENTLY FAILING, because that is
// the only state a regeneration is ever performed in. Routing the
// regeneration through the same enforced entry point made the
// documented remedy unusable in the sole situation it existed for
// (HXC-305 round-6 BLOCKING A): the scan aborted, the CLI wrote the
// error to stderr and exited 1 before emitting any stdout, and the
// shell pipeline the header prescribed silently took `sort`'s exit
// status instead — so `mv` overwrote the floor with a header-only,
// zero-id file at exit 0 and disarmed the check entirely.
//
// Every OTHER error still aborts: an unparseable bank, a cross-bank
// duplicate id, an unreadable directory. Only the floor comparison
// is skipped, so a regeneration can never launder a genuinely broken
// directory into a fresh floor.
func loadDirVerbose(dir string, enforceFloor bool) (*LoadDirResult, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read bank dir %s: %w", dir, err)
	}

	// Index every candidate file by its extension-stripped base name
	// so twin pairs (and only twin pairs) can be resolved before any
	// file is loaded.
	type candidate struct {
		name string // entry.Name(), e.g. "admin-operations.json"
		ext  string
	}
	byBase := map[string][]candidate{}
	var order []string // base names in first-seen (directory) order
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		ext := strings.ToLower(filepath.Ext(entry.Name()))
		if ext != ".yaml" && ext != ".yml" && ext != ".json" {
			continue
		}
		base := strings.TrimSuffix(entry.Name(), filepath.Ext(entry.Name()))
		if _, seen := byBase[base]; !seen {
			order = append(order, base)
		}
		byBase[base] = append(byBase[base], candidate{name: entry.Name(), ext: ext})
	}

	var banks []*BankFile
	var declined []DeclinedFile

	// abort is how every failure AFTER this point returns, so the
	// declined list can never be discarded on an abort path. (The one
	// earlier failure — os.ReadDir above — precedes this declaration
	// and can have declined nothing, so it correctly returns a bare
	// nil.)
	//
	// CHEAP TRIPWIRE — NOT a completeness check, though this block
	// called itself one until round 18. Scoped to this closure's
	// body, it must print 0:
	//
	//	awk '/abort := func\(err error\)/,/^}/' pkg/testbank/loader.go |
	//		grep -cE '^[[:space:]]*return nil,'
	//
	// The SCOPE is the round-18 repair. This block used to prescribe
	// the same pattern over the WHOLE file, where it prints 15 —
	// every legitimate `return nil,` in the other functions — while
	// the sentence introducing it claimed the command "must report
	// nothing" and named no baseline to diff against. A maintainer
	// running it verbatim saw 15 hits and learned to ignore the
	// check: precisely the failure the last paragraph here warns
	// about, at 15x, committed inside the sentence that warns about
	// it. The awk range spans this declaration through the end of
	// loadDirVerbose and nothing else. If that extraction ever comes
	// back EMPTY (pipe it to `wc -l` — 334 lines today; 0 means the
	// anchor stopped matching), then the 0 is vacuous: repair the
	// anchor, and never read an empty range as clean.
	//
	// WHAT IT CANNOT CATCH (measured round 18; every mutant below
	// compiled and ran). It catches the careless line-start form —
	// planting `return nil, nonJSONErr` at the site below takes it
	// from 0 to 1. It is BLIND to two discards that compile and lose
	// exactly as much: `return &LoadDirResult{Banks: banks},
	// nonJSONErr`, which drops Declined through a struct literal, and
	// a one-line `if nonJSONErr != nil { return nil, nonJSONErr }`,
	// which is not at line start. Both score 0 here, and both are
	// killed by TestHXC305_DeclinedListSurvivesEveryAbort's
	// unparseable_non_json_twin case, which asserts the declined twin
	// is PRESENT in res.Declined and in the list Manager hands the
	// CLI — content, not merely a non-nil result. The guarantee for
	// this defect class therefore lives in that table; this grep is
	// only the cheap first line that catches the obvious regression
	// before the tests run. Labelling it a completeness check was an
	// invitation to trust it in the table's place.
	//
	// Match the RETURN SHAPE, never a variable name, and keep the
	// pattern anchored. Round 16 found the eighth discarding abort
	// (`return nil, nonJSONErr`, the unparseable non-JSON twin) still
	// live because the check prescribed here USED to read
	// "return nil, err": it named a variable, so it was structurally
	// incapable of seeing a site whose error happened to be called
	// something else, and it reported a clean zero while the defect it
	// existed to catch sat 183 lines below it. The anchor matters for
	// the same reason — an unanchored pattern matches this very
	// sentence, so the check would report a hit that is only its own
	// prose and a reader would learn to ignore it.
	//
	// HXC-305 round 6 fixed exactly one abort (the id-floor failure)
	// to carry the list and left the rest returning a bare nil. Round
	// 14 measured what that cost on a fixture with a declined twin AND
	// a cross-bank duplicate id: the CLI printed ZERO declined lines,
	// while the same fixture minus the duplicate printed one. The
	// operator saw "duplicate test case id" and nothing about which
	// twin had already been dropped or what content went with it —
	// the precise silent loss this item exists to eliminate — HXC-305's
	// operative statement is that a directory scan must never discard a
	// file without saying so, and a discarded declined list is exactly
	// that. (The standard is the ITEM's, deliberately: LoadDirVerbose's
	// own doc comment promises the same thing, but that comment was
	// written in this very change, so citing it as the authority the
	// code violates would be circular — the doc cannot constrain the
	// code it shipped with.) Round 16
	// measured the identical loss on the twin-parse-failure path,
	// against the real banks/ corpus: corrupting one bank took `list`
	// from 50 declined lines to 0. With this fix the same corruption
	// keeps 49 — every decline except the corrupted pair's own, which
	// now aborts before it can be declined. (Lines, not bytes: each
	// line embeds the bank's full path, so the byte volume of the same
	// 50 declines ranges from ~14 KB to ~42 KB purely with the length
	// of the --banks argument, and is not a stable measure of anything.)
	//
	// Banks is populated for symmetry with the success return, but on
	// an error return it holds only what loaded BEFORE the abort and
	// is therefore NOT the directory's catalog — callers must treat it
	// as meaningless whenever err != nil (every in-tree PRODUCTION
	// caller does: LoadDir and RegenerateBankIDFloor return nil banks
	// on error, and Manager.LoadDirVerbose reads only Declined. Tests
	// in this package read Banks on an error return deliberately, to
	// assert exactly this partial-catalog behaviour).
	abort := func(err error) (*LoadDirResult, error) {
		return &LoadDirResult{Banks: banks, Declined: declined}, err
	}

	// P9: track every id across the directory so cross-bank
	// collisions are caught at load time. The bank-registry layer
	// downstream also derives from these ids and silently drops
	// collisions; blocking here is the permanent fix.
	xref := map[string]string{}
	// twinIDs marks every id claimed by a twin-pair resolution
	// (whether merged, or the sole side that parsed). HXC-305 B3: a
	// LATER collision against one of these — from another twin OR
	// from a genuinely standalone file processed afterwards — must be
	// tolerated (exclude just that case, never abort the whole scan),
	// REGARDLESS of which side of the pair happened to be inserted
	// first in directory order. A collision against a PLAIN
	// (non-twin) id is NOT tolerated: that is the pre-existing P9
	// guarantee for two genuinely unrelated files declaring the same
	// id, unchanged by this fix.
	twinIDs := map[string]bool{}

	addBank := func(path string, bf *BankFile, isTwin bool) ([]excludedCase, bool, error) {
		return insertBank(dir, path, bf, isTwin, xref, twinIDs, &banks)
	}

	for _, base := range order {
		cands := byBase[base]

		// Find the (at most one, per extension) json / yaml / yml
		// candidate for this base name. Three-way {.yaml,.yml,.json}
		// groups are not observed in practice; when they do occur,
		// only the .yaml sibling (preferring .yaml over .yml, matching
		// the historical precedence) is compared against .json — any
		// remaining non-JSON sibling loads unconditionally, same as
		// before this change.
		var jsonName, yamlName, ymlName string
		var extras []string
		for _, c := range cands {
			switch c.ext {
			case ".json":
				if jsonName == "" {
					jsonName = c.name
				} else {
					extras = append(extras, c.name)
				}
			case ".yaml":
				if yamlName == "" {
					yamlName = c.name
				} else {
					extras = append(extras, c.name)
				}
			case ".yml":
				if ymlName == "" {
					ymlName = c.name
				} else {
					extras = append(extras, c.name)
				}
			}
		}
		nonJSONName := yamlName
		if nonJSONName == "" {
			nonJSONName = ymlName
		}
		// If yaml AND yml both exist, the yml sibling is NOT part of
		// the twin comparison (matches pre-existing behaviour: it
		// always loaded unconditionally) — load it plainly now.
		if yamlName != "" && ymlName != "" {
			extras = append(extras, ymlName)
		}

		// loadPlain inserts a genuinely standalone (non-twin) file.
		// Most of its cases always load; it declines in exactly two
		// situations. (1) The file parses but declares NO cases —
		// reported whole, never fatal (see the zero-case branch
		// below). (2) A per-case exclusion, when a case's id happens
		// to collide with one a twin-pair resolution already claimed
		// elsewhere in this same directory scan (HXC-305 B3) — a
		// collision against another PLAIN file's id still hard-fails
		// the whole scan, exactly as before (the pre-existing P9
		// guarantee, untouched).
		loadPlain := func(name string) error {
			path := filepath.Join(dir, name)
			bf, err := LoadFile(path)
			if err != nil {
				return err
			}
			if len(bf.TestCases) == 0 {
				// Contributes nothing to the catalog. Before this was
				// reported, such a file was completely
				// indistinguishable from one that was never read at
				// all — no error, no Declined entry, no count. That
				// covers a truncated or emptied standalone bank AND a
				// stray non-bank YAML/JSON document that happens to
				// live in the directory (a config or fixture file),
				// which parses happily into a bank with no cases.
				// Reported, never fatal: a stray file must not newly
				// break a directory that has always loaded, and
				// unlike the twin case there is no sibling here for
				// lost content to hide behind.
				reason := "parses successfully but declares 0 test cases — " +
					"contributes nothing to the catalog. Either it is not a " +
					"test bank at all (a stray document in the bank directory), " +
					"or it is an empty, truncated or half-written bank that has " +
					"silently lost its content"
				// The most common way a real bank ends up here is a
				// misnamed list key: the loader reads "test_cases:" and
				// drops a top-level "cases:" without a word. Say so, because
				// that is the actionable fact (operator decision 2026-10-08;
				// found in banks/yole-concrete/yole-android-smoke.yaml).
				if hasTopLevelKey(path, "cases") {
					reason += ". This file has a top-level \"cases:\" key, but the " +
						"loader reads \"test_cases:\" — rename the key and its " +
						"cases will load"
				}
				declined = append(declined, DeclinedFile{Path: path, Reason: reason})
				return nil
			}
			excluded, loaded, err := addBank(path, bf, false)
			if err != nil {
				return err
			}
			if len(excluded) > 0 {
				// Say which actually happened: a bank whose EVERY
				// case collided was never appended at all, so calling
				// it "loaded, but excluded ..." would misreport it.
				lead := "loaded, but excluded"
				if !loaded {
					lead = "not loaded at all — every case excluded"
				}
				declined = append(declined, DeclinedFile{
					Path: path,
					Reason: fmt.Sprintf(
						"%s due to genuine cross-bank duplicate id(s) "+
							"that happen to also belong to a twin pair elsewhere: %s",
						lead, describeExcluded(excluded)),
					ExcludedIDs: excludedIDs(excluded),
				})
			}
			return nil
		}

		// insertTwin adds a twin-pair resolution result (a single
		// parseable side, or a content-preferred primary already
		// merged with any case ids unique to its secondary via
		// mergeTwinCases) and always records a Declined entry for
		// declinedPath naming what happened to its content: merged
		// in verbatim, or excluded for genuinely colliding with an
		// unrelated bank elsewhere (HXC-305 B3 — this must never
		// abort the whole directory scan, regardless of whether the
		// twin or the unrelated file is processed first).
		insertTwin := func(keptPath string, bf *BankFile, declinedPath, reason string) error {
			excluded, _, err := addBank(keptPath, bf, true)
			if err != nil {
				return err
			}
			if len(excluded) > 0 {
				reason += fmt.Sprintf(
					"; excluded due to genuine cross-bank duplicate id(s) unrelated to this twin pair: %s",
					describeExcluded(excluded))
			}
			declined = append(declined, DeclinedFile{
				Path:        declinedPath,
				Reason:      reason,
				ExcludedIDs: excludedIDs(excluded),
			})
			return nil
		}

		switch {
		case jsonName != "" && nonJSONName != "":
			// A twin pair. See LoadDirVerbose's doc comment: the twin
			// preferred for its executable content is merged with
			// whatever case ids the other twin has that it lacks, so
			// a content-driven preference can never make a case id
			// silently vanish (HXC-305 B1).
			jsonPath := filepath.Join(dir, jsonName)
			nonJSONPath := filepath.Join(dir, nonJSONName)
			jsonBF, jsonErr := LoadFile(jsonPath)
			nonJSONBF, nonJSONErr := LoadFile(nonJSONPath)

			switch {
			case nonJSONErr != nil:
				// The non-JSON twin is the form LoadDir has ALWAYS
				// required to parse successfully, whether or not a
				// JSON sibling exists — so its failure still aborts
				// the whole scan. This catches SYNTACTIC corruption
				// only; the content classes it cannot see are caught
				// by the zero-case case below and by the id floor
				// (HXC-305 F2 — see LoadDirVerbose's doc comment).
				// Covers the both-twins-broken case too — the
				// non-JSON error is the one to surface either way.
				return abort(nonJSONErr)
			case len(nonJSONBF.TestCases) == 0:
				// Parsed fine, declares nothing. This is what a
				// truncated-to-nothing, emptied, or half-copied
				// non-JSON twin actually looks like — valid YAML, no
				// content — and it is the case the parse check above
				// was long believed to cover but never did. Left
				// alone it resolves quietly to the JSON sibling and
				// silently drops every id the JSON form never
				// converted (measured: emptying banks/atmosphere.yaml
				// loses exactly 50 ids at exit 0, while the decline
				// line claims "none lost"). A bank with no cases is
				// never legitimate — 0 of the 195 real bank files
				// parse to zero cases — so this aborts the scan, and
				// says how to resolve it deliberately if the emptiness
				// was actually intended.
				// jsonBF may be nil here (its own parse may also have
				// failed) — describe the sibling without dereferencing.
				sibling := fmt.Sprintf("its twin %s failed to parse", jsonPath)
				if jsonErr == nil {
					sibling = fmt.Sprintf("its twin %s declares %d",
						jsonPath, len(jsonBF.TestCases))
				}
				return abort(fmt.Errorf(
					"bank file %s: parses successfully but declares 0 test cases, "+
						"while %s — refusing to resolve the pair to the twin alone, "+
						"because every case id unique to %s would vanish from the "+
						"catalog with no error. If %s is genuinely meant to be empty, "+
						"delete it instead of leaving an empty twin in place",
					nonJSONPath, sibling, nonJSONPath, nonJSONPath))
			case jsonErr != nil:
				// Only the non-JSON twin parses (matches the
				// pre-existing default) — nothing to merge from the
				// broken JSON twin.
				reason := fmt.Sprintf(
					"the %s twin failed to parse (%v); kept %s instead",
					jsonPath, jsonErr, nonJSONPath)
				if err := insertTwin(nonJSONPath, nonJSONBF, jsonPath, reason); err != nil {
					return abort(err)
				}
			case len(jsonBF.TestCases) == 0:
				// The JSON twin parses but declares nothing. Unlike an
				// empty non-JSON twin this loses no content — the
				// non-JSON side is the historical source of truth and
				// carries every id — so it must NOT hard-fail a
				// directory that has always loaded (the same
				// asymmetry the parse handling above preserves). It
				// is still reported distinctly rather than passed off
				// as an ordinary superseded twin, because "declares
				// nothing" and "declares the same ids the other twin
				// has" are very different facts about a file, and the
				// ordinary decline line renders both as "none lost".
				reason := fmt.Sprintf(
					"the %s twin parses but declares 0 test cases (an empty, "+
						"truncated or half-written file contributes nothing); "+
						"kept %s instead, which declares %d",
					jsonPath, nonJSONPath, len(nonJSONBF.TestCases))
				if err := insertTwin(nonJSONPath, nonJSONBF, jsonPath, reason); err != nil {
					return abort(err)
				}
			default:
				jsonExec, jsonTotal := countExecutableSteps(jsonBF)
				nonJSONExec, nonJSONTotal := countExecutableSteps(nonJSONBF)

				primary, primaryPath := jsonBF, jsonPath
				secondary, secondaryPath := nonJSONBF, nonJSONPath
				compareReason := fmt.Sprintf(
					"%d/%d executable step(s) vs %d/%d", jsonExec, jsonTotal,
					nonJSONExec, nonJSONTotal)
				if jsonExec <= nonJSONExec {
					// Tie or non-JSON strictly better: keep the
					// pre-existing default.
					primary, primaryPath = nonJSONBF, nonJSONPath
					secondary, secondaryPath = jsonBF, jsonPath
					compareReason = fmt.Sprintf(
						"%d/%d executable step(s) vs %d/%d", nonJSONExec,
						nonJSONTotal, jsonExec, jsonTotal)
				}

				mergedCases, mergedInIDs, preferredIDs := mergeTwinCases(primary, secondary)
				merged := *primary
				merged.TestCases = mergedCases

				reason := fmt.Sprintf("superseded by %s (%s)", primaryPath, compareReason)
				if len(mergedInIDs) > 0 {
					sort.Strings(mergedInIDs)
					reason += fmt.Sprintf(
						"; %d case id(s) absent from %s merged in verbatim from %s: %s",
						len(mergedInIDs), primaryPath, secondaryPath,
						strings.Join(mergedInIDs, ", "))
				} else {
					// State the count the claim rests on. "none lost"
					// is only meaningful alongside how many ids were
					// actually checked — an empty secondary used to
					// render as a confident "none lost" while 50 ids
					// vanished (that shape is now caught outright by
					// the zero-case invariant, but the claim should
					// not have been unfalsifiable in the first place).
					reason += fmt.Sprintf(
						"; all %d case id(s) in %s are already present in %s — none lost",
						len(secondary.TestCases), secondaryPath, primaryPath)
				}
				if len(preferredIDs) > 0 {
					sort.Strings(preferredIDs)
					reason += fmt.Sprintf(
						"; %d case id(s) shared by both twins kept from %s instead, "+
							"its body having strictly more executable step(s) than %s's: %s",
						len(preferredIDs), secondaryPath, primaryPath,
						strings.Join(preferredIDs, ", "))
				}
				if err := insertTwin(primaryPath, &merged, secondaryPath, reason); err != nil {
					return abort(err)
				}
			}
		case jsonName != "":
			if err := loadPlain(jsonName); err != nil {
				return abort(err)
			}
		case nonJSONName != "":
			if err := loadPlain(nonJSONName); err != nil {
				return abort(err)
			}
		}

		for _, name := range extras {
			if err := loadPlain(name); err != nil {
				return abort(err)
			}
		}
	}

	if enforceFloor {
		if err := checkBankIDFloor(dir, banks, declined); err != nil {
			// Returns the declined list ALONGSIDE the error, via the
			// same abort helper every other failure path uses.
			// Returning a bare nil here threw the list away on the
			// single most diagnostic path there is: the operator saw
			// "N case id(s) ... are absent" and nothing about which
			// twin files had been declined or what content went with
			// them, so the CLI printed zero declined lines precisely
			// when they explain the loss (HXC-305 round-6 BLOCKING A,
			// secondary). Round 14 generalised it to seven further
			// aborts and round 16 to the last reachable one, the
			// unparseable twin — so "every abort" became true of this
			// function only in round 16, not in round 14.
			return abort(err)
		}
	}

	return &LoadDirResult{Banks: banks, Declined: declined}, nil
}

// bankIDFloorFile is the OPTIONAL, per-directory record of the case
// ids a bank directory is known to contain — the expected id set the
// zero-case invariant cannot supply on its own, because a PARTIALLY
// truncated bank still declares plenty of cases and looks entirely
// well-formed (HXC-305 F2: truncating banks/atmosphere.yaml at a case
// boundary silently took the catalog 3046 -> 3006 at exit 0).
//
// It deliberately records ids rather than counts. A count says only
// that something changed; an id set says exactly WHAT went missing,
// which is the fact an operator needs, and it is immune to a
// simultaneous add-and-remove that leaves the count unmoved.
//
// Format: one case id per line. Blank lines, and lines whose first
// non-space character is '#', are ignored, so the file can carry its
// own regeneration instructions in a header.
//
// Regenerate it with the dedicated subcommand:
//
//	helixqa banks regen-floor --banks <dir>
//
// which is RegenerateBankIDFloor below. It is deliberately NOT a
// shell pipeline over `helixqa list`. That was the documented remedy
// until HXC-305 round 6 measured what it actually did: a regeneration
// only ever runs while the floor is FAILING, `list` aborts and exits
// 1 on exactly that state before writing any stdout, and the brace
// group took `sort`'s exit status — so the `&& mv` fired and replaced
// a 3046-id floor with a header-only file at exit 0. The remedy
// disarmed the guard in the only state it was ever used in.
const bankIDFloorFile = ".bank-id-floor.txt"

// checkBankIDFloor enforces bankIDFloorFile if the directory carries
// one: every id it records MUST have ended up either in a loaded bank
// or in a Declined entry's ExcludedIDs (the one legitimate way for a
// case to be absent — a genuine cross-bank duplicate, HXC-305 B3).
// Anything else has silently vanished, and that is a hard error
// naming every missing id.
//
// Three properties keep this from being brittle against legitimate
// bank edits:
//
//   - It is a FLOOR, never an equality check. Adding cases — by far
//     the common edit — can never trip it and never requires the file
//     to be regenerated. Only REMOVING a case does, and a removal is
//     precisely the event that should surface in review rather than
//     land silently (§11.4.122).
//   - It is OPT-IN per directory. A directory with no floor file is
//     not checked at all, so `--banks <any path>` keeps behaving
//     exactly as it did; nothing new can fail for a caller pointing
//     at an arbitrary directory.
//   - It is regenerable from the corpus itself by a dedicated
//     command — `helixqa banks regen-floor` / RegenerateBankIDFloor
//     (see bankIDFloorFile) — so it can never drift into a hand-
//     maintained second source of truth (§11.4.77). That command
//     bypasses THIS check by construction, because a regeneration is
//     only ever run while this check is failing.
//
// A malformed or unreadable floor file is itself an error rather than
// a silent skip: a floor that quietly stops being enforced is the
// same class of defect it exists to catch.
//
// HONEST BOUNDARY (§11.4.6). banks/'s floor currently records exactly
// the ids the catalog currently yields (3046 == 3046), so today it
// happens to be indistinguishable from a snapshot. That equality is
// incidental — nothing has been added since it was written — but the
// coverage consequence is real and is NOT a bug being papered over: a
// bank added AFTER a regeneration is outside the floor's protection
// until the next regeneration, so truncating that new bank is not
// caught by this mechanism. Making it an equality check instead would
// close that gap only by making every ADD fail the scan until the
// floor was rewritten, which trades a silent-loss guard for a
// constant tax on the common edit. The chosen posture is therefore
// deliberate: a monotone lower bound, plus a cheap and loud
// regeneration (`helixqa banks regen-floor`) that a change adding
// banks is expected to run. Zero-case detection, which IS
// unconditional, still covers a newly-added bank truncated all the
// way to empty; only PARTIAL truncation of a post-floor bank is
// uncovered.
func checkBankIDFloor(dir string, banks []*BankFile, declined []DeclinedFile) error {
	path := filepath.Join(dir, bankIDFloorFile)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // not enforced for this directory
		}
		return fmt.Errorf("read bank id floor %s: %w", path, err)
	}

	present := map[string]bool{}
	for _, bf := range banks {
		for _, tc := range bf.TestCases {
			if tc.ID != "" {
				present[tc.ID] = true
			}
		}
	}
	// Absent-but-accounted-for: an id excluded as a genuine cross-bank
	// duplicate is reported, not lost, so it satisfies the floor.
	for _, d := range declined {
		for _, id := range d.ExcludedIDs {
			present[id] = true
		}
	}

	var missing []string
	for _, line := range strings.Split(string(data), "\n") {
		id := strings.TrimSpace(line)
		if id == "" || strings.HasPrefix(id, "#") {
			continue
		}
		if !present[id] {
			missing = append(missing, id)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	sort.Strings(missing)
	return fmt.Errorf(
		"bank dir %s: %d case id(s) recorded in %s are absent from the loaded "+
			"catalog and were not reported as excluded duplicates — content has "+
			"gone missing silently (a truncated, emptied or half-written bank "+
			"file is the usual cause; a deliberate removal must be recorded by "+
			"regenerating %s in the same change): %s",
		dir, len(missing), bankIDFloorFile, bankIDFloorFile,
		strings.Join(missing, ", "))
}

// defaultBankIDFloorHeader is written only when a directory is
// getting a floor for the first time. An existing file's own header
// is always preserved verbatim instead, so a directory can carry
// project-specific notes without a regeneration eating them.
const defaultBankIDFloorHeader = `# HelixQA bank id floor — the case ids this directory is known to
# contain. Every id below MUST end up loaded, or reported as an
# excluded cross-bank duplicate; a missing one hard-fails the
# directory scan and is named. See pkg/testbank/loader.go
# checkBankIDFloor.
#
# This is a FLOOR, not a snapshot: ADDING cases never trips it and
# never needs this file touched. Only REMOVING a case does — which is
# the point, so a removal is reviewed rather than silent.
#
# REGENERATE (after a deliberate removal):
#   helixqa banks regen-floor --banks <dir>`

// FloorRegenResult describes what a regeneration changed, so the
// caller can report it. A regeneration is only ever run after a
// deliberate removal, so Removed is the operator-facing fact that
// matters most: it is the exact content the floor will stop
// protecting.
type FloorRegenResult struct {
	Path    string   // the floor file written
	Total   int      // ids written
	Added   []string // ids present now, absent from the previous floor
	Removed []string // ids the previous floor recorded that are now gone
	Created bool     // true when the directory had no floor before
}

// RegenerateBankIDFloor rewrites dir's bankIDFloorFile from the
// directory's own live catalog, preserving any existing header.
//
// This is the ONLY supported way to regenerate a floor, and it exists
// because the shell pipeline that used to be documented in the file's
// header could not work: a regeneration is by definition run while
// the floor is failing, the enforced scan aborts on exactly that
// state, and the pipeline's exit status came from `sort` rather than
// from the aborted scan — so it wrote a header-only, zero-id floor at
// exit 0 and disabled the guard (HXC-305 round-6 BLOCKING A).
//
// Three properties make that failure mode unreachable here rather
// than merely detected:
//
//   - The scan runs with the floor check OFF by construction
//     (loadDirVerbose(dir, false)), so a failing floor cannot
//     suppress the catalog this function reads. Every other error —
//     unparseable bank, duplicate id, unreadable directory — still
//     aborts and no file is written.
//   - An empty id set is refused outright. Writing a floor that
//     protects nothing is never a legitimate outcome, and it is the
//     precise shape the old pipeline produced.
//   - The write is atomic (temp file + rename in the same
//     directory), so an interrupted regeneration leaves the previous
//     floor intact rather than a truncated one.
//
// The recorded set is every id that satisfies the floor: loaded, OR
// reported as an excluded cross-bank duplicate. That mirrors
// checkBankIDFloor's own accounting exactly, which is the point: the two
// sides of the guard agree by construction rather than by coincidence.
//
// It does NOT currently change the result. In any successfully-returned
// LoadDirResult `excluded` is a subset of the loaded ids, so the
// loaded-only view the old `helixqa list` pipeline had yields an
// identical set — it would drop a legitimately excluded id on ZERO
// regenerations, not on every one. The union is kept because that subset
// relation is an invariant of today's insertBank, not a guarantee of the
// contract; TestHXC305_RegenFloorRecordsExcludedIDs is the tripwire that
// fails the day it stops holding.
func RegenerateBankIDFloor(dir string) (*FloorRegenResult, error) {
	res, err := loadDirVerbose(dir, false)
	if err != nil {
		return nil, err
	}

	idSet := map[string]bool{}
	for _, bf := range res.Banks {
		for _, tc := range bf.TestCases {
			if tc.ID != "" {
				idSet[tc.ID] = true
			}
		}
	}
	for _, d := range res.Declined {
		for _, id := range d.ExcludedIDs {
			idSet[id] = true
		}
	}

	ids := make([]string, 0, len(idSet))
	for id := range idSet {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	path := filepath.Join(dir, bankIDFloorFile)
	if len(ids) == 0 {
		return nil, fmt.Errorf(
			"bank dir %s: refusing to write an empty %s — the scan yielded 0 case "+
				"ids, so the floor would protect nothing and every future removal "+
				"would go unnoticed. Either the directory holds no banks, or it is "+
				"itself broken; fix that first",
			dir, bankIDFloorFile)
	}

	// Preserve the existing header verbatim; fall back to the default
	// only when the directory is getting a floor for the first time.
	out := &FloorRegenResult{Path: path, Total: len(ids)}
	header := defaultBankIDFloorHeader
	prev := map[string]bool{}
	switch data, err := os.ReadFile(path); {
	case err == nil:
		var keep []string
		for _, line := range strings.Split(string(data), "\n") {
			t := strings.TrimSpace(line)
			switch {
			case strings.HasPrefix(t, "#"):
				keep = append(keep, line)
			case t != "":
				prev[t] = true
			}
		}
		if len(keep) > 0 {
			header = strings.Join(keep, "\n")
		}
	case os.IsNotExist(err):
		out.Created = true
	default:
		return nil, fmt.Errorf("read bank id floor %s: %w", path, err)
	}

	for _, id := range ids {
		if !prev[id] {
			out.Added = append(out.Added, id)
		}
	}
	for id := range prev {
		if !idSet[id] {
			out.Removed = append(out.Removed, id)
		}
	}
	sort.Strings(out.Removed)
	if len(prev) == 0 {
		// Everything is trivially "added" against a floor that recorded
		// no ids; reporting the whole catalog as additions is noise, not
		// information. Keyed on the previous floor being EMPTY, not on
		// the file being absent: `Created` is only one of the two ways
		// to get here, and the other — a floor that exists but records
		// zero ids — is the header-only file the old shell pipeline
		// wrote (round-6 BLOCKING A), i.e. the state that produces the
		// MOST noise is exactly the one an absence test misses.
		out.Added = nil
	}

	body := header + "\n" + strings.Join(ids, "\n") + "\n"
	tmp, err := os.CreateTemp(dir, bankIDFloorFile+".regen-*")
	if err != nil {
		return nil, fmt.Errorf("create temp floor in %s: %w", dir, err)
	}
	tmpName := tmp.Name()
	if _, err := tmp.WriteString(body); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return nil, fmt.Errorf("write temp floor %s: %w", tmpName, err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return nil, fmt.Errorf("close temp floor %s: %w", tmpName, err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return nil, fmt.Errorf("replace floor %s: %w", path, err)
	}
	return out, nil
}

// mergeTwinCases returns primary's cases, in their original order,
// followed by any case from secondary whose id is NOT already present
// in primary — so the merged case list's id set is always the UNION
// of both twins', never a subset of either (HXC-305 B1: a content-
// driven twin preference must never make a case id vanish just
// because the preferred twin happens to be a PARTIAL conversion of
// the other — e.g. one real bank's JSON form converts 129 of its YAML
// form's 179 cases; the JSON form wins on executable-step count, but
// the 50 cases it never converted still load, verbatim from the YAML
// form). mergedInIDs names every id actually pulled in from secondary
// this way, for a caller to report; it is empty when primary already
// covers every id secondary has (the common case).
//
// For a case id present in BOTH twins, the body is chosen PER CASE,
// not inherited from the bank-level winner (HXC-305 F1): secondary's
// body wins iff it has STRICTLY MORE genuinely-executable steps than
// primary's; ties keep primary's. A bank-level preference alone is
// unsound here — a conversion pass can raise a bank's overall
// executable-step count while DE-CONVERTING an individual case's
// already-executable step back into prose, and the bank-level winner
// then drags that regression in with it. Measured on this project's
// corpus: atmosphere.json wins its bank comparison (49/395 vs
// 24/566) yet holds a strictly worse body for PA-001, PA-005 and
// VR-015 — 3 cases that stop executing entirely under bank
// granularity, against 41 the JSON twin genuinely improves in that
// same bank (684 corpus-wide, of 1897 shared ids).
// preferredFromSecondaryIDs names every shared id resolved to
// secondary's body this way, for a caller to report.
func mergeTwinCases(primary, secondary *BankFile) (
	merged []TestCase, mergedInIDs, preferredFromSecondaryIDs []string,
) {
	secByID := make(map[string]*TestCase, len(secondary.TestCases))
	for i := range secondary.TestCases {
		if id := secondary.TestCases[i].ID; id != "" {
			secByID[id] = &secondary.TestCases[i]
		}
	}

	have := make(map[string]bool, len(primary.TestCases))
	merged = make([]TestCase, 0, len(primary.TestCases)+len(secondary.TestCases))
	for i := range primary.TestCases {
		tc := primary.TestCases[i]
		if tc.ID != "" {
			have[tc.ID] = true
			// Resolve a shared id by comparing THESE TWO BODIES, not
			// the two banks they came from.
			if sec, shared := secByID[tc.ID]; shared {
				primaryExec, _ := countCaseExecutableSteps(&tc)
				secondaryExec, _ := countCaseExecutableSteps(sec)
				if secondaryExec > primaryExec {
					preferredFromSecondaryIDs = append(preferredFromSecondaryIDs, tc.ID)
					tc = *sec
				}
			}
		}
		merged = append(merged, tc)
	}
	for _, tc := range secondary.TestCases {
		if tc.ID != "" && have[tc.ID] {
			continue
		}
		merged = append(merged, tc)
		if tc.ID != "" {
			mergedInIDs = append(mergedInIDs, tc.ID)
			have[tc.ID] = true
		}
	}
	return merged, mergedInIDs, preferredFromSecondaryIDs
}

// insertBank inserts bf into banks case-by-case via xref.
//
// When isTwin is true, every id this call successfully claims is
// marked in twinIDs, so a LATER collision against one of them — from
// another twin-pair resolution, or from a genuinely standalone file
// processed afterwards — is tolerated: that one case is excluded
// (never the whole bank) and reported back to the caller, instead of
// aborting the entire directory scan (HXC-305 B3). This holds
// regardless of processing order: a twin inserted BEFORE an unrelated
// standalone file that happens to share its id, and a twin inserted
// AFTER one, both resolve the same way — only the specific colliding
// case is excluded.
//
// When isTwin is false (loadPlain's case — a genuinely standalone,
// non-twin file), a collision is tolerated ONLY if the existing
// claimant is itself twin-derived (twinIDs[id]); a collision between
// two PLAIN files still returns a hard error, exactly as the
// pre-existing P9 cross-bank duplicate-id guarantee always has — a
// lone duplicate id between two otherwise-unrelated files has no
// twin/merge context to fall back on.
//
// Returns every excluded case (id plus the file that claimed it
// first) — empty in the overwhelmingly common case — and whether the
// bank was actually appended at all, which is false when EVERY one of
// its cases was excluded. Never mutates bf — appends a shallow copy
// carrying only the kept cases.
func insertBank(
	dir, path string, bf *BankFile, isTwin bool,
	xref map[string]string, twinIDs map[string]bool, banks *[]*BankFile,
) (excluded []excludedCase, loaded bool, err error) {
	kept := make([]TestCase, 0, len(bf.TestCases))
	for _, tc := range bf.TestCases {
		if tc.ID != "" {
			if prev, dup := xref[tc.ID]; dup {
				if isTwin || twinIDs[tc.ID] {
					excluded = append(excluded, excludedCase{ID: tc.ID, ClaimedBy: prev})
					continue
				}
				return nil, false, fmt.Errorf(
					"bank dir %s: duplicate test case id %q across banks (also in %s)",
					dir, tc.ID, prev,
				)
			}
			xref[tc.ID] = path
			if isTwin {
				twinIDs[tc.ID] = true
			}
		}
		kept = append(kept, tc)
	}
	if len(kept) == 0 {
		return excluded, false, nil
	}
	out := *bf
	out.TestCases = kept
	*banks = append(*banks, &out)
	return excluded, true, nil
}

// excludedCase is one case id insertBank refused to load because an
// unrelated bank claimed that id first (HXC-305 B3). Kept structured
// rather than pre-formatted so callers can populate
// DeclinedFile.ExcludedIDs exactly — a prose reason alone cannot be
// matched reliably, since bank ids in this corpus are not suffix-free.
type excludedCase struct {
	ID        string
	ClaimedBy string // path of the bank that claimed ID first
}

// excludedIDs returns just the ids, sorted — the structural form for
// DeclinedFile.ExcludedIDs.
func excludedIDs(ex []excludedCase) []string {
	ids := make([]string, 0, len(ex))
	for _, e := range ex {
		ids = append(ids, e.ID)
	}
	sort.Strings(ids)
	return ids
}

// describeExcluded renders the human-readable form for a Reason,
// sorted by id so the text is deterministic.
func describeExcluded(ex []excludedCase) string {
	parts := make([]string, 0, len(ex))
	for _, e := range ex {
		parts = append(parts, fmt.Sprintf("%s (already claimed by %s)", e.ID, e.ClaimedBy))
	}
	sort.Strings(parts)
	return strings.Join(parts, ", ")
}

// countCaseExecutableSteps reports how many of ONE case's steps parse
// as a genuinely executable action (anything other than
// ActionTypeDescription), out of that case's total step count. This
// is the granularity at which a twin's body is actually chosen
// (HXC-305 F1 — see mergeTwinCases); countExecutableSteps aggregates
// it over a whole bank purely to pick the primary.
func countCaseExecutableSteps(tc *TestCase) (exec, total int) {
	for _, st := range tc.Steps {
		total++
		at, _ := st.ParseAction()
		if at != ActionTypeDescription {
			exec++
		}
	}
	return exec, total
}

// countExecutableSteps reports how many of a bank's steps parse as a
// genuinely executable action, out of the total step count. Used to
// pick the PRIMARY of a YAML/JSON twin pair (HXC-305): the twin that
// actually runs more beats the twin that only describes it. It does
// NOT by itself decide any individual shared case's body — that is
// per-case (HXC-305 F1, mergeTwinCases).
func countExecutableSteps(bf *BankFile) (exec, total int) {
	for i := range bf.TestCases {
		e, t := countCaseExecutableSteps(&bf.TestCases[i])
		exec += e
		total += t
	}
	return exec, total
}

// SaveFile writes a BankFile to a YAML file.
func SaveFile(path string, bf *BankFile) error {
	data, err := yaml.Marshal(bf)
	if err != nil {
		return fmt.Errorf("marshal bank file: %w", err)
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create bank dir: %w", err)
	}
	return os.WriteFile(path, data, 0644)
}
