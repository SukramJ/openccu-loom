// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package core_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/north/matter/cluster"
	"github.com/SukramJ/openccu-loom/internal/north/matter/cluster/core"
	"github.com/SukramJ/openccu-loom/internal/north/matter/tlv"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/interfaces"
)

func validBasicInfoConfig() core.Config {
	return core.Config{
		VendorID:    0x1234,
		ProductID:   0x5678,
		NodeLabel:   "openccu-loom-bridge",
		VendorName:  "openccu-loom",
		ProductName: "HomeMatic Bridge",
	}
}

func newValidBasicInfo(t *testing.T) *core.BasicInformation {
	t.Helper()
	b, err := core.NewBasicInformation(validBasicInfoConfig())
	if err != nil {
		t.Fatalf("NewBasicInformation: %v", err)
	}
	return b
}

func TestBasicInfo_ValidationVendorIDZero(t *testing.T) {
	t.Parallel()
	cfg := validBasicInfoConfig()
	cfg.VendorID = 0
	_, err := core.NewBasicInformation(cfg)
	if err == nil {
		t.Fatal("expected error for VendorID=0, got nil")
	}
}

func TestBasicInfo_ValidationProductIDZero(t *testing.T) {
	t.Parallel()
	cfg := validBasicInfoConfig()
	cfg.ProductID = 0
	_, err := core.NewBasicInformation(cfg)
	if err == nil {
		t.Fatal("expected error for ProductID=0, got nil")
	}
}

func TestBasicInfo_ValidationEmptyNodeLabel(t *testing.T) {
	t.Parallel()
	cfg := validBasicInfoConfig()
	cfg.NodeLabel = ""
	_, err := core.NewBasicInformation(cfg)
	if err == nil {
		t.Fatal("expected error for empty NodeLabel, got nil")
	}
}

func TestBasicInfo_DefaultLocation(t *testing.T) {
	t.Parallel()
	b := newValidBasicInfo(t)
	v, ok := b.MatterRead(0x0006)
	if !ok {
		t.Fatal("Location: ok=false")
	}
	bs := v.(tlv.BoundedString)
	if bs.Value != "XX" {
		t.Fatalf("Location = %q, want XX", bs.Value)
	}
	if bs.MaxBytes != 2 {
		t.Fatalf("Location MaxBytes = %d, want 2", bs.MaxBytes)
	}
}

func TestBasicInfo_DefaultDataModelRevision(t *testing.T) {
	t.Parallel()
	cfg := validBasicInfoConfig()
	cfg.DataModelRevision = 0
	b, err := core.NewBasicInformation(cfg)
	if err != nil {
		t.Fatalf("NewBasicInformation: %v", err)
	}
	v, ok := b.MatterRead(0x0000)
	if !ok {
		t.Fatal("DataModelRevision: ok=false")
	}
	if v.(uint16) != 19 {
		t.Fatalf("DataModelRevision = %v, want 19", v)
	}
}

func TestBasicInfo_DefaultMaxPathsPerInvoke(t *testing.T) {
	t.Parallel()
	cfg := validBasicInfoConfig()
	cfg.MaxPathsPerInvoke = 0
	b, err := core.NewBasicInformation(cfg)
	if err != nil {
		t.Fatalf("NewBasicInformation: %v", err)
	}
	v, ok := b.MatterRead(0x0016)
	if !ok {
		t.Fatal("MaxPathsPerInvoke: ok=false")
	}
	if v.(uint16) != 10 {
		t.Fatalf("MaxPathsPerInvoke = %v, want 10 (matter.js HEAD DEFAULT_MAX_PATHS_PER_INVOKE)", v)
	}
}

func TestBasicInfo_ClusterID(t *testing.T) {
	t.Parallel()
	b := newValidBasicInfo(t)
	if got := b.MatterClusterID(); got != 0x0028 {
		t.Fatalf("MatterClusterID = 0x%04X, want 0x0028", got)
	}
}

func TestBasicInfo_ClusterRevision(t *testing.T) {
	t.Parallel()
	b := newValidBasicInfo(t)
	v, ok := b.MatterRead(cluster.AttrGlobalClusterRevision)
	if !ok {
		t.Fatal("ClusterRevision: ok=false")
	}
	if v.(uint16) != 6 {
		t.Fatalf("ClusterRevision = %v, want 6", v)
	}
}

func TestBasicInfo_ReadSpecificationVersion(t *testing.T) {
	t.Parallel()
	b := newValidBasicInfo(t)
	v, ok := b.MatterRead(0x0015)
	if !ok {
		t.Fatal("SpecificationVersion: ok=false")
	}
	if v.(uint32) != cluster.SpecificationVersion {
		t.Fatalf("SpecificationVersion = 0x%08X, want 0x%08X", v.(uint32), cluster.SpecificationVersion)
	}
}

func TestBasicInfo_ReadAllAttributes(t *testing.T) {
	t.Parallel()
	b := newValidBasicInfo(t)
	// Mandatory attributes per Matter §11.1.6 that we always advertise
	// on the Root endpoint. LocalConfigDisabled (0x10) and
	// ConfigurationVersion (0x18) are intentionally OMITTED — matter.js's
	// bridge sample does not emit them on Root, and openccu-loom mirrors
	// that for Apple-pair compatibility (empirically verified). Reachable
	// (0x11) IS emitted on Root because Apple's HMAccessory.Reachable
	// flag depends on it (Run 15 vs Run 16 verification).
	mandatoryIDs := []uint32{
		0x0000, // DataModelRevision
		0x0001, // VendorName
		0x0002, // VendorID
		0x0003, // ProductName
		0x0004, // ProductID
		0x0005, // NodeLabel
		0x0006, // Location
		0x0007, // HardwareVersion
		0x0008, // HardwareVersionStr
		0x0009, // SoftwareVersion
		0x000A, // SoftwareVersionStr
		0x0011, // Reachable — Apple needs this on Root
		0x0012, // UniqueID
		0x0013, // CapabilityMinima
		0x0015, // SpecificationVersion
		0x0016, // MaxPathsPerInvoke
		cluster.AttrGlobalFeatureMap,
		cluster.AttrGlobalClusterRevision,
	}
	for _, id := range mandatoryIDs {
		v, ok := b.MatterRead(id)
		if !ok {
			t.Errorf("MatterRead(0x%04X) = (_, false), want true", id)
		}
		_ = v
	}
	// LocalConfigDisabled / ConfigurationVersion must NOT be advertised
	// when unset (matter.js parity).
	for _, id := range []uint32{0x0010, 0x0018} {
		if _, ok := b.MatterRead(id); ok {
			t.Errorf("MatterRead(0x%04X) returned true; should be unimplemented", id)
		}
	}
}

func TestBasicInfo_OptionalAttributesAbsentWhenUnset(t *testing.T) {
	t.Parallel()
	// Contract: optional attributes return (nil, false) when the
	// Config field is empty/zero — except SerialNumber, which
	// always returns a value (when no SerialNumber is configured,
	// the UniqueID-derived fallback is served so Apple's HAP-mapper
	// cache is pre-populated).
	optionalIDs := []uint32{
		0x000B, // ManufacturingDate
		0x000C, // PartNumber
		0x000D, // ProductURL
		0x000E, // ProductLabel
		0x0014, // ProductAppearance
	}
	b := newValidBasicInfo(t) // helper builds Config with none of the optional fields set
	for _, id := range optionalIDs {
		v, ok := b.MatterRead(id)
		if ok {
			t.Errorf("MatterRead(0x%04X) = (%v, true), want (nil, false) when unset", id, v)
		}
	}
}

// TestBasicInfo_SerialNumberFallback locks the SerialNumber fallback:
// when no SerialNumber is configured, MatterRead serves a deterministic
// fallback derived from UniqueID. Apple Home's HAP-mapper caches
// EP0:0x28:0x000F on the initial Subscribe; without the fallback
// the cache stays empty and `could not find cached attribute
// values for EP0:0x28:0x000F` is the symptom.
func TestBasicInfo_SerialNumberFallback(t *testing.T) {
	t.Parallel()
	b := newValidBasicInfo(t)
	v, ok := b.MatterRead(0x000F)
	if !ok {
		t.Fatalf("MatterRead(SerialNumber) returned false — fallback missing")
	}
	s, isString := v.(string)
	if !isString {
		t.Fatalf("MatterRead(SerialNumber) = %T, want string", v)
	}
	if s == "" {
		t.Fatalf("MatterRead(SerialNumber) returned empty fallback")
	}
	if len(s) > 32 {
		t.Errorf("SerialNumber fallback len=%d, exceeds spec ceiling 32", len(s))
	}
}

func TestBasicInfo_OptionalAttributesPresentWhenSet(t *testing.T) {
	t.Parallel()
	// Contract: optional attributes return (value, true) when the Config field is populated.
	cfg := validBasicInfoConfig()
	cfg.ManufacturingDate = "20260101"
	cfg.PartNumber = "PN-42"
	cfg.ProductURL = "https://example.com"
	cfg.ProductLabel = "My Bridge"
	cfg.SerialNumber = "SN-001"
	cfg.ProductAppearance = core.ProductAppearanceStruct{Finish: 1, PrimaryColor: 3}
	b, err := core.NewBasicInformation(cfg)
	if err != nil {
		t.Fatalf("NewBasicInformation: %v", err)
	}
	cases := []struct {
		id   uint32
		name string
	}{
		{0x000B, "ManufacturingDate"},
		{0x000C, "PartNumber"},
		{0x000D, "ProductURL"},
		{0x000E, "ProductLabel"},
		{0x000F, "SerialNumber"},
		{0x0014, "ProductAppearance"},
	}
	for _, tc := range cases {
		v, ok := b.MatterRead(tc.id)
		if !ok {
			t.Errorf("MatterRead(0x%04X %s) = (_, false), want (value, true) when set", tc.id, tc.name)
		}
		_ = v
	}
}

func TestBasicInfo_UniqueIDIs32CharHex(t *testing.T) {
	t.Parallel()
	cfg := validBasicInfoConfig()
	cfg.VendorID = 0x1234
	cfg.ProductID = 0x5678
	cfg.SerialNumber = ""
	b, err := core.NewBasicInformation(cfg)
	if err != nil {
		t.Fatalf("NewBasicInformation: %v", err)
	}
	v, ok := b.MatterRead(0x0012)
	if !ok {
		t.Fatal("UniqueID: ok=false")
	}
	uid := v.(string)
	if len(uid) != 32 {
		t.Fatalf("UniqueID len=%d, want 32 (matter.js BasicInformationServer.createUniqueId)", len(uid))
	}
	for _, c := range uid {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			t.Fatalf("UniqueID = %q contains non-hex char %q", uid, c)
		}
	}
}

func TestBasicInfo_UniqueIDDeterministicAcrossInstances(t *testing.T) {
	t.Parallel()
	cfg := validBasicInfoConfig()
	cfg.VendorID = 0x1234
	cfg.ProductID = 0x5678
	cfg.SerialNumber = "SN001"
	b1, _ := core.NewBasicInformation(cfg)
	b2, _ := core.NewBasicInformation(cfg)
	v1, _ := b1.MatterRead(0x0012)
	v2, _ := b2.MatterRead(0x0012)
	if v1.(string) != v2.(string) {
		t.Fatalf("UniqueID drift across same-config instances: %q vs %q", v1, v2)
	}
}

func TestBasicInfo_UniqueIDDiffersForDifferentSerial(t *testing.T) {
	t.Parallel()
	cfgA := validBasicInfoConfig()
	cfgA.SerialNumber = "SN001"
	cfgB := validBasicInfoConfig()
	cfgB.SerialNumber = "SN002"
	bA, _ := core.NewBasicInformation(cfgA)
	bB, _ := core.NewBasicInformation(cfgB)
	vA, _ := bA.MatterRead(0x0012)
	vB, _ := bB.MatterRead(0x0012)
	if vA.(string) == vB.(string) {
		t.Fatalf("UniqueID collision for different SerialNumber: %q == %q", vA, vB)
	}
}

func TestBasicInfo_WriteNodeLabelValid(t *testing.T) {
	t.Parallel()
	b := newValidBasicInfo(t)
	err := b.MatterWrite(context.Background(), 0x0005, "new-label", hmenum.CommandPriorityHigh)
	if err != nil {
		t.Fatalf("MatterWrite NodeLabel valid: %v", err)
	}
	v, _ := b.MatterRead(0x0005)
	if v.(tlv.BoundedString).Value != "new-label" {
		t.Fatalf("NodeLabel = %q, want new-label", v.(tlv.BoundedString).Value)
	}
}

func TestBasicInfo_WriteNodeLabelTooLong(t *testing.T) {
	t.Parallel()
	b := newValidBasicInfo(t)
	long := strings.Repeat("x", 33)
	err := b.MatterWrite(context.Background(), 0x0005, long, hmenum.CommandPriorityHigh)
	if err == nil {
		t.Fatal("expected error for NodeLabel > 32 bytes, got nil")
	}
}

func TestBasicInfo_WriteNodeLabelWrongType(t *testing.T) {
	t.Parallel()
	b := newValidBasicInfo(t)
	err := b.MatterWrite(context.Background(), 0x0005, 42, hmenum.CommandPriorityHigh)
	if err == nil {
		t.Fatal("expected error for wrong type, got nil")
	}
}

func TestBasicInfo_WriteLocationValid(t *testing.T) {
	t.Parallel()
	b := newValidBasicInfo(t)
	err := b.MatterWrite(context.Background(), 0x0006, "DE", hmenum.CommandPriorityHigh)
	if err != nil {
		t.Fatalf("MatterWrite Location=DE: %v", err)
	}
	v, _ := b.MatterRead(0x0006)
	if v.(tlv.BoundedString).Value != "DE" {
		t.Fatalf("Location = %q, want DE", v.(tlv.BoundedString).Value)
	}
}

func TestBasicInfo_WriteLocationWrongLength(t *testing.T) {
	t.Parallel()
	b := newValidBasicInfo(t)
	for _, s := range []string{"", "D", "DEU", "DEUU"} {
		err := b.MatterWrite(context.Background(), 0x0006, s, hmenum.CommandPriorityHigh)
		if err == nil {
			t.Errorf("expected error for Location=%q (len=%d), got nil", s, len(s))
		}
	}
}

func TestBasicInfo_WriteLocalConfigDisabledNoOp(t *testing.T) {
	t.Parallel()
	b := newValidBasicInfo(t)
	err := b.MatterWrite(context.Background(), 0x0010, true, hmenum.CommandPriorityHigh)
	if err != nil {
		t.Fatalf("MatterWrite LocalConfigDisabled: %v", err)
	}
}

func TestBasicInfo_WriteReadOnlyAttrReturnsErrBasicInfoUnknown(t *testing.T) {
	t.Parallel()
	b := newValidBasicInfo(t)
	ctx := context.Background()
	// VendorID (0x0002) is read-only.
	err := b.MatterWrite(ctx, 0x0002, uint16(0x9999), hmenum.CommandPriorityHigh)
	if err == nil {
		t.Fatal("expected error for read-only attr, got nil")
	}
	// errBasicInfoUnknown is unexported; verify the error chain contains a non-nil wrapped err.
	// We can only check that errors.Is doesn't panic and that err is non-nil (already checked).
	_ = fmt.Sprintf("%v", err) // ensure Stringer doesn't panic
}

func TestBasicInfo_WriteReadOnlyAttrSentinelViaIs(t *testing.T) {
	t.Parallel()
	b := newValidBasicInfo(t)
	err := b.MatterWrite(context.Background(), 0x0004, uint16(1), hmenum.CommandPriorityHigh)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	// errBasicInfoUnknown is unexported, but we can check it is wrapped via errors.Unwrap.
	unwrapped := errors.Unwrap(err)
	if unwrapped == nil {
		t.Fatal("expected wrapped sentinel error, Unwrap returned nil")
	}
}

func TestBasicInfo_SetNodeLabelValid(t *testing.T) {
	t.Parallel()
	b := newValidBasicInfo(t)
	if err := b.SetNodeLabel("updated"); err != nil {
		t.Fatalf("SetNodeLabel: %v", err)
	}
	v, _ := b.MatterRead(0x0005)
	if v.(tlv.BoundedString).Value != "updated" {
		t.Fatalf("NodeLabel = %q, want updated", v.(tlv.BoundedString).Value)
	}
}

func TestBasicInfo_SetNodeLabelTooLong(t *testing.T) {
	t.Parallel()
	b := newValidBasicInfo(t)
	err := b.SetNodeLabel(strings.Repeat("y", 33))
	if err == nil {
		t.Fatal("expected error for SetNodeLabel > 32 bytes, got nil")
	}
}

func TestBasicInfo_ReadConfigurationVersion(t *testing.T) {
	t.Parallel()
	cfg := validBasicInfoConfig()
	cfg.ConfigurationVersion = 42
	b, err := core.NewBasicInformation(cfg)
	if err != nil {
		t.Fatalf("NewBasicInformation: %v", err)
	}
	v, ok := b.MatterRead(0x0018)
	if !ok {
		t.Fatal("ConfigurationVersion: ok=false")
	}
	if v.(uint32) != 42 {
		t.Fatalf("ConfigurationVersion = %v, want 42", v)
	}
}

func TestBasicInfo_ReadProductAppearanceRoundTrip(t *testing.T) {
	t.Parallel()
	cfg := validBasicInfoConfig()
	cfg.ProductAppearance = core.ProductAppearanceStruct{Finish: 2, PrimaryColor: 5}
	b, err := core.NewBasicInformation(cfg)
	if err != nil {
		t.Fatalf("NewBasicInformation: %v", err)
	}
	v, ok := b.MatterRead(0x0014)
	if !ok {
		t.Fatal("ProductAppearance: ok=false")
	}
	pa := v.(core.ProductAppearanceStruct)
	if pa.Finish != 2 || pa.PrimaryColor != 5 {
		t.Fatalf("ProductAppearance = %+v, want {Finish:2 PrimaryColor:5}", pa)
	}
}

func TestBasicInfo_InvokeReturnsError(t *testing.T) {
	t.Parallel()
	b := newValidBasicInfo(t)
	ctx := context.Background()
	for _, cmdID := range []uint32{0x00, 0x01, 0xFF} {
		_, err := b.MatterInvoke(ctx, cmdID, nil, hmenum.CommandPriorityHigh)
		if err == nil {
			t.Errorf("MatterInvoke(0x%02X) expected error, got nil", cmdID)
		}
	}
}

// TestBasicInfo_MatterEventsContainsAllThree asserts that MatterEvents()
// returns all three event ids (StartUp=0x0000, ShutDown=0x0001,
// Leave=0x0002), satisfying the MatterClusterEventLister contract.
func TestBasicInfo_MatterEventsContainsAllThree(t *testing.T) {
	t.Parallel()
	b := newValidBasicInfo(t)
	events := b.MatterEvents()
	want := map[uint32]string{
		0x0000: "StartUp",
		0x0001: "ShutDown",
		0x0002: "Leave",
	}
	got := make(map[uint32]bool, len(events))
	for _, ev := range events {
		got[ev] = true
	}
	for id, name := range want {
		if !got[id] {
			t.Errorf("MatterEvents() missing %s (0x%04X)", name, id)
		}
	}
}

// TestBasicInfo_EmitStartUp_FiresEvent asserts that EmitStartUp fires
// the Matter §11.1.8.1 StartUp event (cluster=0x0028, event=0x0000,
// priority=Critical) with SoftwareVersion matching the cluster config.
// Mirrors matter.js basic-information.element.ts:84-90.
func TestBasicInfo_EmitStartUp_FiresEvent(t *testing.T) {
	t.Parallel()
	cfg := validBasicInfoConfig()
	cfg.SoftwareVersion = 42
	b, err := core.NewBasicInformation(cfg)
	if err != nil {
		t.Fatalf("NewBasicInformation: %v", err)
	}
	emitter := &fakeEmitter{}
	b.SetMatterEventEmitter(emitter)
	b.SetEndpoint(0)

	b.EmitStartUp()

	emitter.mu.Lock()
	got := append([]recordedEvent(nil), emitter.events...)
	emitter.mu.Unlock()

	if len(got) != 1 {
		t.Fatalf("expected 1 emitted event, got %d", len(got))
	}
	ev := got[0]
	if ev.endpoint != 0 {
		t.Errorf("endpoint = %d, want 0", ev.endpoint)
	}
	if ev.cluster != 0x0028 {
		t.Errorf("cluster = 0x%04X, want 0x0028 (BasicInformation)", ev.cluster)
	}
	if ev.event != 0x0000 {
		t.Errorf("event = 0x%04X, want 0x0000 (StartUp)", ev.event)
	}
	if ev.priority != interfaces.MatterEventPriorityCritical {
		t.Errorf("priority = %v, want Critical", ev.priority)
	}
	payload, ok := ev.data.(core.StartUpEvent)
	if !ok {
		t.Fatalf("data = %T, want core.StartUpEvent", ev.data)
	}
	if payload.SoftwareVersion != 42 {
		t.Errorf("SoftwareVersion = %d, want 42", payload.SoftwareVersion)
	}
}

// TestBasicInfo_EmitShutDown_FiresEvent asserts that EmitShutDown fires
// the Matter §11.1.8.2 ShutDown event (cluster=0x0028, event=0x0001,
// priority=Critical) with an empty payload. Mirrors matter.js
// basic-information.element.ts:91-95.
func TestBasicInfo_EmitShutDown_FiresEvent(t *testing.T) {
	t.Parallel()
	b := newValidBasicInfo(t)
	emitter := &fakeEmitter{}
	b.SetMatterEventEmitter(emitter)
	b.SetEndpoint(0)

	b.EmitShutDown()

	emitter.mu.Lock()
	got := append([]recordedEvent(nil), emitter.events...)
	emitter.mu.Unlock()

	if len(got) != 1 {
		t.Fatalf("expected 1 emitted event, got %d", len(got))
	}
	ev := got[0]
	if ev.cluster != 0x0028 {
		t.Errorf("cluster = 0x%04X, want 0x0028", ev.cluster)
	}
	if ev.event != 0x0001 {
		t.Errorf("event = 0x%04X, want 0x0001 (ShutDown)", ev.event)
	}
	if ev.priority != interfaces.MatterEventPriorityCritical {
		t.Errorf("priority = %v, want Critical", ev.priority)
	}
	if _, ok := ev.data.(core.ShutDownEvent); !ok {
		t.Fatalf("data = %T, want core.ShutDownEvent", ev.data)
	}
}

// TestBasicInfo_EmitLeave_FiresEvent asserts that EmitLeave fires the
// Matter §11.1.8.3 Leave event (cluster=0x0028, event=0x0002, priority=Info)
// with the supplied FabricIndex. Mirrors matter.js
// basic-information.element.ts:96-105.
func TestBasicInfo_EmitLeave_FiresEvent(t *testing.T) {
	t.Parallel()
	b := newValidBasicInfo(t)
	emitter := &fakeEmitter{}
	b.SetMatterEventEmitter(emitter)
	b.SetEndpoint(0)

	b.EmitLeave(3)

	emitter.mu.Lock()
	got := append([]recordedEvent(nil), emitter.events...)
	emitter.mu.Unlock()

	if len(got) != 1 {
		t.Fatalf("expected 1 emitted event, got %d", len(got))
	}
	ev := got[0]
	if ev.cluster != 0x0028 {
		t.Errorf("cluster = 0x%04X, want 0x0028", ev.cluster)
	}
	if ev.event != 0x0002 {
		t.Errorf("event = 0x%04X, want 0x0002 (Leave)", ev.event)
	}
	if ev.priority != interfaces.MatterEventPriorityInfo {
		t.Errorf("priority = %v, want Info", ev.priority)
	}
	payload, ok := ev.data.(core.LeaveEvent)
	if !ok {
		t.Fatalf("data = %T, want core.LeaveEvent", ev.data)
	}
	if payload.FabricIndex != 3 {
		t.Errorf("FabricIndex = %d, want 3", payload.FabricIndex)
	}
}

// TestBasicInfo_EmitStartUp_NoOpWhenEmitterNil asserts no panic when
// emitter is unwired.
func TestBasicInfo_EmitStartUp_NoOpWhenEmitterNil(t *testing.T) {
	t.Parallel()
	b := newValidBasicInfo(t)
	b.EmitStartUp()
}

// TestBasicInfo_EmitShutDown_NoOpWhenEmitterNil asserts no panic when
// emitter is unwired.
func TestBasicInfo_EmitShutDown_NoOpWhenEmitterNil(t *testing.T) {
	t.Parallel()
	b := newValidBasicInfo(t)
	b.EmitShutDown()
}

// TestBasicInfo_EmitLeave_NoOpWhenEmitterNil asserts no panic when
// emitter is unwired.
func TestBasicInfo_EmitLeave_NoOpWhenEmitterNil(t *testing.T) {
	t.Parallel()
	b := newValidBasicInfo(t)
	b.EmitLeave(1)
}

// TestBasicInfo_CapabilityMinima_FloorsAtSpecMinimum pins the
// Matter §11.1.5.18 constraint "3 to 10000" with default 3 on both
// fields. The zero value {0, 0} on the wire makes strict controllers
// (Apple Home) read SubscriptionsPerFabric=0 and refuse the
// subscription pipeline against the bridge.
//
// Mirrors matter.js HEAD packages/model/src/standard/elements/
// basic-information.element.ts:165-169 (`CapabilityMinima.
// CaseSessionsPerFabric default 3`, `... SubscriptionsPerFabric
// default 3`) and the `setDefault` floor in BasicInformationServer.ts.
func TestBasicInfo_CapabilityMinima_FloorsAtSpecMinimum(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name              string
		in                core.CapabilityMinimaStruct
		wantCaseSessions  uint16
		wantSubscriptions uint16
	}{
		{"zero", core.CapabilityMinimaStruct{}, 3, 3},
		{"both_below_floor", core.CapabilityMinimaStruct{CaseSessionsPerFabric: 1, SubscriptionsPerFabric: 2}, 3, 3},
		{"case_only_zero", core.CapabilityMinimaStruct{CaseSessionsPerFabric: 0, SubscriptionsPerFabric: 5}, 3, 5},
		{"sub_only_zero", core.CapabilityMinimaStruct{CaseSessionsPerFabric: 4, SubscriptionsPerFabric: 0}, 4, 3},
		{"both_at_floor", core.CapabilityMinimaStruct{CaseSessionsPerFabric: 3, SubscriptionsPerFabric: 3}, 3, 3},
		{"both_above_floor", core.CapabilityMinimaStruct{CaseSessionsPerFabric: 10, SubscriptionsPerFabric: 8}, 10, 8},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cfg := validBasicInfoConfig()
			cfg.CapabilityMinima = tc.in
			b, err := core.NewBasicInformation(cfg)
			if err != nil {
				t.Fatalf("NewBasicInformation: %v", err)
			}
			v, ok := b.MatterRead(0x0013)
			if !ok {
				t.Fatal("MatterRead(0x0013): ok=false")
			}
			got, ok := v.(core.CapabilityMinimaStruct)
			if !ok {
				t.Fatalf("MatterRead type = %T, want CapabilityMinimaStruct", v)
			}
			if got.CaseSessionsPerFabric != tc.wantCaseSessions {
				t.Errorf("CaseSessionsPerFabric = %d, want %d (floor 3)", got.CaseSessionsPerFabric, tc.wantCaseSessions)
			}
			if got.SubscriptionsPerFabric != tc.wantSubscriptions {
				t.Errorf("SubscriptionsPerFabric = %d, want %d (floor 3)", got.SubscriptionsPerFabric, tc.wantSubscriptions)
			}
			// Bug-J guard: never let zero reach the wire — strict
			// controllers interpret 0 as "no subscriptions allowed"
			// and silently refuse the subscription path.
			if got.CaseSessionsPerFabric == 0 {
				t.Error("CaseSessionsPerFabric = 0 — Bug-J regression: Apple Home reads 0 and refuses subscription path")
			}
			if got.SubscriptionsPerFabric == 0 {
				t.Error("SubscriptionsPerFabric = 0 — Bug-J regression: Apple Home reads 0 and refuses subscription path")
			}
		})
	}
}

// captureLog redirects slog.Default() to a debug-level buffer for the
// duration of the test. Not safe to run in parallel with other tests that
// mutate slog.Default.
func captureLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	prev := slog.Default()
	t.Cleanup(func() { slog.SetDefault(prev) })
	var buf bytes.Buffer
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	return &buf
}

// TestBasicInfo_ValidateLogsEmptyVendorName verifies that constructing
// BasicInformation with an empty VendorName logs a debug diagnostic but
// does not return an error — the validation is advisory only.
func TestBasicInfo_ValidateLogsEmptyVendorName(t *testing.T) {
	buf := captureLog(t)
	cfg := validBasicInfoConfig()
	cfg.VendorName = ""
	_, err := core.NewBasicInformation(cfg)
	if err != nil {
		t.Fatalf("NewBasicInformation with empty VendorName must not error, got: %v", err)
	}
	if !strings.Contains(buf.String(), "VendorName") {
		t.Fatalf("expected debug diagnostic mentioning VendorName, got: %s", buf.String())
	}
}

// TestBasicInfo_ValidateLogsEmptyProductName verifies the debug diagnostic
// for a missing ProductName without blocking construction.
func TestBasicInfo_ValidateLogsEmptyProductName(t *testing.T) {
	buf := captureLog(t)
	cfg := validBasicInfoConfig()
	cfg.ProductName = ""
	_, err := core.NewBasicInformation(cfg)
	if err != nil {
		t.Fatalf("NewBasicInformation with empty ProductName must not error, got: %v", err)
	}
	if !strings.Contains(buf.String(), "ProductName") {
		t.Fatalf("expected debug diagnostic mentioning ProductName, got: %s", buf.String())
	}
}

// TestBasicInfo_ValidateLogsEmptyHardwareVersionStr verifies the debug
// diagnostic for a missing HardwareVersionStr without blocking construction.
func TestBasicInfo_ValidateLogsEmptyHardwareVersionStr(t *testing.T) {
	buf := captureLog(t)
	cfg := validBasicInfoConfig()
	cfg.HardwareVersionStr = ""
	_, err := core.NewBasicInformation(cfg)
	if err != nil {
		t.Fatalf("NewBasicInformation with empty HardwareVersionStr must not error, got: %v", err)
	}
	if !strings.Contains(buf.String(), "HardwareVersionStr") {
		t.Fatalf("expected debug diagnostic mentioning HardwareVersionStr, got: %s", buf.String())
	}
}

// TestBasicInfo_ValidateNoDiagnosticWhenFieldsSet verifies that a
// fully-populated config produces no validation diagnostics for the fields
// covered by validateBasicInfoAttributes (even at debug level).
func TestBasicInfo_ValidateNoDiagnosticWhenFieldsSet(t *testing.T) {
	buf := captureLog(t)
	cfg := validBasicInfoConfig()
	cfg.VendorName = "Test Vendor"
	cfg.ProductName = "Test Product"
	cfg.HardwareVersionStr = "1.0"
	_, err := core.NewBasicInformation(cfg)
	if err != nil {
		t.Fatalf("NewBasicInformation: %v", err)
	}
	if strings.Contains(buf.String(), "matter.basic_information.validate") {
		t.Fatalf("expected no validation diagnostics for well-formed config, got: %s", buf.String())
	}
}

// TestBasicInfo_PersistentWriteHook_FiresOnNodeLabelWrite verifies that a
// Matter write to NodeLabel (0x0005) invokes the hook registered via
// SetOnPersistentWrite with the cluster's current NodeLabel/Location, so
// the daemon can persist a commissioner-set label across restarts.
func TestBasicInfo_PersistentWriteHook_FiresOnNodeLabelWrite(t *testing.T) {
	t.Parallel()
	b := newValidBasicInfo(t)
	var calls int
	var gotLabel, gotLoc string
	b.SetOnPersistentWrite(func(nodeLabel, location string) {
		calls++
		gotLabel = nodeLabel
		gotLoc = location
	})

	if err := b.MatterWrite(context.Background(), 0x0005, "living-room", hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("MatterWrite NodeLabel: %v", err)
	}
	if calls != 1 {
		t.Fatalf("hook fired %d times, want 1", calls)
	}
	if gotLabel != "living-room" {
		t.Errorf("hook nodeLabel = %q, want living-room", gotLabel)
	}
	if gotLoc != "XX" {
		t.Errorf("hook location = %q, want XX (unchanged default)", gotLoc)
	}
}

// TestBasicInfo_PersistentWriteHook_FiresOnLocationWrite verifies that a
// Matter write to Location (0x0006) invokes the hook with the cluster's
// current NodeLabel/Location.
func TestBasicInfo_PersistentWriteHook_FiresOnLocationWrite(t *testing.T) {
	t.Parallel()
	b := newValidBasicInfo(t)
	var calls int
	var gotLabel, gotLoc string
	b.SetOnPersistentWrite(func(nodeLabel, location string) {
		calls++
		gotLabel = nodeLabel
		gotLoc = location
	})

	if err := b.MatterWrite(context.Background(), 0x0006, "DE", hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("MatterWrite Location: %v", err)
	}
	if calls != 1 {
		t.Fatalf("hook fired %d times, want 1", calls)
	}
	if gotLoc != "DE" {
		t.Errorf("hook location = %q, want DE", gotLoc)
	}
	if gotLabel != "openccu-loom-bridge" {
		t.Errorf("hook nodeLabel = %q, want unchanged default", gotLabel)
	}
}

// TestBasicInfo_PersistentWriteHook_NotFiredOnLocalConfigDisabledWrite
// verifies that a write to LocalConfigDisabled (0x0010) does NOT invoke
// the persistence hook — only NodeLabel and Location are persisted
// (Matter §11.1.6 "N" quality attributes the daemon round-trips).
func TestBasicInfo_PersistentWriteHook_NotFiredOnLocalConfigDisabledWrite(t *testing.T) {
	t.Parallel()
	b := newValidBasicInfo(t)
	var calls int
	b.SetOnPersistentWrite(func(string, string) { calls++ })

	if err := b.MatterWrite(context.Background(), 0x0010, true, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("MatterWrite LocalConfigDisabled: %v", err)
	}
	if calls != 0 {
		t.Fatalf("hook fired %d times for LocalConfigDisabled write, want 0", calls)
	}
}

// TestBasicInfo_PersistentWriteHook_NotFiredBySetNodeLabel verifies that
// the out-of-band SetNodeLabel restore path does NOT invoke the
// persistence hook — restores must not echo back into the store.
func TestBasicInfo_PersistentWriteHook_NotFiredBySetNodeLabel(t *testing.T) {
	t.Parallel()
	b := newValidBasicInfo(t)
	var calls int
	b.SetOnPersistentWrite(func(string, string) { calls++ })

	if err := b.SetNodeLabel("restored-label"); err != nil {
		t.Fatalf("SetNodeLabel: %v", err)
	}
	if calls != 0 {
		t.Fatalf("hook fired %d times for out-of-band SetNodeLabel, want 0", calls)
	}
}

// TestBasicInfo_PersistentWriteHook_NotFiredBySetLocation verifies that
// the out-of-band SetLocation restore path does NOT invoke the
// persistence hook.
func TestBasicInfo_PersistentWriteHook_NotFiredBySetLocation(t *testing.T) {
	t.Parallel()
	b := newValidBasicInfo(t)
	var calls int
	b.SetOnPersistentWrite(func(string, string) { calls++ })

	if err := b.SetLocation("FR"); err != nil {
		t.Fatalf("SetLocation: %v", err)
	}
	if calls != 0 {
		t.Fatalf("hook fired %d times for out-of-band SetLocation, want 0", calls)
	}
}

// TestBasicInfo_PersistentWriteHook_DetachedByNil verifies that passing
// nil to SetOnPersistentWrite detaches any previously registered hook and
// subsequent writes do not panic.
func TestBasicInfo_PersistentWriteHook_DetachedByNil(t *testing.T) {
	t.Parallel()
	b := newValidBasicInfo(t)
	var calls int
	b.SetOnPersistentWrite(func(string, string) { calls++ })
	b.SetOnPersistentWrite(nil)

	if err := b.MatterWrite(context.Background(), 0x0005, "no-hook", hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("MatterWrite NodeLabel with detached hook: %v", err)
	}
	if calls != 0 {
		t.Fatalf("hook fired %d times after detach, want 0", calls)
	}
}

// fakeEventEmitter records the last emitted event for BasicInformation event assertions.
type fakeEventEmitter struct {
	endpoint  uint16
	clusterID uint32
	eventID   uint32
	payload   any
	priority  interfaces.MatterEventPriority
}

func (f *fakeEventEmitter) MatterEmitEvent(ep uint16, clID, evID uint32, payload any, pri interfaces.MatterEventPriority) {
	f.endpoint = ep
	f.clusterID = clID
	f.eventID = evID
	f.payload = payload
	f.priority = pri
}

// TestBasicInfo_MatterEventsIncludesReachableChanged verifies that
// MatterEvents() includes the ReachableChanged event (0x0003) so the IM
// dispatcher synthesises the correct EventList attribute for the cluster.
func TestBasicInfo_MatterEventsIncludesReachableChanged(t *testing.T) {
	t.Parallel()
	bi, err := core.NewBasicInformation(core.Config{
		VendorID:  0xFFF1,
		ProductID: 0x8000,
		NodeLabel: "test-bridge",
	})
	if err != nil {
		t.Fatalf("NewBasicInformation: %v", err)
	}

	events := bi.MatterEvents()
	const wantID uint32 = 0x0003
	found := slices.Contains(events, wantID)
	if !found {
		t.Errorf("MatterEvents() = %v, want to contain 0x%04X (ReachableChanged)", events, wantID)
	}
}

// TestBasicInfo_EmitReachableChanged verifies that EmitReachableChanged fires
// the ReachableChanged event (id 0x0003) with the correct payload and
// priority, and is a no-op when no emitter is wired.
func TestBasicInfo_EmitReachableChanged(t *testing.T) {
	t.Parallel()
	bi, err := core.NewBasicInformation(core.Config{
		VendorID:  0xFFF1,
		ProductID: 0x8000,
		NodeLabel: "test-bridge",
	})
	if err != nil {
		t.Fatalf("NewBasicInformation: %v", err)
	}

	// No-op without emitter wired — must not panic.
	bi.EmitReachableChanged(false)

	// Wire emitter and verify.
	em := &fakeEventEmitter{}
	bi.SetMatterEventEmitter(em)
	bi.SetEndpoint(0)
	bi.EmitReachableChanged(true)

	const wantCluster uint32 = 0x0028
	const wantEvent uint32 = 0x0003
	if em.clusterID != wantCluster {
		t.Errorf("emitted clusterID=0x%04X, want 0x%04X", em.clusterID, wantCluster)
	}
	if em.eventID != wantEvent {
		t.Errorf("emitted eventID=0x%04X, want 0x%04X (ReachableChanged)", em.eventID, wantEvent)
	}
	if em.priority != interfaces.MatterEventPriorityInfo {
		t.Errorf("priority=%v, want Info", em.priority)
	}
	payload, ok := em.payload.(core.ReachableChangedEvent)
	if !ok {
		t.Fatalf("payload type=%T, want ReachableChangedEvent", em.payload)
	}
	if !payload.ReachableNewValue {
		t.Errorf("ReachableNewValue=%v, want true", payload.ReachableNewValue)
	}
}

// TestBasicInfo_ValidationVendorIDAboveFFF4 verifies that NewBasicInformation
// rejects VendorID values in the range 0xFFF5–0xFFFF. These are reserved and
// not valid device identity values; using them would cause commissioners to
// reject the device or assign it incorrect ecosystem privileges.
// Mirrors matter.js packages/node/src/behaviors/basic-information/
// basic-information-validators.ts:32 (#3978):
// vendorId === 0 || vendorId > 0xfff4 → ImplementationError.
func TestBasicInfo_ValidationVendorIDAboveFFF4(t *testing.T) {
	t.Parallel()
	for _, vid := range []uint16{0xFFF5, 0xFFFF} {
		t.Run(fmt.Sprintf("VendorID=0x%04X", vid), func(t *testing.T) {
			t.Parallel()
			cfg := validBasicInfoConfig()
			cfg.VendorID = vid
			_, err := core.NewBasicInformation(cfg)
			if err == nil {
				t.Fatalf("NewBasicInformation with VendorID=0x%04X: expected error, got nil", vid)
			}
		})
	}
}

// TestBasicInfo_ValidationVendorIDFFF4Accepted verifies that VendorID=0xFFF4
// (the inclusive upper boundary of the valid 0x0001–0xFFF4 range) is accepted.
// Mirrors matter.js packages/node/src/behaviors/basic-information/
// basic-information-validators.ts:32 (#3978).
func TestBasicInfo_ValidationVendorIDFFF4Accepted(t *testing.T) {
	t.Parallel()
	cfg := validBasicInfoConfig()
	cfg.VendorID = 0xFFF4
	_, err := core.NewBasicInformation(cfg)
	if err != nil {
		t.Fatalf("NewBasicInformation with VendorID=0xFFF4: expected no error, got %v", err)
	}
}

// TestBasicInfo_ValidationVendorID0001Accepted verifies that VendorID=0x0001
// (the inclusive lower boundary of the valid range) is accepted.
// Mirrors matter.js packages/node/src/behaviors/basic-information/
// basic-information-validators.ts:32 (#3978):
// 0x0000 is reserved; 0x0001 is the first valid device identity VendorID.
func TestBasicInfo_ValidationVendorID0001Accepted(t *testing.T) {
	t.Parallel()
	cfg := validBasicInfoConfig()
	cfg.VendorID = 0x0001
	_, err := core.NewBasicInformation(cfg)
	if err != nil {
		t.Fatalf("NewBasicInformation with VendorID=0x0001: expected no error, got %v", err)
	}
}

// TestBasicInfo_MatterRead_UnknownAttr verifies the fall-through (nil, false)
// for an unknown attribute ID.
func TestBasicInfo_MatterRead_UnknownAttr(t *testing.T) {
	t.Parallel()
	bi := newValidBasicInfo(t)
	v, ok := bi.MatterRead(0xDEAD)
	if ok || v != nil {
		t.Errorf("MatterRead(0xDEAD): got (%v, %v), want (nil, false)", v, ok)
	}
}

// TestBasicInfo_MatterRead_SerialTruncated verifies that when the uniqueID
// component produces a long serial, basicInfoSerialFromUniqueID truncates it.
// uniqueID() always returns a 32-char hex string (> 16 chars) so the uid[:16]
// branch always executes here.
func TestBasicInfo_MatterRead_SerialTruncated(t *testing.T) {
	t.Parallel()
	bi := newValidBasicInfo(t)
	v, ok := bi.MatterRead(0x000F) // basicInfoAttrSerialNumber
	if !ok {
		t.Fatal("MatterRead(SerialNumber): ok=false")
	}
	s, ok2 := v.(string)
	if !ok2 {
		t.Fatalf("MatterRead(SerialNumber): type=%T, want string", v)
	}
	if len(s) > 16 {
		t.Errorf("MatterRead(SerialNumber): len=%d > 16 (truncation did not apply)", len(s))
	}
}

// TestBasicInfo_MatterWrite_LocationTypeMismatch exercises the error branch
// when MatterWrite(Location) receives a non-string value.
func TestBasicInfo_MatterWrite_LocationTypeMismatch(t *testing.T) {
	t.Parallel()
	bi := newValidBasicInfo(t)
	const attrLocation = 0x0006
	err := bi.MatterWrite(context.Background(), attrLocation, uint32(42), hmenum.CommandPriorityHigh)
	if err == nil {
		t.Fatal("MatterWrite(Location, uint32): expected error, got nil")
	}
}

// TestBasicInfo_MatterReportable verifies MatterReportable returns a non-empty
// slice of subscribable attributes.
func TestBasicInfo_MatterReportable(t *testing.T) {
	t.Parallel()
	b := newValidBasicInfo(t)
	list := b.MatterReportable()
	if len(list) == 0 {
		t.Fatal("MatterReportable() is empty — subscribe-path broken")
	}
}

// TestBasicInfo_MatterAttributes verifies core mandatory attributes are listed.
func TestBasicInfo_MatterAttributes(t *testing.T) {
	t.Parallel()
	b := newValidBasicInfo(t)
	list := b.MatterAttributes()
	have := make(map[uint32]bool)
	for _, a := range list {
		have[a] = true
	}
	for _, want := range []uint32{0x0001, 0x0002, 0x0003, 0x0004} {
		if !have[want] {
			t.Errorf("MatterAttributes() missing attr 0x%04X", want)
		}
	}
}

// TestBasicInfo_MatterDataVersion verifies MatterDataVersion does not panic.
func TestBasicInfo_MatterDataVersion(t *testing.T) {
	t.Parallel()
	b := newValidBasicInfo(t)
	_ = b.MatterDataVersion()
}

// TestBasicInfo_MatterAttributesWithOptionals verifies that optional fields
// populated in the config appear in MatterAttributes.
func TestBasicInfo_MatterAttributesWithOptionals(t *testing.T) {
	t.Parallel()
	cfg := validBasicInfoConfig()
	cfg.ManufacturingDate = "20260101"
	cfg.PartNumber = "PN-42"
	cfg.ProductURL = "https://example.com"
	cfg.ProductLabel = "My Bridge"
	cfg.SerialNumber = "SN-001"
	cfg.ProductAppearance = core.ProductAppearanceStruct{Finish: 1, PrimaryColor: 3}
	cfg.ConfigurationVersion = 7
	b, err := core.NewBasicInformation(cfg)
	if err != nil {
		t.Fatalf("NewBasicInformation: %v", err)
	}
	list := b.MatterAttributes()
	have := make(map[uint32]bool)
	for _, a := range list {
		have[a] = true
	}
	optionals := map[uint32]string{
		0x000B: "ManufacturingDate",
		0x000C: "PartNumber",
		0x000D: "ProductURL",
		0x000E: "ProductLabel",
		0x000F: "SerialNumber",
		0x0014: "ProductAppearance",
	}
	for id, name := range optionals {
		if !have[id] {
			t.Errorf("MatterAttributes() missing %s (0x%04X) when set", name, id)
		}
	}
}

// TestSoftwareVersionFromString pins the semver-string → numeric
// SoftwareVersion derivation (major*1_000_000 + minor*1_000 + patch,
// components clamped to 999, pre-release/build suffixes dropped,
// result floored at 1) for release, rc, and dev build strings.
func TestSoftwareVersionFromString(t *testing.T) {
	t.Parallel()
	cases := []struct {
		version string
		want    uint32
	}{
		{"0.32.1", 32_001},
		{"1.2.3", 1_002_003},
		{"v1.0.0", 1_000_000},
		{"12.34.56", 12_034_056},
		{"0.32", 32_000},
		{"0.32.0-rc.1", 32_000},
		{"0.32.1+g4add313", 32_001},
		{"1.0.0-beta.2+meta", 1_000_000},
		{"0.32.x", 32_000},
		{"1.1000.0", 1_999_000}, // component clamp at 999
		{"dev", 1},
		{"", 1},
		{"0.0.0", 1}, // floor: never advertise matter.js's dev default 0
		{" 0.32.1 ", 32_001},
	}
	for _, tc := range cases {
		if got := core.SoftwareVersionFromString(tc.version); got != tc.want {
			t.Errorf("SoftwareVersionFromString(%q) = %d, want %d", tc.version, got, tc.want)
		}
	}
}

// TestBasicInfo_SoftwareVersionDerivedFromVersionString verifies that a
// config carrying only the human-readable version string yields a
// numerically consistent SoftwareVersion attribute (0x0009), while an
// explicitly supplied numeric value always wins over the derivation.
func TestBasicInfo_SoftwareVersionDerivedFromVersionString(t *testing.T) {
	t.Parallel()
	t.Run("derived from string", func(t *testing.T) {
		t.Parallel()
		cfg := validBasicInfoConfig()
		cfg.SoftwareVersion = 0
		cfg.SoftwareVersionStr = "0.32.1"
		b, err := core.NewBasicInformation(cfg)
		if err != nil {
			t.Fatalf("NewBasicInformation: %v", err)
		}
		v, ok := b.MatterRead(0x0009)
		if !ok {
			t.Fatal("SoftwareVersion: ok=false")
		}
		if got := v.(uint32); got != 32_001 {
			t.Errorf("SoftwareVersion = %d, want 32001 (derived from %q)", got, cfg.SoftwareVersionStr)
		}
		s, ok := b.MatterRead(0x000A)
		if !ok {
			t.Fatal("SoftwareVersionString: ok=false")
		}
		if got := s.(tlv.BoundedString).Value; got != "0.32.1" {
			t.Errorf("SoftwareVersionString = %q, want %q", got, "0.32.1")
		}
	})
	t.Run("explicit numeric wins", func(t *testing.T) {
		t.Parallel()
		cfg := validBasicInfoConfig()
		cfg.SoftwareVersion = 7
		cfg.SoftwareVersionStr = "0.32.1"
		b, err := core.NewBasicInformation(cfg)
		if err != nil {
			t.Fatalf("NewBasicInformation: %v", err)
		}
		v, ok := b.MatterRead(0x0009)
		if !ok {
			t.Fatal("SoftwareVersion: ok=false")
		}
		if got := v.(uint32); got != 7 {
			t.Errorf("SoftwareVersion = %d, want 7 (explicit config value)", got)
		}
	})
	t.Run("non-semver dev build never yields zero", func(t *testing.T) {
		t.Parallel()
		cfg := validBasicInfoConfig()
		cfg.SoftwareVersion = 0
		cfg.SoftwareVersionStr = "dev"
		b, err := core.NewBasicInformation(cfg)
		if err != nil {
			t.Fatalf("NewBasicInformation: %v", err)
		}
		v, ok := b.MatterRead(0x0009)
		if !ok {
			t.Fatal("SoftwareVersion: ok=false")
		}
		if got := v.(uint32); got == 0 {
			t.Error("SoftwareVersion = 0 for a dev build — must be floored at 1")
		}
	})
}
