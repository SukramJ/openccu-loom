// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package contract

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The two lines every Go file in this repository carries (ADR 0085). The
// SPDX line is what automated licence scanners read; the copyright line
// names the rights holder, which the SPDX identifier alone does not.
const (
	licenseSPDXLine      = "// SPDX-License-Identifier: Apache-2.0"
	licenseCopyrightLine = "// Copyright (C) 2026 SukramJ."
)

// TestLicenseHeaderOnEveryGoFile walks the repository and fails naming
// every .go file that does not carry both header lines ahead of its
// package clause. Before this guard the header was reviewed, not
// enforced; the relicensing (ADR 0085) rewrote every header at once and
// this is what keeps the next file from drifting back.
//
// The header is checked in the block before `package` rather than on
// lines 1 and 2: a file may legitimately open with a //go:build
// constraint, which must precede the licence comment.
func TestLicenseHeaderOnEveryGoFile(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)

	var missing []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			// .git holds no Go source; node_modules and the embedded
			// SPA build hold no Go source either; a vendor tree would
			// hold third-party source carrying its own upstream notice.
			switch d.Name() {
			case ".git", "vendor", "node_modules", "spa_dist":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		hasSPDX, hasCopyright, herr := licenseHeaderLines(path)
		if herr != nil {
			return herr
		}
		rel, rerr := filepath.Rel(root, path)
		if rerr != nil {
			rel = path
		}
		switch {
		case !hasSPDX && !hasCopyright:
			missing = append(missing, rel+": missing both header lines")
		case !hasSPDX:
			missing = append(missing, rel+": missing "+licenseSPDXLine)
		case !hasCopyright:
			missing = append(missing, rel+": missing "+licenseCopyrightLine)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}

	if len(missing) > 0 {
		t.Errorf("%d Go file(s) without a complete licence header:\n  %s",
			len(missing), strings.Join(missing, "\n  "))
	}
}

// licenseHeaderLines reports whether the comment block preceding the
// package clause contains the SPDX and copyright lines. Scanning stops at
// `package` so a later occurrence of either string — inside a string
// literal, or in a generator template — cannot satisfy the check.
func licenseHeaderLines(path string) (hasSPDX, hasCopyright bool, err error) {
	f, err := os.Open(path) //nolint:gosec // walking this repository's own tree
	if err != nil {
		return false, false, err
	}
	defer func() { _ = f.Close() }()

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if strings.HasPrefix(line, "package ") {
			break
		}
		switch line {
		case licenseSPDXLine:
			hasSPDX = true
		case licenseCopyrightLine:
			hasCopyright = true
		}
	}
	if serr := sc.Err(); serr != nil {
		return false, false, serr
	}
	return hasSPDX, hasCopyright, nil
}
