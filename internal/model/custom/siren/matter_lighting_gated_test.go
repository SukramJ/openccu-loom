// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package siren

import (
	"context"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// TestSirenOnOffFeatureMapAdvertisesLighting confirms the OnOff cluster
// advertises the LT (Lighting) feature bit 0 (0x01) — OnOffPlugInUnit
// (0x010A) mandates it (on-off-plug-in-unit.element.ts:22-25), and a
// FeatureMap of 0 previously left the four LT-gated attributes / three
// LT-gated commands unimplemented despite the device type requiring
// them.
func TestSirenOnOffFeatureMapAdvertisesLighting(t *testing.T) {
	r := newRig(t, "HmIP-ASIR:3", &stubWriter{}, custom.SirenCapabilities{SupportsAcoustic: true})
	srv := findCluster(t, r.siren, matterClusterOnOff)
	v, ok := srv.MatterRead(matterAttrFeatureMap)
	if !ok {
		t.Fatal("MatterRead(FeatureMap) ok=false")
	}
	if v.(uint32)&matterFeatureOnOffLT == 0 {
		t.Fatalf("Siren OnOff FeatureMap = 0x%08X, missing LT bit (0x01)", v.(uint32))
	}
}

// TestSirenOnOffLightingGatedAttributesReadable confirms the four
// LT-mandatory OnOff attributes are readable. matter.js
// on-off.element.ts:29-36.
func TestSirenOnOffLightingGatedAttributesReadable(t *testing.T) {
	r := newRig(t, "HmIP-ASIR:3", &stubWriter{}, custom.SirenCapabilities{SupportsAcoustic: true})
	srv := findCluster(t, r.siren, matterClusterOnOff)

	if v, ok := srv.MatterRead(matterAttrOnOffGlobalSceneControl); !ok || v != false {
		t.Fatalf("GlobalSceneControl = (%v, %v), want (false, true)", v, ok)
	}
	if v, ok := srv.MatterRead(matterAttrOnOffOnTime); !ok || v.(uint16) != 0 {
		t.Fatalf("OnTime = (%v, %v), want (0, true)", v, ok)
	}
	if v, ok := srv.MatterRead(matterAttrOnOffOffWaitTime); !ok || v.(uint16) != 0 {
		t.Fatalf("OffWaitTime = (%v, %v), want (0, true)", v, ok)
	}
	if v, ok := srv.MatterRead(matterAttrOnOffStartUpOnOff); !ok || v != nil {
		t.Fatalf("StartUpOnOff = (%v, %v), want (nil/null, true)", v, ok)
	}
}

// TestSirenOnOffLightingGatedAttributesAndCommandsEnumerated confirms
// the LT-mandatory attributes and commands are enumerated for
// conformance reads (chip-tool / Apple Home read AcceptedCommandList +
// the attribute set during commissioning).
func TestSirenOnOffLightingGatedAttributesAndCommandsEnumerated(t *testing.T) {
	r := newRig(t, "HmIP-ASIR:3", &stubWriter{}, custom.SirenCapabilities{SupportsAcoustic: true})
	srv := findCluster(t, r.siren, matterClusterOnOff)

	lister, ok := srv.(interface{ MatterAttributes() []uint32 })
	if !ok {
		t.Fatal("sirenOnOffServer must implement MatterAttributes")
	}
	attrs := map[uint32]bool{}
	for _, a := range lister.MatterAttributes() {
		attrs[a] = true
	}
	for _, want := range []uint32{
		matterAttrOnOffGlobalSceneControl,
		matterAttrOnOffOnTime,
		matterAttrOnOffOffWaitTime,
		matterAttrOnOffStartUpOnOff,
	} {
		if !attrs[want] {
			t.Errorf("MatterAttributes missing LT-gated 0x%04X", want)
		}
	}

	cmdLister, ok := srv.(interface{ MatterAcceptedCommands() []uint32 })
	if !ok {
		t.Fatal("sirenOnOffServer must implement MatterAcceptedCommands")
	}
	cmds := map[uint32]bool{}
	for _, c := range cmdLister.MatterAcceptedCommands() {
		cmds[c] = true
	}
	for _, want := range []uint32{matterCmdOffWithEffect, matterCmdOnWithRecallGlobalScene, matterCmdOnWithTimedOff} {
		if !cmds[want] {
			t.Errorf("MatterAcceptedCommands missing LT-gated 0x%02X", want)
		}
	}
}

// TestSirenOnOffLightingGatedCommandsRouteToOnOff verifies the three
// LT-mandatory commands are accepted and route to the plain alarm
// on/off path — HM-ASIR has no dimming-effect / scene / on-timer
// engine, so the device-type conformance requirement is met by
// accepting the commands without error.
func TestSirenOnOffLightingGatedCommandsRouteToOnOff(t *testing.T) {
	for _, cmd := range []uint32{matterCmdOffWithEffect, matterCmdOnWithRecallGlobalScene, matterCmdOnWithTimedOff} {
		w := &stubWriter{}
		r := newRig(t, "HmIP-ASIR:3", w, custom.SirenCapabilities{SupportsAcoustic: true})
		// Seed an observed acoustic selection so TurnOn resolves a
		// non-empty selection and reaches the wire — a fresh rig with no
		// declared ValueList default would otherwise make TurnOn's write
		// conditionally empty, which is a Siren-selection concern
		// unrelated to the OnOff LT command routing this test locks down.
		r.acousticIdxDP.RecordLabel("FREQUENCY_RISING")
		srv := findCluster(t, r.siren, matterClusterOnOff)
		if _, err := srv.MatterInvoke(context.Background(), cmd, nil, hmenum.CommandPriorityHigh); err != nil {
			t.Fatalf("MatterInvoke(0x%02X) error: %v", cmd, err)
		}
		if len(w.calls) == 0 {
			t.Fatalf("MatterInvoke(0x%02X) did not reach the wire", cmd)
		}
	}
}

// TestSirenOnOffLightingGatedAttributeWritesAccepted verifies OnTime /
// OffWaitTime / StartUpOnOff writes are accepted (RW per on-off.
// element.ts:31-36) rather than rejected outright.
func TestSirenOnOffLightingGatedAttributeWritesAccepted(t *testing.T) {
	r := newRig(t, "HmIP-ASIR:3", &stubWriter{}, custom.SirenCapabilities{SupportsAcoustic: true})
	srv := findCluster(t, r.siren, matterClusterOnOff)

	if err := srv.MatterWrite(context.Background(), matterAttrOnOffOnTime, uint16(30), hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("MatterWrite(OnTime) = %v, want nil", err)
	}
	if err := srv.MatterWrite(context.Background(), matterAttrOnOffOffWaitTime, uint16(30), hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("MatterWrite(OffWaitTime) = %v, want nil", err)
	}
	if err := srv.MatterWrite(context.Background(), matterAttrOnOffStartUpOnOff, uint8(1), hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("MatterWrite(StartUpOnOff, 1) = %v, want nil", err)
	}
	if err := srv.MatterWrite(context.Background(), matterAttrOnOffStartUpOnOff, nil, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("MatterWrite(StartUpOnOff, nil) = %v, want nil", err)
	}
}
