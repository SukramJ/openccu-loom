// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package combined

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/SukramJ/openccu-loom/internal/model/datapoint"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
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
// No production caller exists today: custom/light implements colour
// handling inline via its own HSColor struct. This constructor is kept
// so the combined package remains a coherent, testable unit; a future
// refactor may wire it once materialiseCombinedDataPoints is added.
// See docs/parity/by_design.md BD-A3-CombinedUnused.
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
func NewHSColorWithCentral(central, address string, w Writer, hueParam, satParam hmenum.Parameter) *HSColor {
	c := &HSColor{
		BaseDataPointFields: datapoint.NewBaseDataPointFields(central, address, hsColorKeyName),
		Address:             address,
		Writer:              w,
		HueParameter:        hueParam,
		SaturationParameter: satParam,
	}
	c.SetForcedUsage(hmenum.DataPointUsageNoCreate)
	return c
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
// the CCU at least once. Implements M18.
func (c *HSColor) IsRefreshed() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.hueObserved && c.satObserved
}

// StateUncertain reports whether the combined colour value is held
// optimistically. HSColor has no optimistic tracker of its own.
// Returns false always. Implements M18.
func (c *HSColor) StateUncertain() bool { return false }

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
