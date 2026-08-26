// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

//go:build integration

package integration

// Plan-Runner for the SPA-E2E layer. A `plan` is the declarative
// counterpart to a Svelte tile click: which CDP, which operation,
// which expected wire-state afterwards. The runner walks the
// actions in order, invokes through the daemon dispatcher, and
// re-reads the device state from godevccu to confirm the wire-side
// effect matches the expectation.

import (
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
)

// spaPlan models a single Tile-click sequence.
type spaPlan struct {
	name     string
	model    string
	chNo     int
	wantKind string
	actions  []spaAction
}

// spaAction is one (operation, params) tuple plus the expected
// post-condition. Pre-conditions are seeded via spaPlan.seedWire.
type spaAction struct {
	op     string
	params map[string]any
	// wantWire lists wire-DPs that should hold these values after
	// the dispatcher returns. Each entry is a (parameter, value)
	// tuple read back via godevccu.getValue. nil → skip wire check.
	wantWire map[hmenum.Parameter]any
	// wantEvents asserts that the central EventBus published one
	// DataPointValueChangedEvent per entry. Each entry names a
	// (parameter, value) tuple — the runner waits up to ~250 ms
	// for the matching event to land before asserting. Empty →
	// skip the event check.
	//
	// NOTE: wantEvents only fires once the harness wires the
	// daemon's XML-RPC callback server so godevccu's post-write
	// event() callbacks reach the central EventBus. The current
	// harness does not — wantEvents is plumbed end-to-end but
	// only useful for tests that script their own echoes via the
	// godevccu OnSetValue hook (see startMockCCUWithOptions).
	wantEvents []eventExpect
	// wantErrContains, when non-empty, asserts the invoke returned
	// an error whose Error() contains the substring. Use for
	// negative tests ("set_tilt on a plain Cover").
	wantErrContains string
	// wantCapturedAny asserts that a setValue carrying these
	// (parameter, value) tuples was captured on ANY channel of the
	// device. Use for channel-group custom DPs that write a parameter
	// to an offset sub-channel rather than the DP's primary channel
	// (e.g. the HmIP-BSL fixed-color light writes COLOR to a signal
	// sub-channel), where the channel-scoped wantWire check cannot see
	// the write.
	wantCapturedAny map[hmenum.Parameter]any
}

// eventExpect names a single DataPointValueChangedEvent the
// runner should observe after an action's invoke returns.
type eventExpect struct {
	parameter hmenum.Parameter
	value     any
}

// execute runs the plan and reports each action's pass/fail through
// t.Run so go test -v shows a per-action summary.
func (p spaPlan) execute(t *testing.T, h *spaHarness) {
	t.Helper()
	dp, ch := h.findCustomDP(p.model, p.chNo)
	if p.wantKind != "" {
		if got := h.kindOf(dp); got != p.wantKind {
			t.Fatalf("[%s] kind = %q, want %q", p.name, got, p.wantKind)
		}
	}
	for i, a := range p.actions {
		i, a := i, a
		stepName := fmt.Sprintf("%s/step%d_%s", p.name, i, a.op)
		t.Run(stepName, func(t *testing.T) {
			h.resetEvents()
			err := h.invoke(dp, a.op, a.params)
			if a.wantErrContains != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", a.wantErrContains)
				}
				if !contains(err.Error(), a.wantErrContains) {
					t.Fatalf("error = %q, want substring %q", err.Error(), a.wantErrContains)
				}
				return
			}
			if err != nil {
				t.Fatalf("invoke %s(%v): %v", a.op, a.params, err)
			}
			for param, want := range a.wantWire {
				got := h.readWireValue(ch.Address, param)
				if !equalAny(got, want) {
					// Fall back to the OnSetValue capture for parameters
					// where godevccu's getValue is not meaningful
					// (write-only ACTIONs like COMBINED_PARAMETER,
					// LEVEL_COMBINED, DOOR_COMMAND).
					if captured, ok := h.lastSetValue(ch.Address, param); ok && equalAny(captured, want) {
						continue
					}
					h.setCallMu.Lock()
					calls := append([]spaSetCall(nil), h.setCalls...)
					h.setCallMu.Unlock()
					t.Errorf("wire %s/%s = %v (%T), want %v (%T); captured %d calls: %+v",
						ch.Address, param, got, got, want, want, len(calls), calls)
				}
			}
			for param, want := range a.wantCapturedAny {
				got, ok := h.lastSetValueAnyChannel(param)
				if !ok {
					h.setCallMu.Lock()
					calls := append([]spaSetCall(nil), h.setCalls...)
					h.setCallMu.Unlock()
					t.Errorf("no captured setValue for %s on any channel; captured %d calls: %+v",
						param, len(calls), calls)
					continue
				}
				if !equalAny(got, want) {
					t.Errorf("captured %s = %v (%T), want %v (%T)", param, got, got, want, want)
				}
			}
			for _, ev := range a.wantEvents {
				match := func(e hmevent.DataPointValueChangedEvent) bool {
					return e.Key.ChannelAddress == ch.Address &&
						e.Key.Parameter == string(ev.parameter) &&
						equalAny(e.NewValue.Unwrap(), ev.value)
				}
				captured := h.drainEvents(250*time.Millisecond, match)
				found := false
				for _, e := range captured {
					if match(e) {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("expected event %s/%s=%v not observed; captured %d events",
						ch.Address, ev.parameter, ev.value, len(captured))
				}
			}
		})
	}
}

func contains(haystack, needle string) bool {
	if needle == "" {
		return true
	}
	return len(haystack) >= len(needle) &&
		(haystack == needle ||
			indexOf(haystack, needle) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

// equalAny compares two values with a couple of CCU-friendly tolerances:
//   - int vs int32 / int64: compared by integer value
//   - float64 vs int: collapsed integer floats compare equal
//   - bool: direct equality
//   - string: direct equality
//   - everything else: reflect.DeepEqual
func equalAny(got, want any) bool {
	if got == nil || want == nil {
		return got == want
	}
	switch w := want.(type) {
	case int:
		return toInt64(got) == int64(w)
	case int32:
		return toInt64(got) == int64(w)
	case int64:
		return toInt64(got) == w
	case float64:
		return toFloat64(got) == w
	case bool:
		gb, ok := got.(bool)
		return ok && gb == w
	case string:
		gs, ok := got.(string)
		return ok && gs == w
	}
	return reflect.DeepEqual(got, want)
}

func toInt64(v any) int64 {
	switch x := v.(type) {
	case int:
		return int64(x)
	case int32:
		return int64(x)
	case int64:
		return x
	case float64:
		return int64(x)
	case float32:
		return int64(x)
	}
	return 0
}

func toFloat64(v any) float64 {
	switch x := v.(type) {
	case float64:
		return x
	case float32:
		return float64(x)
	case int:
		return float64(x)
	case int32:
		return float64(x)
	case int64:
		return float64(x)
	}
	return 0
}
