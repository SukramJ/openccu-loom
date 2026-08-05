// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/internal/client/transport/xmlrpc"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
)

// TestEventForwardsDataPointLessEventParameters covers the callback branch
// for parameters that deliberately have no data point.
//
// The device pipeline creates no data point for an impulse (SEQUENCE_OK) or a
// device error (ERROR*, SENSOR_ERROR*): they are events, not state, and the
// resolver drops them mirroring the reference. The callback used to return as
// soon as the channel had no data point for the parameter, which dropped
// precisely those two families before the coordinator ever saw them — so a
// reported fault produced no device-trigger event, no WebSocket broadcast and
// no record on the channel's event group.
//
// Keypress never showed the gap: a PRESS_* parameter is writable, so it does
// get a data point and travels the ordinary path.
//
// Impulse is only reachable here. No device in the e2e fleet carries
// SEQUENCE_OK, so the black-box pin can exercise the device-error half alone.
func TestEventForwardsDataPointLessEventParameters(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		parameter string
		value     xmlrpc.Value
		wantKind  hmenum.DeviceTriggerEventType
	}{
		{
			name:      "device error integer",
			parameter: "ERROR_CODE",
			value:     xmlrpc.IntValue(5),
			wantKind:  hmenum.DeviceTriggerEventType("homematic.device_error"),
		},
		{
			name:      "device error boolean",
			parameter: "ERROR_OVERHEAT",
			value:     xmlrpc.BoolValue(true),
			wantKind:  hmenum.DeviceTriggerEventType("homematic.device_error"),
		},
		{
			name:      "impulse",
			parameter: string(hmenum.ParameterSequenceOK),
			value:     xmlrpc.BoolValue(true),
			wantKind:  hmenum.DeviceTriggerEventType("homematic.impulse"),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			reg, dev := registryWithDevice(t)
			// The channel carries no data point at all — the state the
			// resolver leaves behind for these parameters.
			dev.AddChannel("0001ABCD:1", 1, "TEST", hmenum.ParamsetKeyValues)
			c := reg.List()[0]

			var fired atomic.Int32
			var got hmevent.DeviceTriggerEvent
			unsub := events.Subscribe(c.EventBus, func(e hmevent.DeviceTriggerEvent) {
				fired.Add(1)
				got = e
			})
			defer unsub()

			h := NewCallbackHandlers(c, nil)
			if err := h.Event(context.Background(), "HmIP-RF", "0001ABCD:1", tc.parameter, tc.value); err != nil {
				t.Fatalf("Event: %v", err)
			}

			if n := fired.Load(); n != 1 {
				t.Fatalf("got %d DeviceTriggerEvent for %s, want 1 — a parameter with no data "+
					"point still has to reach the coordinator when it classifies as an event, "+
					"or the whole family is silently undeliverable", n, tc.parameter)
			}
			if got.Parameter != tc.parameter {
				t.Errorf("parameter = %q, want %q", got.Parameter, tc.parameter)
			}
			if got.EventType_ != tc.wantKind {
				t.Errorf("event type = %q, want %q", got.EventType_, tc.wantKind)
			}
			if got.DeviceAddress != "0001ABCD" || got.ChannelNo != 1 {
				t.Errorf("address = %s:%d, want 0001ABCD:1", got.DeviceAddress, got.ChannelNo)
			}
		})
	}
}

// TestEventStillDropsUnknownDataPointLessParameters keeps the widened branch
// narrow: a parameter with no data point that classifies as nothing is noise
// and must stay dropped, or every unmodelled name the CCU pushes would turn
// into a north-bound trigger.
func TestEventStillDropsUnknownDataPointLessParameters(t *testing.T) {
	t.Parallel()

	reg, dev := registryWithDevice(t)
	dev.AddChannel("0001ABCD:1", 1, "TEST", hmenum.ParamsetKeyValues)
	c := reg.List()[0]

	var fired atomic.Int32
	unsubTrigger := events.Subscribe(c.EventBus, func(_ hmevent.DeviceTriggerEvent) { fired.Add(1) })
	defer unsubTrigger()
	unsubValue := events.Subscribe(c.EventBus, func(_ hmevent.DataPointValueChangedEvent) { fired.Add(1) })
	defer unsubValue()

	h := NewCallbackHandlers(c, nil)
	if err := h.Event(context.Background(), "HmIP-RF", "0001ABCD:1", "SOME_UNMODELLED_PARAM",
		xmlrpc.IntValue(1)); err != nil {
		t.Fatalf("Event: %v", err)
	}

	if n := fired.Load(); n != 0 {
		t.Fatalf("got %d events for an unclassified parameter with no data point, want 0", n)
	}
}
