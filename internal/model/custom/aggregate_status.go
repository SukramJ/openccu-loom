// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package custom

import (
	"slices"

	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// AggregateSlot is the narrow view a custom data point's sub-DP must
// expose so a generic [AggregateStatus] helper can answer the
// [AggregateDataPoint] interface without each per-family struct
// reimplementing the same loops.
//
// Every `*generic.DataPoint[T]` and every concrete sub-type
// (`*generic.Switch`, `*generic.Float`, `*generic.Sensor[T]`)
// already exposes the methods through its embedded base — passing
// the typed reference at call site keeps generic.DataPoint's typed
// API intact while letting the helper take a homogeneous slice.
//
// `RawValue()` second return reports observation: a slot is
// "refreshed" iff its second return is true.
type AggregateSlot interface {
	device.AttachableDataPoint
	RawValue() (any, bool)
	IsOptimistic() bool
	IsStatusValid() bool
}

// AggregateStatus answers the [AggregateDataPoint] questions (`IsRefreshed`,
// `StateUncertain`, `SubDataPointKeys`) for a list of sub-DPs. Nil entries
// are skipped so call sites can pass optional slots (HUMIDITY on RF
// thermostats, LEVEL_2 on covers without slat tilt) without branching.
func AggregateStatus(slots ...AggregateSlot) AggregateView {
	return AggregateView{slots: slots}
}

// AggregateView is the result of [AggregateStatus]. The three
// methods mirror the [AggregateDataPoint] interface members and can
// be wired into any custom-DP type via composition / forwarding.
type AggregateView struct {
	slots []AggregateSlot
}

// IsRefreshed reports whether at least one of the sub-DPs has been
// observed since process start.
func (a AggregateView) IsRefreshed() bool {
	for _, s := range a.slots {
		if s == nil {
			continue
		}
		if _, ok := s.RawValue(); ok {
			return true
		}
	}
	return false
}

// StateUncertain reports whether at least one of the sub-DPs is currently in
// the optimistic-update window.
func (a AggregateView) StateUncertain() bool {
	for _, s := range a.slots {
		if s == nil {
			continue
		}
		if s.IsOptimistic() {
			return true
		}
	}
	return false
}

// HasDataPoints reports whether the aggregate has at least one non-nil
// sub-DP.
func (a AggregateView) HasDataPoints() bool {
	for _, s := range a.slots {
		if s != nil {
			return true
		}
	}
	return false
}

// IsStatusValid reports whether all non-nil sub-DPs have a valid status (i.e.
// no paired `_STATUS` parameter is in OVERFLOW / ERROR state).
func (a AggregateView) IsStatusValid() bool {
	for _, s := range a.slots {
		if s == nil {
			continue
		}
		if !s.IsStatusValid() {
			return false
		}
	}
	return true
}

// SubDataPointKeys returns the wire identifiers of every non-nil
// sub-DP. Used by REST diagnostic endpoints to expose the underlying
// parameter list without copying every slot's full state.
func (a AggregateView) SubDataPointKeys() []hmtypes.DataPointKey {
	out := make([]hmtypes.DataPointKey, 0, len(a.slots))
	for _, s := range a.slots {
		if s == nil {
			continue
		}
		out = append(out, s.DataPointKey())
	}
	return out
}

// HasAnyKey reports whether at least one of the given keys is present among
// the aggregate's sub-DPs.
//
// This mirrors custom/data_point.py:183
// `has_data_point_key(data_point_key) → bool` which performs a fast set
// membership check over `data_point_keys`. The Go variant accepts a slice so
// callers can test multiple keys in a single call.
func (a AggregateView) HasAnyKey(keys []hmtypes.DataPointKey) bool {
	for _, s := range a.slots {
		if s == nil {
			continue
		}
		dpk := s.DataPointKey()
		if slices.Contains(keys, dpk) {
			return true
		}
	}
	return false
}
