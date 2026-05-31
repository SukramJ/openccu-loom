// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package generic

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// --- helpers ---------------------------------------------------------

type optWriter struct {
	mu     sync.Mutex
	calls  []any
	delay  time.Duration
	err    error
	errSet atomic.Bool
}

func (w *optWriter) SetValue(ctx context.Context, _ string, _ hmenum.Parameter, v any, _ hmenum.CommandPriority) error {
	if w.delay > 0 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(w.delay):
		}
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.calls = append(w.calls, v)
	if w.errSet.Load() {
		return w.err
	}
	return nil
}

func (w *optWriter) callCount() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.calls)
}

func (w *optWriter) failNext(err error) {
	w.err = err
	w.errSet.Store(true)
}

func newBoolDP(t *testing.T, w Writer, opts ...func(*Spec)) *DataPoint[bool] {
	t.Helper()
	cfg := Spec{
		Key: hmtypes.DataPointKey{
			InterfaceID:    "iface",
			ChannelAddress: "A:1",
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(hmenum.ParameterState),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeBool,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent,
		},
		Writer: w,
	}
	for _, o := range opts {
		o(&cfg)
	}
	return NewDataPoint[bool](cfg)
}

func newFloatDP(t *testing.T, w Writer, opts ...func(*Spec)) *DataPoint[float64] {
	t.Helper()
	cfg := Spec{
		Key: hmtypes.DataPointKey{
			InterfaceID:    "iface",
			ChannelAddress: "A:1",
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(hmenum.ParameterLevel),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeFloat,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent,
		},
		Writer: w,
	}
	for _, o := range opts {
		o(&cfg)
	}
	return NewDataPoint[float64](cfg)
}

// withOptTimeout sets a tight timeout to keep timer-driven tests fast.
func withOptTimeout(d time.Duration) func(*Spec) {
	return func(c *Spec) { c.OptimisticTimeout = d }
}

// withOptimisticDisabled skips the tracker entirely.
func withOptimisticDisabled() func(*Spec) {
	return func(c *Spec) { c.OptimisticDisabled = true }
}

// sendBoolWith wraps the typed setter so tests don't need to import
// the bool-specialised file.
func sendBool(t *testing.T, dp *DataPoint[bool], v bool) error {
	t.Helper()
	return dp.sendAndObserve(context.Background(), v, v, hmenum.CommandPriorityHigh)
}

func sendFloat(t *testing.T, dp *DataPoint[float64], v float64) error {
	t.Helper()
	return dp.sendAndObserve(context.Background(), v, v, hmenum.CommandPriorityHigh)
}

// --- core tracker behaviour -----------------------------------------

func TestOptimisticApplyMakesValueVisibleImmediately(t *testing.T) {
	w := &optWriter{}
	dp := newBoolDP(t, w, withOptTimeout(2*time.Second))
	if err := sendBool(t, dp, true); err != nil {
		t.Fatalf("send: %v", err)
	}
	v, ok := dp.Value()
	if !ok || !v {
		t.Fatalf("Value() = (%v, %v), want (true, true)", v, ok)
	}
	if !dp.IsOptimistic() {
		t.Fatal("IsOptimistic must be true after send before confirm")
	}
}

func TestOptimisticConfirmedValueShowsConfirmedOnly(t *testing.T) {
	w := &optWriter{}
	dp := newBoolDP(t, w, withOptTimeout(2*time.Second))
	dp.OnEvent(false)
	_ = sendBool(t, dp, true)
	if v, ok := dp.ConfirmedValue(); !ok || v != false {
		t.Fatalf("ConfirmedValue = (%v, %v), want (false, true)", v, ok)
	}
	if v, _ := dp.Value(); v != true {
		t.Fatal("Value should reflect optimistic 'true'")
	}
}

func TestOptimisticPendingSendsCounter(t *testing.T) {
	w := &optWriter{}
	dp := newBoolDP(t, w, withOptTimeout(5*time.Second))
	_ = sendBool(t, dp, true)
	if got := dp.PendingSends(); got != 1 {
		t.Fatalf("PendingSends after first send = %d, want 1", got)
	}
	// Burst-skip: same value → no-op, counter unchanged.
	_ = sendBool(t, dp, true)
	if got := dp.PendingSends(); got != 1 {
		t.Fatalf("PendingSends after duplicate = %d, want 1 (burst-skip)", got)
	}
	// Different value → bumps counter.
	dp2 := newBoolDP(t, w, withOptTimeout(5*time.Second))
	_ = sendBool(t, dp2, true)
	_ = sendBool(t, dp2, false)
	if got := dp2.PendingSends(); got != 2 {
		t.Fatalf("PendingSends after different-value send = %d, want 2", got)
	}
}

func TestOptimisticConfirmClearsTracker(t *testing.T) {
	w := &optWriter{}
	dp := newBoolDP(t, w, withOptTimeout(5*time.Second))
	_ = sendBool(t, dp, true)
	dp.OnEvent(true) // CCU confirms
	if dp.IsOptimistic() {
		t.Fatal("IsOptimistic must be false after matching confirm")
	}
	if got := dp.PendingSends(); got != 0 {
		t.Fatalf("PendingSends after confirm = %d, want 0", got)
	}
}

func TestOptimisticBurstConfirmsDrainCounter(t *testing.T) {
	w := &optWriter{}
	dp := newFloatDP(t, w, withOptTimeout(5*time.Second))
	_ = sendFloat(t, dp, 0.5)
	_ = sendFloat(t, dp, 0.7)
	_ = sendFloat(t, dp, 0.9)
	if got := dp.PendingSends(); got != 3 {
		t.Fatalf("PendingSends = %d, want 3", got)
	}
	dp.OnEvent(0.5) // first confirm — value mismatch with current opt 0.9 → clear
	if dp.IsOptimistic() {
		t.Fatal("mismatch confirm should clear tracker (CCU authoritative)")
	}
}

func TestOptimisticBurstSkipDrainsWithSingleConfirm(t *testing.T) {
	// Issue #3049 anchor: three sends of the same value collapse to
	// one pending-send via burst-skip, so a single CCU confirm
	// drains the tracker — no spurious rollback after timeout.
	w := &optWriter{}
	dp := newBoolDP(t, w, withOptTimeout(5*time.Second))
	_ = sendBool(t, dp, true)
	_ = sendBool(t, dp, true) // skipped
	_ = sendBool(t, dp, true) // skipped
	if got := dp.PendingSends(); got != 1 {
		t.Fatalf("PendingSends = %d, want 1 (burst-skip collapses dups)", got)
	}
	dp.OnEvent(true)
	if dp.IsOptimistic() {
		t.Fatal("single confirm must drain tracker after burst-skip")
	}
}

func TestOptimisticPreviousValueCapturedOnFirstSendOnly(t *testing.T) {
	w := &optWriter{}
	dp := newFloatDP(t, w, withOptTimeout(5*time.Second))
	dp.OnEvent(0.0) // initial confirmed value
	_ = sendFloat(t, dp, 0.5)
	_ = sendFloat(t, dp, 0.7)
	_ = sendFloat(t, dp, 0.9)
	// Force a send error → rollback should restore 0.0 (anchor),
	// not 0.7 or any later "current".
	w.failNext(errors.New("wire-busy"))
	// The send-error rollback must fire on the failed 4th send,
	// even though Apply already incremented pendingSends to 4.
	// We don't assert on the returned error here — the relevant
	// invariant is the post-state (anchor restored, tracker
	// cleared), checked below.
	_ = sendFloat(t, dp, 0.95)
	if v, _ := dp.ConfirmedValue(); v != 0.0 {
		t.Fatalf("ConfirmedValue after send error rollback = %v, want 0.0 (rescue anchor)", v)
	}
	if dp.IsOptimistic() {
		t.Fatal("rollback should clear tracker")
	}
}

func TestOptimisticSendErrorRollback(t *testing.T) {
	w := &optWriter{}
	dp := newBoolDP(t, w, withOptTimeout(5*time.Second))
	dp.OnEvent(false)

	w.failNext(errors.New("ccu offline"))
	if err := sendBool(t, dp, true); err == nil {
		t.Fatal("expected send error")
	}
	if dp.IsOptimistic() {
		t.Fatal("send error must roll back tracker")
	}
	if v, ok := dp.Value(); !ok || v != false {
		t.Fatalf("Value after rollback = (%v, %v), want (false, true)", v, ok)
	}
}

func TestOptimisticTimeoutRollback(t *testing.T) {
	w := &optWriter{}
	dp := newBoolDP(t, w, withOptTimeout(50*time.Millisecond))
	dp.OnEvent(false)
	_ = sendBool(t, dp, true)
	// Wait past the timeout window.
	time.Sleep(120 * time.Millisecond)
	if dp.IsOptimistic() {
		t.Fatal("tracker should have rolled back after timeout")
	}
	if v, _ := dp.Value(); v != false {
		t.Fatal("timeout rollback should restore previous value")
	}
}

func TestOptimisticTimeoutPublishesRollbackEvent(t *testing.T) {
	w := &optWriter{}
	dp := newBoolDP(t, w, withOptTimeout(40*time.Millisecond))
	dp.OnEvent(false)

	type rollback struct {
		reason   RollbackReason
		from, to bool
		set      bool
	}
	var got []rollback
	var mu sync.Mutex
	dp.OnRollback(func(r RollbackReason, from, to, set bool) {
		mu.Lock()
		defer mu.Unlock()
		got = append(got, rollback{r, from, to, set})
	})

	_ = sendBool(t, dp, true)
	time.Sleep(120 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	if len(got) != 1 {
		t.Fatalf("expected 1 rollback callback, got %d", len(got))
	}
	if got[0].reason != RollbackReasonTimeout {
		t.Fatalf("reason = %s, want timeout", got[0].reason)
	}
	if got[0].from != true || got[0].to != false || !got[0].set {
		t.Fatalf("rollback payload = %+v", got[0])
	}
}

func TestOptimisticSendErrorPublishesRollbackEvent(t *testing.T) {
	w := &optWriter{}
	dp := newFloatDP(t, w, withOptTimeout(5*time.Second))
	dp.OnEvent(0.25)

	var reason atomic.Value
	dp.OnRollback(func(r RollbackReason, _, _ float64, _ bool) {
		reason.Store(r)
	})

	w.failNext(errors.New("network gone"))
	_ = sendFloat(t, dp, 0.75)

	if r, _ := reason.Load().(RollbackReason); r != RollbackReasonSendError {
		t.Fatalf("reason = %v, want send_error", r)
	}
}

func TestOptimisticMismatchClearsWithoutRollbackEvent(t *testing.T) {
	w := &optWriter{}
	dp := newFloatDP(t, w, withOptTimeout(5*time.Second))
	dp.OnEvent(0.0)

	var rollbackFired atomic.Bool
	dp.OnRollback(func(_ RollbackReason, _, _ float64, _ bool) {
		rollbackFired.Store(true)
	})

	_ = sendFloat(t, dp, 0.5)
	dp.OnEvent(0.6) // CCU rounds / clamps differently → mismatch

	if dp.IsOptimistic() {
		t.Fatal("mismatch must clear tracker")
	}
	if v, _ := dp.Value(); v != 0.6 {
		t.Fatalf("Value after mismatch confirm = %v, want 0.6 (CCU authoritative)", v)
	}
	if rollbackFired.Load() {
		t.Fatal("mismatch must not fire rollback event")
	}
}

func TestOptimisticFloatToleranceForSlightDeviation(t *testing.T) {
	w := &optWriter{}
	dp := newFloatDP(t, w, withOptTimeout(5*time.Second))

	_ = sendFloat(t, dp, 0.5)
	dp.OnEvent(0.503) // within 2-decimal tolerance → confirms

	if dp.IsOptimistic() {
		t.Fatal("close-enough confirm must drain pendingSends and clear")
	}
	if v, _ := dp.Value(); v != 0.503 {
		t.Fatalf("Value after confirm = %v, want 0.503 (CCU value)", v)
	}
}

func TestOptimisticDisabledShortCircuits(t *testing.T) {
	w := &optWriter{}
	dp := newBoolDP(t, w, withOptimisticDisabled())
	dp.OnEvent(false)

	_ = sendBool(t, dp, true)
	if dp.IsOptimistic() {
		t.Fatal("OptimisticDisabled must skip tracker entirely")
	}
	if v, _ := dp.Value(); v != true {
		t.Fatal("disabled path still updates cached value synchronously")
	}
	if w.callCount() != 1 {
		t.Fatalf("calls = %d, want 1", w.callCount())
	}
}

func TestOptimisticDisabledStillSurfacesSendError(t *testing.T) {
	w := &optWriter{}
	dp := newBoolDP(t, w, withOptimisticDisabled())
	w.failNext(errors.New("wire down"))
	if err := sendBool(t, dp, true); err == nil {
		t.Fatal("expected error when wire fails")
	}
	if v, ok := dp.Value(); ok && v {
		t.Fatal("disabled-path failed send must not flip cached value")
	}
}

func TestOptimisticBurstSkipReportsNoError(t *testing.T) {
	w := &optWriter{}
	dp := newBoolDP(t, w, withOptTimeout(5*time.Second))
	_ = sendBool(t, dp, true)
	if err := sendBool(t, dp, true); err != nil {
		t.Fatalf("burst-skip must be silent no-op, got %v", err)
	}
	if w.callCount() != 1 {
		t.Fatalf("burst-skip should not dispatch second wire call: %d", w.callCount())
	}
}

func TestOptimisticUpdateSubscriberSeesOptimisticThenConfirm(t *testing.T) {
	w := &optWriter{}
	dp := newBoolDP(t, w, withOptTimeout(5*time.Second))
	dp.OnEvent(false)

	var transitions [][2]bool
	var mu sync.Mutex
	dp.OnUpdate(func(old, next bool) {
		mu.Lock()
		defer mu.Unlock()
		transitions = append(transitions, [2]bool{old, next})
	})

	_ = sendBool(t, dp, true)
	dp.OnEvent(true)

	mu.Lock()
	defer mu.Unlock()
	if len(transitions) != 2 {
		t.Fatalf("expected 2 update callbacks (optimistic + confirm), got %d: %+v", len(transitions), transitions)
	}
	if transitions[0] != [2]bool{false, true} {
		t.Fatalf("optimistic transition = %v, want {false true}", transitions[0])
	}
	if transitions[1] != [2]bool{false, true} {
		// CCU confirms with same value — old=false because confirmedValue still
		// reflected the rescue anchor before this call.
		t.Fatalf("confirm transition = %v, want {false true}", transitions[1])
	}
}

func TestOptimisticTimerCancelledOnConfirm(t *testing.T) {
	w := &optWriter{}
	dp := newBoolDP(t, w, withOptTimeout(80*time.Millisecond))
	dp.OnEvent(false)

	var rollbacks atomic.Int32
	dp.OnRollback(func(_ RollbackReason, _, _, _ bool) {
		rollbacks.Add(1)
	})

	_ = sendBool(t, dp, true)
	dp.OnEvent(true) // confirm fast — should cancel timer
	time.Sleep(150 * time.Millisecond)

	if rollbacks.Load() != 0 {
		t.Fatal("timer must be cancelled on confirm")
	}
}

func TestOptimisticConcurrentSends(t *testing.T) {
	w := &optWriter{delay: 5 * time.Millisecond}
	dp := newFloatDP(t, w, withOptTimeout(5*time.Second))
	dp.OnEvent(0.0)

	const N = 20
	var wg sync.WaitGroup
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func(v float64) {
			defer wg.Done()
			_ = sendFloat(t, dp, v)
		}(float64(i+1) / 100)
	}
	wg.Wait()

	// Tracker should be active with pendingSends in [1, N] —
	// burst-skip may have dropped duplicates, but the data
	// structures must be coherent (no panics, no negative).
	if !dp.IsOptimistic() {
		t.Fatal("expected tracker to be active after concurrent sends")
	}
	if got := dp.PendingSends(); got <= 0 || got > N {
		t.Fatalf("PendingSends = %d, want in (0, %d]", got, N)
	}
}

func TestOptimisticConcurrentConfirms(t *testing.T) {
	w := &optWriter{}
	dp := newFloatDP(t, w, withOptTimeout(5*time.Second))
	dp.OnEvent(0.0)

	for i := 0; i < 5; i++ {
		_ = sendFloat(t, dp, float64(i+1)/100)
	}

	// Concurrently fire matching confirms — last value is 0.05.
	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			dp.OnEvent(0.05)
		}()
	}
	wg.Wait()

	if dp.IsOptimistic() {
		t.Fatal("all confirms drained — tracker must be cleared")
	}
}

func TestOptimisticUnsubscribeRollback(t *testing.T) {
	w := &optWriter{}
	dp := newBoolDP(t, w, withOptTimeout(40*time.Millisecond))

	var fired atomic.Bool
	unsub := dp.OnRollback(func(_ RollbackReason, _, _, _ bool) {
		fired.Store(true)
	})
	unsub()

	_ = sendBool(t, dp, true)
	time.Sleep(120 * time.Millisecond)

	if fired.Load() {
		t.Fatal("unsubscribed callback must not fire")
	}
}

func TestOptimisticAgeMonotonicallyIncreasing(t *testing.T) {
	w := &optWriter{}
	dp := newBoolDP(t, w, withOptTimeout(5*time.Second))

	_ = sendBool(t, dp, true)
	a1 := dp.OptimisticAge()
	time.Sleep(20 * time.Millisecond)
	a2 := dp.OptimisticAge()
	if a2 <= a1 {
		t.Fatalf("age must grow: a1=%v a2=%v", a1, a2)
	}
	dp.OnEvent(true)
	if dp.OptimisticAge() != 0 {
		t.Fatal("age must reset to zero after confirm")
	}
}

func TestOptimisticPreviousAnchorRescuedOnRollbackChain(t *testing.T) {
	w := &optWriter{}
	dp := newFloatDP(t, w, withOptTimeout(40*time.Millisecond))
	dp.OnEvent(0.42) // anchor

	_ = sendFloat(t, dp, 0.5)
	_ = sendFloat(t, dp, 0.7)

	time.Sleep(120 * time.Millisecond) // rollback fires

	if v, ok := dp.Value(); !ok || v != 0.42 {
		t.Fatalf("Value after timeout rollback = (%v, %v), want (0.42, true)", v, ok)
	}
}

func TestOptimisticRollbackWithoutPreviousValue(t *testing.T) {
	// No OnEvent called → previousSet=false. Rollback must restore
	// "unobserved" state without panicking.
	w := &optWriter{}
	dp := newBoolDP(t, w, withOptTimeout(40*time.Millisecond))

	_ = sendBool(t, dp, true)
	time.Sleep(120 * time.Millisecond)

	if dp.IsOptimistic() {
		t.Fatal("tracker should be cleared")
	}
	if _, observed := dp.Value(); observed {
		t.Fatal("Value should report unobserved after rollback with no anchor")
	}
}

func TestOptimisticRollbackFromUnobservedFiresEvent(t *testing.T) {
	w := &optWriter{}
	dp := newBoolDP(t, w, withOptTimeout(40*time.Millisecond))

	type rec struct {
		reason   RollbackReason
		from, to bool
		set      bool
	}
	var rollbacks []rec
	var mu sync.Mutex
	dp.OnRollback(func(r RollbackReason, f, to, set bool) {
		mu.Lock()
		defer mu.Unlock()
		rollbacks = append(rollbacks, rec{r, f, to, set})
	})

	_ = sendBool(t, dp, true)
	time.Sleep(120 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	if len(rollbacks) != 1 {
		t.Fatalf("rollbacks=%d, want 1", len(rollbacks))
	}
	if rollbacks[0].set {
		t.Fatal("restoredSet must be false when no anchor was captured")
	}
}

func TestOptimisticEventWithoutTrackerBehavesAsBefore(t *testing.T) {
	dp := newBoolDP(t, nil, withOptTimeout(5*time.Second))
	dp.OnEvent(true)
	if v, ok := dp.Value(); !ok || !v {
		t.Fatalf("plain OnEvent path broken: (%v, %v)", v, ok)
	}
	if dp.IsOptimistic() {
		t.Fatal("plain event must not engage tracker")
	}
}

func TestOptimisticDefaultTimeoutIsThirtySeconds(t *testing.T) {
	w := &optWriter{}
	dp := newBoolDP(t, w) // no override
	if dp.optimisticTimeout() != OptimisticDefaultTimeout {
		t.Fatalf("default timeout = %v, want %v", dp.optimisticTimeout(), OptimisticDefaultTimeout)
	}
}

func TestOptimisticZeroTimeoutFallsBackToDefault(t *testing.T) {
	w := &optWriter{}
	dp := newBoolDP(t, w, withOptTimeout(0))
	if dp.optimisticTimeout() != OptimisticDefaultTimeout {
		t.Fatalf("zero timeout must fall back to default, got %v", dp.optimisticTimeout())
	}
}

func TestOptimisticCustomTimeoutHonoured(t *testing.T) {
	w := &optWriter{}
	dp := newBoolDP(t, w, withOptTimeout(7*time.Second))
	if dp.optimisticTimeout() != 7*time.Second {
		t.Fatalf("custom timeout = %v, want 7s", dp.optimisticTimeout())
	}
}

func TestValuesCloseFloatTwoDecimalTolerance(t *testing.T) {
	cases := []struct {
		a, b float64
		want bool
	}{
		{0.5, 0.5, true},
		{0.5, 0.503, true},  // within rounding to 2 decimals
		{0.5, 0.515, false}, // 0.50 vs 0.52 → not close
		{0.0, 0.0049, true},
		{0.0, 0.006, false},
		// 0.005 sits exactly on the half-up boundary; either side
		// is defensible. The mismatch path is harmless (CCU value
		// remains authoritative) so we deliberately don't pin a
		// behaviour here.
	}
	for _, c := range cases {
		got := valuesClose(c.a, c.b)
		if got != c.want {
			t.Errorf("valuesClose(%v, %v) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}

func TestValuesCloseExactForNonFloat(t *testing.T) {
	if !valuesClose(true, true) {
		t.Fatal("bool exact match")
	}
	if valuesClose(true, false) {
		t.Fatal("bool inequality")
	}
	if !valuesClose(int32(42), int32(42)) {
		t.Fatal("int32 match")
	}
	if valuesClose(int32(42), int32(43)) {
		t.Fatal("int32 inequality")
	}
	if !valuesClose("a", "a") {
		t.Fatal("string match")
	}
}
