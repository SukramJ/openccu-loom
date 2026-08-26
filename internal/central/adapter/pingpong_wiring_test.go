// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

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
	"github.com/SukramJ/openccu-loom/internal/clock"
	"github.com/SukramJ/openccu-loom/internal/metrics"
	hubmodel "github.com/SukramJ/openccu-loom/internal/model/hub"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
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
			hmtypes.StringValue(callerID),
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

// TestWirePingPongBusRejectsForeignDaemonPONG is the regression guard for the
// two-daemons-one-CCU bug: when a second OpenCCU-Loom runs against the same CCU,
// the CCU broadcasts ITS PONGs onto our interface too. Both daemons speak the
// same interface TYPE (HmIP-RF), so a correlation keyed on the bare interface
// name could not tell the foreign daemon's PONG from ours — it matched no
// pending ping and piled up as an "unknown" mismatch, decaying interface health
// until /health flipped to 503. Keying the caller_id (and the match) on the full
// wire-boundary triple `<instance>-<central>-<interface>` makes the two daemons
// distinguishable: our own PONG closes pending, the foreign one is ignored.
func TestWirePingPongBusRejectsForeignDaemonPONG(t *testing.T) {
	t.Parallel()

	const (
		centralName = "OttoLoom"
		// ifID is the canonical (instance-stripped) interface id the inbound
		// callback handler hands to the event coordinator.
		ifID = "OttoLoom-HmIP-RF"
		// ourWireID is this daemon's wire-boundary triple — the caller_id base.
		ourWireID = "Otto-OttoLoom-HmIP-RF"
		// foreignWireID is a co-located daemon's triple: same central + interface
		// type, different instance name. Its PONGs reach our interface via the
		// CCU broadcast but must NOT correlate.
		foreignWireID = "OtherLoom-OttoLoom-HmIP-RF"
	)

	c := newTestCentralNamed(t, centralName)
	ic, err := clientpkg.New(clientpkg.Config{
		CentralName:     centralName,
		Interface:       hmenum.InterfaceHmIPRF,
		InitInterfaceID: ourWireID,
		Caller: clientpkg.CallerFunc(func(_ context.Context, _ string, _ []any) (any, error) {
			return nil, nil
		}),
		PingPong: reliability.NewPingPongTracker(reliability.PingPongConfig{
			MismatchThreshold: 5,
			PendingTTL:        30 * time.Second,
			UnknownTTL:        30 * time.Second,
		}),
	})
	if err != nil {
		t.Fatalf("clientpkg.New: %v", err)
	}
	if err := c.Clients.Register(&coordinators.ClientEntry{
		InterfaceID: ifID,
		Interface:   hmenum.InterfaceHmIPRF,
		Client:      ic,
	}); err != nil {
		t.Fatalf("register client: %v", err)
	}
	WirePingPongBus(c, ic, ifID, nil)

	deliver := func(callerID string) {
		c.Events.HandleRawEventNormalized(
			context.Background(), ifID, "CENTRAL", "PONG",
			hmtypes.StringValue(callerID),
		)
	}

	// Our own tracking ping → recorded, then matched by its PONG (echoed with
	// our full triple).
	ic.RecordPing("42")
	deliver(ourWireID + "#42")
	if got := ic.PingPong().PendingCount(); got != 0 {
		t.Fatalf("own PONG must close pending: pending=%d, want 0", got)
	}

	// A co-located daemon's PONGs (same interface type, different instance)
	// broadcast onto our interface → must be ignored, not filed as unknown.
	deliver(foreignWireID + "#7")
	deliver(foreignWireID + "#8")
	if got := ic.PingPong().UnknownCount(); got != 0 {
		t.Fatalf("foreign daemon's PONGs must not be recorded as unknown: "+
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

// TestWirePingPongBusFeedsLatencyFromMatchedPong pins the producer of both
// connection-latency surfaces to the one measurement that actually covers the
// path they are named after: a matched PING→PONG pair. The PING leaves over
// the interface's own transport and the PONG returns on the callback server,
// so the sample includes the reply leg — which the JSON-RPC probe that used to
// feed the hub metric never touched.
//
// Both assertions carry their negative control: nothing is observed before the
// PONG arrives, and an unmatched PONG (one whose token was never pinged) leaves
// both surfaces exactly as they were. Without those, a test that only checks
// "a value is present afterwards" would pass against a producer that files a
// sample for every frame it sees, matched or not.
//
// The tracker runs on a fake clock so the asserted round-trip is the injected
// interval rather than however long the test machine took between two lines.
func TestWirePingPongBusFeedsLatencyFromMatchedPong(t *testing.T) {
	t.Parallel()

	const (
		centralName = "ccu-01"
		ifaceID     = "ccu-01-HmIP-RF"
		rtt         = 250 * time.Millisecond
	)

	fake := clock.NewFake(time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC))
	c := newTestCentralNamed(t, centralName)
	obs := metrics.NewObserver()
	c.SetAggregator(metrics.NewAggregator(centralName, obs))

	ic, err := clientpkg.New(clientpkg.Config{
		CentralName: centralName,
		Interface:   hmenum.InterfaceHmIPRF,
		Caller: clientpkg.CallerFunc(func(_ context.Context, _ string, _ []any) (any, error) {
			return nil, nil
		}),
		PingPong: reliability.NewPingPongTracker(reliability.PingPongConfig{
			MismatchThreshold: 5,
			PendingTTL:        30 * time.Second,
			UnknownTTL:        30 * time.Second,
			Clock:             fake,
		}),
	})
	if err != nil {
		t.Fatalf("clientpkg.New: %v", err)
	}
	if err := c.Clients.Register(&coordinators.ClientEntry{
		InterfaceID: ifaceID,
		Interface:   hmenum.InterfaceHmIPRF,
		Client:      ic,
	}); err != nil {
		t.Fatalf("register client: %v", err)
	}
	WirePingPongBus(c, ic, ifaceID, nil)

	deliver := func(callerID string) {
		c.Events.HandleRawEventNormalized(
			context.Background(), ifaceID, "CENTRAL", "PONG",
			hmtypes.StringValue(callerID),
		)
	}
	prefix := ic.WireBoundaryID()

	// Negative control 1 — nothing measured before a round-trip completes.
	if _, ok := c.HubModel.Metrics.Value(hubmodel.MetricConnectionLatMs); ok {
		t.Fatal("pre-condition: the hub latency metric must be unobserved before the first PONG")
	}
	if got := c.Aggregator.RPC().AvgLatencyMs; got != 0 {
		t.Fatalf("pre-condition: rpc avg_latency_ms = %v, want 0 before the first PONG", got)
	}

	// Negative control 2 — a PONG that matches no outstanding PING is not a
	// round-trip and must leave both surfaces untouched.
	deliver(prefix + "#no-such-ping")
	if _, ok := c.HubModel.Metrics.Value(hubmodel.MetricConnectionLatMs); ok {
		t.Error("an unmatched PONG produced a latency sample — the producer is not gated on a matched pair")
	}
	if got := c.Aggregator.RPC().AvgLatencyMs; got != 0 {
		t.Errorf("an unmatched PONG produced rpc avg_latency_ms = %v, want 0", got)
	}

	// The real thing: ping, let the clock advance by a known interval, pong.
	ic.RecordPing("7")
	fake.Advance(rtt)
	deliver(prefix + "#7")

	want := float64(rtt.Nanoseconds()) / float64(time.Millisecond)
	sample, ok := c.HubModel.Metrics.Value(hubmodel.MetricConnectionLatMs)
	if !ok {
		t.Fatal("a matched PONG left the hub latency metric unobserved — the declared sensor has no producer")
	}
	if sample.Value != want {
		t.Errorf("hub connection latency = %v ms, want %v ms (the injected round-trip)", sample.Value, want)
	}
	if got := c.Aggregator.RPC().AvgLatencyMs; got != want {
		t.Errorf("rpc avg_latency_ms = %v, want %v — the ping_pong.rtt key has no producer", got, want)
	}
}
