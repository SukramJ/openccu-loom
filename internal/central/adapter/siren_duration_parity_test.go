// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"context"
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// TestSirenDurationMeansTheSameOnBothPlanes pins that one payload produces one
// wire value, whichever surface carried it.
//
// The invoke plane (REST, WebSocket, the MQTT cdp-invoke topic) read a bare
// `duration` number as milliseconds; the siren's own service handler, which
// the per-service MQTT topic reaches, read it as seconds. {"duration": 30}
// therefore wrote DURATION_VALUE=0 through one and 30 through the other — same
// key, same device, and no error on either path to say so.
//
// Both planes share one reader now (siren.ParseOnDuration), so this asserts
// the agreement rather than a value: the invoke plane must produce the wire
// values the service handler produces for the same input.
func TestSirenDurationMeansTheSameOnBothPlanes(t *testing.T) {
	t.Parallel()

	dispatched := func(t *testing.T, addr string, params map[string]any) map[string]any {
		t.Helper()
		w := &dispatchWriter{}
		_, carrier := buildSirenDP(addr, w)
		disp, _ := buildDispatcher(t, addr, "STATE", carrier)
		if err := disp.InvokeCustomDP(context.Background(), addr, "STATE", "turn_on",
			params, hmenum.CommandPriorityHigh, "test"); err != nil {
			t.Fatalf("turn_on %v: %v", params, err)
		}
		w.mu.Lock()
		defer w.mu.Unlock()
		if len(w.puts) == 0 {
			t.Fatalf("turn_on %v wrote no paramset", params)
		}
		return w.puts[len(w.puts)-1].values
	}

	// 30 as a bare number is 30 seconds, not 30 milliseconds. Under the old
	// reading this encoded to DURATION_VALUE=0 — the siren fired with no
	// duration at all.
	bare := dispatched(t, "SRNPAR1", map[string]any{"duration": 30.0})
	explicit := dispatched(t, "SRNPAR2", map[string]any{"duration_seconds": 30.0})
	canonical := dispatched(t, "SRNPAR3", map[string]any{"seconds": 30.0})

	for _, key := range []string{"DURATION_VALUE", "DURATION_UNIT"} {
		if bare[key] != explicit[key] {
			t.Errorf("%s: duration=30 wrote %v, duration_seconds=30 wrote %v", key, bare[key], explicit[key])
		}
		if bare[key] != canonical[key] {
			t.Errorf("%s: duration=30 wrote %v, seconds=30 wrote %v", key, bare[key], canonical[key])
		}
	}
	if got := bare["DURATION_VALUE"]; got == 0 || got == nil {
		t.Errorf("DURATION_VALUE = %v for duration=30; a bare number must not encode to no duration", got)
	}
}
