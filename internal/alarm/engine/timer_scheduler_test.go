// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package engine_test

import (
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/SukramJ/openccu-loom/internal/alarm/engine"
)

// The production scheduler runs on runtime timers; the bubble clock
// lets both sides of the deadline be pinned exactly.
func TestTimerSchedulerFiresExactlyAtTheDeadline(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var fired atomic.Int32
		cancel := engine.NewTimerScheduler().Schedule(30*time.Second, func() { fired.Add(1) })
		defer cancel()

		time.Sleep(30*time.Second - time.Nanosecond)
		synctest.Wait()
		if got := fired.Load(); got != 0 {
			t.Fatalf("callback fired %d time(s) 1ns before its deadline", got)
		}

		time.Sleep(time.Nanosecond)
		synctest.Wait()
		if got := fired.Load(); got != 1 {
			t.Fatalf("callback fired %d time(s) at its deadline, want 1", got)
		}

		time.Sleep(time.Hour)
		synctest.Wait()
		if got := fired.Load(); got != 1 {
			t.Fatalf("callback fired %d time(s) in total, want exactly 1", got)
		}
	})
}

func TestTimerSchedulerCancelPreventsTheCallback(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var fired atomic.Int32
		cancel := engine.NewTimerScheduler().Schedule(time.Minute, func() { fired.Add(1) })

		time.Sleep(time.Minute - time.Nanosecond)
		cancel()
		cancel() // a second cancel is a no-op
		time.Sleep(time.Hour)
		synctest.Wait()
		if got := fired.Load(); got != 0 {
			t.Fatalf("cancelled callback fired %d time(s)", got)
		}
	})
}

func TestTimerSchedulerCancelAfterFiringIsHarmless(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var fired atomic.Int32
		cancel := engine.NewTimerScheduler().Schedule(time.Second, func() { fired.Add(1) })

		time.Sleep(time.Second)
		synctest.Wait()
		cancel()
		synctest.Wait()
		if got := fired.Load(); got != 1 {
			t.Fatalf("callback fired %d time(s), want 1", got)
		}
	})
}
