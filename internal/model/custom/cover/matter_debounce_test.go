// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package cover

import (
	"context"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// settleGoToWrites advances the synctest clock past the longest debounce
// window and lets the timer goroutines finish, so every deferred CCU write
// that was not cancelled or replaced has run when it returns. It must be
// called inside a synctest bubble.
func settleGoToWrites() {
	time.Sleep(goToDebounceGestureStart)
	synctest.Wait()
}

// drainOptimisticRollbacks advances the synctest clock past the optimistic
// rollback timeout so the rollback goroutines that a write arms have exited
// before the bubble returns; synctest.Test fails while any goroutine is
// still blocked.
func drainOptimisticRollbacks() {
	time.Sleep(generic.OptimisticDefaultTimeout + 2*time.Second)
}

// expectGoToWritesAfter asserts that no deferred write has run just before
// delay elapses and that exactly want writes have run once it has, which
// pins the debounce delay to the nanosecond. It must be called inside a
// synctest bubble.
func expectGoToWritesAfter(t *testing.T, w *countingWriter, delay time.Duration, want ...any) {
	t.Helper()
	time.Sleep(delay - time.Nanosecond)
	synctest.Wait()
	if got := w.recorded(); len(got) != 0 {
		t.Fatalf("CCU writes %v ran before the %v debounce delay elapsed", got, delay)
	}
	time.Sleep(time.Nanosecond)
	synctest.Wait()
	got := w.recorded()
	if len(got) != len(want) {
		t.Fatalf("CCU writes after the %v debounce delay = %v, want %v", delay, got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("CCU write[%d] = %v, want %v", i, got[i], want[i])
		}
	}
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
	if _, err := srv.MatterInvoke(context.Background(), matterCmdGoToLiftPercentage, pct); err != nil {
		t.Fatalf("GoToLiftPercentage(%d): %v", pct, err)
	}
}

// TestGoToDebounce_GestureStartUsesLongDelay: the first command of a
// gesture — and any command arriving after more than goToGestureGap of
// idle — waits the long goToDebounceGestureStart window, because a
// quick swipe's first value is an unwanted intermediate step.
func TestGoToDebounce_GestureStartUsesLongDelay(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		defer drainOptimisticRollbacks()
		w := &countingWriter{}
		c, _, _ := newRig(t, "HmIP-BROLL:3", w, custom.CoverCapabilities{})

		invokeGoToLift(t, c, 3000)
		expectGoToWritesAfter(t, w, goToDebounceGestureStart, 0.7)

		// Idle past the gesture gap (measured from the first command):
		// the next command starts a new gesture.
		time.Sleep(goToGestureGap + 100*time.Millisecond - goToDebounceGestureStart)
		invokeGoToLift(t, c, 4000)
		w.mu.Lock()
		w.values = nil
		w.mu.Unlock()
		expectGoToWritesAfter(t, w, goToDebounceGestureStart, 0.6)
	})
}

// TestGoToDebounce_ActiveDragReplacesPendingWithShortDelay: commands
// arriving within goToGestureGap of the previous one are an active drag
// — they wait only goToDebounceActiveDrag, and each replaces the
// pending write so exactly one CCU write (the last value) goes out.
func TestGoToDebounce_ActiveDragReplacesPendingWithShortDelay(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		defer drainOptimisticRollbacks()
		w := &countingWriter{}
		c, _, _ := newRig(t, "HmIP-BROLL:3", w, custom.CoverCapabilities{})

		// The steps stay below goToDebounceActiveDrag so no replaced
		// write gets a chance to fire.
		invokeGoToLift(t, c, 3000)
		time.Sleep(100 * time.Millisecond)
		invokeGoToLift(t, c, 4000)
		time.Sleep(100 * time.Millisecond)
		invokeGoToLift(t, c, 5000)

		// Matter 5000 → HM 0.5 — only the value the drag settled on.
		expectGoToWritesAfter(t, w, goToDebounceActiveDrag, 0.5)

		// The replaced writes never fire, not even at the original
		// gesture-start deadline.
		settleGoToWrites()
		if got := w.recorded(); len(got) != 1 {
			t.Fatalf("CCU writes after drag = %v, want exactly 1 (the settled value)", got)
		}
	})
}

// TestGoToDebounce_ActiveDragSecondCommandUsesShortDelay pins the delay
// of the second command of a drag on its own: it is armed with the short
// window, not the gesture-start window the first command used.
func TestGoToDebounce_ActiveDragSecondCommandUsesShortDelay(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		defer drainOptimisticRollbacks()
		w := &countingWriter{}
		c, _, _ := newRig(t, "HmIP-BROLL:3", w, custom.CoverCapabilities{})

		invokeGoToLift(t, c, 3000)
		time.Sleep(100 * time.Millisecond)
		invokeGoToLift(t, c, 4000)

		expectGoToWritesAfter(t, w, goToDebounceActiveDrag, 0.6)
	})
}

// TestGoToLiftPercentage_AtTargetAcknowledgedWithoutWrite: a command
// whose destination is within 1 % (100 percent100ths) of the current
// position returns Success without any radio write, and drops a pending
// intermediate drag value — the freshest intent is "stay here".
func TestGoToLiftPercentage_AtTargetAcknowledgedWithoutWrite(t *testing.T) {
	t.Parallel()

	t.Run("SingleCommandAtCurrent", func(t *testing.T) {
		t.Parallel()
		synctest.Test(t, func(t *testing.T) {
			defer drainOptimisticRollbacks()
			w := &countingWriter{}
			c, _, _ := newRig(t, "HmIP-BROLL:3", w, custom.CoverCapabilities{})
			c.OnLevel(0.4) // current = Matter 6000
			srv := c.MatterClusterServers()[0]

			invokeGoToLift(t, c, 6050) // |6050-6000| = 50 <= 100
			settleGoToWrites()
			if got := w.recorded(); len(got) != 0 {
				t.Fatalf("CCU writes = %v, want none (at target)", got)
			}
			// The commanded destination is still the reported target.
			if v, ok := srv.MatterRead(matterAttrTargetPositionLiftPercent100ths); !ok || v.(uint16) != 6050 {
				t.Fatalf("TargetPositionLift = (%v, %v), want (6050, true)", v, ok)
			}
		})
	})

	t.Run("ReturnToStartDropsPendingDragValue", func(t *testing.T) {
		t.Parallel()
		synctest.Test(t, func(t *testing.T) {
			defer drainOptimisticRollbacks()
			w := &countingWriter{}
			c, _, _ := newRig(t, "HmIP-BROLL:3", w, custom.CoverCapabilities{})
			c.OnLevel(0.4) // current = Matter 6000

			invokeGoToLift(t, c, 3000) // mid-drag value, pending
			invokeGoToLift(t, c, 6050) // drag returned to the start — at target
			settleGoToWrites()
			if got := w.recorded(); len(got) != 0 {
				t.Fatalf("CCU writes = %v, want none — the stale 3000 must not fire", got)
			}
		})
	})

	t.Run("BeyondToleranceStillWrites", func(t *testing.T) {
		t.Parallel()
		synctest.Test(t, func(t *testing.T) {
			defer drainOptimisticRollbacks()
			w := &countingWriter{}
			c, _, _ := newRig(t, "HmIP-BROLL:3", w, custom.CoverCapabilities{})
			c.OnLevel(0.4) // current = Matter 6000

			invokeGoToLift(t, c, 6101) // |6101-6000| = 101 > 100
			settleGoToWrites()
			if got := w.recorded(); len(got) != 1 {
				t.Fatalf("CCU writes = %v, want exactly 1 (beyond tolerance)", got)
			}
		})
	})
}

// TestGoToDebounce_StopMotionCancelsPendingWrite: Stop pre-empts queued
// motion — a debounced GoTo write firing after the STOP would restart
// the movement the user just halted.
func TestGoToDebounce_StopMotionCancelsPendingWrite(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		defer drainOptimisticRollbacks()
		w := &countingWriter{}
		c, _, _ := newRig(t, "HmIP-BROLL:3", w, custom.CoverCapabilities{SupportsStop: true})
		c.OnLevel(0.4)
		srv := c.MatterClusterServers()[0]

		invokeGoToLift(t, c, 3000) // pending
		if _, err := srv.MatterInvoke(context.Background(), matterCmdStopMotion, nil); err != nil {
			t.Fatalf("StopMotion: %v", err)
		}
		settleGoToWrites()

		values := w.recorded()
		if len(values) != 1 || values[0] != true {
			t.Fatalf("CCU writes = %v, want exactly the STOP write (true)", values)
		}
	})
}

// TestGoToDebounce_UnsubscribeCancelsPendingWrite: the unsubscribe
// closure returned by Subscribe — the data point's detach hook invoked
// on channel teardown — stops pending debounced writes so no timer
// writes to the CCU after the endpoint is gone.
func TestGoToDebounce_UnsubscribeCancelsPendingWrite(t *testing.T) {
	t.Parallel()

	t.Run("Cover", func(t *testing.T) {
		t.Parallel()
		synctest.Test(t, func(t *testing.T) {
			defer drainOptimisticRollbacks()
			w := &countingWriter{}
			c, ch, _ := newRig(t, "HmIP-BROLL:3", w, custom.CoverCapabilities{})
			unsub := c.Subscribe(ch)
			if unsub == nil {
				t.Fatal("Subscribe returned nil unsubscribe")
			}

			invokeGoToLift(t, c, 3000) // pending
			unsub()
			settleGoToWrites()
			if got := w.recorded(); len(got) != 0 {
				t.Fatalf("CCU writes after detach = %v, want none", got)
			}
		})
	})
}

// TestGoToDebounce_BlindAxesDebounceIndependently: lift and tilt hold
// separate pending-command slots — a tilt adjustment must not swallow a
// pending lift write and vice versa.
func TestGoToDebounce_BlindAxesDebounceIndependently(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		defer drainOptimisticRollbacks()
		w := &putWriter{}
		b := newBlindRig(t, "VCU3560967:1", w, custom.CoverCapabilities{SupportsTilt: true}, BlindKindHM)
		srv := b.MatterClusterServers()[0]

		if _, err := srv.MatterInvoke(context.Background(), matterCmdGoToLiftPercentage, uint16(3000)); err != nil {
			t.Fatalf("GoToLiftPercentage: %v", err)
		}
		if _, err := srv.MatterInvoke(context.Background(), matterCmdGoToTiltPercentage, uint16(2500)); err != nil {
			t.Fatalf("GoToTiltPercentage: %v", err)
		}
		settleGoToWrites()

		if cc := w.combinedCalls(); len(cc) != 2 {
			t.Fatalf("combined writes = %d, want 2 (one per axis slot)", len(cc))
		}
	})
}

// TestGoToDebounce_RealTimerFiresPendingWrite exercises the production
// path directly: schedule arms a time.AfterFunc whose callback passes
// the generation check in fire and runs the pending write.
func TestGoToDebounce_RealTimerFiresPendingWrite(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		var d goToDebouncer
		done := make(chan struct{})
		d.schedule(goToAxisLift, func() { close(done) })

		time.Sleep(goToDebounceGestureStart - time.Nanosecond)
		synctest.Wait()
		select {
		case <-done:
			t.Fatal("pending write fired before the gesture-start delay elapsed")
		default:
		}
		time.Sleep(time.Nanosecond)
		synctest.Wait()
		select {
		case <-done:
		default:
			t.Fatal("pending write did not fire once the gesture-start delay elapsed")
		}
	})
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
	synctest.Test(t, func(t *testing.T) {
		defer drainOptimisticRollbacks()
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

		settleGoToWrites()
		values := w.recorded()
		if len(values) != 1 || values[0].(float64) != 0.7 {
			t.Fatalf("deferred CCU write = %v, want [0.7] (Matter 3000 → HM 0.7)", values)
		}
	})
}
