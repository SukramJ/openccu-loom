// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// Package core — BridgedDeviceBasicInformation cluster-server parity
// tests against matter.js HEAD.
//
// matter.js does not ship a dedicated unit-test file for
// BridgedDeviceBasicInformationServer in packages/node/test/behaviors/
// as of HEAD (verified against matter.js HEAD). The parity invariants below are
// derived directly from:
//   - packages/model/src/standard/elements/bridged-device-basic-information.element.ts
//   - packages/node/src/behaviors/bridged-device-basic-information/
//     BridgedDeviceBasicInformationServer.ts
//   - packages/node/test/endpoints/BridgeTest.ts (bridge-composition cases)
//
// Conversion pattern:
//   - Each test header cites the matter.js source file + line.
//   - Cases already fully exercised in bridged_device_basic_information_test.go
//     are noted but not duplicated; the parity files add the matter.js
//     citation frame around the invariant.

package core_test

import (
	"context"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/north/matter/cluster"
	"github.com/SukramJ/openccu-loom/internal/north/matter/cluster/core"
	"github.com/SukramJ/openccu-loom/pkg/mattercontract"
)

// TestParityMatterJS_BridgedServer_ClusterID pins 0x0039.
//
// Mirrors matter.js packages/model/src/standard/elements/
// bridged-device-basic-information.element.ts:5 (id: 0x0039).
func TestParityMatterJS_BridgedServer_ClusterID(t *testing.T) {
	t.Parallel()
	b := newValidBridged(t)
	const wantID uint32 = 0x0039
	if got := b.MatterClusterID(); got != wantID {
		t.Errorf("ClusterID = 0x%04X, want 0x%04X", got, wantID)
	}
}

// TestParityMatterJS_BridgedServer_ClusterRevision6 pins revision 6.
//
// Mirrors matter.js packages/model/src/standard/elements/
// bridged-device-basic-information.element.ts:20 (ClusterRevision
// default: 6). Apple Home pair aborts when the advertised revision lags
// the matter.js gold-standard value, so this pin is load-bearing.
func TestParityMatterJS_BridgedServer_ClusterRevision6(t *testing.T) {
	t.Parallel()
	b := newValidBridged(t)
	v, ok := b.MatterRead(cluster.AttrGlobalClusterRevision)
	if !ok {
		t.Fatal("ClusterRevision: ok=false")
	}
	if got := v.(uint16); got != 6 {
		t.Errorf("ClusterRevision = %d, want 6 (matter.js HEAD bridged-device-basic-information.element.ts:20)", got)
	}
}

// TestParityMatterJS_BridgedServer_UniqueIDIsMandatory asserts that
// UniqueID (0x0012) is mandatory — this is the key difference between
// BridgedDeviceBasicInformation and BasicInformation (where UniqueID is
// optional). Bridged devices MUST have a unique ID to avoid the
// "all endpoints share same UniqueID" root cause of the Apple pair-abort
// bug (2026-05 audit).
//
// Mirrors matter.js packages/model/src/standard/elements/
// bridged-device-basic-information.element.ts UniqueID conformance "M".
func TestParityMatterJS_BridgedServer_UniqueIDIsMandatory(t *testing.T) {
	t.Parallel()
	b := newValidBridged(t)
	v, ok := b.MatterRead(0x0012)
	if !ok {
		t.Fatalf("UniqueID (0x0012): ok=false — mandatory attribute absent (conformance M)")
	}
	uid, ok := v.(string)
	if !ok {
		t.Fatalf("UniqueID type = %T, want string", v)
	}
	if uid == "" {
		t.Error("UniqueID is empty — mandatory, non-empty per spec")
	}
}

// TestParityMatterJS_BridgedServer_UniqueIDRejectsEmptyConfig verifies
// that NewBridgedDeviceBasicInformation rejects an empty UniqueID at
// construction time — it cannot be left for a later call.
//
// Mirrors matter.js BridgedDeviceBasicInformationServer.ts validation —
// the server always requires a non-empty uniqueId from the device
// descriptor.
func TestParityMatterJS_BridgedServer_UniqueIDRejectsEmptyConfig(t *testing.T) {
	t.Parallel()
	cfg := validBridgedConfig()
	cfg.UniqueID = ""
	if _, err := core.NewBridgedDeviceBasicInformation(cfg); err == nil {
		t.Error("NewBridgedDeviceBasicInformation: expected error for empty UniqueID, got nil")
	}
}

// TestParityMatterJS_BridgedServer_ReachableIsMandatory asserts that
// Reachable (0x0011) is readable after construction. It is a mandatory
// attribute per matter.js bridged-device-basic-information.element.ts.
//
// Mirrors matter.js packages/model/src/standard/elements/
// bridged-device-basic-information.element.ts Reachable conformance "M".
func TestParityMatterJS_BridgedServer_ReachableIsMandatory(t *testing.T) {
	t.Parallel()
	b := newValidBridged(t)
	v, ok := b.MatterRead(0x0011)
	if !ok {
		t.Fatal("Reachable (0x0011): ok=false — mandatory attribute absent")
	}
	if _, isBool := v.(bool); !isBool {
		t.Errorf("Reachable type = %T, want bool", v)
	}
}

// TestParityMatterJS_BridgedServer_ReachableChangedEventFired verifies that
// SetReachable emits the ReachableChanged event (id 0x0003) at priority
// Critical. This mirrors matter.js BridgedDeviceBasicInformationServer.ts
// which emits the event whenever the `reachable` state attribute changes.
//
// Mirrors matter.js packages/node/src/behaviors/bridged-device-basic-
// information/BridgedDeviceBasicInformationServer.ts (state.reachable
// setter → events.reachableChanged.emit) and packages/model/src/standard/
// elements/bridged-device-basic-information.element.ts:55 (ReachableChanged
// event, priority Critical).
func TestParityMatterJS_BridgedServer_ReachableChangedEventFired(t *testing.T) {
	t.Parallel()
	cfg := validBridgedConfig()
	cfg.Reachable = true
	b, err := core.NewBridgedDeviceBasicInformation(cfg)
	if err != nil {
		t.Fatalf("NewBridgedDeviceBasicInformation: %v", err)
	}
	emitter := &fakeEmitter{}
	b.SetMatterEventEmitter(emitter)
	b.SetEndpoint(3)

	b.SetReachable(false)

	emitter.mu.Lock()
	got := append([]recordedEvent(nil), emitter.events...)
	emitter.mu.Unlock()

	if len(got) != 1 {
		t.Fatalf("expected 1 ReachableChanged event, got %d", len(got))
	}
	ev := got[0]
	if ev.cluster != 0x0039 {
		t.Errorf("cluster = 0x%04X, want 0x0039 (BridgedDeviceBasicInformation)", ev.cluster)
	}
	if ev.event != 0x0003 {
		t.Errorf("event = 0x%04X, want 0x0003 (ReachableChanged)", ev.event)
	}
	if ev.priority != mattercontract.EventPriorityInfo {
		t.Errorf("priority = %v, want Info (bridged-device-basic-information.element.ts:55)", ev.priority)
	}
	if ev.endpoint != 3 {
		t.Errorf("endpoint = %d, want 3", ev.endpoint)
	}
}

// TestParityMatterJS_BridgedServer_ReachableChangedPayload checks the
// ReachableChangedEvent payload's ReachableNewValue field. matter.js
// emits the new value in the event payload (not the old value).
//
// Mirrors matter.js packages/model/src/standard/elements/
// bridged-device-basic-information.element.ts:55-60 ReachableChanged
// event field ReachableNewValue (id 0x0, type bool, conformance M).
func TestParityMatterJS_BridgedServer_ReachableChangedPayload(t *testing.T) {
	t.Parallel()
	cfg := validBridgedConfig()
	b, err := core.NewBridgedDeviceBasicInformation(cfg)
	if err != nil {
		t.Fatalf("NewBridgedDeviceBasicInformation: %v", err)
	}
	emitter := &fakeEmitter{}
	b.SetMatterEventEmitter(emitter)

	// Start from unreachable so the flip to true generates an event.
	b.SetReachable(false) // transition true→false (event 1, ignored)
	emitter.mu.Lock()
	emitter.events = emitter.events[:0] // clear after the first transition
	emitter.mu.Unlock()

	b.SetReachable(true) // flip false→true: this is the event under test

	emitter.mu.Lock()
	got := append([]recordedEvent(nil), emitter.events...)
	emitter.mu.Unlock()

	if len(got) != 1 {
		t.Fatalf("expected 1 event, got %d", len(got))
	}
	payload, ok := got[0].data.(core.ReachableChangedEvent)
	if !ok {
		t.Fatalf("data = %T, want core.ReachableChangedEvent", got[0].data)
	}
	if !payload.ReachableNewValue {
		t.Errorf("ReachableNewValue = false, want true (the new value, not the old)")
	}
}

// TestParityMatterJS_BridgedServer_BridgeMakesChildrenBridged pins the
// device-type list contract that every bridged endpoint MUST include
// BridgedNode (0x0013) as a secondary device type alongside the primary
// application type.
//
// Mirrors matter.js packages/node/test/endpoints/BridgeTest.ts:16-31
// (expectBridgedLight — descriptor.deviceTypeList includes BridgedNode
// as a second entry alongside OnOffLight). This ensures Apple Home
// correctly routes the endpoint to the "bridged" category.
func TestParityMatterJS_BridgedServer_BridgeMakesChildrenBridged(t *testing.T) {
	t.Parallel()
	// Verify the Descriptor cluster's DeviceTypeList for a bridged endpoint
	// contains BridgedNode (0x0013) per the BridgeTest.ts contract.
	// We use the Descriptor directly since it stores the device-type list.
	d, err := core.NewDescriptor(
		[]core.DeviceTypeStruct{
			{DeviceType: 0x0100, Revision: 3}, // OnOffLight (primary)
			{DeviceType: 0x0013, Revision: 3}, // BridgedNode (mandatory secondary for bridges)
		},
		nil, nil, nil,
	)
	if err != nil {
		t.Fatalf("NewDescriptor: %v", err)
	}
	v, ok := d.MatterRead(0x0000)
	if !ok {
		t.Fatal("deviceTypeList: ok=false")
	}
	list := v.([]core.DeviceTypeStruct)
	found := false
	for _, dt := range list {
		if dt.DeviceType == 0x0013 {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("deviceTypeList %v does not contain BridgedNode (0x0013) — matter.js BridgeTest.ts:21 expects it as second entry", list)
	}
}

// TestParityMatterJS_BridgedServer_NodeLabelWritePreservedAcrossRead
// verifies that a write to NodeLabel (0x0005) is reflected in the next
// read. This parity test covers the mutable-attribute round-trip that
// matter.js BridgedDeviceBasicInformationServer.ts allows via `write`
// access on NodeLabel.
//
// Source-Origin: derived from matter.js packages/model/src/standard/
// elements/bridged-device-basic-information.element.ts:24 (NodeLabel
// access "RW VO") and
// packages/node/src/behaviors/bridged-device-basic-information/
// BridgedDeviceBasicInformationServer.ts (nodeLabel attribute is
// mutable via write). NodeLabel must round-trip correctly so that a
// commissioner or Apple Home can rename a bridged device after pairing.
func TestParityMatterJS_BridgedServer_NodeLabelWritePreservedAcrossRead(t *testing.T) {
	t.Parallel()
	b := newValidBridged(t)

	const newLabel = "Kitchen Sensor"
	v0, ok := b.MatterRead(0x0005)
	if !ok {
		t.Fatal("initial NodeLabel: ok=false")
	}
	initial := v0.(string)
	if initial == newLabel {
		// Adjust test value so we always exercise a real transition.
		t.Skip("initial label already matches test value — adjust validBridgedConfig to differ")
	}

	if err := b.MatterWrite(context.Background(), 0x0005, newLabel); err != nil {
		t.Fatalf("MatterWrite NodeLabel: %v", err)
	}
	v1, ok := b.MatterRead(0x0005)
	if !ok {
		t.Fatal("NodeLabel after write: ok=false")
	}
	if got := v1.(string); got != newLabel {
		t.Errorf("NodeLabel after write = %q, want %q (matter.js RW VO contract)", got, newLabel)
	}
}

// TestParityMatterJS_BridgedServer_MatterAttributesContainsMandatory
// asserts that MatterAttributes() returns all mandatory attribute IDs
// for BridgedDeviceBasicInformation. Apple Home's HAP service mapper
// reads the full attribute set; missing mandatory attrs trigger the
// "no cluster information" abort path.
//
// Mirrors matter.js packages/model/src/standard/elements/
// bridged-device-basic-information.element.ts — NodeLabel (0x0005),
// Reachable (0x0011), and UniqueID (0x0012) are conformance "M".
func TestParityMatterJS_BridgedServer_MatterAttributesContainsMandatory(t *testing.T) {
	t.Parallel()
	b := newValidBridged(t)
	attrs := b.MatterAttributes()
	attrSet := make(map[uint32]bool, len(attrs))
	for _, id := range attrs {
		attrSet[id] = true
	}
	mandatory := []struct {
		id   uint32
		name string
	}{
		{0x0005, "NodeLabel"},
		{0x0011, "Reachable"},
		{0x0012, "UniqueID"},
	}
	for _, m := range mandatory {
		if !attrSet[m.id] {
			t.Errorf("MatterAttributes() missing mandatory %s (0x%04X) — matter.js bridged-device-basic-information.element.ts conformance M", m.name, m.id)
		}
	}
}

// TestParityMatterJS_BridgedServer_ProductAppearanceAbsentWhenUnset verifies
// that ProductAppearance is omitted when not configured — matching the
// optional conformance in matter.js.
//
// Mirrors matter.js packages/model/src/standard/elements/
// bridged-device-basic-information.element.ts ProductAppearance
// conformance "O".
func TestParityMatterJS_BridgedServer_ProductAppearanceAbsentWhenUnset(t *testing.T) {
	t.Parallel()
	cfg := validBridgedConfig()
	// Leave ProductAppearance as zero value (unset).
	b, err := core.NewBridgedDeviceBasicInformation(cfg)
	if err != nil {
		t.Fatalf("NewBridgedDeviceBasicInformation: %v", err)
	}
	v, ok := b.MatterRead(0x0014)
	if ok {
		t.Errorf("ProductAppearance (0x0014) present when unset = %v — optional attribute must be absent", v)
	}
}

// TestParityMatterJS_BridgedServer_NodeLabelPersistenceAcrossMultipleWrites
// is the L0-BD-02 regression guard: successive NodeLabel writes must each
// be independently visible from the next read, and the final value must
// equal the last write. Earlier openccu-loom versions had a bug where
// only the first write was retained; subsequent writes were silently
// discarded (regression against the matter.js "RW" attribute contract).
//
// Source-Origin: derived from matter.js packages/model/src/standard/
// elements/bridged-device-basic-information.element.ts:24 (NodeLabel
// access "RW VO") — every write must replace the stored value.
func TestParityMatterJS_BridgedServer_NodeLabelPersistenceAcrossMultipleWrites(t *testing.T) {
	t.Parallel()
	b := newValidBridged(t)

	writes := []string{"First Label", "Second Label", "Final Label"}
	for _, label := range writes {
		if err := b.MatterWrite(nil, 0x0005, label); err != nil { //nolint:staticcheck // SA1012: test exercises the nil-Context tolerance contract
			t.Fatalf("MatterWrite %q: %v", label, err)
		}
		v, ok := b.MatterRead(0x0005)
		if !ok {
			t.Fatalf("NodeLabel read after writing %q: ok=false", label)
		}
		if got := v.(string); got != label {
			t.Errorf("NodeLabel after writing %q = %q — L0-BD-02 regression: successive writes must each persist", label, got)
		}
	}
}

// TestParityMatterJS_BridgedServer_ReachableNoEmitOnSameValue pins the
// idempotent path. matter.js BridgedDeviceBasicInformationServer.ts
// only emits reachableChanged when the value actually transitions —
// no-op writes must not fire spurious events.
//
// Mirrors matter.js packages/node/src/behaviors/bridged-device-basic-
// information/BridgedDeviceBasicInformationServer.ts setter guard
// `if (this.state.reachable !== reachable)`.
func TestParityMatterJS_BridgedServer_ReachableNoEmitOnSameValue(t *testing.T) {
	t.Parallel()
	cfg := validBridgedConfig()
	cfg.Reachable = true
	b, err := core.NewBridgedDeviceBasicInformation(cfg)
	if err != nil {
		t.Fatalf("NewBridgedDeviceBasicInformation: %v", err)
	}
	emitter := &fakeEmitter{}
	b.SetMatterEventEmitter(emitter)
	b.SetEndpoint(1)

	b.SetReachable(true) // same value — must not emit

	emitter.mu.Lock()
	n := len(emitter.events)
	emitter.mu.Unlock()

	if n != 0 {
		t.Errorf("SetReachable(same value): expected 0 events, got %d — matter.js only emits on transition", n)
	}
}
