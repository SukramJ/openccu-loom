// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package climate

import (
	"context"
	"errors"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
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

// TestSetTemperatureRawClampsToCapabilityBounds verifies that SetTemperatureRaw
// still clamps the value to the capability envelope even though it skips the
// soft range check. A value below Capabilities.MinTemperature is clamped to
// MinTemperature before hitting the wire.
func TestSetTemperatureRawClampsToCapabilityBounds(t *testing.T) {
	t.Parallel()
	w := &stubWriter{}
	caps := custom.ClimateCapabilities{
		MinTemperature: 5,
		MaxTemperature: 30,
	}
	r := newRig(t, "DEV:1", KindIP, w, caps)

	// Below the absolute hardware minimum — must be clamped to 5.
	if err := r.climate.SetTemperatureRaw(context.Background(), 0, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("SetTemperatureRaw(0): %v", err)
	}
	got := w.last()
	if got.param != hmenum.ParameterSetPointTemperature {
		t.Fatalf("expected SETPOINT_TEMPERATURE, got %v", got.param)
	}
	if v, ok := got.value.(float64); !ok || v != 5 {
		t.Fatalf("expected clamped value 5, got %v", got.value)
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
