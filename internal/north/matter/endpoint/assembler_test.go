// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package endpoint_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/internal/north/matter/endpoint"
	"github.com/SukramJ/openccu-loom/internal/north/matter/store"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
	"github.com/SukramJ/openccu-loom/pkg/interfaces"
)

// ─── stub types ──────────────────────────────────────────────────────

// stubEndpointSource is a minimal [interfaces.MatterEndpointSource] for tests.
// It only implements the interface; it has no real cluster logic.
type stubEndpointSource struct {
	key        hmtypes.DataPointKey
	deviceType uint16
}

func (s *stubEndpointSource) DataPointKey() hmtypes.DataPointKey { return s.key }
func (s *stubEndpointSource) MatterDeviceType() uint16           { return s.deviceType }
func (s *stubEndpointSource) MatterClusterServers() []interfaces.MatterClusterServer {
	return nil
}

// stubMeasurementSource is a minimal [interfaces.MatterMeasurementSource]
// that does NOT implement MatterEndpointSource.
type stubMeasurementSource struct {
	key   hmtypes.DataPointKey
	class interfaces.MatterMeasurementClass
}

func (s *stubMeasurementSource) DataPointKey() hmtypes.DataPointKey { return s.key }
func (s *stubMeasurementSource) MatterMeasurementClass() interfaces.MatterMeasurementClass {
	return s.class
}

// ─── helpers ─────────────────────────────────────────────────────────

func validConfig() endpoint.Config {
	return endpoint.Config{
		VendorID:  0x1234,
		ProductID: 0x5678,
		NodeLabel: "TestBridge",
	}
}

// newDevice builds a minimal *device.Device with the given address and name.
func newDevice(addr, name string) *device.Device {
	return device.New(device.Config{
		Address: addr,
		Name:    name,
	})
}

// addChannel adds a channel to dev and returns it.
func addChannel(dev *device.Device, addr string, no int) *device.Channel {
	return dev.AddChannel(addr, no, "TEST", hmenum.ParamsetKeyValues)
}

// dpKey builds a DataPointKey suitable for test stubs.
func dpKey(channelAddr, parameter string) hmtypes.DataPointKey {
	return hmtypes.DataPointKey{
		ChannelAddress: channelAddr,
		Parameter:      parameter,
	}
}

// pressButtonDP builds an event-only press DP (a real generic.Button)
// the way the resolver does for KEY / KEY_TRANSCEIVER channels, so the
// assembler tests exercise the same shape production channels carry.
func pressButtonDP(channelAddr string, p hmenum.Parameter) *generic.Button {
	return generic.NewButton(generic.Spec{
		Key: hmtypes.DataPointKey{
			InterfaceID:    "iface",
			ChannelAddress: channelAddr,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(p),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeAction,
			Operations: hmenum.OperationsEvent,
		},
	})
}

// dpKeyAllowChecker allows exactly the listed dp_key values.
type dpKeyAllowChecker struct{ allowed map[string]bool }

func (c dpKeyAllowChecker) IsExposed(_ context.Context, key store.EndpointKey) (bool, error) {
	return c.allowed[key.DPKey], nil
}

// ─── Config.Validate ─────────────────────────────────────────────────

func TestConfigValidate_ZeroVendorID(t *testing.T) {
	t.Parallel()
	cfg := validConfig()
	cfg.VendorID = 0
	if err := cfg.Validate(); err == nil {
		t.Error("expected error for zero VendorID, got nil")
	}
}

func TestConfigValidate_ZeroProductID(t *testing.T) {
	t.Parallel()
	cfg := validConfig()
	cfg.ProductID = 0
	if err := cfg.Validate(); err == nil {
		t.Error("expected error for zero ProductID, got nil")
	}
}

func TestConfigValidate_EmptyNodeLabel(t *testing.T) {
	t.Parallel()
	for _, lbl := range []string{"", "   ", "\t"} {
		t.Run("label="+lbl, func(t *testing.T) {
			t.Parallel()
			cfg := validConfig()
			cfg.NodeLabel = lbl
			if err := cfg.Validate(); err == nil {
				t.Error("expected error for empty NodeLabel, got nil")
			}
		})
	}
}

func TestConfigValidate_OK(t *testing.T) {
	t.Parallel()
	if err := validConfig().Validate(); err != nil {
		t.Errorf("expected nil for valid config, got %v", err)
	}
}

// ─── New() ───────────────────────────────────────────────────────────

func TestNew_NilStoreReturnsError(t *testing.T) {
	t.Parallel()
	_, err := endpoint.New(nil, validConfig(), nil)
	if err == nil {
		t.Error("expected error for nil store, got nil")
	}
}

func TestNew_InvalidConfigReturnsError(t *testing.T) {
	t.Parallel()
	cfg := validConfig()
	cfg.VendorID = 0
	_, err := endpoint.New(newFakeStore(), cfg, nil)
	if err == nil {
		t.Error("expected error for invalid config, got nil")
	}
}

func TestNew_NilLoggerUsesDefault(t *testing.T) {
	t.Parallel()
	// Must not panic when logger is nil.
	a, err := endpoint.New(newFakeStore(), validConfig(), nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if a == nil {
		t.Error("expected non-nil Assembler")
	}
}

func TestNew_ValidConstruction(t *testing.T) {
	t.Parallel()
	a, err := endpoint.New(newFakeStore(), validConfig(), nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if a == nil {
		t.Error("Assembler should not be nil")
	}
}

// ─── Assemble — empty snapshots ──────────────────────────────────────

func TestAssemble_EmptySnapshotsProducesRootOnly(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	a, _ := endpoint.New(newFakeStore(), validConfig(), nil)

	top, err := a.Assemble(ctx, nil)
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	// Empty snapshot produces root (ID 0) + aggregator (ID 1) — 2 endpoints.
	if len(top.Endpoints) != 2 {
		t.Fatalf("len(Endpoints)=%d want 2 (root + aggregator)", len(top.Endpoints))
	}
	root := top.Endpoints[0]
	if !root.IsRoot() {
		t.Errorf("Endpoints[0].ID=%d, expected root (0)", root.ID)
	}
	if root.DeviceType != 0x0016 {
		t.Errorf("root DeviceType=0x%04X, want 0x0016 (RootNode)", root.DeviceType)
	}
	if !root.Reachable {
		t.Error("root Reachable should be true")
	}
	// Aggregator endpoint (ID 1, DeviceType 0x000E).
	agg := top.Endpoints[1]
	if !agg.IsAggregator() {
		t.Errorf("Endpoints[1].ID=%d, expected aggregator (1)", agg.ID)
	}
	if agg.DeviceType != 0x000E {
		t.Errorf("aggregator DeviceType=0x%04X, want 0x000E", agg.DeviceType)
	}
	if !agg.Reachable {
		t.Error("aggregator Reachable should be true")
	}
}

func TestAssemble_EmptyDeviceList_RootOnly(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	a, _ := endpoint.New(newFakeStore(), validConfig(), nil)

	snap := endpoint.Snapshot{
		CentralName: "ccu1",
		Devices:     nil,
	}
	top, err := a.Assemble(ctx, []endpoint.Snapshot{snap})
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	// Empty device list still produces root (ID 0) + aggregator (ID 1).
	if len(top.Endpoints) != 2 {
		t.Errorf("expected root+aggregator topology (2 endpoints), got %d endpoints", len(top.Endpoints))
	}
}

// ─── Assemble — custom DP (MatterEndpointSource) ─────────────────────

func TestAssemble_CustomDP_EndpointSource(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	const centralName = "ccu1"
	const devAddr = "ABC0001"
	const chAddr = "ABC0001:1"
	const chNo = 1
	const dpParam = "RGBW_LIGHT"
	const wantDeviceType = uint16(0x0101)

	dev := newDevice(devAddr, "Lampe")
	ch := addChannel(dev, chAddr, chNo)

	src := &stubEndpointSource{
		key:        dpKey(chAddr, dpParam),
		deviceType: wantDeviceType,
	}
	ch.SetCustomDataPoint(src)

	snap := endpoint.Snapshot{CentralName: centralName, Devices: []*device.Device{dev}}

	cfg := validConfig()
	fs := newFakeStore()
	a, _ := endpoint.New(fs, cfg, nil)

	top, err := a.Assemble(ctx, []endpoint.Snapshot{snap})
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}

	// root (ID 0) + aggregator (ID 1) + bridged (ID 2) = 3 endpoints.
	if len(top.Endpoints) != 3 {
		t.Fatalf("expected 3 endpoints (root + aggregator + bridged), got %d", len(top.Endpoints))
	}

	bridged := top.Endpoints[2]
	if bridged.ID != 2 {
		t.Errorf("bridged ID=%d, want 2", bridged.ID)
	}
	if bridged.DeviceType != wantDeviceType {
		t.Errorf("DeviceType=0x%04X, want 0x%04X", bridged.DeviceType, wantDeviceType)
	}
	if bridged.Reachable != dev.Available() {
		t.Errorf("Reachable=%v, want %v", bridged.Reachable, dev.Available())
	}
	if bridged.Source == nil {
		t.Error("Source should be non-nil for custom DP endpoint")
	}
	if bridged.Measurement != nil {
		t.Error("Measurement should be nil for custom DP endpoint")
	}
	if bridged.Channel == nil {
		t.Error("Channel should be non-nil")
	}

	// SourceKey fields.
	sk := bridged.SourceKey
	if sk.CentralName != centralName {
		t.Errorf("SourceKey.CentralName=%q, want %q", sk.CentralName, centralName)
	}
	if sk.DeviceAddress != devAddr {
		t.Errorf("SourceKey.DeviceAddress=%q, want %q", sk.DeviceAddress, devAddr)
	}
	if sk.ChannelNo != chNo {
		t.Errorf("SourceKey.ChannelNo=%d, want %d", sk.ChannelNo, chNo)
	}
	if sk.DPKind != store.DPKindCustom {
		t.Errorf("SourceKey.DPKind=%q, want %q", sk.DPKind, store.DPKindCustom)
	}
	if sk.DPKey != dpParam {
		t.Errorf("SourceKey.DPKey=%q, want %q", sk.DPKey, dpParam)
	}

	// Bridge VID/PID propagate from Config → Topology → each bridged
	// endpoint. Without this propagation the
	// BridgedDeviceBasicInformation cluster server defaults to the CSA
	// test pair (0xFFF1 / 0x8001) and an operator-configured
	// production VID/PID never surfaces on the wire.
	if bridged.BridgeVendorID != cfg.VendorID {
		t.Errorf("bridged BridgeVendorID=0x%04X, want 0x%04X", bridged.BridgeVendorID, cfg.VendorID)
	}
	if bridged.BridgeProductID != cfg.ProductID {
		t.Errorf("bridged BridgeProductID=0x%04X, want 0x%04X", bridged.BridgeProductID, cfg.ProductID)
	}
	// chip bridge-app/linux/main.cpp:276 sets parentEndpointId=1
	// (Aggregator) for every bridged endpoint via the call to
	// `emberAfSetDynamicEndpoint(..., parentEndpointId=1)`. Pin the
	// invariant so the assembler can never accidentally orphan a
	// bridged endpoint at the root level.
	if bridged.ParentEndpointID != 1 || !bridged.HasParentEndpointID {
		t.Errorf("bridged ParentEndpointID=%d HasParent=%v, want 1 / true",
			bridged.ParentEndpointID, bridged.HasParentEndpointID)
	}
	// Root + Aggregator (ID 0, ID 1) must NOT receive the BridgeVID
	// stamp — they carry root-side BasicInformation only.
	if top.Endpoints[0].BridgeVendorID != 0 {
		t.Errorf("root BridgeVendorID must stay 0, got 0x%04X", top.Endpoints[0].BridgeVendorID)
	}
	if top.Endpoints[1].BridgeVendorID != 0 {
		t.Errorf("aggregator BridgeVendorID must stay 0, got 0x%04X", top.Endpoints[1].BridgeVendorID)
	}
}

// ─── Assemble — calculated DP (MatterEndpointSource) ─────────────────

func TestAssemble_CalculatedDP_EndpointSource(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	const centralName = "ccu1"
	const devAddr = "SMOKE0001"
	const chAddr = "SMOKE0001:1"
	const dpParam = "SMOKE_CO_ALARM"
	const wantDeviceType = uint16(0x0076)

	dev := newDevice(devAddr, "Rauchmelder")
	ch := addChannel(dev, chAddr, 1)

	src := &stubEndpointSource{
		key:        dpKey(chAddr, dpParam),
		deviceType: wantDeviceType,
	}
	ch.AttachCalculatedDataPoint(src)

	snap := endpoint.Snapshot{CentralName: centralName, Devices: []*device.Device{dev}}
	a, _ := endpoint.New(newFakeStore(), validConfig(), nil)

	top, err := a.Assemble(ctx, []endpoint.Snapshot{snap})
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	// root (ID 0) + aggregator (ID 1) + bridged (ID 2) = 3 endpoints.
	if len(top.Endpoints) != 3 {
		t.Fatalf("expected 3 endpoints (root + aggregator + bridged), got %d", len(top.Endpoints))
	}

	bridged := top.Endpoints[2]
	if bridged.SourceKey.DPKind != store.DPKindCalculated {
		t.Errorf("DPKind=%q, want calculated", bridged.SourceKey.DPKind)
	}
	if bridged.Source == nil {
		t.Error("Source should be non-nil for calculated MatterEndpointSource")
	}
}

// ─── Assemble — measurement DP ───────────────────────────────────────

func TestAssemble_MeasurementDP_ExcludedWhenFlagOff(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	dev := newDevice("TEMP0001", "Thermometer")
	ch := addChannel(dev, "TEMP0001:1", 1)

	meas := &stubMeasurementSource{
		key:   dpKey("TEMP0001:1", "APPARENT_TEMPERATURE"),
		class: interfaces.MatterMeasurementTemperature,
	}
	ch.AttachCalculatedDataPoint(meas)

	// IncludeMeasurements = false (default).
	cfg := validConfig()
	cfg.IncludeMeasurements = false
	a, _ := endpoint.New(newFakeStore(), cfg, nil)

	top, err := a.Assemble(ctx, []endpoint.Snapshot{{CentralName: "ccu1", Devices: []*device.Device{dev}}})
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	// Measurement excluded: root (ID 0) + aggregator (ID 1) = 2 endpoints.
	if len(top.Endpoints) != 2 {
		t.Errorf("expected root+aggregator (measurement excluded), got %d endpoints", len(top.Endpoints))
	}
}

func TestAssemble_MeasurementDP_IncludedWhenFlagOn(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	dev := newDevice("TEMP0002", "Thermometer")
	ch := addChannel(dev, "TEMP0002:1", 1)

	meas := &stubMeasurementSource{
		key:   dpKey("TEMP0002:1", "APPARENT_TEMPERATURE"),
		class: interfaces.MatterMeasurementTemperature,
	}
	ch.AttachCalculatedDataPoint(meas)

	cfg := validConfig()
	cfg.IncludeMeasurements = true
	a, _ := endpoint.New(newFakeStore(), cfg, nil)

	top, err := a.Assemble(ctx, []endpoint.Snapshot{{CentralName: "ccu1", Devices: []*device.Device{dev}}})
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	// root (ID 0) + aggregator (ID 1) + measurement endpoint (ID 2) = 3 endpoints.
	if len(top.Endpoints) != 3 {
		t.Fatalf("expected 3 endpoints (root + aggregator + measurement), got %d", len(top.Endpoints))
	}

	bridged := top.Endpoints[2]
	if bridged.SourceKey.DPKind != store.DPKindMeasurement {
		t.Errorf("DPKind=%q, want measurement", bridged.SourceKey.DPKind)
	}
	if bridged.Source != nil {
		t.Error("Source should be nil for measurement-only endpoint")
	}
	if bridged.Measurement == nil {
		t.Error("Measurement should be non-nil for measurement endpoint")
	}
	if bridged.Channel == nil {
		t.Error("Channel should be non-nil for measurement endpoint")
	}
	if bridged.DeviceType != 0x0302 {
		t.Errorf("DeviceType=0x%04X, want 0x0302 (Temperature Sensor)", bridged.DeviceType)
	}
}

// ─── Button consolidation ────────────────────────────────────────────

// TestAssemble_ButtonChannelConsolidatesPressDPs verifies that every
// press-event DP of one channel lands on ONE GenericSwitch endpoint —
// a physical button is one Matter switch; the §1.13 press-cycle events
// only sequence correctly on a single cluster instance.
func TestAssemble_ButtonChannelConsolidatesPressDPs(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	dev := newDevice("BTN0001", "Taster")
	ch := addChannel(dev, "BTN0001:1", 1)
	for _, p := range []hmenum.Parameter{
		hmenum.ParameterPressShort, hmenum.ParameterPressLong,
		hmenum.ParameterPressCont, hmenum.ParameterPressLongRelease,
	} {
		ch.Put(pressButtonDP("BTN0001:1", p))
	}

	a, _ := endpoint.New(newFakeStore(), validConfig(), nil)
	top, err := a.Assemble(ctx, []endpoint.Snapshot{{CentralName: "ccu1", Devices: []*device.Device{dev}}})
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}

	bridged := top.Bridged()
	if len(bridged) != 1 {
		t.Fatalf("expected ONE consolidated button endpoint, got %d", len(bridged))
	}
	ep := bridged[0]
	if ep.SourceKey.DPKey != endpoint.ButtonGroupDPKey {
		t.Errorf("DPKey = %q, want %q", ep.SourceKey.DPKey, endpoint.ButtonGroupDPKey)
	}
	if ep.SourceKey.DPKind != store.DPKindGeneric {
		t.Errorf("DPKind = %q, want generic", ep.SourceKey.DPKind)
	}
	if ep.DeviceType != 0x000F {
		t.Errorf("DeviceType = 0x%04X, want 0x000F (Generic Switch)", ep.DeviceType)
	}
	long, ok := ep.Measurement.(interface{ MatterSwitchSupportsLongPress() bool })
	if !ok {
		t.Fatal("Measurement must expose the GenericSwitchSource surface")
	}
	if !long.MatterSwitchSupportsLongPress() {
		t.Error("group with PRESS_LONG members must advertise long press")
	}
}

// TestAssemble_MultiButtonRemote_OneEndpointPerChannel verifies that a
// multi-button remote keeps one endpoint per physical button: buttons
// are separate channels, and consolidation happens per channel only.
func TestAssemble_MultiButtonRemote_OneEndpointPerChannel(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	dev := newDevice("RC0001", "Fernbedienung")
	for chNo := 1; chNo <= 2; chNo++ {
		addr := fmt.Sprintf("RC0001:%d", chNo)
		ch := addChannel(dev, addr, chNo)
		ch.Put(pressButtonDP(addr, hmenum.ParameterPressShort))
		ch.Put(pressButtonDP(addr, hmenum.ParameterPressLong))
	}

	a, _ := endpoint.New(newFakeStore(), validConfig(), nil)
	top, err := a.Assemble(ctx, []endpoint.Snapshot{{CentralName: "ccu1", Devices: []*device.Device{dev}}})
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}

	bridged := top.Bridged()
	if len(bridged) != 2 {
		t.Fatalf("expected one endpoint per button channel (2), got %d", len(bridged))
	}
	channels := map[int]bool{}
	for _, ep := range bridged {
		if ep.SourceKey.DPKey != endpoint.ButtonGroupDPKey {
			t.Errorf("DPKey = %q, want %q", ep.SourceKey.DPKey, endpoint.ButtonGroupDPKey)
		}
		channels[ep.SourceKey.ChannelNo] = true
	}
	if !channels[1] || !channels[2] {
		t.Errorf("expected endpoints for channels 1 and 2, got %v", channels)
	}
}

// TestAssemble_ButtonGroupAllowlistPerMember verifies that the
// allowlist stays per press parameter: only allowed members join the
// group (a short-only exposure yields a group without long-press
// support), and a fully denied channel produces no endpoint.
func TestAssemble_ButtonGroupAllowlistPerMember(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	build := func() *device.Device {
		dev := newDevice("BTN0002", "Taster")
		ch := addChannel(dev, "BTN0002:1", 1)
		ch.Put(pressButtonDP("BTN0002:1", hmenum.ParameterPressShort))
		ch.Put(pressButtonDP("BTN0002:1", hmenum.ParameterPressLong))
		return dev
	}

	a, _ := endpoint.New(newFakeStore(), validConfig(), nil)
	a.SetExposureChecker(dpKeyAllowChecker{allowed: map[string]bool{"PRESS_SHORT": true}})
	top, err := a.Assemble(ctx, []endpoint.Snapshot{{CentralName: "ccu1", Devices: []*device.Device{build()}}})
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	bridged := top.Bridged()
	if len(bridged) != 1 {
		t.Fatalf("expected 1 endpoint (PRESS_SHORT allowed), got %d", len(bridged))
	}
	long, ok := bridged[0].Measurement.(interface{ MatterSwitchSupportsLongPress() bool })
	if !ok {
		t.Fatal("Measurement must expose the GenericSwitchSource surface")
	}
	if long.MatterSwitchSupportsLongPress() {
		t.Error("group must not advertise long press when the long member is not allowlisted")
	}

	denyAll, _ := endpoint.New(newFakeStore(), validConfig(), nil)
	denyAll.SetExposureChecker(dpKeyAllowChecker{allowed: map[string]bool{}})
	top2, err := denyAll.Assemble(ctx, []endpoint.Snapshot{{CentralName: "ccu1", Devices: []*device.Device{build()}}})
	if err != nil {
		t.Fatalf("Assemble (deny all): %v", err)
	}
	if got := len(top2.Bridged()); got != 0 {
		t.Errorf("expected no endpoint when every press member is denied, got %d", got)
	}
}

// TestAssemble_ButtonGroupReplacesLegacyPerPressRows verifies the
// deterministic store transition: persisted per-parameter endpoint
// rows from the previous composition vanish from the assembled set and
// are garbage-collected on a model-complete assembly, replaced by the
// single consolidated row.
func TestAssemble_ButtonGroupReplacesLegacyPerPressRows(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	dev := newDevice("BTN0003", "Taster")
	ch := addChannel(dev, "BTN0003:1", 1)
	ch.Put(pressButtonDP("BTN0003:1", hmenum.ParameterPressShort))
	ch.Put(pressButtonDP("BTN0003:1", hmenum.ParameterPressLong))

	fs := newFakeStore()
	legacyKey := func(param string) store.EndpointKey {
		return store.EndpointKey{
			CentralName:   "ccu1",
			DeviceAddress: "BTN0003",
			ChannelNo:     1,
			DPKind:        store.DPKindGeneric,
			DPKey:         param,
		}
	}
	for _, param := range []string{"PRESS_SHORT", "PRESS_LONG"} {
		if _, err := fs.UpsertEndpointAssigning(ctx, store.EndpointRecord{
			Key:        legacyKey(param),
			DeviceType: 0x000F,
		}); err != nil {
			t.Fatalf("seed legacy row: %v", err)
		}
	}

	a, _ := endpoint.New(fs, validConfig(), nil)
	if _, err := a.Assemble(ctx, []endpoint.Snapshot{
		{CentralName: "ccu1", Devices: []*device.Device{dev}, ModelComplete: true},
	}); err != nil {
		t.Fatalf("Assemble: %v", err)
	}

	for _, param := range []string{"PRESS_SHORT", "PRESS_LONG"} {
		if _, err := fs.GetEndpoint(ctx, legacyKey(param)); !errors.Is(err, store.ErrEndpointNotFound) {
			t.Errorf("legacy row %s must be garbage-collected, got err=%v", param, err)
		}
	}
	if _, err := fs.GetEndpoint(ctx, legacyKey(endpoint.ButtonGroupDPKey)); err != nil {
		t.Errorf("consolidated BUTTON row must be persisted, got err=%v", err)
	}
}

// ─── Stable IDs ──────────────────────────────────────────────────────

func TestAssemble_StableEndpointIDs(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	dev := newDevice("DEV0001", "Lamp")
	ch := addChannel(dev, "DEV0001:1", 1)
	src := &stubEndpointSource{key: dpKey("DEV0001:1", "LIGHT"), deviceType: 0x0100}
	ch.SetCustomDataPoint(src)

	snap := endpoint.Snapshot{CentralName: "ccu1", Devices: []*device.Device{dev}}

	fs := newFakeStore()
	a, _ := endpoint.New(fs, validConfig(), nil)

	top1, err := a.Assemble(ctx, []endpoint.Snapshot{snap})
	if err != nil {
		t.Fatalf("first Assemble: %v", err)
	}
	// Endpoints[2] is the first bridged endpoint (root=0, aggregator=1, bridged=2).
	id1 := top1.Endpoints[2].ID

	top2, err := a.Assemble(ctx, []endpoint.Snapshot{snap})
	if err != nil {
		t.Fatalf("second Assemble: %v", err)
	}
	id2 := top2.Endpoints[2].ID

	if id1 != id2 {
		t.Errorf("endpoint ID changed between runs: %d → %d", id1, id2)
	}
}

// ─── Fresh ID assignment on second source ────────────────────────────

func TestAssemble_FreshIDForNewSource(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	dev1 := newDevice("DEV0001", "Lamp1")
	ch1 := addChannel(dev1, "DEV0001:1", 1)
	src1 := &stubEndpointSource{key: dpKey("DEV0001:1", "LIGHT"), deviceType: 0x0100}
	ch1.SetCustomDataPoint(src1)
	snap1 := endpoint.Snapshot{CentralName: "ccu1", Devices: []*device.Device{dev1}}

	dev2 := newDevice("DEV0002", "Lamp2")
	ch2 := addChannel(dev2, "DEV0002:1", 1)
	src2 := &stubEndpointSource{key: dpKey("DEV0002:1", "LIGHT"), deviceType: 0x0100}
	ch2.SetCustomDataPoint(src2)
	snap2 := endpoint.Snapshot{CentralName: "ccu1", Devices: []*device.Device{dev2}}

	snapBoth := endpoint.Snapshot{CentralName: "ccu1", Devices: []*device.Device{dev1, dev2}}

	fs := newFakeStore()
	a, _ := endpoint.New(fs, validConfig(), nil)

	// First run: only src1.
	top1, err := a.Assemble(ctx, []endpoint.Snapshot{snap1})
	if err != nil {
		t.Fatalf("first Assemble: %v", err)
	}
	// Endpoints[2] is the first bridged endpoint (root=0, aggregator=1, bridged=2).
	idFirst := top1.Endpoints[2].ID

	// Second run: src1 + src2.
	top2, err := a.Assemble(ctx, []endpoint.Snapshot{snapBoth})
	if err != nil {
		t.Fatalf("second Assemble: %v", err)
	}

	// Find src1 and src2 IDs in the second run.
	var id1, id2 uint16
	for _, ep := range top2.Bridged() {
		if ep.SourceKey.DeviceAddress == "DEV0001" {
			id1 = ep.ID
		}
		if ep.SourceKey.DeviceAddress == "DEV0002" {
			id2 = ep.ID
		}
	}

	if id1 != idFirst {
		t.Errorf("src1 ID changed: was %d, now %d", idFirst, id1)
	}
	if id2 == 0 {
		t.Error("src2 should have received an ID")
	}
	if id1 == id2 {
		t.Errorf("src1 and src2 share ID %d, expected distinct IDs", id1)
	}

	_ = snap2 // kept for documentation
}

// ─── GC ──────────────────────────────────────────────────────────────

func TestAssemble_GCRemovesVanishedSources(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	dev1 := newDevice("GC0001", "DevA")
	ch1 := addChannel(dev1, "GC0001:1", 1)
	src1 := &stubEndpointSource{key: dpKey("GC0001:1", "LIGHT"), deviceType: 0x0100}
	ch1.SetCustomDataPoint(src1)

	dev2 := newDevice("GC0002", "DevB")
	ch2 := addChannel(dev2, "GC0002:1", 1)
	src2 := &stubEndpointSource{key: dpKey("GC0002:1", "LIGHT"), deviceType: 0x0100}
	ch2.SetCustomDataPoint(src2)

	fs := newFakeStore()
	a, _ := endpoint.New(fs, validConfig(), nil)

	// Run 1: both. ModelComplete vouches that the device list is the
	// full fleet, so the assembler may treat absent rows as vanished.
	snapBoth := endpoint.Snapshot{CentralName: "ccu1", Devices: []*device.Device{dev1, dev2}, ModelComplete: true}
	if _, err := a.Assemble(ctx, []endpoint.Snapshot{snapBoth}); err != nil {
		t.Fatalf("first Assemble: %v", err)
	}

	// Run 2: only dev1 (dev2 vanishes).
	snap1Only := endpoint.Snapshot{CentralName: "ccu1", Devices: []*device.Device{dev1}, ModelComplete: true}
	if _, err := a.Assemble(ctx, []endpoint.Snapshot{snap1Only}); err != nil {
		t.Fatalf("second Assemble: %v", err)
	}

	// The store should contain only one entry for "ccu1".
	rows, err := fs.ListEndpoints(ctx, "ccu1")
	if err != nil {
		t.Fatalf("ListEndpoints: %v", err)
	}
	if len(rows) != 1 {
		t.Errorf("store has %d rows for ccu1, want 1 (dev2 should be GC'd)", len(rows))
	}
	if len(rows) == 1 && rows[0].Key.DeviceAddress != "GC0001" {
		t.Errorf("remaining row has DeviceAddress=%q, want GC0001", rows[0].Key.DeviceAddress)
	}
}

// TestAssemble_GCSkipsCentralWithIncompleteModel is the boot-wipe
// regression: the daemon assembles the topology at start, before the
// readiness-gated CCU device load, so a registered central presents an
// EMPTY device list. That assembly must not delete the central's
// persisted endpoint-ID rows — otherwise every restart renumbers the
// bridged fleet and controllers lose their cached accessory mapping.
func TestAssemble_GCSkipsCentralWithIncompleteModel(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	dev := newDevice("BOOT0001", "Dev")
	ch := addChannel(dev, "BOOT0001:1", 1)
	src := &stubEndpointSource{key: dpKey("BOOT0001:1", "LIGHT"), deviceType: 0x0100}
	ch.SetCustomDataPoint(src)

	fs := newFakeStore()
	a, _ := endpoint.New(fs, validConfig(), nil)

	// Run 1 (previous daemon life): full model — persists the row.
	full := endpoint.Snapshot{CentralName: "ccu1", Devices: []*device.Device{dev}, ModelComplete: true}
	top1, err := a.Assemble(ctx, []endpoint.Snapshot{full})
	if err != nil {
		t.Fatalf("first Assemble: %v", err)
	}
	wantID := top1.Bridged()[0].ID

	// Run 2 (boot of the next daemon life): the central is registered
	// but its device load has not completed — empty Devices, not
	// model-complete. The persisted row must survive.
	boot := endpoint.Snapshot{CentralName: "ccu1", Devices: nil, ModelComplete: false}
	top2, err := a.Assemble(ctx, []endpoint.Snapshot{boot})
	if err != nil {
		t.Fatalf("boot Assemble: %v", err)
	}
	if got := len(top2.Bridged()); got != 0 {
		t.Fatalf("boot topology has %d bridged endpoints, want 0", got)
	}
	rows, err := fs.ListEndpoints(ctx, "ccu1")
	if err != nil {
		t.Fatalf("ListEndpoints: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("boot assemble deleted persisted rows: store has %d rows for ccu1, want 1", len(rows))
	}

	// Run 3 (ready reassemble): the device load completed — the source
	// must reappear under its persisted endpoint ID, not a fresh one.
	top3, err := a.Assemble(ctx, []endpoint.Snapshot{full})
	if err != nil {
		t.Fatalf("ready Assemble: %v", err)
	}
	bridged := top3.Bridged()
	if len(bridged) != 1 {
		t.Fatalf("ready topology has %d bridged endpoints, want 1", len(bridged))
	}
	if bridged[0].ID != wantID {
		t.Errorf("endpoint ID changed across the boot assemble: was %d, now %d", wantID, bridged[0].ID)
	}
}

// TestAssemble_GCOfVanishedStillWorksAfterIncompleteAssembly locks the
// second half of the boot-wipe fix: exempting model-incomplete
// snapshots must not disable GC permanently — once the central signals
// model-complete again, genuinely vanished devices are still reaped.
func TestAssemble_GCOfVanishedStillWorksAfterIncompleteAssembly(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	dev1 := newDevice("RDY0001", "DevA")
	ch1 := addChannel(dev1, "RDY0001:1", 1)
	src1 := &stubEndpointSource{key: dpKey("RDY0001:1", "LIGHT"), deviceType: 0x0100}
	ch1.SetCustomDataPoint(src1)

	dev2 := newDevice("RDY0002", "DevB")
	ch2 := addChannel(dev2, "RDY0002:1", 1)
	src2 := &stubEndpointSource{key: dpKey("RDY0002:1", "LIGHT"), deviceType: 0x0100}
	ch2.SetCustomDataPoint(src2)

	fs := newFakeStore()
	a, _ := endpoint.New(fs, validConfig(), nil)

	// Run 1 (previous daemon life): both devices persisted.
	both := endpoint.Snapshot{CentralName: "ccu1", Devices: []*device.Device{dev1, dev2}, ModelComplete: true}
	top1, err := a.Assemble(ctx, []endpoint.Snapshot{both})
	if err != nil {
		t.Fatalf("first Assemble: %v", err)
	}
	var wantDev1ID uint16
	for _, ep := range top1.Bridged() {
		if ep.SourceKey.DeviceAddress == "RDY0001" {
			wantDev1ID = ep.ID
		}
	}

	// Run 2 (boot): model incomplete — both rows survive.
	boot := endpoint.Snapshot{CentralName: "ccu1", Devices: nil, ModelComplete: false}
	if _, err := a.Assemble(ctx, []endpoint.Snapshot{boot}); err != nil {
		t.Fatalf("boot Assemble: %v", err)
	}
	if rows, _ := fs.ListEndpoints(ctx, "ccu1"); len(rows) != 2 {
		t.Fatalf("boot assemble deleted persisted rows: store has %d rows for ccu1, want 2", len(rows))
	}

	// Run 3 (ready reassemble): dev2 was genuinely removed while the
	// daemon was down. GC must reap it now that the model is complete,
	// and dev1 must keep its persisted ID.
	onlyDev1 := endpoint.Snapshot{CentralName: "ccu1", Devices: []*device.Device{dev1}, ModelComplete: true}
	top3, err := a.Assemble(ctx, []endpoint.Snapshot{onlyDev1})
	if err != nil {
		t.Fatalf("ready Assemble: %v", err)
	}
	rows, err := fs.ListEndpoints(ctx, "ccu1")
	if err != nil {
		t.Fatalf("ListEndpoints: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("store has %d rows for ccu1, want 1 (dev2 should be GC'd after ready)", len(rows))
	}
	if rows[0].Key.DeviceAddress != "RDY0001" {
		t.Errorf("remaining row has DeviceAddress=%q, want RDY0001", rows[0].Key.DeviceAddress)
	}
	bridged := top3.Bridged()
	if len(bridged) != 1 || bridged[0].ID != wantDev1ID {
		t.Errorf("dev1 endpoint ID changed across the boot assemble: want %d, got %+v", wantDev1ID, bridged)
	}
}

// ─── Multi-central ───────────────────────────────────────────────────

func TestAssemble_MultiCentralDistinctIDs(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	// Same device address, different centrals.
	const sharedAddr = "ABC0001"
	const chAddr = "ABC0001:1"

	dev1 := newDevice(sharedAddr, "Dev on CCU1")
	ch1 := addChannel(dev1, chAddr, 1)
	src1 := &stubEndpointSource{key: dpKey(chAddr, "LIGHT"), deviceType: 0x0100}
	ch1.SetCustomDataPoint(src1)

	dev2 := newDevice(sharedAddr, "Dev on CCU2")
	ch2 := addChannel(dev2, chAddr, 1)
	src2 := &stubEndpointSource{key: dpKey(chAddr, "LIGHT"), deviceType: 0x0100}
	ch2.SetCustomDataPoint(src2)

	snapCCU1 := endpoint.Snapshot{CentralName: "ccu1", Devices: []*device.Device{dev1}}
	snapCCU2 := endpoint.Snapshot{CentralName: "ccu2", Devices: []*device.Device{dev2}}

	fs := newFakeStore()
	a, _ := endpoint.New(fs, validConfig(), nil)

	top, err := a.Assemble(ctx, []endpoint.Snapshot{snapCCU1, snapCCU2})
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}

	bridged := top.Bridged()
	if len(bridged) != 2 {
		t.Fatalf("expected 2 bridged endpoints (one per central), got %d", len(bridged))
	}
	if bridged[0].ID == bridged[1].ID {
		t.Errorf("endpoints from different centrals share ID %d, expected distinct", bridged[0].ID)
	}

	// Both remain in the store under their respective central names.
	rowsCCU1, _ := fs.ListEndpoints(ctx, "ccu1")
	rowsCCU2, _ := fs.ListEndpoints(ctx, "ccu2")
	if len(rowsCCU1) != 1 {
		t.Errorf("ccu1: expected 1 store row, got %d", len(rowsCCU1))
	}
	if len(rowsCCU2) != 1 {
		t.Errorf("ccu2: expected 1 store row, got %d", len(rowsCCU2))
	}
}

// ─── Snapshot without CentralName ────────────────────────────────────

func TestAssemble_EmptyCentralNameReturnsError(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	dev := newDevice("DEV0001", "Dev")
	ch := addChannel(dev, "DEV0001:1", 1)
	src := &stubEndpointSource{key: dpKey("DEV0001:1", "LIGHT"), deviceType: 0x0100}
	ch.SetCustomDataPoint(src)

	a, _ := endpoint.New(newFakeStore(), validConfig(), nil)
	snap := endpoint.Snapshot{CentralName: "", Devices: []*device.Device{dev}}

	_, err := a.Assemble(ctx, []endpoint.Snapshot{snap})
	if err == nil {
		t.Error("expected error for empty CentralName, got nil")
	}
}

// ─── Store error propagation ─────────────────────────────────────────

func TestAssemble_StoreGetErrorPropagates(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	dev := newDevice("ERR0001", "Dev")
	ch := addChannel(dev, "ERR0001:1", 1)
	src := &stubEndpointSource{key: dpKey("ERR0001:1", "LIGHT"), deviceType: 0x0100}
	ch.SetCustomDataPoint(src)

	// failingStore returns errStoreError from GetEndpoint (not ErrNotFound).
	fs := &failingStore{newFakeStore()}
	a, _ := endpoint.New(fs, validConfig(), nil)

	snap := endpoint.Snapshot{CentralName: "ccu1", Devices: []*device.Device{dev}}
	_, err := a.Assemble(ctx, []endpoint.Snapshot{snap})
	if err == nil {
		t.Error("expected error from store, got nil")
	}
	if !errors.Is(err, errStoreError) {
		t.Errorf("expected errStoreError in chain, got %v", err)
	}
}

// ─── Topology methods ─────────────────────────────────────────────────

func TestTopology_FindByID_HitAndMiss(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	dev := newDevice("F0001", "Dev")
	ch := addChannel(dev, "F0001:1", 1)
	src := &stubEndpointSource{key: dpKey("F0001:1", "LIGHT"), deviceType: 0x0100}
	ch.SetCustomDataPoint(src)

	a, _ := endpoint.New(newFakeStore(), validConfig(), nil)
	top, err := a.Assemble(ctx, []endpoint.Snapshot{{CentralName: "ccu1", Devices: []*device.Device{dev}}})
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}

	// Hit: root.
	if top.FindByID(0) == nil {
		t.Error("FindByID(0) should return root, got nil")
	}
	// Hit: bridged (Endpoints[2] — root=0, aggregator=1, bridged=2).
	bridgedID := top.Endpoints[2].ID
	if top.FindByID(bridgedID) == nil {
		t.Errorf("FindByID(%d) returned nil, expected bridged endpoint", bridgedID)
	}
	// Miss.
	if top.FindByID(9999) != nil {
		t.Error("FindByID(9999) should return nil")
	}
}

func TestTopology_Bridged_EmptyWhenOnlyRoot(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	a, _ := endpoint.New(newFakeStore(), validConfig(), nil)
	top, _ := a.Assemble(ctx, nil)
	if got := top.Bridged(); got != nil {
		t.Errorf("Bridged() on root-only topology = %v, want nil", got)
	}
}

func TestTopology_Bridged_ExcludesRoot(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	dev := newDevice("B0001", "Dev")
	ch := addChannel(dev, "B0001:1", 1)
	src := &stubEndpointSource{key: dpKey("B0001:1", "LIGHT"), deviceType: 0x0100}
	ch.SetCustomDataPoint(src)

	a, _ := endpoint.New(newFakeStore(), validConfig(), nil)
	top, _ := a.Assemble(ctx, []endpoint.Snapshot{{CentralName: "ccu1", Devices: []*device.Device{dev}}})

	bridged := top.Bridged()
	if len(bridged) != 1 {
		t.Fatalf("Bridged()=%d, want 1", len(bridged))
	}
	if bridged[0].ID == 0 {
		t.Error("Bridged() must not contain the root endpoint")
	}
}

func TestEndpoint_IsRoot(t *testing.T) {
	t.Parallel()
	root := &endpoint.Endpoint{ID: 0}
	if !root.IsRoot() {
		t.Error("Endpoint{ID:0}.IsRoot() should be true")
	}
	bridged := &endpoint.Endpoint{ID: 1}
	if bridged.IsRoot() {
		t.Error("Endpoint{ID:1}.IsRoot() should be false")
	}
}

// ─── Topology metadata ───────────────────────────────────────────────

func TestAssemble_TopologyCarriesConfigMetadata(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	cfg := endpoint.Config{
		VendorID:  0xAAAA,
		ProductID: 0xBBBB,
		NodeLabel: "MyBridge",
	}
	a, _ := endpoint.New(newFakeStore(), cfg, nil)
	top, err := a.Assemble(ctx, nil)
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	if top.VendorID != 0xAAAA {
		t.Errorf("VendorID=0x%04X, want 0xAAAA", top.VendorID)
	}
	if top.ProductID != 0xBBBB {
		t.Errorf("ProductID=0x%04X, want 0xBBBB", top.ProductID)
	}
	if top.NodeLabel != "MyBridge" {
		t.Errorf("NodeLabel=%q, want MyBridge", top.NodeLabel)
	}
}
