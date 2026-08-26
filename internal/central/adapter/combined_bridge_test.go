// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"sync/atomic"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
)

// fakeCombinedDP is a minimal stand-in implementing
// [CombinedDataPoint]: callers register an OnAnyUpdate listener and
// the test fires Emit() to drive an event through.
type fakeCombinedDP struct {
	cb func(old, next any)
}

func (f *fakeCombinedDP) OnAnyUpdate(fn func(old, next any)) func() {
	f.cb = fn
	return func() { f.cb = nil }
}

// Emit drives a synthetic value change through the listener.
func (f *fakeCombinedDP) Emit(old, next any) {
	if f.cb != nil {
		f.cb(old, next)
	}
}

// TestBridgeCombinedDataPointPublishesValueChangedEvent pins the
// PR-9 contract: every emission from a combined DP yields exactly
// one DataPointValueChangedEvent on the bus, with the channel /
// parameter / interface coordinates the caller supplied.
func TestBridgeCombinedDataPointPublishesValueChangedEvent(t *testing.T) {
	t.Parallel()
	bus := events.NewBus()

	var received atomic.Int32
	unsub := events.Subscribe(bus, func(e hmevent.DataPointValueChangedEvent) {
		if e.Key.ChannelAddress != "ABC0001:1" {
			t.Errorf("Key.ChannelAddress = %q, want ABC0001:1", e.Key.ChannelAddress)
		}
		if e.Key.Parameter != "HS_COLOR" {
			t.Errorf("Key.Parameter = %q, want HS_COLOR", e.Key.Parameter)
		}
		received.Add(1)
	})
	defer unsub()

	dp := &fakeCombinedDP{}
	stop := BridgeCombinedDataPoint(bus, dp, "HmIP-RF", "ABC0001:1", "HS_COLOR", nil)
	defer func() {
		if stop != nil {
			stop()
		}
	}()

	dp.Emit(nil, "h120s50")
	dp.Emit("h120s50", "h0s100")

	if got := received.Load(); got != 2 {
		t.Errorf("received %d events, want 2", got)
	}
}

// TestBridgeCombinedDataPointNilDPIsSafe pins the nil-input safety:
// passing a nil combined DP must produce a no-op closure rather
// than panic.
func TestBridgeCombinedDataPointNilDPIsSafe(t *testing.T) {
	t.Parallel()
	bus := events.NewBus()
	if got := BridgeCombinedDataPoint(bus, nil, "i", "ch", "p", nil); got != nil {
		t.Error("BridgeCombinedDataPoint(nil) must return nil, not a closure")
	}
}
