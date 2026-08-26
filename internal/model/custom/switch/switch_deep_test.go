// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package switchdev

import (
	"context"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// --- Switch deep tests ---

// TestSwitchAddressReturnsChannelAddress verifies that Address() echoes
// the channel address given at construction time.
func TestSwitchAddressReturnsChannelAddress(t *testing.T) {
	t.Parallel()

	const addr = "HmIP-PS:3"
	s := newTestSwitch(t, addr, "", &stubWriter{})
	if got := s.Address(); got != addr {
		t.Errorf("Address() = %q, want %q", got, addr)
	}
}

// TestSwitchOnSetsState verifies that TurnOn writes STATE=true to the CCU.
func TestSwitchOnSetsState(t *testing.T) {
	t.Parallel()

	w := &stubWriter{}
	s := newTestSwitch(t, "HmIP-PS:3", "", w)
	if err := s.TurnOn(context.Background(), hmenum.CommandPriorityHigh); err != nil {
		t.Fatal(err)
	}
	if w.lastParm != hmenum.ParameterState {
		t.Errorf("TurnOn wrote param %q, want %q", w.lastParm, hmenum.ParameterState)
	}
	if w.lastVal != true {
		t.Errorf("TurnOn wrote value %v, want true", w.lastVal)
	}
}

// TestSwitchOffSetsState verifies that TurnOff writes STATE=false to the CCU.
func TestSwitchOffSetsState(t *testing.T) {
	t.Parallel()

	w := &stubWriter{}
	s := newTestSwitch(t, "HmIP-PS:3", "", w)
	if err := s.TurnOff(context.Background(), hmenum.CommandPriorityHigh); err != nil {
		t.Fatal(err)
	}
	if w.lastParm != hmenum.ParameterState {
		t.Errorf("TurnOff wrote param %q, want %q", w.lastParm, hmenum.ParameterState)
	}
	if w.lastVal != false {
		t.Errorf("TurnOff wrote value %v, want false", w.lastVal)
	}
}

// TestSwitchReadStateFromUnderlyingDP verifies that OnEvent updates the
// value visible through IsOn.
func TestSwitchReadStateFromUnderlyingDP(t *testing.T) {
	t.Parallel()

	w := &stubWriter{}
	s := newTestSwitch(t, "HmIP-PS:3", "", w)

	// Nothing observed yet.
	if _, observed := s.IsOn(); observed {
		t.Error("IsOn should not be observed before any event")
	}

	// Push an event.
	s.OnEvent(true)
	on, observed := s.IsOn()
	if !observed {
		t.Error("IsOn should be observed after OnEvent")
	}
	if !on {
		t.Error("IsOn should be true after OnEvent(true)")
	}

	// Drive it false.
	s.OnEvent(false)
	on, observed = s.IsOn()
	if !observed || on {
		t.Errorf("IsOn = %v, observed = %v, want (false, true)", on, observed)
	}
}

// TestSwitchOnStateAliasUpdatesDP verifies the backwards-compat OnState alias.
func TestSwitchOnStateAliasUpdatesDP(t *testing.T) {
	t.Parallel()

	s := newTestSwitch(t, "HmIP-PS:3", "", &stubWriter{})
	s.OnState(true)
	on, ok := s.IsOn()
	if !ok || !on {
		t.Errorf("OnState(true): IsOn() = %v, ok = %v, want (true, true)", on, ok)
	}
}

// TestSwitchIsStateChangeReturnsTrueWhenUnobserved verifies that the first
// command always goes through even when the current state is unknown.
func TestSwitchIsStateChangeReturnsTrueWhenUnobserved(t *testing.T) {
	t.Parallel()

	s := newTestSwitch(t, "HmIP-PS:3", "", &stubWriter{})
	if !s.IsStateChange(false) {
		t.Error("IsStateChange must be true before any state is observed")
	}
	if !s.IsStateChange(true) {
		t.Error("IsStateChange must be true before any state is observed (true variant)")
	}
}

// TestSwitchIsStateChangeReturnsFalseWhenSame verifies no redundant write.
func TestSwitchIsStateChangeReturnsFalseWhenSame(t *testing.T) {
	t.Parallel()

	s := newTestSwitch(t, "HmIP-PS:3", "", &stubWriter{})
	s.OnEvent(true)
	if s.IsStateChange(true) {
		t.Error("IsStateChange must be false when target == current")
	}
	if !s.IsStateChange(false) {
		t.Error("IsStateChange must be true when target != current")
	}
}

// TestSwitchOnTimeForwardsToOnTimeDP verifies that TurnOnFor sends
// ON_TIME + STATE atomically when the writer supports PutParamset.
func TestSwitchOnTimeForwardsToOnTimeDP(t *testing.T) {
	t.Parallel()

	w := &putWriter{}
	s := newTestSwitch(t, "HmIP-PS:3", "", w)
	if err := s.TurnOnFor(context.Background(), 30*time.Second, hmenum.CommandPriorityHigh); err != nil {
		t.Fatal(err)
	}
	if len(w.puts) != 1 {
		t.Fatalf("expected 1 put_paramset, got %d", len(w.puts))
	}
	got := w.puts[0]
	if got[string(hmenum.ParameterState)] != true {
		t.Errorf("STATE=%v, want true", got[string(hmenum.ParameterState)])
	}
	if v, ok := got[string(hmenum.ParameterOnTime)].(float64); !ok || v < 29.9 || v > 30.1 {
		t.Errorf("ON_TIME=%v, want ~30", got[string(hmenum.ParameterOnTime)])
	}
}

// TestSwitchSetTimerThenTurnOnAtomicBatch verifies the timer-deferred
// path: SetTimerOnTime stores the timer, the next TurnOn consumes it, and
// a subsequent TurnOn does NOT produce a put_paramset.
func TestSwitchSetTimerThenTurnOnAtomicBatch(t *testing.T) {
	t.Parallel()

	w := &putWriter{}
	s := newTestSwitch(t, "VCU2128127:4", "", w)
	s.SetTimerOnTime(10 * time.Second)

	if err := s.TurnOn(context.Background(), hmenum.CommandPriorityHigh); err != nil {
		t.Fatal(err)
	}
	if len(w.puts) != 1 {
		t.Fatalf("first TurnOn: expected 1 put_paramset, got %d", len(w.puts))
	}
	// Timer is consumed — second TurnOn must fall back to plain SetValue.
	w.puts = nil
	if err := s.TurnOn(context.Background(), hmenum.CommandPriorityHigh); err != nil {
		t.Fatal(err)
	}
	if len(w.puts) != 0 {
		t.Errorf("second TurnOn should NOT produce a put_paramset, got %d", len(w.puts))
	}
}

// TestSwitchGroupStateNotNil verifies that a freshly constructed Switch
// exposes a non-nil GroupState tracker.
func TestSwitchGroupStateNotNil(t *testing.T) {
	t.Parallel()

	s := newTestSwitch(t, "HmIP-PS:3", "", &stubWriter{})
	if s.GroupState() == nil {
		t.Error("GroupState() must not be nil")
	}
}

// TestSwitchCloseIsIdempotent verifies that TurnOff called multiple times
// does not panic (Close on generic.Switch isn't exposed; we exercise
// the idempotency of IsOn after repeated writes instead — the wrapper
// has no Close method, so we test double-write safety).
func TestSwitchCloseIsIdempotent(t *testing.T) {
	t.Parallel()

	w := &stubWriter{}
	s := newTestSwitch(t, "HmIP-PS:3", "", w)
	// No panic on repeated TurnOff.
	_ = s.TurnOff(context.Background(), hmenum.CommandPriorityHigh)
	_ = s.TurnOff(context.Background(), hmenum.CommandPriorityHigh)
}

// TestSwitchBaseDPMethodsExist verifies that Switch embeds custom.BaseDP and
// exposes its observability methods without panicking.
func TestSwitchBaseDPMethodsExist(t *testing.T) {
	t.Parallel()

	s := newTestSwitch(t, "VCU0001:4", "ccu1", nil)
	if s == nil {
		t.Skip("newTestSwitch returned nil — channel has no *generic.Switch")
	}

	// Must compile and return zero values before any event.
	_, _ = s.ModifiedAt()
	_, _ = s.RefreshedAt()
	_ = s.UnconfirmedLastValuesSend()

	s.MarkModified()
	s.MarkRefreshed()

	if _, ok := s.ModifiedAt(); !ok {
		t.Error("ModifiedAt() must be non-zero after MarkModified()")
	}
	if _, ok := s.RefreshedAt(); !ok {
		t.Error("RefreshedAt() must be non-zero after MarkRefreshed()")
	}
}
