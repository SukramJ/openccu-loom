// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package patches

import (
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
)

// ---------------------------------------------------------------------------
// Patch.Ticket field
// ---------------------------------------------------------------------------

func TestPatchTicketField(t *testing.T) {
	t.Parallel()
	p := Patch{
		Model:     "HM-CC-RT-DN",
		Parameter: hmenum.ParameterSetTemperature,
		Reason:    "test reason",
		Ticket:    "aiohomematic#9999",
		Apply:     func(_ *hmproto.ParameterData) bool { return false },
	}
	if p.Ticket != "aiohomematic#9999" {
		t.Errorf("Ticket=%q want aiohomematic#9999", p.Ticket)
	}
}

func TestPatchTicketFieldOptional(t *testing.T) {
	t.Parallel()
	// Ticket is optional; empty string must not cause errors.
	p := Patch{
		Model:     "HM-CC-RT-DN",
		Parameter: hmenum.ParameterSetTemperature,
		Apply:     func(_ *hmproto.ParameterData) bool { return false },
	}
	if p.Ticket != "" {
		t.Errorf("default Ticket=%q want empty string", p.Ticket)
	}
}

// ---------------------------------------------------------------------------
// Registry.HasPatches
// ---------------------------------------------------------------------------

func TestRegistryHasPatchesEmpty(t *testing.T) {
	t.Parallel()
	r := &Registry{}
	if r.HasPatches() {
		t.Error("HasPatches on empty registry must return false")
	}
}

func TestRegistryHasPatchesAfterRegister(t *testing.T) {
	t.Parallel()
	r := &Registry{}
	r.Register(Patch{
		Model:     "Any",
		Parameter: hmenum.ParameterLevel,
		Apply:     func(_ *hmproto.ParameterData) bool { return true },
	})
	if !r.HasPatches() {
		t.Error("HasPatches must return true after Register")
	}
}

func TestRegistryHasPatchesBuiltIn(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	if !r.HasPatches() {
		t.Error("NewRegistry has built-in patches; HasPatches must return true")
	}
}
