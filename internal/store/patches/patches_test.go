// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package patches

import (
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
)

func TestEnergyCounterUnitPatchApplied(t *testing.T) {
	r := NewRegistry()
	pd := &hmproto.ParameterData{Type: hmenum.ParameterTypeFloat}
	changes := r.ApplyTo("HM-ES-PMSw1-Pl", hmenum.ParamsetKeyValues, hmenum.Parameter("ENERGY_COUNTER"), pd)
	if changes == 0 {
		t.Fatal("expected patch")
	}
	if pd.Unit != "Wh" {
		t.Fatalf("unit=%q", pd.Unit)
	}
}

func TestPatchIdempotent(t *testing.T) {
	r := NewRegistry()
	pd := &hmproto.ParameterData{Type: hmenum.ParameterTypeFloat, Unit: "Wh"}
	changes := r.ApplyTo("HM-ES-PMSw1-Pl", hmenum.ParamsetKeyValues, hmenum.Parameter("ENERGY_COUNTER"), pd)
	if changes != 0 {
		t.Fatal("second apply must be no-op")
	}
}

func TestRGBWSaturationEventBitPatch(t *testing.T) {
	r := NewRegistry()
	pd := &hmproto.ParameterData{Type: hmenum.ParameterTypeFloat, Operations: hmenum.OperationsRead | hmenum.OperationsWrite}
	changes := r.ApplyTo("HmIP-RGBW", hmenum.ParamsetKeyValues, hmenum.ParameterSaturation, pd)
	if changes == 0 {
		t.Fatal("expected patch")
	}
	if !pd.Operations.IsEvent() {
		t.Fatalf("operations=%d", pd.Operations)
	}
}

func TestPatchRegistryRegister(t *testing.T) {
	r := NewRegistry()
	before := r.Len()
	r.Register(Patch{Parameter: hmenum.ParameterLevel, Apply: func(*hmproto.ParameterData) bool { return false }})
	if r.Len() != before+1 {
		t.Fatal("registry must grow")
	}
}
