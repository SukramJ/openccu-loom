// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

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
		{"DoorLock/LockDoor", 0x0101, 0x00, true},
		{"DoorLock/UnlockDoor", 0x0101, 0x01, true},
		{"DoorLock/UnboltDoor", 0x0101, 0x27, true},
		{"DoorLock/UnknownCommand", 0x0101, 0x02, false},
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
// against the matter.js cluster element files for every cluster currently
// listed in timed.go. If matter.js adds, removes, or changes the timed
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
	elementsDir := filepath.Join(repoRoot, "..", "matter.js", "packages", "model", "src", "standard", "elements")

	cases := []struct {
		name      string
		clusterID uint32
		file      string
		want      map[uint32]struct{}
		idRange   uint32 // exclusive upper bound scanned for typo/false-positive detection
		// fullCoverage is true when want equals the cluster's ENTIRE
		// matter.js timed-command set (AdministratorCommissioning: every
		// command is "A T"). It is false for clusters where the bridge only
		// implements a subset of the cluster's commands — DoorLock ships
		// LockDoor/UnlockDoor/UnboltDoor only; matter.js also marks
		// SetUser/ClearUser/SetCredential/ClearCredential/Aliro commands as
		// timed, but those are not yet exposed by the bridge so they are
		// intentionally absent from timedInvokePaths.
		fullCoverage bool
	}{
		{
			name:         "AdministratorCommissioning",
			clusterID:    0x003C,
			file:         "administrator-commissioning.element.ts",
			want:         map[uint32]struct{}{0x0: {}, 0x1: {}, 0x2: {}},
			idRange:      0xA,
			fullCoverage: true,
		},
		{
			name:      "DoorLock",
			clusterID: 0x0101,
			file:      "door-lock-cluster.element.ts",
			want:      map[uint32]struct{}{0x00: {}, 0x01: {}, 0x27: {}},
			idRange:   0x30,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			elementPath := filepath.Join(elementsDir, tc.file)
			if _, err := os.Stat(elementPath); err != nil {
				t.Skip("matter.js checkout not present")
			}

			timedIDs := timedCommandIDsFromElementFile(t, elementPath)

			for id := range tc.want {
				if _, ok := timedIDs[id]; !ok {
					t.Errorf("matter.js marks command 0x%X as timed but it is missing from the parsed set %v", id, timedIDs)
				}
			}
			if tc.fullCoverage && len(timedIDs) != len(tc.want) {
				t.Fatalf("matter.js %s timed command IDs = %v, want %v", tc.name, timedIDs, tc.want)
			}

			// Cross-check against IsTimedInvoke across an ID range so an
			// erroneous extra entry in timedInvokePaths (a typo'd ID not in
			// tc.want) is caught too.
			for id := range tc.idRange {
				_, wantTimed := tc.want[id]
				gotTimed := schema.IsTimedInvoke(tc.clusterID, id)
				if gotTimed != wantTimed {
					t.Errorf("IsTimedInvoke(0x%04X, 0x%X) = %v, want %v (matter.js parity)", tc.clusterID, id, gotTimed, wantTimed)
				}
			}
		})
	}
}
