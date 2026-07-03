// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package cover

import (
	"context"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// TestGoToLiftPercentageWireFields drives GoToLiftPercentage with the
// exact payload shape the bridge's commandFieldsReader produces for a
// command with no typed decoder: a context-tag-keyed map[uint8]any whose
// unsigned integer values land as uint64 (see decodeGenericTagMap in
// internal/north/matter/bridge/fields_reader.go). Tag 0 is
// LiftPercent100thsValue. The prior extractor only accepted a bare
// uint16 or a string-keyed map, so every real Apple/Google
// "blind to N %" reached the server as an error.
func TestGoToLiftPercentageWireFields(t *testing.T) {
	w := &stubWriter{}
	c, _, _ := newRig(t, "HmIP-BROLL:3", w, custom.CoverCapabilities{})
	srv := c.MatterClusterServers()[0]

	fields := map[uint8]any{0: uint64(7500)} // Matter 7500 = "75 % closed"
	if _, err := srv.MatterInvoke(context.Background(), 0x05, fields, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("GoToLiftPercentage wire-shape err: %v", err)
	}
	if w.last.(float64) != 0.25 {
		t.Fatalf("Matter 7500 → HM %v, want 0.25", w.last)
	}
}
