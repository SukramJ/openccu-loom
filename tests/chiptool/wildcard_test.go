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

// TestWildcard_DescriptorOnAllEndpoints performs a wildcard read of
// Descriptor.* across every endpoint by issuing
// `descriptor read parts-list 0x1234 0xFFFF`. The wildcard endpoint
// `0xFFFF` per Matter Core §8.9.2.3 means "every endpoint matching
// the cluster filter".
//
// Mirrors v9 capability report T8.
func TestWildcard_DescriptorOnAllEndpoints(t *testing.T) {
	b := requireBridge(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	out, err := b.SharedCtl.ReadAttr(ctx, t, "descriptor", "parts-list", 0xFFFF)
	if err != nil {
		t.Fatalf("wildcard descriptor parts-list: %v", err)
	}
	if !harness.AttrReadOK(out) {
		t.Errorf("wildcard read did not succeed:\n%s", out)
	}
	// Wildcard returns multiple AttributePathIBs, one per endpoint.
	// Count Endpoint: markers as a structural sanity check — must be
	// at least 2 (root + aggregator) for the bridge.
	count := strings.Count(out, "Endpoint:")
	if count < 2 {
		t.Errorf("wildcard returned %d endpoint paths, want ≥ 2:\n%s", count, out)
	}
}

// TestWildcard_BDBI_OnAggregatorChildren issues a wildcard
// BDBI.NodeLabel read scoped to the aggregator's children. With
// chip-tool the cluster + attribute pair is fixed and endpoint
// 0xFFFF expands to every matching one — the bridge must emit one
// AttributeReportIB per bridged endpoint.
//
// SKIPs when no bridged endpoints are present (exposures not
// enabled) — the per-endpoint wildcard expansion only makes sense
// against an actually-populated topology.
func TestWildcard_BDBI_OnAggregatorChildren(t *testing.T) {
	b := requireBridge(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	aggOut, err := b.SharedCtl.ReadAttr(ctx, t, "descriptor", "parts-list", 1)
	if err != nil {
		t.Fatalf("read aggregator parts-list: %v", err)
	}
	if len(harness.EndpointsInPartsList(aggOut)) == 0 {
		t.Skip("aggregator has no bridged children — exposures not enabled")
	}

	out, err := b.SharedCtl.ReadAttr(ctx, t, "bridgeddevicebasicinformation", "node-label", 0xFFFF)
	if err != nil {
		t.Fatalf("wildcard bdbi node-label: %v", err)
	}
	if !harness.AttrReadOK(out) {
		t.Errorf("wildcard BDBI read did not succeed:\n%s", out)
	}
	if !strings.Contains(out, "NodeLabel") {
		t.Errorf("wildcard BDBI emitted no NodeLabel:\n%s", out)
	}
}

// TestWildcard_ServerListSpansBridge issues a wildcard ServerList
// read; the response must contain at least the root + aggregator
// ServerList plus every bridged endpoint's.
func TestWildcard_ServerListSpansBridge(t *testing.T) {
	b := requireBridge(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	out, err := b.SharedCtl.ReadAttr(ctx, t, "descriptor", "server-list", 0xFFFF)
	if err != nil {
		t.Fatalf("wildcard server-list: %v", err)
	}
	count := strings.Count(out, "ServerList")
	if count < 2 {
		t.Errorf("wildcard ServerList surfaced %d entries, want ≥ 2 (root + aggregator):\n%s", count, out)
	}
}
