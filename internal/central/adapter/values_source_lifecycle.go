// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"log/slog"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// sourceMarker is the optional interface implemented by every wire
// data point that participates in the lifecycle (cache → live →
// stale). The two methods return the previous source token plus a
// changed-flag so the lifecycle wiring can publish a
// DataPointSourceChangedEvent only on actual transitions, and with
// a precise (old, new) pair. See generic.DataPoint.
type sourceMarker interface {
	MarkStale() (hmenum.ValueSource, bool)
	MarkLive() (hmenum.ValueSource, bool)
	Source() hmenum.ValueSource
	RawValue() (any, bool)
	DataPointKey() hmtypes.DataPointKey
}

// WireValueSourceLifecycle subscribes the wire-DP source-token
// transitions to the central's event bus:
//
//   - [hmevent.ConnectionLostEvent] flips every wire DP on the
//     affected interface from `live` to `stale`. The value is
//     preserved; only the source token changes so REST / MQTT
//     consumers see the freshness loss.
//
//   - [hmevent.RecoveryCompletedEvent] flips every wire DP on the
//     recovered interface back to `live`. Push events that arrive
//     after recovery overwrite the value as normal; the symmetric
//     mark-live ensures the bridge surface reports renewed
//     freshness even when no live values flow yet.
//
// The lifecycle wiring is per central — passing the unit's own
// EventBus avoids cross-central interference in multi-CCU
// deployments. Returns a closer that unsubscribes both handlers;
// safe to call on daemon shutdown.
func WireValueSourceLifecycle(unit *central.Unit, logger *slog.Logger) func() {
	if unit == nil || unit.EventBus == nil || unit.ModelRegistry == nil {
		return func() {}
	}
	publishTransition := func(centralName string, m sourceMarker, oldSrc, newSrc hmenum.ValueSource) {
		raw, _ := m.RawValue()
		key := m.DataPointKey()
		events.Publish(unit.EventBus, hmevent.DataPointSourceChangedEvent{
			Base:           hmevent.NewBase(),
			CentralName:    centralName,
			InterfaceID:    key.InterfaceID,
			ChannelAddress: key.ChannelAddress,
			Parameter:      key.Parameter,
			OldSource:      oldSrc,
			NewSource:      newSrc,
			Value:          raw,
		})
	}

	unsubLost := events.Subscribe(unit.EventBus, func(e hmevent.ConnectionLostEvent) {
		transitions := 0
		count := walkWireDPs(unit, e.InterfaceID, func(dp any) {
			m, ok := dp.(sourceMarker)
			if !ok {
				return
			}
			if old, changed := m.MarkStale(); changed {
				publishTransition(e.CentralName, m, old, hmenum.ValueSourceStale)
				transitions++
			}
		})
		if logger != nil && count > 0 {
			logger.Debug("values_source.stale",
				slog.String("central", e.CentralName),
				slog.String("interface", e.InterfaceID),
				slog.Int("data_points", count),
				slog.Int("transitions", transitions))
		}
	})
	unsubDone := events.Subscribe(unit.EventBus, func(e hmevent.RecoveryCompletedEvent) {
		transitions := 0
		count := walkWireDPs(unit, e.InterfaceID, func(dp any) {
			m, ok := dp.(sourceMarker)
			if !ok {
				return
			}
			if old, changed := m.MarkLive(); changed {
				publishTransition(e.CentralName, m, old, hmenum.ValueSourceLive)
				transitions++
			}
		})
		if logger != nil && count > 0 {
			logger.Debug("values_source.live",
				slog.String("central", e.CentralName),
				slog.String("interface", e.InterfaceID),
				slog.Int("data_points", count),
				slog.Int("transitions", transitions))
		}
	})
	return func() {
		unsubLost()
		unsubDone()
	}
}

// walkWireDPs invokes fn on every wire-side data point of every
// channel of every device that lives on interfaceID. Returns the
// total number of data points visited.
func walkWireDPs(unit *central.Unit, interfaceID string, fn func(any)) int {
	if unit == nil || unit.ModelRegistry == nil {
		return 0
	}
	count := 0
	for _, d := range unit.ModelRegistry.List() {
		if d == nil || d.InterfaceID != interfaceID {
			continue
		}
		for _, ch := range d.Channels() {
			if ch == nil {
				continue
			}
			for _, dp := range ch.DataPoints() {
				if dp == nil {
					continue
				}
				fn(dp)
				count++
			}
		}
	}
	return count
}

// _ keeps the device import referenced even when the package is built
// with only the lifecycle path active — avoids "imported and not
// used" in a future split where this file is the only consumer.
var _ = (*device.Device)(nil)
