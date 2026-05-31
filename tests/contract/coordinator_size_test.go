// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package contract

import (
	"bufio"
	"os"
	"path/filepath"
	"testing"
)

// TestCoordinatorMinimumLOC is an early-warning canary for the parity
// audit's most consequential observation: every Go coordinator is much
// Smaller than its Python counterpart. The
// (see) sits at:
//
// - Cache ~300 LOC
// - Configuration ~500 LOC
// - ConnectionRecovery ~1700 LOC
// - Device ~1500 LOC
// - Event ~500 LOC
// - Hub ~600 LOC
// - Link ~380 LOC
//
// Mapping those 1:1 to Go is unrealistic — the language is more
// concise — but a coordinator dropping below the floor below means we
// either accidentally deleted behaviour or never ported it. The floors
// are chosen to **fail loudly** when a coordinator regresses, not to
// match the Python LOC.
//
// When a coordinator legitimately gains depth (e.g. P0-2 expands
// connection_recovery.go), bump its floor. When a coordinator legit-
// imately shrinks (a major refactor lifts logic into a sub-package),
// move the floor *and* note it in “ §6.2.
func TestCoordinatorMinimumLOC(t *testing.T) {
	t.Parallel()
	// Floors are set ~10 % below the current non-blank/non-comment
	// LOC count. Bump after a coordinator legitimately gains depth.
	floors := map[string]int{
		"cache.go":               40,
		"client.go":              60,
		"configuration.go":       130,
		"connection_recovery.go": 280,
		"device.go":              80,
		"event.go":               120,
		"hub.go":                 140,
		"link.go":                115,
		"reconciler.go":          95,
		"recovery_stages.go":     50,
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
