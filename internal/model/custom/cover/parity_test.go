// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// Parity tests for the Cover custom data point. Each test function maps to
// one semantic from the Python reference.

package cover

import (
	"context"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// TestParityCoverOpenWritesLevel1 verifies that Open() writes LEVEL=1.0
// (fully open). Mirrors test_cecover → "open() → LEVEL=_OPEN_LEVEL".
func TestParityCoverOpenWritesLevel1(t *testing.T) {
	t.Parallel()

	w := &stubWriter{}
	c, _, _ := newRig(t, "VCU8537918:4", w, custom.CoverCapabilities{})
	if err := c.Open(context.Background(), hmenum.CommandPriorityHigh); err != nil {
		t.Fatal(err)
	}
	if v, ok := w.last.(float64); !ok || v != 1.0 {
		t.Errorf("Open() wrote %v, want 1.0", w.last)
	}
}

// TestParityCoverCloseWritesLevel0 verifies that Close() writes LEVEL=0.0
// (fully closed). Mirrors test_cecover → "close() → LEVEL=_CLOSED_LEVEL".
func TestParityCoverCloseWritesLevel0(t *testing.T) {
	t.Parallel()

	w := &stubWriter{}
	c, _, _ := newRig(t, "VCU8537918:4", w, custom.CoverCapabilities{})
	if err := c.Close(context.Background(), hmenum.CommandPriorityHigh); err != nil {
		t.Fatal(err)
	}
	if v, ok := w.last.(float64); !ok || v != 0.0 {
		t.Errorf("Close() wrote %v, want 0.0", w.last)
	}
}

// TestParityCoverSetPosition81Pct verifies that SetPosition(0.81) writes
// LEVEL=0.81 and Position().OpenFraction()==81. Mirrors test_cecover →
// "set_position(81) → LEVEL=0.81, current_position==81".
func TestParityCoverSetPosition81Pct(t *testing.T) {
	t.Parallel()

	w := &stubWriter{}
	c, _, level := newRig(t, "VCU8537918:4", w, custom.CoverCapabilities{})
	if err := c.SetPosition(context.Background(), 0.81, hmenum.CommandPriorityHigh); err != nil {
		t.Fatal(err)
	}
	if v, ok := w.last.(float64); !ok || v != 0.81 {
		t.Errorf("SetPosition(0.81) wrote %v, want 0.81", w.last)
	}
	level.OnEvent(0.81)
	p, ok := c.Position()
	if !ok {
		t.Fatal("Position() ok=false after OnEvent")
	}
	if p.OpenFraction() != 81 {
		t.Errorf("Position().OpenFraction()=%d, want 81", p.OpenFraction())
	}
}

// TestParityCoverIsClosedWhenLevel0 verifies IsClosed returns true when
// LEVEL=0 and false otherwise. Mirrors test_cecover → is_closed checks.
func TestParityCoverIsClosedWhenLevel0(t *testing.T) {
	t.Parallel()

	cases := []struct {
		level  float64
		closed bool
	}{
		{0.0, true},
		{0.01, false},
		{0.5, false},
		{1.0, false},
	}
	for _, tc := range cases {
		c, _, level := newRig(t, "x", &stubWriter{}, custom.CoverCapabilities{})
		level.OnEvent(tc.level)
		if got := c.IsClosed(); got != tc.closed {
			t.Errorf("level=%v: IsClosed()=%v, want %v", tc.level, got, tc.closed)
		}
	}
}

// TestParityCoverIsOpeningIsClosingFromDirection verifies that direction
// events translate to correct IsOpening/IsClosing booleans. Mirrors
// test_cecover → "ACTIVITY_STATE=1 → is_opening=True".
func TestParityCoverIsOpeningIsClosingFromDirection(t *testing.T) {
	t.Parallel()

	cases := []struct {
		dir     CoverDirection
		opening bool
		closing bool
	}{
		{DirectionUp, true, false},
		{DirectionDown, false, true},
		{DirectionNone, false, false},
	}
	for _, tc := range cases {
		c, _, _ := newRig(t, "x", &stubWriter{}, custom.CoverCapabilities{})
		c.OnDirection(tc.dir)
		if c.IsOpening() != tc.opening {
			t.Errorf("dir=%v: IsOpening()=%v, want %v", tc.dir, c.IsOpening(), tc.opening)
		}
		if c.IsClosing() != tc.closing {
			t.Errorf("dir=%v: IsClosing()=%v, want %v", tc.dir, c.IsClosing(), tc.closing)
		}
	}
}

// TestParityCoverInvertedSetPosition verifies that InvertedControl flips the
// wire-level but domain Position stays user-facing. Mirrors
// test_cecover → inverted cover set_position checks.
func TestParityCoverInvertedSetPosition(t *testing.T) {
	t.Parallel()

	w := &stubWriter{}
	c, _, _ := newRig(t, "x", w, custom.CoverCapabilities{InvertedControl: true})
	if err := c.SetPosition(context.Background(), 0.25, hmenum.CommandPriorityHigh); err != nil {
		t.Fatal(err)
	}
	if v, ok := w.last.(float64); !ok || v != 0.75 {
		t.Errorf("inverted SetPosition(0.25) wrote %v, want 0.75", w.last)
	}
}

// TestParityCoverInvertedOnLevel verifies that an inverted cover inverts the
// CCU LEVEL on ingestion too. Mirrors test_cecover → OnLevelInverted.
func TestParityCoverInvertedOnLevel(t *testing.T) {
	t.Parallel()

	c, _, _ := newRig(t, "x", &stubWriter{}, custom.CoverCapabilities{InvertedControl: true})
	c.OnLevel(0.8)
	p, ok := c.Position()
	if !ok {
		t.Fatal("Position() ok=false")
	}
	// 1 - 0.8 = 0.2 with floating-point tolerance.
	if p.Level() < 0.19 || p.Level() > 0.21 {
		t.Errorf("inverted OnLevel(0.8): Position=%v, want ~0.2", p.Level())
	}
}

// TestParityCoverStopWithoutCapabilityIsNoOp verifies Stop does nothing when
// the SupportsStop capability is absent. Mirrors test_cecover → stop no-op.
func TestParityCoverStopWithoutCapabilityIsNoOp(t *testing.T) {
	t.Parallel()

	w := &stubWriter{}
	c, _, _ := newRig(t, "x", w, custom.CoverCapabilities{})
	if err := c.Stop(context.Background(), hmenum.CommandPriorityHigh); err != nil {
		t.Fatal(err)
	}
	if w.last != nil {
		t.Error("Stop without SupportsStop must be a no-op")
	}
}

// TestParityCoverStopAlwaysCritical verifies the CRITICAL-priority invariant
// on Stop.
func TestParityCoverStopAlwaysCritical(t *testing.T) {
	t.Parallel()

	w := &priorityWriter{}
	c, _, _ := newRig(t, "x", w, custom.CoverCapabilities{SupportsStop: true})
	_ = c.Stop(context.Background(), hmenum.CommandPriorityHigh)
	if len(w.priorities) != 1 {
		t.Fatalf("expected 1 SetValue call, got %d", len(w.priorities))
	}
	if w.priorities[0] != hmenum.CommandPriorityCritical {
		t.Errorf("Stop priority=%v, want CommandPriorityCritical", w.priorities[0])
	}
}

// TestParityCoverIsRefreshed verifies IsRefreshed returns false before and
// true after a CCU event.
func TestParityCoverIsRefreshed(t *testing.T) {
	t.Parallel()

	c, _, level := newRig(t, "x", &stubWriter{}, custom.CoverCapabilities{})
	if c.IsRefreshed() {
		t.Error("IsRefreshed() must be false before any wire event")
	}
	level.OnEvent(0.5)
	if !c.IsRefreshed() {
		t.Error("IsRefreshed() must be true after OnEvent")
	}
}

// TestParityCoverAddressRoundtrip verifies Address() returns the construction
// address.
func TestParityCoverAddressRoundtrip(t *testing.T) {
	t.Parallel()

	const addr = "VCU8537918:4"
	c, _, _ := newRig(t, addr, &stubWriter{}, custom.CoverCapabilities{})
	if got := c.Address(); got != addr {
		t.Errorf("Address()=%q, want %q", got, addr)
	}
}

// TestParityCoverVariantShutterDefault verifies that a cover constructed
// without an explicit variant reports VariantShutter as the zero value.
func TestParityCoverVariantShutterDefault(t *testing.T) {
	t.Parallel()

	c, _, _ := newRig(t, "x", &stubWriter{}, custom.CoverCapabilities{})
	if c.Variant != VariantShutter {
		t.Errorf("default Variant=%v, want VariantShutter", c.Variant)
	}
}

// TestParityVariantString verifies the string representation of each
// variant.
func TestParityVariantString(t *testing.T) {
	t.Parallel()

	cases := []struct {
		v    CoverVariant
		want string
	}{
		{VariantShutter, "shutter"},
		{VariantBlind, "blind"},
		{VariantAwning, "awning"},
		{VariantCurtain, "curtain"},
		{VariantDamper, "damper"},
		{VariantShade, "shade"},
		{VariantWindow, "window"},
		{VariantGarage, "garage"},
	}
	for _, tc := range cases {
		if got := VariantString(tc.v); got != tc.want {
			t.Errorf("VariantString(%v)=%q, want %q", tc.v, got, tc.want)
		}
	}
}

// TestParityCoverInvertedDirectionSwapped verifies that inverted covers
// swap opening/closing direction semantics. Mirrors test_cecover →
// inverted direction checks.
func TestParityCoverInvertedDirectionSwapped(t *testing.T) {
	t.Parallel()

	c, _, _ := newRig(t, "x", &stubWriter{}, custom.CoverCapabilities{InvertedControl: true})
	c.OnDirection(DirectionUp)
	// Up → inverted → closing.
	if c.IsOpening() {
		t.Error("inverted DirectionUp must NOT be opening")
	}
	if !c.IsClosing() {
		t.Error("inverted DirectionUp must be closing")
	}
	c.OnDirection(DirectionDown)
	if !c.IsOpening() {
		t.Error("inverted DirectionDown must be opening")
	}
}

// TestCoverBaseDPMethodsExist verifies that Cover embeds custom.BaseDP and
// exposes its observability methods without panicking.
func TestCoverBaseDPMethodsExist(t *testing.T) {
	t.Parallel()

	w := &stubWriter{}
	c, _, _ := newRig(t, "HmIP-BROLL:1", w, custom.CoverCapabilities{})

	// Must compile and return zero values before any event.
	_, _ = c.ModifiedAt()
	_, _ = c.RefreshedAt()
	_ = c.UnconfirmedLastValuesSend()

	c.MarkModified()
	c.MarkRefreshed()

	if _, ok := c.ModifiedAt(); !ok {
		t.Error("ModifiedAt() must be non-zero after MarkModified()")
	}
	if _, ok := c.RefreshedAt(); !ok {
		t.Error("RefreshedAt() must be non-zero after MarkRefreshed()")
	}
}
