// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package central

import (
	"context"
	"strconv"
	"sync"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
)

// TestRenameDeviceWithChannelsPublishesAfterTheChannelsAreRenamed pins
// the ordering the north-bound re-snapshot depends on.
//
// The metadata event makes the EventBridge restamp every channel's
// cached name from the live model and republish the device's discovery
// payload. Publishing it before the channel loop ran meant that
// re-snapshot could read — and then publish — the *old* channel names,
// and nothing publishes again afterwards, so the stale labels survive
// until a broker reconnect. RenameChannel already publishes after the
// mutation; this is the same contract for the whole-device rename.
func TestRenameDeviceWithChannelsPublishesAfterTheChannelsAreRenamed(t *testing.T) {
	c := newTestCentral(t)

	const addr = "DEVREN01"
	dev := device.New(device.Config{
		InterfaceID: "HmIP-RF",
		Interface:   hmenum.InterfaceHmIPRF,
		Address:     addr,
		Model:       "HmIP-BSM",
	})
	for _, no := range []int{1, 2, 3} {
		dev.AddChannel(addr+":"+strconv.Itoa(no), no, "SWITCH_VIRTUAL_RECEIVER", hmenum.ParamsetKeyValues)
	}
	c.ModelRegistry.Put(dev)

	var (
		mu       sync.Mutex
		observed [][]string
	)
	unsubscribe := events.Subscribe(c.EventBus, func(_ hmevent.DeviceMetadataChangedEvent) {
		// Snapshot the channel names as they are at publish time — that
		// is exactly what the discovery re-snapshot reads.
		names := make([]string, 0, 3)
		for _, ch := range dev.Channels() {
			names = append(names, ch.Name())
		}
		mu.Lock()
		observed = append(observed, names)
		mu.Unlock()
	})
	defer unsubscribe()

	if err := c.RenameDeviceWithChannels(context.Background(), addr, "Kueche", true); err != nil {
		t.Fatalf("RenameDeviceWithChannels: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(observed) != 1 {
		t.Fatalf("metadata events: got %d, want exactly 1 (%v)", len(observed), observed)
	}
	want := []string{"Kueche:1", "Kueche:2", "Kueche:3"}
	for i, got := range observed[0] {
		if got != want[i] {
			t.Errorf("channel %d name at publish time: got %q, want %q", i+1, got, want[i])
		}
	}
}
