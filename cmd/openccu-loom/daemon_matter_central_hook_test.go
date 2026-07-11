// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package main

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/internal/north/matter/endpoint"
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

// TestMatterCentralHook_AdoptedCentralReadinessReachesSnapshotter drives the
// live-adopt shape end to end: a central wired through the hook (not through
// the boot-time loops) fires its CentralSouthboundReadyEvent, and that must
// (a) latch the SAME readiness tracker the snapshotter reads, so the adopted
// central's snapshot stamps ModelComplete, and (b) run the reassemble
// callback through the shared debounce pipeline. Without the hook an adopted
// central stays model-incomplete forever and its persisted exposures never
// assemble.
func TestMatterCentralHook_AdoptedCentralReadinessReachesSnapshotter(t *testing.T) {
	t.Parallel()
	reg := buildTestRegistry(t, "ccu-boot", "ccu-adopted")
	readiness := newMatterCentralReadiness()

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

	hook := newMatterCentralHook(readiness, trigger, nil)
	adopted, _ := reg.Get("ccu-adopted")
	unwire := hook(adopted)
	if unwire == nil {
		t.Fatal("hook returned nil unwire for a unit with an event bus")
	}
	t.Cleanup(unwire)

	if readiness.isReady("ccu-adopted") {
		t.Fatal("adopted central must start model-incomplete before its ready event")
	}

	events.Publish(adopted.EventBus, hmevent.CentralSouthboundReadyEvent{
		Base:        hmevent.NewBase(),
		CentralName: "ccu-adopted",
	})

	if !readiness.isReady("ccu-adopted") {
		t.Error("adopted central not latched after its southbound-ready event")
	}
	byName := map[string]endpoint.Snapshot{}
	for _, s := range matterSnapshotter(reg, readiness)(context.Background()) {
		byName[s.CentralName] = s
	}
	if !byName["ccu-adopted"].ModelComplete {
		t.Error("adopted central: snapshot ModelComplete = false after ready event, want true")
	}
	if byName["ccu-boot"].ModelComplete {
		t.Error("boot central: ModelComplete = true without ready event, want false")
	}

	select {
	case <-reassembled:
	case <-time.After(2 * time.Second):
		t.Fatal("adopted central's ready event did not trigger a reassemble")
	}
	if got := reassembles.Load(); got != 1 {
		t.Fatalf("reassemble count = %d, want 1", got)
	}
}

// TestMatterCentralHook_SeedsAlreadyReadyCentral pins the adopt-window race:
// a central whose bring-up completed BEFORE the hook ran (the ready event is
// gone) must still be latched — from the unit's queryable flag — and must
// receive a reassemble kick, since the event that would have triggered one
// will never re-fire.
func TestMatterCentralHook_SeedsAlreadyReadyCentral(t *testing.T) {
	t.Parallel()
	reg := buildTestRegistry(t, "ccu-adopted")
	adopted, _ := reg.Get("ccu-adopted")
	adopted.MarkSouthboundReady() // ready before the hook exists

	readiness := newMatterCentralReadiness()
	var triggers atomic.Int32
	hook := newMatterCentralHook(readiness, func() { triggers.Add(1) }, nil)
	unwire := hook(adopted)
	if unwire == nil {
		t.Fatal("hook returned nil unwire")
	}
	t.Cleanup(unwire)

	if !readiness.isReady("ccu-adopted") {
		t.Error("already-ready central not seeded into the readiness tracker")
	}
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
	hook := newMatterCentralHook(newMatterCentralReadiness(), nil, rec.notify)
	unwire := hook(adopted)
	if unwire == nil {
		t.Fatal("hook returned nil unwire")
	}
	t.Cleanup(unwire)

	events.Publish(adopted.EventBus, hmevent.DeviceLifecycleEvent{
		Base:        hmevent.NewBase(),
		CentralName: "ccu-adopted",
		Address:     "VCU0000001",
		Subtype:     hmenum.DeviceLifecycleSubtypeAvailabilityChanged,
		Available:   false,
	})
	// A non-availability subtype must not forward.
	events.Publish(adopted.EventBus, hmevent.DeviceLifecycleEvent{
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
// lifecycle event mutates the readiness tracker, the reassemble pipeline, or
// the reachable notifier.
func TestMatterCentralHook_UnwireStopsSubscriptions(t *testing.T) {
	t.Parallel()
	reg := buildTestRegistry(t, "ccu-adopted")
	adopted, _ := reg.Get("ccu-adopted")

	readiness := newMatterCentralReadiness()
	var triggers atomic.Int32
	rec := &reachableRecorder{}
	hook := newMatterCentralHook(readiness, func() { triggers.Add(1) }, rec.notify)
	unwire := hook(adopted)
	if unwire == nil {
		t.Fatal("hook returned nil unwire")
	}
	unwire()

	events.Publish(adopted.EventBus, hmevent.CentralSouthboundReadyEvent{
		Base:        hmevent.NewBase(),
		CentralName: "ccu-adopted",
	})
	events.Publish(adopted.EventBus, hmevent.DeviceLifecycleEvent{
		Base:      hmevent.NewBase(),
		Address:   "VCU0000001",
		Subtype:   hmenum.DeviceLifecycleSubtypeAvailabilityChanged,
		Available: true,
	})

	if readiness.isReady("ccu-adopted") {
		t.Error("ready event after unwire must not latch readiness")
	}
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
	hook := newMatterCentralHook(newMatterCentralReadiness(), func() {}, nil)
	if unwire := hook(nil); unwire != nil {
		t.Error("hook(nil unit) returned a non-nil unwire, want nil")
	}
}
