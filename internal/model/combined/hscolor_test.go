// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package combined

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// errWriter is a Writer that always returns an error.
type errWriter struct{ err error }

func (e *errWriter) SetValue(_ context.Context, _ string, _ hmenum.Parameter, _ any, _ hmenum.CommandPriority) error {
	return e.err
}

// TestHSColorSaturationMultiplierRoundtrip verifies that a CCU-side
// saturation fraction (0.0–1.0) is exposed as a percentage (0–100) on
// read, and that a write converts the percentage back to a fraction
// before handing it to the Writer (÷100).
func TestHSColorSaturationMultiplierRoundtrip(t *testing.T) {
	t.Parallel()
	w := &stubWriter{}
	c := NewHSColor("addr:3", w, hmenum.ParameterHue, hmenum.ParameterSaturation)

	// CCU pushes 0.75 fraction — should appear as 75 % in Value().
	c.OnHue(180)
	c.OnSaturation(0.75)

	hs, ok := c.Value()
	if !ok {
		t.Fatal("value not observed")
	}
	if hs.Saturation != 75 {
		t.Fatalf("read saturation: got %v, want 75", hs.Saturation)
	}

	// Set with 75 % saturation — writer must receive 0.75 fraction.
	if err := c.SetColor(context.Background(), HS{Hue: 180, Saturation: 75}, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("SetColor: %v", err)
	}
	v, ok := w.find(hmenum.ParameterSaturation)
	if !ok {
		t.Fatal("SATURATION not written")
	}
	if v.(float64) != 0.75 {
		t.Fatalf("wire saturation: got %v, want 0.75", v)
	}
}

// TestHSColorEdgeValuesZero confirms that hue=0 and saturation=0.0
// are valid observations and produce (0, 0) rather than treating zero
// as "unobserved".
func TestHSColorEdgeValuesZero(t *testing.T) {
	t.Parallel()
	c := NewHSColor("addr:1", &stubWriter{}, hmenum.ParameterHue, hmenum.ParameterSaturation)
	c.OnHue(0)
	c.OnSaturation(0)
	hs, ok := c.Value()
	if !ok {
		t.Fatal("value not observed after hue=0 / sat=0")
	}
	if hs.Hue != 0 || hs.Saturation != 0 {
		t.Fatalf("expected (0, 0), got %+v", hs)
	}
}

// TestHSColorEdgeValuesMax confirms that hue=359 and saturation=1.0
// are represented as (359, 100) in the consumer-facing form.
func TestHSColorEdgeValuesMax(t *testing.T) {
	t.Parallel()
	c := NewHSColor("addr:1", &stubWriter{}, hmenum.ParameterHue, hmenum.ParameterSaturation)
	c.OnHue(359)
	c.OnSaturation(1.0)
	hs, ok := c.Value()
	if !ok {
		t.Fatal("value not observed")
	}
	if hs.Hue != 359 || hs.Saturation != 100 {
		t.Fatalf("expected (359, 100), got %+v", hs)
	}
}

// TestHSColorHueWrapsModulo360 verifies that hue values outside [0,359]
// are normalised by wrapping (same semantics as the Python reference).
func TestHSColorHueWrapsModulo360(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   int32
		want int32
	}{
		{360, 0},
		{361, 1},
		{-1, 359},
		{720, 0},
		{400, 40},
	}
	for _, tc := range cases {
		c := NewHSColor("x", &stubWriter{}, hmenum.ParameterHue, hmenum.ParameterSaturation)
		c.OnHue(tc.in)
		c.OnSaturation(0.5)
		hs, ok := c.Value()
		if !ok {
			t.Fatalf("in=%d: not observed", tc.in)
		}
		if hs.Hue != tc.want {
			t.Errorf("in=%d: got hue=%d, want %d", tc.in, hs.Hue, tc.want)
		}
	}
}

// TestHSColorSaturationClamped confirms that out-of-range saturation
// values pushed from the CCU are clamped to [0.0, 1.0].
func TestHSColorSaturationClamped(t *testing.T) {
	t.Parallel()
	c := NewHSColor("x", &stubWriter{}, hmenum.ParameterHue, hmenum.ParameterSaturation)
	c.OnHue(90)
	c.OnSaturation(1.5) // > 1.0 — must be clamped to 1.0 → 100 %
	hs, ok := c.Value()
	if !ok || hs.Saturation != 100 {
		t.Fatalf("expected saturation=100, got %v (ok=%v)", hs.Saturation, ok)
	}

	c2 := NewHSColor("x", &stubWriter{}, hmenum.ParameterHue, hmenum.ParameterSaturation)
	c2.OnHue(90)
	c2.OnSaturation(-0.5) // < 0.0 — must be clamped to 0.0 → 0 %
	hs2, ok2 := c2.Value()
	if !ok2 || hs2.Saturation != 0 {
		t.Fatalf("expected saturation=0, got %v (ok=%v)", hs2.Saturation, ok2)
	}
}

// TestHSColorOnUpdateNoFireOnIdenticalValue verifies that the callback
// is suppressed when both hue and saturation remain unchanged.
func TestHSColorOnUpdateNoFireOnIdenticalValue(t *testing.T) {
	t.Parallel()
	c := NewHSColor("x", &stubWriter{}, hmenum.ParameterHue, hmenum.ParameterSaturation)
	c.OnHue(120)
	c.OnSaturation(0.5) // first observation → fires

	var count atomic.Int32
	c.OnUpdate(func(_, _ HS) { count.Add(1) })

	c.OnHue(120)        // same → no fire
	c.OnSaturation(0.5) // same → no fire
	if n := count.Load(); n != 0 {
		t.Fatalf("callback fired %d times, want 0 on identical updates", n)
	}
}

// TestHSColorUnsubscribeIsIdempotent verifies that calling the returned
// unsubscribe closure more than once is safe and stops future callbacks.
func TestHSColorUnsubscribeIsIdempotent(t *testing.T) {
	t.Parallel()
	c := NewHSColor("x", &stubWriter{}, hmenum.ParameterHue, hmenum.ParameterSaturation)

	var fired atomic.Int32
	unsub := c.OnUpdate(func(_, _ HS) { fired.Add(1) })

	// Unsubscribe twice — must not panic.
	unsub()
	unsub()

	c.OnHue(45)
	c.OnSaturation(0.3)
	if n := fired.Load(); n != 0 {
		t.Fatalf("callback fired %d times after unsubscribe", n)
	}
}

// TestHSColorSetColorWriterError ensures that errors from the Writer are
// propagated and wrapped correctly for both hue and saturation failures.
func TestHSColorSetColorWriterError(t *testing.T) {
	t.Parallel()
	sentinel := errors.New("write failed")
	ew := &errWriter{err: sentinel}
	c := NewHSColor("x", ew, hmenum.ParameterHue, hmenum.ParameterSaturation)

	err := c.SetColor(context.Background(), HS{Hue: 100, Saturation: 50}, hmenum.CommandPriorityHigh)
	if err == nil {
		t.Fatal("expected error from writer")
	}
	if !errors.Is(err, sentinel) {
		t.Fatalf("error chain does not contain sentinel: %v", err)
	}
}

// TestHSColorSetColorClampsAndWraps is an integration-style check that
// both the hue wrap and saturation clamp applied during SetColor are
// reflected in what the Writer receives.
func TestHSColorSetColorClampsAndWraps(t *testing.T) {
	t.Parallel()
	w := &stubWriter{}
	c := NewHSColor("x", w, hmenum.ParameterHue, hmenum.ParameterSaturation)

	// Hue 370 → wraps to 10; saturation 0 % → 0.0 on wire.
	if err := c.SetColor(context.Background(), HS{Hue: 370, Saturation: 0}, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("SetColor: %v", err)
	}
	if v, ok := w.find(hmenum.ParameterHue); !ok || v.(int32) != 10 {
		t.Errorf("hue: got %v, want 10", v)
	}
	if v, ok := w.find(hmenum.ParameterSaturation); !ok || v.(float64) != 0.0 {
		t.Errorf("sat wire: got %v, want 0.0", v)
	}
}

// IsRefreshed / StateUncertain on HSColor

func TestHSColorIsRefreshedRequiresBothInputs(t *testing.T) {
	t.Parallel()
	c := NewHSColor("x", &stubWriter{}, hmenum.ParameterHue, hmenum.ParameterSaturation)
	if c.IsRefreshed() {
		t.Fatal("IsRefreshed must be false before any input")
	}
	c.OnHue(180)
	if c.IsRefreshed() {
		t.Fatal("IsRefreshed must be false after only Hue")
	}
	c.OnSaturation(0.5)
	if !c.IsRefreshed() {
		t.Fatal("IsRefreshed must be true after both inputs")
	}
}

func TestHSColorStateUncertainAlwaysFalse(t *testing.T) {
	t.Parallel()
	c := NewHSColor("x", &stubWriter{}, hmenum.ParameterHue, hmenum.ParameterSaturation)
	if c.StateUncertain() {
		t.Fatal("StateUncertain must always be false for HSColor")
	}
}
