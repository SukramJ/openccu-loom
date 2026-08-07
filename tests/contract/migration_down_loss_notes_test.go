// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package contract

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// TestMigrationDownDropsHaveLossNotes enforces the documented policy that
// `goose down` is not a supported operator path (see CONTRIBUTING.md and
// docs/adr/0054-migration-down-path-unsupported.md): every migration keeps a
// syntactically valid `-- +goose Down` block for development/test convenience
// (e.g. schema-diff tooling that walks up and back down), but a Down block
// that drops a table or column must say so in plain language directly above
// the `-- +goose Down` marker, naming what is destroyed and why it cannot be
// recovered. Without this, the down path silently discards data that ranges
// from a rebuildable cache to bcrypt password hashes, Matter NOC private
// keys, and the append-only alarm journal — an operator who runs `goose
// down` finds out only after the fact.
//
// A migration whose Down block does not touch DROP TABLE / DROP COLUMN needs
// no note (renames, additive-only rollbacks, and similar reversible shapes
// are exempt — see migrations/031_alarm_zones_rename.sql for an example that
// this test does not flag).
func TestMigrationDownDropsHaveLossNotes(t *testing.T) {
	t.Parallel()

	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}
	dirs := []string{
		filepath.Join(repoRoot, "internal", "store", "sqlite", "migrations"),
		filepath.Join(repoRoot, "internal", "store", "sqlite", "migrations_history"),
	}

	dropRE := regexp.MustCompile(`(?i)\bDROP\s+(TABLE|COLUMN)\b`)
	gooseDownRE := regexp.MustCompile(`^--\s*\+goose\s+Down\b`)

	// minNoteChars keeps the check measurable: a one-word placeholder like
	// "-- see above" must not satisfy it. The shortest real loss note in this
	// repo runs well past 100 characters.
	const minNoteChars = 30

	var violations []string
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatalf("read %s: %v", dir, err)
		}
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
				continue
			}
			path := filepath.Join(dir, entry.Name())
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read %s: %v", path, err)
			}
			lines := strings.Split(string(raw), "\n")

			downIdx := -1
			for i, line := range lines {
				if gooseDownRE.MatchString(strings.TrimSpace(line)) {
					downIdx = i
					break
				}
			}
			if downIdx == -1 {
				// Every migration in this repo carries a goose Down marker;
				// a missing one is a different problem than this test polices.
				continue
			}

			downBlock := strings.Join(lines[downIdx:], "\n")
			if !dropRE.MatchString(downBlock) {
				continue // nothing destructive in this Down block
			}

			// Walk upward from the marker, collecting contiguous "--" comment
			// lines. A blank line (or any non-comment line) breaks the run —
			// the note must sit directly above the marker, matching the
			// convention every annotated migration in this repo follows.
			var noteChars int
			for j := downIdx - 1; j >= 0; j-- {
				trimmed := strings.TrimSpace(lines[j])
				if trimmed == "" || !strings.HasPrefix(trimmed, "--") {
					break
				}
				if gooseDownRE.MatchString(trimmed) {
					break // a stray directive line does not count as a note
				}
				noteChars += len(strings.TrimSpace(strings.TrimPrefix(trimmed, "--")))
			}

			if noteChars < minNoteChars {
				violations = append(violations, fmt.Sprintf(
					"%s: Down block drops a table/column but has no loss note directly above \"-- +goose Down\" (found %d comment chars there, want >= %d)",
					entry.Name(), noteChars, minNoteChars,
				))
			}
		}
	}

	if len(violations) > 0 {
		sort.Strings(violations)
		t.Fatalf("migrations with a destructive Down block but no loss note (see CONTRIBUTING.md \"goose down is unsupported\"):\n%s",
			strings.Join(violations, "\n"))
	}
}
