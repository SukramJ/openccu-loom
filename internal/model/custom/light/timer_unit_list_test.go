// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package light

import (
	"context"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// unitsWithTenMilliseconds is the VALUES DURATION_UNIT / RAMP_TIME_UNIT
// VALUE_LIST of HmIP-RGBW, HmIP-BSL and HmIP-MP3P in godevccu's captured
// paramset descriptions; both are write-only (OPERATIONS 2) ENUMs there.
// unitsWithoutTenMilliseconds is the shorter list other HmIP channels
// declare (HmIP-ASIR-2 channel 3 DURATION_UNIT in the same capture).
var (
	unitsWithTenMilliseconds    = []string{"S", "M", "H", "10MS"}
	unitsWithoutTenMilliseconds = []string{"S", "M", "H"}
)

// unitOrdinal is the wire ordinal of label: its position in the list the
// channel declares, read from the list rather than assumed.
func unitOrdinal(t *testing.T, list []string, label string) int32 {
	t.Helper()
	i := slices.Index(list, label)
	if i < 0 {
		t.Fatalf("VALUE_LIST %v has no %q", list, label)
	}
	return int32(i) //nolint:gosec // a four-entry list
}

// wireRecorder keeps the last value of every parameter, whether it reached
// the wire as a single write or inside a paramset.
type wireRecorder struct {
	mu   sync.Mutex
	wire map[string]any
}

func (r *wireRecorder) record(p string, v any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.wire == nil {
		r.wire = map[string]any{}
	}
	r.wire[p] = v
}

func (r *wireRecorder) SetValue(_ context.Context, _ string, p hmenum.Parameter, v any, _ hmenum.CommandPriority) error {
	r.record(string(p), v)
	return nil
}

func (r *wireRecorder) PutParamset(_ context.Context, _ string, _ hmenum.ParamsetKey, values map[string]any, _ hmenum.CommandPriority) error {
	for p, v := range values {
		r.record(p, v)
	}
	return nil
}

func (r *wireRecorder) assertPair(t *testing.T, valueParam, unitParam hmenum.Parameter, wantValue, wantUnit int32) {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	v, vok := r.wire[string(valueParam)].(int32)
	u, uok := r.wire[string(unitParam)].(int32)
	if !vok || !uok || v != wantValue || u != wantUnit {
		t.Errorf("%s/%s = (%v, %v), want (%d, %d)", valueParam, unitParam,
			r.wire[string(valueParam)], r.wire[string(unitParam)], wantValue, wantUnit)
	}
}

// newTimerPairChannel builds a light channel carrying the two timer pairs
// as the HmIP value/unit families declare them: DURATION_VALUE and
// RAMP_TIME_VALUE as write-only INTEGER (MAX 16343), DURATION_UNIT and
// RAMP_TIME_UNIT as write-only ENUM over unitList.
func newTimerPairChannel(t *testing.T, address string, channelNo int, w Writer, unitList []string) *device.Channel {
	t.Helper()
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "TMR0001"})
	ch := d.AddChannel(address, channelNo, "DIMMER", hmenum.ParamsetKeyValues)
	putWritableFloat(ch, address, hmenum.ParameterLevel, w)
	key := func(p hmenum.Parameter) hmtypes.DataPointKey {
		return hmtypes.DataPointKey{ChannelAddress: address, ParamsetKey: hmenum.ParamsetKeyValues, Parameter: string(p)}
	}
	for _, p := range []hmenum.Parameter{hmenum.ParameterDurationValue, hmenum.ParameterRampTimeValue} {
		ch.Put(generic.NewInteger(generic.Spec{
			Key:        key(p),
			Descriptor: hmproto.ParameterData{Type: hmenum.ParameterTypeInteger, Operations: hmenum.OperationsWrite},
			Writer:     w,
		}))
	}
	for _, p := range []hmenum.Parameter{hmenum.ParameterDurationUnit, hmenum.ParameterRampTimeUnit} {
		ch.Put(generic.NewActionSelect(generic.Spec{
			Key: key(p),
			Descriptor: hmproto.ParameterData{
				Type: hmenum.ParameterTypeEnum, Operations: hmenum.OperationsWrite, ValueList: unitList,
			},
			Writer: w,
		}))
	}
	return ch
}

// TestLightTimersUseTenMillisecondsWhereTheChannelOffersIt drives every
// light encoder site — SetRampTime, SetOnTime and the two pairs TurnOnWith
// stages — on a channel whose unit lists offer 10MS. A sub-second duration
// has to reach the device as hundredths of a second; truncated to seconds it
// arrives as a zero count.
func TestLightTimersUseTenMillisecondsWhereTheChannelOffersIt(t *testing.T) {
	t.Parallel()
	tenMS := unitOrdinal(t, unitsWithTenMilliseconds, "10MS")
	const addr = "TMR0001:1"

	t.Run("SetRampTime and SetOnTime", func(t *testing.T) {
		t.Parallel()
		w := &wireRecorder{}
		l := New(Config{
			Channel: newTimerPairChannel(t, addr, 1, w, unitsWithTenMilliseconds), Writer: w,
			Capabilities: custom.LightCapabilities{Dimmable: true},
		})
		if err := l.SetRampTime(context.Background(), w, addr, 500*time.Millisecond, hmenum.CommandPriorityHigh); err != nil {
			t.Fatalf("SetRampTime: %v", err)
		}
		if err := l.SetOnTime(context.Background(), w, addr, 1500*time.Millisecond, hmenum.CommandPriorityHigh); err != nil {
			t.Fatalf("SetOnTime: %v", err)
		}
		w.assertPair(t, hmenum.ParameterRampTimeValue, hmenum.ParameterRampTimeUnit, 50, tenMS)
		w.assertPair(t, hmenum.ParameterDurationValue, hmenum.ParameterDurationUnit, 150, tenMS)
	})

	t.Run("TurnOnWith", func(t *testing.T) {
		t.Parallel()
		w := &wireRecorder{}
		l := New(Config{
			Channel: newTimerPairChannel(t, addr, 1, w, unitsWithTenMilliseconds), Writer: w,
			Capabilities: custom.LightCapabilities{Dimmable: true},
		})
		onTime, ramp := 700*time.Millisecond, 250*time.Millisecond
		if err := l.TurnOnWith(context.Background(), OnConfig{OnTime: &onTime, RampTime: &ramp}, hmenum.CommandPriorityHigh); err != nil {
			t.Fatalf("TurnOnWith: %v", err)
		}
		w.assertPair(t, hmenum.ParameterDurationValue, hmenum.ParameterDurationUnit, 70, tenMS)
		w.assertPair(t, hmenum.ParameterRampTimeValue, hmenum.ParameterRampTimeUnit, 25, tenMS)
	})
}

// TestLightTimersRoundToSecondsWithoutTenMilliseconds drives the same sites
// on a channel whose lists stop at H: the fourth ordinal does not exist
// there, so a sub-second duration rounds to the nearest whole second and
// never below one.
func TestLightTimersRoundToSecondsWithoutTenMilliseconds(t *testing.T) {
	t.Parallel()
	secs := unitOrdinal(t, unitsWithoutTenMilliseconds, "S")
	const addr = "TMR0001:1"
	w := &wireRecorder{}
	l := New(Config{
		Channel: newTimerPairChannel(t, addr, 1, w, unitsWithoutTenMilliseconds), Writer: w,
		Capabilities: custom.LightCapabilities{Dimmable: true},
	})

	if err := l.SetRampTime(context.Background(), w, addr, 500*time.Millisecond, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("SetRampTime: %v", err)
	}
	w.assertPair(t, hmenum.ParameterRampTimeValue, hmenum.ParameterRampTimeUnit, 1, secs)

	onTime := 1500 * time.Millisecond
	if err := l.TurnOnWith(context.Background(), OnConfig{OnTime: &onTime}, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("TurnOnWith: %v", err)
	}
	w.assertPair(t, hmenum.ParameterDurationValue, hmenum.ParameterDurationUnit, 2, secs)
}

// TestSoundPlayerLEDTimersUseTenMilliseconds drives the HmIP-MP3P LED
// channel's TurnOn, which stages both pairs itself. Channel 6 of the
// captured HmIP-MP3P declares both unit lists with 10MS.
func TestSoundPlayerLEDTimersUseTenMilliseconds(t *testing.T) {
	t.Parallel()
	tenMS := unitOrdinal(t, unitsWithTenMilliseconds, "10MS")
	const addr = "MP3P0001:6"
	w := &wireRecorder{}
	ch := newTimerPairChannel(t, addr, 6, w, unitsWithTenMilliseconds)
	putWritableSelect(ch, addr, hmenum.ParameterColor, w, fixedColorValueList)
	led := NewSoundPlayerLED(Config{Channel: ch, Writer: w})

	if err := led.TurnOn(context.Background(), LedOnConfig{OnTime: 0.5, RampTime: 0.25}, w, addr, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("TurnOn: %v", err)
	}
	w.assertPair(t, hmenum.ParameterDurationValue, hmenum.ParameterDurationUnit, 50, tenMS)
	w.assertPair(t, hmenum.ParameterRampTimeValue, hmenum.ParameterRampTimeUnit, 25, tenMS)
}
