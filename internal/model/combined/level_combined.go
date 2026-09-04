// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package combined

import (
	"sync"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/internal/model/datapoint"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// levelCombinedKeyName is the canonical key segment used by
// [LevelCombined]'s promoted [datapoint.BaseDataPointFields.UniqueID].
// Matches the family-prefixed token convention shared with
// `COMBINED/HSCOLOR` and `COMBINED/WEEKPROFILE` so north-bound
// adapters can dispatch by family regardless of the per-channel
// LEVEL/LEVEL_2/LEVEL_COMBINED parameter triple.
const levelCombinedKeyName = "COMBINED/LEVEL_COMBINED"

// LevelComposite bundles the two 0..1 fractions a shade-with-slats
// device carries on a single channel.
type LevelComposite struct {
	Level      custom.Position
	SlatsLevel custom.Position
}

// LevelCombined is the read-side aggregate of a shade's LEVEL and
// LEVEL_2 (slats): it subscribes to both generic data points and
// surfaces the pair as one composite on the event bus. It carries no
// write path. The atomic LEVEL_COMBINED / COMBINED_PARAMETER write is
// Blind.SetCombined in internal/model/custom/cover, which also owns the
// command lock, the per-axis inversion, the stop-if-moving guard and
// the staging of the unconfirmed values.
//
// LevelCombined embeds [datapoint.BaseDataPointFields] (V4 fix in
// PR-32) so the canonical [datapoint.BaseDataPointFields.UniqueID]
// [datapoint.BaseDataPointFields.Visible]
// [datapoint.BaseDataPointFields.SetForcedUsage] surfaces are
// promoted. The constructor force-marks
// [hmenum.DataPointUsageNoCreate] because LevelCombined is consumed
// internally by [cover.Cover] and should not surface as a top-level
// entity (mirrors the [WeekProfile] default-NoCreate rationale).
type LevelCombined struct {
	datapoint.BaseDataPointFields

	Address string

	LevelParameter hmenum.Parameter
	SlatsParameter hmenum.Parameter

	mu         sync.RWMutex
	level      custom.Position
	hasLevel   bool
	slatsLevel custom.Position
	hasSlats   bool
	callbacks  []func(old, next LevelComposite)
}

// NewLevelCombined constructs a LevelCombined with no central-name
// scoping. The promoted [datapoint.BaseDataPointFields.UniqueID]
// renders as `:<address>:COMBINED/LEVEL_COMBINED`. Multi-CCU-safe
// call sites MUST use [NewLevelCombinedWithCentral].
//
// The multi-CCU form is what production uses — custom/cover builds its
// level data point through NewLevelCombinedWithCentral.
//
// levelParam / slatsParam are the per-channel reading parameters
// (LEVEL and LEVEL_2) the aggregate subscribes to.
func NewLevelCombined(address string, levelParam, slatsParam hmenum.Parameter) *LevelCombined {
	return NewLevelCombinedWithCentral("", address, levelParam, slatsParam)
}

// NewLevelCombinedWithCentral is the multi-CCU-safe constructor. The
// promoted [datapoint.BaseDataPointFields] is wired with `central`
// scoping so the resulting UniqueID shape is
// `<central>:<address>:COMBINED/LEVEL_COMBINED`. ADR 0002 (multi-CCU
// first-class) requires production callers to set `central`.
//
// The constructor force-marks [hmenum.DataPointUsageNoCreate];
// callers that want LevelCombined surfaced as a regular DP must
// explicitly call SetForcedUsage with a visible usage.
func NewLevelCombinedWithCentral(
	centralName, address string,
	levelParam, slatsParam hmenum.Parameter,
) *LevelCombined {
	lc := &LevelCombined{
		BaseDataPointFields: datapoint.NewBaseDataPointFields(centralName, address, levelCombinedKeyName),
		Address:             address,
		LevelParameter:      levelParam,
		SlatsParameter:      slatsParam,
	}
	lc.SetForcedUsage(hmenum.DataPointUsageNoCreate)
	return lc
}

// IsCombined satisfies the [device.CombinedDataPoint] marker interface
// so Channel.CombinedDataPoints surfaces the LevelCombined.
func (l *LevelCombined) IsCombined() bool { return true }

// DataPointKey returns the combined DP's identity. Satisfies the
// [device.AttachableDataPoint] contract so LevelCombined can be
// registered on a channel via Channel.AttachCalculatedDataPoint.
func (l *LevelCombined) DataPointKey() hmtypes.DataPointKey {
	return hmtypes.DataPointKey{
		ChannelAddress: l.Address,
		ParamsetKey:    hmenum.ParamsetKeyCombined,
		Parameter:      levelCombinedKeyName,
	}
}

// Subscribe wires LevelCombined to the channel's LEVEL and LEVEL_2 generic
// DPs. When either fires an OnAnyUpdate event the new float value is fed into
// OnLevel / OnSlatsLevel so the composite tracks the live CCU state. Returns
// nil when either of the two source parameters is absent.
//
// Satisfies the [device.SubscribingDataPoint] contract; channels invoke it
// from AttachCalculatedDataPoint.
func (l *LevelCombined) Subscribe(ch *device.Channel) func() {
	if ch == nil {
		return nil
	}
	levelDP, _ := any(ch.Parameter(l.LevelParameter)).(timerWireDataPoint)
	slatsDP, _ := any(ch.Parameter(l.SlatsParameter)).(timerWireDataPoint)
	if levelDP == nil || slatsDP == nil {
		return nil
	}
	unsubLevel := levelDP.OnAnyUpdate(func(_, next any) {
		if v, ok := toFloat64(next); ok {
			l.OnLevel(v)
		}
	})
	unsubSlats := slatsDP.OnAnyUpdate(func(_, next any) {
		if v, ok := toFloat64(next); ok {
			l.OnSlatsLevel(v)
		}
	})
	// Seed with already-observed values so the composite is immediately
	// populated on reconnect without waiting for the next push event.
	if raw, ok := levelDP.RawValue(); ok {
		if v, ok2 := toFloat64(raw); ok2 {
			l.OnLevel(v)
		}
	}
	if raw, ok := slatsDP.RawValue(); ok {
		if v, ok2 := toFloat64(raw); ok2 {
			l.OnSlatsLevel(v)
		}
	}
	return func() {
		if unsubLevel != nil {
			unsubLevel()
		}
		if unsubSlats != nil {
			unsubSlats()
		}
	}
}

// OnAnyUpdate satisfies the adapter.CombinedDataPoint interface. The typed
// LevelComposite value is JSON-encoded to a string so BridgeCombinedDataPoint
// can wrap it in a ParamValue and publish it on the event bus.
//
// Encoding goes through [EncodeLevelCompositeJSON], the same renderer the
// combined state topic uses, so one value never reaches two planes spelled
// two ways.
func (l *LevelCombined) OnAnyUpdate(fn func(old, next any)) func() {
	return l.OnUpdate(func(_, next LevelComposite) {
		fn(nil, EncodeLevelCompositeJSON(next))
	})
}

// Value returns the last observed composite position and whether
// both inputs have been seen.
func (l *LevelCombined) Value() (LevelComposite, bool) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	if !l.hasLevel || !l.hasSlats {
		return LevelComposite{}, false
	}
	return LevelComposite{Level: l.level, SlatsLevel: l.slatsLevel}, true
}

// OnLevel feeds a CCU-reported LEVEL value (0..1 fraction).
func (l *LevelCombined) OnLevel(v float64) {
	l.mu.Lock()
	prev, observed := l.snapshotLocked()
	l.level = custom.NewPosition(v)
	l.hasLevel = true
	next, nowObserved := l.snapshotLocked()
	l.mu.Unlock()
	l.fire(observed, nowObserved, prev, next)
}

// OnSlatsLevel feeds a CCU-reported LEVEL_2 value (0..1 fraction).
func (l *LevelCombined) OnSlatsLevel(v float64) {
	l.mu.Lock()
	prev, observed := l.snapshotLocked()
	l.slatsLevel = custom.NewPosition(v)
	l.hasSlats = true
	next, nowObserved := l.snapshotLocked()
	l.mu.Unlock()
	l.fire(observed, nowObserved, prev, next)
}

// OnUpdate registers a change handler fired once both inputs are
// observed.
func (l *LevelCombined) OnUpdate(fn func(old, next LevelComposite)) func() {
	l.mu.Lock()
	l.callbacks = append(l.callbacks, fn)
	idx := len(l.callbacks) - 1
	l.mu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			l.mu.Lock()
			defer l.mu.Unlock()
			if idx < len(l.callbacks) {
				l.callbacks[idx] = nil
			}
		})
	}
}

// IsRefreshed reports whether both LEVEL and LEVEL_2 inputs have been
// observed from the CCU at least once. Satisfies the custom.AggregateDataPoint
// contract.
func (l *LevelCombined) IsRefreshed() bool {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.hasLevel && l.hasSlats
}

// StateUncertain reports whether the composite position is held
// optimistically. LevelCombined has no optimistic tracker.
// Returns false always. Satisfies the custom.AggregateDataPoint contract.
func (l *LevelCombined) StateUncertain() bool { return false }

func (l *LevelCombined) snapshotLocked() (LevelComposite, bool) {
	if !l.hasLevel || !l.hasSlats {
		return LevelComposite{}, false
	}
	return LevelComposite{Level: l.level, SlatsLevel: l.slatsLevel}, true
}

func (l *LevelCombined) fire(wasObserved, nowObserved bool, prev, next LevelComposite) {
	if !nowObserved {
		return
	}
	if wasObserved && prev == next {
		return
	}
	// The `is_refreshed` invariant is satisfied here because nowObserved==true.
	if l.PublishedEventRecently() {
		return
	}
	l.mu.RLock()
	cbs := make([]func(old, next LevelComposite), len(l.callbacks))
	copy(cbs, l.callbacks)
	l.mu.RUnlock()
	for _, cb := range cbs {
		if cb != nil {
			cb(prev, next)
		}
	}
}
