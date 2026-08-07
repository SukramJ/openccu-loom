// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package coordinators

import (
	"context"
	"errors"
	"sort"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/internal/reqctx"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// TestHubUpdateSysvarEmitsChangedEventOnly verifies that the first
// UpdateSysvar call emits a SysvarChangedEvent, and that a second call
// with the same value does not emit a second event.
func TestHubUpdateSysvarEmitsChangedEventOnly(t *testing.T) {
	t.Parallel()
	bus := events.NewBus()
	var count atomic.Int32
	events.Subscribe(bus, func(_ hmevent.SysvarChangedEvent) {
		count.Add(1)
	})
	h := NewHubCoordinator("c1", bus)

	h.UpdateSysvar(context.Background(), SysvarSnapshot{
		Name:      "Var1",
		Value:     hmtypes.IntValue(42),
		ValueType: hmenum.HubValueTypeInteger,
	})
	if count.Load() != 1 {
		t.Fatalf("first UpdateSysvar must emit exactly one event, got %d", count.Load())
	}

	// Same value — no second event.
	h.UpdateSysvar(context.Background(), SysvarSnapshot{
		Name:      "Var1",
		Value:     hmtypes.IntValue(42),
		ValueType: hmenum.HubValueTypeInteger,
	})
	if count.Load() != 1 {
		t.Fatalf("identical value must not emit a second event, got %d", count.Load())
	}
}

// TestHubUpdateSysvarEmitsOnValueDelta verifies that a value change from
// 1 → 2 produces two total events and that each event carries the correct
// OldValue / NewValue payload.
func TestHubUpdateSysvarEmitsOnValueDelta(t *testing.T) {
	t.Parallel()
	bus := events.NewBus()
	var mu sync.Mutex
	var received []hmevent.SysvarChangedEvent
	events.Subscribe(bus, func(e hmevent.SysvarChangedEvent) {
		mu.Lock()
		received = append(received, e)
		mu.Unlock()
	})
	h := NewHubCoordinator("c2", bus)

	h.UpdateSysvar(context.Background(), SysvarSnapshot{
		Name:      "Counter",
		Value:     hmtypes.IntValue(1),
		ValueType: hmenum.HubValueTypeInteger,
	})
	h.UpdateSysvar(context.Background(), SysvarSnapshot{
		Name:      "Counter",
		Value:     hmtypes.IntValue(2),
		ValueType: hmenum.HubValueTypeInteger,
	})

	mu.Lock()
	n := len(received)
	mu.Unlock()
	if n != 2 {
		t.Fatalf("want 2 events (value 1 then 2), got %d", n)
	}

	mu.Lock()
	first, second := received[0], received[1]
	mu.Unlock()

	// First event: brand-new key → OldValue is the zero value.
	if !first.NewValue.Equal(hmtypes.IntValue(1)) {
		t.Errorf("first event NewValue=%v, want 1", first.NewValue)
	}
	if !first.OldValue.Equal(hmtypes.ParamValue{}) {
		t.Errorf("first event OldValue=%v, want zero value", first.OldValue)
	}

	// Second event: 1 → 2.
	if !second.OldValue.Equal(hmtypes.IntValue(1)) {
		t.Errorf("second event OldValue=%v, want 1", second.OldValue)
	}
	if !second.NewValue.Equal(hmtypes.IntValue(2)) {
		t.Errorf("second event NewValue=%v, want 2", second.NewValue)
	}
}

// TestHubSysvarsReturnsSortedSnapshot verifies that Sysvars() returns all
// registered entries. Note: the current implementation iterates a map so
// the order is not guaranteed; the test sorts before comparing to assert
// completeness rather than ordering.
func TestHubSysvarsReturnsSortedSnapshot(t *testing.T) {
	t.Parallel()
	bus := events.NewBus()
	h := NewHubCoordinator("c3", bus)

	for _, name := range []string{"z", "a", "m"} {
		h.UpdateSysvar(context.Background(), SysvarSnapshot{
			Name:      name,
			Value:     hmtypes.StringValue(name),
			ValueType: hmenum.HubValueTypeString,
		})
	}

	snaps := h.Sysvars()
	if len(snaps) != 3 {
		t.Fatalf("want 3 sysvars, got %d", len(snaps))
	}

	names := make([]string, len(snaps))
	for i, s := range snaps {
		names[i] = s.Name
	}
	sort.Strings(names)

	want := []string{"a", "m", "z"}
	for i, w := range want {
		if names[i] != w {
			t.Errorf("sorted[%d]=%q, want %q", i, names[i], w)
		}
	}
}

// TestHubNotifyProgramExecutedEmits verifies that NotifyProgramExecuted
// publishes a ProgramExecutedEvent with the correct payload fields.
func TestHubNotifyProgramExecutedEmits(t *testing.T) {
	t.Parallel()
	bus := events.NewBus()
	var mu sync.Mutex
	var got []hmevent.ProgramExecutedEvent
	events.Subscribe(bus, func(e hmevent.ProgramExecutedEvent) {
		mu.Lock()
		got = append(got, e)
		mu.Unlock()
	})
	h := NewHubCoordinator("c4", bus)

	h.NotifyProgramExecuted(context.Background(), "prog-007", hmenum.ProgramTriggerUser, true)

	mu.Lock()
	n := len(got)
	mu.Unlock()
	if n != 1 {
		t.Fatalf("expected 1 ProgramExecutedEvent, got %d", n)
	}

	mu.Lock()
	e := got[0]
	mu.Unlock()
	if e.CentralName != "c4" {
		t.Errorf("CentralName=%q, want %q", e.CentralName, "c4")
	}
	if e.ProgramID != "prog-007" {
		t.Errorf("ProgramID=%q, want %q", e.ProgramID, "prog-007")
	}
	if e.Trigger != hmenum.ProgramTriggerUser {
		t.Errorf("Trigger=%v, want %v", e.Trigger, hmenum.ProgramTriggerUser)
	}
	if !e.Success {
		t.Errorf("Success=false, want true")
	}
	if e.Source != "" {
		t.Errorf("Source=%q, want empty for an unstamped context", e.Source)
	}
}

// TestHubNotifyProgramExecutedLiftsRequestOperation verifies the event's
// Source is taken from the request-context Operation the ingress stamped —
// the channel that lets the audit trail name the surface a run came from.
func TestHubNotifyProgramExecutedLiftsRequestOperation(t *testing.T) {
	t.Parallel()
	bus := events.NewBus()
	var mu sync.Mutex
	var got []hmevent.ProgramExecutedEvent
	events.Subscribe(bus, func(e hmevent.ProgramExecutedEvent) {
		mu.Lock()
		got = append(got, e)
		mu.Unlock()
	})
	h := NewHubCoordinator("c4", bus)

	ctx := reqctx.WithOperation(context.Background(), "mqtt:program-trigger")
	h.NotifyProgramExecuted(ctx, "prog-007", hmenum.ProgramTriggerAPI, true)

	mu.Lock()
	defer mu.Unlock()
	if len(got) != 1 {
		t.Fatalf("expected 1 ProgramExecutedEvent, got %d", len(got))
	}
	if got[0].Source != "mqtt:program-trigger" {
		t.Errorf("Source=%q, want %q", got[0].Source, "mqtt:program-trigger")
	}
}

// TestHubSetRefreshHooksOverwritePrior verifies that calling SetRefreshHooks
// twice causes only the second hook to be active; the first hook must not
// fire after the second registration.
func TestHubSetRefreshHooksOverwritePrior(t *testing.T) {
	t.Parallel()
	bus := events.NewBus()
	h := NewHubCoordinator("c5", bus)

	var firstCalls, secondCalls atomic.Int32

	h.SetRefreshHooks(RefreshHooks{
		Sysvars: func(_ context.Context) error {
			firstCalls.Add(1)
			return nil
		},
	})
	// Overwrite with a second hook.
	h.SetRefreshHooks(RefreshHooks{
		Sysvars: func(_ context.Context) error {
			secondCalls.Add(1)
			return nil
		},
	})

	if err := h.RefreshSysvars(context.Background()); err != nil {
		t.Fatalf("RefreshSysvars error: %v", err)
	}

	if firstCalls.Load() != 0 {
		t.Errorf("first hook must not fire after being overwritten, got %d calls", firstCalls.Load())
	}
	if secondCalls.Load() != 1 {
		t.Errorf("second hook must fire exactly once, got %d calls", secondCalls.Load())
	}
}

// TestHubRefreshHooksNilSafe verifies that calling every Refresh* method
// without ever calling SetRefreshHooks returns nil and does not panic.
func TestHubRefreshHooksNilSafe(t *testing.T) {
	t.Parallel()
	bus := events.NewBus()
	h := NewHubCoordinator("c6", bus)
	ctx := context.Background()

	if err := h.RefreshPrograms(ctx); err != nil {
		t.Errorf("RefreshPrograms: %v", err)
	}
	if err := h.RefreshSysvars(ctx); err != nil {
		t.Errorf("RefreshSysvars: %v", err)
	}
	if err := h.RefreshInbox(ctx); err != nil {
		t.Errorf("RefreshInbox: %v", err)
	}
	if err := h.RefreshServiceMessages(ctx); err != nil {
		t.Errorf("RefreshServiceMessages: %v", err)
	}
	if err := h.RefreshAlarmMessages(ctx); err != nil {
		t.Errorf("RefreshAlarmMessages: %v", err)
	}
	if err := h.RefreshSystemUpdate(ctx); err != nil {
		t.Errorf("RefreshSystemUpdate: %v", err)
	}
}

// TestHubRefreshSurfacesHookError verifies that when the Sysvars hook
// returns an error, RefreshSysvars surfaces it.
func TestHubRefreshSurfacesHookError(t *testing.T) {
	t.Parallel()
	bus := events.NewBus()
	h := NewHubCoordinator("c7", bus)
	boom := errors.New("boom")
	h.SetRefreshHooks(RefreshHooks{
		Sysvars: func(_ context.Context) error { return boom },
	})

	err := h.RefreshSysvars(context.Background())
	if err == nil {
		t.Fatal("expected an error from RefreshSysvars, got nil")
	}
	if !errors.Is(err, boom) {
		t.Errorf("error=%v, want errors.Is(err, boom)", err)
	}
}

// TestHubSysvarsCopyIsIndependent verifies that mutating the slice returned
// by Sysvars() does not affect subsequent calls to Sysvars().
func TestHubSysvarsCopyIsIndependent(t *testing.T) {
	t.Parallel()
	bus := events.NewBus()
	h := NewHubCoordinator("c8", bus)

	for _, name := range []string{"x", "y", "z"} {
		h.UpdateSysvar(context.Background(), SysvarSnapshot{
			Name:      name,
			Value:     hmtypes.IntValue(1),
			ValueType: hmenum.HubValueTypeInteger,
		})
	}

	first := h.Sysvars()
	if len(first) != 3 {
		t.Fatalf("want 3 sysvars, got %d", len(first))
	}

	// Mutate the returned slice — zero out its entries.
	for i := range first {
		first[i] = SysvarSnapshot{}
	}

	// A fresh call must still return all three original entries.
	second := h.Sysvars()
	if len(second) != 3 {
		t.Fatalf("after slice mutation, want 3 sysvars, got %d", len(second))
	}
	for _, s := range second {
		if s.Name == "" {
			t.Errorf("got zeroed entry in second Sysvars() call: %+v", s)
		}
	}
}

// TestHubConcurrentUpdateSysvarRaceFree exercises UpdateSysvar from 64
// concurrent goroutines — each using a distinct name — and then asserts
// that len(Sysvars()) matches the expected unique-name count. Run with
// -race to detect data-race violations.
func TestHubConcurrentUpdateSysvarRaceFree(t *testing.T) {
	t.Parallel()
	bus := events.NewBus()
	h := NewHubCoordinator("c9", bus)

	const goroutines = 64
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := range goroutines {
		go func(idx int) {
			defer wg.Done()
			// Each goroutine writes a distinct name keyed by its index so
			// that the total unique-name count equals goroutines.
			uniqueName := string(rune('A'+idx%26)) + "_" + intToStr(idx)
			h.UpdateSysvar(context.Background(), SysvarSnapshot{
				Name:      uniqueName,
				Value:     hmtypes.IntValue(idx),
				ValueType: hmenum.HubValueTypeInteger,
			})
		}(i)
	}
	wg.Wait()

	snaps := h.Sysvars()
	if len(snaps) != goroutines {
		t.Fatalf("want %d sysvars after concurrent writes, got %d", goroutines, len(snaps))
	}
}

// intToStr converts a non-negative int to its decimal string without
// importing strconv (keeps import set lean for this file).
func intToStr(n int) string {
	if n == 0 {
		return "0"
	}
	buf := [20]byte{}
	pos := len(buf)
	for n > 0 {
		pos--
		buf[pos] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[pos:])
}
