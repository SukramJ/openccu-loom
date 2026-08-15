// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package contract

import (
	"bufio"
	"os"
	"path/filepath"
	"testing"
)

// TestCoordinatorMinimumLOC is a gutting tripwire for the coordinator
// package: it fails when a coordinator loses most of its body, which is what
// "we deleted behaviour and nothing noticed" looks like from the outside.
//
// It is deliberately coarse and nothing more. The floors sit near 60 % of the
// current non-blank/non-comment line count, so ordinary work — extracting a
// helper, tightening a switch — never trips it, while a file collapsing to a
// stub does. A tighter band would fail on every honest refactor and be
// ratcheted into meaninglessness within a release; the fine-grained coverage
// lives in the coordinators' own unit tests and in the wiring pins under
// tests/contract/wiring_pins/.
//
// Keep the floors in that band. When a coordinator legitimately gains depth,
// raise its floor; when a refactor legitimately lifts logic into a
// sub-package, lower it in the same change that moves the code — and say so in
// the commit message, because a floor lowered on its own is indistinguishable
// from the regression this test exists to catch.
func TestCoordinatorMinimumLOC(t *testing.T) {
	t.Parallel()
	floors := map[string]int{
		"cache.go":               200,
		"client.go":              170,
		"configuration.go":       120,
		"connection_recovery.go": 500,
		"device.go":              550,
		"event.go":               215,
		"hub.go":                 360,
		"link.go":                120,
		"reconciler.go":          125,
		"recovery_stages.go":     45,
	}
	dir := coordinatorsDir(t)
	for name, floor := range floors {
		got := nonBlankLOC(t, filepath.Join(dir, name))
		if got < floor {
			t.Errorf("%s shrank below floor: %d LOC (floor %d). "+
				"If this is intentional, update the floor value.", name, got, floor)
		}
	}
}

// TestCoordinatorSetIsStable lists every coordinator file we expect
// to exist. New coordinators must be added to floors above; deletions
// trigger a clear failure here so the audit table is updated in lock-
// step.
func TestCoordinatorSetIsStable(t *testing.T) {
	t.Parallel()
	want := map[string]struct{}{
		"cache.go":               {},
		"client.go":              {},
		"configuration.go":       {},
		"connection_recovery.go": {},
		"device.go":              {},
		"doc.go":                 {},
		"event.go":               {},
		"hub.go":                 {},
		"hub_refresh.go":         {},
		"link.go":                {},
		"reconciler.go":          {},
		"recovery_stages.go":     {},
	}
	dir := coordinatorsDir(t)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read coordinators dir: %v", err)
	}
	got := map[string]struct{}{}
	for _, e := range entries {
		if e.IsDir() || !filenameLooksLikeProductionCode(e.Name()) {
			continue
		}
		got[e.Name()] = struct{}{}
	}
	for name := range want {
		if _, ok := got[name]; !ok {
			t.Errorf("expected coordinator %q is missing — update the floor map", name)
		}
	}
	for name := range got {
		if _, ok := want[name]; !ok {
			t.Errorf("new coordinator %q appeared — add it to the floor map", name)
		}
	}
}

func filenameLooksLikeProductionCode(name string) bool {
	if filepath.Ext(name) != ".go" {
		return false
	}
	if len(name) >= 8 && name[len(name)-8:] == "_test.go" {
		return false
	}
	return true
}

func coordinatorsDir(t *testing.T) string {
	t.Helper()
	// tests/contract → repo root → internal/central/coordinators.
	return filepath.Join("..", "..", "internal", "central", "coordinators")
}

func nonBlankLOC(t *testing.T, path string) int {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer func() { _ = f.Close() }()
	count := 0
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		// Skip blank lines and pure-comment lines so we count actual
		// behaviour density, not boilerplate.
		trimmed := trimSpaceASCII(line)
		if len(trimmed) == 0 {
			continue
		}
		if len(trimmed) >= 2 && trimmed[0] == '/' && trimmed[1] == '/' {
			continue
		}
		count++
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan %s: %v", path, err)
	}
	return count
}

func trimSpaceASCII(b []byte) []byte {
	for len(b) > 0 && (b[0] == ' ' || b[0] == '\t') {
		b = b[1:]
	}
	for len(b) > 0 && (b[len(b)-1] == ' ' || b[len(b)-1] == '\t') {
		b = b[:len(b)-1]
	}
	return b
}
