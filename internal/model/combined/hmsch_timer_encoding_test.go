// SPDX-License-Identifier: MIT
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
// It is the largest whole-second duration [custom.EncodeTimerDuration] still
// expresses at the seconds unit; one second more promotes to minutes.
func hmSchDurationValueMaxPerUnit(t *testing.T) int64 {
	t.Helper()
	lo, hi := int64(1), int64(1)<<31
	if _, unit := custom.EncodeTimerDuration(time.Duration(lo) * time.Second); unit != int32(hmenum.TimerUnitSeconds) {
		t.Fatalf("the encoder promotes even 1 s away from the seconds unit — the probe lost its subject")
	}
	if _, unit := custom.EncodeTimerDuration(time.Duration(hi) * time.Second); unit == int32(hmenum.TimerUnitSeconds) {
		t.Fatalf("the encoder never promotes below %d s — the probe lost its subject", hi)
	}
	for lo+1 < hi {
		mid := lo + (hi-lo)/2
		if _, unit := custom.EncodeTimerDuration(time.Duration(mid) * time.Second); unit == int32(hmenum.TimerUnitSeconds) {
			lo = mid
			continue
		}
		hi = mid
	}
	return lo
}

// TestHmSchRecalcUnitEncodesLikeTheServicePathEncoder pins the two encoders of
// the CCU's (DURATION_VALUE, DURATION_UNIT) pair against each other.
//
// Both write the same two parameters on the same channel: custom.EncodeTimerDuration
// on the service path (Siren.TurnOn and friends) and [RecalcUnit] on the
// combined MQTT write path. They disagreed on the value — RecalcUnit promoted
// in float64 and staged the fraction on a parameter the device declares
// INTEGER, so one requested duration reached the device as a different number
// depending on which path carried it.
//
// The cases are the two the divergence was found on plus the boundaries the
// promotion chain turns at.
func TestHmSchRecalcUnitEncodesLikeTheServicePathEncoder(t *testing.T) {
	t.Parallel()

	perUnitMax := hmSchDurationValueMaxPerUnit(t)
	for _, seconds := range []float64{
		0, 1, 1.5, 30, 61,
		float64(perUnitMax), float64(perUnitMax + 1),
		16373, 100000, float64(perUnitMax) * 60, float64(perUnitMax)*60 + 1,
		custom.TimerNotUsed,
	} {
		wantValue, wantUnit := custom.EncodeTimerDuration(time.Duration(seconds * float64(time.Second)))
		gotValue, gotUnit := RecalcUnit(seconds)
		if gotValue != float64(wantValue) || int32(gotUnit) != wantUnit {
			t.Errorf("RecalcUnit(%v) = (%v, %d); custom.EncodeTimerDuration encodes the "+
				"same duration as (%d, %d) — one wire pair, one rule",
				seconds, gotValue, int32(gotUnit), wantValue, wantUnit)
		}
		if gotValue != float64(int64(gotValue)) {
			t.Errorf("RecalcUnit(%v) staged %v on DURATION_VALUE, which the device "+
				"declares INTEGER", seconds, gotValue)
		}
	}
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
	if got != 1 {
		t.Errorf("1.5 s staged as %d, want 1 (truncated toward zero at the write "+
			"boundary, as on the service path)", got)
	}
}

// TestHmSchTimerMaxIsAReachableSecondsCeiling pins what [Timer.Max] means.
//
// 16343 is DURATION_VALUE's per-unit INTEGER maximum — the count, paired with
// DURATION_UNIT ∈ {S, M, H} — not a number of seconds (HMIPServer
// de.eq3.cbcs.devicedescription.channelspecification.stateparameter.GeneralStateParameterFactory#createDurationValueParameter).
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
	value, unit := custom.EncodeTimerDuration(time.Duration(got) * time.Second)
	if int64(value) != perUnitMax || unit != int32(hmenum.TimerUnitMinutes) {
		t.Errorf("the published maximum %v s encodes as (%d, %d), which is not the "+
			"largest count at the minutes unit", got, value, unit)
	}
}
