// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package calculated

import (
	"sync"
	"time"

	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// SourceDP is the minimal interface a source data point must satisfy so that
// a calculated sensor can aggregate state-uncertainty and refreshed-state
// across its inputs. Every *generic.DataPoint[T] satisfies this interface
// automatically.
//
// is_refreshed    = all(dp.is_refreshed    for dp in relevant_dps)
// state_uncertain = any(dp.state_uncertain for dp in relevant_dps)
//
// In Go, `is_refreshed` maps to `RawValue() ok == true` (the data point has
// been observed at least once) and `state_uncertain` maps to
// `StateUncertain()` (an optimistic write is in flight waiting for CCU
// confirmation).
type SourceDP interface {
	// RawValue returns the current value and whether it has ever been
	// observed. `ok == false` means no CCU event has arrived yet.
	RawValue() (any, bool)
	// StateUncertain reports whether the cached value is held
	// optimistically (pending CCU confirmation).
	StateUncertain() bool
	// PublishedEventRecently reports whether the data point dispatched a publish
	// event within the last 500 ms. Used by [shouldPublishCalcUpdate] to
	// suppress redundant calculated-sensor callbacks when all source DPs already
	// published moments ago.
	PublishedEventRecently() bool
}

// sourceTimestampProvider is the optional interface that source DPs
// implement when they carry modification and refresh timestamps.
// Every production *generic.DataPoint[T] satisfies it; test stubs
// that do not are silently skipped.
type sourceTimestampProvider interface {
	ModifiedAt() time.Time
	RefreshedAt() time.Time
}

// sourceSink aggregates StateUncertain across a dynamic set of source
// DPs. It is embedded in every climate-derived sensor struct so that
// [Subscribe] can register the source DPs it resolves from the
// channel, and so that [StateUncertain] is available as a calculated-
// sensor-level method.
//
// sourceSink is safe for concurrent use: the [RegisterSource] method
// may be called from Subscribe (typically on the setup goroutine),
// while [StateUncertain] may be read from multiple northbound adapters.
type sourceSink struct {
	mu      sync.RWMutex
	sources []SourceDP
}

// RegisterSource adds dp to the aggregation set. Duplicate
// registrations are harmless (each DP is queried independently).
// A nil dp is silently ignored so callers do not need to guard
// optional parameters (e.g. AIR_PRESSURE on EnthalpySensor).
func (ss *sourceSink) RegisterSource(dp SourceDP) {
	if dp == nil {
		return
	}
	ss.mu.Lock()
	ss.sources = append(ss.sources, dp)
	ss.mu.Unlock()
}

// StateUncertain aggregates over all registered source DPs.
//
// Returns true when:
//   - No source DPs have been registered yet (no information at all).
//   - Any registered source DP has never been observed
//
// (RawValue ok == false → equivalent
//
//	  `not dp.is_refreshed` → is_refreshed = false ⇒ uncertain).
//	- Any registered source DP is in optimistic state
//	  (StateUncertain() == true).
//
// State_uncertain
//
//	return any(dp.state_uncertain for dp in self._relevant_data_points)
//
// with the additional invariant that an unobserved DP also makes the
// Calculated value uncertain (consistent
// `is_refreshed` guard in `_should_publish_data_point_updated_callback`).
func (ss *sourceSink) StateUncertain() bool {
	ss.mu.RLock()
	srcs := ss.sources
	ss.mu.RUnlock()

	if len(srcs) == 0 {
		// No source DPs registered → cannot confirm the state.
		return true
	}
	for _, dp := range srcs {
		_, observed := dp.RawValue()
		if !observed {
			return true // not refreshed → uncertain
		}
		if dp.StateUncertain() {
			return true // optimistic write in flight → uncertain
		}
	}
	return false
}

// dataPointKeyProvider is the internal interface used by
// [sourceSink.LoadDataPointValue] to extract the channel address and
// parameter name from a registered source DP without extending the
// public [SourceDP] interface. Every [*generic.DataPoint[T]] satisfies
// it in production; test stubs that don't are silently skipped.
type dataPointKeyProvider interface {
	DataPointKey() hmtypes.DataPointKey
}

// LoadDataPointValue triggers a CCU-side value refresh for all registered
// source DPs that implement [dataPointKeyProvider]. For each such DP, loader
// is called with (channelAddress, parameterName). A nil loader is a no-op.
//
// for dp in self._readable_data_points: await dp.load_data_point_value(...)
func (ss *sourceSink) LoadDataPointValue(loader func(channelAddress, parameter string)) {
	if loader == nil {
		return
	}
	ss.mu.RLock()
	srcs := ss.sources
	ss.mu.RUnlock()
	for _, dp := range srcs {
		if kp, ok := dp.(dataPointKeyProvider); ok {
			dpk := kp.DataPointKey()
			loader(dpk.ChannelAddress, dpk.Parameter)
		}
	}
}

// aggregateModifiedAt returns the maximum ModifiedAt across all registered
// source DPs that implement [sourceTimestampProvider]. Returns the zero time
// when no sources are registered or none carry a timestamp.
func (ss *sourceSink) aggregateModifiedAt() time.Time {
	ss.mu.RLock()
	srcs := ss.sources
	ss.mu.RUnlock()
	var latest time.Time
	for _, dp := range srcs {
		if tp, ok := dp.(sourceTimestampProvider); ok {
			if t := tp.ModifiedAt(); t.After(latest) {
				latest = t
			}
		}
	}
	return latest
}

// aggregateRefreshedAt returns the maximum RefreshedAt across all registered
// source DPs that implement [sourceTimestampProvider]. Returns the zero time
// when no sources are registered or none carry a timestamp.
func (ss *sourceSink) aggregateRefreshedAt() time.Time {
	ss.mu.RLock()
	srcs := ss.sources
	ss.mu.RUnlock()
	var latest time.Time
	for _, dp := range srcs {
		if tp, ok := dp.(sourceTimestampProvider); ok {
			if t := tp.RefreshedAt(); t.After(latest) {
				latest = t
			}
		}
	}
	return latest
}

// IsRefreshed reports whether ALL registered source DPs have been observed at
// least once.
//
// is_refreshed = all(dp.is_refreshed for dp in relevant_dps)
//
// Returns false when no sources are registered (no information yet).
func (ss *sourceSink) IsRefreshedFromSources() bool {
	ss.mu.RLock()
	srcs := ss.sources
	ss.mu.RUnlock()

	if len(srcs) == 0 {
		return false
	}
	for _, dp := range srcs {
		_, observed := dp.RawValue()
		if !observed {
			return false
		}
	}
	return true
}

// shouldPublishCalcUpdate guards calculated-sensor publish calls.
// Returns false when ALL source DPs have published an event within the last
// 500 ms AND there are at least two sources
// _should_publish_data_point_updated_callback logic
// (calculated/data_point.py:147-161):
//
//	return all(dp.published_event_recently for dp in relevant_values_data_point)
//
// The guard suppresses a redundant recalculation callback when both
// temperature AND humidity just published — a single CCU burst updates them
// together and the calculated sensor would otherwise fire twice in quick
// succession with an intermediate (incorrect) value. With ≤1 source the
// guard is always true (no "all sources published" condition to satisfy).
//
// Callers check the sensor-level published_event_recently separately before
// calling this helper (the sensor-level guard lives in feedSink).
func shouldPublishCalcUpdate(sources []SourceDP) bool {
	if len(sources) <= 1 {
		return true
	}
	for _, dp := range sources {
		if !dp.PublishedEventRecently() {
			return true // at least one source hasn't published yet → allow publish
		}
	}
	// All sources published recently → suppress redundant intermediate callback.
	return false
}
