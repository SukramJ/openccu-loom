// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package combined

import (
	"context"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// hmSchDurationValueMaxPerUnit derives DURATION_VALUE's per-unit maximum by
// probing the encoder that owns the rule, rather than restating the number.
// It is the largest whole-second duration [custom.DurationUnits.Encode] still
// expresses at the seconds unit; one second more promotes to minutes.
func hmSchDurationValueMaxPerUnit(t *testing.T) int64 {
	t.Helper()
	lo, hi := int64(1), int64(1)<<31
	if _, unit := (custom.DurationUnits{}).Encode(time.Duration(lo) * time.Second); unit != int32(hmenum.TimerUnitSeconds) {
		t.Fatalf("the encoder promotes even 1 s away from the seconds unit — the probe lost its subject")
	}
	if _, unit := (custom.DurationUnits{}).Encode(time.Duration(hi) * time.Second); unit == int32(hmenum.TimerUnitSeconds) {
		t.Fatalf("the encoder never promotes below %d s — the probe lost its subject", hi)
	}
	for lo+1 < hi {
		mid := lo + (hi-lo)/2
		if _, unit := (custom.DurationUnits{}).Encode(time.Duration(mid) * time.Second); unit == int32(hmenum.TimerUnitSeconds) {
			lo = mid
			continue
		}
		hi = mid
	}
	return lo
}

// TestHmSchTimerWritesAnIntegerDurationValue is the wire half: the value
// reaches the writer as an integer type, not as a float64 the transport would
// encode as <double> for a parameter declared INTEGER.
func TestHmSchTimerWritesAnIntegerDurationValue(t *testing.T) {
	t.Parallel()

	w := &stubWriter{}
	tm := NewTimer("VCU0000001:1", w, hmenum.ParameterDurationValue, hmenum.ParameterDurationUnit)
	if err := tm.SetDuration(context.Background(), 1500*time.Millisecond, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("SetDuration: %v", err)
	}
	v, ok := w.find(hmenum.ParameterDurationValue)
	if !ok {
		t.Fatal("SetDuration wrote no DURATION_VALUE")
	}
	got, isInt := v.(int32)
	if !isInt {
		t.Fatalf("DURATION_VALUE staged as %T (%v); DURATION_VALUE is an INTEGER "+
			"parameter and the write path applies no coercion of its own", v, v)
	}
	if got != 2 {
		t.Errorf("1.5 s staged as %d, want 2 (rounded half-up to the nearest second "+
			"on a channel without a declared unit list, as on the service path)", got)
	}
}

// TestHmSchTimerMaxIsAReachableSecondsCeiling pins what [Timer.Max] means.
//
// 16343 is DURATION_VALUE's per-unit INTEGER maximum — the count, paired with
// a DURATION_UNIT ENUM that starts S, M, H — not a number of seconds: every HmIP
// paramset description in the descriptor corpus that declares DURATION_VALUE
// declares MAX 16343.
// Publishing it as a maximum in seconds capped the data point at 4 h 32 min.
//
// The ceiling is derived here rather than restated: the largest count at the
// coarsest unit the promotion chain will still choose for a finite duration.
func TestHmSchTimerMaxIsAReachableSecondsCeiling(t *testing.T) {
	t.Parallel()

	perUnitMax := hmSchDurationValueMaxPerUnit(t)
	tm := NewTimer("VCU0000001:1", &stubWriter{}, hmenum.ParameterDurationValue, hmenum.ParameterDurationUnit)
	got, ok := tm.Max()
	if !ok {
		t.Fatal("Timer.Max reports no maximum")
	}
	want := float64(perUnitMax * 60)
	if got != want {
		t.Errorf("Timer.Max() = %v s, want %v s (%d counts at the minutes unit); "+
			"%d is the per-unit INTEGER maximum of DURATION_VALUE, not a duration",
			got, want, perUnitMax, perUnitMax)
	}
	value, unit := (custom.DurationUnits{}).Encode(time.Duration(got) * time.Second)
	if int64(value) != perUnitMax || unit != int32(hmenum.TimerUnitMinutes) {
		t.Errorf("the published maximum %v s encodes as (%d, %d), which is not the "+
			"largest count at the minutes unit", got, value, unit)
	}
}
