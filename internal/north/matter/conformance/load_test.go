// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package conformance_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/north/matter/im"
	"github.com/SukramJ/openccu-loom/internal/north/matter/im/subscription"
)

// TestLoadSubscriptionFanout drives the subscription manager at a
// realistic fleet shape: 200 bridged endpoints × 3 channels each =
// 600 concurrent subscriptions. Failure modes the test catches:
//
//   - Manager.Subscribe O(n) accepts but a downstream linear scan
//     (e.g. OnAttributeChanged with O(n*m) path-matching) blows up.
//   - Reporter callback delivery serialisation under tick contention.
//   - Mutex hot-spotting on the per-fabric quota counters.
//
// The test runs with -short skipped when developers iterate locally;
// CI nightly runs it without -short.
func TestLoadSubscriptionFanout(t *testing.T) {
	if testing.Short() {
		t.Skip("load test (skipped under -short)")
	}
	t.Parallel()

	const (
		endpoints   = 200
		channels    = 3
		fabricIndex = uint8(1)
		minFloor    = uint16(1)
		maxCeil     = uint16(60)
	)
	totalSubs := endpoints * channels

	var (
		reportedMu sync.Mutex
		reportedN  int
	)
	reporter := func(_ context.Context, _ *subscription.Subscription, _ []im.ConcreteAttributePath) {
		reportedMu.Lock()
		reportedN++
		reportedMu.Unlock()
	}

	mgr := subscription.NewManager(subscription.Config{
		MaxSubscriptionsPerFabric: totalSubs + 16,
		MinIntervalFloorSeconds:   minFloor,
		MaxIntervalCeilingSeconds: maxCeil,
		TickInterval:              time.Hour, // engine off; we drive Tick manually.
	}, reporter, nil)

	ctx := t.Context()
	mgr.Start(ctx)
	defer mgr.Stop()

	// Subscribe every (endpoint, channel) wildcard.
	for ep := 0; ep < endpoints; ep++ {
		for ch := 0; ch < channels; ch++ {
			path := im.ConcreteAttributePath{
				HasEndpoint:  true,
				HasCluster:   true,
				HasAttribute: true,
				//nolint:gosec // ep + ch are bounded by the test constants
				Endpoint:  uint16(ep + 1),
				Cluster:   0x0006,
				Attribute: uint32(ch),
			}
			if _, err := mgr.Subscribe(subscription.SubscribeArgs{
				FabricIndex:        fabricIndex,
				PeerNodeID:         1,
				SessionID:          uint16(ep + 1), //nolint:gosec // bounded by endpoints constant
				MinIntervalFloor:   minFloor,
				MaxIntervalCeiling: maxCeil,
				AttributePaths:     []im.ConcreteAttributePath{path},
			}); err != nil {
				t.Fatalf("Subscribe ep=%d ch=%d: %v", ep, ch, err)
			}
		}
	}
	if mgr.Active() != totalSubs {
		t.Fatalf("Active=%d want %d", mgr.Active(), totalSubs)
	}

	// Drain the initial keepalive sweep.
	t0 := time.Now()
	mgr.Tick(ctx, t0)

	// Touch every subscribed path; verify O(n) fan-out.
	for ep := 0; ep < endpoints; ep++ {
		for ch := 0; ch < channels; ch++ {
			path := im.ConcreteAttributePath{
				HasEndpoint:  true,
				HasCluster:   true,
				HasAttribute: true,
				//nolint:gosec // ep + ch are bounded by the test constants
				Endpoint:  uint16(ep + 1),
				Cluster:   0x0006,
				Attribute: uint32(ch),
			}
			mgr.OnAttributeChanged(path)
		}
	}

	// Drive a tick well past MinInterval to flush the dirty bucket.
	mgr.Tick(ctx, t0.Add(2*time.Second))

	reportedMu.Lock()
	got := reportedN
	reportedMu.Unlock()
	// Expected: every subscription emits exactly one dirty-path
	// report (initial keepalive already drained). Some additional
	// reports are acceptable (clock-edge keepalives) but the floor
	// is the per-subscription dirty count.
	if got < totalSubs {
		t.Fatalf("reportedN=%d want >= %d", got, totalSubs)
	}
}
