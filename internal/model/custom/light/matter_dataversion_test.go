// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package light

import (
	"context"
	"testing"

	"thingfabric.dev/go-fabric/cluster/levelcontrol"
	"thingfabric.dev/go-fabric/cluster/onoff"
	"thingfabric.dev/go-fabric/cluster/spec"
	levelcontroldef "thingfabric.dev/go-fabric/cluster/spec/levelcontrol"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
)

// The light carries no DataVersion of its own: a bridged endpoint's
// DataVersion is the one the endpoint hosts, which the bridge advances on
// the servers' reports and the light's change notifications. What the
// kit's OnOff and LevelControl servers keep are their own counters, which
// these tests read: each advances on a change its server commits and on
// nothing else.

// TestParityMatterJS_LightOnOffDataVersionBumpsOnWrite verifies that a
// successful OnOff attribute write (OnTime) advances the OnOff server's
// data version.
func TestParityMatterJS_LightOnOffDataVersionBumpsOnWrite(t *testing.T) {
	t.Parallel()
	l, _ := newLightRig(t, "HmIP-BDT:4", &stubWriter{}, custom.LightCapabilities{Dimmable: true})
	srv := onOffServer(t, l)
	before := srv.MatterDataVersion()

	if err := srv.MatterWrite(context.Background(), onoff.AttrOnTime, uint64(5)); err != nil {
		t.Fatalf("MatterWrite(OnTime): %v", err)
	}
	if after := srv.MatterDataVersion(); after <= before {
		t.Fatalf("OnOff data version did not advance after an OnTime write: before=%d after=%d", before, after)
	}
}

// TestParityMatterJS_LightOnOffDataVersionBumpsOnInvoke verifies that a
// successful On command advances the OnOff server's data version.
func TestParityMatterJS_LightOnOffDataVersionBumpsOnInvoke(t *testing.T) {
	t.Parallel()
	l, _ := newLightRig(t, "HmIP-BDT:4", &stubWriter{}, custom.LightCapabilities{Dimmable: true})
	srv := onOffServer(t, l)
	before := srv.MatterDataVersion()

	if _, err := srv.MatterInvoke(context.Background(), onoff.CmdOn, nil); err != nil {
		t.Fatalf("MatterInvoke(On): %v", err)
	}
	if after := srv.MatterDataVersion(); after <= before {
		t.Fatalf("OnOff data version did not advance after On: before=%d after=%d", before, after)
	}
}

// TestParityMatterJS_LightLevelDataVersionBumpsOnWrite verifies that a
// successful LevelControl attribute write (OnLevel) advances the
// LevelControl server's data version.
func TestParityMatterJS_LightLevelDataVersionBumpsOnWrite(t *testing.T) {
	t.Parallel()
	l, _ := newLightRig(t, "HmIP-BDT:4", &stubWriter{}, custom.LightCapabilities{Dimmable: true})
	srv := levelServer(t, l)
	changes := levelChanges(t, srv)

	if err := srv.MatterWrite(context.Background(), levelcontrol.AttrOnLevel, uint64(127)); err != nil {
		t.Fatalf("MatterWrite(OnLevel): %v", err)
	}
	if changes() == 0 {
		t.Fatal("LevelControl committed no change for an OnLevel write")
	}
}

// TestParityMatterJS_LightLevelDataVersionBumpsOnInvoke verifies that a
// MoveToLevel commits its change once the device reports the level it
// reached, and not before: matter.js advances a version only for a commit
// that changed a value. The light is primed on via l.OnLevel first so the
// command reaches the CCU rather than ending in the ExecuteIfOff gate.
func TestParityMatterJS_LightLevelDataVersionBumpsOnInvoke(t *testing.T) {
	t.Parallel()
	l, _ := newLightRig(t, "HmIP-BDT:4", &stubWriter{}, custom.LightCapabilities{Dimmable: true})
	l.OnLevel(0.5) // light is on
	srv := levelServer(t, l)
	changes := levelChanges(t, srv)

	if _, err := srv.MatterInvoke(context.Background(), levelcontrol.CmdMoveToLevel, levelcontroldef.MoveToLevelRequest{Level: 190, TransitionTime: spec.Nullable[uint16]{Null: true}}); err != nil {
		t.Fatalf("MatterInvoke(MoveToLevel): %v", err)
	}
	l.OnLevel(190.0 / 254)
	if changes() == 0 {
		t.Fatal("LevelControl committed no change once the device reported the level MoveToLevel asked for")
	}
}

// TestParityMatterJS_LightDataVersionMonotonicallyRises verifies that
// consecutive writes to OnOff (OnTime, OffWaitTime) and LevelControl each
// advance their server's counter strictly.
func TestParityMatterJS_LightDataVersionMonotonicallyRises(t *testing.T) {
	t.Parallel()
	l, _ := newLightRig(t, "HmIP-BDT:4", &stubWriter{}, custom.LightCapabilities{Dimmable: true})
	oo := onOffServer(t, l)
	lv := levelServer(t, l)
	lvChanges := levelChanges(t, lv)
	lvVersion := func() uint32 { return uint32(lvChanges()) }

	ops := []struct {
		version func() uint32
		op      func() error
	}{
		{oo.MatterDataVersion, func() error {
			return oo.MatterWrite(context.Background(), onoff.AttrOnTime, uint64(5))
		}},
		{lvVersion, func() error {
			return lv.MatterWrite(context.Background(), levelcontrol.AttrOptions, uint64(levelcontrol.OptionExecuteIfOff))
		}},
		{oo.MatterDataVersion, func() error {
			return oo.MatterWrite(context.Background(), onoff.AttrOffWaitTime, uint64(7))
		}},
	}
	for i, c := range ops {
		prev := c.version()
		if err := c.op(); err != nil {
			t.Fatalf("op %d: %v", i, err)
		}
		if next := c.version(); next <= prev {
			t.Fatalf("op %d: data version not monotonically rising: prev=%d next=%d", i, prev, next)
		}
	}
}

// TestParityMatterJS_LightDataVersionStableOnRead verifies that a read of
// either cluster server leaves its data version alone.
func TestParityMatterJS_LightDataVersionStableOnRead(t *testing.T) {
	t.Parallel()
	l, _ := newLightRig(t, "HmIP-BDT:4", &stubWriter{}, custom.LightCapabilities{Dimmable: true})
	oo := onOffServer(t, l)
	lv := levelServer(t, l)
	lvChanges := levelChanges(t, lv)
	ooBefore := oo.MatterDataVersion()

	oo.MatterRead(onoff.AttrOnOff)
	oo.MatterRead(matterAttrClusterRevision)
	lv.MatterRead(levelcontrol.AttrCurrentLevel)
	lv.MatterRead(matterAttrFeatureMap)

	if oo.MatterDataVersion() != ooBefore || lvChanges() != 0 {
		t.Fatalf("a read changed state: OnOff version %d→%d, LevelControl changes %d",
			ooBefore, oo.MatterDataVersion(), lvChanges())
	}
}

// TestParityMatterJS_LightDataVersionStableOnFailedWrite verifies that
// refused writes — a wrongly typed OnTime, the read-only OnOff, the
// read-only CurrentLevel, an undefined Options bit — leave the data
// versions alone.
func TestParityMatterJS_LightDataVersionStableOnFailedWrite(t *testing.T) {
	t.Parallel()
	l, _ := newLightRig(t, "HmIP-BDT:4", &stubWriter{}, custom.LightCapabilities{Dimmable: true})
	oo := onOffServer(t, l)
	lv := levelServer(t, l)
	lvChanges := levelChanges(t, lv)
	ooBefore := oo.MatterDataVersion()

	if err := oo.MatterWrite(context.Background(), onoff.AttrOnTime, true); err == nil {
		t.Fatal("MatterWrite(OnTime, bool) succeeded, want a type refusal")
	}
	if err := oo.MatterWrite(context.Background(), onoff.AttrOnOff, true); err == nil {
		t.Fatal("MatterWrite(OnOff) succeeded, want a refusal — OnOff is read-only")
	}
	if err := lv.MatterWrite(context.Background(), levelcontrol.AttrCurrentLevel, uint64(100)); err == nil {
		t.Fatal("MatterWrite(CurrentLevel) succeeded, want a refusal — CurrentLevel is read-only")
	}
	if err := lv.MatterWrite(context.Background(), levelcontrol.AttrOptions, uint64(0x04)); err == nil {
		t.Fatal("MatterWrite(Options=0x04) succeeded, want a refusal")
	}
	_ = lv.MatterWrite(context.Background(), 0x4001, uint8(0))

	if oo.MatterDataVersion() != ooBefore || lvChanges() != 0 {
		t.Fatalf("failed writes changed state: OnOff version %d→%d, LevelControl changes %d",
			ooBefore, oo.MatterDataVersion(), lvChanges())
	}
}
