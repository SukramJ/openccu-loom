// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package core_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/north/matter/cluster"
	"github.com/SukramJ/openccu-loom/internal/north/matter/cluster/core"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/interfaces"
)

func validBridgedConfig() core.BridgedConfig {
	return core.BridgedConfig{
		UniqueID:  "HM-42:1",
		NodeLabel: "Living Room Light",
		Reachable: true,
	}
}

func newValidBridged(t *testing.T) *core.BridgedDeviceBasicInformation {
	t.Helper()
	b, err := core.NewBridgedDeviceBasicInformation(validBridgedConfig())
	if err != nil {
		t.Fatalf("NewBridgedDeviceBasicInformation: %v", err)
	}
	return b
}

func TestBridgedBasicInfo_ValidationEmptyUniqueID(t *testing.T) {
	t.Parallel()
	cfg := validBridgedConfig()
	cfg.UniqueID = ""
	_, err := core.NewBridgedDeviceBasicInformation(cfg)
	if err == nil {
		t.Fatal("expected error for empty UniqueID, got nil")
	}
}

func TestBridgedBasicInfo_ValidationEmptyNodeLabel(t *testing.T) {
	t.Parallel()
	cfg := validBridgedConfig()
	cfg.NodeLabel = ""
	_, err := core.NewBridgedDeviceBasicInformation(cfg)
	if err == nil {
		t.Fatal("expected error for empty NodeLabel, got nil")
	}
}

func TestBridgedBasicInfo_ClusterID(t *testing.T) {
	t.Parallel()
	b := newValidBridged(t)
	if got := b.MatterClusterID(); got != 0x0039 {
		t.Fatalf("MatterClusterID = 0x%04X, want 0x0039", got)
	}
}

func TestBridgedBasicInfo_ClusterRevision(t *testing.T) {
	t.Parallel()
	b := newValidBridged(t)
	v, ok := b.MatterRead(cluster.AttrGlobalClusterRevision)
	if !ok {
		t.Fatal("ClusterRevision: ok=false")
	}
	if v.(uint16) != 5 {
		t.Fatalf("ClusterRevision = %v, want 5", v)
	}
}

func TestBridgedBasicInfo_ReadAllAttributes(t *testing.T) {
	t.Parallel()
	cfg := core.BridgedConfig{
		UniqueID:           "HM-1:2",
		NodeLabel:          "Test Device",
		VendorName:         "Homematic",
		VendorID:           0x00AB,
		ProductName:        "HM-CC-RT-DN",
		ProductID:          0x0001,
		HardwareVersion:    1,
		HardwareVersionStr: "1.0",
		SoftwareVersion:    2,
		SoftwareVersionStr: "2.0",
		ManufacturingDate:  "20240101",
		PartNumber:         "PN-001",
		ProductURL:         "https://example.com",
		ProductLabel:       "Thermostat",
		SerialNumber:       "SN123",
		ProductAppearance:  core.ProductAppearanceStruct{Finish: 1, PrimaryColor: 3},
		Reachable:          true,
	}
	b, err := core.NewBridgedDeviceBasicInformation(cfg)
	if err != nil {
		t.Fatalf("NewBridgedDeviceBasicInformation: %v", err)
	}

	attrIDs := []uint32{
		0x0001, // VendorName
		0x0002, // VendorID
		0x0003, // ProductName
		0x0004, // ProductID
		0x0005, // NodeLabel
		0x0007, // HardwareVersion
		0x0008, // HardwareVersionStr
		0x0009, // SoftwareVersion
		0x000A, // SoftwareVersionStr
		0x000B, // ManufacturingDate
		0x000C, // PartNumber
		0x000D, // ProductURL
		0x000E, // ProductLabel
		0x000F, // SerialNumber
		0x0011, // Reachable
		0x0012, // UniqueID
		0x0014, // ProductAppearance
		cluster.AttrGlobalFeatureMap,
		cluster.AttrGlobalClusterRevision,
	}
	for _, id := range attrIDs {
		v, ok := b.MatterRead(id)
		if !ok {
			t.Errorf("MatterRead(0x%04X) = (_, false), want true", id)
		}
		_ = v
	}
}

func TestBridgedBasicInfo_WriteNodeLabelValid(t *testing.T) {
	t.Parallel()
	b := newValidBridged(t)
	err := b.MatterWrite(context.Background(), 0x0005, "new-label", hmenum.CommandPriorityHigh)
	if err != nil {
		t.Fatalf("MatterWrite NodeLabel: %v", err)
	}
	v, _ := b.MatterRead(0x0005)
	if v.(string) != "new-label" {
		t.Fatalf("NodeLabel = %q, want new-label", v.(string))
	}
}

func TestBridgedBasicInfo_WriteNodeLabelTooLong(t *testing.T) {
	t.Parallel()
	b := newValidBridged(t)
	err := b.MatterWrite(context.Background(), 0x0005, strings.Repeat("z", 33), hmenum.CommandPriorityHigh)
	if err == nil {
		t.Fatal("expected error for NodeLabel > 32 bytes, got nil")
	}
}

func TestBridgedBasicInfo_WriteNodeLabelWrongType(t *testing.T) {
	t.Parallel()
	b := newValidBridged(t)
	err := b.MatterWrite(context.Background(), 0x0005, 42, hmenum.CommandPriorityHigh)
	if err == nil {
		t.Fatal("expected error for wrong type, got nil")
	}
}

func TestBridgedBasicInfo_WriteReadOnlyAttrReturnsError(t *testing.T) {
	t.Parallel()
	b := newValidBridged(t)
	ctx := context.Background()
	for _, attrID := range []uint32{0x0001, 0x0002, 0x0011, 0x0012} {
		err := b.MatterWrite(ctx, attrID, "x", hmenum.CommandPriorityHigh)
		if err == nil {
			t.Errorf("MatterWrite(0x%04X) expected error, got nil", attrID)
		}
		// errBridgedBasicInfoUnknown is unexported; verify a wrapped error is present.
		if unwrapped := errors.Unwrap(err); unwrapped == nil {
			t.Errorf("MatterWrite(0x%04X): expected wrapped sentinel, Unwrap returned nil", attrID)
		}
	}
}

func TestBridgedBasicInfo_SetReachableChange(t *testing.T) {
	t.Parallel()
	cfg := validBridgedConfig()
	cfg.Reachable = true
	b, err := core.NewBridgedDeviceBasicInformation(cfg)
	if err != nil {
		t.Fatalf("NewBridgedDeviceBasicInformation: %v", err)
	}
	changed := b.SetReachable(false)
	if !changed {
		t.Fatal("SetReachable(false) from true: expected changed=true")
	}
}

func TestBridgedBasicInfo_SetReachableSameValue(t *testing.T) {
	t.Parallel()
	cfg := validBridgedConfig()
	cfg.Reachable = true
	b, err := core.NewBridgedDeviceBasicInformation(cfg)
	if err != nil {
		t.Fatalf("NewBridgedDeviceBasicInformation: %v", err)
	}
	changed := b.SetReachable(true)
	if changed {
		t.Fatal("SetReachable(true) from true: expected changed=false")
	}
}

func TestBridgedBasicInfo_SetReachableReflectedInRead(t *testing.T) {
	t.Parallel()
	cfg := validBridgedConfig()
	cfg.Reachable = true
	b, err := core.NewBridgedDeviceBasicInformation(cfg)
	if err != nil {
		t.Fatalf("NewBridgedDeviceBasicInformation: %v", err)
	}
	b.SetReachable(false)
	v, ok := b.MatterRead(0x0011)
	if !ok {
		t.Fatal("Reachable: ok=false")
	}
	if v.(bool) != false {
		t.Fatal("Reachable = true, want false after SetReachable(false)")
	}
}

func TestBridgedBasicInfo_ConcurrentReadSetReachable(t *testing.T) {
	t.Parallel()
	b := newValidBridged(t)

	const goroutines = 20
	var wg sync.WaitGroup
	wg.Add(goroutines * 2)

	for i := range goroutines {
		go func(i int) {
			defer wg.Done()
			b.SetReachable(i%2 == 0)
		}(i)
		go func() {
			defer wg.Done()
			_, _ = b.MatterRead(0x0011)
		}()
	}
	wg.Wait()
}

// recordedEvent captures a single MatterEmitEvent call for assertions.
type recordedEvent struct {
	endpoint uint16
	cluster  uint32
	event    uint32
	data     any
	priority interface{}
}

// fakeEmitter implements [interfaces.MatterEventEmitter] by appending
// every call into a slice. Mirrors the GenericSwitch test pattern at
// `cluster/wire/genericswitch_test.go`.
type fakeEmitter struct {
	mu     sync.Mutex
	events []recordedEvent
}

func (f *fakeEmitter) MatterEmitEvent(endpoint uint16, clusterID, event uint32, data any, priority interfaces.MatterEventPriority) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, recordedEvent{endpoint: endpoint, cluster: clusterID, event: event, data: data, priority: priority})
}

// TestBridgedBasicInfo_SetReachableEmitsReachableChanged asserts that
// flipping reachable fires the Matter §9.13.6 ReachableChanged event
// (id 0x0003) at priority Critical, addressed to the configured
// endpoint with payload {ReachableNewValue: <new>}. Mirrors matter.js
// HEAD's BridgedDeviceBasicInformationServer behavior. Without this
// event Apple Home's HAP-service mapper caches the boot-time reachable
// value forever — ongoing CCU drop-outs never surface to the user.
func TestBridgedBasicInfo_SetReachableEmitsReachableChanged(t *testing.T) {
	t.Parallel()
	cfg := validBridgedConfig()
	cfg.Reachable = true
	b, err := core.NewBridgedDeviceBasicInformation(cfg)
	if err != nil {
		t.Fatalf("NewBridgedDeviceBasicInformation: %v", err)
	}
	emitter := &fakeEmitter{}
	b.SetMatterEventEmitter(emitter)
	b.SetEndpoint(7)

	if changed := b.SetReachable(false); !changed {
		t.Fatal("SetReachable(false) from true: expected changed=true")
	}
	emitter.mu.Lock()
	got := append([]recordedEvent(nil), emitter.events...)
	emitter.mu.Unlock()
	if len(got) != 1 {
		t.Fatalf("expected 1 emitted event, got %d", len(got))
	}
	ev := got[0]
	if ev.endpoint != 7 {
		t.Errorf("endpoint = %d, want 7", ev.endpoint)
	}
	if ev.cluster != 0x0039 {
		t.Errorf("cluster = 0x%04X, want 0x0039 (BridgedDeviceBasicInformation)", ev.cluster)
	}
	if ev.event != 0x0003 {
		t.Errorf("event = 0x%04X, want 0x0003 (ReachableChanged)", ev.event)
	}
	if ev.priority != interfaces.MatterEventPriorityCritical {
		t.Errorf("priority = %v, want Critical (matter.js bridged-device-basic-information.element.ts:55)", ev.priority)
	}
	payload, ok := ev.data.(core.ReachableChangedEvent)
	if !ok {
		t.Fatalf("data = %T, want ReachableChangedEvent", ev.data)
	}
	if payload.ReachableNewValue != false {
		t.Errorf("ReachableNewValue = true, want false (post-flip)")
	}
}

// TestBridgedBasicInfo_SetReachableNoEmitOnSameValue verifies the
// idempotent path: re-setting the same reachable value MUST NOT
// trigger an event. Apple HAP otherwise sees double "Reachable=true"
// reports and incorrectly debounces the next real change.
func TestBridgedBasicInfo_SetReachableNoEmitOnSameValue(t *testing.T) {
	t.Parallel()
	cfg := validBridgedConfig()
	cfg.Reachable = true
	b, err := core.NewBridgedDeviceBasicInformation(cfg)
	if err != nil {
		t.Fatalf("NewBridgedDeviceBasicInformation: %v", err)
	}
	emitter := &fakeEmitter{}
	b.SetMatterEventEmitter(emitter)

	if changed := b.SetReachable(true); changed {
		t.Fatal("SetReachable(true) from true: expected changed=false")
	}
	emitter.mu.Lock()
	defer emitter.mu.Unlock()
	if len(emitter.events) != 0 {
		t.Fatalf("expected 0 emitted events on no-op SetReachable, got %d", len(emitter.events))
	}
}

// TestBridgedBasicInfo_SetReachableNoEmitterIsSafe verifies the
// pre-wired path: flipping reachable before SetMatterEventEmitter has
// been called does NOT panic. Bridge topology assembly happens after
// the cluster is constructed; calls to SetReachable in that window
// (e.g. boot-time CCU connectivity probes) must degrade gracefully.
func TestBridgedBasicInfo_SetReachableNoEmitterIsSafe(t *testing.T) {
	t.Parallel()
	cfg := validBridgedConfig()
	cfg.Reachable = true
	b, err := core.NewBridgedDeviceBasicInformation(cfg)
	if err != nil {
		t.Fatalf("NewBridgedDeviceBasicInformation: %v", err)
	}
	if changed := b.SetReachable(false); !changed {
		t.Fatal("SetReachable(false) without emitter: expected changed=true")
	}
}

func TestBridgedBasicInfo_InvokeReturnsError(t *testing.T) {
	t.Parallel()
	b := newValidBridged(t)
	ctx := context.Background()
	for _, cmdID := range []uint32{0x00, 0xFF} {
		_, err := b.MatterInvoke(ctx, cmdID, nil, hmenum.CommandPriorityHigh)
		if err == nil {
			t.Errorf("MatterInvoke(0x%02X) expected error, got nil", cmdID)
		}
	}
}

// TestBridgedBasicInfo_MatterEvents verifies that MatterEvents returns
// exactly the ReachableChanged event ID (0x0003) and that the cluster
// satisfies the MatterClusterEventLister interface at compile time.
func TestBridgedBasicInfo_MatterEvents(t *testing.T) {
	t.Parallel()
	b := newValidBridged(t)
	var lister interfaces.MatterClusterEventLister = b
	events := lister.MatterEvents()
	if len(events) != 1 {
		t.Fatalf("MatterEvents() len = %d, want 1", len(events))
	}
	if events[0] != 0x0003 {
		t.Fatalf("MatterEvents()[0] = 0x%04X, want 0x0003 (ReachableChanged)", events[0])
	}
}

// TestBridgedBasicInfo_MatterEventsNotEmpty is a belt-and-suspenders
// guard: MatterEvents must always return a non-nil, non-empty slice so
// the dispatcher's EventList synthesis produces a valid TLV list.
func TestBridgedBasicInfo_MatterEventsNotEmpty(t *testing.T) {
	t.Parallel()
	b := newValidBridged(t)
	events := b.MatterEvents()
	if len(events) == 0 {
		t.Fatal("MatterEvents() returned empty slice — ReachableChanged must be present")
	}
}

// newMinimalBridged creates a BridgedDeviceBasicInformation with only the
// mandatory fields set; all optional fields are zero/empty.
func newMinimalBridged(t *testing.T) *core.BridgedDeviceBasicInformation {
	t.Helper()
	b, err := core.NewBridgedDeviceBasicInformation(core.BridgedConfig{
		UniqueID:  "min-unique-id",
		NodeLabel: "Minimal",
		Reachable: true,
	})
	if err != nil {
		t.Fatalf("NewBridgedDeviceBasicInformation: %v", err)
	}
	return b
}

// TestBridgedBasicInfo_MatterRead_EmptyOptionals verifies that reading optional
// attributes on a minimal device (all optional fields empty/zero) returns
// (nil, false).
func TestBridgedBasicInfo_MatterRead_EmptyOptionals(t *testing.T) {
	t.Parallel()
	b := newMinimalBridged(t)

	optionalAttrs := []struct {
		id   uint32
		name string
	}{
		{0x0001, "VendorName"},
		{0x0002, "VendorID"},
		{0x0003, "ProductName"},
		{0x0004, "ProductID"},
		{0x0007, "HardwareVersion"},
		{0x0008, "HardwareVersionStr"},
		{0x0009, "SoftwareVersion"},
		{0x000A, "SoftwareVersionStr"},
		{0x000B, "ManufacturingDate"},
		{0x000C, "PartNumber"},
		{0x000D, "ProductURL"},
		{0x000E, "ProductLabel"},
		{0x0014, "ProductAppearance"},
	}
	for _, a := range optionalAttrs {
		a := a
		t.Run(a.name, func(t *testing.T) {
			t.Parallel()
			v, ok := b.MatterRead(a.id)
			if ok || v != nil {
				t.Errorf("MatterRead(%s 0x%04X) with empty field: got (%v, %v), want (nil, false)", a.name, a.id, v, ok)
			}
		})
	}
}

// TestBridgedBasicInfo_MatterRead_UnknownAttr verifies the fall-through
// (nil, false) for an unknown attribute ID.
func TestBridgedBasicInfo_MatterRead_UnknownAttr(t *testing.T) {
	t.Parallel()
	b := newMinimalBridged(t)
	v, ok := b.MatterRead(0xBEEF)
	if ok || v != nil {
		t.Errorf("MatterRead(0xBEEF): got (%v, %v), want (nil, false)", v, ok)
	}
}

// TestBridgedBasicInfo_MatterAttributes_WithProductAppearance verifies that
// MatterAttributes includes ProductAppearance when the field is non-zero.
func TestBridgedBasicInfo_MatterAttributes_WithProductAppearance(t *testing.T) {
	t.Parallel()
	b, err := core.NewBridgedDeviceBasicInformation(core.BridgedConfig{
		UniqueID:          "ua-001",
		NodeLabel:         "Fancy Device",
		Reachable:         true,
		ProductAppearance: core.ProductAppearanceStruct{Finish: 2, PrimaryColor: 5},
	})
	if err != nil {
		t.Fatalf("NewBridgedDeviceBasicInformation: %v", err)
	}
	attrs := b.MatterAttributes()
	found := false
	for _, a := range attrs {
		if a == 0x0014 { // bridgedBasicInfoAttrProductAppearance
			found = true
			break
		}
	}
	if !found {
		t.Error("MatterAttributes: ProductAppearance (0x0014) missing when ProductAppearance is non-zero")
	}
}

// TestBridgedBasicInfo_MatterRead_ConfigurationVersion verifies that reading
// attribute 0x0018 (ConfigurationVersion) returns uint32(1).
func TestBridgedBasicInfo_MatterRead_ConfigurationVersion(t *testing.T) {
	t.Parallel()
	b := newMinimalBridged(t)
	v, ok := b.MatterRead(0x0018)
	if !ok {
		t.Fatal("MatterRead(0x0018 ConfigurationVersion): ok=false")
	}
	got, ok2 := v.(uint32)
	if !ok2 {
		t.Fatalf("type=%T, want uint32", v)
	}
	if got != 1 {
		t.Errorf("ConfigurationVersion=%d, want 1", got)
	}
}

// TestBridgedBasicInfo_MatterDataVersion verifies MatterDataVersion does not panic.
func TestBridgedBasicInfo_MatterDataVersion(t *testing.T) {
	t.Parallel()
	b, err := core.NewBridgedDeviceBasicInformation(validBridgedConfig())
	if err != nil {
		t.Fatalf("NewBridgedDeviceBasicInformation: %v", err)
	}
	_ = b.MatterDataVersion()
}

// TestBridgedBasicInfo_MatterReportable verifies Reachable (0x0011) is
// reportable for CCU drop-out detection.
func TestBridgedBasicInfo_MatterReportable(t *testing.T) {
	t.Parallel()
	b, err := core.NewBridgedDeviceBasicInformation(validBridgedConfig())
	if err != nil {
		t.Fatalf("NewBridgedDeviceBasicInformation: %v", err)
	}
	list := b.MatterReportable()
	have := make(map[uint32]bool)
	for _, a := range list {
		have[a] = true
	}
	if !have[0x0011] {
		t.Errorf("MatterReportable() missing Reachable (0x0011); list = %v", list)
	}
}

// TestBridgedBasicInfo_MatterAttributesFullSurface verifies all configured
// optional attributes appear in MatterAttributes.
func TestBridgedBasicInfo_MatterAttributesFullSurface(t *testing.T) {
	t.Parallel()
	cfg := validBridgedConfig()
	cfg.VendorName = "Acme"
	cfg.VendorID = 0x1234
	cfg.ProductName = "Sensor"
	cfg.ProductID = 0x5678
	cfg.HardwareVersion = 1
	cfg.HardwareVersionStr = "1.0"
	cfg.SoftwareVersion = 2
	cfg.SoftwareVersionStr = "2.0"
	cfg.ManufacturingDate = "20260101"
	cfg.PartNumber = "PN-99"
	cfg.ProductURL = "https://example.com"
	cfg.ProductLabel = "My Sensor"
	cfg.SerialNumber = "SN-007"
	cfg.UniqueID = "unique-007"
	b, err := core.NewBridgedDeviceBasicInformation(cfg)
	if err != nil {
		t.Fatalf("NewBridgedDeviceBasicInformation: %v", err)
	}
	list := b.MatterAttributes()
	have := make(map[uint32]bool)
	for _, a := range list {
		have[a] = true
	}
	optionals := map[uint32]string{
		0x0001: "VendorName",
		0x0002: "VendorID",
		0x0003: "ProductName",
		0x0004: "ProductID",
		0x0007: "HardwareVersion",
		0x0008: "HardwareVersionStr",
		0x0009: "SoftwareVersion",
		0x000A: "SoftwareVersionStr",
		0x000B: "ManufacturingDate",
		0x000C: "PartNumber",
		0x000D: "ProductURL",
		0x000E: "ProductLabel",
		0x000F: "SerialNumber",
		0x0011: "Reachable",
		0x0012: "UniqueID",
	}
	for id, name := range optionals {
		if !have[id] {
			t.Errorf("MatterAttributes() missing %s (0x%04X)", name, id)
		}
	}
}
