// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package contract

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestDocPurity walks every .go source file under internal/, pkg/, and cmd/
// and fails if any comment line contains a wave-numbering or audit-tag
// pattern that belongs only to the internal tracking system, not to
// published documentation, or a provenance reference to a legacy
// reference project (provenance belongs in the Markdown docs).
//
// Patterns detected (all scoped to lines whose first non-whitespace is "//"):
//
//   - Wave/Welle numbers: Wave-3, W6-A, W6-B2, Welle 4
//   - Audit item IDs: A3-L05, A4-L17
//   - Parity-audit M-codes: M1234, M2048 (4-digit codes)
//   - G-code items: G-24, G-53
//   - V/Q items: V8-N29, Q-23
//   - parity_audit.md references
//   - Audit phase tags: Phase-3, Phase-3a (standalone tag form)
//   - Migration-step tags: migration step 4
//   - Legacy-project tokens: aiohomematic, aiohomematic-config,
//     aiohomematic_config, aiohomematic2mqtt, homematicip_local,
//     homematicip-local, homematicip-local-frontend, pydevccu, openccu-data
//   - Drift-ID tracking codes: Drift L0-D01, drift L9-NEW-1, etc.
//   - Audit-run references: "audit run #02", "parity audit"
//
// Exceptions:
//   - ha_ prefixed files (W8-A conflict zone)
//   - tests/integration/testdata/ (golden wire data)
//   - This file itself (doc_purity_test.go) — it enumerates the patterns
//     in its own doc-comment above.
//
// Test function names, identifiers, string literals, and `t.Errorf` /
// `t.Fatalf` / `t.Skipf` / `t.Logf` argument strings are NOT checked.
// Only comment lines are scanned.
func TestDocPurity(t *testing.T) {
	t.Parallel()

	// forbidden is the set of patterns we no longer allow in comment lines.
	// Each entry is (label, compiled regexp). A match on any pattern is a hit.
	type rule struct {
		label string
		re    *regexp.Regexp
	}

	rules := []rule{
		// Wave-N / Welle N / wave\d+ / W6-A — internal wave-of-changes
		// markers that must not bleed into published code documentation.
		{"Wave-N numbering", regexp.MustCompile(`\bWave-\d+\b`)},
		{"Wave N numbering", regexp.MustCompile(`\bWave\s+\d+\b`)},
		{"wave<N> tag", regexp.MustCompile(`\bwave\d+\b`)},
		{"W<N>-<X> wave tag", regexp.MustCompile(`\bW\d+-[A-Z]\d*\b`)},
		{"Welle N numbering", regexp.MustCompile(`\bWelle\s+\d+\b`)},
		// Audit item IDs: A3-L05, L7.4, L1.2 — internal tracking IDs.
		{"Audit item A<N>-L<N>", regexp.MustCompile(`\bA\d+-L\d+\b`)},
		{"L<digit>.<digit> audit item", regexp.MustCompile(`\bL\d+\.\d+\b`)},
		// 4-digit M-codes (audit item numbers).
		{"M-code M<4digits>", regexp.MustCompile(`\bM\d{4}\b`)},
		// G-codes / QW-codes.
		{"G-code G-<N>", regexp.MustCompile(`\bG-\d+\b`)},
		{"QW-<N> item", regexp.MustCompile(`\bQW-\d+\b`)},
		// V/Q identifiers.
		{"V<N>-N<N> item", regexp.MustCompile(`\bV\d+-N\d+\b`)},
		{"Q-<N> item", regexp.MustCompile(`\bQ-\d+\b`)},
		// parity_audit / parity_request references.
		{"parity_audit.md reference", regexp.MustCompile(`\bparity_audit\.md\b`)},
		{"parity_request.md reference", regexp.MustCompile(`\bparity_request\.md\b`)},
		// Phase-N (standalone tag).
		{"Phase-N tag", regexp.MustCompile(`\bPhase-\d+[a-z]?\b`)},
		// "Phase N" prose form (audit-tracking phases).
		{"Phase N tag", regexp.MustCompile(`\bPhase\s+\d+\b`)},
		// "migration step N".
		{"migration step N", regexp.MustCompile(`\bmigration step \d+\b`)},
		// Legacy-project provenance tokens — these belong in the
		// Markdown documentation, not in code comments.
		{"legacy: aiohomematic", regexp.MustCompile(`\baiohomematic\b`)},
		{"legacy: aiohomematic-config", regexp.MustCompile(`\baiohomematic-config\b`)},
		// Underscore form: `\baiohomematic\b` does not match inside
		// "aiohomematic_config" because the underscore is a word char.
		{"legacy: aiohomematic_config", regexp.MustCompile(`\baiohomematic_config\b`)},
		{"legacy: aiohomematic2mqtt", regexp.MustCompile(`\baiohomematic2mqtt\b`)},
		{"legacy: homematicip_local", regexp.MustCompile(`\bhomematicip_local\b`)},
		{"legacy: homematicip-local", regexp.MustCompile(`\bhomematicip-local\b`)},
		{"legacy: homematicip-local-frontend", regexp.MustCompile(`\bhomematicip-local-frontend\b`)},
		{"legacy: pydevccu", regexp.MustCompile(`\bpydevccu\b`)},
		{"legacy: openccu-data", regexp.MustCompile(`\bopenccu-data\b`)},
		// Audit date stamps inside comments. Generated files (entity
		// descriptions etc.) carry generation timestamps and are
		// excluded by the path filter below.
		{"audit date stamp", regexp.MustCompile(`\b2026-0[456]-\d{2}\b`)},
		// Drift-ID tracking codes. The Drift-IDs are audit-internal
		// tracking handles that belong in the markdown audit-trail
		// (master_tracker.md, by_design.md, commit messages). They have
		// no useful meaning to a future reader of production code — the
		// comment should describe WHAT the code does and WHY, not which
		// audit-row originally requested the change.
		//
		// Multiple shapes have appeared in the codebase over time; the
		// rules cover all observed variants:
		//
		//   - `Drift L0-D01`, `drift L1-NEW-2` (with the literal "Drift")
		//   - `L9-D8`, `L2-D06`, `L10-D02` (bare layer-drift IDs)
		//   - `L9-NEW-5`, `L5-NEW-D03` (NEW-suffix forms)
		//   - `L3-D6-FUTURE`, `L0x-D_FUTURE_OBSERVER` (skip-placeholder
		//     suffixes used in `t.Skip` annotations)
		//   - `L0-OC-01` (sub-system-specific drift IDs)
		{"drift-ID tracking code", regexp.MustCompile(`\b[Dd]rift L\d+`)},
		{"bare drift-ID L<x>-D<y>", regexp.MustCompile(`\bL\d+-D\d+\b`)},
		{"drift-ID L<x>-NEW", regexp.MustCompile(`\bL\d+-NEW(-\d+|-D\d+)?\b`)},
		{"drift-ID L<x>-OC", regexp.MustCompile(`\bL\d+-OC-\d+\b`)},
		{"drift-ID FUTURE placeholder", regexp.MustCompile(`\bL\d+[a-z]?-D\w*FUTURE\w*\b`)},
		// `audit run #02` / `parity audit` / `parity sweep` mentions —
		// same rationale as drift-IDs.
		{"audit run reference", regexp.MustCompile(`\baudit run #\d+\b`)},
		{"parity audit reference", regexp.MustCompile(`\bparity audit\b`)},
		{"parity sweep reference", regexp.MustCompile(`\bparity sweep\b`)},
		// German/English audit-hybrid token `MANDATORY-FEHLT`.
		{"audit token: MANDATORY-FEHLT", regexp.MustCompile(`\bMANDATORY-FEHLT\b`)},
		// German common words that should not appear in code comments.
		// Heuristic: short German function-words that almost never occur
		// in English technical prose. The `\b...\b` boundaries avoid
		// false positives on identifiers.
		{"German: darf", regexp.MustCompile(`\bdarf\b`)},
		{"German: soll", regexp.MustCompile(`\bsoll\b`)},
		{"German: muss", regexp.MustCompile(`\bmuss\b`)},
		{"German: nicht", regexp.MustCompile(`\bnicht\b`)},
		{"German: über", regexp.MustCompile(`\büber\b`)},
		{"German: dürfen", regexp.MustCompile(`\bdürfen\b`)},
		{"German: müssen", regexp.MustCompile(`\bmüssen\b`)},
		{"German: während", regexp.MustCompile(`\bwährend\b`)},
		{"German: damit", regexp.MustCompile(`\bdamit\b`)},
		{"German: dafür", regexp.MustCompile(`\bdafür\b`)},
		{"German: daher", regexp.MustCompile(`\bdaher\b`)},
		{"German: liefert", regexp.MustCompile(`\bliefert\b`)},
		{"German: enthält", regexp.MustCompile(`\benthält\b`)},
		{"German: erlaubt", regexp.MustCompile(`\berlaubt\b`)},
		{"German: ergänzt", regexp.MustCompile(`\bergänzt\b`)},
		{"German abbrev: bzw.", regexp.MustCompile(`\bbzw\.`)},
		{"German abbrev: z.B.", regexp.MustCompile(`\bz\.\s?B\.`)},
	}

	repoRoot := filepath.Join("..", "..")
	scanDirs := []string{
		filepath.Join(repoRoot, "internal"),
		filepath.Join(repoRoot, "pkg"),
		filepath.Join(repoRoot, "cmd"),
		filepath.Join(repoRoot, "tests", "contract"),
	}

	// Self-referencing test files are excluded — they describe the
	// forbidden patterns in their own doc-comments. Both this file and
	// `markdown_links_test.go` enumerate audit-tracking jargon in their
	// rationale prose.
	thisFile, _ := filepath.Abs("doc_purity_test.go")
	mdLinksFile, _ := filepath.Abs("markdown_links_test.go")

	type hit struct {
		file    string
		lineNo  int
		line    string
		pattern string
	}

	var hits []hit

	for _, root := range scanDirs {
		err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if info.IsDir() {
				return nil
			}
			if !strings.HasSuffix(path, ".go") {
				return nil
			}

			// Skip ha_ files (W8-A conflict zone).
			base := filepath.Base(path)
			if strings.HasPrefix(base, "ha_") {
				return nil
			}

			// Skip integration testdata.
			if strings.Contains(path, filepath.Join("tests", "integration", "testdata")) {
				return nil
			}

			// Skip this file.
			abs, _ := filepath.Abs(path)
			if abs == thisFile || abs == mdLinksFile {
				return nil
			}

			content, readErr := os.ReadFile(path)
			if readErr != nil {
				return readErr
			}

			for lineNo, rawLine := range strings.Split(string(content), "\n") {
				// Only look at comment lines.
				trimmed := strings.TrimSpace(rawLine)
				if !strings.HasPrefix(trimmed, "//") {
					continue
				}

				for _, r := range rules {
					if r.re.MatchString(trimmed) {
						hits = append(hits, hit{
							file:    path,
							lineNo:  lineNo + 1,
							line:    strings.TrimRight(rawLine, "\r"),
							pattern: r.label,
						})
						break // one hit per line is enough
					}
				}
			}

			return nil
		})
		if err != nil {
			t.Errorf("walk %q: %v", root, err)
		}
	}

	if len(hits) == 0 {
		return
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "doc_purity: %d comment line(s) contain forbidden audit/wave patterns:\n\n", len(hits))
	for _, h := range hits {
		fmt.Fprintf(&sb, "  %s:%d [%s]\n    %s\n\n", h.file, h.lineNo, h.pattern, strings.TrimSpace(h.line))
	}
	sb.WriteString("Run the cleanup script or remove the patterns manually.\n")
	sb.WriteString("Audit tags like G-24, M1234, A3-L05 must not appear in comments.\n")
	sb.WriteString("Legacy-project provenance (aiohomematic, homematicip_local, ...)\n")
	sb.WriteString("belongs in the Markdown documentation, not in code comments.\n")

	t.Fatal(sb.String())
}

// TestDocPurity_MarkdownRefsExist scans every `//` comment in
// internal/, pkg/, cmd/ for references that look like markdown file
// paths and fails when the referenced file does not exist at the
// project root.
//
// Rationale: references to transient audit-trail docs (audit_runs/*,
// memory hand-offs, todo lists) age into stale pointers within weeks.
// References to permanent documents (ADRs, SPECIFICATION.md,
// notes/parity/by_design.md, architecture concepts) stay useful for
// years. The test enforces the boundary mechanically: if you cite a
// .md file in a code comment, that file has to be checked in.
//
// Heuristic: a markdown reference is a token of the form
// `<path-segment>(\.md)\b` containing at least one of `/` or `.`
// characters. The bare token `foo.md` without a directory prefix is
// only resolved relative to the project root; nested `docs/...` paths
// are resolved verbatim.
func TestDocPurity_MarkdownRefsExist(t *testing.T) {
	t.Parallel()

	repoRoot := filepath.Join("..", "..")
	absRoot, err := filepath.Abs(repoRoot)
	if err != nil {
		t.Fatalf("abs repo root: %v", err)
	}

	scanDirs := []string{
		filepath.Join(repoRoot, "internal"),
		filepath.Join(repoRoot, "pkg"),
		filepath.Join(repoRoot, "cmd"),
	}

	// Token matching: a printable, non-whitespace run that ends in `.md`.
	// We strip surrounding punctuation (`, ', ", ), ], etc.) and then check
	// for at least one path-like character so bare words like "foo.md"
	// inside prose don't false-positive (those are caught by the path-
	// existence check anyway).
	mdRefRE := regexp.MustCompile(`[A-Za-z0-9_./-]+\.md\b`)

	type missingRef struct {
		file   string
		lineNo int
		ref    string
		line   string
	}
	var missing []missingRef

	for _, root := range scanDirs {
		_ = filepath.Walk(root, func(path string, info os.FileInfo, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if info.IsDir() || !strings.HasSuffix(path, ".go") {
				return nil
			}
			content, readErr := os.ReadFile(path)
			if readErr != nil {
				return readErr
			}
			for lineNo, rawLine := range strings.Split(string(content), "\n") {
				trimmed := strings.TrimSpace(rawLine)
				if !strings.HasPrefix(trimmed, "//") {
					continue
				}
				for _, ref := range mdRefRE.FindAllString(trimmed, -1) {
					// Resolve relative to repo root. If the ref starts
					// with a known top-level dir or is the bare top-level
					// document, look it up directly; otherwise also try
					// relative-to-package as a fallback.
					candidate := filepath.Join(absRoot, ref)
					if _, statErr := os.Stat(candidate); statErr == nil {
						continue
					}
					// Fallback: relative to the file's own directory.
					alt := filepath.Join(filepath.Dir(path), ref)
					if _, statErr := os.Stat(alt); statErr == nil {
						continue
					}
					missing = append(missing, missingRef{
						file:   path,
						lineNo: lineNo + 1,
						ref:    ref,
						line:   strings.TrimRight(rawLine, "\r"),
					})
				}
			}
			return nil
		})
	}

	if len(missing) == 0 {
		return
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "doc_purity: %d markdown reference(s) point at non-existent files:\n\n", len(missing))
	for _, m := range missing {
		fmt.Fprintf(&sb, "  %s:%d [%s]\n    %s\n\n", m.file, m.lineNo, m.ref, strings.TrimSpace(m.line))
	}
	sb.WriteString("Either restore the referenced file at the cited path, or remove\n")
	sb.WriteString("the reference from the comment. Code comments must only cite\n")
	sb.WriteString("durable documents (ADRs, SPECIFICATION.md, architecture concepts,\n")
	sb.WriteString("the by_design catalogue) — transient audit-trail markdown\n")
	sb.WriteString("rots and leaves stale pointers behind.\n")

	t.Fatal(sb.String())
}
