// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package combined

import (
	"context"
	"fmt"
	"sync"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/internal/model/datapoint"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
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

// LevelCombined combines LEVEL and LEVEL_2 (slats) into one
// LEVEL_COMBINED write that the CCU accepts as a single atomic
// command. Reads remain split — the two underlying data points each
// emit their own event.
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
	Writer  Writer

	LevelParameter         hmenum.Parameter
	SlatsParameter         hmenum.Parameter
	CombinedWriteParameter hmenum.Parameter

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
// No production caller exists today: custom/cover implements level
// encoding inline via hmLevelCombined. This constructor is kept so
// the combined package remains a coherent, testable unit; see
// docs/parity/by_design.md BD-A3-CombinedUnused.
//
// levelParam / slatsParam are the per-channel reading parameters
// (LEVEL and LEVEL_2). combinedParam is the write-only paramset entry
// the CCU exposes for atomic moves (LEVEL_COMBINED).
func NewLevelCombined(address string, w Writer, levelParam, slatsParam, combinedParam hmenum.Parameter) *LevelCombined {
	return NewLevelCombinedWithCentral("", address, w, levelParam, slatsParam, combinedParam)
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
	w Writer,
	levelParam, slatsParam, combinedParam hmenum.Parameter,
) *LevelCombined {
	lc := &LevelCombined{
		BaseDataPointFields:    datapoint.NewBaseDataPointFields(centralName, address, levelCombinedKeyName),
		Address:                address,
		Writer:                 w,
		LevelParameter:         levelParam,
		SlatsParameter:         slatsParam,
		CombinedWriteParameter: combinedParam,
	}
	lc.SetForcedUsage(hmenum.DataPointUsageNoCreate)
	return lc
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

// Set writes a new composite position as a single LEVEL_COMBINED
// command. The CCU expects the encoded byte
//
//	value = (int(level*200 + 1)<<8) | int(slats*200 + 1)
//
// matching the eQ-3 on-wire convention for HmIP blinds.
func (l *LevelCombined) Set(ctx context.Context, c LevelComposite, priority hmenum.CommandPriority) error {
	level := clamp01(c.Level.Level())
	slats := clamp01(c.SlatsLevel.Level())

	encoded := encodeLevelCombined(level, slats)
	if err := l.Writer.SetValue(ctx, l.Address, l.CombinedWriteParameter, encoded, priority); err != nil {
		return fmt.Errorf("levelcombined: write: %w", err)
	}

	l.mu.Lock()
	prev, observed := l.snapshotLocked()
	l.level = custom.NewPosition(level)
	l.slatsLevel = custom.NewPosition(slats)
	l.hasLevel = true
	l.hasSlats = true
	next, nowObserved := l.snapshotLocked()
	l.mu.Unlock()
	l.fire(observed, nowObserved, prev, next)
	return nil
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
// observed from the CCU at least once. Implements M18.
func (l *LevelCombined) IsRefreshed() bool {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.hasLevel && l.hasSlats
}

// StateUncertain reports whether the composite position is held
// optimistically. LevelCombined has no optimistic tracker.
// Returns false always. Implements M18.
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

// encodeLevelCombined converts two 0..1 fractions into the byte-pair
// LEVEL_COMBINED wire encoding (high byte = level, low byte = slats;
// each byte follows the CCU "value * 200 + 1" convention).
func encodeLevelCombined(level, slats float64) int32 {
	// Clamp defensively; callers should have clamped already.
	level = clamp01(level)
	slats = clamp01(slats)
	hi := int32(level*200) + 1
	lo := int32(slats*200) + 1
	return (hi << 8) | lo
}

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}
