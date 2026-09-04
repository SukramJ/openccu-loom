// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"context"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/internal/client/transport/xmlrpc"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// TestBreakerNoteLiteralDrivesTheCircuitScore crosses the package boundary the
// breaker note straddles: internal/central/adapter writes the note text, and
// internal/health's scorer matches on that text with strings.Contains. The two
// spellings are only tied by the literal, and nothing failed when they drifted.
//
// The test drives WireHealth on a real bus with a real tracker and measures
// the 30 % circuit pillar collapsing when the breaker opens.
func TestBreakerNoteLiteralDrivesTheCircuitScore(t *testing.T) {
	t.Parallel()

	const wireID = "ccu-01-HmIP-RF"

	reg, dev := registryWithDevice(t)
	c := reg.List()[0]
	ch := dev.AddChannel("0001ABCD:1", 1, "TEST", hmenum.ParamsetKeyValues)
	ch.Put(generic.NewDataPoint[bool](generic.Spec{
		Key: hmtypes.DataPointKey{
			InterfaceID:    wireID,
			ChannelAddress: "0001ABCD:1",
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      "STATE",
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeBool,
			Operations: hmenum.OperationsRead | hmenum.OperationsEvent,
		},
	}))

	closer := WireHealth(c)
	defer closer()

	// Bring the interface to full credit on the state and activity pillars so
	// only the circuit pillar can move the score.
	events.Publish(c.EventBus, hmevent.ClientStateChangedEvent{
		Base:        hmevent.NewBase(),
		CentralName: c.Name(),
		InterfaceID: wireID,
		To:          hmenum.ClientStateConnected,
	})
	h := NewCallbackHandlers(c, nil)
	if err := h.Event(context.Background(), wireID, "0001ABCD:1", "STATE", xmlrpc.BoolValue(true)); err != nil {
		t.Fatalf("Event: %v", err)
	}

	events.Publish(c.EventBus, hmevent.CircuitBreakerStateChangedEvent{
		Base:        hmevent.NewBase(),
		CentralName: c.Name(),
		InterfaceID: wireID,
		To:          hmenum.CircuitStateClosed,
	})
	closed := c.Health.ClientScore(wireID)
	if closed <= 0.9 {
		t.Fatalf("baseline score with a closed breaker = %.2f, want > 0.9", closed)
	}

	events.Publish(c.EventBus, hmevent.CircuitBreakerStateChangedEvent{
		Base:        hmevent.NewBase(),
		CentralName: c.Name(),
		InterfaceID: wireID,
		To:          hmenum.CircuitStateOpen,
	})
	open := c.Health.ClientScore(wireID)

	// An open breaker is recorded unhealthy, so the 40 % state pillar drops
	// whatever the note says. The 30 % circuit pillar is the one that depends
	// on the note text: a note the scorer does not recognise leaves it at full
	// credit and the score lands 0.3 higher. With the activity pillar at full
	// credit the two outcomes are 0.30 (recognised) and 0.60 (not), so the
	// bound below separates them.
	const circuitCollapsed = 0.35
	if open > circuitCollapsed {
		t.Fatalf("ClientScore after the breaker opened = %.2f (closed = %.2f), want <= %.2f: "+
			"the circuit pillar kept its credit — the note the wiring writes is not the "+
			"one internal/health's scorer matches", open, closed, circuitCollapsed)
	}
}
