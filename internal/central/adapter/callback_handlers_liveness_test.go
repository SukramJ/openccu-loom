// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central/coordinators"
	"github.com/SukramJ/openccu-loom/internal/client"
	"github.com/SukramJ/openccu-loom/internal/client/backends"
	"github.com/SukramJ/openccu-loom/internal/client/reliability"
	"github.com/SukramJ/openccu-loom/internal/client/transport/xmlrpc"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// pingPongClient builds an InterfaceClient that advertises the ping-pong
// capability and sits in the CONNECTED state, so IsCallbackAlive reflects the
// last-callback timestamp rather than short-circuiting on capability/state.
func pingPongClient(t *testing.T, centralName, iface string) *client.InterfaceClient {
	t.Helper()
	ic, err := client.New(client.Config{
		CentralName: centralName,
		Interface:   hmenum.Interface(iface),
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
	ic.SetState(hmenum.ClientStateConnected)
	return ic
}

// TestEventStampsCallbackLiveness is the regression tripwire for the
// reconnect-every-180s loop. CallbackHandlers.Event updated the model and
// forwarded to the EventCoordinator, but never refreshed the InterfaceClient's
// callback-liveness timestamp. That timestamp (read by IsCallbackAlive) was
// only ever stamped on a reconnect, so on a quiet CCU it went stale exactly
// callbackFreshness (180s) after each reconnect — the check_connection
// watchdog then declared the channel dead and triggered another reconnect, in
// an endless 180s cycle.
//
// Mirrors the reference event-coordinator data_point_event flow, which calls
// set_last_event_seen_for_interface for every inbound callback.
func TestEventStampsCallbackLiveness(t *testing.T) {
	t.Parallel()

	reg, dev := registryWithDevice(t)
	ch := dev.AddChannel("0001ABCD:1", 1, "TEST", hmenum.ParamsetKeyValues)
	dp := generic.NewDataPoint[bool](generic.Spec{
		Key: hmtypes.DataPointKey{
			InterfaceID:    "HmIP-RF",
			ChannelAddress: "0001ABCD:1",
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      "STATE",
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeBool,
			Operations: hmenum.OperationsRead | hmenum.OperationsEvent,
		},
	})
	ch.Put(dp)

	c := reg.List()[0]
	ic := pingPongClient(t, "ccu-01", "HmIP-RF")
	if err := c.Clients.Register(&coordinators.ClientEntry{
		InterfaceID: "HmIP-RF",
		Interface:   hmenum.InterfaceHmIPRF,
		Client:      ic,
	}); err != nil {
		t.Fatalf("register client: %v", err)
	}

	// Precondition: no callback observed yet.
	if !ic.LastCallbackAt().IsZero() {
		t.Fatalf("precondition: LastCallbackAt should be zero before any event")
	}

	h := NewCallbackHandlers(c, nil)
	before := time.Now()
	if err := h.Event(context.Background(), "HmIP-RF", "0001ABCD:1", "STATE", xmlrpc.BoolValue(true)); err != nil {
		t.Fatalf("Event: %v", err)
	}

	last := ic.LastCallbackAt()
	if last.IsZero() {
		t.Fatal("CallbackHandlers.Event must stamp the client's callback-liveness " +
			"timestamp (NotifyCallback) for every inbound event — otherwise " +
			"IsCallbackAlive goes stale 180s after each reconnect and the " +
			"check_connection watchdog reconnects in an endless loop")
	}
	if last.Before(before) {
		t.Fatalf("LastCallbackAt %v is older than the event time %v", last, before)
	}
	if !ic.IsCallbackAlive() {
		t.Fatal("IsCallbackAlive must be true immediately after an inbound event")
	}
}

// TestEventStampsCallbackLivenessForUnmirroredDevice verifies that an event
// for a device the daemon does not mirror still refreshes the liveness
// timestamp. The CCU emits callbacks (including PONG, see below) on
// pseudo/unmirrored addresses; if the device-existence guard short-circuits
// before stamping liveness, a quiet CCU whose only traffic is such callbacks
// would still be declared dead.
func TestEventStampsCallbackLivenessForUnmirroredDevice(t *testing.T) {
	t.Parallel()

	c := newTestCentralNamed(t, "ccu-01")
	ic := pingPongClient(t, "ccu-01", "HmIP-RF")
	if err := c.Clients.Register(&coordinators.ClientEntry{
		InterfaceID: "HmIP-RF",
		Interface:   hmenum.InterfaceHmIPRF,
		Client:      ic,
	}); err != nil {
		t.Fatalf("register client: %v", err)
	}

	h := NewCallbackHandlers(c, nil)
	if err := h.Event(context.Background(), "HmIP-RF", "UNKNOWN:1", "STATE", xmlrpc.BoolValue(true)); err != nil {
		t.Fatalf("Event for unmirrored device must not error: %v", err)
	}
	if ic.LastCallbackAt().IsZero() {
		t.Fatal("liveness must be stamped before the device-existence guard")
	}
}

// TestEventRoutesPongToTracker is the regression tripwire for the ping/pong
// pending pile-up. PONG callbacks arrive on the pseudo-address "CENTRAL",
// which is not a mirrored device, so CallbackHandlers.Event's
// device-existence guard dropped them before the PONG-token routing at the
// bottom of the method could run. The ping-pong tracker therefore never
// matched a PONG, pending grew to its cap (100) on every interface, and
// health stayed permanently degraded.
//
// Mirrors the reference event-coordinator data_point_event flow, which routes
// PONG before any device-specific logic.
func TestEventRoutesPongToTracker(t *testing.T) {
	t.Parallel()

	c := newTestCentralNamed(t, "ccu-01")
	ic := pingPongClient(t, "ccu-01", "HmIP-RF")
	if err := c.Clients.Register(&coordinators.ClientEntry{
		InterfaceID: "HmIP-RF",
		Interface:   hmenum.InterfaceHmIPRF,
		Client:      ic,
	}); err != nil {
		t.Fatalf("register client: %v", err)
	}

	// Wire the PONG-ingest hook onto the EventCoordinator (normally done by
	// WirePingPongBus during central bring-up).
	WirePingPongBus(c, ic, "HmIP-RF", nil)

	// Record an outbound PING, exactly as CheckConnectionAvailability does:
	// caller_id = "<interface>#<token>".
	ic.RecordPing("42")
	if got := ic.PingPong().PendingCount(); got != 1 {
		t.Fatalf("precondition: PendingCount=%d, want 1", got)
	}

	// The CCU echoes the caller_id back as a PONG event on the CENTRAL
	// pseudo-address.
	h := NewCallbackHandlers(c, nil)
	if err := h.Event(context.Background(), "HmIP-RF", "CENTRAL", "PONG", xmlrpc.StringValue("HmIP-RF#42")); err != nil {
		t.Fatalf("Event(PONG): %v", err)
	}

	if got := ic.PingPong().PendingCount(); got != 0 {
		t.Fatalf("PONG must close the pending ping: PendingCount=%d, want 0 — "+
			"CallbackHandlers.Event must route PONG to the ping-pong tracker "+
			"before the device-existence guard", got)
	}
}

// TestPongForUnregisteredInterfaceLeavesNoEventClock pins that the one
// callback which runs before the device-existence guard cannot create
// per-interface state for an interface this central never registered.
//
// PONG is stamped on the pseudo-address "CENTRAL", so nothing downstream
// constrains the interface_id the sender chose. The XML-RPC callback listener
// takes no authentication and its source-IP allow-list is off by default, and
// the event clocks are only reset when the central is torn down: any host on
// the LAN could otherwise post PONGs with fresh (and multi-megabyte)
// interface_ids and grow the daemon's live heap until it is killed. An
// interface no client registered under carries no liveness signal for this
// central, so dropping it loses nothing.
func TestPongForUnregisteredInterfaceLeavesNoEventClock(t *testing.T) {
	t.Parallel()

	c := newTestCentralNamed(t, "ccu-01")
	ic := pingPongClient(t, "ccu-01", "HmIP-RF")
	if err := c.Clients.Register(&coordinators.ClientEntry{
		InterfaceID: "HmIP-RF",
		Interface:   hmenum.InterfaceHmIPRF,
		Client:      ic,
	}); err != nil {
		t.Fatalf("register client: %v", err)
	}

	h := NewCallbackHandlers(c, nil)
	fabricated := strings.Repeat("A", 4096)
	if err := h.Event(context.Background(), fabricated, "CENTRAL", "PONG",
		xmlrpc.StringValue(fabricated+"#1")); err != nil {
		t.Fatalf("Event(PONG) for an unregistered interface must not error: %v", err)
	}
	if _, observed := c.Events.LastEventMonotonicForInterface(fabricated); observed {
		t.Fatal("a PONG naming an interface no client registered under must not create " +
			"an event-clock entry — the map is only cleared on teardown, so every " +
			"fabricated id is retained for the lifetime of the daemon")
	}

	// The registered interface keeps its liveness signal.
	if err := h.Event(context.Background(), "HmIP-RF", "CENTRAL", "PONG",
		xmlrpc.StringValue("HmIP-RF#1")); err != nil {
		t.Fatalf("Event(PONG): %v", err)
	}
	if _, observed := c.Events.LastEventMonotonicForInterface("HmIP-RF"); !observed {
		t.Fatal("a PONG for a registered interface must still stamp the event clock")
	}
}
