// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package ws

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestMatchActiveProfileHasUIConsumer verifies that
// `MasterProfileStore.MatchActiveProfile` remains bound to a real UI
// consumer (REST handler or WS command). Without a consumer the method
// would be dead code.
//
// Current consumer path:
//
//   - internal/north/rest/ws/commands_extended.go
//     WS command handler calls `s.MatchActiveProfile(...)` against the
//     configured *masterprofile.Store.
//
// This test reads commands_extended.go directly and verifies the call site
// is still present. A refactor that renames or moves the consumer must
// update this test to prevent the consumer silently falling out of the UI
// path.
func TestMatchActiveProfileHasUIConsumer(t *testing.T) {
	t.Parallel()

	_, thisFile, _, _ := runtime.Caller(0)
	dir := filepath.Dir(thisFile)
	consumer := filepath.Join(dir, "commands_extended.go")

	body, err := os.ReadFile(consumer) //nolint:gosec // fixed test path
	if err != nil {
		t.Fatalf("read %s: %v", consumer, err)
	}
	if !strings.Contains(string(body), "MatchActiveProfile(") {
		t.Errorf("commands_extended.go must contain a MatchActiveProfile(...) call;"+
			" got body of %d bytes without the consumer pattern."+
			" Refactor that moved the call must update this test.", len(body))
	}
}
