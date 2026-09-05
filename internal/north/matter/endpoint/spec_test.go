// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// Package endpoint_test — assembly from hand-written [endpoint.Spec]
// values.
//
// This file deliberately imports NO device-model package. It is the
// standing proof that the assembly's input is a flat description an
// owner produces from whatever tree it has, not the Homematic device
// tree the daemon happens to hold: if a model type ever creeps back
// into the assembly input, this file stops compiling.
package endpoint_test

import (
	"context"
	"log/slog"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/north/matter/endpoint"
	"github.com/SukramJ/openccu-loom/internal/north/matter/store"
	"github.com/SukramJ/openccu-loom/pkg/mattercontract"
)

// specSource is a minimal [mattercontract.EndpointSource]: enough to
// occupy an endpoint's Source slot, no cluster logic.
type specSource struct {
	deviceType uint16
}

func (s specSource) MatterDeviceType() uint16 { return s.deviceType }

func (s specSource) MatterClusterServers() []mattercontract.ClusterServer { return nil }

func specKey(central, addr string, channel int, key string) store.EndpointKey {
	return store.EndpointKey{
		CentralName:   central,
		DeviceAddress: addr,
		ChannelNo:     channel,
		DPKind:        store.DPKindCustom,
		DPKey:         key,
	}
}

func specAssembler(t *testing.T) *endpoint.Assembler {
	t.Helper()
	a, err := endpoint.New(newFakeStore(), endpoint.Config{
		VendorID:  0x1234,
		ProductID: 0x5678,
		NodeLabel: "SpecBridge",
	}, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("endpoint.New: %v", err)
	}
	return a
}

// TestAssembleFromSpecsBuildsThreeTierTopology drives a full assembly
// from hand-written specs and pins the shape a commissioner sees: root
// at 0, Aggregator at 1, every bridged endpoint at 2+ parented on the
// Aggregator and carrying the values its spec supplied.
func TestAssembleFromSpecsBuildsThreeTierTopology(t *testing.T) {
	t.Parallel()

	const central = "ccu-spec"
	dead := func() bool { return false }

	specs := []endpoint.Spec{
		{
			StableKey:      specKey(central, "DEV0001", 1, "SWITCH"),
			DeviceType:     0x010A,
			FriendlyName:   "Lamp",
			ChannelAddress: "DEV0001:1",
			Source:         specSource{deviceType: 0x010A},
		},
		{
			StableKey:      specKey(central, "DEV0002", 3, "SWITCH"),
			DeviceType:     0x0100,
			FriendlyName:   "Unreachable Lamp",
			ChannelAddress: "DEV0002:3",
			Availability:   dead,
			Source:         specSource{deviceType: 0x0100},
		},
	}

	top, err := specAssembler(t).Assemble(context.Background(), []endpoint.Snapshot{{
		CentralName:   central,
		Endpoints:     specs,
		ModelComplete: true,
	}})
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}

	if len(top.Endpoints) != 4 {
		t.Fatalf("len(Endpoints)=%d, want 4 (root + aggregator + 2 bridged)", len(top.Endpoints))
	}
	if root := top.Endpoints[0]; !root.IsRoot() || root.DeviceType != 0x0016 {
		t.Errorf("endpoint 0 = {ID:%d, DeviceType:0x%04X}, want the root node (0, 0x0016)", root.ID, root.DeviceType)
	}
	if agg := top.Endpoints[1]; !agg.IsAggregator() || agg.DeviceType != 0x000E {
		t.Errorf("endpoint 1 = {ID:%d, DeviceType:0x%04X}, want the aggregator (1, 0x000E)", agg.ID, agg.DeviceType)
	}

	bridged := top.Bridged()
	if len(bridged) != len(specs) {
		t.Fatalf("len(Bridged())=%d, want %d", len(bridged), len(specs))
	}
	for i, ep := range bridged {
		spec := specs[i]
		if ep.ID < 2 {
			t.Errorf("bridged[%d].ID=%d, want >= 2", i, ep.ID)
		}
		if ep.SourceKey != spec.StableKey {
			t.Errorf("bridged[%d].SourceKey=%+v, want %+v", i, ep.SourceKey, spec.StableKey)
		}
		if ep.DeviceType != spec.DeviceType {
			t.Errorf("bridged[%d].DeviceType=0x%04X, want 0x%04X", i, ep.DeviceType, spec.DeviceType)
		}
		if ep.FriendlyName != spec.FriendlyName {
			t.Errorf("bridged[%d].FriendlyName=%q, want %q", i, ep.FriendlyName, spec.FriendlyName)
		}
		if ep.ChannelAddress != spec.ChannelAddress {
			t.Errorf("bridged[%d].ChannelAddress=%q, want %q", i, ep.ChannelAddress, spec.ChannelAddress)
		}
		if !ep.HasParentEndpointID || ep.ParentEndpointID != 1 {
			t.Errorf("bridged[%d] parent = (%d, has=%v), want (1, true)", i, ep.ParentEndpointID, ep.HasParentEndpointID)
		}
		if ep.BridgeVendorID != top.VendorID || ep.BridgeProductID != top.ProductID {
			t.Errorf("bridged[%d] VID/PID = 0x%04X/0x%04X, want the bridge's 0x%04X/0x%04X",
				i, ep.BridgeVendorID, ep.BridgeProductID, top.VendorID, top.ProductID)
		}
	}

	// A nil probe reads as permanently reachable; a probe that says no
	// is carried into the assembled reading AND kept for the live
	// re-read the cluster surface does on every dispatch.
	if !bridged[0].Reachable {
		t.Error("bridged[0].Reachable=false, want true for a spec with no availability probe")
	}
	if bridged[1].Reachable {
		t.Error("bridged[1].Reachable=true, want false — its probe reports the source dead")
	}
	if bridged[1].Availability == nil || bridged[1].Availability() {
		t.Error("bridged[1].Availability must carry the spec's probe so a dispatch re-reads it live")
	}
}

// TestAssembleCapsNodeLabelFromSpec pins that the 32-byte
// BridgedDeviceBasicInformation.NodeLabel maximum is enforced by the
// assembly itself. The constraint is Matter's, so an owner that hands
// over an over-long label must not be able to put a non-conformant one
// on the wire.
func TestAssembleCapsNodeLabelFromSpec(t *testing.T) {
	t.Parallel()

	const central = "ccu-spec-cap"
	const overLong = "A name far longer than the Matter NodeLabel maximum"

	top, err := specAssembler(t).Assemble(context.Background(), []endpoint.Snapshot{{
		CentralName: central,
		Endpoints: []endpoint.Spec{{
			StableKey:    specKey(central, "DEV0003", 1, "SWITCH"),
			DeviceType:   0x010A,
			FriendlyName: overLong,
			Source:       specSource{deviceType: 0x010A},
		}},
		ModelComplete: true,
	}})
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	bridged := top.Bridged()
	if len(bridged) != 1 {
		t.Fatalf("len(Bridged())=%d, want 1", len(bridged))
	}
	if got := bridged[0].FriendlyName; len(got) != 32 || got != overLong[:32] {
		t.Errorf("FriendlyName=%q (%d bytes), want the first 32 bytes of the supplied label", got, len(got))
	}
}

// TestAssembleReusesEndpointIDsAcrossReassembly pins the property a
// paired controller depends on: the same stable key keeps the same
// endpoint id when the topology is rebuilt, and an unrelated addition
// does not renumber it.
func TestAssembleReusesEndpointIDsAcrossReassembly(t *testing.T) {
	t.Parallel()

	const central = "ccu-spec-stable"
	first := endpoint.Spec{
		StableKey:    specKey(central, "DEV0004", 1, "SWITCH"),
		DeviceType:   0x010A,
		FriendlyName: "First",
		Source:       specSource{deviceType: 0x010A},
	}
	second := endpoint.Spec{
		StableKey:    specKey(central, "DEV0005", 1, "SWITCH"),
		DeviceType:   0x010A,
		FriendlyName: "Second",
		Source:       specSource{deviceType: 0x010A},
	}

	a := specAssembler(t)
	ctx := context.Background()

	before, err := a.Assemble(ctx, []endpoint.Snapshot{{CentralName: central, Endpoints: []endpoint.Spec{first}, ModelComplete: true}})
	if err != nil {
		t.Fatalf("Assemble (first): %v", err)
	}
	firstID := before.Bridged()[0].ID

	after, err := a.Assemble(ctx, []endpoint.Snapshot{{CentralName: central, Endpoints: []endpoint.Spec{second, first}, ModelComplete: true}})
	if err != nil {
		t.Fatalf("Assemble (second): %v", err)
	}
	var got uint16
	for _, ep := range after.Bridged() {
		if ep.SourceKey == first.StableKey {
			got = ep.ID
		}
	}
	if got != firstID {
		t.Errorf("endpoint id for the unchanged source moved from %d to %d — every paired controller would have to re-add it", firstID, got)
	}
}

// TestAssembleRejectsUnnamedCentral pins the multi-CCU invariant: an
// endpoint has to be scoped to a central, so a snapshot without one is
// an error rather than a silently mis-scoped topology.
func TestAssembleRejectsUnnamedCentral(t *testing.T) {
	t.Parallel()

	_, err := specAssembler(t).Assemble(context.Background(), []endpoint.Snapshot{{
		Endpoints: []endpoint.Spec{{
			StableKey:  specKey("", "DEV0006", 1, "SWITCH"),
			DeviceType: 0x010A,
			Source:     specSource{deviceType: 0x010A},
		}},
	}})
	if err == nil {
		t.Fatal("Assemble accepted a snapshot with no CentralName, want an error")
	}
}
