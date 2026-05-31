// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package handlers

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestSuppressServiceMessageNoUIConsumerByDesign documents the deliberate
// scope decision: a `HubCoordinator.SuppressServiceMessage` coordinator
// exists in openccu-loom, but there is **no REST/WS endpoint** that
// calls it.
//
// Rationale:
//
//   - The method is exposed as an HA service, not as a WS command.
//     The openccu-loom SPA uses WS for events; service calls go
//     through REST.
//   - The openccu-loom UI (Svelte 5 SPA + HTMX fallback) currently has
//     no call path for this method, so the endpoint remains out of
//     scope.
//
// When a future UI workflow needs this method, this test should fail
// (triggering endpoint implementation) rather than silently drifting. If
// someone extends incidents.go or a similar file to call the method, this
// test should be replaced by a positive consumer test or deleted.
func TestSuppressServiceMessageNoUIConsumerByDesign(t *testing.T) {
	t.Parallel()

	_, thisFile, _, _ := runtime.Caller(0)
	dir := filepath.Dir(thisFile)

	// Walk all *.go files in handlers/ and confirm none of them
	// invokes Hub-Coordinator's SuppressServiceMessage. If a future
	// commit adds such a call, the test fails — that is the cue
	// to update the test once the feature is implemented.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("readdir handlers/: %v", err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") {
			continue
		}
		if strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		body, err := os.ReadFile(path) //nolint:gosec // fixed test path
		if err != nil {
			continue
		}
		if strings.Contains(string(body), "SuppressServiceMessage(") {
			t.Errorf("REST handler %s now invokes SuppressServiceMessage —"+
				" scope changed from 'out-of-scope' to 'implemented';"+
				" replace this test with a positive consumer test.", entry.Name())
		}
	}
}
