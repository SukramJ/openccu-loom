// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package bridge_test

import (
	"context"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/north/matter/bridge"
	"github.com/SukramJ/openccu-loom/internal/north/matter/mdns"
)

// activeOperationalInstances collects the InstanceName of every
// operational (`_matter._tcp`) record currently published by noop.
func activeOperationalInstances(noop *mdns.Noop) map[string]bool {
	out := make(map[string]bool)
	active := noop.Active()
	for i := range active {
		if active[i].ServiceType == mdns.ServiceTypeOperational {
			out[active[i].InstanceName] = true
		}
	}
	return out
}

// TestWithdrawFabric_RemovesAnnouncedOperationalRecord verifies that
// [bridge.Bridge.WithdrawFabric] retracts the operational `_matter._tcp`
// instance a prior [bridge.Bridge.AnnounceFabric] published for the same
// (compressedFabricID, nodeID) identity. A republish alone cannot retire
// the record — the advertiser only re-announces what it still holds —
// so RemoveFabric and a NodeID-changing UpdateNOC both need the explicit
// withdraw. Mirrors matter.js DeviceAdvertiser.ts:76-86.
func TestWithdrawFabric_RemovesAnnouncedOperationalRecord(t *testing.T) {
	t.Parallel()
	noop := mdns.NewNoop()
	b, err := bridge.New(bridge.NewFakeStore(), emptySnapshotter, noop, bridge.Config{
		Listen:    ":0",
		VendorID:  0x1234,
		ProductID: 0x5678,
		NodeLabel: "test-withdraw-fabric",
	}, nil)
	if err != nil {
		t.Fatalf("New: unexpected error: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := b.Start(ctx); err != nil {
		t.Fatalf("Start: unexpected error: %v", err)
	}
	t.Cleanup(func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer stopCancel()
		_ = b.Stop(stopCtx)
	})

	compressedID := [8]byte{0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77, 0x88}
	nodeID := uint64(0x0102030405060708)

	b.AnnounceFabric(ctx, compressedID, nodeID)
	svc := mdns.BuildOperationalService(mdns.OperationalServiceConfig{
		CompressedFabricID: compressedID,
		NodeID:             nodeID,
	})
	if !activeOperationalInstances(noop)[svc.InstanceName] {
		t.Fatalf("AnnounceFabric: instance %q not found in Active() after publish", svc.InstanceName)
	}

	b.WithdrawFabric(ctx, compressedID, nodeID)
	if activeOperationalInstances(noop)[svc.InstanceName] {
		t.Errorf("WithdrawFabric: instance %q still present in Active() after withdraw", svc.InstanceName)
	}
}

// TestWithdrawFabric_NeverAnnounced_NoPanic verifies that withdrawing an
// identity that was never announced (e.g. RemoveFabric racing a bridge
// restart before AnnounceFabric ran) does not panic — [mdns.Noop.Withdraw]
// returns [mdns.ErrServiceNotFound] for that case, and [bridge.Bridge.
// WithdrawFabric] logs and returns rather than propagating the error.
func TestWithdrawFabric_NeverAnnounced_NoPanic(t *testing.T) {
	t.Parallel()
	noop := mdns.NewNoop()
	b, err := bridge.New(bridge.NewFakeStore(), emptySnapshotter, noop, bridge.Config{
		Listen:    ":0",
		VendorID:  0x1234,
		ProductID: 0x5678,
		NodeLabel: "test-withdraw-fabric-noop",
	}, nil)
	if err != nil {
		t.Fatalf("New: unexpected error: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := b.Start(ctx); err != nil {
		t.Fatalf("Start: unexpected error: %v", err)
	}
	t.Cleanup(func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer stopCancel()
		_ = b.Stop(stopCtx)
	})

	// No AnnounceFabric call preceded this — must not panic.
	b.WithdrawFabric(ctx, [8]byte{0xAA, 0xBB}, 0xDEADBEEF)
}
