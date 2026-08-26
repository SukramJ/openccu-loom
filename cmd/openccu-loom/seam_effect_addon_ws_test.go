// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package main

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/addonupdate"
	"github.com/SukramJ/openccu-loom/internal/north/rest/ws"
	"github.com/SukramJ/openccu-loom/internal/wiring"
)

// TestSeamEffect_AddonUpdateWS_ReachesAWebSocketClient asserts what the
// ws.addon_update_status seam's Why claims: that an add-on update's
// progress reaches a WebSocket client.
//
// The observable is a broadcast on the add-on-update topic, not the
// presence of a subscription. A listener registered on the wrong updater,
// or one whose callback published to a different topic, would leave every
// SPA showing the state it had when the page loaded — and would satisfy
// any assertion about the subscription itself.
func TestSeamEffect_AddonUpdateWS_ReachesAWebSocketClient(t *testing.T) {
	hub := ws.NewHub()
	updater := seamEffectUpdater(t)

	unsub := wireAddonUpdateWS(wiring.NewManifest(), updater, hub)
	t.Cleanup(func() {
		if unsub != nil {
			unsub()
		}
	})

	// Check transitions into the busy state before it touches the network,
	// and that transition is a status change like any other. What it does
	// afterwards (fail, with no release server to reach) does not matter
	// here: the seam's claim is about progress reaching a client at all.
	_ = updater.Check(context.Background())

	ev := waitForWSTopic(t, hub, "system.addon_update")
	if _, ok := ev.Payload.(ws.AddonUpdateStatusPayload); !ok {
		t.Errorf("broadcast payload is %T, want ws.AddonUpdateStatusPayload — a client "+
			"decoding the documented shape would drop it", ev.Payload)
	}
}

// seamEffectUpdater builds an updater the platform check accepts, so
// Check gets past its ErrUnsupported guard and emits a transition. The
// probe is the only thing stubbed; the updater itself is the production
// constructor.
func seamEffectUpdater(t *testing.T) *addonupdate.Updater {
	t.Helper()

	installer := t.TempDir() + "/installer"
	if err := os.WriteFile(installer, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil { //nolint:gosec // an executable stub is the point
		t.Fatalf("write installer stub: %v", err)
	}
	return addonupdate.NewUpdater(addonupdate.Deps{
		Capability: addonupdate.CapabilityProbe{
			IsAddonBuild:  func() bool { return true },
			StatInstaller: func(string) (os.FileInfo, error) { return os.Stat(installer) },
		},
		Logger:  discardTestLogger(),
		Context: context.Background(),
	})
}

// TestSeamEffect_AddonUpdateWS_IsAttributableToTheSeam is the negative
// control: without the wiring, the same status change must reach no
// client. Without it, a broadcast from any other source would read as
// success.
func TestSeamEffect_AddonUpdateWS_IsAttributableToTheSeam(t *testing.T) {
	hub := ws.NewHub()
	updater := seamEffectUpdater(t)

	_ = updater.Check(context.Background())

	// Publish runs on the caller's goroutine, so anything the status change
	// was going to broadcast is already buffered. A short settle keeps the
	// control honest against a listener that hands off to a goroutine.
	time.Sleep(100 * time.Millisecond)
	if events := wsEventsOnTopic(hub, "system.addon_update"); len(events) > 0 {
		t.Errorf("%d add-on-update broadcast(s) arrived without the seam being wired — "+
			"something else publishes them, so the test above proves nothing about this seam",
			len(events))
	}
}
