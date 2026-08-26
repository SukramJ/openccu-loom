// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// Tests for the MinTemp / MaxTemp read-chain (M2 fix).
//
// Resolution order
// 1. TEMPERATURE_MINIMUM / TEMPERATURE_MAXIMUM operator-configured value
// 2. Setpoint descriptor MIN / MAX
// 3. ClimateCapabilities static fallback
package climate

import (
	"encoding/json"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// buildMinMaxRig constructs a Climate with all three resolution-chain
// sources wired: a TEMPERATURE_MINIMUM/MAXIMUM DP, a setpoint DP
// with configurable descriptor bounds, and ClimateCapabilities fallbacks.
type minMaxRig struct {
	climate *Climate
	tempMin *generic.Float
	tempMax *generic.Float
}

func newMinMaxRig(
	t *testing.T,
	descMin, descMax float64, // setpoint descriptor bounds (0 = omit)
	fallbackMin, fallbackMax float64, // ClimateCapabilities fallback
) *minMaxRig {
	t.Helper()
	address := "MMR:1"
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "MMR0001"})
	ch := d.AddChannel(address, 1, "CLIMATE", hmenum.ParamsetKeyValues)

	// Build setpoint descriptor with optional MIN/MAX.
	var rawMin, rawMax json.RawMessage
	if descMin != 0 {
		b, _ := json.Marshal(descMin)
		rawMin = b
	}
	if descMax != 0 {
		b, _ := json.Marshal(descMax)
		rawMax = b
	}

	setpoint := generic.NewFloat(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: address,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(hmenum.ParameterSetPointTemperature),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeFloat,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent,
			Min:        rawMin,
			Max:        rawMax,
		},
		Writer: &stubWriter{},
	})
	ch.Put(setpoint)

	// Wire TEMPERATURE_MINIMUM DP.
	tempMin := generic.NewFloat(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: address,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(hmenum.ParameterTemperatureMinimum),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeFloat,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent,
		},
		Writer: &stubWriter{},
	})
	ch.Put(tempMin)

	// Wire TEMPERATURE_MAXIMUM DP.
	tempMax := generic.NewFloat(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: address,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(hmenum.ParameterTemperatureMaximum),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeFloat,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent,
		},
		Writer: &stubWriter{},
	})
	ch.Put(tempMax)

	caps := custom.ClimateCapabilities{
		MinTemperature:  fallbackMin,
		MaxTemperature:  fallbackMax,
		TemperatureStep: 0.5,
	}
	c := New(Config{Channel: ch, Writer: &stubWriter{}, Capabilities: caps, Kind: KindIP})

	return &minMaxRig{
		climate: c,
		tempMin: tempMin,
		tempMax: tempMax,
	}
}

func TestMinTemp(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		descMin      float64  // setpoint descriptor MIN (0 = omit)
		tempMinValue *float64 // TEMPERATURE_MINIMUM observed value (nil = unobserved)
		want         float64
	}{
		{
			name:         "no_operator_no_descriptor_returns_fallback",
			descMin:      0,
			tempMinValue: nil,
			// Capabilities fallback (4.5) equals _OFF_TEMPERATURE → +step → 5.0
			want: 5.0,
		},
		{
			name:         "no_operator_descriptor_8_returns_descriptor",
			descMin:      8.0,
			tempMinValue: nil,
			want:         8.0, // step 2: descriptor MIN
		},
		{
			name:         "operator_12_5_wins_over_descriptor_8",
			descMin:      8.0,
			tempMinValue: new(12.5),
			want:         12.5, // step 1: operator override
		},
		{
			name: "operator_0_is_unobserved_falls_back_to_descriptor",
			// In Python, value=None → unset. In Go, (0, false) from Value()
			// means unobserved. We never call OnEvent so the DP stays at
			// (0, false) — indistinguishable from the 0 value, so the
			// read-chain correctly skips to step 2.
			descMin:      8.0,
			tempMinValue: nil, // not observed — represents "unset"
			want:         8.0,
		},
		{
			// default step (0.5) when the resolved minimum equals the
			// HmIP "off" temperature (4.5 °C) so HA's slider doesn't
			// expose the off-state as a normal setpoint. Mirror it 1:1.
			name:         "off_temperature_in_operator_steps_up",
			descMin:      0,
			tempMinValue: new(4.5),
			want:         5.0,
		},
		{
			name:         "off_temperature_in_descriptor_steps_up",
			descMin:      4.5,
			tempMinValue: nil,
			want:         5.0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := newMinMaxRig(t, tc.descMin, 30.5, 4.5, 30.5)
			if tc.tempMinValue != nil {
				r.tempMin.OnEvent(*tc.tempMinValue)
			}
			got := r.climate.MinTemp()
			if got != tc.want {
				t.Fatalf("MinTemp()=%.2f, want=%.2f", got, tc.want)
			}
		})
	}
}

func TestMaxTemp(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		descMax      float64  // setpoint descriptor MAX (0 = omit)
		tempMaxValue *float64 // TEMPERATURE_MAXIMUM observed value (nil = unobserved)
		want         float64
	}{
		{
			name:         "no_operator_no_descriptor_returns_fallback",
			descMax:      0,
			tempMaxValue: nil,
			want:         30.5, // ClimateCapabilities fallback
		},
		{
			name:         "no_operator_descriptor_28_returns_descriptor",
			descMax:      28.0,
			tempMaxValue: nil,
			want:         28.0, // step 2: descriptor MAX
		},
		{
			name:         "operator_35_wins_over_descriptor_28",
			descMax:      28.0,
			tempMaxValue: new(35.0),
			want:         35.0, // step 1: operator override
		},
		{
			name:         "operator_unobserved_falls_back_to_descriptor",
			descMax:      28.0,
			tempMaxValue: nil,
			want:         28.0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := newMinMaxRig(t, 4.5, tc.descMax, 4.5, 30.5)
			if tc.tempMaxValue != nil {
				r.tempMax.OnEvent(*tc.tempMaxValue)
			}
			got := r.climate.MaxTemp()
			if got != tc.want {
				t.Fatalf("MaxTemp()=%.2f, want=%.2f", got, tc.want)
			}
		})
	}
}

// TestClimateMinMaxTempReadChainHmIPBWTH verifies that when a
// TEMPERATURE_MINIMUM/MAXIMUM DP carries an observed CCU value, MinTemp()
// and MaxTemp() return those values (step 1 of the read-chain) rather
// than the setpoint-descriptor defaults (step 2).
//
// Scenario: HmIP-BWTH with TEMPERATURE_MINIMUM=5.0 and
// TEMPERATURE_MAXIMUM=30.0 observed, setpoint descriptor bounds 4.5/30.5.
// Expected: MinTemp()=5.0, MaxTemp()=30.0 — not the descriptor defaults.
func TestClimateMinMaxTempReadChainHmIPBWTH(t *testing.T) {
	t.Parallel()

	// Setpoint descriptor: 4.5/30.5 (typical BWTH defaults from CCU).
	// TEMPERATURE_MINIMUM observed value: 5.0 (operator-configured).
	// TEMPERATURE_MAXIMUM observed value: 30.0 (operator-configured).
	r := newMinMaxRig(t, 4.5, 30.5, 4.5, 30.5)
	r.tempMin.OnEvent(5.0)
	r.tempMax.OnEvent(30.0)

	if got := r.climate.MinTemp(); got != 5.0 {
		t.Errorf("MinTemp()=%.2f, want 5.00 (TEMPERATURE_MINIMUM observed value wins over setpoint descriptor 4.5)", got)
	}
	if got := r.climate.MaxTemp(); got != 30.0 {
		t.Errorf("MaxTemp()=%.2f, want 30.00 (TEMPERATURE_MAXIMUM observed value wins over setpoint descriptor 30.5)", got)
	}
}
