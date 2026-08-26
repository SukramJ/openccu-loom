// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/internal/client/transport/xmlrpc"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// TestEvent_EmptyStringForIntegerIsSilentlyAbsorbed locks the
// production fix: when the CCU pushes an empty string for an
// INTEGER- or FLOAT-typed descriptor (observed live on HmIP-BDT
// channel-3 SECTION between dim-program transitions), the
// callback handler MUST treat it as an "absent value" sentinel —
// no coerce_failed log, no self-reload retry, no
// DataPointValueChangedEvent on the bus.
func TestEvent_EmptyStringForIntegerIsSilentlyAbsorbed(t *testing.T) {
	t.Parallel()

	reg, dev := registryWithDevice(t)
	ch := dev.AddChannel("0001ABCD:3", 3, "DIMMER_TRANSMITTER", hmenum.ParamsetKeyValues)

	// Sensor[int32] for an INTEGER descriptor — the production
	// resolver registers SECTION (TYPE=INTEGER) as Sensor[int32].
	dp := generic.NewSensor[int32](generic.Spec{
		Key: hmtypes.DataPointKey{
			InterfaceID:    "HmIP-RF",
			ChannelAddress: "0001ABCD:3",
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(hmenum.ParameterSection),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeInteger,
			Operations: hmenum.OperationsRead | hmenum.OperationsEvent,
		},
	})
	ch.Put(dp)

	c := reg.List()[0]

	var fired atomic.Int32
	unsub := events.Subscribe(c.EventBus, func(_ hmevent.DataPointValueChangedEvent) {
		fired.Add(1)
	})
	defer unsub()

	h := NewCallbackHandlers(c, nil)
	if err := h.Event(context.Background(), "HmIP-RF", "0001ABCD:3",
		string(hmenum.ParameterSection), xmlrpc.StringValue("")); err != nil {
		t.Fatalf("Event: %v", err)
	}

	if n := fired.Load(); n != 0 {
		t.Errorf("DataPointValueChangedEvent fired %d times — empty-string-for-INTEGER must absorb silently, no bus event", n)
	}
	if _, observed := dp.Value(); observed {
		t.Errorf("DP observed=true after empty-string event — absorb must leave the DP untouched")
	}
}

// TestEvent_EmptyStringForFloatIsSilentlyAbsorbed mirrors the
// INTEGER case for FLOAT-typed descriptors. Both numeric kinds
// take the same "empty-string = absent value" branch in the
// callback handler.
func TestEvent_EmptyStringForFloatIsSilentlyAbsorbed(t *testing.T) {
	t.Parallel()

	reg, dev := registryWithDevice(t)
	ch := dev.AddChannel("0001ABCD:4", 4, "CLIMATE", hmenum.ParamsetKeyValues)

	dp := generic.NewSensor[float64](generic.Spec{
		Key: hmtypes.DataPointKey{
			InterfaceID:    "HmIP-RF",
			ChannelAddress: "0001ABCD:4",
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(hmenum.ParameterActualTemperature),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeFloat,
			Operations: hmenum.OperationsRead | hmenum.OperationsEvent,
		},
	})
	ch.Put(dp)

	c := reg.List()[0]
	h := NewCallbackHandlers(c, nil)
	if err := h.Event(context.Background(), "HmIP-RF", "0001ABCD:4",
		string(hmenum.ParameterActualTemperature), xmlrpc.StringValue("")); err != nil {
		t.Fatalf("Event: %v", err)
	}

	if _, observed := dp.Value(); observed {
		t.Errorf("DP observed=true after empty-string event for FLOAT type — absorb must leave the DP untouched")
	}
}
