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

// TestSetTemperatureRejectsBelow verifies that a value below MinTemp returns
// ErrTemperatureOutOfRange.
func TestSetTemperatureRejectsBelow(t *testing.T) {
	t.Parallel()

	w := &stubWriter{}
	r := newRig(t, "HmIP-BWTH:1", KindIP, w, custom.ClimateCapabilities{
		MinTemperature: 5.0,
		MaxTemperature: 30.0,
	})

	err := r.climate.SetTemperature(context.Background(), 1.0, hmenum.CommandPriorityHigh)
	if !errors.Is(err, ErrTemperatureOutOfRange) {
		t.Fatalf("SetTemperature(1.0) err = %v, want ErrTemperatureOutOfRange", err)
	}
}

// TestSetTemperatureRejectsAbove verifies that a value above MaxTemp returns
// ErrTemperatureOutOfRange.
func TestSetTemperatureRejectsAbove(t *testing.T) {
	t.Parallel()

	w := &stubWriter{}
	r := newRig(t, "HmIP-BWTH:1", KindIP, w, custom.ClimateCapabilities{
		MinTemperature: 5.0,
		MaxTemperature: 30.0,
	})

	err := r.climate.SetTemperature(context.Background(), 35.0, hmenum.CommandPriorityHigh)
	if !errors.Is(err, ErrTemperatureOutOfRange) {
		t.Fatalf("SetTemperature(35.0) err = %v, want ErrTemperatureOutOfRange", err)
	}
}

// TestSetTemperatureAcceptsInRange verifies that a value within [MinTemp,MaxTemp]
// succeeds.
func TestSetTemperatureAcceptsInRange(t *testing.T) {
	t.Parallel()

	w := &stubWriter{}
	r := newRig(t, "HmIP-BWTH:1", KindIP, w, custom.ClimateCapabilities{
		MinTemperature: 5.0,
		MaxTemperature: 30.0,
	})

	if err := r.climate.SetTemperature(context.Background(), 20.0, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("SetTemperature(20.0) err = %v, want nil", err)
	}
}

// TestSetTemperatureBoundaryAtMinExact verifies that the exact MinTemp value
// is accepted (boundary inclusive).
func TestSetTemperatureBoundaryAtMinExact(t *testing.T) {
	t.Parallel()

	w := &stubWriter{}
	r := newRig(t, "HmIP-BWTH:1", KindIP, w, custom.ClimateCapabilities{
		MinTemperature: 5.0,
		MaxTemperature: 30.0,
	})

	if err := r.climate.SetTemperature(context.Background(), 5.0, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("SetTemperature at MinTemp boundary: %v", err)
	}
}

// TestSetTemperatureBoundaryAtMaxExact verifies that the exact MaxTemp value
// is accepted (boundary inclusive).
func TestSetTemperatureBoundaryAtMaxExact(t *testing.T) {
	t.Parallel()

	w := &stubWriter{}
	r := newRig(t, "HmIP-BWTH:1", KindIP, w, custom.ClimateCapabilities{
		MinTemperature: 5.0,
		MaxTemperature: 30.0,
	})

	if err := r.climate.SetTemperature(context.Background(), 30.0, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("SetTemperature at MaxTemp boundary: %v", err)
	}
}

// TestClimateBaseDPMethodsExist verifies that Climate exposes the baseDP
// delegation methods without panicking.
func TestClimateBaseDPMethodsExist(t *testing.T) {
	t.Parallel()

	w := &stubWriter{}
	r := newRig(t, "HmIP-BWTH:1", KindIP, w, custom.ClimateCapabilities{})

	// Must compile and return zero values before any event.
	_, _ = r.climate.ModifiedAt()
	_, _ = r.climate.RefreshedAt()
	_ = r.climate.UnconfirmedLastValuesSend()

	r.climate.MarkModified()
	r.climate.MarkRefreshed()

	if _, ok := r.climate.ModifiedAt(); !ok {
		t.Error("ModifiedAt() must be non-zero after MarkModified()")
	}
	if _, ok := r.climate.RefreshedAt(); !ok {
		t.Error("RefreshedAt() must be non-zero after MarkRefreshed()")
	}
}
