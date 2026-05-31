// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package combined

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// ParameterDuration is the synthetic combined-DP parameter name used by
// [Timer] when CombinedParameter is left empty. Distinct from the wire
// DURATION_VALUE and DURATION_UNIT parameters so the combined DP can be
// attached to the same channel without colliding with the underlying
// generic DPs.
const ParameterDuration hmenum.Parameter = "DURATION"

// TimerUnit names the three unit slots the CCU's timer pairs use.
type TimerUnit int

// TimerUnit values. The numeric order matches the CCU enum (0 s, 1 m,
// 2 h) so the int32 cast stays safe.
const (
	TimerUnitSeconds TimerUnit = 0
	TimerUnitMinutes TimerUnit = 1
	TimerUnitHours   TimerUnit = 2
)

// timerUpperBoundSeconds is the threshold above which seconds are
// re-expressed as minutes, and analogously for minutes→hours.
const timerUpperBoundSeconds = 16343

// timerNotUsed is the sentinel value the CCU sends to signal that
// a timer is disabled. When this value is encountered, RecalcUnit
// returns it unchanged with TimerUnitHours so the CCU re-interprets
// the sentinel correctly without silent unit-conversion artefacts.
// Mirrors _TIMER_NOT_USED in mixins.py.
const timerNotUsed = 111600.0

// Timer combines a value + unit pair into one "seconds" value. On
// read it exposes the last observed seconds payload; on write it
// auto-selects the smallest unit that fits.
type Timer struct {
	Address string
	Writer  Writer

	ValueParameter hmenum.Parameter
	UnitParameter  hmenum.Parameter

	// CombinedParameter is the synthetic parameter name used as the
	// combined DP's identity. Defaults to the literal "DURATION" so it
	// never collides with a wire parameter (CCU uses DURATION_VALUE /
	// DURATION_UNIT).
	CombinedParameter hmenum.Parameter

	// InterfaceID completes the DataPointKey produced by
	// [DataPointKey]. Optional — defaults to empty so the key is
	// channel-local.
	InterfaceID string

	mu        sync.RWMutex
	seconds   float64
	observed  bool
	callbacks []func(old, next float64)
	// defaultSeconds stores the default duration in seconds captured from the
	// value parameter descriptor at Subscribe time. Returns 0 when no default
	// was found. Exposed via [Default].
	defaultSeconds float64
	hasDefault     bool
}

// NewTimer constructs a Timer.
func NewTimer(address string, w Writer, valueParam, unitParam hmenum.Parameter) *Timer {
	return &Timer{
		Address:           address,
		Writer:            w,
		ValueParameter:    valueParam,
		UnitParameter:     unitParam,
		CombinedParameter: ParameterDuration,
	}
}

// DataPointKey returns the combined DP's identity. Satisfies the
// [device.AttachableDataPoint] contract so the Timer can be attached to
// a channel via Channel.AttachCalculatedDataPoint.
func (t *Timer) DataPointKey() hmtypes.DataPointKey {
	param := t.CombinedParameter
	if param == "" {
		param = ParameterDuration
	}
	return hmtypes.DataPointKey{
		InterfaceID:    t.InterfaceID,
		ChannelAddress: t.Address,
		ParamsetKey:    hmenum.ParamsetKeyValues,
		Parameter:      string(param),
	}
}

// IsCombined satisfies the [device.CombinedDataPoint] marker interface
// so Channel.CombinedDataPoints surfaces the Timer.
func (t *Timer) IsCombined() bool { return true }

// timerWireDataPoint is the narrow contract Subscribe needs from a
// channel's generic VALUES paramset DP. Every generic.DataPoint[T]
// implements it through its RawValue / OnAnyUpdate methods.
type timerWireDataPoint interface {
	RawValue() (any, bool)
	OnAnyUpdate(fn func(old, next any)) func()
}

// timerDefaultProvider is an optional extension of [timerWireDataPoint]
// that exposes the parameter's raw descriptor. Used by Subscribe to capture
// the value parameter's default so [Timer.Default] can return it.
type timerDefaultProvider interface {
	Default() any
}

// Subscribe wires Timer to the channel's value + unit generic DPs.
// When either fires an OnAnyUpdate event, the latest raw value+unit
// pair is converted to seconds and pushed into OnComponents.
//
// Satisfies the [device.SubscribingDataPoint] contract; channels invoke
// it from AttachCalculatedDataPoint. Returns nil when either of the
// two underlying parameters is absent on the channel.
func (t *Timer) Subscribe(ch *device.Channel) func() {
	if ch == nil {
		return nil
	}
	valueDP, _ := any(ch.Parameter(t.ValueParameter)).(timerWireDataPoint)
	unitDP, _ := any(ch.Parameter(t.UnitParameter)).(timerWireDataPoint)
	if valueDP == nil || unitDP == nil {
		return nil
	}
	// Capture the value parameter's default at subscribe time so Default()
	// can return it. The default is stored as seconds using TimerUnitSeconds.
	if dp, ok := any(ch.Parameter(t.ValueParameter)).(timerDefaultProvider); ok {
		if def := dp.Default(); def != nil {
			if f, fok := toFloat64(def); fok {
				t.mu.Lock()
				t.defaultSeconds = f
				t.hasDefault = true
				t.mu.Unlock()
			}
		}
	}
	push := func() {
		rawValue, valueOK := valueDP.RawValue()
		rawUnit, unitOK := unitDP.RawValue()
		if !valueOK || !unitOK {
			return
		}
		v, vOK := toFloat64(rawValue)
		u, uOK := toTimerUnit(rawUnit)
		if !vOK || !uOK {
			return
		}
		t.OnComponents(v, u)
	}
	unsubValue := valueDP.OnAnyUpdate(func(_, _ any) { push() })
	unsubUnit := unitDP.OnAnyUpdate(func(_, _ any) { push() })
	// Seed the combined state immediately if both wire values are
	// already observed (e.g. after a cache hydration).
	push()
	return func() {
		if unsubValue != nil {
			unsubValue()
		}
		if unsubUnit != nil {
			unsubUnit()
		}
	}
}

func toFloat64(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case float32:
		return float64(x), true
	case int:
		return float64(x), true
	case int32:
		return float64(x), true
	case int64:
		return float64(x), true
	}
	return 0, false
}

func toTimerUnit(v any) (TimerUnit, bool) {
	switch x := v.(type) {
	case int:
		return TimerUnit(x), true
	case int32:
		return TimerUnit(x), true
	case int64:
		return TimerUnit(x), true
	case float64:
		return TimerUnit(int(x)), true
	}
	return TimerUnitSeconds, false
}

// Value returns the last observed duration.
func (t *Timer) Value() (time.Duration, bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	if !t.observed {
		return 0, false
	}
	return time.Duration(t.seconds) * time.Second, true
}

// DefaultDuration returns the default duration captured from the device's
// parameter descriptor at Subscribe time. Returns a zero Duration when no
// default was declared. Callers use this to reset the timer to the
// device-native default rather than leaving a previously set on-time active.
func (t *Timer) DefaultDuration() time.Duration {
	t.mu.RLock()
	defer t.mu.RUnlock()
	if !t.hasDefault {
		return 0
	}
	return time.Duration(t.defaultSeconds * float64(time.Second))
}

// ValueSeconds returns the last observed duration as a float64 seconds value
// — the canonical form exposed to north-bound adapters and the REST/WS API.
//
// Returns (0, false) when no value has been observed yet.
func (t *Timer) ValueSeconds() (float64, bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	if !t.observed {
		return 0, false
	}
	return t.seconds, true
}

// OnComponents feeds a (rawValue, unit) pair from the wire. The
// combined data point stores the computed seconds.
func (t *Timer) OnComponents(rawValue float64, unit TimerUnit) {
	seconds := toSeconds(rawValue, unit)
	t.mu.Lock()
	prev := t.seconds
	was := t.observed
	t.seconds = seconds
	t.observed = true
	t.mu.Unlock()
	if was && prev == seconds {
		return
	}
	t.fire(prev, seconds)
}

// SetDuration writes the duration back to the device, auto-selecting
// the unit. Durations longer than the value-parameter's representable
// range switch from seconds → minutes → hours.
func (t *Timer) SetDuration(ctx context.Context, d time.Duration, priority hmenum.CommandPriority) error {
	seconds := d.Seconds()
	value, unit := RecalcUnit(seconds)

	if err := t.Writer.SetValue(ctx, t.Address, t.UnitParameter, int32(unit), priority); err != nil { //nolint:gosec // unit in 0..2
		return fmt.Errorf("timer: UNIT: %w", err)
	}
	if err := t.Writer.SetValue(ctx, t.Address, t.ValueParameter, value, priority); err != nil {
		return fmt.Errorf("timer: VALUE: %w", err)
	}

	t.mu.Lock()
	prev := t.seconds
	was := t.observed
	t.seconds = seconds
	t.observed = true
	t.mu.Unlock()
	if !was || prev != seconds {
		t.fire(prev, seconds)
	}
	return nil
}

// OnUpdate registers a change handler. Returns an unsubscribe closure.
func (t *Timer) OnUpdate(fn func(old, next float64)) func() {
	t.mu.Lock()
	t.callbacks = append(t.callbacks, fn)
	idx := len(t.callbacks) - 1
	t.mu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			t.mu.Lock()
			defer t.mu.Unlock()
			if idx < len(t.callbacks) {
				t.callbacks[idx] = nil
			}
		})
	}
}

func (t *Timer) fire(prev, next float64) {
	t.mu.RLock()
	cbs := make([]func(old, next float64), len(t.callbacks))
	copy(cbs, t.callbacks)
	t.mu.RUnlock()
	for _, cb := range cbs {
		if cb != nil {
			cb(prev, next)
		}
	}
}

// StateUncertain reports whether the Timer's value is in an uncertain state,
// i.e. the combined value has never been observed from the CCU. Combined DPs
// have no optimistic write tracker; uncertain means unobserved.
func (t *Timer) StateUncertain() bool {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return !t.observed
}

// LoadDataPointValue triggers a CCU-side value refresh for both the value and
// unit parameters. The loader function is called with (channelAddress,
// parameterName) for each underlying data point. A nil loader is a no-op.
//
// For Timer, those are the value and unit parameters on the channel address.
func (t *Timer) LoadDataPointValue(loader func(channelAddress, parameter string)) {
	if loader == nil {
		return
	}
	loader(t.Address, string(t.ValueParameter))
	if t.UnitParameter != "" {
		loader(t.Address, string(t.UnitParameter))
	}
}

// SendDefault sends the default value for the timer. The default is
// determined by calling defaultFn for the value and unit parameters.
// If defaultFn returns (0, false) the parameter is skipped.
//
// Sends unit default first, then value default. In Go the caller
// supplies a ctx + writer via the Timer's [Writer] field; this
// convenience method uses the already-configured Writer.
//
// If a non-nil collector is passed, the writes are queued into it
// Instead of being executed immediately (mirrors
// `CallParameterCollector` pattern). In the current Go implementation
// the collector is represented by a simple accumulator callback.
func (t *Timer) SendDefault(
	ctx context.Context,
	defaultFn func(parameter string) (float64, bool),
	priority hmenum.CommandPriority,
) error {
	if defaultFn == nil || t.Writer == nil {
		return nil
	}
	if t.UnitParameter != "" {
		if v, ok := defaultFn(string(t.UnitParameter)); ok {
			if err := t.Writer.SetValue(ctx, t.Address, t.UnitParameter, int32(v), priority); err != nil { //nolint:gosec // unit in 0..2
				return fmt.Errorf("timer send_default UNIT: %w", err)
			}
		}
	}
	if v, ok := defaultFn(string(t.ValueParameter)); ok {
		if err := t.Writer.SetValue(ctx, t.Address, t.ValueParameter, v, priority); err != nil {
			return fmt.Errorf("timer send_default VALUE: %w", err)
		}
	}
	return nil
}

// RecalcUnit picks the smallest unit that keeps the value
// within the representable range. The promotion thresholds mirror
// the reference timer logic:
//
//	> 16343 s  → minutes
//	> 16343 min → hours
//
// The sentinel value 111600 (timerNotUsed) is returned unchanged with
// TimerUnitHours so the CCU re-interprets the "disabled" marker
// correctly without unit-conversion artefacts.
//
// loom:reachable:reason="called internally by Timer.Set to pick the correct unit before writing to the CCU"
func RecalcUnit(seconds float64) (float64, TimerUnit) {
	if seconds == timerNotUsed {
		return seconds, TimerUnitHours
	}
	if seconds < 0 {
		seconds = 0
	}
	if seconds <= timerUpperBoundSeconds {
		return seconds, TimerUnitSeconds
	}
	minutes := seconds / 60
	if minutes <= timerUpperBoundSeconds {
		return minutes, TimerUnitMinutes
	}
	return seconds / 3600, TimerUnitHours
}

func toSeconds(rawValue float64, unit TimerUnit) float64 {
	switch unit {
	case TimerUnitMinutes:
		return rawValue * 60
	case TimerUnitHours:
		return rawValue * 3600
	case TimerUnitSeconds:
		return rawValue
	}
	return rawValue
}
