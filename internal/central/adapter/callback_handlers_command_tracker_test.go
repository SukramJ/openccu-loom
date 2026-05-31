// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"context"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central/coordinators"
	"github.com/SukramJ/openccu-loom/internal/client"
	"github.com/SukramJ/openccu-loom/internal/client/transport/xmlrpc"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// TestEventClearsCommandTrackerOnCoercedEcho is the regression tripwire
// for the bug where CallbackHandlers.Event updated the model but never
// called CommandTracker.ClearForKey on the matching client. Without
// the clear, last-value-send entries linger until their 60 s TTL
// elapses; downstream consumers that read HasInFlight (metrics,
// optimistic-state probes) see stale data for up to a full TTL window.
//
// Mirrors the reference event() flow which always calls
// last_value_send_tracker.remove_last_value_send(dpk, value) after a
// confirmed CCU push.
func TestEventClearsCommandTrackerOnCoercedEcho(t *testing.T) {
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

	// Build a minimal InterfaceClient and register it via ClientCoordinator
	// so the CallbackHandlers.Event path can resolve the tracker.
	ic, err := client.New(client.Config{
		CentralName: "ccu-01",
		Interface:   hmenum.InterfaceHmIPRF,
		Caller: client.CallerFunc(func(_ context.Context, _ string, _ []any) (any, error) {
			return nil, nil
		}),
	})
	if err != nil {
		t.Fatalf("client.New: %v", err)
	}
	if err := c.Clients.Register(&coordinators.ClientEntry{
		InterfaceID: "HmIP-RF",
		Interface:   hmenum.InterfaceHmIPRF,
		Client:      ic,
	}); err != nil {
		t.Fatalf("register client: %v", err)
	}

	dpk := hmtypes.DataPointKey{
		InterfaceID:    "HmIP-RF",
		ChannelAddress: "0001ABCD:1",
		ParamsetKey:    hmenum.ParamsetKeyValues,
		Parameter:      "STATE",
	}

	// Seed an in-flight entry; the CCU echo should clear it.
	ic.CommandTracker().AddSetValue("0001ABCD:1", hmenum.Parameter("STATE"), hmenum.ParamsetKeyValues, true)
	if !ic.CommandTracker().HasInFlight(dpk) {
		t.Fatal("precondition: AddSetValue must register the entry")
	}

	h := NewCallbackHandlers(c, nil)
	if err := h.Event(context.Background(), "HmIP-RF", "0001ABCD:1", "STATE", xmlrpc.BoolValue(true)); err != nil {
		t.Fatalf("Event: %v", err)
	}

	if ic.CommandTracker().HasInFlight(dpk) {
		t.Fatal("CallbackHandlers.Event must clear CommandTracker entry on coerced CCU echo")
	}
}

// TestEventKeepsCommandTrackerOnCoerceFailure verifies that a failed
// coercion does NOT clear the tracker — the in-flight entry stays so
// the operator-visible state remains "pending" while the self-reload
// retries.
func TestEventKeepsCommandTrackerOnCoerceFailure(t *testing.T) {
	t.Parallel()

	reg, dev := registryWithDevice(t)
	ch := dev.AddChannel("0001ABCD:1", 1, "TEST", hmenum.ParamsetKeyValues)
	// Bool DP; we feed a non-coercible string to force the !coerced
	// branch.
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
	ic, err := client.New(client.Config{
		CentralName: "ccu-01",
		Interface:   hmenum.InterfaceHmIPRF,
		Caller: client.CallerFunc(func(_ context.Context, _ string, _ []any) (any, error) {
			return nil, nil
		}),
	})
	if err != nil {
		t.Fatalf("client.New: %v", err)
	}
	if err := c.Clients.Register(&coordinators.ClientEntry{
		InterfaceID: "HmIP-RF",
		Interface:   hmenum.InterfaceHmIPRF,
		Client:      ic,
	}); err != nil {
		t.Fatalf("register client: %v", err)
	}

	dpk := hmtypes.DataPointKey{
		InterfaceID:    "HmIP-RF",
		ChannelAddress: "0001ABCD:1",
		ParamsetKey:    hmenum.ParamsetKeyValues,
		Parameter:      "STATE",
	}
	ic.CommandTracker().AddSetValue("0001ABCD:1", hmenum.Parameter("STATE"), hmenum.ParamsetKeyValues, true)

	h := NewCallbackHandlers(c, nil)
	// Struct-typed xmlrpc value is non-coercible to bool; OnWireValue
	// returns false and the !coerced branch fires.
	if err := h.Event(context.Background(), "HmIP-RF", "0001ABCD:1", "STATE",
		xmlrpc.StructValue{Members: nil}); err != nil {
		t.Fatalf("Event: %v", err)
	}

	if !ic.CommandTracker().HasInFlight(dpk) {
		t.Fatal("a failed coercion must NOT clear the tracker — the optimistic state stays pending")
	}
}
