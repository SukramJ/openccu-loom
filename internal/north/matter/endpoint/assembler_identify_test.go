// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package endpoint_test

import (
	"context"
	"testing"

	"go.uber.org/goleak"

	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/north/matter/endpoint"
	"github.com/SukramJ/openccu-loom/internal/north/matter/im"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
	"github.com/SukramJ/openccu-loom/pkg/mattercontract"
)

// onOffStubServer is a minimal read-only cluster server. A bridged
// endpoint whose source contributes no cluster server at all is dropped
// by [endpoint.ClusterServers] before the mandatory Identify server is
// mounted, so the source below must yield one.
type onOffStubServer struct{}

func (onOffStubServer) MatterClusterID() uint32       { return 0x0006 }
func (onOffStubServer) MatterRead(uint32) (any, bool) { return uint8(0), true }
func (onOffStubServer) MatterReportable() []uint32    { return nil }

func (onOffStubServer) MatterWrite(context.Context, uint32, any) error {
	return nil
}

func (onOffStubServer) MatterInvoke(context.Context, uint32, any) (any, error) {
	return nil, nil
}

// identifiableSource is a [mattercontract.EndpointSource] that mounts
// one cluster server, so the assembled endpoint carries the full bridged
// surface including Identify.
type identifiableSource struct {
	key        hmtypes.DataPointKey
	deviceType uint16
}

func (s *identifiableSource) DataPointKey() hmtypes.DataPointKey { return s.key }
func (s *identifiableSource) MatterDeviceType() uint16           { return s.deviceType }
func (s *identifiableSource) MatterClusterServers() []mattercontract.ClusterServer {
	return []mattercontract.ClusterServer{onOffStubServer{}}
}

// buildIdentifiableDevice returns a device whose single channel hosts an
// [identifiableSource] — the assembler turns it into exactly one bridged
// endpoint with an Identify cluster server.
func buildIdentifiableDevice(addr, name string) *device.Device {
	dev := newDevice(addr, name)
	ch := addChannel(dev, addr+":1", 1)
	ch.SetCustomDataPoint(&identifiableSource{key: dpKey(addr+":1", "STATE"), deviceType: 0x010A})
	return dev
}

// identifyCommandPath addresses Identify(0x0003) command 0x00 on ep.
func identifyCommandPath(ep uint16) im.ConcreteCommandPath {
	return im.ConcreteCommandPath{
		Endpoint: ep, HasEndpoint: true,
		Cluster: 0x0003, HasCluster: true,
		Command: 0x00, HasCommand: true,
	}
}

// identifyTimePath addresses Identify.IdentifyTime (0x0003 / 0x0000) on ep.
func identifyTimePath(ep uint16) im.ConcreteAttributePath {
	return im.ConcreteAttributePath{
		Endpoint: ep, HasEndpoint: true,
		Cluster: 0x0003, HasCluster: true,
		Attribute: 0x0000, HasAttribute: true,
	}
}

// readIdentifyTime dispatches a concrete read of IdentifyTime and returns
// the value.
func readIdentifyTime(t *testing.T, top *endpoint.Topology, epID uint16) uint16 {
	t.Helper()
	results := endpoint.NewTopologyDispatcher(top).Read(context.Background(), identifyTimePath(epID))
	if len(results) != 1 {
		t.Fatalf("Read(IdentifyTime): want 1 result, got %d", len(results))
	}
	if results[0].Status != im.StatusSuccess {
		t.Fatalf("Read(IdentifyTime) status = %v, want StatusSuccess", results[0].Status)
	}
	got, ok := results[0].Value.Value.(uint16)
	if !ok {
		t.Fatalf("IdentifyTime value = %#v, want uint16", results[0].Value.Value)
	}
	return got
}

// TestAssemble_IdentifyRunsAcrossReassemblyAndStopsWhenTheEndpointVanishes
// locks the Identify (0x0003) lifetime to the endpoint IDENTITY rather than
// to a single *Endpoint instance. Identify is the one bridged cluster
// server with mutable state — IdentifyTime plus a once-per-second countdown
// goroutine — while cluster servers are materialised per dispatch and the
// *Endpoint struct is rebuilt on every Assemble. So the invariant has two
// halves and both are asserted through the real assembler + dispatcher:
//
//  1. An identify a commissioner started stays observable after an
//     unrelated topology change rebuilds the endpoint; otherwise "Identify
//     accessory" reports Success and then reads back 0 forever.
//  2. When the endpoint itself leaves the topology the countdown stops, so
//     an orphaned instance cannot tick for the remaining IdentifyTime
//     (up to 65535 s). Mirrors matter.js disposing IdentifyServer with the
//     endpoint (packages/node/src/behaviors/identify/IdentifyServer.ts).
func TestAssemble_IdentifyRunsAcrossReassemblyAndStopsWhenTheEndpointVanishes(t *testing.T) {
	ignore := goleak.IgnoreCurrent()
	ctx := context.Background()
	const central = "ccu1"
	// Long enough that a stranded countdown would still be running at the
	// end of the test — the leak check would then fail.
	const identifyTime = uint16(600)

	devX := buildIdentifiableDevice("AAA0001", "X")
	devY := buildIdentifiableDevice("BBB0002", "Y")

	a, err := endpoint.New(newFakeStore(), validConfig(), nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	top1, err := a.AssembleDevices(ctx, []endpoint.DeviceSnapshot{{CentralName: central, Devices: []*device.Device{devX, devY}}})
	if err != nil {
		t.Fatalf("Assemble #1: %v", err)
	}
	epX1 := findBridgedByAddress(top1, "AAA0001")
	if epX1 == nil {
		t.Fatal("endpoint X missing from first assembly")
	}

	res := endpoint.NewTopologyDispatcher(top1).Invoke(ctx, identifyCommandPath(epX1.ID), identifyTime)
	if res.Status != im.StatusSuccess {
		t.Fatalf("Invoke(Identify) status = %v, want StatusSuccess", res.Status)
	}
	if got := readIdentifyTime(t, top1, epX1.ID); got == 0 {
		t.Fatal("IdentifyTime reads 0 right after a successful Identify command")
	}

	// (1) Unrelated change: sibling Y removed. X is rebuilt as a new
	// *Endpoint but must keep identifying.
	top2, err := a.AssembleDevices(ctx, []endpoint.DeviceSnapshot{{CentralName: central, Devices: []*device.Device{devX}}})
	if err != nil {
		t.Fatalf("Assemble #2: %v", err)
	}
	epX2 := findBridgedByAddress(top2, "AAA0001")
	if epX2 == nil {
		t.Fatal("endpoint X missing after unrelated reassembly")
	}
	if epX2 == epX1 {
		t.Fatal("assembler returned the SAME *Endpoint — cannot prove the identify state survives a struct rebuild")
	}
	if got := readIdentifyTime(t, top2, epX2.ID); got == 0 {
		t.Fatal("IdentifyTime reset to 0 across an UNRELATED reassembly — the identify was bound to the discarded *Endpoint")
	}

	// (2) X itself vanishes: its countdown must stop with it.
	if _, err = a.AssembleDevices(ctx, []endpoint.DeviceSnapshot{{CentralName: central, ModelComplete: true}}); err != nil {
		t.Fatalf("Assemble #3: %v", err)
	}
	goleak.VerifyNone(t, ignore)
}
