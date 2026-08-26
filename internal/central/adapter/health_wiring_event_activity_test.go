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

// TestSouthboundCallbackFeedsHealthActivity drives a real XML-RPC callback
// through the production chain — CallbackHandlers.Event → EventCoordinator →
// bus → WireHealth — and asserts the effect on the health tracker.
//
// The tracker's activity pillar is 30 % of an interface's score, and the only
// thing that feeds it is a `event-received` sample from
// [health.Tracker.RecordEventReceived]. Its sole production caller sits behind
// a DataPointValueReceivedEvent subscription; while nothing published that
// event, a CCU that had been pushing callbacks for hours still reported a zero
// "last event received" and could never score above 0.70. The test deliberately
// does not publish the event itself: it fires the callback the CCU fires and
// checks what an operator would see on the diagnostics surface.
func TestSouthboundCallbackFeedsHealthActivity(t *testing.T) {
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

	// A connected, fault-free interface: everything except the activity pillar
	// is already at full credit, so the score isolates what the callback adds.
	events.Publish(c.EventBus, hmevent.ClientStateChangedEvent{
		Base:        hmevent.NewBase(),
		CentralName: c.Name(),
		InterfaceID: wireID,
		To:          hmenum.ClientStateConnected,
	})
	if got := c.Health.ClientScore(wireID); got > 0.9 {
		t.Fatalf("score %.2f before any callback — the activity pillar must start empty", got)
	}

	h := NewCallbackHandlers(c, nil)
	if err := h.Event(context.Background(), wireID, "0001ABCD:1", "STATE", xmlrpc.BoolValue(true)); err != nil {
		t.Fatalf("Event: %v", err)
	}

	if !c.Health.CanReceiveEvents(wireID, 0) {
		t.Error("CanReceiveEvents is false after a southbound callback — " +
			"nothing recorded interface activity")
	}
	if got := c.Health.ClientScore(wireID); got <= 0.9 {
		t.Errorf("ClientScore = %.2f after a southbound callback on a healthy "+
			"interface, want > 0.9 — the 30%% activity pillar is dead", got)
	}

	// A repeated identical value is still traffic on the wire. The coordinator
	// suppresses the value-change event for it; the activity signal must
	// survive that filter, or a device reporting a stable reading looks silent.
	before := c.Health.ClientScore(wireID)
	if err := h.Event(context.Background(), wireID, "0001ABCD:1", "STATE", xmlrpc.BoolValue(true)); err != nil {
		t.Fatalf("Event (repeat): %v", err)
	}
	if !c.Health.CanReceiveEvents(wireID, 0) || c.Health.ClientScore(wireID) < before {
		t.Error("a repeated identical value stopped counting as interface activity")
	}
}
