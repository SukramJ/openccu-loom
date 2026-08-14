// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"context"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/north/rest/ws"
)

// TestDeferredDeviceReachesTheInboxBroadcast crosses the whole chain a
// deferred device travels on its way to an open SPA: the newDevices
// callback parks it, the queue is mirrored onto the hub inbox aggregate,
// and the aggregate's change hook reaches the WebSocket broadcast the
// clients subscribe to. Each half was provable on its own while the
// deferred device was invisible on every surface, which is why this pin
// walks the whole chain instead.
func TestDeferredDeviceReachesTheInboxBroadcast(t *testing.T) {
	t.Parallel()

	reg := central.NewRegistry()
	cu, err := central.New(central.Config{Name: "home"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	if err := reg.Register(cu); err != nil {
		t.Fatalf("reg.Register: %v", err)
	}
	hub := ws.NewHub()
	sub := ws.NewHubEventsSubscriber(reg, hub)
	sub.Start()
	t.Cleanup(sub.Stop)

	h := NewCallbackHandlers(cu, nil)
	defer h.Stop()
	h.SetDelayNewDeviceCreation(true)
	if err := h.NewDevices(context.Background(), "HmIP-RF", newDeviceDescs()); err != nil {
		t.Fatalf("NewDevices: %v", err)
	}

	topic := ws.InboxTopic("home")
	deadline := time.Now().Add(2 * time.Second)
	for {
		res := hub.Replay(0, func(candidate string) bool { return candidate == topic })
		if len(res.Events) > 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("no inbox broadcast for the deferred device: an open SPA never learns it is waiting")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
