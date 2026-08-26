// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

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
	// IsValid reports whether the source carries a usable reading: refreshed,
	// paired STATUS acceptable, value type as declared, and within the
	// descriptor's bounds. Observation alone is not enough — a source read at
	// startup that returned an out-of-range value, or one the CCU flagged
	// OVERFLOW, is observed but has nothing to calculate from. Feeds
	// [sourceSink.SourcesValid].
	IsValid() bool
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

// snapshotSources returns the registered sources under the read lock. Every
// reader goes through it: [RegisterSource] appends while a calculated sensor
// recomputes on a callback goroutine, so touching the slice field directly is
// a race — the Subscribe hooks install their update handler before they
// register the resolved source, which leaves a window on every attach.
//
// Returning the slice header is safe because append never rewrites the
// elements a previous reader observed.
func (ss *sourceSink) snapshotSources() []SourceDP {
	ss.mu.RLock()
	defer ss.mu.RUnlock()
	return ss.sources
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
	srcs := ss.snapshotSources()

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

// SourcesValid reports whether every registered source carries a usable
// reading. It is the validity gate a calculated sensor installs on its inner
// data point (see [generic.DataPoint.SetValidityGate]).
//
// A derived value can only be as good as the inputs it was computed from, so
// each state-carrying source must be valid itself. Checking observation alone
// is not enough: a source read at boot that returned an unusable value is
// observed, yet there is nothing to calculate from — the calculated sensor
// would keep publishing a number the CCU has already disowned.
//
// Returns false when no sources are registered: without a state carrier there
// is nothing to derive a value from.
//
// Only VALUES sources are registered by the Subscribe methods. MASTER entries
// such as LOW_BAT_LIMIT are configuration inputs a sleeping battery device may
// never deliver; they are read into a field instead of registered, so they
// cannot gate validity.
func (ss *sourceSink) SourcesValid() bool {
	srcs := ss.snapshotSources()

	if len(srcs) == 0 {
		return false
	}
	for _, dp := range srcs {
		if !dp.IsValid() {
			return false
		}
	}
	return true
}

// validityGated is the seam every calculated sensor's inner data point
// exposes for [installSourceValidityGate]. Satisfied by
// [generic.Sensor] and [generic.BinarySensor] through the promoted
// [generic.DataPoint.SetValidityGate].
type validityGated interface {
	SetValidityGate(gate func() bool)
}

// installSourceValidityGate ties a calculated sensor's north-bound
// availability to the validity of the sources it derives from. Every
// calculated-sensor constructor calls it, because a derived data point has
// no descriptor of its own to validate against and would otherwise report
// a value computed from an unusable input as a confirmed reading.
func installSourceValidityGate(inner validityGated, sources *sourceSink) {
	inner.SetValidityGate(sources.SourcesValid)
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
	srcs := ss.snapshotSources()
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
	srcs := ss.snapshotSources()
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
	srcs := ss.snapshotSources()
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
	srcs := ss.snapshotSources()

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

// shouldPublishCalcUpdate guards calculated-sensor publish calls: it
// suppresses the recalculation only when every one of ≥2 sources
// published an event within the last 500 ms, i.e. when the whole CCU
// burst has already been accounted for and this callback would emit the
// same number twice.
//
// The reference contract is the other way round — publish once
// `all(dp.published_event_recently ...)` holds, so an intermediate value
// computed from one fresh and one previous-cycle input never reaches a
// consumer. **Do not "correct" the direction here in isolation.** That
// contract only works when the source data points stamp a publish
// timestamp, and in this stack no wire data point installs a publisher,
// so PublishedEventRecently() is permanently false for every source.
// Flipping the comparison therefore turns the guard into an
// unconditional suppression and silences DewPoint, FrostPoint,
// Enthalpy, VaporConcentration and ApparentTemperature completely
// (measured: nine sensor tests stop emitting). Reaching the reference
// behaviour means wiring the publish stamping onto generic data points
// first, and flipping this comparison in the same change.
//
// With ≤1 source there is no burst and the guard always allows.
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
