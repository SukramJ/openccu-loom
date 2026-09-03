// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central/coordinators"
	clientpkg "github.com/SukramJ/openccu-loom/internal/client"
	"github.com/SukramJ/openccu-loom/internal/client/backends"
	"github.com/SukramJ/openccu-loom/internal/client/reliability"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// TestPingCallerIDRoundTripsThroughTheWire drives the caller_id grammar end to
// end through its two production halves: the ping the client emits, and the
// PONG-ingest hook WirePingPongBus installs on the event coordinator. Neither
// side is asked to spell the separator here — the test only echoes back what
// the wire carried — so a producer and a parser that disagree show up as an
// uncorrelated PONG.
func TestPingCallerIDRoundTripsThroughTheWire(t *testing.T) {
	t.Parallel()

	const (
		centralName = "ccu-pp"
		ifaceID     = "instance-ccu-pp-HmIP-RF"
	)

	c := newTestCentralNamed(t, centralName)

	var mu sync.Mutex
	var sentCallerID string
	ic, err := clientpkg.New(clientpkg.Config{
		CentralName:     centralName,
		Interface:       hmenum.InterfaceHmIPRF,
		InitInterfaceID: ifaceID,
		Capabilities:    backends.Capabilities{PingPong: true},
		Caller: clientpkg.CallerFunc(func(_ context.Context, method string, args []any) (any, error) {
			if method == "ping" && len(args) == 1 {
				mu.Lock()
				sentCallerID, _ = args[0].(string)
				mu.Unlock()
			}
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
		InterfaceID: ifaceID,
		Interface:   hmenum.InterfaceHmIPRF,
		Client:      ic,
	}); err != nil {
		t.Fatalf("Register: %v", err)
	}

	WirePingPongBus(c, ic, ifaceID, nil)

	if !ic.CheckConnectionAvailability(context.Background(), true) {
		t.Fatal("CheckConnectionAvailability reported the probe failed")
	}
	mu.Lock()
	callerID := sentCallerID
	mu.Unlock()
	if callerID == "" {
		t.Fatal("the ping carried no caller_id")
	}
	if ic.PingPong().PendingCount() != 1 {
		t.Fatalf("PendingPingCount after the ping = %d, want 1", ic.PingPong().PendingCount())
	}

	// Echo the caller_id back exactly as the CCU does, through the real PONG
	// route on the event coordinator.
	c.Events.HandleRawEventNormalized(
		context.Background(), ifaceID, "BidCoS-RF:0", "PONG",
		hmtypes.ParamValue{Kind: hmtypes.ValueKindString, String: callerID},
	)

	if got := ic.PingPong().PendingCount(); got != 0 {
		t.Fatalf("the echoed caller_id %q was not correlated: PendingPingCount = %d, want 0", callerID, got)
	}

	// A PONG whose prefix belongs to another daemon must not be attributed
	// here, and must not be filed as one of our unknowns either.
	c.Events.HandleRawEventNormalized(
		context.Background(), ifaceID, "BidCoS-RF:0", "PONG",
		hmtypes.ParamValue{Kind: hmtypes.ValueKindString, String: "OtherLoom-other-HmIP-RF#1"},
	)
	if got := ic.PingPong().UnknownCount(); got != 0 {
		t.Fatalf("a foreign daemon's PONG was attributed to this client: UnknownPongCount = %d, want 0", got)
	}
}
