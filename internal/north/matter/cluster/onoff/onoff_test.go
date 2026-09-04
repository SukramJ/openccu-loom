// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package onoff_test

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/north/matter/cluster/onoff"
	"github.com/SukramJ/openccu-loom/internal/north/matter/schema"
)

// TestRevisionComesFromTheGeneratedSnapshot pins that the revision is read
// from the generated matter.js schema rather than restated.
//
// Four projections carried their own `= 6`. A regeneration that moved the
// revision would have left all four behind, and a controller reading a
// revision the cluster does not implement is exactly the drift the project
// forbids hand-coding Matter constants for.
func TestRevisionComesFromTheGeneratedSnapshot(t *testing.T) {
	t.Parallel()

	want, ok := schema.ClusterRevisions[onoff.ClusterID]
	if !ok {
		t.Fatalf("cluster %#04x missing from the generated schema", onoff.ClusterID)
	}
	if got := onoff.Revision(); got != want {
		t.Errorf("Revision() = %d, want %d from the snapshot", got, want)
	}
}

// TestLightingSetsAreOrderedAndComplete pins the two derived lists against
// matter.js conformance: the four 0x40xx attributes are "LT"-gated, and the
// command set is Off (M) plus On/Toggle (!OFFONLY) plus the three LT
// commands.
func TestLightingSetsAreOrderedAndComplete(t *testing.T) {
	t.Parallel()

	attrs := onoff.LightingAttributes()
	wantAttrs := []uint32{0x0000, 0x4000, 0x4001, 0x4002, 0x4003}
	if len(attrs) != len(wantAttrs) {
		t.Fatalf("attributes = %#v, want %#v", attrs, wantAttrs)
	}
	for i := range attrs {
		if attrs[i] != wantAttrs[i] {
			t.Errorf("attribute %d = %#04x, want %#04x", i, attrs[i], wantAttrs[i])
		}
	}

	cmds := onoff.LightingCommands()
	wantCmds := []uint32{0x00, 0x01, 0x02, 0x40, 0x41, 0x42}
	if len(cmds) != len(wantCmds) {
		t.Fatalf("commands = %#v, want %#v", cmds, wantCmds)
	}
	for i := range cmds {
		if cmds[i] != wantCmds[i] {
			t.Errorf("command %d = %#02x, want %#02x", i, cmds[i], wantCmds[i])
		}
	}
}
