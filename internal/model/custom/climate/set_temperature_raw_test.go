// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package climate

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// TestSetTemperatureRawSkipsRangeValidation verifies that SetTemperatureRaw
// does not return ErrTemperatureOutOfRange even when the value is outside
// the configured [MinTemp, MaxTemp] window.
func TestSetTemperatureRawSkipsRangeValidation(t *testing.T) {
	t.Parallel()
	w := &stubWriter{}
	caps := custom.ClimateCapabilities{
		MinTemperature: 5,
		MaxTemperature: 30,
	}
	r := newRig(t, "DEV:1", KindIP, w, caps)

	// Value outside the soft min/max — SetTemperature would reject this.
	// SetTemperatureRaw must succeed.
	const outOfRange = 4.5
	if err := r.climate.SetTemperatureRaw(context.Background(), outOfRange, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("SetTemperatureRaw(%v): unexpected error %v", outOfRange, err)
	}
}

// TestSetTemperatureVsRawBehaviour confirms that SetTemperature rejects the
// same value that SetTemperatureRaw accepts.
func TestSetTemperatureVsRawBehaviour(t *testing.T) {
	t.Parallel()
	w := &stubWriter{}
	caps := custom.ClimateCapabilities{
		MinTemperature: 5,
		MaxTemperature: 30,
	}
	r := newRig(t, "DEV:1", KindIP, w, caps)

	const outOfRange = 4.5
	// SetTemperature must reject the value.
	err := r.climate.SetTemperature(context.Background(), outOfRange, hmenum.CommandPriorityHigh)
	if !errors.Is(err, ErrTemperatureOutOfRange) {
		t.Fatalf("SetTemperature(%v): got %v, want ErrTemperatureOutOfRange", outOfRange, err)
	}
	// SetTemperatureRaw must accept it.
	if err := r.climate.SetTemperatureRaw(context.Background(), outOfRange, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("SetTemperatureRaw(%v): %v", outOfRange, err)
	}
}

// TestSetTemperatureRawDoesNotClampToCapabilityBounds verifies that
// SetTemperatureRaw writes the value as given, without silently clamping it
// to Capabilities.MinTemperature/MaxTemperature. That fallback is the
// last-resort bound MinTemp/MaxTemp fall back to when neither the
// operator-configured bound nor the setpoint descriptor is available — it
// is not a hardware-safe ceiling to re-apply after validation has already
// been bypassed, mirroring the reference's do_validate=False contract
// (climate.py set_temperature), which sends the value unmodified.
func TestSetTemperatureRawDoesNotClampToCapabilityBounds(t *testing.T) {
	t.Parallel()
	w := &stubWriter{}
	caps := custom.ClimateCapabilities{
		MinTemperature: 5,
		MaxTemperature: 30,
	}
	r := newRig(t, "DEV:1", KindIP, w, caps)

	// Below the capability fallback minimum — must reach the wire as-is.
	if err := r.climate.SetTemperatureRaw(context.Background(), 0, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("SetTemperatureRaw(0): %v", err)
	}
	got := w.last()
	if got.param != hmenum.ParameterSetPointTemperature {
		t.Fatalf("expected SETPOINT_TEMPERATURE, got %v", got.param)
	}
	if v, ok := got.value.(float64); !ok || v != 0 {
		t.Fatalf("expected unclamped value 0, got %v", got.value)
	}
}

// TestSetTemperatureRawNoWriterError verifies that SetTemperatureRaw returns
// an error when there is no setpoint DP and no writer.
func TestSetTemperatureRawNoWriterError(t *testing.T) {
	t.Parallel()
	c := &Climate{}
	err := c.SetTemperatureRaw(context.Background(), 20, hmenum.CommandPriorityHigh)
	if err == nil {
		t.Fatal("SetTemperatureRaw on empty Climate must return an error")
	}
}

// TestSetTemperatureAcceptsDescriptorMaxAboveCapabilityFallback pins a
// setpoint the device itself advertises as legal: every HmIP thermostat's
// SET_POINT_TEMPERATURE descriptor MAX is 30.5 °C, while
// Capabilities.MaxTemperature is only the 30.0 °C last-resort fallback
// MaxTemp() falls back to when neither the operator config nor the
// descriptor carries a bound. SetTemperature must write the
// descriptor-validated value unmodified — not silently round it down to
// the capability fallback, which HADiscoveryPayload never even advertises
// as the ceiling (MaxTemp() drives it).
func TestSetTemperatureAcceptsDescriptorMaxAboveCapabilityFallback(t *testing.T) {
	t.Parallel()
	w := &stubWriter{}
	const addr = "DEV0001:1"
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "DEV0001"})
	ch := d.AddChannel(addr, 1, "CLIMATE", hmenum.ParamsetKeyValues)
	sp := generic.NewFloat(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: addr,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(hmenum.ParameterSetPointTemperature),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeFloat,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent,
			Min:        json.RawMessage("4.5"),
			Max:        json.RawMessage("30.5"),
		},
		Writer: w,
	})
	ch.Put(sp)

	c := New(Config{
		Channel: ch,
		Writer:  w,
		Kind:    KindIP,
		// Mirrors ipCapabilities: the Go-only fallback (30.0) sits below
		// the real device's wire-descriptor MAX (30.5).
		Capabilities: custom.ClimateCapabilities{MinTemperature: 5.0, MaxTemperature: 30.0},
	})

	const wireMax = 30.5
	if got := c.MaxTemp(); got != wireMax {
		t.Fatalf("MaxTemp() = %v, want %v", got, wireMax)
	}
	if err := c.SetTemperature(context.Background(), wireMax, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("SetTemperature(%v): unexpected error %v", wireMax, err)
	}
	got := w.last()
	if v, ok := got.value.(float64); !ok || v != wireMax {
		t.Fatalf("wire value = %v, want %v unclamped", got.value, wireMax)
	}
}
