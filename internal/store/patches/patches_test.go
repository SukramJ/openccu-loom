// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package patches

import (
	"encoding/json"
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

func TestFwiCodeIDMaxPatch(t *testing.T) {
	r := NewRegistry()
	ps := hmproto.Paramset{
		"CODE_ID": hmproto.ParameterData{
			Type: hmenum.ParameterTypeInteger,
			Min:  json.RawMessage(`1`),
			Max:  json.RawMessage(`21`),
		},
	}
	// Channel 0 is where the CCU exposes CODE_ID on the HmIP-FWI; the patch is
	// channel-scoped, so it only applies through the channel-aware ingestion path.
	changes := r.ApplyParamset("HmIP-FWI", "VCU4820995:0", hmenum.ParamsetKeyValues, ps)
	if changes == 0 {
		t.Fatal("expected CODE_ID patch to apply on channel 0")
	}
	if got := string(ps["CODE_ID"].Max); got != "31" {
		t.Fatalf("MAX=%s, want 31", got)
	}
	if got := string(ps["CODE_ID"].Min); got != "1" {
		t.Fatalf("MIN=%s, want 1 (untouched)", got)
	}
}

func TestFwiCodeIDPatchSkippedOnOtherChannel(t *testing.T) {
	r := NewRegistry()
	ps := hmproto.Paramset{
		"CODE_ID": hmproto.ParameterData{Type: hmenum.ParameterTypeInteger, Max: json.RawMessage(`21`)},
	}
	if changes := r.ApplyParamset("HmIP-FWI", "VCU4820995:1", hmenum.ParamsetKeyValues, ps); changes != 0 {
		t.Fatalf("patch must not apply off channel 0, got %d changes", changes)
	}
	if got := string(ps["CODE_ID"].Max); got != "21" {
		t.Fatalf("MAX=%s, want 21 (unchanged)", got)
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
