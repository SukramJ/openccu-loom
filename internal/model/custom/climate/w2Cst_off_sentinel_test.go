// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package climate

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// w2CstIPClimateWithSetpointMin builds a KindIP Climate whose
// SET_POINT_TEMPERATURE descriptor declares the given MIN — the value
// [Climate.MinTemp]'s second resolution step reads.
func w2CstIPClimateWithSetpointMin(t *testing.T, w custom.Writer, declaredMin float64, declared bool) *Climate {
	t.Helper()

	const addr = "0001D8A9B12345:1"
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "0001D8A9B12345"})
	ch := d.AddChannel(addr, 1, "HEATING_CLIMATECONTROL_TRANSCEIVER", hmenum.ParamsetKeyValues)

	desc := hmproto.ParameterData{
		Type:       hmenum.ParameterTypeFloat,
		Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent,
	}
	if declared {
		b, err := json.Marshal(declaredMin)
		if err != nil {
			t.Fatalf("marshal %v: %v", declaredMin, err)
		}
		desc.Min = b
	}
	ch.Put(generic.NewFloat(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: addr,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(hmenum.ParameterSetPointTemperature),
		},
		Descriptor: desc,
		Writer:     w,
	}))

	return New(Config{
		Channel:      ch,
		Writer:       w,
		Kind:         KindIP,
		Capabilities: custom.ClimateCapabilities{MinTemperature: 5.0, MaxTemperature: 30.5},
	})
}

// TestW2CstOffSentinelIsOneValueAcrossTheProfile crosses the three readers of
// the thermostat OFF sentinel, without naming the number.
//
// The rule is one: a setpoint of the sentinel means "off", not a selectable
// temperature. Three sites acted on it independently — the mode-OFF write put
// it on the wire, [Climate.MinTemp] bumped a resolved minimum equal to it by
// one step so the slider never presents the off state as a setpoint, and
// [Climate.temperatureForHeatMode] treated anything at or below it as "no
// usable setpoint". Move one and the others keep the old value: change the
// sentinel the mode-OFF write uses and MinTemp stops bumping, so the daemon
// writes an OFF value it simultaneously advertises as a selectable minimum.
//
// The check reads the sentinel out of the mode-OFF write and feeds it to the
// other two, so it is satisfied by any consistent value and bites on drift.
func TestW2CstOffSentinelIsOneValueAcrossTheProfile(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	// Reader 1: the mode-OFF write. Whatever it puts on
	// SET_POINT_TEMPERATURE is this profile's OFF sentinel.
	w := &putWriter{}
	c := w2CstIPClimateWithSetpointMin(t, w, 0, false)
	if err := c.setIPMode(ctx, ModeOff, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("setIPMode(OFF): %v", err)
	}
	if len(w.puts) != 1 {
		t.Fatalf("mode-OFF produced %d put_paramset calls, want 1", len(w.puts))
	}
	raw, ok := w.puts[0][string(hmenum.ParameterSetPointTemperature)]
	if !ok {
		t.Fatalf("mode-OFF wrote %v, with no SET_POINT_TEMPERATURE", w.puts[0])
	}
	sentinel, ok := raw.(float64)
	if !ok {
		t.Fatalf("mode-OFF wrote SET_POINT_TEMPERATURE as %T(%v), want a float64", raw, raw)
	}

	// Reader 2: MinTemp's bump. A descriptor minimum equal to the sentinel
	// must not be reported as a selectable minimum.
	bumped := w2CstIPClimateWithSetpointMin(t, &putWriter{}, sentinel, true)
	if got := bumped.MinTemp(); got <= sentinel {
		t.Errorf("the mode-OFF write puts %v on SET_POINT_TEMPERATURE, but MinTemp() reports %v for a device declaring that same value as its descriptor minimum — the OFF sentinel is being advertised as a selectable setpoint",
			sentinel, got)
	}

	// A minimum above the sentinel is passed through untouched, so the
	// bump cannot be satisfied by adding a step unconditionally.
	plain := w2CstIPClimateWithSetpointMin(t, &putWriter{}, sentinel+2, true)
	if got := plain.MinTemp(); got != sentinel+2 {
		t.Errorf("MinTemp() = %v for a descriptor minimum of %v — only a minimum equal to the OFF sentinel may be bumped", got, sentinel+2)
	}

	// Reader 3: the OFF→HEAT transition. A setpoint at the sentinel is not
	// a usable heat setpoint, so the returned value must sit above it.
	heat := w2CstIPClimateWithSetpointMin(t, &putWriter{}, 0, false)
	heat.mu.Lock()
	heat.oldManuSetpoint, heat.hasOldManuSetpoint = sentinel, true
	heat.mu.Unlock()
	if got := heat.temperatureForHeatMode(); got <= sentinel {
		t.Errorf("temperatureForHeatMode() = %v with the last setpoint at the OFF sentinel %v — an OFF→HEAT transition would write the OFF value back",
			got, sentinel)
	}
}
