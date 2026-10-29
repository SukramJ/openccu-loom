// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package main

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
)

// reachableRecorder captures NotifyDeviceReachable-shaped forwards.
type reachableRecorder struct {
	mu    sync.Mutex
	calls []reachableCall
}

type reachableCall struct {
	central   string
	address   string
	reachable bool
}

func (r *reachableRecorder) notify(centralName, address string, reachable bool) {
	r.mu.Lock()
	r.calls = append(r.calls, reachableCall{central: centralName, address: address, reachable: reachable})
	r.mu.Unlock()
}

func (r *reachableRecorder) snapshot() []reachableCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]reachableCall(nil), r.calls...)
}

// TestMatterCentralHook_AdoptedCentralReadyEventReassembles drives the
// live-adopt shape: an adopted central firing its CentralSouthboundReadyEvent
// must run the reassemble callback through the shared debounce pipeline, or
// its persisted exposures never assemble.
func TestMatterCentralHook_AdoptedCentralReadyEventReassembles(t *testing.T) {
	t.Parallel()
	reg := buildTestRegistry(t, "ccu-boot", "ccu-adopted")

	var reassembles atomic.Int32
	reassembled := make(chan struct{}, 8)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	// Zero boot-time buses: the daemon booted before the central existed.
	_, trigger := wireMatterReassembleOnReady(ctx, nil, func(context.Context) error {
		reassembles.Add(1)
		reassembled <- struct{}{}
		return nil
	}, 20*time.Millisecond, discardLogger())

	hook := newMatterCentralHook(trigger, nil, nil)
	adopted, _ := reg.Get("ccu-adopted")
	unwire := hook(adopted)
	if unwire == nil {
		t.Fatal("hook returned nil unwire for a unit with an event bus")
	}
	t.Cleanup(unwire)

	adopted.EventBus.Publish(hmevent.CentralSouthboundReadyEvent{
		Base:        hmevent.NewBase(),
		CentralName: "ccu-adopted",
	})

	select {
	case <-reassembled:
	case <-time.After(2 * time.Second):
		t.Fatal("adopted central's ready event did not trigger a reassemble")
	}
	if got := reassembles.Load(); got != 1 {
		t.Fatalf("reassemble count = %d, want 1", got)
	}
}

// TestMatterCentralHook_KicksAnAlreadyReadyCentral pins the adopt-window
// race: a central whose bring-up completed BEFORE the hook ran must still
// receive a reassemble kick, since the ready event that would have triggered
// one is gone and will never re-fire.
func TestMatterCentralHook_KicksAnAlreadyReadyCentral(t *testing.T) {
	t.Parallel()
	reg := buildTestRegistry(t, "ccu-adopted")
	adopted, _ := reg.Get("ccu-adopted")
	adopted.MarkSouthboundReady() // ready before the hook exists

	var triggers atomic.Int32
	hook := newMatterCentralHook(func() { triggers.Add(1) }, nil, nil)
	unwire := hook(adopted)
	if unwire == nil {
		t.Fatal("hook returned nil unwire")
	}
	t.Cleanup(unwire)

	if got := triggers.Load(); got != 1 {
		t.Errorf("reassemble trigger fired %d times, want 1 (kick for the missed ready event)", got)
	}
}

// TestMatterCentralHook_ForwardsAvailabilityChanges verifies the hook wires
// the device-availability → Reachable forward for an adopted central: only
// AVAILABILITY_CHANGED lifecycle events reach the bridge notifier, carrying
// the central name, device address and new availability.
func TestMatterCentralHook_ForwardsAvailabilityChanges(t *testing.T) {
	t.Parallel()
	reg := buildTestRegistry(t, "ccu-adopted")
	adopted, _ := reg.Get("ccu-adopted")

	rec := &reachableRecorder{}
	hook := newMatterCentralHook(nil, nil, rec.notify)
	unwire := hook(adopted)
	if unwire == nil {
		t.Fatal("hook returned nil unwire")
	}
	t.Cleanup(unwire)

	adopted.EventBus.Publish(hmevent.DeviceLifecycleEvent{
		Base:        hmevent.NewBase(),
		CentralName: "ccu-adopted",
		Address:     "VCU0000001",
		Subtype:     hmenum.DeviceLifecycleSubtypeAvailabilityChanged,
		Available:   false,
	})
	// A non-availability subtype must not forward.
	adopted.EventBus.Publish(hmevent.DeviceLifecycleEvent{
		Base:        hmevent.NewBase(),
		CentralName: "ccu-adopted",
		Address:     "VCU0000002",
		Subtype:     hmenum.DeviceLifecycleSubtypeCreated,
	})

	calls := rec.snapshot()
	if len(calls) != 1 {
		t.Fatalf("reachable forwards = %d, want 1 (availability change only)", len(calls))
	}
	want := reachableCall{central: "ccu-adopted", address: "VCU0000001", reachable: false}
	if calls[0] != want {
		t.Errorf("forwarded call = %+v, want %+v", calls[0], want)
	}
}

// TestMatterCentralHook_UnwireStopsSubscriptions verifies live-remove
// hygiene: after the unwire runs, neither a late ready event nor a late
// lifecycle event reaches the reassemble pipeline or the reachable notifier.
func TestMatterCentralHook_UnwireStopsSubscriptions(t *testing.T) {
	t.Parallel()
	reg := buildTestRegistry(t, "ccu-adopted")
	adopted, _ := reg.Get("ccu-adopted")

	var triggers atomic.Int32
	rec := &reachableRecorder{}
	hook := newMatterCentralHook(func() { triggers.Add(1) }, nil, rec.notify)
	unwire := hook(adopted)
	if unwire == nil {
		t.Fatal("hook returned nil unwire")
	}
	unwire()

	adopted.EventBus.Publish(hmevent.CentralSouthboundReadyEvent{
		Base:        hmevent.NewBase(),
		CentralName: "ccu-adopted",
	})
	adopted.EventBus.Publish(hmevent.DeviceLifecycleEvent{
		Base:      hmevent.NewBase(),
		Address:   "VCU0000001",
		Subtype:   hmenum.DeviceLifecycleSubtypeAvailabilityChanged,
		Available: true,
	})

	if got := triggers.Load(); got != 0 {
		t.Errorf("reassemble trigger fired %d times after unwire, want 0", got)
	}
	if got := rec.snapshot(); len(got) != 0 {
		t.Errorf("reachable forwards after unwire = %v, want none", got)
	}
}

// TestMatterCentralHook_NilUnitIsNoop pins nil-safety: the adopt path calls
// the hook unconditionally when the bridge is enabled, so a nil unit (or one
// without an event bus) must yield a nil unwire instead of panicking.
func TestMatterCentralHook_NilUnitIsNoop(t *testing.T) {
	t.Parallel()
	hook := newMatterCentralHook(func() {}, nil, nil)
	if unwire := hook(nil); unwire != nil {
		t.Error("hook(nil unit) returned a non-nil unwire, want nil")
	}
}

// TestMatterCentralHook_ForgetsADeviceTheCCUDeleted pins the forget the hook
// wires on an adopted central: a device the CCU deleted is handed to forget,
// so its endpoint numbers are freed, while the cache-clear re-init's
// teardown — every device removed without the operator deleting any —
// forgets nothing.
func TestMatterCentralHook_ForgetsADeviceTheCCUDeleted(t *testing.T) {
	t.Parallel()
	reg := buildTestRegistry(t, "ccu-adopted")
	var mu sync.Mutex
	var forgotten []string
	hook := newMatterCentralHook(func() {}, func(centralName, address string) {
		mu.Lock()
		forgotten = append(forgotten, centralName+"|"+address)
		mu.Unlock()
	}, nil)
	adopted, _ := reg.Get("ccu-adopted")
	unwire := hook(adopted)
	if unwire == nil {
		t.Fatal("hook returned nil unwire for a unit with an event bus")
	}
	t.Cleanup(unwire)

	adopted.EventBus.Publish(hmevent.DeviceRemovedEvent{Base: hmevent.NewBase(), CentralName: "ccu-adopted", Address: "TEARDOWN01", ModelTeardown: true})
	adopted.EventBus.Publish(hmevent.DeviceRemovedEvent{Base: hmevent.NewBase(), CentralName: "ccu-adopted", Address: "DELETED001"})

	mu.Lock()
	defer mu.Unlock()
	if len(forgotten) != 1 || forgotten[0] != "ccu-adopted|DELETED001" {
		t.Fatalf("forgotten = %v, want only ccu-adopted|DELETED001", forgotten)
	}
}
