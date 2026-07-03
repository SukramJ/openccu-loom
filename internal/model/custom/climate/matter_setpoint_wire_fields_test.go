// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package climate

import (
	"context"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// TestThermostatSetpointRaiseLowerWireFieldsRaises drives
// SetpointRaiseLower (0x00) with the exact payload shape the bridge's
// commandFieldsReader produces for a command with no typed decoder: a
// context-tag-keyed map[uint8]any whose unsigned integer values land as
// uint64 and whose signed integer values (Amount is a signed int8 on the
// wire) land as int64 — see decodeGenericTagMap in
// internal/north/matter/bridge/fields_reader.go. Tag 0 is Mode, tag 1 is
// Amount. The prior extractor only accepted a string-keyed map, so every
// real Apple/Google "raise the setpoint" reached the server as an error.
// mode=0 (Both), amount=+15 (0.1°C units, +1.5°C) matches the magnitude
// of the string-keyed sibling TestThermostatSetpointRaiseLowerCommand.
func TestThermostatSetpointRaiseLowerWireFieldsRaises(t *testing.T) {
	w := &stubWriter{}
	r := newRig(t, "HmIP-BWTH:1", KindIP, w, custom.ClimateCapabilities{
		MinTemperature: 4.5,
		MaxTemperature: 30.5,
	})
	r.setpoint.OnEvent(20.0)
	srv := findCluster(t, r.climate, 0x0201)
	fields := map[uint8]any{0: uint64(0), 1: int64(15)} // mode=Both, +1.5 °C
	_, err := srv.MatterInvoke(context.Background(), matterCmdSetpointRaiseLower, fields, hmenum.CommandPriorityHigh)
	if err != nil {
		t.Fatalf("SetpointRaiseLower wire-shape err: %v", err)
	}
	if got := w.last(); got.value.(float64) != 21.5 {
		t.Fatalf("raise (wire map) reached wire as %v, want 21.5", got.value)
	}
}

// TestThermostatSetpointRaiseLowerWireFieldsLowers mirrors the raise
// case with a negative Amount (int64(-15) = -1.5 °C).
func TestThermostatSetpointRaiseLowerWireFieldsLowers(t *testing.T) {
	w := &stubWriter{}
	r := newRig(t, "HmIP-BWTH:1", KindIP, w, custom.ClimateCapabilities{
		MinTemperature: 4.5,
		MaxTemperature: 30.5,
	})
	r.setpoint.OnEvent(20.0)
	srv := findCluster(t, r.climate, 0x0201)
	fields := map[uint8]any{0: uint64(0), 1: int64(-15)} // mode=Both, -1.5 °C
	_, err := srv.MatterInvoke(context.Background(), matterCmdSetpointRaiseLower, fields, hmenum.CommandPriorityHigh)
	if err != nil {
		t.Fatalf("SetpointRaiseLower wire-shape err: %v", err)
	}
	if got := w.last(); got.value.(float64) != 18.5 {
		t.Fatalf("lower (wire map) reached wire as %v, want 18.5", got.value)
	}
}
