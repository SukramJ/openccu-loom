// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package switchdev

import (
	"context"
	"testing"

	"github.com/SukramJ/go-fabric/cluster/onoff"
)

// TestParityMatterJS_SwitchDataVersionBumpsOnInvoke verifies that an
// accepted On or Off advances the cluster's data version.
func TestParityMatterJS_SwitchDataVersionBumpsOnInvoke(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		startOn bool
		cmd     uint32
	}{
		{"On", false, onoff.CmdOn},
		{"Off", true, onoff.CmdOff},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s, srv, _ := matterSwitch(t)
			s.OnState(tc.startOn)
			before := srv.MatterDataVersion()
			if _, err := srv.MatterInvoke(context.Background(), tc.cmd, nil); err != nil {
				t.Fatalf("%s: %v", tc.name, err)
			}
			if after := srv.MatterDataVersion(); after <= before {
				t.Fatalf("data version did not advance after %s: before=%d after=%d", tc.name, before, after)
			}
		})
	}
}

// TestParityMatterJS_SwitchDataVersionMonotonicallyRises verifies that
// consecutive state-changing commands each advance the counter.
func TestParityMatterJS_SwitchDataVersionMonotonicallyRises(t *testing.T) {
	t.Parallel()
	_, srv, _ := matterSwitch(t)
	for i, cmd := range []uint32{onoff.CmdOn, onoff.CmdOff, onoff.CmdToggle} {
		prev := srv.MatterDataVersion()
		if _, err := srv.MatterInvoke(context.Background(), cmd, nil); err != nil {
			t.Fatalf("command %d: %v", i, err)
		}
		if next := srv.MatterDataVersion(); next <= prev {
			t.Fatalf("command %d: data version not rising: prev=%d next=%d", i, prev, next)
		}
	}
}

// TestParityMatterJS_SwitchDataVersionStableOnRead verifies that reads
// leave the counter alone.
func TestParityMatterJS_SwitchDataVersionStableOnRead(t *testing.T) {
	t.Parallel()
	_, srv, _ := matterSwitch(t)
	before := srv.MatterDataVersion()
	srv.MatterRead(onoff.AttrOnOff)
	srv.MatterRead(0xFFFC)
	srv.MatterRead(0xFFFD)
	if after := srv.MatterDataVersion(); after != before {
		t.Fatalf("a read advanced the data version: before=%d after=%d", before, after)
	}
}

// TestParityMatterJS_SwitchDataVersionStableOnRefusedWrite verifies that
// a refused write to the read-only OnOff attribute leaves the counter
// alone.
func TestParityMatterJS_SwitchDataVersionStableOnRefusedWrite(t *testing.T) {
	t.Parallel()
	_, srv, _ := matterSwitch(t)
	before := srv.MatterDataVersion()
	_ = srv.MatterWrite(context.Background(), onoff.AttrOnOff, true)
	if after := srv.MatterDataVersion(); after != before {
		t.Fatalf("a refused write advanced the data version: before=%d after=%d", before, after)
	}
}

// TestParityMatterJS_SwitchDataVersionStableOnUnknownCommand verifies
// that a rejected command leaves the counter alone.
func TestParityMatterJS_SwitchDataVersionStableOnUnknownCommand(t *testing.T) {
	t.Parallel()
	_, srv, _ := matterSwitch(t)
	before := srv.MatterDataVersion()
	_, _ = srv.MatterInvoke(context.Background(), 0x43, nil)
	if after := srv.MatterDataVersion(); after != before {
		t.Fatalf("a rejected command advanced the data version: before=%d after=%d", before, after)
	}
}

// TestSwitch_MatterDataVersion_BumpsOnCCUEcho verifies that a CCU-confirmed
// STATE transition advances the OnOff cluster's data version: Apple's HAP
// mapper dedupes a report with an unchanged version, so a relay flip from
// outside Matter (wall switch, MQTT, ReGa) would never reach the
// accessory. A same-value echo is no transition and must not advance it.
func TestSwitch_MatterDataVersion_BumpsOnCCUEcho(t *testing.T) {
	t.Parallel()
	sw := newTestSwitch(t, "00021BE9957782:4", "test", nil)
	srv := matterOnOffServerOf(t, sw)

	before := srv.MatterDataVersion()
	sw.OnEvent(true)
	if after := srv.MatterDataVersion(); after <= before {
		t.Fatalf("first CCU echo (true): data version did not advance: before=%d after=%d", before, after)
	}

	stable := srv.MatterDataVersion()
	sw.OnEvent(true)
	if after := srv.MatterDataVersion(); after != stable {
		t.Fatalf("same-value CCU echo: data version must not advance: stable=%d after=%d", stable, after)
	}

	before = srv.MatterDataVersion()
	sw.OnEvent(false)
	if after := srv.MatterDataVersion(); after <= before {
		t.Fatalf("CCU echo (true→false): data version did not advance: before=%d after=%d", before, after)
	}
}
