// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package contract

// callback_liveness_contract_test.go is the behavioural end-to-end guard for
// the keepalive watchdog. It drives the production inbound-callback handler
// (CallbackHandlers.Event) and asserts the two invariants whose absence caused
// an endless ~180 s reconnect loop on quiet CCUs:
//
//  1. Every inbound callback refreshes the per-client callback-liveness
//     timestamp (IsCallbackAlive), not only a reconnect. Otherwise the
//     timestamp goes stale callbackFreshness (180 s) after each reconnect and
//     check_connection declares the channel dead.
//  2. A PONG callback — which the CCU delivers on the non-device "CENTRAL"
//     pseudo-address — reaches the ping-pong tracker and closes the pending
//     round-trip. Otherwise pending PINGs pile up to the per-interface cap and
//     health stays permanently degraded (and /health returns 503).
//
// These are behavioural, not static: the wiring-pin guards in
// tests/contract/wiring_pins assert the tracker hooks exist, yet the original
// bug sat upstream — Event dropped the PONG (and never stamped liveness)
// before either hook could run — so only an end-to-end test catches it.

import (
	"context"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/adapter"
	"github.com/SukramJ/openccu-loom/internal/central/coordinators"
	"github.com/SukramJ/openccu-loom/internal/client"
	"github.com/SukramJ/openccu-loom/internal/client/backends"
	"github.com/SukramJ/openccu-loom/internal/client/reliability"
	"github.com/SukramJ/openccu-loom/internal/client/transport/xmlrpc"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// newKeepaliveFixture builds a central with one CONNECTED, ping-pong-capable
// InterfaceClient registered and the ping-pong bus wired, plus the production
// callback handler. This is the minimal slice of the inbound-callback path.
func newKeepaliveFixture(t *testing.T) (*adapter.CallbackHandlers, *client.InterfaceClient) {
	t.Helper()
	const (
		centralName = "ccu-keepalive"
		ifaceID     = "HmIP-RF"
	)
	c, err := central.New(central.Config{Name: centralName})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	ic, err := client.New(client.Config{
		CentralName: centralName,
		Interface:   hmenum.Interface(ifaceID),
		Caller: client.CallerFunc(func(_ context.Context, _ string, _ []any) (any, error) {
			return nil, nil
		}),
		Capabilities: backends.Capabilities{PingPong: true},
		PingPong: reliability.NewPingPongTracker(reliability.PingPongConfig{
			MismatchThreshold: 15,
			PendingTTL:        30 * time.Second,
			UnknownTTL:        30 * time.Second,
		}),
	})
	if err != nil {
		t.Fatalf("client.New: %v", err)
	}
	// CONNECTED + ping-pong capability is the regime where IsCallbackAlive
	// actually consults the liveness timestamp (other regimes short-circuit).
	ic.SetState(hmenum.ClientStateConnected)
	if err := c.Clients.Register(&coordinators.ClientEntry{
		InterfaceID: ifaceID,
		Interface:   hmenum.InterfaceHmIPRF,
		Client:      ic,
	}); err != nil {
		t.Fatalf("register client: %v", err)
	}
	adapter.WirePingPongBus(c, ic, ifaceID, nil)
	return adapter.NewCallbackHandlers(c, nil), ic
}

// TestKeepaliveContract_PongClosesPendingAndRefreshesLiveness pins the full
// keepalive round-trip: a recorded outbound PING (as the check_connection job
// records it) is closed by a PONG delivered through the production callback
// handler, and that PONG also marks the channel alive. A regression in either
// the PONG routing or the liveness stamp reintroduces the reconnect loop.
func TestKeepaliveContract_PongClosesPendingAndRefreshesLiveness(t *testing.T) {
	t.Parallel()

	h, ic := newKeepaliveFixture(t)

	// The keepalive records caller_id "<interface>#<token>" before the ping
	// RPC; the tracker keys the pending entry on the token.
	ic.RecordPing("7")
	if got := ic.PingPong().PendingCount(); got != 1 {
		t.Fatalf("precondition: PendingCount=%d, want 1", got)
	}

	// The CCU echoes the caller_id back as a PONG event on the non-device
	// "CENTRAL" pseudo-address.
	if err := h.Event(context.Background(), "HmIP-RF", "CENTRAL", "PONG",
		xmlrpc.StringValue("HmIP-RF#7")); err != nil {
		t.Fatalf("Event(PONG): %v", err)
	}

	if got := ic.PingPong().PendingCount(); got != 0 {
		t.Fatalf("PONG must close the pending ping through the callback handler: "+
			"PendingCount=%d, want 0", got)
	}
	if ic.LastCallbackAt().IsZero() {
		t.Fatal("PONG callback must refresh the callback-liveness timestamp")
	}
	if !ic.IsCallbackAlive() {
		t.Fatal("channel must read alive immediately after a PONG round-trip")
	}
}

// TestKeepaliveContract_AnyEventRefreshesLiveness pins that an ordinary
// (non-PONG) inbound callback also refreshes liveness — even for a device the
// daemon does not mirror. This is what keeps a busy CCU alive between
// keepalive ticks, and it must run before Event's device-existence guard.
func TestKeepaliveContract_AnyEventRefreshesLiveness(t *testing.T) {
	t.Parallel()

	h, ic := newKeepaliveFixture(t)
	if !ic.LastCallbackAt().IsZero() {
		t.Fatal("precondition: liveness timestamp must start zero")
	}

	// An event for an unmirrored device still proves the callback channel is
	// alive; the device-existence guard must not swallow that signal.
	before := time.Now()
	if err := h.Event(context.Background(), "HmIP-RF", "UNMIRRORED:1", "STATE",
		xmlrpc.BoolValue(true)); err != nil {
		t.Fatalf("Event: %v", err)
	}

	last := ic.LastCallbackAt()
	if last.IsZero() || last.Before(before) {
		t.Fatalf("inbound event must refresh liveness before the device guard: "+
			"LastCallbackAt=%v, event time=%v", last, before)
	}
}
