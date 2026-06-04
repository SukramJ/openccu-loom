// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package contract

import (
	"encoding/json"
	"maps"
	"reflect"
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
)

// TestNormalizeDeviceIdempotent pins the invariant that normalisation
// applied twice is the same as once. Change-detection relies on it.
func TestNormalizeDeviceIdempotent(t *testing.T) {
	d := hmproto.DeviceDescription{
		Address:   "  ABC  ",
		Type:      "HM-TEST",
		Paramsets: []string{"VALUES", "MASTER"},
		Children:  []string{"c2", "c1"},
	}
	a := hmproto.NormalizeDevice(d)
	b := hmproto.NormalizeDevice(a)
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("NormalizeDevice not idempotent")
	}
}

// TestNormalizeParameterIdempotent locks parameter normalisation.
func TestNormalizeParameterIdempotent(t *testing.T) {
	p := hmproto.ParameterData{
		Type:       hmenum.ParameterTypeFloat,
		Operations: hmenum.OperationsRead | hmenum.OperationsWrite,
		Min:        json.RawMessage("  0.0 "),
		Max:        json.RawMessage(" 100.0"),
		Unit:       "  °C ",
	}
	a := hmproto.NormalizeParameter(p)
	b := hmproto.NormalizeParameter(a)
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("NormalizeParameter not idempotent")
	}
}

// TestHashStableAcrossParamsetMapOrder locks the guarantee that
// paramset hashing is insensitive to Go map iteration order.
func TestHashStableAcrossParamsetMapOrder(t *testing.T) {
	// Populate two logically-equivalent paramsets whose insertion order
	// differs. Under Go's randomised iteration, naive serialisation
	// would flip-flop; our canonical hasher must not.
	ps1 := hmproto.Paramset{
		"LEVEL":           {Type: hmenum.ParameterTypeFloat},
		"SET_TEMPERATURE": {Type: hmenum.ParameterTypeFloat},
		"WORKING":         {Type: hmenum.ParameterTypeBool},
	}
	ps2 := hmproto.Paramset{}
	maps.Copy(ps2, ps1)

	for i := range 64 {
		h1, err := hmproto.HashParamset(ps1)
		if err != nil {
			t.Fatal(err)
		}
		h2, err := hmproto.HashParamset(ps2)
		if err != nil {
			t.Fatal(err)
		}
		if h1 != h2 {
			t.Fatalf("hash diverged on iter %d: %s vs %s", i, h1, h2)
		}
	}
}

// TestHashChangesOnMutation ensures the hash is not trivially constant
// (a failure mode that would silently disable change-detection).
func TestHashChangesOnMutation(t *testing.T) {
	base := hmproto.DeviceDescription{Address: "ABC", Firmware: "1.0"}
	mutated := base
	mutated.Firmware = "1.1"

	hBase, _ := hmproto.HashDevice(base)
	hMut, _ := hmproto.HashDevice(mutated)
	if hBase == hMut {
		t.Fatal("hash did not change after firmware bump")
	}
}
