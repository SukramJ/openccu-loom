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
// Both walk the same devices and record the OnChange subscriptions they wire
// so Stop() can release them. Appending to that slice without the lock detach()
// reads it under loses whole passes to the last writer's slice header: the
// dropped subscriptions are never released, so their handlers keep publishing
// into a torn-down bridge for the rest of the process lifetime. Under -race the
// same window is a reported data race.
//
// The assertion is count-based rather than call-based: every subscription a
// pass wires must still be there for Stop() to release.
func TestSnapshotPassesRecordEverySubscriptionUnderConcurrency(t *testing.T) {
	t.Parallel()

	reg, dev := registryWithDevice(t)
	// An updatable device gets a firmware-state OnChange subscription wired on
	// every snapshot pass — one recorded unsub per pass, deterministically.
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

	before := eb.unsubCount()

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

	// Each pass wires exactly one firmware subscription for the single device.
	if got, want := eb.unsubCount()-before, 2*passes; got < want {
		t.Fatalf("recorded %d subscriptions across %d snapshot passes, want %d — "+
			"concurrent passes lost appends to an unsynchronised slice", got, 2*passes, want)
	}

	eb.Stop()
	if got := eb.unsubCount(); got != 0 {
		t.Fatalf("Stop left %d subscriptions unreleased", got)
	}
}

// TestSnapshotAfterStopReleasesItsOwnSubscriptions pins the second half of the
// contract: a snapshot pass that finishes after the bridge was torn down must
// release what it wired instead of recording it, otherwise it leaves handlers
// publishing into a stopped bridge with nothing left to release them.
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

	if got := eb.unsubCount(); got != 0 {
		t.Fatalf("post-Stop snapshot recorded %d subscriptions nobody will release", got)
	}
}

// unsubCount reports how many subscriptions the bridge currently holds for
// teardown. Test-only accessor: the slice is guarded by startMu.
func (b *EventBridge) unsubCount() int {
	b.startMu.Lock()
	defer b.startMu.Unlock()
	return len(b.unsubs)
}
