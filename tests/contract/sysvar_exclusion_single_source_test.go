// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package contract

// sysvar_exclusion_single_source_test.go pins that the rule deciding which
// CCU system variables never enter the hub model has exactly one definition.
//
// It was duplicated once before — an exported, tested copy in the model that
// no production path called, and a private copy in the fetch adapter that
// every path called and that carried an extra branch the model lacked. The
// divergence was invisible because each copy was asserted against its own
// literals. This guard is source-level on purpose: it fails when a second
// definition appears anywhere in internal/ or pkg/, which no behavioural
// test can see.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// sysvarExclusionRuleHome is the single file allowed to spell the rule out.
const sysvarExclusionRuleHome = "internal/model/hub/sysvar.go"

// sysvarExclusionMarkerLiterals are the quoted name substrings that only the
// rule's home may carry. The fixed ISE IDs ("40"/"41") are deliberately not
// scanned for: they are two-character strings that occur legitimately all
// over the tree, so a scan for them would report noise rather than a second
// rule.
var sysvarExclusionMarkerLiterals = []string{`"OldVal"`, `"pcCCUID"`}

// TestSysvarExclusionRuleHasOneDefinition asserts that the sysvar-exclusion
// marker literals appear in exactly one non-test source file, the rule's home
// in internal/model/hub. A second spelling anywhere under internal/, pkg/ or
// cmd/ fails the guard.
func TestSysvarExclusionRuleHasOneDefinition(t *testing.T) {
	t.Parallel()
	root := repoRoot(t)
	for _, tree := range []string{"internal", "pkg", "cmd"} {
		base := filepath.Join(root, tree)
		err := filepath.WalkDir(base, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			rel, relErr := filepath.Rel(root, path)
			if relErr != nil {
				return relErr
			}
			if filepath.ToSlash(rel) == sysvarExclusionRuleHome {
				return nil
			}
			raw, readErr := os.ReadFile(path) //nolint:gosec // walked repo source file
			if readErr != nil {
				return readErr
			}
			src := string(raw)
			for _, lit := range sysvarExclusionMarkerLiterals {
				if strings.Contains(src, lit) {
					t.Errorf("%s spells the sysvar-exclusion marker %s; the rule lives only in %s "+
						"(call hub.IsExcludedSysvar instead of restating it)", filepath.ToSlash(rel), lit, sysvarExclusionRuleHome)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", tree, err)
		}
	}
}
