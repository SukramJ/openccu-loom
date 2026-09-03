// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package combined

import (
	"context"
	"fmt"
	"math"
	"sync"
	"time"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
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

// timerValueMaxPerUnit is DURATION_VALUE's maximum as an INTEGER count in
// whichever unit DURATION_UNIT names — not a number of seconds. The CCU
// declares it once, in the single factory every DURATION_VALUE-bearing channel
// goes through: LogicalType.INTEGER, default 0, min 0, max 16343, paired with
// a DURATION_UNIT ENUM over {S, M, H} (HMIPServer
// de.eq3.cbcs.devicedescription.channelspecification.stateparameter.GeneralStateParameterFactory#createDurationValueParameter).
//
// It is therefore also the threshold at which the encoder promotes to the next
// coarser unit — a count above it has no representation in the current one.
// The promotion itself lives in [custom.EncodeTimerDuration]; this constant is
// kept only to express the seconds ceiling in [Timer.Max], and
// TestHmSchTimerMaxIsAReachableSecondsCeiling re-derives it from that encoder
// rather than trusting the two copies to stay equal.
const timerValueMaxPerUnit = 16343

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
	// can return it. The default is stored as seconds using hmenum.TimerUnitSeconds.
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

func toTimerUnit(v any) (hmenum.TimerUnit, bool) {
	switch x := v.(type) {
	case int:
		return hmenum.TimerUnit(x), true
	case int32:
		return hmenum.TimerUnit(x), true
	case int64:
		return hmenum.TimerUnit(x), true
	case float64:
		return hmenum.TimerUnit(int(x)), true
	}
	return hmenum.TimerUnitSeconds, false
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
func (t *Timer) OnComponents(rawValue float64, unit hmenum.TimerUnit) {
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
//
// The count goes on the wire as an int32: DURATION_VALUE is declared INTEGER,
// and nothing between here and the transport coerces it — a float64 staged
// here would be encoded as a <double> on a parameter the device declares as an
// integer.
func (t *Timer) SetDuration(ctx context.Context, d time.Duration, priority hmenum.CommandPriority) error {
	seconds := d.Seconds()
	value, unit := custom.EncodeTimerDuration(d)

	if err := t.Writer.SetValue(ctx, t.Address, t.UnitParameter, unit, priority); err != nil {
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
			if err := t.Writer.SetValue(ctx, t.Address, t.UnitParameter, int32(v), priority); err != nil { //nolint:gosec // unit in 0..2; see #20
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

// RecalcUnit expresses a duration in seconds as the CCU's (DURATION_VALUE,
// DURATION_UNIT) pair: the smallest unit whose count stays within
// [timerValueMaxPerUnit], with the count truncated toward zero because
// DURATION_VALUE is an INTEGER parameter.
//
// It is the same rule [custom.EncodeTimerDuration] applies on the service
// path, and it delegates to it rather than restating it. The two used to be
// separate implementations that agreed on the unit and disagreed on the value
// — this one promoted in floating point and staged the fraction on the wire,
// so 16373 s reached the device as 272.88 minutes here and as 272 minutes
// there, for one requested duration. The sentinel [custom.TimerNotUsed] and
// the negative case come from that encoder too.
//
// The returned value is a float64 only because the combined DP's seconds
// domain is one; it always holds a whole count.
//
// loom:reachable:reason="called by Timer.SetDuration to pick the correct unit before writing to the CCU"
func RecalcUnit(seconds float64) (float64, hmenum.TimerUnit) {
	value, unit := custom.EncodeTimerDuration(secondsToDuration(seconds))
	return float64(value), hmenum.TimerUnit(unit)
}

// secondsToDuration converts a seconds total to a [time.Duration], saturating
// rather than wrapping: a nanosecond count past the int64 range would come
// back as a negative duration and read as "no duration at all".
func secondsToDuration(seconds float64) time.Duration {
	const maxSeconds = float64(math.MaxInt64) / float64(time.Second)
	if seconds >= maxSeconds {
		return time.Duration(math.MaxInt64)
	}
	if seconds <= -maxSeconds {
		return time.Duration(math.MinInt64)
	}
	return time.Duration(seconds * float64(time.Second))
}

func toSeconds(rawValue float64, unit hmenum.TimerUnit) float64 {
	switch unit {
	case hmenum.TimerUnitMinutes:
		return rawValue * 60
	case hmenum.TimerUnitHours:
		return rawValue * 3600
	case hmenum.TimerUnitSeconds:
		return rawValue
	}
	return rawValue
}
