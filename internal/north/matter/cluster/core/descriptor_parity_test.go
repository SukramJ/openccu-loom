// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// Package core — Descriptor cluster-server parity tests.
//
// Mirrors matter.js packages/node/test/behaviors/descriptor/
// DescriptorServerTest.ts (cases ported from lines 34–310).
//
// Conversion pattern:
//   - Each test cites the matter.js source file + approximate line.
//   - Async endpoint-lifecycle tests (adds parts automatically, removes
//     parts automatically) require the MockServerNode / MockEndpoint
//     infrastructure that has no direct Go equivalent in openccu-loom's
//     unit-test layer; those are marked t.Skip with a future-gap note.
//   - Type-extension tests use interface assertions instead of TS satisfies.

package core_test

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/north/matter/cluster/core"
)

// TestParityMatterJS_DescriptorServer_ProperlyExtendsEndpointType verifies
// that NewDescriptor produces defaults with deviceTypeList, partsList,
// serverList, and clientList — structural parity with matter.js's
// MutableEndpoint.with(DescriptorServer).defaults shape.
//
// Mirrors matter.js packages/node/test/behaviors/descriptor/
// DescriptorServerTest.ts:34 (case "properly extends endpoint type").
func TestParityMatterJS_DescriptorServer_ProperlyExtendsEndpointType(t *testing.T) {
	t.Parallel()
	d, err := core.NewDescriptor(
		[]core.DeviceTypeStruct{{DeviceType: 1, Revision: 1}},
		[]uint32{0x001D, 0x001E},
		[]uint32{},
		[]uint16{},
	)
	if err != nil {
		t.Fatalf("NewDescriptor: %v", err)
	}
	// Verify all four attribute reads return coherent non-error results.
	for attrID, name := range map[uint32]string{
		0x0000: "deviceTypeList",
		0x0001: "serverList",
		0x0002: "clientList",
		0x0003: "partsList",
	} {
		_, ok := d.MatterRead(attrID)
		if !ok {
			t.Errorf("attribute %s (0x%04X): ok=false — Descriptor defaults must be present", name, attrID)
		}
	}
}

// TestParityMatterJS_DescriptorServer_AddsDeviceTypeAutomatically asserts
// that if a single device-type entry is provided, deviceTypeList returns
// exactly that entry. The matter.js case verifies the mock device type (1,
// revision 1) is auto-added.
//
// Mirrors matter.js packages/node/test/behaviors/descriptor/
// DescriptorServerTest.ts:51 (case "adds device type automatically if necessary").
func TestParityMatterJS_DescriptorServer_AddsDeviceTypeAutomatically(t *testing.T) {
	t.Parallel()
	d, err := core.NewDescriptor(
		[]core.DeviceTypeStruct{{DeviceType: 1, Revision: 1}},
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
	if len(list) != 1 {
		t.Fatalf("deviceTypeList len=%d, want 1", len(list))
	}
	if list[0].DeviceType != 1 || list[0].Revision != 1 {
		t.Errorf("deviceTypeList[0] = {%d, %d}, want {1, 1}", list[0].DeviceType, list[0].Revision)
	}
}

// TestParityMatterJS_DescriptorServer_DoesNotAddDeviceTypeWhenUnnecessary
// verifies that a custom device-type (e.g. DeviceTypeId(2), revision 2)
// survives unchanged — the server must not override an explicitly supplied list.
//
// Mirrors matter.js packages/node/test/behaviors/descriptor/
// DescriptorServerTest.ts:61 (case "does not add device type automatically
// if unnecessary").
func TestParityMatterJS_DescriptorServer_DoesNotAddDeviceTypeWhenUnnecessary(t *testing.T) {
	t.Parallel()
	d, err := core.NewDescriptor(
		[]core.DeviceTypeStruct{{DeviceType: 2, Revision: 2}},
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
	if len(list) != 1 {
		t.Fatalf("deviceTypeList len=%d, want 1", len(list))
	}
	if list[0].DeviceType != 2 || list[0].Revision != 2 {
		t.Errorf("deviceTypeList[0] = {%d, %d}, want {2, 2}", list[0].DeviceType, list[0].Revision)
	}
}

// TestParityMatterJS_DescriptorServer_AddsServersAutomatically verifies
// that SetServerListProvider can supply a dynamic server list — parity with
// matter.js DescriptorServer auto-updating ServerList when behaviors are
// required (packages/node/src/behaviors/descriptor/DescriptorServer.ts:236-244).
//
// Mirrors matter.js packages/node/test/behaviors/descriptor/
// DescriptorServerTest.ts:75 (case "adds servers automatically").
// Note: matter.js uses behaviors.require(OnOffServer) + event listener;
// openccu-loom uses SetServerListProvider closure (Bug K fix).
func TestParityMatterJS_DescriptorServer_AddsServersAutomatically(t *testing.T) {
	t.Parallel()
	d, err := core.NewDescriptor(
		[]core.DeviceTypeStruct{{DeviceType: 1, Revision: 1}},
		[]uint32{0x001D}, // initial static: Descriptor only
		nil, nil,
	)
	if err != nil {
		t.Fatalf("NewDescriptor: %v", err)
	}
	// Simulate adding OnOff via the provider (matter.js: behaviors.require(OnOffServer)
	// triggers serverList$Changed → [29, 6] — Descriptor=0x001D, OnOff=0x0006).
	d.SetServerListProvider(func() []uint32 {
		return []uint32{0x001D, 0x0006}
	})
	v, ok := d.MatterRead(0x0001)
	if !ok {
		t.Fatal("serverList: ok=false")
	}
	list := v.([]uint32)
	if len(list) != 2 {
		t.Fatalf("serverList len=%d, want 2 (Descriptor + OnOff)", len(list))
	}
	found := false
	for _, id := range list {
		if id == 0x0006 {
			found = true
		}
	}
	if !found {
		t.Errorf("serverList %v does not contain OnOff (0x0006)", list)
	}
}

// TestParityMatterJS_DescriptorServer_AddsPartsAutomatically verifies that
// SetPartsList correctly updates the partsList attribute.
//
// Mirrors matter.js packages/node/test/behaviors/descriptor/
// DescriptorServerTest.ts:91 (case "adds parts automatically").
// The async endpoint hierarchy is collapsed to a synchronous SetPartsList call.
func TestParityMatterJS_DescriptorServer_AddsPartsAutomatically(t *testing.T) {
	t.Parallel()
	d, err := core.NewDescriptor(
		[]core.DeviceTypeStruct{{DeviceType: 0x000E, Revision: 1}},
		nil, nil, nil,
	)
	if err != nil {
		t.Fatalf("NewDescriptor: %v", err)
	}
	d.SetPartsList([]uint16{2})
	v, ok := d.MatterRead(0x0003)
	if !ok {
		t.Fatal("partsList: ok=false")
	}
	list := v.([]uint16)
	if len(list) != 1 || list[0] != 2 {
		t.Errorf("partsList = %v, want [2]", list)
	}
}

// TestParityMatterJS_DescriptorServer_RemovesPartsAutomatically verifies
// that clearing the parts list via SetPartsList produces an empty result.
//
// Mirrors matter.js packages/node/test/behaviors/descriptor/
// DescriptorServerTest.ts:102 (case "removes parts automatically").
// The matter.js test closes the child endpoint asynchronously; here
// we model the same semantic via SetPartsList([]).
func TestParityMatterJS_DescriptorServer_RemovesPartsAutomatically(t *testing.T) {
	t.Parallel()
	d, err := core.NewDescriptor(
		[]core.DeviceTypeStruct{{DeviceType: 0x000E, Revision: 1}},
		nil, nil, []uint16{2},
	)
	if err != nil {
		t.Fatalf("NewDescriptor: %v", err)
	}
	// Initial state: [2].
	v1, ok := d.MatterRead(0x0003)
	if !ok || len(v1.([]uint16)) != 1 {
		t.Fatalf("initial partsList = %v, want [2]", v1)
	}
	// "Close" the child: clear parts list.
	d.SetPartsList([]uint16{})
	v2, ok := d.MatterRead(0x0003)
	if !ok {
		t.Fatal("partsList after clear: ok=false")
	}
	if got := v2.([]uint16); len(got) != 0 {
		t.Errorf("partsList after clear = %v, want []", got)
	}
}

// TestParityMatterJS_DescriptorServer_FullyPopulatesDeviceTypes verifies
// that a ColorTemperatureLight-equivalent device type is preserved with the
// correct revision.
//
// Mirrors matter.js packages/node/test/behaviors/descriptor/
// DescriptorServerTest.ts:122 (case "fully populates device types").
// matter.js expects [{deviceType:268 (0x010C), revision:4}].
func TestParityMatterJS_DescriptorServer_FullyPopulatesDeviceTypes(t *testing.T) {
	t.Parallel()
	// 0x010C = ColorTemperatureLight (268 decimal), revision 4 per matter.js HEAD.
	d, err := core.NewDescriptor(
		[]core.DeviceTypeStruct{{DeviceType: 0x010C, Revision: 4}},
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
	if len(list) != 1 {
		t.Fatalf("deviceTypeList len=%d, want 1", len(list))
	}
	if list[0].DeviceType != 0x010C {
		t.Errorf("deviceType = 0x%04X, want 0x010C", list[0].DeviceType)
	}
	if list[0].Revision != 4 {
		t.Errorf("revision = %d, want 4 (matter.js HEAD ColorTemperatureLight)", list[0].Revision)
	}
}

// TestParityMatterJS_DescriptorServer_PartsListAutoIDHierarchy tests the
// hierarchical PartsList plumbing for a grandparent → parent → child
// topology — the multi-level scenario tested by matter.js's
// "adds parts automatically with indexed grandparent and parent" suite.
//
// Mirrors matter.js packages/node/test/behaviors/descriptor/
// DescriptorServerTest.ts:141 (describe "adds parts automatically with
// indexed grandparent and parent" / "when constructed with full hierarchy
// (manual ID)").
// The async MockServerNode construction is collapsed to synchronous
// SetPartsList calls on two independent Descriptor instances.
func TestParityMatterJS_DescriptorServer_PartsListAutoIDHierarchy(t *testing.T) {
	t.Parallel()
	// Root (grandparent) descriptor — knows about parent AND child.
	root, err := core.NewDescriptor(
		[]core.DeviceTypeStruct{{DeviceType: 0x0016, Revision: 3}},
		nil, nil, []uint16{1, 2}, // parent=1, child=2
	)
	if err != nil {
		t.Fatalf("NewDescriptor(root): %v", err)
	}
	// Parent descriptor — knows about child only.
	parent, err := core.NewDescriptor(
		[]core.DeviceTypeStruct{{DeviceType: 0x000E, Revision: 2}},
		nil, nil, []uint16{2}, // child=2
	)
	if err != nil {
		t.Fatalf("NewDescriptor(parent): %v", err)
	}

	rootParts, _ := root.MatterRead(0x0003)
	parentParts, _ := parent.MatterRead(0x0003)

	rootList := rootParts.([]uint16)
	parentList := parentParts.([]uint16)

	// Root should list [1, 2] (parent + child).
	if len(rootList) != 2 || rootList[0] != 1 || rootList[1] != 2 {
		t.Errorf("root.partsList = %v, want [1 2]", rootList)
	}
	// Parent should list [2] (child only).
	if len(parentList) != 1 || parentList[0] != 2 {
		t.Errorf("parent.partsList = %v, want [2]", parentList)
	}
}

// TestParityMatterJS_DescriptorServer_PartsListLifecycle_AddAdditionalChild
// tests adding a second child to an existing hierarchy — the
// "when additional child is added (auto ID)" scenario.
//
// Mirrors matter.js packages/node/test/behaviors/descriptor/
// DescriptorServerTest.ts:268 (case "when additional child is added (auto ID)").
func TestParityMatterJS_DescriptorServer_PartsListLifecycle_AddAdditionalChild(t *testing.T) {
	t.Parallel()
	d, err := core.NewDescriptor(
		[]core.DeviceTypeStruct{{DeviceType: 0x000E, Revision: 2}},
		nil, nil, []uint16{2},
	)
	if err != nil {
		t.Fatalf("NewDescriptor: %v", err)
	}
	// Add second child (endpoint 3).
	d.SetPartsList([]uint16{2, 3})

	v, ok := d.MatterRead(0x0003)
	if !ok {
		t.Fatal("partsList: ok=false")
	}
	list := v.([]uint16)
	if len(list) != 2 || list[0] != 2 || list[1] != 3 {
		t.Errorf("partsList = %v, want [2 3]", list)
	}
}

// TestParityMatterJS_DescriptorServer_AsyncPartsListUnsupported records that
// fully async endpoint-lifecycle tests (matter.js MockServerNode, async add/
// close semantics) require a live endpoint registry that does not exist in
// openccu-loom's unit-test layer. They are covered at the integration level
// instead.
//
// Mirrors matter.js packages/node/test/behaviors/descriptor/
// DescriptorServerTest.ts:158 onwards (async hierarchy construction variants).
func TestParityMatterJS_DescriptorServer_AsyncPartsListUnsupported(t *testing.T) {
	t.Skip("FixMe: openccu-loom gap — async MockServerNode lifecycle has no unit-test equivalent; covered by integration tests")
}

// TestParityMatterJS_DescriptorServer_MatterAttributesIncludesGlobals pins
// that MatterAttributes() explicitly enumerates FeatureMap (0xFFFC) and
// ClusterRevision (0xFFFD) alongside the four descriptor-specific
// attributes. This is the regression guard for the wildcard-Subscribe
// path: Apple Home and chip-tool both perform wildcard Subscribe on the Descriptor
// cluster; if the global attribute IDs are missing from the MatterAttributes
// list the dispatcher's wildcard expansion may omit them and Apple logs
// "could not find cached attribute values" for EP14/EP28
// Descriptor.FeatureMap / ClusterRevision.
//
// Source-Origin: derived from matter.js packages/node/test/behaviors/
// descriptor/DescriptorServerTest.ts:34 (case "properly extends endpoint
// type") which asserts all four descriptor attributes are present, and
// chip src/app/clusters/descriptor/descriptor.cpp which serves FeatureMap
// and ClusterRevision via AttributeAccessInterface on every endpoint.
func TestParityMatterJS_DescriptorServer_MatterAttributesIncludesGlobals(t *testing.T) {
	t.Parallel()
	d, err := core.NewDescriptor(
		[]core.DeviceTypeStruct{{DeviceType: 1, Revision: 1}},
		nil, nil, nil,
	)
	if err != nil {
		t.Fatalf("NewDescriptor: %v", err)
	}
	attrs := d.MatterAttributes()
	attrSet := make(map[uint32]bool, len(attrs))
	for _, id := range attrs {
		attrSet[id] = true
	}
	required := []struct {
		id   uint32
		name string
	}{
		{0x0000, "deviceTypeList"},
		{0x0001, "serverList"},
		{0x0002, "clientList"},
		{0x0003, "partsList"},
		{0xFFFC, "FeatureMap"},
		{0xFFFD, "ClusterRevision"},
	}
	for _, r := range required {
		if !attrSet[r.id] {
			t.Errorf("MatterAttributes() missing %s (0x%04X) — L2-D06 regression guard", r.name, r.id)
		}
	}
}

// TestParityMatterJS_DescriptorServer_TagListGatedByFeatureMap verifies
// that TagList (0x0004) is not included in MatterAttributes() and that
// reading it returns (nil, false) when FeatureMap=0 (no TAGLIST bit).
// Advertising TagList without the TAGLIST feature bit causes Apple's iOS
// Matter SDK to reject the whole Descriptor cluster (Apple's `semtag`
// struct schema is not yet shipped) and HAP build fails with
// HAPErrorDomain Code=14.
//
// Source-Origin: derived from matter.js packages/model/src/standard/
// elements/descriptor.element.ts TagList attribute (id 0x0004, conformance
// "[TAGLIST]") — conformance requires the TAGLIST feature bit to be
// advertised before the attribute is served.
func TestParityMatterJS_DescriptorServer_TagListGatedByFeatureMap(t *testing.T) {
	t.Parallel()
	d, err := core.NewDescriptor(
		[]core.DeviceTypeStruct{{DeviceType: 1, Revision: 1}},
		nil, nil, nil,
	)
	if err != nil {
		t.Fatalf("NewDescriptor: %v", err)
	}

	// FeatureMap must be 0 (no TAGLIST).
	fm, ok := d.MatterRead(0xFFFC)
	if !ok {
		t.Fatal("FeatureMap: ok=false")
	}
	if got := fm.(uint32); got != 0 {
		t.Errorf("FeatureMap = 0x%08X, want 0 (TAGLIST bit must not be set without semantic tag support)", got)
	}

	// TagList (0x0004) must be absent when FeatureMap=0.
	v, ok := d.MatterRead(0x0004)
	if ok {
		t.Errorf("TagList present = %v, want absent (conformance [TAGLIST] requires FeatureMap TAGLIST bit)", v)
	}

	// MatterAttributes must not enumerate TagList.
	for _, id := range d.MatterAttributes() {
		if id == 0x0004 {
			t.Error("MatterAttributes() includes TagList (0x0004) — must be gated behind TAGLIST feature bit")
		}
	}
}
