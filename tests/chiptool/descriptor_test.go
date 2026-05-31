// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

//go:build chiptool

package chiptool

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/tests/chiptool/harness"
)

// TestDescriptor_RootPartsList reads Descriptor.PartsList on
// endpoint 0 (root). The list must contain the Aggregator endpoint
// (id 1) and every bridged endpoint. We assert membership of EP 1
// rather than equality — bridged endpoint count varies with the
// godevccu fleet.
//
// Mirrors v9 capability report T3 + matter.js parts-behavior test
// `packages/node/test/endpoints/BridgeTest.ts:35`.
func TestDescriptor_RootPartsList(t *testing.T) {
	b := requireBridge(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	out, err := b.SharedCtl.ReadAttr(ctx, t, "descriptor", "parts-list", 0)
	if err != nil {
		t.Fatalf("read parts-list ep0: %v", err)
	}
	if !harness.AttrReadOK(out) {
		t.Errorf("parts-list read did not succeed:\n%s", out)
	}
	eps := harness.EndpointsInPartsList(out)
	if len(eps) == 0 {
		t.Fatalf("root PartsList empty:\n%s", out)
	}
	var hasAggregator bool
	for _, ep := range eps {
		if ep == 1 {
			hasAggregator = true
		}
	}
	if !hasAggregator {
		t.Errorf("root PartsList %v missing aggregator (EP 1)", eps)
	}
	// When the godevccu fleet's exposures are not yet enabled (the
	// /matter/exposable bulk path can return 500 on some daemon
	// builds), len(eps) == 1 — just the Aggregator. The cluster /
	// subscribe / invoke tests SKIP in that mode; here we settle for
	// the structural assertion that the Aggregator is published.
}

// TestDescriptor_AggregatorPartsList reads Descriptor.PartsList on
// endpoint 1 (Aggregator). The list must NOT contain endpoint 0 or
// 1 themselves and must contain ≥ 1 bridged endpoint.
func TestDescriptor_AggregatorPartsList(t *testing.T) {
	b := requireBridge(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	out, err := b.SharedCtl.ReadAttr(ctx, t, "descriptor", "parts-list", 1)
	if err != nil {
		t.Fatalf("read parts-list ep1: %v", err)
	}
	eps := harness.EndpointsInPartsList(out)
	if len(eps) == 0 {
		t.Skipf("aggregator PartsList empty — no exposures enabled. Light up /matter/exposable rows to populate bridged endpoints. Output:\n%s", out)
	}
	for _, ep := range eps {
		if ep == 0 || ep == 1 {
			t.Errorf("aggregator PartsList contains reserved ID %d (root/aggregator)", ep)
		}
		if ep < 2 {
			t.Errorf("aggregator PartsList entry %d < 2", ep)
		}
	}
}

// TestDescriptor_RootDeviceTypeList reads
// Descriptor.DeviceTypeList on endpoint 0. The Root Node device
// type (0x0016) MUST be present.
func TestDescriptor_RootDeviceTypeList(t *testing.T) {
	b := requireBridge(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	out, err := b.SharedCtl.ReadAttr(ctx, t, "descriptor", "device-type-list", 0)
	if err != nil {
		t.Fatalf("read device-type-list ep0: %v", err)
	}
	if !harness.AttrReadOK(out) {
		t.Errorf("device-type-list read did not succeed:\n%s", out)
	}
	// chip-tool prints either decimal (22) or hex (0x0016); accept
	// both.
	if !strings.Contains(out, "DeviceType: 22") &&
		!strings.Contains(out, "DeviceType: 0x16") &&
		!strings.Contains(out, "DeviceType: 0x0016") {
		t.Errorf("root DeviceTypeList missing RootNode (0x0016):\n%s", out)
	}
}

// TestDescriptor_AggregatorDeviceTypeList reads
// Descriptor.DeviceTypeList on endpoint 1. The Aggregator device
// type (0x000E) MUST be present.
func TestDescriptor_AggregatorDeviceTypeList(t *testing.T) {
	b := requireBridge(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	out, err := b.SharedCtl.ReadAttr(ctx, t, "descriptor", "device-type-list", 1)
	if err != nil {
		t.Fatalf("read device-type-list ep1: %v", err)
	}
	if !strings.Contains(out, "DeviceType: 14") &&
		!strings.Contains(out, "DeviceType: 0xE") &&
		!strings.Contains(out, "DeviceType: 0x000E") {
		t.Errorf("aggregator DeviceTypeList missing Aggregator (0x000E):\n%s", out)
	}
}

// TestDescriptor_RootServerListIncludesMandatory reads
// Descriptor.ServerList on endpoint 0. Must contain Descriptor
// (0x001D), BasicInformation (0x0028), AccessControl (0x001F),
// GeneralCommissioning (0x0030), OperationalCredentials (0x003E),
// AdministratorCommissioning (0x003C), GeneralDiagnostics (0x0033).
//
// Mirrors Matter Core §9.10 mandatory root-node cluster set.
func TestDescriptor_RootServerListIncludesMandatory(t *testing.T) {
	b := requireBridge(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	out, err := b.SharedCtl.ReadAttr(ctx, t, "descriptor", "server-list", 0)
	if err != nil {
		t.Fatalf("read server-list ep0: %v", err)
	}
	ids := harness.ServerListIDs(out)
	mandatory := []uint32{
		0x001D, // Descriptor
		0x0028, // BasicInformation
		0x001F, // AccessControl
		0x0030, // GeneralCommissioning
		0x003E, // OperationalCredentials
		0x003C, // AdministratorCommissioning
		0x0033, // GeneralDiagnostics
	}
	for _, want := range mandatory {
		if !harness.HasCluster(ids, want) {
			t.Errorf("root ServerList missing mandatory cluster 0x%04X (server list: %v)", want, ids)
		}
	}
}

// TestDescriptor_BridgedEndpointsHaveBDBI walks every bridged
// endpoint listed in the Aggregator's PartsList and asserts each
// surfaces BridgedDeviceBasicInformation (0x0039) and Descriptor
// (0x001D) in ServerList.
//
// BDBI on every bridged endpoint is a Matter §9.13 mandatory.
func TestDescriptor_BridgedEndpointsHaveBDBI(t *testing.T) {
	b := requireBridge(t)
	// Budget scales with bridged-endpoint count: chip-tool spawns one
	// process per ReadAttr call (~0.7-0.9s including PASE/CASE
	// setup), so a fleet of ~30 endpoints needs ~30s before the loop
	// hits the deadline. 120s gives headroom for fleets up to ~100
	// EPs without flaky timeouts on a slower CI runner.
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	aggOut, err := b.SharedCtl.ReadAttr(ctx, t, "descriptor", "parts-list", 1)
	if err != nil {
		t.Fatalf("read aggregator parts-list: %v", err)
	}
	eps := harness.EndpointsInPartsList(aggOut)
	if len(eps) == 0 {
		t.Skip("aggregator PartsList empty — godevccu fleet produced no bridged endpoints")
	}
	for _, ep := range eps {
		out, err := b.SharedCtl.ReadAttr(ctx, t, "descriptor", "server-list", ep)
		if err != nil {
			t.Errorf("EP %d: read server-list: %v", ep, err)
			continue
		}
		ids := harness.ServerListIDs(out)
		if !harness.HasCluster(ids, 0x0039) {
			t.Errorf("EP %d: BDBI (0x0039) missing from ServerList %v", ep, ids)
		}
		if !harness.HasCluster(ids, 0x001D) {
			t.Errorf("EP %d: Descriptor (0x001D) missing from ServerList %v", ep, ids)
		}
	}
}
