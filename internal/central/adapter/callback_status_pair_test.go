// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"context"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/client/transport/xmlrpc"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// TestCallbackHandlersStatusPairFallback verifies the `<X>_STATUS` → `<X>`
// fallback.
func TestCallbackHandlersStatusPairFallback(t *testing.T) {
	reg, dev := registryWithDevice(t)
	ch := dev.AddChannel("0001ABCD:1", 1, "TEST", hmenum.ParamsetKeyValues)

	level := newFloatDP(hmenum.ParameterLevel, "0001ABCD:1")
	ch.Put(level)
	// Note: NO LEVEL_STATUS data point is registered.

	c := reg.List()[0]
	h := NewCallbackHandlers(c, nil)

	// Seed a real measurement first.
	level.OnWireValue(19.6)

	// CCU sends a STATUS event for LEVEL (measurement status index,
	// typically 0 = NORMAL). It must update the base DP's STATUS — and
	// MUST NOT overwrite the measurement: writing the status index over
	// the value made every HA sensor oscillate between the real value
	// and 0 (one bogus zero per CCU burst).
	if err := h.Event(context.Background(), "HmIP-RF", "0001ABCD:1",
		"LEVEL_STATUS", xmlrpc.IntValue(0)); err != nil {
		t.Fatalf("Event: %v", err)
	}
	if v, ok := level.Value(); !ok || v != 19.6 {
		t.Fatalf("LEVEL value after _STATUS event: v=%v ok=%v want 19.6 (unchanged)", v, ok)
	}
}

// TestCallbackHandlersStatusPairPrefersDedicatedDP verifies the regular
// case (openccu-loom creates a DP for every wire parameter): the _STATUS
// event lands on the dedicated LEVEL_STATUS DP as its value, the base
// DP's VALUE stays untouched, and the base DP's STATUS is updated.
func TestCallbackHandlersStatusPairPrefersDedicatedDP(t *testing.T) {
	reg, dev := registryWithDevice(t)
	ch := dev.AddChannel("0001ABCD:1", 1, "TEST", hmenum.ParamsetKeyValues)

	level := newFloatDP(hmenum.ParameterLevel, "0001ABCD:1")
	levelStatus := newFloatDP(hmenum.Parameter("LEVEL_STATUS"), "0001ABCD:1")
	ch.Put(level)
	ch.Put(levelStatus)
	level.SetStatusParameter("LEVEL_STATUS", []string{"NORMAL", "UNKNOWN", "OVERFLOW"})

	c := reg.List()[0]
	h := NewCallbackHandlers(c, nil)

	// Seed a real measurement, then deliver the paired status (0 = NORMAL).
	level.OnWireValue(19.6)
	if err := h.Event(context.Background(), "HmIP-RF", "0001ABCD:1",
		"LEVEL_STATUS", xmlrpc.IntValue(0)); err != nil {
		t.Fatalf("Event: %v", err)
	}
	if v, ok := level.Value(); !ok || v != 19.6 {
		t.Fatalf("LEVEL value after _STATUS event: v=%v ok=%v want 19.6 (unchanged)", v, ok)
	}
	if s, ok := level.Status(); !ok || s != hmenum.ParameterStatusNormal {
		t.Fatalf("LEVEL status = %v ok=%v, want NORMAL", s, ok)
	}
}

// TestCallbackHandlersStatusPairUnknownPair verifies that a _STATUS
// event with neither a dedicated DP nor a base counterpart silently
// drops (no panic, no error).
func TestCallbackHandlersStatusPairUnknownPair(t *testing.T) {
	reg, dev := registryWithDevice(t)
	dev.AddChannel("0001ABCD:1", 1, "TEST", hmenum.ParamsetKeyValues)

	c := reg.List()[0]
	h := NewCallbackHandlers(c, nil)
	if err := h.Event(context.Background(), "HmIP-RF", "0001ABCD:1",
		"FOOBAR_STATUS", xmlrpc.DoubleValue(0.42)); err != nil {
		t.Fatalf("Event must not error on unknown pair: %v", err)
	}
}
