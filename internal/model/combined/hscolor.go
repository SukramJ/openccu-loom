// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package combined

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/SukramJ/openccu-loom/internal/model/datapoint"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// hsColorKeyName is the canonical key segment used by [HSColor]'s
// promoted [datapoint.BaseDataPointFields.UniqueID]. Mirrors the
// `COMBINED/HSCOLOR` family identifier from
// surfaces the colour DP under a stable, family-prefixed token across
// MQTT / REST / WS adapters.
const hsColorKeyName = "COMBINED/HSCOLOR"

// HS carries a hue (°, 0..359) / saturation (%, 0..100) pair.
type HS struct {
	Hue        int32
	Saturation float64
}

// HSColor combines HUE and SATURATION into one logical colour value.
// On the wire HUE is an integer 0..359 and SATURATION a fraction
// 0.0..1.0; the combined data point exposes the 0..100 % form that
// Home-Assistant-flavoured consumers expect.
//
// HSColor embeds [datapoint.BaseDataPointFields] so the
// canonical [datapoint.BaseDataPointFields.UniqueID]
// [datapoint.BaseDataPointFields.Visible]
// [datapoint.BaseDataPointFields.SetForcedUsage]
// [datapoint.BaseDataPointFields.SetPublisher] surfaces are promoted
// into the type. The legacy [HSColor.Address] field is kept as a
// public field for callers that still read it directly; new code
// should prefer the promoted [datapoint.BaseDataPointFields.Address]
// accessor.
type HSColor struct {
	datapoint.BaseDataPointFields

	Address string
	Writer  Writer

	HueParameter        hmenum.Parameter
	SaturationParameter hmenum.Parameter

	mu          sync.RWMutex
	hue         int32
	hueObserved bool
	saturation  float64
	satObserved bool
	callbacks   []func(old, next HS)
}

// NewHSColor constructs an HSColor combining hue + saturation.
//
// This legacy signature drops the central-name segment of the unique
// identifier — callers that need a multi-CCU-safe identifier MUST use
// [NewHSColorWithCentral] instead. Existing call sites stay
// source-compatible; the promoted [datapoint.BaseDataPointFields]
// surface is initialised with an empty central.
//
// The multi-CCU form is what production uses — custom/light builds its
// colour data point through NewHSColorWithCentral.
func NewHSColor(address string, w Writer, hueParam, satParam hmenum.Parameter) *HSColor {
	return NewHSColorWithCentral("", address, w, hueParam, satParam)
}

// NewHSColorWithCentral is the multi-CCU-safe constructor. The
// promoted [datapoint.BaseDataPointFields] is wired with `central`
// scoping so the resulting [datapoint.BaseDataPointFields.UniqueID]
// shape is `<central>:<address>:COMBINED/HSCOLOR`. ADR 0002 (multi-
// CCU first-class) requires production callers to set `central`.
//
// Default-NoCreate gives `_get_data_point_usage()=NO_CREATE` for all
// combined DPs that do not explicitly set visible=True. The custom
// channel that owns this combined DP drives its visibility through
// the parent's CDPPrimary/CDPVisible usage, not through the combined
// sub-DP itself.
func NewHSColorWithCentral(centralName, address string, w Writer, hueParam, satParam hmenum.Parameter) *HSColor {
	c := &HSColor{
		BaseDataPointFields: datapoint.NewBaseDataPointFields(centralName, address, hsColorKeyName),
		Address:             address,
		Writer:              w,
		HueParameter:        hueParam,
		SaturationParameter: satParam,
	}
	c.SetForcedUsage(hmenum.DataPointUsageNoCreate)
	return c
}

// IsCombined satisfies the [device.CombinedDataPoint] marker interface
// so Channel.CombinedDataPoints surfaces the HSColor.
func (c *HSColor) IsCombined() bool { return true }

// DataPointKey returns the combined DP's identity. Satisfies the
// [device.AttachableDataPoint] contract so HSColor can be registered
// on a channel via Channel.AttachCalculatedDataPoint.
func (c *HSColor) DataPointKey() hmtypes.DataPointKey {
	return hmtypes.DataPointKey{
		ChannelAddress: c.Address,
		ParamsetKey:    hmenum.ParamsetKeyCombined,
		Parameter:      hsColorKeyName,
	}
}

// Subscribe wires HSColor to the channel's HUE and SATURATION generic DPs.
// When either fires an OnAnyUpdate event the new value is fed into OnHue /
// OnSaturation so the composite tracks the live CCU state. Returns nil when
// either source parameter is absent.
//
// Satisfies the [device.SubscribingDataPoint] contract; channels invoke it
// from AttachCalculatedDataPoint.
func (c *HSColor) Subscribe(ch *device.Channel) func() {
	if ch == nil {
		return nil
	}
	hueDP, _ := any(ch.Parameter(c.HueParameter)).(timerWireDataPoint)
	satDP, _ := any(ch.Parameter(c.SaturationParameter)).(timerWireDataPoint)
	if hueDP == nil || satDP == nil {
		return nil
	}
	unsubHue := hueDP.OnAnyUpdate(func(_, next any) {
		if v, ok := toInt32(next); ok {
			c.OnHue(v)
		}
	})
	unsubSat := satDP.OnAnyUpdate(func(_, next any) {
		if v, ok := toFloat64(next); ok {
			c.OnSaturation(v)
		}
	})
	// Seed with already-observed values so the composite is immediately
	// populated on reconnect without waiting for the next push event.
	if raw, ok := hueDP.RawValue(); ok {
		if v, ok2 := toInt32(raw); ok2 {
			c.OnHue(v)
		}
	}
	if raw, ok := satDP.RawValue(); ok {
		if v, ok2 := toFloat64(raw); ok2 {
			c.OnSaturation(v)
		}
	}
	return func() {
		if unsubHue != nil {
			unsubHue()
		}
		if unsubSat != nil {
			unsubSat()
		}
	}
}

// OnAnyUpdate satisfies the adapter.CombinedDataPoint interface. The typed
// HS value is JSON-encoded to a string so BridgeCombinedDataPoint can wrap
// it in a ParamValue and publish it on the event bus.
//
// Encoding goes through [EncodeHSJSON], the same renderer the combined
// state topic uses, so one value never reaches two planes spelled two
// ways. Measured difference between the two spellings: fmt's %g and
// encoding/json pick different exponent thresholds, so a saturation
// below 1e-4 on the 0..100 scale rendered as "1e-05" under one and
// "0.00001" under the other. Both parse to the same float64.
func (c *HSColor) OnAnyUpdate(fn func(old, next any)) func() {
	return c.OnUpdate(func(_, next HS) {
		fn(nil, EncodeHSJSON(next))
	})
}

// Value returns the current (hue, saturation) pair and whether both
// inputs have been observed.
func (c *HSColor) Value() (HS, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if !c.hueObserved || !c.satObserved {
		return HS{}, false
	}
	return HS{Hue: c.hue, Saturation: c.saturation * 100}, true
}

// OnHue feeds a new HUE observation (0..359, wraps modulo 360).
func (c *HSColor) OnHue(v int32) {
	v = ((v % 360) + 360) % 360
	now := time.Now()
	c.mu.Lock()
	prev, observed := c.snapshotLocked()
	c.hue = v
	c.hueObserved = true
	next, nowObserved := c.snapshotLocked()
	c.mu.Unlock()
	c.MarkRefreshed(now)
	if !observed || prev.Hue != next.Hue {
		c.MarkModified(now)
	}
	c.fire(observed, nowObserved, prev, next)
}

// OnSaturation feeds a new SATURATION observation (0.0..1.0).
func (c *HSColor) OnSaturation(v float64) {
	if v < 0 {
		v = 0
	}
	if v > 1 {
		v = 1
	}
	now := time.Now()
	c.mu.Lock()
	prev, observed := c.snapshotLocked()
	c.saturation = v
	c.satObserved = true
	next, nowObserved := c.snapshotLocked()
	c.mu.Unlock()
	c.MarkRefreshed(now)
	if !observed || prev.Saturation != next.Saturation {
		c.MarkModified(now)
	}
	c.fire(observed, nowObserved, prev, next)
}

// SetColor writes (hue, saturation) to the device. Hue wraps modulo
// 360; saturation is clamped into [0, 100] then converted to the
// 0..1 wire form.
func (c *HSColor) SetColor(ctx context.Context, hs HS, priority hmenum.CommandPriority) error {
	hue := ((hs.Hue % 360) + 360) % 360
	sat := hs.Saturation
	if sat < 0 {
		sat = 0
	}
	if sat > 100 {
		sat = 100
	}
	if err := c.Writer.SetValue(ctx, c.Address, c.HueParameter, hue, priority); err != nil {
		return fmt.Errorf("hscolor: HUE: %w", err)
	}
	if err := c.Writer.SetValue(ctx, c.Address, c.SaturationParameter, sat/100.0, priority); err != nil {
		return fmt.Errorf("hscolor: SATURATION: %w", err)
	}
	c.mu.Lock()
	prev, observed := c.snapshotLocked()
	c.hue = hue
	c.saturation = sat / 100.0
	c.hueObserved = true
	c.satObserved = true
	next, nowObserved := c.snapshotLocked()
	c.mu.Unlock()
	c.fire(observed, nowObserved, prev, next)
	return nil
}

// OnUpdate registers a change handler fired when both inputs are
// observed and the combined value changes. Returns an unsubscribe
// closure that is idempotent.
func (c *HSColor) OnUpdate(fn func(old, next HS)) func() {
	c.mu.Lock()
	c.callbacks = append(c.callbacks, fn)
	idx := len(c.callbacks) - 1
	c.mu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			c.mu.Lock()
			defer c.mu.Unlock()
			if idx < len(c.callbacks) {
				c.callbacks[idx] = nil
			}
		})
	}
}

// LoadDataPointValue triggers a CCU-side value refresh for both the hue and
// saturation parameters. The loader function is called with (channelAddress,
// parameterName) for each underlying data point. A nil loader is a no-op.
//
// For HSColor, those are the hue and saturation parameters on the channel
// address.
func (c *HSColor) LoadDataPointValue(loader func(channelAddress, parameter string)) {
	if loader == nil {
		return
	}
	loader(c.Address, string(c.HueParameter))
	loader(c.Address, string(c.SaturationParameter))
}

// SendDefault sends the default values for hue and saturation. The defaultFn
// receives the parameter name and returns (defaultValue, ok). When ok is
// false the parameter is skipped. A nil defaultFn or nil Writer is a no-op.
func (c *HSColor) SendDefault(
	ctx context.Context,
	defaultFn func(parameter string) (any, bool),
	priority hmenum.CommandPriority,
) error {
	if defaultFn == nil || c.Writer == nil {
		return nil
	}
	if v, ok := defaultFn(string(c.HueParameter)); ok {
		if err := c.Writer.SetValue(ctx, c.Address, c.HueParameter, v, priority); err != nil {
			return fmt.Errorf("hscolor send_default HUE: %w", err)
		}
	}
	if v, ok := defaultFn(string(c.SaturationParameter)); ok {
		if err := c.Writer.SetValue(ctx, c.Address, c.SaturationParameter, v, priority); err != nil {
			return fmt.Errorf("hscolor send_default SATURATION: %w", err)
		}
	}
	return nil
}

// IsRefreshed reports whether both HUE and SATURATION have been observed from
// the CCU at least once. Satisfies the custom.AggregateDataPoint contract.
func (c *HSColor) IsRefreshed() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.hueObserved && c.satObserved
}

// StateUncertain reports whether the combined colour value is held
// optimistically. HSColor has no optimistic tracker of its own.
// Returns false always. Satisfies the custom.AggregateDataPoint contract.
func (c *HSColor) StateUncertain() bool { return false }

// toInt32 converts an arbitrary CCU wire value to int32. Handles the types a
// JSON decoder produces plus the primitive Go counterparts (int/int32/int64/
// float64). Returns (0, false) for unrecognised types.
func toInt32(v any) (int32, bool) {
	switch x := v.(type) {
	case int32:
		return x, true
	case int:
		return int32(x), true //nolint:gosec // CCU hue is 0..359; narrowing is safe
	case int64:
		return int32(x), true //nolint:gosec // CCU hue is 0..359; narrowing is safe
	case float64:
		return int32(x), true //nolint:gosec // CCU hue is 0..359; narrowing is safe
	}
	return 0, false
}

// snapshotLocked returns the current (hs, observed) pair while the
// caller holds the mutex.
func (c *HSColor) snapshotLocked() (HS, bool) {
	if !c.hueObserved || !c.satObserved {
		return HS{}, false
	}
	return HS{Hue: c.hue, Saturation: c.saturation * 100}, true
}

// fire invokes the callbacks when observed transitions from false→true
// or when the value actually changed.
func (c *HSColor) fire(wasObserved, nowObserved bool, prev, next HS) {
	if !nowObserved {
		return
	}
	if wasObserved && prev == next {
		return
	}
	c.mu.RLock()
	cbs := make([]func(old, next HS), len(c.callbacks))
	copy(cbs, c.callbacks)
	c.mu.RUnlock()
	for _, cb := range cbs {
		if cb != nil {
			cb(prev, next)
		}
	}
}
