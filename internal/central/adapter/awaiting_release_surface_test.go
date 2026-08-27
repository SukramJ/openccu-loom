// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"context"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// TestAwaitingReleaseReachesTheInboxSurface pins the wizard's middle state
// onto the surface an operator can actually see.
//
// A device between the accept and the release is on no other list: it is
// materialised, so nothing announces it as new, and withheld, so no
// ecosystem shows it. Without this it would be configurable but
// unfindable — the operator would have to know its address to reach it.
func TestAwaitingReleaseReachesTheInboxSurface(t *testing.T) {
	t.Parallel()
	c, err := central.New(central.Config{Name: "ccu-await-surface"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	ctx := context.Background()
	iface := hmtypes.ParseWireInterfaceID("ccu-await-surface-HmIP-RF")

	c.Devices.StoreDelayedDeviceDescriptions(ctx, iface, gateDescs()[:2])
	_ = c.Devices.TakeDelayedDeviceDescriptions(ctx, iface, "GATE0001")
	p := NewDevicePipeline(c)
	if err := p.Ingest(ctx, string(iface), hmenum.InterfaceHmIPRF, gateDescs()); err != nil {
		t.Fatalf("Ingest: %v", err)
	}

	PublishAwaitingRelease(c)

	listed := c.HubModel.Inbox.List()
	if len(listed) != 1 || listed[0].Address != "GATE0001" {
		t.Fatalf("inbox = %+v, want exactly the withheld GATE0001", listed)
	}
	if !listed[0].AwaitingRelease {
		t.Error("the entry is not flagged awaiting_release — a client would offer to accept an already-accepted device")
	}
	if listed[0].PendingCreation {
		t.Error("the entry is flagged pending_creation too; the two states are different asks and must not read the same")
	}

	// Negative control: releasing takes it off the surface. Without this
	// half the test would pass on a publisher that lists everything.
	if !ReleaseDevice(ctx, c, "GATE0001") {
		t.Fatal("ReleaseDevice reported nothing to release")
	}
	if listed := c.HubModel.Inbox.List(); len(listed) != 0 {
		t.Errorf("inbox still lists %+v after the release", listed)
	}
}
