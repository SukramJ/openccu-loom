// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package combined_test

import (
	"context"
	"math"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/model/combined"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// unitsWithTenMilliseconds is the VALUES DURATION_UNIT / RAMP_TIME_UNIT
// VALUE_LIST of HmIP-RGBW, HmIP-BSL and HmIP-MP3P in godevccu's captured
// paramset descriptions. unitsWithoutTenMilliseconds is DURATION_UNIT on
// HmIP-ASIR-2 channel 3 in the same capture.
var (
	unitsWithTenMilliseconds    = []string{"S", "M", "H", "10MS"}
	unitsWithoutTenMilliseconds = []string{"S", "M", "H"}
)

// unitOrdinal is the wire ordinal of label: its position in the list the
// channel declares. It is read from the list, never assumed.
func unitOrdinal(t *testing.T, list []string, label string) int32 {
	t.Helper()
	i := slices.Index(list, label)
	if i < 0 {
		t.Fatalf("VALUE_LIST %v has no %q", list, label)
	}
	return int32(i) //nolint:gosec // a four-entry list
}

// pairRecorder records the last value written to each parameter.
type pairRecorder struct {
	mu     sync.Mutex
	values map[hmenum.Parameter]any
}

func (r *pairRecorder) SetValue(_ context.Context, _ string, p hmenum.Parameter, v any, _ hmenum.CommandPriority) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.values == nil {
		r.values = map[hmenum.Parameter]any{}
	}
	r.values[p] = v
	return nil
}

func (r *pairRecorder) pair(t *testing.T) (value, unit int32) {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	v, vok := r.values[hmenum.ParameterDurationValue].(int32)
	u, uok := r.values[hmenum.ParameterDurationUnit].(int32)
	if !vok || !uok {
		t.Fatalf("DURATION pair not written as int32: %#v", r.values)
	}
	return v, u
}

// timerRig is a channel whose DURATION_UNIT declares unitList, with a
// subscribed combined timer on it. The wire data points are readable so
// the read half can be driven: a device, or another client, that reports
// a pair must read back as the duration it means.
type timerRig struct {
	timer *combined.Timer
	value *generic.Integer
	unit  *generic.Select
	w     *pairRecorder
}

func newTimerRig(t *testing.T, unitList []string) *timerRig {
	t.Helper()
	const addr = "VCU0000001:1"
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "VCU0000001"})
	ch := d.AddChannel(addr, 1, "DIMMER", hmenum.ParamsetKeyValues)
	key := func(p hmenum.Parameter) hmtypes.DataPointKey {
		return hmtypes.DataPointKey{ChannelAddress: addr, ParamsetKey: hmenum.ParamsetKeyValues, Parameter: string(p)}
	}
	rwe := hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent
	value := generic.NewInteger(generic.Spec{
		Key:        key(hmenum.ParameterDurationValue),
		Descriptor: hmproto.ParameterData{Type: hmenum.ParameterTypeInteger, Operations: rwe},
	})
	unit := generic.NewSelect(generic.Spec{
		Key:        key(hmenum.ParameterDurationUnit),
		Descriptor: hmproto.ParameterData{Type: hmenum.ParameterTypeEnum, Operations: rwe, ValueList: unitList},
	})
	ch.Put(value)
	ch.Put(unit)
	w := &pairRecorder{}
	tm := combined.NewTimer(addr, w, hmenum.ParameterDurationValue, hmenum.ParameterDurationUnit)
	unsub := tm.Subscribe(ch)
	if unsub == nil {
		t.Fatal("timer did not subscribe to the DURATION pair")
	}
	t.Cleanup(unsub)
	return &timerRig{timer: tm, value: value, unit: unit, w: w}
}

func (r *timerRig) report(value, unit int32) {
	r.value.OnEvent(value)
	r.unit.OnEvent(unit)
}

// TestTimerReadsATenMillisecondPairAsItsDuration drives the read half: a
// pair reported in 10MS is a count of hundredths of a second. Read as
// seconds, (50, 10MS) shows as 50 s instead of 0.5 s.
func TestTimerReadsATenMillisecondPairAsItsDuration(t *testing.T) {
	t.Parallel()
	cases := []struct {
		count int32
		want  time.Duration
	}{
		{50, 500 * time.Millisecond},
		{150, 1500 * time.Millisecond},
		{16343, 163430 * time.Millisecond},
	}
	for _, c := range cases {
		r := newTimerRig(t, unitsWithTenMilliseconds)
		r.report(c.count, unitOrdinal(t, unitsWithTenMilliseconds, "10MS"))

		secs, ok := r.timer.ValueSeconds()
		if !ok || math.Abs(secs-c.want.Seconds()) > 1e-9 {
			t.Errorf("(%d, 10MS): ValueSeconds = %v (observed %v), want %v", c.count, secs, ok, c.want.Seconds())
		}
		got, ok := r.timer.Value()
		if !ok || got != c.want {
			t.Errorf("(%d, 10MS): Value = %v (observed %v), want %v — the fraction of a second is dropped",
				c.count, got, ok, c.want)
		}
	}
}

// TestTimerSetDurationEncodesByTheChannelsUnitList pins the encoder rule
// for every unit list: 10MS only where the channel declares it, only for a
// duration that is not a whole second, and only while the count fits; every
// unit rounds half-up to its nearest step, and a positive duration never
// collapses to a zero count.
func TestTimerSetDurationEncodesByTheChannelsUnitList(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		list      []string
		d         time.Duration
		wantValue int32
		wantUnit  string
	}{
		{"half second where 10MS is offered", unitsWithTenMilliseconds, 500 * time.Millisecond, 50, "10MS"},
		{"one and a half seconds where 10MS is offered", unitsWithTenMilliseconds, 1500 * time.Millisecond, 150, "10MS"},
		{"largest 10MS count", unitsWithTenMilliseconds, 163430 * time.Millisecond, 16343, "10MS"},
		{"past the 10MS range falls back to seconds", unitsWithTenMilliseconds, 163440 * time.Millisecond, 163, "S"},
		{"past the 10MS range rounds half-up", unitsWithTenMilliseconds, 200500 * time.Millisecond, 201, "S"},
		{"a whole second stays in seconds", unitsWithTenMilliseconds, 5 * time.Second, 5, "S"},
		{"below one 10MS step is one step", unitsWithTenMilliseconds, 4 * time.Millisecond, 1, "10MS"},
		{"minutes round half-up", unitsWithTenMilliseconds, 16373 * time.Second, 273, "M"},

		{"half second without 10MS is one second", unitsWithoutTenMilliseconds, 500 * time.Millisecond, 1, "S"},
		{"seconds round half-up", unitsWithoutTenMilliseconds, 1500 * time.Millisecond, 2, "S"},
		{"seconds round down below the half", unitsWithoutTenMilliseconds, 1400 * time.Millisecond, 1, "S"},
		{"a few milliseconds without 10MS is one second", unitsWithoutTenMilliseconds, 4 * time.Millisecond, 1, "S"},
		{"minutes round half-up without 10MS", unitsWithoutTenMilliseconds, 16373 * time.Second, 273, "M"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			r := newTimerRig(t, c.list)
			if err := r.timer.SetDuration(context.Background(), c.d, hmenum.CommandPriorityHigh); err != nil {
				t.Fatalf("SetDuration: %v", err)
			}
			value, unit := r.w.pair(t)
			wantUnit := unitOrdinal(t, c.list, c.wantUnit)
			if value != c.wantValue || unit != wantUnit {
				t.Errorf("SetDuration(%v) on %v wrote (%d, %d), want (%d, %d = %s)",
					c.d, c.list, value, unit, c.wantValue, wantUnit, c.wantUnit)
			}
			if int(unit) >= len(c.list) {
				t.Errorf("SetDuration(%v) wrote ordinal %d, outside the channel's list %v", c.d, unit, c.list)
			}
		})
	}
}

// TestTimerRoundTripsADurationWithinHalfAStep encodes through SetDuration,
// feeds the written pair back as a wire report, and reads it. Rounding to the
// nearest step keeps the error within half a step of the unit the channel's
// own list makes available for that duration.
func TestTimerRoundTripsADurationWithinHalfAStep(t *testing.T) {
	t.Parallel()
	cases := []struct {
		list []string
		d    time.Duration
		tol  time.Duration
	}{
		{unitsWithTenMilliseconds, 700 * time.Millisecond, 5 * time.Millisecond},
		{unitsWithTenMilliseconds, 1250 * time.Millisecond, 5 * time.Millisecond},
		{unitsWithTenMilliseconds, 163430 * time.Millisecond, 5 * time.Millisecond},
		{unitsWithTenMilliseconds, 200700 * time.Millisecond, 500 * time.Millisecond},
		{unitsWithTenMilliseconds, 16373 * time.Second, 30 * time.Second},
		{unitsWithoutTenMilliseconds, 700 * time.Millisecond, 500 * time.Millisecond},
		{unitsWithoutTenMilliseconds, 16373 * time.Second, 30 * time.Second},
		{unitsWithoutTenMilliseconds, 5 * time.Hour, 30 * time.Second},
	}
	for _, c := range cases {
		r := newTimerRig(t, c.list)
		if err := r.timer.SetDuration(context.Background(), c.d, hmenum.CommandPriorityHigh); err != nil {
			t.Fatalf("SetDuration(%v): %v", c.d, err)
		}
		value, unit := r.w.pair(t)
		// A fresh rig reads the pair as a device report, so the value the
		// write path cached cannot stand in for the decode.
		back := newTimerRig(t, c.list)
		back.report(value, unit)
		got, ok := back.timer.ValueSeconds()
		if !ok {
			t.Fatalf("(%d, %d) was not observed", value, unit)
		}
		if diff := math.Abs(got - c.d.Seconds()); diff > c.tol.Seconds()+1e-9 {
			t.Errorf("%v on %v → (%d, %d) → %vs: off by %vs, want ≤ %v",
				c.d, c.list, value, unit, got, diff, c.tol)
		}
	}
}
