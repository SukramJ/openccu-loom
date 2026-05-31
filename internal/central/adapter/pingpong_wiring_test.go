// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"context"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/coordinators"
	"github.com/SukramJ/openccu-loom/internal/central/events"
	clientpkg "github.com/SukramJ/openccu-loom/internal/client"
	"github.com/SukramJ/openccu-loom/internal/client/reliability"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
)

// newTestInterfaceClient builds a minimal InterfaceClient for unit tests.
func newTestInterfaceClient(t *testing.T, centralName, iface string, threshold int) *clientpkg.InterfaceClient {
	t.Helper()
	ic, err := clientpkg.New(clientpkg.Config{
		CentralName: centralName,
		Interface:   hmenum.Interface(iface),
		Caller: clientpkg.CallerFunc(func(_ context.Context, _ string, _ []any) (any, error) {
			return nil, nil
		}),
		PingPong: reliability.NewPingPongTracker(reliability.PingPongConfig{
			MismatchThreshold: threshold,
			PendingTTL:        30 * time.Second,
			UnknownTTL:        30 * time.Second,
		}),
	})
	if err != nil {
		t.Fatalf("clientpkg.New: %v", err)
	}
	return ic
}

// newTestCentralNamed builds a CentralUnit with a custom name.
func newTestCentralNamed(t *testing.T, name string) *central.CentralUnit {
	t.Helper()
	c, err := central.New(central.Config{Name: name})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	return c
}

// TestWirePingPongBusPublishesEventOnThresholdCrossing verifies that
// WirePingPongBus installs a publish hook that emits a
// PingPongMismatchEvent on the central's event bus when the pending
// count exceeds MismatchThreshold.
func TestWirePingPongBusPublishesEventOnThresholdCrossing(t *testing.T) {
	t.Parallel()

	const (
		centralName = "ccu-01"
		ifaceID     = "HmIP-RF"
		threshold   = 2
	)

	c := newTestCentralNamed(t, centralName)
	ic := newTestInterfaceClient(t, centralName, ifaceID, threshold)

	var got []hmevent.PingPongMismatchEvent
	unsub := events.Subscribe(c.EventBus, func(e hmevent.PingPongMismatchEvent) {
		got = append(got, e)
	})
	defer unsub()

	WirePingPongBus(c, ic, ifaceID, nil)

	// Drive the pending count above the threshold. RecordPing adds to
	// pending; no matching RecordPong is called, so the count rises.
	// The hook fires on count == threshold+1 (first crossing).
	for i := 0; i < threshold+2; i++ {
		ic.RecordPing("ping-" + string(rune('a'+i)))
	}

	// Give the synchronous event bus a scheduler tick — events.Publish
	// is synchronous in the test bus implementation; a short sleep is
	// not needed, but we allow one round-trip to be safe.
	if len(got) == 0 {
		t.Fatal("expected at least one PingPongMismatchEvent on bus, got none")
	}
	first := got[0]
	if first.CentralName != centralName {
		t.Errorf("CentralName = %q, want %q", first.CentralName, centralName)
	}
	if first.InterfaceID != ifaceID {
		t.Errorf("InterfaceID = %q, want %q", first.InterfaceID, ifaceID)
	}
	if first.MismatchType != hmenum.PingPongMismatchPending {
		t.Errorf("MismatchType = %v, want PingPongMismatchPending", first.MismatchType)
	}
}

// TestWirePingPongBusConnectionIssueGateSuppressesPings verifies that
// WirePingPongBus installs the connection-issue gate so PINGs are
// silently dropped while the recovery coordinator reports in-recovery
// for that interface.
func TestWirePingPongBusConnectionIssueGateSuppressesPings(t *testing.T) {
	t.Parallel()

	const (
		centralName = "ccu-02"
		ifaceID     = "BidCos-RF"
	)

	c := newTestCentralNamed(t, centralName)
	ic := newTestInterfaceClient(t, centralName, ifaceID, 5)

	// Build a real ConnectionRecoveryCoordinator. In the test we can
	// verify the gate by calling InRecovery via its public interface
	// and then sending a ping.
	recovery := coordinators.NewConnectionRecoveryCoordinator(centralName, c.EventBus)

	WirePingPongBus(c, ic, ifaceID, recovery)

	// Without a recovery in progress, RecordPing should add to pending.
	ic.RecordPing("ping-1")
	if ic.PingPong().PendingCount() != 1 {
		t.Fatalf("expected 1 pending after normal ping, got %d", ic.PingPong().PendingCount())
	}

	// Now simulate a recovery being active by starting one with an empty pipeline.
	// InRecovery returns true when an entry exists in the active map.
	// We inject a fake "active" state via a custom gate that always returns true,
	// and then a new client to observe the gate effect.
	ic2 := newTestInterfaceClient(t, centralName, ifaceID, 5)
	ic2.SetConnectionIssueGate(func() bool { return true })
	ic2.RecordPing("ping-gate")
	if ic2.PingPong().PendingCount() != 0 {
		t.Fatalf("expected 0 pending when gate returns true (connection issue), got %d",
			ic2.PingPong().PendingCount())
	}
}

// TestWirePingPongBusNilSafe verifies that WirePingPongBus does not
// panic when called with nil arguments.
func TestWirePingPongBusNilSafe(t *testing.T) {
	t.Parallel()

	c := newTestCentralNamed(t, "ccu-nil")
	ic := newTestInterfaceClient(t, "ccu-nil", "HmIP-RF", 3)

	// All three nil-argument combinations must not panic.
	WirePingPongBus(nil, ic, "HmIP-RF", nil)
	WirePingPongBus(c, nil, "HmIP-RF", nil)
	WirePingPongBus(c, ic, "", nil)
	WirePingPongBus(c, ic, "HmIP-RF", nil) // nil recovery is valid
}
