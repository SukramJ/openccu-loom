// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package schema_test

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/north/matter/schema"
)

// TestIsTimedInvoke covers the timed-required (cluster, command) pairs
// currently pinned in timed.go plus representative non-timed pairs on
// both a timed-bearing cluster (unknown command) and non-timed clusters.
func TestIsTimedInvoke(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		clusterID uint32
		commandID uint32
		want      bool
	}{
		{"AdministratorCommissioning/OpenCommissioningWindow", 0x003C, 0x0, true},
		{"AdministratorCommissioning/OpenBasicCommissioningWindow", 0x003C, 0x1, true},
		{"AdministratorCommissioning/RevokeCommissioning", 0x003C, 0x2, true},
		{"AdministratorCommissioning/UnknownCommand", 0x003C, 0x5, false},
		{"OnOff/On", 0x0006, 0x0, false},
		{"BasicInformation/Command0", 0x0028, 0x0, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := schema.IsTimedInvoke(tc.clusterID, tc.commandID); got != tc.want {
				t.Errorf("IsTimedInvoke(0x%04X, 0x%X) = %v, want %v", tc.clusterID, tc.commandID, got, tc.want)
			}
		})
	}
}

// commandAccessRe matches one CommandElement header object, capturing its
// `id:` and `access:` fields. matter.js command headers are single-line
// object literals with no nested braces (see administrator-commissioning
// .element.ts), so a non-greedy match up to the first `}` is safe.
var commandAccessRe = regexp.MustCompile(`(?s)Command\(\s*\{(.*?)\}`)

var commandIDRe = regexp.MustCompile(`\bid:\s*(0x[0-9a-fA-F]+)`)

var commandAccessFieldRe = regexp.MustCompile(`\baccess:\s*"([^"]*)"`)

// timedCommandIDsFromElementFile parses a matter.js *.element.ts cluster
// file and returns the set of command IDs whose access string carries the
// standalone "T" token (Access.Timed.Required — matter.js
// packages/model/src/aspects/Access.ts:38). The regex is deliberately
// narrow: it only needs to survive the AdministratorCommissioning element
// shape, not a general TypeScript parser.
func timedCommandIDsFromElementFile(t *testing.T, path string) map[uint32]struct{} {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	out := make(map[uint32]struct{})
	for _, block := range commandAccessRe.FindAllStringSubmatch(string(raw), -1) {
		header := block[1]
		idMatch := commandIDRe.FindStringSubmatch(header)
		if idMatch == nil {
			t.Fatalf("command block without id: %q", header)
		}
		id, err := strconv.ParseUint(idMatch[1], 0, 32)
		if err != nil {
			t.Fatalf("parse command id %q: %v", idMatch[1], err)
		}
		accessMatch := commandAccessFieldRe.FindStringSubmatch(header)
		if accessMatch == nil {
			continue // no access field → not timed-required
		}
		// Access strings look like "A T", "R V", "R W VA" — split on
		// whitespace and match the exact token "T", never a substring
		// (e.g. "VA" or a hypothetical future qualifier must not match).
		for _, tok := range strings.Fields(accessMatch[1]) {
			if tok == "T" {
				out[uint32(id)] = struct{}{}
				break
			}
		}
	}
	return out
}

// TestTimedInvokeParity pins schema.timedInvokePaths (via IsTimedInvoke)
// against the matter.js AdministratorCommissioning element file — the
// single cluster the bridge currently exposes with "T"-access commands
// (see timed.go). If matter.js adds, removes, or changes the timed
// qualifier on a command here, this test fails and timed.go must be
// extended alongside it.
//
// Skips (not fails) when no local matter.js checkout is present, matching
// the CI posture: the checkout is a developer-machine sibling, not a
// vendored dependency.
func TestTimedInvokeParity(t *testing.T) {
	t.Parallel()

	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller(0) failed")
	}
	// This file lives at internal/north/matter/schema/timed_test.go, so the
	// repo root is four directories up; matter.js is a sibling checkout one
	// level above the repo root (see CLAUDE.md "matter.js as the Matter Gold
	// Standard").
	repoRoot := filepath.Join(filepath.Dir(thisFile), "..", "..", "..", "..")
	elementPath := filepath.Join(repoRoot, "..", "matter.js", "packages", "model", "src", "standard", "elements", "administrator-commissioning.element.ts")

	if _, err := os.Stat(elementPath); err != nil {
		t.Skip("matter.js checkout not present")
	}

	timedIDs := timedCommandIDsFromElementFile(t, elementPath)

	want := map[uint32]struct{}{0x0: {}, 0x1: {}, 0x2: {}}
	if len(timedIDs) != len(want) {
		t.Fatalf("matter.js AdministratorCommissioning timed command IDs = %v, want %v", timedIDs, want)
	}
	for id := range want {
		if _, ok := timedIDs[id]; !ok {
			t.Errorf("matter.js marks command 0x%X as timed but it is missing from the parsed set %v", id, timedIDs)
		}
	}

	// Cross-check against IsTimedInvoke across a small ID range so an
	// erroneous extra entry in timedInvokePaths (a false positive not
	// present in matter.js) is caught too.
	for id := range uint32(0xA) {
		_, wantTimed := timedIDs[id]
		gotTimed := schema.IsTimedInvoke(0x003C, id)
		if gotTimed != wantTimed {
			t.Errorf("IsTimedInvoke(0x003C, 0x%X) = %v, want %v (matter.js parity)", id, gotTimed, wantTimed)
		}
	}
}
