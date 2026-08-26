// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"context"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/client/transport/xmlrpc"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// TestCallbackHandlersDispatchesCombinedParameter verifies that a
// COMBINED_PARAMETER callback decomposes into the individual LEVEL / LEVEL_2
// data points instead of being routed to a non-existent `COMBINED_PARAMETER`
// data point.
func TestCallbackHandlersDispatchesCombinedParameter(t *testing.T) {
	reg, dev := registryWithDevice(t)
	ch := dev.AddChannel("0001ABCD:1", 1, "TEST", hmenum.ParamsetKeyValues)

	level := newFloatDP(hmenum.ParameterLevel, "0001ABCD:1")
	level2 := newFloatDP(hmenum.ParameterLevel2, "0001ABCD:1")
	ch.Put(level)
	ch.Put(level2)

	c := reg.List()[0]
	h := NewCallbackHandlers(c, nil)
	if err := h.Event(context.Background(), "HmIP-RF", "0001ABCD:1",
		"COMBINED_PARAMETER", xmlrpc.StringValue("L=80,L2=20")); err != nil {
		t.Fatalf("Event: %v", err)
	}

	if v, ok := level.Value(); !ok || v != 0.8 {
		t.Fatalf("LEVEL: v=%v ok=%v want 0.8", v, ok)
	}
	if v, ok := level2.Value(); !ok || v != 0.2 {
		t.Fatalf("LEVEL_2: v=%v ok=%v want 0.2", v, ok)
	}
}

// TestCallbackHandlersDispatchesLevelCombined verifies that
// LEVEL_COMBINED with hex pair decomposes into LEVEL + LEVEL_SLATS.
func TestCallbackHandlersDispatchesLevelCombined(t *testing.T) {
	reg, dev := registryWithDevice(t)
	ch := dev.AddChannel("0001ABCD:1", 1, "TEST", hmenum.ParamsetKeyValues)

	level := newFloatDP(hmenum.ParameterLevel, "0001ABCD:1")
	slats := newFloatDP(hmenum.Parameter("LEVEL_SLATS"), "0001ABCD:1")
	ch.Put(level)
	ch.Put(slats)

	c := reg.List()[0]
	h := NewCallbackHandlers(c, nil)
	if err := h.Event(context.Background(), "HmIP-RF", "0001ABCD:1",
		"LEVEL_COMBINED", xmlrpc.StringValue("0xc8,0x32")); err != nil {
		t.Fatalf("Event: %v", err)
	}

	if v, ok := level.Value(); !ok || v != 1.0 {
		t.Fatalf("LEVEL: v=%v ok=%v want 1.0", v, ok)
	}
	if v, ok := slats.Value(); !ok || v != 0.25 {
		t.Fatalf("LEVEL_SLATS: v=%v ok=%v want 0.25", v, ok)
	}
}

// TestCallbackHandlersCombinedUnparseableSilentlyDrops verifies that
// a malformed combined-parameter payload neither errors nor mutates
// any data point.
func TestCallbackHandlersCombinedUnparseableSilentlyDrops(t *testing.T) {
	reg, dev := registryWithDevice(t)
	ch := dev.AddChannel("0001ABCD:1", 1, "TEST", hmenum.ParamsetKeyValues)
	level := newFloatDP(hmenum.ParameterLevel, "0001ABCD:1")
	ch.Put(level)

	c := reg.List()[0]
	h := NewCallbackHandlers(c, nil)
	// Missing '=' triggers parseCombined to abort.
	if err := h.Event(context.Background(), "HmIP-RF", "0001ABCD:1",
		"COMBINED_PARAMETER", xmlrpc.StringValue("garbage")); err != nil {
		t.Fatalf("Event: %v", err)
	}
	if _, observed := level.Value(); observed {
		t.Fatal("data point should remain unobserved when payload unparseable")
	}
}

// TestCallbackHandlersCombinedSkipsUnknownSubParam verifies that a
// LEVEL_2 sub-update for a channel that only carries LEVEL is
// silently ignored (no panic, no partial state mutation).
func TestCallbackHandlersCombinedSkipsUnknownSubParam(t *testing.T) {
	reg, dev := registryWithDevice(t)
	ch := dev.AddChannel("0001ABCD:1", 1, "TEST", hmenum.ParamsetKeyValues)
	level := newFloatDP(hmenum.ParameterLevel, "0001ABCD:1")
	ch.Put(level) // intentionally no LEVEL_2 data point

	c := reg.List()[0]
	h := NewCallbackHandlers(c, nil)
	if err := h.Event(context.Background(), "HmIP-RF", "0001ABCD:1",
		"COMBINED_PARAMETER", xmlrpc.StringValue("L=42,L2=99")); err != nil {
		t.Fatalf("Event: %v", err)
	}
	if v, ok := level.Value(); !ok || v != 0.42 {
		t.Fatalf("LEVEL: v=%v ok=%v want 0.42", v, ok)
	}
}

// newFloatDP builds a minimal generic.DataPoint[float64] suitable for
// the combined-parameter dispatch tests. The descriptor exposes only
// what `OnWireValue` consults (Type + Operations).
func newFloatDP(p hmenum.Parameter, channelAddr string) *generic.DataPoint[float64] {
	return generic.NewDataPoint[float64](generic.Spec{
		Key: hmtypes.DataPointKey{
			InterfaceID:    "HmIP-RF",
			ChannelAddress: channelAddr,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(p),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeFloat,
			Operations: hmenum.OperationsRead | hmenum.OperationsEvent,
		},
	})
}
