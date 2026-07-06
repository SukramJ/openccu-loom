// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package combined

import (
	"context"
	"errors"
	"sync"

	"github.com/SukramJ/openccu-loom/internal/model/datapoint"
	"github.com/SukramJ/openccu-loom/internal/model/schedule"
	"github.com/SukramJ/openccu-loom/internal/model/weekprofile"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// weekProfileKeyName is the canonical key segment used by
// [WeekProfile]'s promoted [datapoint.BaseDataPointFields.UniqueID].
// Mirrors the `COMBINED/WEEKPROFILE` family identifier.
// stable, family-prefixed token regardless of whether the underlying
// `WEEK_PROFILE` paramset entry has any synonym variant on a specific
// model.
const weekProfileKeyName = "COMBINED/WEEKPROFILE"

// WeekProfile is the combined data point for a thermostat's weekly
// Schedule. It mirrors
// `model/combined.CombinedWeekProfile`: a thin wrapper over the per-
// channel `weekprofile.ClimateProfile` that exposes a write-side
// `Set` aliased to a single CCU paramset patch.
//
// Reads stay split across the underlying ClimatePeriod data points;
// the wrapper publishes one composite-update event per CCU-side change
// so SPA + MQTT consumers can subscribe to "schedule changed" instead
// of tracking each profile slot individually.
//
// Writes are atomic: Set serializes the schedule and hands it to the
// configured Writer in one call. Mirrors the
// `combinedParam = WEEK_PROFILE` semantics.
//
// WeekProfile embeds [datapoint.BaseDataPointFields] so
// the canonical [datapoint.BaseDataPointFields.UniqueID] /
// [datapoint.BaseDataPointFields.Visible] /
// [datapoint.BaseDataPointFields.SetForcedUsage] /
// [datapoint.BaseDataPointFields.SetPublisher] surfaces are promoted
// into the type. The legacy [WeekProfile.Address] field is kept as a
// public field for callers that still read it directly; new code
// should prefer the promoted [datapoint.BaseDataPointFields.Address]
// accessor.
type WeekProfile struct {
	datapoint.BaseDataPointFields

	Address string
	Writer  Writer

	// CombinedWriteParameter is the paramset entry the CCU exposes for
	// atomic schedule writes (typically `WEEK_PROFILE`).
	CombinedWriteParameter hmenum.Parameter

	profile *weekprofile.ClimateProfile

	mu        sync.RWMutex
	current   *schedule.Climate
	hasValue  bool
	callbacks []func(old, next *schedule.Climate)
	unsub     func()
}

// NewWeekProfile constructs a WeekProfile bound to a
// [weekprofile.ClimateProfile]. The profile's loader is the read side;
// the Writer + combinedParam carry the write side.
//
// This legacy signature drops the central-name segment of the unique
// identifier — callers that need a multi-CCU-safe identifier MUST use
// [NewCombinedWeekProfile] instead. Existing call sites stay source-
// compatible; the promoted [datapoint.BaseDataPointFields] surface is
// initialised with an empty central.
//
// The wrapper subscribes to the underlying profile so external
// callbacks fire whenever the profile is reloaded or saved.
//
// No production caller exists today: weekprofile.NewProfileDataPoint is
// used directly by custom climate. This constructor is retained so the
// combined package remains testable; see docs/parity/by_design.md
// BD-A3-CombinedUnused.
func NewWeekProfile(
	address string,
	w Writer,
	profile *weekprofile.ClimateProfile,
	combinedParam hmenum.Parameter,
) *WeekProfile {
	return NewCombinedWeekProfile("", address, w, profile, combinedParam)
}

// NewCombinedWeekProfile is the multi-CCU-safe constructor. The
// promoted [datapoint.BaseDataPointFields] is wired with `central`
// scoping so the resulting [datapoint.BaseDataPointFields.UniqueID]
// shape is `<central>:<address>:COMBINED/WEEKPROFILE`. ADR 0002
// (multi-CCU first-class) requires production callers to set
// `central`.
//
// The wrapper subscribes to the underlying profile so external
// callbacks fire whenever the profile is reloaded or saved.
func NewCombinedWeekProfile(
	centralName, address string,
	w Writer,
	profile *weekprofile.ClimateProfile,
	combinedParam hmenum.Parameter,
) *WeekProfile {
	wp := &WeekProfile{
		BaseDataPointFields:    datapoint.NewBaseDataPointFields(centralName, address, weekProfileKeyName),
		Address:                address,
		Writer:                 w,
		CombinedWriteParameter: combinedParam,
		profile:                profile,
	}
	// Default-NoCreate
	// takes `visible: bool = False` and the `_get_data_point_usage`
	// override returns `NO_CREATE` unless explicitly enabled
	// (combined/data_point.py:261-265). openccu-loom's
	// BaseDataPointFields has no _visible field, so we pin the same
	// behaviour by force-marking NoCreate at construction time —
	// callers that want the WeekProfile surfaced as a regular DP
	// must explicitly call SetForcedUsage(CDPVisible) (or DataPoint).
	wp.SetForcedUsage(hmenum.DataPointUsageNoCreate)
	if profile != nil {
		wp.unsub = profile.OnChange(func(_, next *schedule.Climate) {
			wp.observe(next)
		})
	}
	return wp
}

// Close releases the subscription to the underlying profile. Safe to
// call multiple times.
func (w *WeekProfile) Close() {
	if w.unsub != nil {
		w.unsub()
		w.unsub = nil
	}
}

// Value returns the most recently observed climate schedule and
// whether one is available.
func (w *WeekProfile) Value() (*schedule.Climate, bool) {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.current, w.hasValue
}

// observe is the channel for incoming schedule snapshots. It updates
// the local cache and fires every registered OnUpdate callback.
func (w *WeekProfile) observe(next *schedule.Climate) {
	w.mu.Lock()
	prev := w.current
	wasObserved := w.hasValue
	w.current = next
	w.hasValue = true
	cbs := make([]func(old, next *schedule.Climate), len(w.callbacks))
	copy(cbs, w.callbacks)
	w.mu.Unlock()
	if !wasObserved && next == nil {
		return
	}
	for _, cb := range cbs {
		if cb != nil {
			cb(prev, next)
		}
	}
}

// Set serialises the schedule and writes it to the CCU as a single
// atomic command. The Writer + CombinedWriteParameter combination
// determines which paramset entry receives the payload.
func (w *WeekProfile) Set(
	ctx context.Context,
	s *schedule.Climate,
	priority hmenum.CommandPriority,
) error {
	if w == nil {
		return errors.New("week_profile: nil receiver")
	}
	if w.Writer == nil {
		return errors.New("week_profile: no writer wired")
	}
	if s == nil {
		return errors.New("week_profile: nil schedule")
	}
	if w.profile != nil {
		return w.profile.Save(ctx, s)
	}
	// Fallback path: no underlying profile (write-only configuration)
	// — hand the schedule to the writer directly.
	return w.Writer.SetValue(ctx, w.Address, w.CombinedWriteParameter, s, priority)
}

// IsRefreshed reports whether the WeekProfile has received at least one
// schedule snapshot. Satisfies the custom.AggregateDataPoint contract.
func (w *WeekProfile) IsRefreshed() bool {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.hasValue
}

// StateUncertain reports whether the schedule is held optimistically.
// WeekProfile has no optimistic tracker. Returns false always. Satisfies
// the custom.AggregateDataPoint contract.
func (w *WeekProfile) StateUncertain() bool { return false }

// IsReadable returns false. WeekProfile is a write-only combined DP
// (_operations = Operations.WRITE).
func (w *WeekProfile) IsReadable() bool { return false }

// IsWritable returns true. WeekProfile is a write-only combined DP.
func (w *WeekProfile) IsWritable() bool { return true }

// Signature returns the stable cross-stack identifier in the format
// "week_profile/{model}/WEEK_PROFILE".
func (w *WeekProfile) Signature() string {
	param := string(w.CombinedWriteParameter)
	if param == "" {
		param = "WEEK_PROFILE"
	}
	return combinedSignature(hmenum.DataPointCategoryWeekProfile, param)
}

// OnUpdate registers fn for change events. The returned closure
// unsubscribes idempotently.
func (w *WeekProfile) OnUpdate(fn func(old, next *schedule.Climate)) func() {
	w.mu.Lock()
	w.callbacks = append(w.callbacks, fn)
	idx := len(w.callbacks) - 1
	w.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			w.mu.Lock()
			defer w.mu.Unlock()
			if idx < len(w.callbacks) {
				w.callbacks[idx] = nil
			}
		})
	}
}
