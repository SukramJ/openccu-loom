// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package light

import (
	"context"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/internal/north/matter/cluster/wire"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// TestLevelStepUpWireFieldsRaisesBrightness drives Step (0x02) with the
// exact payload shape the bridge's commandFieldsReader produces for a
// command with no typed decoder: a context-tag-keyed map[uint8]any whose
// unsigned integer values land as uint64 (see decodeGenericTagMap in
// internal/north/matter/bridge/fields_reader.go). Tag 0 is StepMode, tag
// 1 is StepSize. The prior extractor only accepted a string-keyed map,
// so every real Apple/Google "brighten by N" reached the server as an
// error. The magnitude matches TestLevelInvokeStepUpAndDown's
// string-keyed sibling: (127+20)/254 ≈ 0.579.
func TestLevelStepUpWireFieldsRaisesBrightness(t *testing.T) {
	w := &stubWriter{}
	l, _ := newLightRig(t, "HmIP-BDT:4", w, custom.LightCapabilities{Dimmable: true})
	l.OnLevel(0.5) // current ≈ 127/254

	srv := levelServer(t, l)
	fields := map[uint8]any{0: uint64(wire.LevelStepModeUp), 1: uint64(20)}
	if _, err := srv.MatterInvoke(context.Background(), 0x02, fields, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("Step up wire-shape err: %v", err)
	}
	if w.last < 0.56 || w.last > 0.60 {
		t.Fatalf("Step up (wire map) wrote %v, want ~0.579", w.last)
	}
}

// TestLevelStepWithOnOffDownWireFieldsLowersBrightness mirrors the raise
// case for StepWithOnOff (0x06) with StepMode=Down (tag 0 = 1). Magnitude:
// (127-20)/254 ≈ 0.421.
func TestLevelStepWithOnOffDownWireFieldsLowersBrightness(t *testing.T) {
	w := &stubWriter{}
	l, _ := newLightRig(t, "HmIP-BDT:4", w, custom.LightCapabilities{Dimmable: true})
	l.OnLevel(0.5) // current ≈ 127/254

	srv := levelServer(t, l)
	fields := map[uint8]any{0: uint64(wire.LevelStepModeDown), 1: uint64(20)}
	if _, err := srv.MatterInvoke(context.Background(), 0x06, fields, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("StepWithOnOff down wire-shape err: %v", err)
	}
	if w.last < 0.40 || w.last > 0.44 {
		t.Fatalf("Step down (wire map) wrote %v, want ~0.421", w.last)
	}
}
