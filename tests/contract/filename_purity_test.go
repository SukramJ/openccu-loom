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

// TestFilenamePurity walks every .go file under internal/, pkg/, cmd/,
// and tests/ and fails when a filename carries an internal audit /
// migration-phase marker that has no place in the long-term filename
// vocabulary.
//
// Forbidden patterns:
//
//   - `_wave\d+_` / `wave\d+_` — wave-of-changes marker
//   - `_a\d+_` / `_l\d+_` — audit-area / audit-item marker
//     (note: the related _a\d+_l\d+_ combo is covered by both rules)
//   - `_parity_a\d_` — audit-area parity marker
//   - `_g\d+_` — G-code item marker (rare)
//   - `_phase\d+_` / `phase\d+_` — migration-phase marker
//
// Files that legitimately reference a number in their basename
// (`base16_test.go`, `oauth2_test.go`, …) match neither pattern because
// the regexps require either an `_a` / `_l` / `wave` / `phase` prefix
// or a numeric segment between underscores starting with one of those
// markers.
//
// The single intentional exception is the `migrations/` directory under
// `internal/store/sqlite/`, where filenames carry a goose-style numeric
// prefix (`0001_init.sql`, `0042_session_recorder.sql`, …). Those are
// data files, not Go sources, and are not visited by the walk filter
// (only .go files are scanned).
func TestFilenamePurity(t *testing.T) {
	t.Parallel()

	type rule struct {
		label string
		re    *regexp.Regexp
	}
	rules := []rule{
		{"wave-N filename marker", regexp.MustCompile(`(^|[_/])wave\d+(_|\.|$)`)},
		{"audit-area _a<N>_ marker", regexp.MustCompile(`_a\d+_`)},
		{"audit-item _l<N>_ marker", regexp.MustCompile(`_l\d+_`)},
		{"audit-area _parity_a<N>_ marker", regexp.MustCompile(`_parity_a\d+_`)},
		{"audit-item _g\\d+_ marker", regexp.MustCompile(`_g\d+_`)},
		{"phase<N> filename marker", regexp.MustCompile(`(^|[_/])phase\d+(_|\.|$)`)},
	}

	repoRoot := filepath.Join("..", "..")
	scanDirs := []string{
		filepath.Join(repoRoot, "internal"),
		filepath.Join(repoRoot, "pkg"),
		filepath.Join(repoRoot, "cmd"),
		filepath.Join(repoRoot, "tests"),
	}

	type hit struct {
		file    string
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
			base := filepath.Base(path)
			lower := strings.ToLower(base)
			for _, r := range rules {
				if r.re.MatchString(lower) {
					hits = append(hits, hit{file: path, pattern: r.label})
					break
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
	fmt.Fprintf(&sb, "filename_purity: %d file(s) carry an audit/migration-phase marker:\n\n", len(hits))
	for _, h := range hits {
		fmt.Fprintf(&sb, "  %s [%s]\n", h.file, h.pattern)
	}
	sb.WriteString("\nRename them to descriptive names. ")
	sb.WriteString("Wave/audit/phase markers belong in commit messages, not filenames.\n")
	t.Fatal(sb.String())
}
