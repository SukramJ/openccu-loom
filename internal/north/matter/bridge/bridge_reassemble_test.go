// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package bridge

// White-box tests for Bridge.reassembleLocked that verify subscription
// reaping when endpoints are removed.

import (
	"context"
	"testing"
	"time"

	endpointpkg "github.com/SukramJ/openccu-loom/internal/north/matter/endpoint"
	"github.com/SukramJ/openccu-loom/internal/north/matter/im"
	"github.com/SukramJ/openccu-loom/internal/north/matter/im/subscription"
	"github.com/SukramJ/openccu-loom/internal/north/matter/mdns"
)

// TestReassemble_ReapsSubscriptionsForRemovedEndpoints verifies that when a
// topology swap removes endpoint 5, any subscription referencing endpoint 5
// is closed by the subscription manager.
//
// Mirrors matter.js packages/node/src/behaviors/network/ServerNode.ts
// lifecycle: removing a BridgedNode endpoint tears down its active
// subscriptions via endpoint.lifecycle.remove() →
// packages/protocol/src/interaction/SubscriptionHandler.ts close().
func TestReassemble_ReapsSubscriptionsForRemovedEndpoints(t *testing.T) {
	t.Parallel()

	// Build and start a bridge with a controlled snapshotter.
	// Step 1: the snapshotter returns endpoint 5 in the topology.
	var snapshotterEps []*endpointpkg.Endpoint
	snapshotter := func(_ context.Context) []endpointpkg.Snapshot {
		// Return nil devices; reassembleLocked builds its own root+aggregator,
		// so we patch b.topology directly below.
		return nil
	}

	b, err := New(
		NewFakeStore(),
		snapshotter,
		mdns.NewNoop(),
		Config{
			Listen:    ":0",
			VendorID:  0x1234,
			ProductID: 0x5678,
			NodeLabel: "reassemble-test",
		},
		nil,
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() {
		cancel()
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer stopCancel()
		_ = b.Stop(stopCtx)
	})
	if err := b.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Wire a subscription manager.
	mgr := subscription.NewManager(subscription.Config{}, nil, nil)
	b.AttachSubscriptionManager(mgr)

	// Inject a synthetic "old" topology that includes endpoint 5.
	ep5 := &endpointpkg.Endpoint{ID: 5, DeviceType: 0x010A, Reachable: true}
	_ = snapshotterEps // suppress unused warning
	snapshotterEps = []*endpointpkg.Endpoint{ep5}
	_ = snapshotterEps

	b.mu.Lock()
	// Install a topology that contains ep 5 so the next reassemble sees
	// it in prevTopology and can detect the removal.
	b.topology = &endpointpkg.Topology{
		Endpoints: []*endpointpkg.Endpoint{
			{ID: 0, DeviceType: 0x0016}, // root
			{ID: 1, DeviceType: 0x000E}, // aggregator
			ep5,
		},
	}
	b.mu.Unlock()

	// Subscribe to endpoint 5.
	sub, err := mgr.Subscribe(subscription.SubscribeArgs{
		FabricIndex:        1,
		PeerNodeID:         0xDEAD,
		SessionID:          10,
		MinIntervalFloor:   1,
		MaxIntervalCeiling: 60,
		AttributePaths: []im.ConcreteAttributePath{
			{Endpoint: 5, Cluster: 0x0006, Attribute: 0x0000, HasEndpoint: true, HasCluster: true, HasAttribute: true},
		},
	})
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	if mgr.Active() != 1 {
		t.Fatalf("Active() = %d, want 1 before reassemble", mgr.Active())
	}

	// Now reassemble with a topology that does NOT contain endpoint 5.
	// The assembler will produce root+aggregator only (snapshotter returns nil).
	if err := b.Reassemble(ctx); err != nil {
		t.Fatalf("Reassemble: %v", err)
	}

	// Endpoint 5 was in the old topology and absent in the new one —
	// its subscription must have been reaped.
	if mgr.Active() != 0 {
		t.Errorf("manager.Active() = %d after reassemble without ep5, want 0 (sub must be closed)", mgr.Active())
	}
	if !sub.IsClosed() {
		t.Error("subscription for endpoint=5 should be closed after endpoint removal")
	}
}
