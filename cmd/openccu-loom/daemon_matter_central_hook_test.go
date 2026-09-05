// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package main

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central"
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
// live-adopt shape end to end, through the two halves production wires: the
// registry observer that owns the readiness latch and the per-central hook
// that owns the reassemble pipeline. An adopted central firing its
// CentralSouthboundReadyEvent must (a) latch the tracker the snapshotter
// reads, so its snapshot stamps ModelComplete, and (b) run the reassemble
// callback through the shared debounce pipeline. Without both an adopted
// central stays model-incomplete forever and its persisted exposures never
// assemble.
func TestMatterCentralHook_AdoptedCentralReadinessReachesSnapshotter(t *testing.T) {
	t.Parallel()
	reg := buildTestRegistry(t, "ccu-boot", "ccu-adopted")
	readiness, unwireReadiness := wireMatterCentralReadiness(reg)
	t.Cleanup(unwireReadiness)

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

	hook := newMatterCentralHook(trigger, nil)
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
	byName := map[string]endpoint.DeviceSnapshot{}
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
	hook := newMatterCentralHook(func() { triggers.Add(1) }, nil)
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
	hook := newMatterCentralHook(nil, rec.notify)
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
// lifecycle event reaches the reassemble pipeline or the reachable notifier.
func TestMatterCentralHook_UnwireStopsSubscriptions(t *testing.T) {
	t.Parallel()
	reg := buildTestRegistry(t, "ccu-adopted")
	adopted, _ := reg.Get("ccu-adopted")

	var triggers atomic.Int32
	rec := &reachableRecorder{}
	hook := newMatterCentralHook(func() { triggers.Add(1) }, rec.notify)
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
	hook := newMatterCentralHook(func() {}, nil)
	if unwire := hook(nil); unwire != nil {
		t.Error("hook(nil unit) returned a non-nil unwire, want nil")
	}
}

// TestMatterCentralHook_ReAdoptedCentralStartsModelIncomplete pins the
// teardown half of the readiness latch.
//
// The latch decides whether the assembler's vanished-source GC trusts a
// central's device list. Removing a central and registering a fresh unit
// under the same name — a re-add, or the SPA's disable/enable toggle — hands
// the snapshotter a unit whose ModelRegistry is still empty, because
// reg.Register runs long before the readiness-gated CCU load. With the
// removed central's latch still set, that empty snapshot is stamped
// ModelComplete, the GC reads every persisted endpoint as vanished and
// deletes it, and the refill renumbers the fleet: controllers key their
// accessory cache on the endpoint number, so Apple Home / Google Home lose
// every accessory of that CCU.
func TestMatterCentralHook_ReAdoptedCentralStartsModelIncomplete(t *testing.T) {
	t.Parallel()
	const name = "ccu-readopted"

	reg := buildTestRegistry(t, name)
	readiness, unwireReadiness := wireMatterCentralReadiness(reg)
	t.Cleanup(unwireReadiness)
	hook := newMatterCentralHook(func() {}, nil)

	first, _ := reg.Get(name)
	unwire := hook(first)
	if unwire == nil {
		t.Fatal("hook returned nil unwire for a unit with an event bus")
	}
	events.Publish(first.EventBus, hmevent.CentralSouthboundReadyEvent{
		Base:        hmevent.NewBase(),
		CentralName: name,
	})
	if !readiness.isReady(name) {
		t.Fatal("precondition: the central must be latched after its ready event")
	}

	// Live remove: the orchestrator runs the hook's unwire, then the unit
	// leaves the registry.
	unwire()
	reg.Unregister(name)

	// Re-adopt: a brand-new unit under the same name, model not loaded yet.
	replacement, err := central.New(central.Config{Name: name})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	if err := reg.Register(replacement); err != nil {
		t.Fatalf("reg.Register: %v", err)
	}
	t.Cleanup(hook(replacement))

	for _, snap := range matterSnapshotter(reg, readiness)(context.Background()) {
		if snap.CentralName != name {
			continue
		}
		if snap.ModelComplete {
			t.Fatalf("re-adopted central reports ModelComplete with %d devices loaded; "+
				"the vanished-source GC would delete every persisted endpoint id",
				len(snap.Devices))
		}
	}
}
