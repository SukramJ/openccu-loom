// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

//go:build chiptool

package chiptool

import (
	"context"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/tests/chiptool/harness"
)

// TestReceive_BooleanStateContact exercises BooleanState (0x0045) on the
// fleet's first BooleanState source — a generic bool sensor (a shutter
// contact STATE, a SABOTAGE tamper switch, …). The cluster is read-only at
// the wire layer (measurement.BooleanStateServer.MatterWrite always returns
// errReadOnly), so the SEND direction is a negative case rather than a value
// round-trip.
//
// The injected parameter is the dp_key ResolveCCUAddress reports for the
// discovered endpoint, NOT a hard-coded "STATE": the harness maps a bridged
// endpoint back to its CCU device (via BridgedDeviceBasicInformation.
// SerialNumber) and then to that device's first BooleanState channel, which
// on a multi-BooleanState device (SABOTAGE on ch0, contact STATE on ch1) is
// the ch0 SABOTAGE row — firing a hard-coded "STATE" there hits a parameter
// the channel does not describe.
func TestReceive_BooleanStateContact(t *testing.T) {
	b := requireBridge(t)
	eps := discoverEndpointsWith(t, b, 0x0045, 1)
	if len(eps) == 0 {
		t.Skip("no BooleanState endpoint — godevccu fleet lacks a bool-sensor device")
	}
	ep := eps[0]
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	address, dpKey, ok := b.ResolveCCUAddress(ctx, t, ep, 0x0045)
	if !ok {
		t.Fatalf("could not resolve CCU address for BooleanState endpoint %d", ep)
	}

	// SEND (negative) — BooleanState is a read-only measurement cluster;
	// a controller write must always be rejected at the wire layer.
	t.Run("negative/write-state-value", func(t *testing.T) {
		out, _ := b.SharedCtl.WriteAttr(ctx, t, "booleanstate", "state-value", "1", ep)
		hex, ok := harness.WriteStatus(out)
		if !ok {
			t.Fatalf("no write status parsed from booleanstate write:\n%s", out)
		}
		if hex == "0x0" {
			t.Fatalf("booleanstate write unexpectedly succeeded (status 0x0):\n%s", out)
		}
	})

	// RECEIVE — a bool-sensor trip must reach the controller as a
	// proactive StateValue report. StateValue is non-nullable: an
	// unobserved attribute defaults to false, so injecting true only
	// after the subscription is live is the only way to prove the
	// change-notifier (rather than the subscribe's own initial read)
	// produced the report.
	t.Run("receive/state-value", func(t *testing.T) {
		out, err := harness.AwaitProactiveReport(ctx, t, b.SharedCtl,
			"booleanstate", "state-value", ep,
			func() error { return b.CCU.FireDeviceEvent(address, dpKey, true) },
			func(out string) bool {
				v, ok := harness.FindAttrBool(out, "StateValue")
				return ok && v
			},
			30*time.Second)
		if err != nil {
			t.Fatalf("await proactive StateValue=true: %v\n%s", err, out)
		}
	})
}

// TestReceive_OccupancySensing exercises OccupancySensing (0x0406) on
// HmIP-SMI (motion + illuminance). Like BooleanState, the cluster is
// read-only at the wire layer (measurement.OccupancySensingServer.
// MatterWrite always returns errReadOnly).
func TestReceive_OccupancySensing(t *testing.T) {
	b := requireBridge(t)
	eps := discoverEndpointsWith(t, b, 0x0406, 1)
	if len(eps) == 0 {
		t.Skip("no OccupancySensing endpoint — godevccu fleet lacks a motion device")
	}
	ep := eps[0]
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	address, _, ok := b.ResolveCCUAddress(ctx, t, ep, 0x0406)
	if !ok {
		t.Fatalf("could not resolve CCU address for OccupancySensing endpoint %d", ep)
	}

	// SEND (negative) — OccupancySensing is a read-only measurement
	// cluster; a controller write must always be rejected.
	t.Run("negative/write-occupancy", func(t *testing.T) {
		out, _ := b.SharedCtl.WriteAttr(ctx, t, "occupancysensing", "occupancy", "1", ep)
		hex, ok := harness.WriteStatus(out)
		if !ok {
			t.Fatalf("no write status parsed from occupancysensing write:\n%s", out)
		}
		if hex == "0x0" {
			t.Fatalf("occupancysensing write unexpectedly succeeded (status 0x0):\n%s", out)
		}
	})

	// RECEIVE — a motion trigger must reach the controller as a
	// proactive Occupancy report; bit 0 of the Occupancy bitmap is the
	// "Occupied" bit (Matter §2.7.4). Occupancy differs from
	// BooleanState.StateValue in that an unobserved source can report
	// null rather than a default false, so the assertion checks bit 0
	// only once FindAttrUint has a parsed value at all.
	t.Run("receive/occupancy", func(t *testing.T) {
		out, err := harness.AwaitProactiveReport(ctx, t, b.SharedCtl,
			"occupancysensing", "occupancy", ep,
			func() error { return b.CCU.FireDeviceEvent(address, "MOTION", true) },
			func(out string) bool {
				v, ok := harness.FindAttrUint(out, "Occupancy")
				return ok && v&1 == 1
			},
			30*time.Second)
		if err != nil {
			t.Fatalf("await proactive Occupancy bit0=1: %v\n%s", err, out)
		}
	})
}

// TestReceive_PowerSourceBattery exercises the battery flavour of
// PowerSource (0x002F) on any battery device already in the fleet
// (HmIP-SWSD / HmIP-BWTH ch0 LOWBAT). measurement.PowerSourceServer is
// read-only at the wire layer and has no commands, so SEND is a
// negative case.
func TestReceive_PowerSourceBattery(t *testing.T) {
	b := requireBridge(t)
	eps := discoverEndpointsWith(t, b, 0x002F, 1)
	if len(eps) == 0 {
		t.Skip("no PowerSource endpoint — godevccu fleet lacks a battery device")
	}
	ep := eps[0]
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	address, _, ok := b.ResolveCCUAddress(ctx, t, ep, 0x002F)
	if !ok {
		t.Fatalf("could not resolve CCU address for PowerSource endpoint %d", ep)
	}

	// SEND (negative) — PowerSource is a read-only measurement cluster;
	// a controller write must always be rejected.
	t.Run("negative/write-bat-charge-level", func(t *testing.T) {
		out, _ := b.SharedCtl.WriteAttr(ctx, t, "powersource", "bat-charge-level", "1", ep)
		hex, ok := harness.WriteStatus(out)
		if !ok {
			t.Fatalf("no write status parsed from powersource write:\n%s", out)
		}
		if hex == "0x0" {
			t.Fatalf("powersource write unexpectedly succeeded (status 0x0):\n%s", out)
		}
	})

	// RECEIVE — a LOW_BAT push must reach the controller as a proactive
	// BatChargeLevel report. BatChargeLevel is non-nullable with an OK
	// (0) default (HM has no Critical-level signal — a fully critical
	// device just disconnects), so Warning (1) is distinct from the
	// pre-state and can only arrive via the change-notifier.
	t.Run("receive/bat-charge-level", func(t *testing.T) {
		out, err := harness.AwaitProactiveReport(ctx, t, b.SharedCtl,
			"powersource", "bat-charge-level", ep,
			func() error { return b.CCU.FireDeviceEvent(address, "LOW_BAT", true) },
			func(out string) bool {
				v, ok := harness.FindAttrUint(out, "BatChargeLevel")
				return ok && v == 1 // Warning
			},
			30*time.Second)
		if err != nil {
			t.Fatalf("await proactive BatChargeLevel=Warning(1): %v\n%s", err, out)
		}
	})

	// The same LOW_BAT source also drives BatReplacementNeeded
	// (measurement.go: PowerSourceServer.MatterRead maps the same bool
	// source onto both attributes). Confirmed via a plain read after the
	// proactive report above landed, so this cell does not depend on a
	// second independent notifier firing.
	t.Run("receive/bat-replacement-needed", func(t *testing.T) {
		read, err := b.SharedCtl.ReadAttr(ctx, t, "powersource", "bat-replacement-needed", ep)
		if err != nil {
			t.Fatalf("read bat-replacement-needed: %v", err)
		}
		if v, ok := harness.FindAttrBool(read, "BatReplacementNeeded"); !ok || !v {
			t.Fatalf("BatReplacementNeeded not true after LOW_BAT push: got %v (ok=%v)\n%s", v, ok, read)
		}
	})
}
