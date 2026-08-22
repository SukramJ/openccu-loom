// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package cover

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// neuterGoToTimers replaces the debouncer's timer factory with one that
// never fires on its own, so tests control the deferred CCU write
// explicitly via [flushGoToWrites]. Installed by every rig helper — the
// production delays (150/400 ms) would otherwise fire mid-test and race
// the assertions.
func neuterGoToTimers(d *goToDebouncer) {
	d.afterFunc = func(time.Duration, func()) *time.Timer {
		t := time.NewTimer(time.Hour)
		t.Stop()
		return t
	}
}

// flushGoToWrites synchronously runs every pending deferred write, as
// if the debounce delays had elapsed.
func flushGoToWrites(d *goToDebouncer) {
	for axis := goToAxis(0); axis < goToAxisCount; axis++ {
		d.mu.Lock()
		slot := &d.slots[axis]
		slot.gen++
		if slot.timer != nil {
			slot.timer.Stop()
			slot.timer = nil
		}
		write := slot.write
		slot.write = nil
		d.mu.Unlock()
		if write != nil {
			write()
		}
	}
}

// goToScheduleRecorder fakes the debouncer's clock and records every
// scheduled delay so the two-phase decision is observable without real
// timers.
type goToScheduleRecorder struct {
	mu     sync.Mutex
	now    time.Time
	delays []time.Duration
}

func installGoToRecorder(d *goToDebouncer) *goToScheduleRecorder {
	r := &goToScheduleRecorder{now: time.Unix(1_000_000, 0)}
	d.now = func() time.Time {
		r.mu.Lock()
		defer r.mu.Unlock()
		return r.now
	}
	d.afterFunc = func(delay time.Duration, _ func()) *time.Timer {
		r.mu.Lock()
		r.delays = append(r.delays, delay)
		r.mu.Unlock()
		t := time.NewTimer(time.Hour)
		t.Stop()
		return t
	}
	return r
}

func (r *goToScheduleRecorder) advance(d time.Duration) {
	r.mu.Lock()
	r.now = r.now.Add(d)
	r.mu.Unlock()
}

func (r *goToScheduleRecorder) recorded() []time.Duration {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]time.Duration, len(r.delays))
	copy(out, r.delays)
	return out
}

// countingWriter records every SetValue payload in arrival order.
type countingWriter struct {
	mu     sync.Mutex
	values []any
}

func (w *countingWriter) SetValue(_ context.Context, _ string, _ hmenum.Parameter, value any, _ hmenum.CommandPriority) error {
	w.mu.Lock()
	w.values = append(w.values, value)
	w.mu.Unlock()
	return nil
}

func (w *countingWriter) recorded() []any {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := make([]any, len(w.values))
	copy(out, w.values)
	return out
}

// invokeGoToLift is a small shortcut for the tests below.
func invokeGoToLift(t *testing.T, c *Cover, pct uint16) {
	t.Helper()
	srv := c.MatterClusterServers()[0]
	if _, err := srv.MatterInvoke(context.Background(), matterCmdGoToLiftPercentage, pct, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("GoToLiftPercentage(%d): %v", pct, err)
	}
}

// TestGoToDebounce_GestureStartUsesLongDelay: the first command of a
// gesture — and any command arriving after more than goToGestureGap of
// idle — waits the long goToDebounceGestureStart window, because a
// quick swipe's first value is an unwanted intermediate step.
func TestGoToDebounce_GestureStartUsesLongDelay(t *testing.T) {
	t.Parallel()
	c, _, _ := newRig(t, "HmIP-BROLL:3", &countingWriter{}, custom.CoverCapabilities{})
	rec := installGoToRecorder(&c.matterGoTo)

	invokeGoToLift(t, c, 3000)
	rec.advance(goToGestureGap + 100*time.Millisecond) // idle: next command starts a new gesture
	invokeGoToLift(t, c, 4000)

	delays := rec.recorded()
	if len(delays) != 2 {
		t.Fatalf("scheduled delays = %v, want 2 entries", delays)
	}
	if delays[0] != goToDebounceGestureStart {
		t.Errorf("first command delay = %v, want %v (gesture start)", delays[0], goToDebounceGestureStart)
	}
	if delays[1] != goToDebounceGestureStart {
		t.Errorf("post-idle command delay = %v, want %v (new gesture)", delays[1], goToDebounceGestureStart)
	}
}

// TestGoToDebounce_ActiveDragReplacesPendingWithShortDelay: commands
// arriving within goToGestureGap of the previous one are an active drag
// — they wait only goToDebounceActiveDrag, and each replaces the
// pending write so exactly one CCU write (the last value) goes out.
func TestGoToDebounce_ActiveDragReplacesPendingWithShortDelay(t *testing.T) {
	t.Parallel()
	w := &countingWriter{}
	c, _, _ := newRig(t, "HmIP-BROLL:3", w, custom.CoverCapabilities{})
	rec := installGoToRecorder(&c.matterGoTo)

	invokeGoToLift(t, c, 3000)
	rec.advance(200 * time.Millisecond)
	invokeGoToLift(t, c, 4000)
	rec.advance(200 * time.Millisecond)
	invokeGoToLift(t, c, 5000)

	delays := rec.recorded()
	want := []time.Duration{goToDebounceGestureStart, goToDebounceActiveDrag, goToDebounceActiveDrag}
	if len(delays) != len(want) {
		t.Fatalf("scheduled delays = %v, want %v", delays, want)
	}
	for i := range want {
		if delays[i] != want[i] {
			t.Errorf("delay[%d] = %v, want %v", i, delays[i], want[i])
		}
	}

	flushGoToWrites(&c.matterGoTo)
	values := w.recorded()
	if len(values) != 1 {
		t.Fatalf("CCU writes after drag = %v, want exactly 1 (the settled value)", values)
	}
	// Matter 5000 → HM 0.5 — only the value the drag settled on.
	if values[0].(float64) != 0.5 {
		t.Errorf("settled write = %v, want 0.5", values[0])
	}
}

// TestGoToLiftPercentage_AtTargetAcknowledgedWithoutWrite: a command
// whose destination is within 1 % (100 percent100ths) of the current
// position returns Success without any radio write, and drops a pending
// intermediate drag value — the freshest intent is "stay here".
func TestGoToLiftPercentage_AtTargetAcknowledgedWithoutWrite(t *testing.T) {
	t.Parallel()

	t.Run("SingleCommandAtCurrent", func(t *testing.T) {
		t.Parallel()
		w := &countingWriter{}
		c, _, _ := newRig(t, "HmIP-BROLL:3", w, custom.CoverCapabilities{})
		c.OnLevel(0.4) // current = Matter 6000
		srv := c.MatterClusterServers()[0]

		invokeGoToLift(t, c, 6050) // |6050-6000| = 50 <= 100
		flushGoToWrites(&c.matterGoTo)
		if got := w.recorded(); len(got) != 0 {
			t.Fatalf("CCU writes = %v, want none (at target)", got)
		}
		// The commanded destination is still the reported target.
		if v, ok := srv.MatterRead(matterAttrTargetPositionLiftPercent100ths); !ok || v.(uint16) != 6050 {
			t.Fatalf("TargetPositionLift = (%v, %v), want (6050, true)", v, ok)
		}
	})

	t.Run("ReturnToStartDropsPendingDragValue", func(t *testing.T) {
		t.Parallel()
		w := &countingWriter{}
		c, _, _ := newRig(t, "HmIP-BROLL:3", w, custom.CoverCapabilities{})
		c.OnLevel(0.4) // current = Matter 6000

		invokeGoToLift(t, c, 3000) // mid-drag value, pending
		invokeGoToLift(t, c, 6050) // drag returned to the start — at target
		flushGoToWrites(&c.matterGoTo)
		if got := w.recorded(); len(got) != 0 {
			t.Fatalf("CCU writes = %v, want none — the stale 3000 must not fire", got)
		}
	})

	t.Run("BeyondToleranceStillWrites", func(t *testing.T) {
		t.Parallel()
		w := &countingWriter{}
		c, _, _ := newRig(t, "HmIP-BROLL:3", w, custom.CoverCapabilities{})
		c.OnLevel(0.4) // current = Matter 6000

		invokeGoToLift(t, c, 6101) // |6101-6000| = 101 > 100
		flushGoToWrites(&c.matterGoTo)
		if got := w.recorded(); len(got) != 1 {
			t.Fatalf("CCU writes = %v, want exactly 1 (beyond tolerance)", got)
		}
	})
}

// TestGoToDebounce_StopMotionCancelsPendingWrite: Stop pre-empts queued
// motion — a debounced GoTo write firing after the STOP would restart
// the movement the user just halted.
func TestGoToDebounce_StopMotionCancelsPendingWrite(t *testing.T) {
	t.Parallel()
	w := &countingWriter{}
	c, _, _ := newRig(t, "HmIP-BROLL:3", w, custom.CoverCapabilities{SupportsStop: true})
	c.OnLevel(0.4)
	srv := c.MatterClusterServers()[0]

	invokeGoToLift(t, c, 3000) // pending
	if _, err := srv.MatterInvoke(context.Background(), matterCmdStopMotion, nil, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("StopMotion: %v", err)
	}
	flushGoToWrites(&c.matterGoTo)

	values := w.recorded()
	if len(values) != 1 || values[0] != true {
		t.Fatalf("CCU writes = %v, want exactly the STOP write (true)", values)
	}
}

// TestGoToDebounce_UnsubscribeCancelsPendingWrite: the unsubscribe
// closure returned by Subscribe — the data point's detach hook invoked
// on channel teardown — stops pending debounced writes so no timer
// writes to the CCU after the endpoint is gone.
func TestGoToDebounce_UnsubscribeCancelsPendingWrite(t *testing.T) {
	t.Parallel()

	t.Run("Cover", func(t *testing.T) {
		t.Parallel()
		w := &countingWriter{}
		c, ch, _ := newRig(t, "HmIP-BROLL:3", w, custom.CoverCapabilities{})
		unsub := c.Subscribe(ch)
		if unsub == nil {
			t.Fatal("Subscribe returned nil unsubscribe")
		}

		invokeGoToLift(t, c, 3000) // pending
		unsub()
		flushGoToWrites(&c.matterGoTo)
		if got := w.recorded(); len(got) != 0 {
			t.Fatalf("CCU writes after detach = %v, want none", got)
		}
	})
}

// TestGoToDebounce_BlindAxesDebounceIndependently: lift and tilt hold
// separate pending-command slots — a tilt adjustment must not swallow a
// pending lift write and vice versa.
func TestGoToDebounce_BlindAxesDebounceIndependently(t *testing.T) {
	t.Parallel()
	w := &putWriter{}
	b := newBlindRig(t, "VCU3560967:1", w, custom.CoverCapabilities{SupportsTilt: true}, BlindKindHM)
	srv := b.MatterClusterServers()[0]

	if _, err := srv.MatterInvoke(context.Background(), matterCmdGoToLiftPercentage, uint16(3000), hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("GoToLiftPercentage: %v", err)
	}
	if _, err := srv.MatterInvoke(context.Background(), matterCmdGoToTiltPercentage, uint16(2500), hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("GoToTiltPercentage: %v", err)
	}
	flushGoToWrites(&b.matterGoTo)

	if cc := w.combinedCalls(); len(cc) != 2 {
		t.Fatalf("combined writes = %d, want 2 (one per axis slot)", len(cc))
	}
}

// TestGoToDebounce_RealTimerFiresPendingWrite exercises the unseamed
// production path: schedule arms a real time.AfterFunc whose callback
// passes the generation check in fire and runs the pending write.
func TestGoToDebounce_RealTimerFiresPendingWrite(t *testing.T) {
	t.Parallel()
	var d goToDebouncer
	done := make(chan struct{})
	d.schedule(goToAxisLift, func() { close(done) })
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("pending write did not fire within 5s (gesture-start delay is 400ms)")
	}
}

// TestParityMatterJS_GoToPercentageStoresTargetBeforeDeviceWrite pins
// the accepted-before-written command contract: matter.js
// goToLiftPercentage stores TargetPositionLiftPercent100ths and returns
// while the movement itself runs as a detached worker
// (WindowCoveringServer.ts:574-589; :379-383 "this method returns
// before actual movement completes"). The Go projection mirrors that
// order — the commanded target is readable the instant the invoke
// returns, and the CCU write follows after the debounce window.
func TestParityMatterJS_GoToPercentageStoresTargetBeforeDeviceWrite(t *testing.T) {
	t.Parallel()
	w := &countingWriter{}
	c, _, _ := newRig(t, "HmIP-BROLL:3", w, custom.CoverCapabilities{})
	c.OnLevel(0.4) // current = Matter 6000
	srv := c.MatterClusterServers()[0]

	invokeGoToLift(t, c, 3000)
	if v, ok := srv.MatterRead(matterAttrTargetPositionLiftPercent100ths); !ok || v.(uint16) != 3000 {
		t.Fatalf("TargetPositionLift right after invoke = (%v, %v), want (3000, true)", v, ok)
	}
	if got := w.recorded(); len(got) != 0 {
		t.Fatalf("CCU writes before the debounce window elapsed = %v, want none", got)
	}

	flushGoToWrites(&c.matterGoTo)
	values := w.recorded()
	if len(values) != 1 || values[0].(float64) != 0.7 {
		t.Fatalf("deferred CCU write = %v, want [0.7] (Matter 3000 → HM 0.7)", values)
	}
}
