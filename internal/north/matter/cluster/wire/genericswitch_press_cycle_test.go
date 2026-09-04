// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package wire_test

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/internal/north/matter/cluster/wire"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
	"github.com/SukramJ/openccu-loom/pkg/matterport"
)

// The press-cycle parity case lives in the external test package: it
// drives the wire cluster from a model-layer button group, and
// internal/model/generic imports this package for the OnOff device-type
// stubs, so an in-package test file would close an import cycle.

// pressCycleRecorder records the event IDs the GenericSwitch emits, in
// order, so the parity cases below can assert the full gesture sequence.
type pressCycleRecorder struct {
	events []uint32
}

func (r *pressCycleRecorder) MatterEmitEvent(_ uint16, _, event uint32, _ any, _ matterport.EventPriority) {
	r.events = append(r.events, event)
}

// TestParityMatterJS_GenericSwitchPressCycleSequences pins the
// end-to-end press-cycle event ordering (model button group → wire
// cluster → emitter) against matter.js
// packages/node/src/behaviors/switch/SwitchServer.ts:
//
//   - a short press yields InitialPress then ShortRelease
//     (#handleSwitchPositionChange: initialPress on the move away from
//     momentaryNeutralPosition; shortRelease on return while the
//     longPress timer is still running);
//   - a hold yields InitialPress, then exactly ONE LongPress
//     (#handleLongPress fires once per hold), then LongRelease on the
//     return to neutral — device-side repeats (PRESS_CONT frames)
//     produce no extra events;
//   - LongRelease is only ever emitted after a LongPress since the
//     previous InitialPress (the `currentIsLongPress` gate), so a
//     button without a release parameter must complete the full
//     sequence per long press.
func TestParityMatterJS_GenericSwitchPressCycleSequences(t *testing.T) {
	t.Parallel()

	pressDP := func(p hmenum.Parameter) *generic.Button {
		return generic.NewButton(generic.Spec{
			Key: hmtypes.DataPointKey{
				InterfaceID:    "iface",
				ChannelAddress: "BTN0001:1",
				ParamsetKey:    hmenum.ParamsetKeyValues,
				Parameter:      string(p),
			},
			Descriptor: hmproto.ParameterData{
				Type:       hmenum.ParameterTypeAction,
				Operations: hmenum.OperationsEvent,
			},
		})
	}
	assertSeq := func(t *testing.T, rec *pressCycleRecorder, want ...uint32) {
		t.Helper()
		if len(rec.events) != len(want) {
			t.Fatalf("event id sequence = %v, want %v", rec.events, want)
		}
		for i := range want {
			if rec.events[i] != want[i] {
				t.Fatalf("event id sequence = %v, want %v", rec.events, want)
			}
		}
	}
	wireUp := func(t *testing.T, buttons ...*generic.Button) *pressCycleRecorder {
		t.Helper()
		srcs := make([]generic.PressEventSource, 0, len(buttons))
		for _, b := range buttons {
			srcs = append(srcs, b)
		}
		group := generic.NewButtonGroup(srcs...)
		if group == nil {
			t.Fatal("button group did not construct")
		}
		gs := wire.NewGenericSwitch(7, group)
		rec := &pressCycleRecorder{}
		gs.SetMatterEventEmitter(rec)
		t.Cleanup(group.WireMatterSwitchHandler(gs))
		return rec
	}

	t.Run("short_press", func(t *testing.T) {
		t.Parallel()
		short := pressDP(hmenum.ParameterPressShort)
		rec := wireUp(t, short, pressDP(hmenum.ParameterPressLong))
		short.OnEvent(true)
		assertSeq(t, rec, wire.MatterEventInitialPress, wire.MatterEventShortRelease)
	})

	t.Run("hold_with_release", func(t *testing.T) {
		t.Parallel()
		long := pressDP(hmenum.ParameterPressLong)
		cont := pressDP(hmenum.ParameterPressCont)
		release := pressDP(hmenum.ParameterPressLongRelease)
		rec := wireUp(t, long, cont, release)
		long.OnEvent(true)
		cont.OnEvent(true) // ~300 ms continuation repeats → suppressed
		cont.OnEvent(true)
		release.OnEvent(true)
		assertSeq(t, rec, wire.MatterEventInitialPress, wire.MatterEventLongPress, wire.MatterEventLongRelease)
	})

	t.Run("hold_without_release_parameter", func(t *testing.T) {
		t.Parallel()
		long := pressDP(hmenum.ParameterPressLong)
		rec := wireUp(t, pressDP(hmenum.ParameterPressShort), long)
		long.OnEvent(true)
		assertSeq(t, rec, wire.MatterEventInitialPress, wire.MatterEventLongPress, wire.MatterEventLongRelease)
	})
}
