// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package main

import (
	"context"
	"log/slog"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/internal/store/visibility"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// seedUnIgnorableDevice plants a device carrying one VALUES data point on
// the named central's model registry, and returns the data point so the
// caller can read its un-ignore mark.
func seedUnIgnorableDevice(t *testing.T, reg *central.Registry, centralName, address, model string) *generic.Switch {
	t.Helper()
	unit, ok := reg.Get(centralName)
	if !ok || unit == nil || unit.ModelRegistry == nil {
		t.Fatalf("central %q has no model registry", centralName)
	}
	d := device.New(device.Config{InterfaceID: "iface", Address: address, Model: model})
	ch := d.AddChannel(address+":1", 1, "SWITCH_VIRTUAL_RECEIVER", hmenum.ParamsetKeyValues)
	dp := generic.NewSwitch(generic.Spec{
		Key: hmtypes.DataPointKey{
			InterfaceID:    "iface",
			ChannelAddress: ch.Address,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      "SECTION",
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeBool,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite,
		},
	})
	ch.Put(dp)
	unit.ModelRegistry.Put(d)
	return dp
}

// TestUnIgnorePatternsApplyAcrossTheFleet measures the scope of an
// un-ignore pattern, which the storage shape does not reveal.
//
// The patterns are stored per central and the SPA edits one central's
// list at a time, so both the table and the editor read as if the effect
// were per central. It is not: [applyVisibilityUnIgnore] unions every
// central's patterns into the one shared visibility decider and then
// re-applies the marks across every device of every central. Nor could it
// be otherwise as written — a pattern names a model, a channel type and a
// parameter, none of which identify a CCU.
//
// This is the measurement behind that claim, and it is written as a guard
// because the claim now sits in documentation an operator reads: the
// config field, the store, the concept document and a line in the
// visibility editor. Changing the scope is an architectural decision — it
// would need a per-central decider, and it would silently narrow every
// rule an operator has already saved — so the honest move for now is to
// state the behaviour and pin it.
func TestUnIgnorePatternsApplyAcrossTheFleet(t *testing.T) {
	t.Parallel()

	cfg := config.Default()
	cfg.Centrals = []config.CentralConfig{{Name: "ccu-01"}, {Name: "ccu-02"}}
	store := buildVisibilityStore(t)
	visReg := visibility.NewRegistry()
	reg := buildTestRegistry(t, "ccu-01", "ccu-02")
	logger := slog.New(slog.DiscardHandler)

	// Both centrals host a device of the same model. Only ccu-01 gets the
	// pattern, and it is written in the form the parser accepts:
	// PARAMETER:PARAMSET@MODEL:CHANNEL, with an empty channel meaning any.
	onOwnCentral := seedUnIgnorableDevice(t, reg, "ccu-01", "PSM0001", "HmIP-PSM")
	onOtherCentral := seedUnIgnorableDevice(t, reg, "ccu-02", "PSM0002", "HmIP-PSM")

	ctx := context.Background()
	if err := store.Replace(ctx, "ccu-01", []string{"SECTION:VALUES@HmIP-PSM:"}, "test"); err != nil {
		t.Fatalf("Replace: %v", err)
	}

	if n := applyVisibilityUnIgnore(ctx, cfg, reg, store, visReg, logger); n != 1 {
		t.Fatalf("expected exactly one central to carry patterns, got %d", n)
	}

	// The central that owns the pattern: the premise. Without this the
	// assertion below would pass on an apply that did nothing at all.
	if !onOwnCentral.IsUnIgnored() {
		t.Fatal("the pattern did not take effect on its own central; the rest of this test would measure nothing")
	}
	if !onOtherCentral.IsUnIgnored() {
		t.Error("a pattern stored for ccu-01 did not reach ccu-02.\n" +
			"If that is now intended, the scope became per-central and four places say otherwise:\n" +
			"  config.VisibilityConfig.UnIgnore, sqlite.VisibilityUnIgnoreStore,\n" +
			"  notes/concepts/ui/unignore-concept.md, and the unignore.fleet_scope\n" +
			"  string rendered in the visibility editor.")
	}
}
