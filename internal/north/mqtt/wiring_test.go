// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package mqtt

import (
	"context"
	"io"
	"log/slog"
	"testing"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func newWiringWithBridge(t *testing.T) (*Wiring, *Bridge) {
	t.Helper()
	client := NewNoopClient()
	b := NewBridge(BridgeConfig{Base: "test", CentralName: "ccu1", RawEnabled: true}, client)
	w := NewWiring(b, testLogger())
	return w, b
}

// TestWiringSwapBridge_Atomic verifies that SwapBridge atomically replaces
// the internal bridge and returns the previous one.
func TestWiringSwapBridge_Atomic(t *testing.T) {
	t.Parallel()
	client := NewNoopClient()
	bridgeA := NewBridge(BridgeConfig{Base: "a", CentralName: "ccu1"}, client)
	bridgeB := NewBridge(BridgeConfig{Base: "b", CentralName: "ccu1"}, client)

	w := NewWiring(bridgeA, testLogger())
	prev := w.SwapBridge(bridgeB)

	if prev != bridgeA {
		t.Fatalf("SwapBridge returned wrong previous bridge: got %p want %p", prev, bridgeA)
	}
	if w.Bridge() != bridgeB {
		t.Fatalf("Bridge() after swap: got %p want %p", w.Bridge(), bridgeB)
	}
}

// TestWiringSwapBridge_ResetsDiscoveryCache verifies that SwapBridge
// clears the lastDiscovered cache so the next MarkDiscovered call returns
// true and DiscoveryCount resets to zero.
func TestWiringSwapBridge_ResetsDiscoveryCache(t *testing.T) {
	t.Parallel()
	w, _ := newWiringWithBridge(t)

	if !w.MarkDiscovered("obj1", "hashA") {
		t.Fatal("first MarkDiscovered must return true")
	}
	if w.MarkDiscovered("obj1", "hashA") {
		t.Fatal("second MarkDiscovered with same args must return false (cached)")
	}
	if w.DiscoveryCount() != 1 {
		t.Fatalf("DiscoveryCount before swap: got %d want 1", w.DiscoveryCount())
	}

	client := NewNoopClient()
	newBridge := NewBridge(BridgeConfig{Base: "test2", CentralName: "ccu1"}, client)
	w.SwapBridge(newBridge)

	if w.DiscoveryCount() != 0 {
		t.Fatalf("DiscoveryCount after swap: got %d want 0", w.DiscoveryCount())
	}
	if !w.MarkDiscovered("obj1", "hashA") {
		t.Fatal("MarkDiscovered after swap must return true (cache was reset)")
	}
	if w.DiscoveryCount() != 1 {
		t.Fatalf("DiscoveryCount after first post-swap MarkDiscovered: got %d want 1", w.DiscoveryCount())
	}
}

// TestWiringSwapBridge_NilBridgePublishIsNoop verifies that publishing
// through a Wiring whose bridge has been swapped to nil does not panic.
func TestWiringSwapBridge_NilBridgePublishIsNoop(t *testing.T) {
	t.Parallel()
	w, _ := newWiringWithBridge(t)
	w.SwapBridge(nil)

	// Must not panic.
	w.Publish(context.Background(), Event{})
}
