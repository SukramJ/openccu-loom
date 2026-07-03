// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package light

import (
	"context"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// TestParityMatterJS_LightOnOffDataVersionBumpsOnWrite verifies that a
// successful OnOff attribute write via lightOnOffServer increments the
// shared Light.MatterDataVersion. Controllers rely on this counter for
// DataVersionFilter evaluation.
func TestParityMatterJS_LightOnOffDataVersionBumpsOnWrite(t *testing.T) {
	t.Parallel()
	l, _ := newLightRig(t, "HmIP-BDT:4", &stubWriter{}, custom.LightCapabilities{Dimmable: true})
	before := l.MatterDataVersion()

	srv := onOffServer(t, l)
	if err := srv.MatterWrite(context.Background(), matterAttrOnOffOnOff, true, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("MatterWrite(OnOff): %v", err)
	}
	if after := l.MatterDataVersion(); after <= before {
		t.Fatalf("MatterDataVersion did not bump after OnOff write: before=%d after=%d", before, after)
	}
}

// TestParityMatterJS_LightOnOffDataVersionBumpsOnInvoke verifies that a
// successful OnOff Invoke (On command) via lightOnOffServer increments
// MatterDataVersion.
func TestParityMatterJS_LightOnOffDataVersionBumpsOnInvoke(t *testing.T) {
	t.Parallel()
	l, _ := newLightRig(t, "HmIP-BDT:4", &stubWriter{}, custom.LightCapabilities{Dimmable: true})
	before := l.MatterDataVersion()

	srv := onOffServer(t, l)
	if _, err := srv.MatterInvoke(context.Background(), matterCmdOn, nil, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("MatterInvoke(On): %v", err)
	}
	if after := l.MatterDataVersion(); after <= before {
		t.Fatalf("MatterDataVersion did not bump after OnOff invoke On: before=%d after=%d", before, after)
	}
}

// TestParityMatterJS_LightLevelDataVersionBumpsOnWrite verifies that a
// successful CurrentLevel write via lightLevelServer increments the
// shared MatterDataVersion.
func TestParityMatterJS_LightLevelDataVersionBumpsOnWrite(t *testing.T) {
	t.Parallel()
	l, _ := newLightRig(t, "HmIP-BDT:4", &stubWriter{}, custom.LightCapabilities{Dimmable: true})
	before := l.MatterDataVersion()

	srv := levelServer(t, l)
	if err := srv.MatterWrite(context.Background(), matterAttrLevelCurrent, uint8(127), hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("MatterWrite(CurrentLevel): %v", err)
	}
	if after := l.MatterDataVersion(); after <= before {
		t.Fatalf("MatterDataVersion did not bump after Level write: before=%d after=%d", before, after)
	}
}

// TestParityMatterJS_LightLevelDataVersionBumpsOnInvoke verifies that a
// successful MoveToLevel invoke via lightLevelServer increments
// MatterDataVersion. The light is primed on via l.OnLevel first: plain
// MoveToLevel (0x00) is gated on the effective ExecuteIfOff option while
// off (matter.js LevelControlServer.ts:596), and a gated no-op must not
// bump the version — see TestGatedMoveToLevelDoesNotBumpDataVersion.
func TestParityMatterJS_LightLevelDataVersionBumpsOnInvoke(t *testing.T) {
	t.Parallel()
	l, _ := newLightRig(t, "HmIP-BDT:4", &stubWriter{}, custom.LightCapabilities{Dimmable: true})
	l.OnLevel(0.5) // light is on
	before := l.MatterDataVersion()

	srv := levelServer(t, l)
	if _, err := srv.MatterInvoke(context.Background(), matterCmdMoveToLevel, uint8(190), hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("MatterInvoke(MoveToLevel): %v", err)
	}
	if after := l.MatterDataVersion(); after <= before {
		t.Fatalf("MatterDataVersion did not bump after Level invoke: before=%d after=%d", before, after)
	}
}

// TestParityMatterJS_LightDataVersionMonotonicallyRises verifies that
// alternating writes to OnOff and LevelControl each increment the shared
// counter strictly.
func TestParityMatterJS_LightDataVersionMonotonicallyRises(t *testing.T) {
	t.Parallel()
	l, _ := newLightRig(t, "HmIP-BDT:4", &stubWriter{}, custom.LightCapabilities{Dimmable: true})
	oo := onOffServer(t, l)
	lv := levelServer(t, l)

	ops := []func() error{
		func() error {
			return oo.MatterWrite(context.Background(), matterAttrOnOffOnOff, true, hmenum.CommandPriorityHigh)
		},
		func() error {
			return lv.MatterWrite(context.Background(), matterAttrLevelCurrent, uint8(100), hmenum.CommandPriorityHigh)
		},
		func() error {
			return oo.MatterWrite(context.Background(), matterAttrOnOffOnOff, false, hmenum.CommandPriorityHigh)
		},
	}
	for i, op := range ops {
		prev := l.MatterDataVersion()
		if err := op(); err != nil {
			t.Fatalf("op %d: %v", i, err)
		}
		if next := l.MatterDataVersion(); next <= prev {
			t.Fatalf("op %d: DataVersion not monotonically rising: prev=%d next=%d", i, prev, next)
		}
	}
}

// TestParityMatterJS_LightDataVersionStableOnRead verifies that
// MatterRead on either cluster server does not alter MatterDataVersion.
func TestParityMatterJS_LightDataVersionStableOnRead(t *testing.T) {
	t.Parallel()
	l, _ := newLightRig(t, "HmIP-BDT:4", &stubWriter{}, custom.LightCapabilities{Dimmable: true})
	before := l.MatterDataVersion()

	oo := onOffServer(t, l)
	lv := levelServer(t, l)
	oo.MatterRead(matterAttrOnOffOnOff)
	oo.MatterRead(matterAttrClusterRevision)
	lv.MatterRead(matterAttrLevelCurrent)
	lv.MatterRead(matterAttrFeatureMap)

	if after := l.MatterDataVersion(); after != before {
		t.Fatalf("MatterRead bumped DataVersion: before=%d after=%d", before, after)
	}
}

// TestParityMatterJS_LightDataVersionStableOnFailedWrite verifies that
// a write to an unsupported attribute ID does not increment
// MatterDataVersion.
func TestParityMatterJS_LightDataVersionStableOnFailedWrite(t *testing.T) {
	t.Parallel()
	l, _ := newLightRig(t, "HmIP-BDT:4", &stubWriter{}, custom.LightCapabilities{Dimmable: true})
	before := l.MatterDataVersion()

	oo := onOffServer(t, l)
	_ = oo.MatterWrite(context.Background(), 0x4001, true, hmenum.CommandPriorityHigh)
	lv := levelServer(t, l)
	_ = lv.MatterWrite(context.Background(), 0x4001, uint8(0), hmenum.CommandPriorityHigh)

	if after := l.MatterDataVersion(); after != before {
		t.Fatalf("failed writes bumped DataVersion: before=%d after=%d", before, after)
	}
}
