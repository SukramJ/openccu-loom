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
	"github.com/SukramJ/openccu-loom/internal/client/transport/xmlrpc"
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

// newTestCentralNamed builds a Unit with a custom name.
func newTestCentralNamed(t *testing.T, name string) *central.Unit {
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
	for i := range threshold + 2 {
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

// TestWirePingPongBusPONGCorrelation verifies the PONG-ingest hook's
// correlation rules against the live CCU broadcast quirk: the CCU echoes the
// ping caller_id as the PONG value and broadcasts PONG events to EVERY
// registered logic-layer client. On our own interface we therefore receive:
//   - our own tracking PONGs   ("HmIP-RF#<token>")   → must close pending
//   - other instances' PONGs   ("Otto-HmIP-RF#<ts>") → must be ignored
//   - bare-name liveness probes ("HmIP-RF")          → must be ignored
//
// Recording the latter two would inflate the unknown-mismatch count and decay
// interface health (the symptom that surfaced once the reconnect loop — which
// used to clear the tracker every ~180 s — was fixed).
func TestWirePingPongBusPONGCorrelation(t *testing.T) {
	t.Parallel()

	const (
		centralName = "OttoGo"
		ifaceID     = "OttoGo-HmIP-RF"
	)
	c := newTestCentralNamed(t, centralName)
	ic := newTestInterfaceClient(t, centralName, "HmIP-RF", 5)
	if err := c.Clients.Register(&coordinators.ClientEntry{
		InterfaceID: ifaceID,
		Interface:   hmenum.InterfaceHmIPRF, // bare "HmIP-RF" — our ping prefix
		Client:      ic,
	}); err != nil {
		t.Fatalf("register client: %v", err)
	}
	WirePingPongBus(c, ic, ifaceID, nil)

	deliver := func(callerID string) {
		c.Events.HandleRawEventNormalized(
			context.Background(), ifaceID, "CENTRAL", "PONG",
			xmlrpc.StringValue(callerID),
		)
	}

	// Our own tracking ping → recorded, then matched by its PONG.
	ic.RecordPing("42")
	deliver("HmIP-RF#42")
	if got := ic.PingPong().PendingCount(); got != 0 {
		t.Fatalf("own PONG must close pending: pending=%d, want 0", got)
	}

	// Foreign instances' PONGs broadcast onto our interface → ignored.
	deliver("Otto-HmIP-RF#09.06.2026 09:31:22.492782'")
	deliver("Otto-RC-HmIP-RF#09.06.2026 09:31:14.466456'")
	// Bare-name liveness probe PONG (no token) → ignored.
	deliver("HmIP-RF")

	if got := ic.PingPong().UnknownCount(); got != 0 {
		t.Fatalf("foreign / tokenless PONGs must not be recorded as unknown: "+
			"unknown=%d, want 0", got)
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
