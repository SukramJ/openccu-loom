// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

//go:build chiptool

package chiptool

import (
	"context"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/tests/chiptool/harness"
)

// TestBasicInformation_VendorID asserts BasicInformation.VendorID
// matches the development VID (0xFFF1 = 65521) the harness wires.
func TestBasicInformation_VendorID(t *testing.T) {
	b := requireBridge(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	out, err := b.SharedCtl.ReadAttr(ctx, t, "basicinformation", "vendor-id", 0)
	if err != nil {
		t.Fatalf("read vendor-id: %v", err)
	}
	v, ok := harness.FindAttrUint(out, "VendorID")
	if !ok {
		t.Fatalf("VendorID not parsed:\n%s", out)
	}
	if v != 0xFFF1 {
		t.Errorf("VendorID=%d (0x%X), want 0xFFF1", v, v)
	}
}

// TestBasicInformation_ProductID asserts BasicInformation.ProductID
// matches the development PID (0x8001).
func TestBasicInformation_ProductID(t *testing.T) {
	b := requireBridge(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	out, err := b.SharedCtl.ReadAttr(ctx, t, "basicinformation", "product-id", 0)
	if err != nil {
		t.Fatalf("read product-id: %v", err)
	}
	v, ok := harness.FindAttrUint(out, "ProductID")
	if !ok {
		t.Fatalf("ProductID not parsed:\n%s", out)
	}
	if v != 0x8001 {
		t.Errorf("ProductID=%d (0x%X), want 0x8001", v, v)
	}
}

// TestBasicInformation_NodeLabel asserts BasicInformation.NodeLabel
// matches the harness's configured value.
func TestBasicInformation_NodeLabel(t *testing.T) {
	b := requireBridge(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	out, err := b.SharedCtl.ReadAttr(ctx, t, "basicinformation", "node-label", 0)
	if err != nil {
		t.Fatalf("read node-label: %v", err)
	}
	label, ok := harness.FindAttrString(out, "NodeLabel")
	if !ok {
		t.Fatalf("NodeLabel not parsed:\n%s", out)
	}
	if label != "openccu-loom-chiptool" {
		t.Errorf("NodeLabel=%q, want %q", label, "openccu-loom-chiptool")
	}
}

// TestBasicInformation_SoftwareVersion asserts SoftwareVersion is
// readable and non-zero (per Matter §11.1.5.13).
func TestBasicInformation_SoftwareVersion(t *testing.T) {
	b := requireBridge(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	out, err := b.SharedCtl.ReadAttr(ctx, t, "basicinformation", "software-version", 0)
	if err != nil {
		t.Fatalf("read software-version: %v", err)
	}
	v, ok := harness.FindAttrUint(out, "SoftwareVersion")
	if !ok {
		t.Fatalf("SoftwareVersion not parsed:\n%s", out)
	}
	if v < 1 {
		t.Errorf("SoftwareVersion=%d, want ≥ 1", v)
	}
}

// TestBasicInformation_HardwareVersion asserts HardwareVersion is
// readable. May be 0 (per spec the field is optional but mandatory-
// present); we only assert successful read.
func TestBasicInformation_HardwareVersion(t *testing.T) {
	b := requireBridge(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	out, err := b.SharedCtl.ReadAttr(ctx, t, "basicinformation", "hardware-version", 0)
	if err != nil {
		t.Fatalf("read hardware-version: %v", err)
	}
	if !harness.AttrReadOK(out) {
		t.Errorf("hardware-version read did not report success:\n%s", out)
	}
}

// TestBasicInformation_ProductName asserts ProductName is a
// non-empty string. The harness's daemon advertises
// "openccu-loom Matter Bridge" by default.
func TestBasicInformation_ProductName(t *testing.T) {
	b := requireBridge(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	out, err := b.SharedCtl.ReadAttr(ctx, t, "basicinformation", "product-name", 0)
	if err != nil {
		t.Fatalf("read product-name: %v", err)
	}
	name, ok := harness.FindAttrString(out, "ProductName")
	if !ok {
		t.Fatalf("ProductName not parsed:\n%s", out)
	}
	if name == "" {
		t.Errorf("ProductName is empty:\n%s", out)
	}
}

// TestBasicInformation_VendorName_NonEmpty asserts VendorName is a
// non-empty string. The bridge advertises some vendor name even
// under the development VID block.
func TestBasicInformation_VendorName_NonEmpty(t *testing.T) {
	b := requireBridge(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	out, err := b.SharedCtl.ReadAttr(ctx, t, "basicinformation", "vendor-name", 0)
	if err != nil {
		t.Fatalf("read vendor-name: %v", err)
	}
	name, ok := harness.FindAttrString(out, "VendorName")
	if !ok {
		t.Fatalf("VendorName not parsed:\n%s", out)
	}
	if name == "" {
		t.Errorf("VendorName is empty:\n%s", out)
	}
}

// TestBridgedDeviceBasicInformation_PerEndpoint walks every bridged
// endpoint and reads NodeLabel + UniqueID + Reachable from BDBI.
//
// Assertions:
//   - NodeLabel non-empty
//   - UniqueID non-empty AND distinct across endpoints (duplicate
//     fingerprints cause Apple Home pair-abort)
//   - Reachable reported (TRUE for godevccu's never-stale fleet)
func TestBridgedDeviceBasicInformation_PerEndpoint(t *testing.T) {
	b := requireBridge(t)
	// Three wildcard reads (endpoint 0xFFFF) replace ~N × 3 per-endpoint
	// reads — chip-tool spawns one process per ReadAttr (~0.7-0.9s
	// including PASE/CASE setup), and on larger fleets the daemon's
	// CASE session table accumulates idle sessions faster than the
	// idle-eviction sweep can keep up. The wildcard path opens one
	// CASE session per read regardless of fleet size and parses every
	// AttributeReportIB out of the single response — fast, stable,
	// and exercises the same code path Apple Home uses post-CASE.
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	aggOut, err := b.SharedCtl.ReadAttr(ctx, t, "descriptor", "parts-list", 1)
	if err != nil {
		t.Fatalf("read aggregator parts-list: %v", err)
	}
	eps := harness.EndpointsInPartsList(aggOut)
	if len(eps) == 0 {
		t.Skip("no bridged endpoints — godevccu fleet empty")
	}

	nlOut, err := b.SharedCtl.ReadAttr(ctx, t, "bridgeddevicebasicinformation", "node-label", 0xFFFF)
	if err != nil {
		t.Fatalf("wildcard read BDBI.NodeLabel: %v", err)
	}
	labels := harness.FindAttrStringPerEndpoint(nlOut, "NodeLabel")

	uidOut, err := b.SharedCtl.ReadAttr(ctx, t, "bridgeddevicebasicinformation", "unique-id", 0xFFFF)
	if err != nil {
		t.Fatalf("wildcard read BDBI.UniqueID: %v", err)
	}
	uids := harness.FindAttrStringPerEndpoint(uidOut, "UniqueID")

	reOut, err := b.SharedCtl.ReadAttr(ctx, t, "bridgeddevicebasicinformation", "reachable", 0xFFFF)
	if err != nil {
		t.Fatalf("wildcard read BDBI.Reachable: %v", err)
	}
	reach := harness.FindAttrBoolPerEndpoint(reOut, "Reachable")

	seenUIDs := make(map[string]uint16, len(eps))
	for _, ep := range eps {
		label, hasLabel := labels[ep]
		if !hasLabel || label == "" {
			t.Errorf("EP %d: BDBI.NodeLabel empty or unparsed (wildcard read produced %d labels total)", ep, len(labels))
		}
		uid, hasUID := uids[ep]
		if !hasUID || uid == "" {
			t.Errorf("EP %d: BDBI.UniqueID empty or unparsed (wildcard read produced %d UIDs total)", ep, len(uids))
			continue
		}
		if prev, exists := seenUIDs[uid]; exists {
			t.Errorf("EP %d: BDBI.UniqueID %q already used by EP %d — duplicate fingerprints cause Apple Home pair-abort", ep, uid, prev)
		}
		seenUIDs[uid] = ep
		if _, hasReach := reach[ep]; !hasReach {
			t.Errorf("EP %d: BDBI.Reachable marker missing (wildcard read produced %d reach values total)", ep, len(reach))
		}
	}
}
