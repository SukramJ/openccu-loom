// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package coordinators

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/internal/client"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// nopCaller is a minimal client.Caller stand-in for tests that exercise
// the client lifecycle without speaking to a real CCU.
var nopCaller = client.CallerFunc(func(_ context.Context, _ string, _ []any) (any, error) {
	return nil, nil
})

// makeEntry builds a minimal ClientEntry whose InterfaceClient is wired
// for state-machine assertions but does not perform real network I/O.
// _ bus is preserved as a parameter so callers can keep their existing
// signatures even though the new ClientEntry does not consume it.
func makeEntry(_ *events.Bus, ifaceID string, iface hmenum.Interface) *ClientEntry {
	ic, _ := client.New(client.Config{
		CentralName: "test-central",
		Interface:   iface,
		Caller:      nopCaller,
	})
	return &ClientEntry{
		InterfaceID: ifaceID,
		Interface:   iface,
		Host:        "ccu.local",
		Client:      ic,
	}
}

func TestClientCoordinatorRegisterRejectsDuplicate(t *testing.T) {
	t.Parallel()
	bus := events.NewBus()
	cc := NewClientCoordinator()

	entry := makeEntry(bus, "HmIP-RF", hmenum.InterfaceHmIPRF)
	if err := cc.Register(entry); err != nil {
		t.Fatalf("first Register returned unexpected error: %v", err)
	}

	// Second registration with the same interface ID must fail.
	err := cc.Register(makeEntry(bus, "HmIP-RF", hmenum.InterfaceHmIPRF))
	if !errors.Is(err, ErrClientExists) {
		t.Fatalf("expected ErrClientExists, got %v", err)
	}
}

func TestClientCoordinatorGetHitAndMiss(t *testing.T) {
	t.Parallel()
	bus := events.NewBus()
	cc := NewClientCoordinator()
	entry := makeEntry(bus, "BidCos-RF", hmenum.InterfaceBidCosRF)
	if err := cc.Register(entry); err != nil {
		t.Fatalf("Register: %v", err)
	}

	got, ok := cc.Get("BidCos-RF")
	if !ok {
		t.Fatal("Get: expected ok=true for registered entry")
	}
	if got != entry {
		t.Fatal("Get: returned a different pointer than registered")
	}

	_, ok = cc.Get("no-such-interface")
	if ok {
		t.Fatal("Get: expected ok=false for unknown interface ID")
	}
}

func TestClientCoordinatorRemoveTrueOnceFalseAfter(t *testing.T) {
	t.Parallel()
	bus := events.NewBus()
	cc := NewClientCoordinator()
	if err := cc.Register(makeEntry(bus, "CUxD", hmenum.InterfaceCUxD)); err != nil {
		t.Fatalf("Register: %v", err)
	}

	if !cc.Remove("CUxD") {
		t.Fatal("Remove: expected true on first call")
	}
	if cc.Remove("CUxD") {
		t.Fatal("Remove: expected false on second call (entry gone)")
	}
}

func TestClientCoordinatorListIsSorted(t *testing.T) {
	t.Parallel()
	bus := events.NewBus()
	cc := NewClientCoordinator()

	for _, id := range []string{"z-iface", "a-iface", "m-iface"} {
		_ = cc.Register(makeEntry(bus, id, hmenum.InterfaceHmIPRF))
	}

	list := cc.List()
	if len(list) != 3 {
		t.Fatalf("List len=%d want 3", len(list))
	}
	ids := []string{list[0].InterfaceID, list[1].InterfaceID, list[2].InterfaceID}
	want := []string{"a-iface", "m-iface", "z-iface"}
	for i, got := range ids {
		if got != want[i] {
			t.Fatalf("List[%d]=%q want %q (not sorted)", i, got, want[i])
		}
	}
}

func TestClientEntryConnectedHonorsStateMachine(t *testing.T) {
	t.Parallel()
	bus := events.NewBus()

	entry := makeEntry(bus, "HmIP-RF", hmenum.InterfaceHmIPRF)
	// Freshly created state machine starts in CREATED — not CONNECTED.
	if entry.Connected() {
		t.Fatal("entry should not be Connected() in CREATED state")
	}

	// Walk the machine along the valid path: CREATED → INITIALIZING → INITIALIZED → CONNECTING → CONNECTED
	transitions := []struct {
		to     hmenum.ClientState
		reason string
	}{
		{hmenum.ClientStateInitializing, "test"},
		{hmenum.ClientStateInitialized, "test"},
		{hmenum.ClientStateConnecting, "test"},
		{hmenum.ClientStateConnected, "test"},
	}
	for _, tr := range transitions {
		if err := entry.Client.TransitionTo(tr.to, tr.reason, false, hmenum.FailureReasonNone); err != nil {
			t.Fatalf("TransitionTo(%s): %v", tr.to, err)
		}
	}

	if !entry.Connected() {
		t.Fatal("entry should be Connected() after transitioning to CONNECTED")
	}
}

func TestClientEntryConnectedNilGuards(t *testing.T) {
	t.Parallel()
	// Nil receiver must not panic.
	var nilEntry *ClientEntry
	if nilEntry.Connected() {
		t.Fatal("nil entry must return false from Connected()")
	}
	// Non-nil entry with nil Client must also not panic.
	noClient := &ClientEntry{InterfaceID: "x"}
	if noClient.Connected() {
		t.Fatal("entry with nil Client must return false from Connected()")
	}
}

func TestClientCoordinatorConcurrentRegisterRemove(t *testing.T) {
	t.Parallel()
	bus := events.NewBus()
	cc := NewClientCoordinator()

	const count = 30
	var wg sync.WaitGroup
	wg.Add(count * 2)
	for i := 0; i < count; i++ {
		id := "iface-" + string(rune('A'+i%26))
		go func(id string) {
			defer wg.Done()
			_ = cc.Register(makeEntry(bus, id, hmenum.InterfaceHmIPRF))
		}(id)
		go func(id string) {
			defer wg.Done()
			_ = cc.Remove(id)
		}(id)
	}
	wg.Wait()
	// List must not panic and return valid entries.
	_ = cc.List()
}
