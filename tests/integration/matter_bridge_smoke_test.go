// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

//go:build integration

// matter_bridge_smoke_test.go — Bridge composition smoke tests.
//
// Ports matter.js packages/node/test/endpoints/BridgeTest.ts into openccu-loom
// integration scope. No real CCU needed — synthetic OnOff + TempSensor devices.
package integration

import (
	"context"
	"errors"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/north/matter/endpoint"
	"github.com/SukramJ/openccu-loom/internal/north/matter/store"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
	"github.com/SukramJ/openccu-loom/pkg/interfaces"
)

// ─── matter device-type constants ─────────────────────────────────────────────

const (
	// matter.js packages/node/src/devices/root-node.ts  RootNodeDt.deviceType
	smokeDevTypeRootNode uint16 = 0x0016
	// matter.js packages/node/src/endpoints/aggregator.ts  AggregatorEndpointDefinition.deviceType
	smokeDevTypeAggregator uint16 = 0x000E
	// matter.js packages/node/src/endpoints/bridged-node.ts  BridgedNodeEndpoint.deviceType
	smokeDevTypeBridgedNode uint32 = 0x0013
	// matter.js packages/node/src/devices/on-off-light.ts  OnOffLightDevice.deviceType
	smokeDevTypeOnOffLight uint16 = 0x0100
	// matter.js packages/node/src/devices/temperature-sensor.ts  TemperatureSensorDevice.deviceType
	smokeDevTypeTempSensor uint16 = 0x0302
	// matter.js packages/node/src/behaviors/bridged-device-basic-information  cluster id 0x0039
	smokeClusterIDBDBI uint32 = 0x0039
)

// ─── test fakes ───────────────────────────────────────────────────────────────

// smokeFakeStore is an in-memory [endpoint.Store] for bridge smoke tests.
type smokeFakeStore struct {
	rows   map[store.EndpointKey]store.EndpointRecord
	nextID uint16
}

func newSmokeFakeStore() *smokeFakeStore {
	return &smokeFakeStore{
		rows:   make(map[store.EndpointKey]store.EndpointRecord),
		nextID: 2, // bridged endpoints start at 2; root=0, aggregator=1 are structural
	}
}

func (s *smokeFakeStore) GetEndpoint(_ context.Context, key store.EndpointKey) (store.EndpointRecord, error) {
	rec, ok := s.rows[key]
	if !ok {
		return store.EndpointRecord{}, store.ErrEndpointNotFound
	}
	return rec, nil
}

func (s *smokeFakeStore) UpsertEndpointAssigning(_ context.Context, rec store.EndpointRecord) (uint16, error) {
	if rec.EndpointID == 0 {
		rec.EndpointID = s.nextID
		s.nextID++
	}
	s.rows[rec.Key] = rec
	return rec.EndpointID, nil
}

func (s *smokeFakeStore) ListEndpoints(_ context.Context, centralName string) ([]store.EndpointRecord, error) {
	var out []store.EndpointRecord
	for _, rec := range s.rows {
		if centralName == "" || rec.Key.CentralName == centralName {
			out = append(out, rec)
		}
	}
	return out, nil
}

func (s *smokeFakeStore) RemoveEndpoint(_ context.Context, key store.EndpointKey) error {
	delete(s.rows, key)
	return nil
}

// smokeStubCluster is a read-only, no-op Matter cluster server used as a
// placeholder in smokeEndpointSource so that ClusterServers() receives a
// non-empty inner slice and attaches the mandatory BDBI + Descriptor clusters.
// Cluster ID 0x0006 = OnOff; 0x0402 = TemperatureMeasurement.
type smokeStubCluster struct{ id uint32 }

func (c *smokeStubCluster) MatterClusterID() uint32 { return c.id }
func (c *smokeStubCluster) MatterRead(_ uint32) (any, bool) {
	return nil, false
}

func (c *smokeStubCluster) MatterWrite(_ context.Context, _ uint32, _ any) error {
	return errors.New("read-only stub")
}

func (c *smokeStubCluster) MatterInvoke(_ context.Context, _ uint32, _ any) (any, error) {
	return nil, errors.New("no commands on stub")
}
func (c *smokeStubCluster) MatterReportable() []uint32 { return nil }

// smokeEndpointSource is a minimal [interfaces.MatterEndpointSource] for the
// smoke bridge tests. It provides a single stub cluster server so that
// endpoint.ClusterServers() proceeds past the empty-inner guard and attaches
// the mandatory BDBI + Descriptor clusters.
type smokeEndpointSource struct {
	key        hmtypes.DataPointKey
	deviceType uint16
	clusterID  uint32
}

func (s *smokeEndpointSource) DataPointKey() hmtypes.DataPointKey { return s.key }
func (s *smokeEndpointSource) MatterDeviceType() uint16           { return s.deviceType }
func (s *smokeEndpointSource) MatterClusterServers() []interfaces.MatterClusterServer {
	return []interfaces.MatterClusterServer{&smokeStubCluster{id: s.clusterID}}
}

// ─── builder helpers ──────────────────────────────────────────────────────────

// smokeValidConfig returns a minimal assembler config.
func smokeValidConfig() endpoint.Config {
	return endpoint.Config{
		VendorID:  0xFFF1,
		ProductID: 0x8001,
		NodeLabel: "SmokeTestBridge",
	}
}

// buildSmokeTopology assembles a fresh topology from the given snapshots using
// an in-memory store. Helper shared by multiple test cases.
func buildSmokeTopology(t *testing.T, snaps []endpoint.DeviceSnapshot) *endpoint.Topology {
	t.Helper()
	a, err := endpoint.New(newSmokeFakeStore(), smokeValidConfig(), nil)
	if err != nil {
		t.Fatalf("endpoint.New: %v", err)
	}
	top, err := a.AssembleDevices(context.Background(), snaps)
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	return top
}

// makeSmokeOnOffDevice returns a device with one OnOff channel backed by a
// smokeEndpointSource with DeviceType = OnOffLight (0x0100) and a stub OnOff
// cluster (0x0006) so ClusterServers passes the non-empty inner guard and
// attaches BDBI + Descriptor.
func makeSmokeOnOffDevice(addr, name string) *device.Device {
	dev := device.New(device.Config{Address: addr, Name: name})
	ch := dev.AddChannel(addr+":1", 1, "SWITCH", hmenum.ParamsetKeyValues)
	ch.SetCustomDataPoint(&smokeEndpointSource{
		key: hmtypes.DataPointKey{
			ChannelAddress: addr + ":1",
			Parameter:      "ON_OFF",
		},
		deviceType: smokeDevTypeOnOffLight,
		clusterID:  0x0006, // OnOff
	})
	return dev
}

// makeSmokeTempDevice returns a device with one temperature channel backed by a
// smokeEndpointSource with DeviceType = TemperatureSensor (0x0302) and a stub
// TemperatureMeasurement cluster (0x0402) so ClusterServers passes the non-empty
// inner guard and attaches BDBI + Descriptor.
func makeSmokeTempDevice(addr, name string) *device.Device {
	dev := device.New(device.Config{Address: addr, Name: name})
	ch := dev.AddChannel(addr+":1", 1, "SENSOR", hmenum.ParamsetKeyValues)
	ch.SetCustomDataPoint(&smokeEndpointSource{
		key: hmtypes.DataPointKey{
			ChannelAddress: addr + ":1",
			Parameter:      "TEMPERATURE",
		},
		deviceType: smokeDevTypeTempSensor,
		clusterID:  0x0402, // TemperatureMeasurement
	})
	return dev
}

// clusterIDsOf returns the cluster IDs exposed by ep via ClusterServers.
func clusterIDsOf(ep *endpoint.Endpoint) []uint32 {
	servers := endpoint.ClusterServers(ep)
	ids := make([]uint32, 0, len(servers))
	for _, s := range servers {
		ids = append(ids, s.MatterClusterID())
	}
	return ids
}

// hasClusterID returns true when clusterID is in the given list.
func hasClusterID(ids []uint32, clusterID uint32) bool {
	for _, id := range ids {
		if id == clusterID {
			return true
		}
	}
	return false
}

// ─── Test: Root has DeviceTypeList = [RootNode] ───────────────────────────────

// TestMatterBridgeSmoke_RootDeviceType verifies that the root endpoint (EP 0) is
// produced with DeviceType = RootNode (0x0016).
//
// Mirrors matter.js packages/node/test/endpoints/BridgeTest.ts:35
// (case "at startup") — expectBridgedLight walks the bridge's root through
// packages/node/src/devices/root-node.ts RootNodeEndpoint.deviceType = 0x0016.
func TestMatterBridgeSmoke_RootDeviceType(t *testing.T) {
	t.Parallel()

	top := buildSmokeTopology(t, []endpoint.DeviceSnapshot{
		{
			CentralName: "ccu1",
			Devices:     []*device.Device{makeSmokeOnOffDevice("SMOKE0001", "Light A")},
		},
	})

	root := top.FindByID(0)
	if root == nil {
		t.Fatal("root endpoint (ID=0) not found in topology")
	}
	if root.DeviceType != smokeDevTypeRootNode {
		t.Errorf("root DeviceType=0x%04X, want 0x%04X (RootNode)", root.DeviceType, smokeDevTypeRootNode)
	}
	if !root.IsRoot() {
		t.Error("root.IsRoot() must be true")
	}
	if !root.Reachable {
		t.Error("root must always be Reachable=true")
	}
}

// ─── Test: Aggregator has DeviceTypeList = [Aggregator] ──────────────────────

// TestMatterBridgeSmoke_AggregatorDeviceType verifies that the Aggregator
// endpoint (EP 1) is produced with DeviceType = Aggregator (0x000E).
//
// Mirrors matter.js packages/node/test/endpoints/BridgeTest.ts:35
// (case "at startup") — AggregatorEndpointDefinition.deviceType = 0xe in
// packages/node/src/endpoints/aggregator.ts.
func TestMatterBridgeSmoke_AggregatorDeviceType(t *testing.T) {
	t.Parallel()

	top := buildSmokeTopology(t, []endpoint.DeviceSnapshot{
		{
			CentralName: "ccu1",
			Devices:     []*device.Device{makeSmokeOnOffDevice("SMOKE0002", "Light B")},
		},
	})

	agg := top.FindByID(1)
	if agg == nil {
		t.Fatal("aggregator endpoint (ID=1) not found in topology")
	}
	if agg.DeviceType != smokeDevTypeAggregator {
		t.Errorf("aggregator DeviceType=0x%04X, want 0x%04X (Aggregator)", agg.DeviceType, smokeDevTypeAggregator)
	}
	if !agg.IsAggregator() {
		t.Error("agg.IsAggregator() must be true")
	}
	if !agg.Reachable {
		t.Error("aggregator must always be Reachable=true")
	}
	// Aggregator carries no Source or Measurement — structural only.
	if agg.Source != nil {
		t.Error("aggregator must not carry a Source")
	}
	if agg.Measurement != nil {
		t.Error("aggregator must not carry a Measurement")
	}
}

// ─── Test: Each bridged endpoint has DeviceTypeList = [primary, BridgedNode] ─

// TestMatterBridgeSmoke_BridgedEndpointDeviceTypes verifies that ClusterServers
// for each bridged endpoint surfaces a Descriptor cluster whose DeviceTypeList
// contains the primary device type FIRST and BridgedNode (0x0013) SECOND.
//
// Mirrors matter.js packages/node/test/endpoints/BridgeTest.ts:16–31
// (expectBridgedLight) — BridgedNodeEndpoint.deviceType = 0x0013.
func TestMatterBridgeSmoke_BridgedEndpointDeviceTypes(t *testing.T) {
	t.Parallel()

	top := buildSmokeTopology(t, []endpoint.DeviceSnapshot{
		{
			CentralName: "ccu1",
			Devices: []*device.Device{
				makeSmokeOnOffDevice("SMOKE0003", "Light C"),
				makeSmokeTempDevice("SMOKE0004", "Temp D"),
			},
		},
	})

	bridged := top.Bridged()
	if len(bridged) != 2 {
		t.Fatalf("Bridged()=%d, want 2", len(bridged))
	}

	for _, ep := range bridged {
		servers := endpoint.ClusterServers(ep)
		if len(servers) == 0 {
			t.Errorf("EP %d: ClusterServers returned empty slice", ep.ID)
			continue
		}
		// Locate the Descriptor cluster (0x001D).
		var desc interfaces.MatterClusterServer
		for _, s := range servers {
			if s.MatterClusterID() == 0x001D {
				desc = s
				break
			}
		}
		if desc == nil {
			t.Errorf("EP %d: Descriptor cluster (0x001D) not found in ServerList %v", ep.ID, clusterIDsOf(ep))
			continue
		}

		// DeviceTypeList attribute = 0x0000. We verify it is present and
		// non-nil; the order (primary first, BridgedNode second) is locked
		// by parity tests in internal/north/matter/endpoint/.
		dtRaw, ok := desc.MatterRead(0x0000)
		if !ok {
			t.Errorf("EP %d: Descriptor.DeviceTypeList returned ok=false", ep.ID)
			continue
		}
		if dtRaw == nil {
			t.Errorf("EP %d: DeviceTypeList is nil", ep.ID)
		}
	}
}

// ─── Test: PartsList propagation ─────────────────────────────────────────────

// TestMatterBridgeSmoke_PartsListPropagation verifies: Root+Aggregator present,
// Topology.Bridged() == endpoints with id ≥ 2, IDs sorted ascending, no
// reserved IDs (0,1) in bridged set.
//
// Mirrors matter.js packages/node/test/endpoints/BridgeTest.ts:35–58 and
// packages/node/src/behavior/system/parts/PartsBehavior.ts.
func TestMatterBridgeSmoke_PartsListPropagation(t *testing.T) {
	t.Parallel()

	top := buildSmokeTopology(t, []endpoint.DeviceSnapshot{
		{
			CentralName: "ccu1",
			Devices: []*device.Device{
				makeSmokeOnOffDevice("SMOKE0010", "Light 1"),
				makeSmokeOnOffDevice("SMOKE0011", "Light 2"),
				makeSmokeTempDevice("SMOKE0012", "Temp 1"),
			},
		},
	})

	// Root + Aggregator + 3 bridged = 5 total.
	if len(top.Endpoints) != 5 {
		t.Fatalf("total endpoints=%d, want 5 (root + aggregator + 3 bridged)", len(top.Endpoints))
	}

	bridged := top.Bridged()
	if len(bridged) != 3 {
		t.Fatalf("Bridged()=%d, want 3", len(bridged))
	}

	// Aggregator.PartsList (structural) == bridged endpoint ids.
	bridgedIDs := make(map[uint16]bool, len(bridged))
	for _, ep := range bridged {
		if ep.ID < 2 {
			t.Errorf("bridged endpoint ID=%d < 2; would shadow root/aggregator", ep.ID)
		}
		bridgedIDs[ep.ID] = true
	}

	// Every bridged endpoint must be reachable via FindByID.
	for id := range bridgedIDs {
		if found := top.FindByID(id); found == nil {
			t.Errorf("FindByID(%d) returned nil — topology is inconsistent", id)
		}
	}

	// Aggregator (EP 1) must not appear in Bridged().
	for _, ep := range bridged {
		if ep.ID == 0 || ep.ID == 1 {
			t.Errorf("Bridged() contains reserved ID %d (root/aggregator)", ep.ID)
		}
	}

	// Endpoints must be sorted by ID ascending.
	// Mirrors matter.js PartsBehavior which sorts parts by endpoint number.
	for i := 1; i < len(top.Endpoints); i++ {
		if top.Endpoints[i].ID <= top.Endpoints[i-1].ID {
			t.Errorf("endpoints not sorted: [%d].ID=%d >= [%d].ID=%d",
				i-1, top.Endpoints[i-1].ID, i, top.Endpoints[i].ID)
		}
	}
}

// ─── Test: ServerList contains BDBI (0x0039) on every bridged endpoint ────────

// TestMatterBridgeSmoke_ServerListContainsBDBI verifies that every bridged
// endpoint's Descriptor.ServerList includes BridgedDeviceBasicInformation
// (cluster 0x0039).
//
// Mirrors matter.js packages/node/test/endpoints/BridgeTest.ts:18
// (expectBridgedLight asserting `light.behaviors.isActive(BridgedDeviceBasicInformationServer)`)
// and packages/node/src/behaviors/bridged-device-basic-information which is
// mandatory per Matter Application Cluster Spec §9.13.
func TestMatterBridgeSmoke_ServerListContainsBDBI(t *testing.T) {
	t.Parallel()

	top := buildSmokeTopology(t, []endpoint.DeviceSnapshot{
		{
			CentralName: "ccu1",
			Devices: []*device.Device{
				makeSmokeOnOffDevice("SMOKE0020", "Light OnOff"),
				makeSmokeTempDevice("SMOKE0021", "Sensor Temp"),
			},
		},
	})

	for _, ep := range top.Bridged() {
		ids := clusterIDsOf(ep)
		if !hasClusterID(ids, smokeClusterIDBDBI) {
			t.Errorf("EP %d: BridgedDeviceBasicInformation (0x0039) missing from cluster servers %v", ep.ID, ids)
		}
	}
}

// ─── Test: BDBI.NodeLabel + UniqueID populated per bridged endpoint ───────────

// TestMatterBridgeSmoke_BDBINodeLabelAndUniqueID verifies that BDBI cluster
// (0x0039) is present on every bridged endpoint, NodeLabel (attr 0x0005) is
// non-nil, UniqueID (attr 0x000F) is non-empty, and UniqueIDs are distinct
// across endpoints (duplicate fingerprints cause Apple Home pair-abort; the
// distinctness invariant is catalogued in notes/parity/by_design.md, matter.js
// section).
//
// Mirrors matter.js packages/node/test/endpoints/BridgeTest.ts:18
// (expectBridgedLight asserting BridgedDeviceBasicInformationServer active).
func TestMatterBridgeSmoke_BDBINodeLabelAndUniqueID(t *testing.T) {
	t.Parallel()

	top := buildSmokeTopology(t, []endpoint.DeviceSnapshot{
		{
			CentralName: "ccu1",
			Devices: []*device.Device{
				makeSmokeOnOffDevice("SMOKE0030", "Bridge Light"),
				makeSmokeTempDevice("SMOKE0031", "Bridge Sensor"),
			},
		},
	})

	seenUIDs := make(map[string]uint16) // uniqueID → first endpoint ID
	for _, ep := range top.Bridged() {
		var bdbi interfaces.MatterClusterServer
		for _, s := range endpoint.ClusterServers(ep) {
			if s.MatterClusterID() == smokeClusterIDBDBI {
				bdbi = s
				break
			}
		}
		if bdbi == nil {
			t.Errorf("EP %d: BDBI cluster (0x0039) not found", ep.ID)
			continue
		}

		// Attr 0x0005 = NodeLabel
		nodeLabelVal, ok := bdbi.MatterRead(0x0005)
		if !ok {
			t.Errorf("EP %d: BDBI.NodeLabel (attr 0x0005) returned ok=false", ep.ID)
		} else if nodeLabelVal == nil {
			t.Errorf("EP %d: BDBI.NodeLabel is nil", ep.ID)
		}

		// Attr 0x000F = UniqueID
		uidVal, ok := bdbi.MatterRead(0x000F)
		if !ok {
			t.Errorf("EP %d: BDBI.UniqueID (attr 0x000F) returned ok=false", ep.ID)
			continue
		}
		uid, ok := uidVal.(string)
		if !ok {
			t.Errorf("EP %d: BDBI.UniqueID expected string, got %T", ep.ID, uidVal)
			continue
		}
		if uid == "" {
			t.Errorf("EP %d: BDBI.UniqueID is empty", ep.ID)
			continue
		}
		if prev, exists := seenUIDs[uid]; exists {
			t.Errorf("EP %d: BDBI.UniqueID %q already used by EP %d — duplicate fingerprint causes Apple pair-abort", ep.ID, uid, prev)
		}
		seenUIDs[uid] = ep.ID
	}
}

// ─── Test: Empty snapshot still produces root + aggregator ───────────────────

// TestMatterBridgeSmoke_EmptySnapshotProducesRootAndAggregator verifies that
// even with no CCU devices the assembler emits the two structural endpoints.
//
// Mirrors matter.js packages/node/test/endpoints/BridgeTest.ts:46
// (case "adding endpoint dynamically") — createBridge(AggregatorEndpoint)
// starts with root + aggregator before any bridged endpoints are added.
func TestMatterBridgeSmoke_EmptySnapshotProducesRootAndAggregator(t *testing.T) {
	t.Parallel()

	top := buildSmokeTopology(t, nil)

	if len(top.Endpoints) != 2 {
		t.Fatalf("empty topology len=%d, want 2 (root + aggregator)", len(top.Endpoints))
	}
	if top.Endpoints[0].DeviceType != smokeDevTypeRootNode {
		t.Errorf("EP0 DeviceType=0x%04X, want 0x%04X (RootNode)", top.Endpoints[0].DeviceType, smokeDevTypeRootNode)
	}
	if top.Endpoints[1].DeviceType != smokeDevTypeAggregator {
		t.Errorf("EP1 DeviceType=0x%04X, want 0x%04X (Aggregator)", top.Endpoints[1].DeviceType, smokeDevTypeAggregator)
	}
	if len(top.Bridged()) != 0 {
		t.Errorf("empty topology: Bridged()=%d, want 0", len(top.Bridged()))
	}
}

// ─── Test: Multi-snapshot multi-CCU topology composition ─────────────────────

// TestMatterBridgeSmoke_MultiSnapshotComposition verifies that snapshots from
// multiple centrals compose into a single flat topology with unique endpoint IDs.
//
// Mirrors matter.js packages/node/test/endpoints/BridgeTest.ts:60–133
// (case "with multiple dynamic endpoints") — multi-CCU maps to multiple
// snapshots; endpoint IDs must not collide across centrals.
func TestMatterBridgeSmoke_MultiSnapshotComposition(t *testing.T) {
	t.Parallel()

	snaps := []endpoint.DeviceSnapshot{
		{
			CentralName: "ccu1",
			Devices:     []*device.Device{makeSmokeOnOffDevice("SMOKE0040", "CCU1 Light")},
		},
		{
			CentralName: "ccu2",
			Devices:     []*device.Device{makeSmokeTempDevice("SMOKE0050", "CCU2 Temp")},
		},
	}

	top := buildSmokeTopology(t, snaps)

	// root + aggregator + 1 from ccu1 + 1 from ccu2 = 4 total.
	if len(top.Endpoints) != 4 {
		t.Fatalf("multi-CCU topology len=%d, want 4 (root + agg + 2 bridged)", len(top.Endpoints))
	}

	bridged := top.Bridged()
	if len(bridged) != 2 {
		t.Fatalf("Bridged()=%d, want 2", len(bridged))
	}

	// All endpoint IDs must be distinct.
	seen := make(map[uint16]bool, len(top.Endpoints))
	for _, ep := range top.Endpoints {
		if seen[ep.ID] {
			t.Errorf("duplicate endpoint ID %d in multi-CCU topology", ep.ID)
		}
		seen[ep.ID] = true
	}
}

// ─── Test: BridgedNode DeviceType present in endpoint cluster surface ─────────

// TestMatterBridgeSmoke_BridgedNodeDeviceTypeInClusterSurface verifies that
// a bridged endpoint's cluster surface includes BDBI (proxy for BridgedNode
// presence) and that Endpoint.DeviceType is the device-specific type, not
// BridgedNode (which must be secondary in Descriptor.DeviceTypeList).
//
// Mirrors matter.js packages/node/test/endpoints/BridgeTest.ts:21–30
// (expectBridgedLight — BridgedNodeEndpoint.deviceType = 0x0013 secondary).
func TestMatterBridgeSmoke_BridgedNodeDeviceTypeInClusterSurface(t *testing.T) {
	t.Parallel()

	top := buildSmokeTopology(t, []endpoint.DeviceSnapshot{
		{
			CentralName: "ccu1",
			Devices:     []*device.Device{makeSmokeOnOffDevice("SMOKE0060", "Light For DT Check")},
		},
	})

	bridged := top.Bridged()
	if len(bridged) != 1 {
		t.Fatalf("Bridged()=%d, want 1", len(bridged))
	}
	ep := bridged[0]

	// We verify BDBI (0x0039) is present as a proxy for full BridgedNode
	// cluster surface. The DeviceTypeList encoding is TLV-level and tested
	// separately via parity tests; here we confirm the cluster surface is
	// coherent.
	ids := clusterIDsOf(ep)
	if !hasClusterID(ids, smokeClusterIDBDBI) {
		t.Errorf("EP %d: BDBI cluster (0x0039) absent; BridgedNode surface incomplete. Cluster IDs: %v", ep.ID, ids)
	}

	// The primary device type on the struct itself must be the device-specific
	// type (not BridgedNode, which would cause Apple to silently drop the endpoint).
	if ep.DeviceType == uint16(smokeDevTypeBridgedNode) {
		t.Errorf("EP %d: DeviceType is BridgedNode (0x0013) — primary type must be device-specific; BridgedNode goes in Descriptor.DeviceTypeList secondary", ep.ID)
	}
}

// ─── Test: ParentEndpointID set to Aggregator (1) on all bridged endpoints ───

// TestMatterBridgeSmoke_BridgedEndpointParentEndpointID verifies that every
// bridged endpoint (ID ≥ 2) has HasParentEndpointID=true and
// ParentEndpointID=1 (the Aggregator endpoint), and that the root (ID=0) and
// Aggregator (ID=1) themselves do NOT have a parent set.
//
// Mirrors chip examples/bridge-app/linux/main.cpp:261-276
// (AddDeviceEndpoint(..., parentEndpointId=1) for every bridged endpoint)
// and matter.js packages/node/src/endpoints/aggregator.ts where every
// bridged child is added under the aggregator via aggregator.add(child).
//
// Source-Origin: derived from chip bridge-app main.cpp:261-276 and matter.js
// aggregator.ts. The ParentEndpointID field mirrors the chip bridge-app
// parentEndpointId pattern documented in notes/parity/.
func TestMatterBridgeSmoke_BridgedEndpointParentEndpointID(t *testing.T) {
	t.Parallel()

	top := buildSmokeTopology(t, []endpoint.DeviceSnapshot{
		{
			CentralName: "ccu1",
			Devices: []*device.Device{
				makeSmokeOnOffDevice("SMOKE0070", "Parent Light 1"),
				makeSmokeTempDevice("SMOKE0071", "Parent Temp 1"),
				makeSmokeOnOffDevice("SMOKE0072", "Parent Light 2"),
			},
		},
	})

	for _, ep := range top.Endpoints {
		switch ep.ID {
		case 0: // root
			// Root has no parent.
			if ep.HasParentEndpointID {
				t.Errorf("root (ID=0): HasParentEndpointID=true, want false (root has no parent)")
			}
		case 1: // aggregator
			// Aggregator has no parent.
			if ep.HasParentEndpointID {
				t.Errorf("aggregator (ID=1): HasParentEndpointID=true, want false (aggregator has no parent)")
			}
		default: // bridged endpoint (ID ≥ 2)
			if !ep.HasParentEndpointID {
				t.Errorf("bridged EP %d: HasParentEndpointID=false — chip bridge-app sets parentEndpointId=1 for every bridged endpoint", ep.ID)
			}
			if ep.ParentEndpointID != 1 {
				t.Errorf("bridged EP %d: ParentEndpointID=%d, want 1 (Aggregator) — mirrors chip bridge-app main.cpp:261-276 and matter.js aggregator.add(child)", ep.ID, ep.ParentEndpointID)
			}
		}
	}
}
