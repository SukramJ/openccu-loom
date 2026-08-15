// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"log/slog"

	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// CombinedDataPoint is the narrow contract a combined data point
// (HSColor, Timer, LevelCombined, WeekProfile, …) must satisfy so
// the EventBridge can fan its updates out as
// [hmevent.DataPointValueChangedEvent] on the central event bus.
//
// Each concrete combined type already exposes a typed
// `OnUpdate(fn func(old, next T)) func()` method; the bridge
// erases the type via the `OnAnyUpdate` convention (`func(old,
// next any)`) so a single implementation handles every variant.
//
// Combined types that do not yet expose `OnAnyUpdate` can be
// wired through [BridgeCombinedDataPoint] via a typed adapter
// closure that converts the typed callback into the `any`-typed
// shape — see [BridgeHSColor] / [BridgeTimer] / [BridgeWeekProfile]
// in the same file.
type CombinedDataPoint interface {
	OnAnyUpdate(fn func(old, next any)) func()
}

// BridgeCombinedDataPoint registers an OnAnyUpdate listener on the
// combined data point and translates each emission into a
// [hmevent.DataPointValueChangedEvent] on bus. The event reuses
// the synthetic parameter id (e.g. `HS_COLOR`, `TIMER`,
// `WEEK_PROFILE`) as the wire `Parameter` so MQTT topics render
// uniformly with regular data point topics under the channel
// address.
//
// Returns the unsubscribe closure. Idempotent re-registration is
// the caller's responsibility — typical use is one bridge per
// (channel, combined-DP) pair, installed at materialise time.
//
// Installed by materialiseCombinedDataPoints in device_pipeline.go, which
// runs from finishIngest — the shared post-ingest path both interface
// bring-up and hot-plug go through.
func BridgeCombinedDataPoint(
	bus *events.Bus,
	dp CombinedDataPoint,
	interfaceID, channelAddress, parameter string,
	logger *slog.Logger,
) func() {
	if bus == nil || dp == nil {
		return nil
	}
	return dp.OnAnyUpdate(func(_, next any) {
		newVal, err := hmtypes.NewParamValue(next)
		if err != nil {
			if logger != nil {
				logger.Debug("combined.bridge.skip",
					slog.String("channel", channelAddress),
					slog.String("param", parameter),
					slog.String("err", err.Error()))
			}
			return
		}
		events.Publish(bus, hmevent.DataPointValueChangedEvent{
			Base: hmevent.NewBase(),
			Key: hmtypes.DataPointKey{
				InterfaceID:    interfaceID,
				ChannelAddress: channelAddress,
				// Combined data points sit on a synthetic paramset
				// distinct from the wire VALUES paramset.
				// on `CombinedDataPoint.dpk`. Adapters that route by
				// paramset can therefore distinguish derived
				// aggregates from raw wire values.
				ParamsetKey: hmenum.ParamsetKeyCombined,
				Parameter:   parameter,
			},
			OldValue: hmtypes.NoneValue(),
			NewValue: newVal,
		})
	})
}

// TypedCombinedSubscriber is the typed flavour of [CombinedDataPoint]
// for the four concrete combined types in `internal/model/combined`.
// Each one exposes `OnUpdate(fn func(old, next T)) func()`; the
// adapter closures below convert the typed callback into the
// `any`-typed signature [BridgeCombinedDataPoint] expects.
type TypedCombinedSubscriber[T any] interface {
	OnUpdate(fn func(old, next T)) func()
}

// AnyUpdateAdapter wraps a typed [TypedCombinedSubscriber] into a
// [CombinedDataPoint]. The wrapper allocates a small closure on
// each `OnAnyUpdate` call so the typed combined DP keeps its
// strongly-typed `OnUpdate` signature while fitting into the
// adapter's `any`-keyed bus-bridge plumbing.
type AnyUpdateAdapter[T any] struct {
	Inner TypedCombinedSubscriber[T]
}

// OnAnyUpdate satisfies [CombinedDataPoint].
func (a AnyUpdateAdapter[T]) OnAnyUpdate(fn func(old, next any)) func() {
	if a.Inner == nil {
		return func() {}
	}
	return a.Inner.OnUpdate(func(old, next T) {
		fn(old, next)
	})
}
