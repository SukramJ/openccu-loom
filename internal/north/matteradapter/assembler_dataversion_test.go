// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package matteradapter_test

import (
	"context"
	"testing"

	"github.com/SukramJ/go-fabric/contract"
	"github.com/SukramJ/go-fabric/endpoint"

	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/north/matteradapter"
)

// buildCustomDPDevice returns a device whose single channel hosts a
// custom-DP MatterEndpointSource — the assembler turns it into exactly
// one bridged endpoint.
func buildCustomDPDevice(addr, name, param string, deviceType uint16) *device.Device {
	dev := newDevice(addr, name)
	ch := addChannel(dev, addr+":1", 1)
	ch.SetCustomDataPoint(&stubEndpointSource{
		key:        dpKey(addr+":1", param),
		deviceType: deviceType,
	})
	return dev
}

// findBridgedByAddress returns the bridged endpoint whose source device
// has the given address, or nil.
func findBridgedByAddress(top *endpoint.Topology, addr string) *endpoint.Endpoint {
	for _, ep := range top.Bridged() {
		if ep.DeviceAddress == addr {
			return ep
		}
	}
	return nil
}

// TestAssemble_BridgedDataVersionSurvivesUnrelatedReassemble locks in the
// invariant that a bridged endpoint's per-cluster DataVersion stays
// STABLE across a reassembly triggered by an UNRELATED change (a sibling
// endpoint added / removed). Mirrors matter.js
// packages/node/src/behavior/state/managed/Datasource.ts:349 — the
// version is sampled once per behavior lifetime, bound to the endpoint's
// own lifecycle; adding or removing an unrelated sibling endpoint never
// recreates another endpoint's Datasource. Without stability every
// exposure toggle re-seeds a fresh random version on every OTHER
// endpoint, so controllers' cached DataVersionFilters all miss and the
// whole bridge re-transfers on each config edit (resubscribe storm).
func TestAssemble_BridgedDataVersionSurvivesUnrelatedReassemble(t *testing.T) {
	// Deterministic version seeds (distinct per fresh tracker) so the
	// assertions are crisp. Not parallel — mutates a package var.
	restore := contract.InitialDataVersion
	var seed uint32
	contract.InitialDataVersion = func() uint32 { seed += 100; return seed }
	t.Cleanup(func() { contract.InitialDataVersion = restore })

	ctx := context.Background()
	const central = "ccu1"
	const clusterID = uint32(0x0006) // OnOff — arbitrary; only the id matters here.

	devX := buildCustomDPDevice("AAA0001", "X", "RGBW_LIGHT", 0x0101)
	devY := buildCustomDPDevice("BBB0002", "Y", "RGBW_LIGHT", 0x0101)

	// One assembler, reused across every Assemble — the version registry
	// lives on it and must survive reassembly.
	a, err := matteradapter.New(newFakeStore(), validConfig(), nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// (1) Initial assembly with X + Y.
	top1, err := a.AssembleDevices(ctx, []matteradapter.DeviceSnapshot{{CentralName: central, Devices: []*device.Device{devX, devY}}})
	if err != nil {
		t.Fatalf("Assemble #1: %v", err)
	}
	epX1 := findBridgedByAddress(top1, "AAA0001")
	if epX1 == nil {
		t.Fatal("endpoint X missing from first assembly")
	}
	vX := epX1.ClusterDataVersion(clusterID)
	if vX == 0 {
		t.Fatal("endpoint X DataVersion is zero — must be a nonzero random seed")
	}

	// (2) Reassemble triggered by an UNRELATED change: Y removed. X's
	// DataVersion must be UNCHANGED even though the *Endpoint struct is
	// rebuilt.
	top2, err := a.AssembleDevices(ctx, []matteradapter.DeviceSnapshot{{CentralName: central, Devices: []*device.Device{devX}}})
	if err != nil {
		t.Fatalf("Assemble #2: %v", err)
	}
	epX2 := findBridgedByAddress(top2, "AAA0001")
	if epX2 == nil {
		t.Fatal("endpoint X missing after unrelated reassembly")
	}
	if epX2 == epX1 {
		t.Fatal("assembler returned the SAME *Endpoint — cannot prove the version survives a struct rebuild")
	}
	if got := epX2.ClusterDataVersion(clusterID); got != vX {
		t.Fatalf("endpoint X DataVersion changed across UNRELATED reassembly: got %d, want %d (stable)", got, vX)
	}

	// (3) A real state change on X's cluster still bumps X's version...
	epX2.BumpClusterDataVersion(clusterID)
	bumped := epX2.ClusterDataVersion(clusterID)
	if bumped != vX+1 {
		t.Fatalf("BumpClusterDataVersion: got %d, want %d", bumped, vX+1)
	}

	// ...and the bump survives the next reassembly.
	top3, err := a.AssembleDevices(ctx, []matteradapter.DeviceSnapshot{{CentralName: central, Devices: []*device.Device{devX}}})
	if err != nil {
		t.Fatalf("Assemble #3: %v", err)
	}
	epX3 := findBridgedByAddress(top3, "AAA0001")
	if epX3 == nil {
		t.Fatal("endpoint X missing after third assembly")
	}
	if got := epX3.ClusterDataVersion(clusterID); got != bumped {
		t.Fatalf("bumped DataVersion did not survive reassembly: got %d, want %d", got, bumped)
	}

	// (4) A brand-new endpoint (device Z) gets its OWN nonzero version,
	// independent of X — two different devices must not collide.
	devZ := buildCustomDPDevice("CCC0003", "Z", "RGBW_LIGHT", 0x0101)
	top4, err := a.AssembleDevices(ctx, []matteradapter.DeviceSnapshot{{CentralName: central, Devices: []*device.Device{devX, devZ}}})
	if err != nil {
		t.Fatalf("Assemble #4: %v", err)
	}
	epZ := findBridgedByAddress(top4, "CCC0003")
	if epZ == nil {
		t.Fatal("endpoint Z missing")
	}
	vZ := epZ.ClusterDataVersion(clusterID)
	if vZ == 0 {
		t.Fatal("brand-new endpoint Z DataVersion is zero")
	}
	if vZ == bumped {
		t.Fatalf("brand-new endpoint Z shares X's version %d — trackers collided across devices", vZ)
	}
	// X still unchanged in the same assembly (adding Z must not disturb X).
	if got := findBridgedByAddress(top4, "AAA0001").ClusterDataVersion(clusterID); got != bumped {
		t.Fatalf("endpoint X version disturbed by adding Z: got %d, want %d", got, bumped)
	}
}
