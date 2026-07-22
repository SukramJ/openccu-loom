// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// hub_bidcos_interfaces_test.go covers the HubCoordinator's per-interface
// BidCos radio-utilisation cache and its refresh hook: the SetBidcosInterfaces
// / BidcosInterface accessors, the RefreshBidcosInterfaces delegation, and
// cache-clearing semantics (empty map and Clear).

package coordinators

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central/events"
)

// TestHubRefreshBidcosInterfacesInvokesHook verifies that a hook wired via
// SetRefreshHooks is called when RefreshBidcosInterfaces is invoked.
func TestHubRefreshBidcosInterfacesInvokesHook(t *testing.T) {
	t.Parallel()

	bus := events.NewBus()
	h := NewHubCoordinator("c", bus)

	var called atomic.Int32
	h.SetRefreshHooks(RefreshHooks{
		BidcosInterfaces: func(_ context.Context) error {
			called.Add(1)
			return nil
		},
	})

	if err := h.RefreshBidcosInterfaces(context.Background()); err != nil {
		t.Fatalf("RefreshBidcosInterfaces: %v", err)
	}
	if called.Load() != 1 {
		t.Fatalf("BidcosInterfaces hook called %d times, want 1", called.Load())
	}
}

// TestHubRefreshBidcosInterfacesNilHookIsNoop verifies that RefreshBidcosInterfaces
// with no wired hook returns nil without panicking.
func TestHubRefreshBidcosInterfacesNilHookIsNoop(t *testing.T) {
	t.Parallel()
	h := NewHubCoordinator("c", events.NewBus())
	if err := h.RefreshBidcosInterfaces(context.Background()); err != nil {
		t.Fatalf("RefreshBidcosInterfaces: %v", err)
	}
}

// TestHubBidcosInterfaceCache verifies that SetBidcosInterfaces stores a
// snapshot readable via BidcosInterface, that unknown ids miss, and that the
// stored map is copied (mutating the caller's map does not affect the cache).
func TestHubBidcosInterfaceCache(t *testing.T) {
	t.Parallel()
	h := NewHubCoordinator("c", events.NewBus())

	if _, ok := h.BidcosInterface("BidCos-RF"); ok {
		t.Fatal("expected miss on empty cache")
	}

	src := map[string]BidcosInterfaceInfo{
		"BidCos-RF": {Address: "OEQ1234567", Type: "CCU2", DutyCycle: 42, CarrierSense: -1, Connected: true},
	}
	h.SetBidcosInterfaces(src)

	// Mutating the source after Set must not change the cache.
	src["BidCos-RF"] = BidcosInterfaceInfo{DutyCycle: 99}

	info, ok := h.BidcosInterface("BidCos-RF")
	if !ok {
		t.Fatal("expected hit after SetBidcosInterfaces")
	}
	if info.DutyCycle != 42 || info.Address != "OEQ1234567" || !info.Connected {
		t.Fatalf("info = %+v", info)
	}
	if info.CarrierSense != -1 {
		t.Fatalf("CarrierSense = %d, want -1", info.CarrierSense)
	}

	if _, ok := h.BidcosInterface("HmIP-RF"); ok {
		t.Fatal("expected miss for interface without a snapshot")
	}
}

// TestHubBidcosInterfacesEmptyClears verifies that an empty map clears the
// cache, and that Clear drops the snapshot too.
func TestHubBidcosInterfacesEmptyClears(t *testing.T) {
	t.Parallel()
	h := NewHubCoordinator("c", events.NewBus())
	h.SetBidcosInterfaces(map[string]BidcosInterfaceInfo{
		"BidCos-RF": {DutyCycle: 10},
	})
	if _, ok := h.BidcosInterface("BidCos-RF"); !ok {
		t.Fatal("expected hit before clear")
	}

	h.SetBidcosInterfaces(nil)
	if _, ok := h.BidcosInterface("BidCos-RF"); ok {
		t.Fatal("expected miss after empty set")
	}

	h.SetBidcosInterfaces(map[string]BidcosInterfaceInfo{"BidCos-RF": {DutyCycle: 10}})
	h.Clear()
	if _, ok := h.BidcosInterface("BidCos-RF"); ok {
		t.Fatal("expected miss after Clear")
	}
}
