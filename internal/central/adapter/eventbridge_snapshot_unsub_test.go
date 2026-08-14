// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"context"
	"sync"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/north/mqtt"
)

// TestSnapshotPassesRecordEverySubscriptionUnderConcurrency drives the two
// goroutines that publish snapshots in a running daemon: the MQTT lifecycle's
// reconnect loop (PublishInitialSnapshot, registered as an OnConnect hook) and
// the fan-out worker that publishes a central the moment its readiness-gated
// bring-up completes (PublishCentralSnapshot).
//
// Both walk the same devices and wire the same callbacks, and both must end up
// in the registry detach() reads under startMu. A pass whose bookkeeping is
// lost leaves a handler publishing into a torn-down bridge for the rest of the
// process lifetime; under -race the same window is a reported data race.
//
// The two assertions are the whole contract: every pass over the same objects
// converges on ONE installed subscription (they are keyed by object identity —
// see eventbridge_live_subs.go), and Stop() finds and releases it.
func TestSnapshotPassesRecordEverySubscriptionUnderConcurrency(t *testing.T) {
	t.Parallel()

	reg, dev := registryWithDevice(t)
	// An updatable device gets a firmware-state OnChange subscription wired on
	// every snapshot pass — one subscription for the single device, no matter
	// how many passes run.
	dev.Firmware().Set(device.FirmwareInfo{Current: "1.0.0", Updatable: true})
	dev.AttachUpdate(nil, nil)

	pub := mqtt.NewNoopClient()
	bridge := mqtt.NewBridge(mqtt.BridgeConfig{
		Base:        "openccu-loom",
		CentralName: "ccu-01",
		RawEnabled:  true,
	}, pub)

	eb := NewEventBridge(reg, nil, mqtt.NewWiring(bridge, nil))
	eb.Start(context.Background())

	const passes = 40
	var wg sync.WaitGroup
	wg.Go(func() {
		for range passes {
			eb.PublishInitialSnapshot(context.Background())
		}
	})
	wg.Go(func() {
		for range passes {
			eb.PublishCentralSnapshot(context.Background(), "ccu-01")
		}
	})
	wg.Wait()

	if got := eb.liveSubCount(); got != 1 {
		t.Fatalf("%d subscriptions recorded across %d concurrent snapshot passes over one "+
			"firmware tracker, want 1", got, 2*passes)
	}

	eb.Stop()
	if got := eb.liveSubCount(); got != 0 {
		t.Fatalf("Stop left %d subscriptions unreleased", got)
	}
}

// TestSnapshotAfterStopReleasesItsOwnSubscriptions pins the second half of the
// contract: a snapshot pass that finishes after the bridge was torn down must
// not leave a handler behind, otherwise it publishes into a stopped bridge
// with nothing left to release it.
func TestSnapshotAfterStopReleasesItsOwnSubscriptions(t *testing.T) {
	t.Parallel()

	reg, dev := registryWithDevice(t)
	dev.Firmware().Set(device.FirmwareInfo{Current: "1.0.0", Updatable: true})
	dev.AttachUpdate(nil, nil)

	pub := mqtt.NewNoopClient()
	bridge := mqtt.NewBridge(mqtt.BridgeConfig{
		Base:        "openccu-loom",
		CentralName: "ccu-01",
		RawEnabled:  true,
	}, pub)

	eb := NewEventBridge(reg, nil, mqtt.NewWiring(bridge, nil))
	eb.Start(context.Background())
	eb.Stop()

	eb.PublishInitialSnapshot(context.Background())

	if got := eb.liveSubCount(); got != 0 {
		t.Fatalf("post-Stop snapshot recorded %d subscriptions nobody will release", got)
	}
}
