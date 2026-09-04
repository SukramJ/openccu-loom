// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/internal/store/sqlite"
	"github.com/SukramJ/openccu-loom/internal/wiring"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
)

// measurementEvictTimeout bounds the deletes the eviction handler runs when a
// device is removed. It is larger than the single-statement caches' budget:
// the measurement purge is one transaction across the raw table and both
// rollup tiers.
const measurementEvictTimeout = 60 * time.Second

// MeasurementEvictor is the handle [WireMeasurementEviction] returns. It
// mirrors [ValuesCacheEvictor], for the measurement history and the
// operator's per-channel recording overrides.
//
// loom:reachable:reason="returned by WireMeasurementEviction to the daemon's southbound bring-up, which calls Stop as a teardown and StartCentral for every central it adopts; the analyzer resolves a type's methods per loaded package variant, so the reachable instance is not the one it classifies"
type MeasurementEvictor struct {
	history   *sqlite.MeasurementStore
	overrides *sqlite.RecordingOverrideStore
	logger    *slog.Logger

	removeObserver func()
	once           sync.Once
}

// StartCentral subscribes one central's EventBus to DeviceRemovedEvent.
//
// The returned closure releases the subscription; nil-safe and idempotent.
func (e *MeasurementEvictor) StartCentral(u *central.Unit) func() {
	if e == nil || u == nil || u.EventBus == nil {
		return func() {}
	}
	unsub := events.Subscribe(u.EventBus, func(ev hmevent.DeviceRemovedEvent) {
		if ev.ModelTeardown {
			// A cache-clear re-init drops the whole model and re-pulls it
			// without the operator asking for any device to go; purging here
			// would take every device's history with it (ADR 0042).
			return
		}
		//nolint:contextcheck // bus handlers carry no ctx; the deletes are bounded by measurementEvictTimeout
		ctx, cancel := context.WithTimeout(context.Background(), measurementEvictTimeout)
		defer cancel()
		if err := e.history.DeleteDevice(ctx, ev.CentralName, ev.InterfaceID, ev.Address); err != nil && e.logger != nil {
			e.logger.Warn("measurements.evict_err",
				slog.String("central", ev.CentralName),
				slog.String("interface", ev.InterfaceID),
				slog.String("address", ev.Address),
				slog.String("err", err.Error()))
		}
		if err := e.overrides.DeleteDevice(ctx, ev.CentralName, ev.InterfaceID, ev.Address); err != nil && e.logger != nil {
			e.logger.Warn("recording_overrides.evict_err",
				slog.String("central", ev.CentralName),
				slog.String("interface", ev.InterfaceID),
				slog.String("address", ev.Address),
				slog.String("err", err.Error()))
		}
	})
	var once sync.Once
	return func() { once.Do(unsub) }
}

// Stop releases every subscription. Idempotent and nil-safe.
func (e *MeasurementEvictor) Stop() {
	if e == nil {
		return
	}
	e.once.Do(func() {
		if e.removeObserver != nil {
			e.removeObserver()
		}
	})
}

// WireMeasurementEviction subscribes every central's EventBus to
// DeviceRemovedEvent and purges the removed device's measurement history and
// recording overrides.
//
// Both stores carried a DeleteDevice whose doc comment said it runs on
// device-remove / unpair, and neither had a production caller: history across
// all three tiers and the operator's recording overrides survived unpairing
// indefinitely. That is the resurfacing the multi-tier delete was written to
// prevent — a device re-paired at the same address, which is routine because
// the CCU reuses addresses when hardware is swapped, inherited the previous
// device's series and its recording decisions.
//
// Pass a nil registry or nil stores to disable — the returned handle is nil
// and every method on it is a no-op, which is the shape history-off
// deployments take (the stores are nil when history is disabled, ADR 0040).
func WireMeasurementEviction(
	reg *central.Registry,
	history *sqlite.MeasurementStore,
	overrides *sqlite.RecordingOverrideStore,
	logger *slog.Logger,
) *MeasurementEvictor {
	if reg == nil || history == nil || overrides == nil {
		return nil
	}
	e := &MeasurementEvictor{history: history, overrides: overrides, logger: logger}
	e.removeObserver = reg.OnRegisterDeclared(wiring.Seam{
		Name:         "store.measurement_eviction",
		Collaborator: "*adapter.MeasurementEvictor",
		Phase:        wiring.PhasePerCentral,
		Why:          "an unpaired device kept its measurement history and recording overrides forever; nothing on the removal path called either store's DeleteDevice",
	}, e.StartCentral)
	return e
}
