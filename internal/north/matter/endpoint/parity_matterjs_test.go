// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// Parity tests for the three-tier topology produced by [Assembler.Assemble]
// against matter.js HEAD.
//
// matter.js reference:
//   packages/node/src/endpoints/aggregator.ts — AggregatorEndpointDefinition
//   packages/node/src/devices/bridged-device.ts — BridgedNodeEndpoint
//
// Three-tier topology shape (matter.js BridgedDevicesNode pattern):
//
//	EP 0 = RootNode  (DeviceType 0x0016, revision from schema) — system services.
//	EP 1 = Aggregator(DeviceType 0x000E, deviceRevision 2) — Parts + Index mandatory.
//	EP >= 2 = bridged endpoints, each with BridgedNode (0x0013) in DeviceTypeList.
//
// The Assembler's job is to produce this topology from snapshots. This file
// verifies the structural invariants so a change in the assembler logic does
// not silently break the Apple Home / Google Home bridge composition pattern.

package endpoint_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/north/matter/endpoint"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/matterport"
)

// stubParityClusterServer is a minimal [matterport.ClusterServer]
// that only advertises a cluster ID. Used to populate Source-backed
// bridged endpoints in parity tests without importing any real cluster package.
type stubParityClusterServer struct{ id uint32 }

func (s stubParityClusterServer) MatterClusterID() uint32 { return s.id }
func (s stubParityClusterServer) MatterRead(_ uint32) (any, bool) {
	return nil, false
}

func (s stubParityClusterServer) MatterWrite(_ context.Context, _ uint32, _ any) error {
	return errors.New("stub: read-only")
}

func (s stubParityClusterServer) MatterInvoke(_ context.Context, _ uint32, _ any) (any, error) {
	return nil, errors.New("stub: no commands")
}
func (s stubParityClusterServer) MatterReportable() []uint32 { return nil }

// stubSourceWithClusters is a minimal [matterport.EndpointSource]
// that returns a fixed set of cluster servers.
type stubSourceWithClusters struct {
	deviceType uint16
	servers    []matterport.ClusterServer
}

func (s *stubSourceWithClusters) MatterDeviceType() uint16 { return s.deviceType }
func (s *stubSourceWithClusters) MatterClusterServers() []matterport.ClusterServer {
	return s.servers
}

// TestParityMatterJS_AggregatorTopology_ThreeTier verifies that Assemble
// produces the three-tier bridge topology required by matter.js HEAD:
//
//	EP 0 = RootNode  (0x0016) — always present, always reachable.
//	EP 1 = Aggregator(0x000E) — always present, always reachable.
//	EP >= 2 = bridged endpoints (one per exposed source).
//
// Mirrors matter.js packages/node/src/endpoints/aggregator.ts:
// AggregatorEndpointDefinition.deviceType = 0xe.
func TestParityMatterJS_AggregatorTopology_ThreeTier(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	// Build three bridged sources from two devices (simulating N CCU devices).
	dev1 := device.New(device.Config{Address: "DEV0001", Name: "Device 1"})
	ch1 := dev1.AddChannel("DEV0001:1", 1, "SWITCH", hmenum.ParamsetKeyValues)
	ch1.SetCustomDataPoint(&stubEndpointSource{
		key:        dpKey("DEV0001:1", "ON_OFF"),
		deviceType: 0x010A, // OnOffPlugInUnit
	})

	dev2 := device.New(device.Config{Address: "DEV0002", Name: "Device 2"})
	ch2a := dev2.AddChannel("DEV0002:1", 1, "DIMMER", hmenum.ParamsetKeyValues)
	ch2a.SetCustomDataPoint(&stubEndpointSource{
		key:        dpKey("DEV0002:1", "DIMMABLE"),
		deviceType: 0x0101, // DimmableLight
	})
	ch2b := dev2.AddChannel("DEV0002:2", 2, "SWITCH", hmenum.ParamsetKeyValues)
	ch2b.SetCustomDataPoint(&stubEndpointSource{
		key:        dpKey("DEV0002:2", "ON_OFF"),
		deviceType: 0x010A, // OnOffPlugInUnit
	})

	snap := endpoint.Snapshot{
		CentralName: "ccu1",
		Devices:     []*device.Device{dev1, dev2},
	}

	a, err := endpoint.New(newFakeStore(), validConfig(), nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	top, err := a.Assemble(ctx, []endpoint.Snapshot{snap})
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}

	// Must have root (EP0) + aggregator (EP1) + 3 bridged = 5 total.
	if len(top.Endpoints) != 5 {
		t.Fatalf("len(Endpoints)=%d, want 5 (root + aggregator + 3 bridged)", len(top.Endpoints))
	}

	// EP 0: RootNode device-type 0x0016.
	// Mirrors matter.js packages/node/src/devices/root-node.ts RootNodeEndpoint.
	root := top.Endpoints[0]
	if root.ID != 0 {
		t.Errorf("EP0 ID=%d, want 0", root.ID)
	}
	if root.DeviceType != 0x0016 {
		t.Errorf("EP0 DeviceType=0x%04X, want 0x0016 (RootNode)", root.DeviceType)
	}
	if !root.IsRoot() {
		t.Error("EP0 IsRoot() must be true")
	}
	if !root.Reachable {
		t.Error("EP0 (root) must always be Reachable=true")
	}

	// EP 1: Aggregator device-type 0x000E.
	// Mirrors matter.js packages/node/src/endpoints/aggregator.ts:
	//   AggregatorEndpointDefinition.deviceType = 0xe
	agg := top.Endpoints[1]
	if agg.ID != 1 {
		t.Errorf("EP1 ID=%d, want 1", agg.ID)
	}
	if agg.DeviceType != 0x000E {
		t.Errorf("EP1 DeviceType=0x%04X, want 0x000E (Aggregator)", agg.DeviceType)
	}
	if !agg.IsAggregator() {
		t.Error("EP1 IsAggregator() must be true")
	}
	if !agg.Reachable {
		t.Error("EP1 (aggregator) must always be Reachable=true")
	}
	if agg.Source != nil || agg.Measurement != nil {
		t.Error("EP1 (aggregator) must not carry Source or Measurement — structure-only endpoint")
	}

	// EP >= 2: bridged endpoints.
	bridged := top.Bridged()
	if len(bridged) != 3 {
		t.Fatalf("Bridged()=%d, want 3", len(bridged))
	}
	for _, ep := range bridged {
		if ep.ID < 2 {
			t.Errorf("bridged EP ID=%d < 2 — would collide with root/aggregator", ep.ID)
		}
		if ep.IsRoot() || ep.IsAggregator() {
			t.Errorf("bridged EP ID=%d must not be root or aggregator", ep.ID)
		}
	}

	// Endpoints must be sorted by ID ascending (spec §9.5.1.1 PartsList order).
	for i := 1; i < len(top.Endpoints); i++ {
		if top.Endpoints[i].ID <= top.Endpoints[i-1].ID {
			t.Errorf("endpoints not sorted: [%d].ID=%d >= [%d].ID=%d",
				i-1, top.Endpoints[i-1].ID, i, top.Endpoints[i].ID)
		}
	}
}

// TestParityMatterJS_AggregatorTopology_EmptySnapshotIsStillPresent verifies
// that the root and aggregator endpoints are emitted even when no CCU devices
// are visible. matter.js always mounts the AggregatorEndpoint on the
// BridgedDevicesNode even before any bridged endpoints are added.
//
// Mirrors matter.js packages/node/src/devices/aggregator.ts:
// the node initialises with root + aggregator regardless of bridged count.
func TestParityMatterJS_AggregatorTopology_EmptySnapshotIsStillPresent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	a, _ := endpoint.New(newFakeStore(), validConfig(), nil)

	top, err := a.Assemble(ctx, nil)
	if err != nil {
		t.Fatalf("Assemble(nil): %v", err)
	}
	if len(top.Endpoints) != 2 {
		t.Fatalf("empty topology len=%d, want 2 (root + aggregator)", len(top.Endpoints))
	}
	if top.Endpoints[0].DeviceType != 0x0016 {
		t.Errorf("EP0 DeviceType=0x%04X, want 0x0016", top.Endpoints[0].DeviceType)
	}
	if top.Endpoints[1].DeviceType != 0x000E {
		t.Errorf("EP1 DeviceType=0x%04X, want 0x000E", top.Endpoints[1].DeviceType)
	}
}

// TestParityMatterJS_AggregatorTopology_PartsListContainsBridgedIDs verifies
// that the set of bridged endpoint IDs produced by Assemble is what the
// Aggregator's Descriptor.PartsList should enumerate.
//
// The Topology.Bridged() slice is the canonical source for PartsList —
// the bridge layer reads this and passes it to the Descriptor cluster's
// PartsList attribute on each Subscribe initial report.
//
// Mirrors matter.js packages/node/src/behavior/system/parts/PartsBehavior.ts:
// the PartsBehavior tracks every child endpoint; PartsList is derived from it.
func TestParityMatterJS_AggregatorTopology_PartsListContainsBridgedIDs(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	dev := device.New(device.Config{Address: "PL0001", Name: "Parts Test"})
	ch := dev.AddChannel("PL0001:1", 1, "SWITCH", hmenum.ParamsetKeyValues)
	ch.SetCustomDataPoint(&stubEndpointSource{
		key:        dpKey("PL0001:1", "ON_OFF"),
		deviceType: 0x010A,
	})

	a, _ := endpoint.New(newFakeStore(), validConfig(), nil)
	top, err := a.Assemble(ctx, []endpoint.Snapshot{{CentralName: "ccu1", Devices: []*device.Device{dev}}})
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}

	// Bridged() is the slice the bridge uses to build Descriptor.PartsList
	// on EP1 (Aggregator). Every bridged endpoint must be in this set.
	bridged := top.Bridged()
	if len(bridged) != 1 {
		t.Fatalf("Bridged()=%d, want 1", len(bridged))
	}

	// The bridged ID must not be 0 or 1 (those are root / aggregator).
	ep := bridged[0]
	if ep.ID == 0 || ep.ID == 1 {
		t.Errorf("bridged endpoint has reserved ID %d — would shadow root/aggregator", ep.ID)
	}

	// FindByID must locate the bridged endpoint by its ID.
	if found := top.FindByID(ep.ID); found == nil {
		t.Errorf("FindByID(%d) returned nil — topology is inconsistent", ep.ID)
	}
}

// TestParityMatterJS_EndpointNumbersReservedUntilExplicitRemoval verifies the
// endpoint-number persistence contract: a persisted endpoint number is only
// released when the source is known to have been removed, never because the
// data source has not been populated yet.
//
// Mirrors matter.js packages/node/src/storage/server/ServerEndpointStores.ts:
// load() pre-allocates every persisted number ("Ensure all known numbers are
// allocated") and assignNumber() reuses the stored number; the only release
// path is eraseStoreForEndpoint, invoked from
// packages/node/src/node/server/ServerEndpointInitializer.ts eraseDescendant
// on explicit endpoint deletion. An assembly run over a central whose model
// is not yet complete must therefore behave like matter.js's "unknown
// endpoints initialize before known endpoints" case — numbers stay reserved.
func TestParityMatterJS_EndpointNumbersReservedUntilExplicitRemoval(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	dev := device.New(device.Config{Address: "NUM0001", Name: "Persist Test"})
	ch := dev.AddChannel("NUM0001:1", 1, "SWITCH", hmenum.ParamsetKeyValues)
	ch.SetCustomDataPoint(&stubEndpointSource{
		key:        dpKey("NUM0001:1", "ON_OFF"),
		deviceType: 0x010A,
	})

	fs := newFakeStore()
	a, _ := endpoint.New(fs, validConfig(), nil)

	full := endpoint.Snapshot{CentralName: "ccu1", Devices: []*device.Device{dev}, ModelComplete: true}
	top1, err := a.Assemble(ctx, []endpoint.Snapshot{full})
	if err != nil {
		t.Fatalf("Assemble(full): %v", err)
	}
	assigned := top1.Bridged()[0].ID

	// A model-incomplete assembly (the boot-time shape) keeps the number
	// reserved even though the source is currently absent.
	incomplete := endpoint.Snapshot{CentralName: "ccu1", Devices: nil, ModelComplete: false}
	if _, err := a.Assemble(ctx, []endpoint.Snapshot{incomplete}); err != nil {
		t.Fatalf("Assemble(incomplete): %v", err)
	}
	if rows, _ := fs.ListEndpoints(ctx, "ccu1"); len(rows) != 1 || rows[0].EndpointID != assigned {
		t.Fatalf("persisted endpoint number not reserved across incomplete assembly: rows=%v", rows)
	}

	// Only a model-complete assembly without the source (the explicit
	// removal case) releases the number.
	removed := endpoint.Snapshot{CentralName: "ccu1", Devices: nil, ModelComplete: true}
	if _, err := a.Assemble(ctx, []endpoint.Snapshot{removed}); err != nil {
		t.Fatalf("Assemble(removed): %v", err)
	}
	if rows, _ := fs.ListEndpoints(ctx, "ccu1"); len(rows) != 0 {
		t.Fatalf("explicit removal did not release the endpoint number: rows=%v", rows)
	}
}

// TestParityMatterJS_BridgedEndpoint_ServerListDerivedFromMountedClusters
// verifies that a bridged endpoint's Descriptor.ServerList (attribute 0x0001)
// is derived from the set of cluster IDs actually mounted on that endpoint,
// not from a hardcoded static list.
//
// Schema-derived pattern: the advertised ServerList is the closure over the
// final mounted cluster set — identical to the Root / Aggregator derivation.
// Apple Home reads ServerList post-CASE and rejects an endpoint as
// schematically inconsistent when a cluster IS in ServerList but never
// produces an AttributeReport, or vice versa.
func TestParityMatterJS_BridgedEndpoint_ServerListDerivedFromMountedClusters(t *testing.T) {
	t.Parallel()

	const sourceClusterID uint32 = 0x0006 // OnOff — a real cluster ID, deterministic

	ep := &endpoint.Endpoint{
		ID:           2,
		DeviceType:   0x010A, // OnOffPlugInUnit
		Reachable:    true,
		FriendlyName: "Test Device",
		Source: &stubSourceWithClusters{
			deviceType: 0x010A,
			servers:    []matterport.ClusterServer{stubParityClusterServer{id: sourceClusterID}},
		},
	}

	servers := endpoint.ClusterServers(ep)
	if len(servers) == 0 {
		t.Fatal("ClusterServers returned empty slice for bridged endpoint with Source")
	}

	// Locate the Descriptor cluster (0x001D) in the returned slice.
	var descriptorServer matterport.ClusterServer
	for _, srv := range servers {
		if srv.MatterClusterID() == 0x001D {
			descriptorServer = srv
			break
		}
	}
	if descriptorServer == nil {
		t.Fatal("ClusterServers: Descriptor (0x001D) not present on bridged endpoint")
	}

	// Read ServerList from the Descriptor (attribute 0x0001).
	raw, ok := descriptorServer.MatterRead(0x0001)
	if !ok {
		t.Fatal("Descriptor.MatterRead(0x0001 ServerList) returned ok=false")
	}
	serverList, ok := raw.([]uint32)
	if !ok {
		t.Fatalf("Descriptor.ServerList: got type %T, want []uint32", raw)
	}

	// Build the expected set: the IDs of every server in the returned slice.
	wantSet := make(map[uint32]struct{}, len(servers))
	for _, srv := range servers {
		wantSet[srv.MatterClusterID()] = struct{}{}
	}

	// The ServerList must equal the mounted cluster set exactly.
	gotSet := make(map[uint32]struct{}, len(serverList))
	for _, id := range serverList {
		gotSet[id] = struct{}{}
	}

	for id := range wantSet {
		if _, found := gotSet[id]; !found {
			t.Errorf("ServerList missing cluster ID 0x%04X (present in mounted set)", id)
		}
	}
	for id := range gotSet {
		if _, found := wantSet[id]; !found {
			t.Errorf("ServerList contains cluster ID 0x%04X not in mounted set", id)
		}
	}

	// Verify the source cluster and mandatory clusters are individually present.
	mustHave := []uint32{
		0x0003,          // Identify — mandatory on bridged endpoints
		sourceClusterID, // OnOff — from the source
		0x001D,          // Descriptor itself
		0x0039,          // BridgedDeviceBasicInformation
	}
	for _, id := range mustHave {
		if _, found := gotSet[id]; !found {
			t.Errorf("ServerList missing mandatory cluster ID 0x%04X", id)
		}
	}

	// Duplicate-free: each ID appears at most once.
	seen := make(map[uint32]int, len(serverList))
	for _, id := range serverList {
		seen[id]++
	}
	for id, count := range seen {
		if count > 1 {
			t.Errorf("ServerList contains duplicate cluster ID 0x%04X (%d times)", id, count)
		}
	}

	// Sort-stable consistency: a second call must return the same set.
	raw2, _ := descriptorServer.MatterRead(0x0001)
	serverList2 := raw2.([]uint32)
	sl1 := append([]uint32(nil), serverList...)
	sl2 := append([]uint32(nil), serverList2...)
	slices.Sort(sl1)
	slices.Sort(sl2)
	if len(sl1) != len(sl2) {
		t.Fatalf("consecutive ServerList reads: len %d vs %d", len(sl1), len(sl2))
	}
	for i := range sl1 {
		if sl1[i] != sl2[i] {
			t.Errorf("consecutive ServerList reads differ at index %d: 0x%04X vs 0x%04X", i, sl1[i], sl2[i])
		}
	}
}

// stubFloatMeasurement is a [matterport.MeasurementSource] carrying
// a float reading, the shape every analog sensor DP presents to the bridge.
type stubFloatMeasurement struct {
	class matterport.MeasurementClass
	val   float64
}

func (s stubFloatMeasurement) MatterMeasurementClass() matterport.MeasurementClass {
	return s.class
}
func (s stubFloatMeasurement) MatterFloatValue() (float64, bool) { return s.val, true }

// stubBoolMeasurement is the binary counterpart of stubFloatMeasurement.
type stubBoolMeasurement struct {
	class matterport.MeasurementClass
}

func (s stubBoolMeasurement) MatterMeasurementClass() matterport.MeasurementClass {
	return s.class
}
func (s stubBoolMeasurement) MatterBoolValue() (value, observed bool) { return true, true }

// TestParityMatterJS_MeasurementDeviceTypeMandatoryClusters asserts that
// every measurement endpoint the bridge materialises serves the full
// mandatory server-cluster set its primary device type declares in
// matter.js `packages/node/src/devices/<name>.ts`
// (`Requirements.server.mandatory`). A device type advertised without
// its mandatory clusters fails the controller-side requirement check:
// Apple Home cannot build the matching HAP service and the endpoint
// never becomes an accessory.
//
// Identify (0x0003) is mandatory for all of them and is mounted by
// [endpoint.ClusterServers] itself, so it is checked once rather than
// listed per row.
func TestParityMatterJS_MeasurementDeviceTypeMandatoryClusters(t *testing.T) {
	t.Parallel()

	const identifyCluster uint32 = 0x0003

	rows := []struct {
		deviceTypeName string
		deviceType     uint16
		measurement    matterport.MeasurementSource
		mandatory      []uint32
	}{
		{
			deviceTypeName: "TemperatureSensor",
			deviceType:     0x0302,
			measurement:    stubFloatMeasurement{class: matterport.MeasurementTemperature, val: 21.5},
			mandatory:      []uint32{0x0402}, // TemperatureMeasurement
		},
		{
			deviceTypeName: "HumiditySensor",
			deviceType:     0x0307,
			measurement:    stubFloatMeasurement{class: matterport.MeasurementHumidity, val: 48},
			mandatory:      []uint32{0x0405}, // RelativeHumidityMeasurement
		},
		{
			deviceTypeName: "LightSensor",
			deviceType:     0x0106,
			measurement:    stubFloatMeasurement{class: matterport.MeasurementIlluminance, val: 300},
			mandatory:      []uint32{0x0400}, // IlluminanceMeasurement
		},
		{
			deviceTypeName: "PressureSensor",
			deviceType:     0x0305,
			measurement:    stubFloatMeasurement{class: matterport.MeasurementPressure, val: 1013},
			mandatory:      []uint32{0x0403}, // PressureMeasurement
		},
		{
			deviceTypeName: "AirQualitySensorCO2",
			deviceType:     0x002C,
			measurement:    stubFloatMeasurement{class: matterport.MeasurementCO2, val: 650},
			mandatory:      []uint32{0x005B}, // AirQuality — every concentration cluster is optional
		},
		{
			deviceTypeName: "AirQualitySensorPM25",
			deviceType:     0x002C,
			measurement:    stubFloatMeasurement{class: matterport.MeasurementPM25, val: 12},
			mandatory:      []uint32{0x005B},
		},
		{
			deviceTypeName: "AirQualitySensorPM10",
			deviceType:     0x002C,
			measurement:    stubFloatMeasurement{class: matterport.MeasurementPM10, val: 30},
			mandatory:      []uint32{0x005B},
		},
		{
			deviceTypeName: "OccupancySensor",
			deviceType:     0x0107,
			measurement:    stubBoolMeasurement{class: matterport.MeasurementOccupancy},
			mandatory:      []uint32{0x0406}, // OccupancySensing
		},
		{
			deviceTypeName: "ContactSensor",
			deviceType:     0x0015,
			measurement:    stubBoolMeasurement{class: matterport.MeasurementContact},
			mandatory:      []uint32{0x0045}, // BooleanState
		},
	}

	for _, r := range rows {
		t.Run(r.deviceTypeName, func(t *testing.T) {
			t.Parallel()

			// Pin the device type the assembler derives for this class —
			// otherwise the mandatory set below would be checked against
			// the wrong requirement list.
			if got := matterport.MeasurementClassDeviceType(r.measurement.MatterMeasurementClass()); got != r.deviceType {
				t.Fatalf("device type for class = 0x%04X, want 0x%04X", got, r.deviceType)
			}

			ep := &endpoint.Endpoint{
				ID:           2,
				DeviceType:   r.deviceType,
				Reachable:    true,
				FriendlyName: r.deviceTypeName,
				Measurement:  r.measurement,
			}
			mounted := make(map[uint32]bool)
			for _, srv := range endpoint.ClusterServers(ep) {
				mounted[srv.MatterClusterID()] = true
			}
			if !mounted[identifyCluster] {
				t.Errorf("Identify (0x0003) not mounted on device type 0x%04X; mounted = %v", r.deviceType, mounted)
			}
			for _, id := range r.mandatory {
				if !mounted[id] {
					t.Errorf("mandatory cluster 0x%04X of device type 0x%04X not mounted; mounted = %v",
						id, r.deviceType, mounted)
				}
			}
		})
	}
}
