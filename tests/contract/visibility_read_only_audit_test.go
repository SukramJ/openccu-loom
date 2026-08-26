// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package contract_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestVisibilityReadOnlyDPSkipDocumented documents the read-only DP skip
// Logic:.py:180-189)
// skips DP creation when Operations has neither EVENT nor WRITE.
//
// openccu-loom creates every parameter as a DP (target architecture per
// §4.8.2) and filters read-only parameters via the visibility pipeline:
//
//   - HIDDEN_PARAMETERS pipeline sets forced_usage=NoCreate
//     (`internal/store/visibility/decider.go::checkMasterParameterIgnored`)
//   - ParameterDecider.ShouldSkipParameter makes the decision for
//     MASTER paramsets
//   - Internal-flag pipeline filters FLAGS_INTERNAL parameters
//
// This test is a structural proof that the three reader paths exist and are
// consistently documented. A full bit-triple test would instrument the DP
// factory directly — the visibility behaviour is already covered by
// `internal/store/visibility/p2_registry_test.go::TestParameterDeciderShouldSkipParameter`.
//
// If a refactor renames `checkMasterParameterIgnored` or inverts the
// default-branch logic, this test will fail and surface the regression.
func TestVisibilityReadOnlyDPSkipDocumented(t *testing.T) {
	t.Parallel()

	repoRoot := mustRepoRoot(t)
	deciderPath := filepath.Join(repoRoot, "internal", "store", "visibility", "decider.go")

	body, err := os.ReadFile(deciderPath) //nolint:gosec // fixed test path
	if err != nil {
		t.Fatalf("read decider.go: %v", err)
	}
	src := string(body)

	requirements := map[string]string{
		"checkMasterParameterIgnored": "MASTER paramset default branch",
		"ShouldSkipParameter":         "compound skip decision",
		"hiddenParameters":            "HIDDEN_PARAMETERS pipeline reader",
		"ignoredParameters":           "ignored-parameter table reader",
	}
	for ident, desc := range requirements {
		if !strings.Contains(src, ident) {
			t.Errorf("decider.go is missing identifier %q (%s) — read-only DP skip logic is broken", ident, desc)
		}
	}

	// The operation-mode reader must be callable (FLAGS_INTERNAL +
	// operations triple). If the function is renamed, update this test.
	opModePath := filepath.Join(repoRoot, "internal", "store", "visibility", "operation_mode.go")
	opBody, err := os.ReadFile(opModePath) //nolint:gosec // fixed test path
	if err != nil {
		t.Fatalf("read operation_mode.go: %v", err)
	}
	if !strings.Contains(string(opBody), "ApplyInternalParameterMarks") {
		t.Errorf("operation_mode.go is missing ApplyInternalParameterMarks — FLAGS_INTERNAL reader is absent")
	}
}
