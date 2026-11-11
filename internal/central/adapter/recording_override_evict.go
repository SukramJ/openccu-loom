// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/wiring"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
)

// recordingOverrideEvictTimeout bounds the delete the eviction handler runs
// when a device is removed.
const recordingOverrideEvictTimeout = 10 * time.Second

// RecordingOverridePurger removes every recording override of one device,
// from the durable rows and from whatever the forwarder consults at run
// time. The measurement forwarder's overlay satisfies it.
type RecordingOverridePurger interface {
	DeleteDevice(ctx context.Context, centralName, interfaceID, deviceAddress string) error
}

// RecordingOverrideEvictor is the handle [WireRecordingOverrideEviction]
// returns. It mirrors [ValuesCacheEvictor], for the operator's
// per-data-point recording overrides.
//
// loom:reachable:reason="returned by WireRecordingOverrideEviction to the daemon's southbound bring-up, which calls Stop as a teardown; a struct reached only through that return value, which the analyzer's type heuristic cannot see used"
type RecordingOverrideEvictor struct {
	overrides RecordingOverridePurger
	logger    *slog.Logger

	removeObserver func()
	once           sync.Once
}

// StartCentral subscribes one central's EventBus to DeviceRemovedEvent.
//
// The returned closure releases the subscription; nil-safe and idempotent.
func (e *RecordingOverrideEvictor) StartCentral(u *central.Unit) func() {
	if e == nil || u == nil || u.EventBus == nil {
		return func() {}
	}
	unsub := u.EventBus.Subscribe(func(ev hmevent.DeviceRemovedEvent) {
		if ev.ModelTeardown {
			// A cache-clear re-init drops the whole model and re-pulls it
			// without the operator asking for any device to go; purging
			// here would take every device's overrides with it (ADR 0042).
			return
		}
		//nolint:contextcheck // bus handlers carry no ctx; the delete is bounded by recordingOverrideEvictTimeout
		ctx, cancel := context.WithTimeout(context.Background(), recordingOverrideEvictTimeout)
		defer cancel()
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
func (e *RecordingOverrideEvictor) Stop() {
	if e == nil {
		return
	}
	e.once.Do(func() {
		if e.removeObserver != nil {
			e.removeObserver()
		}
	})
}

// WireRecordingOverrideEviction subscribes every central's EventBus to
// DeviceRemovedEvent and purges the removed device's recording overrides.
//
// A device re-paired at the same address is routine — the CCU reuses
// addresses when hardware is swapped — and without the purge the new device
// inherits the forwarding decisions an operator made for the old one.
//
// Pass a nil registry or a nil purger to disable: the returned handle is nil
// and every method on it is a no-op, which is the shape a daemon without
// measurement forwarding takes.
func WireRecordingOverrideEviction(
	reg *central.Registry,
	overrides RecordingOverridePurger,
	logger *slog.Logger,
) *RecordingOverrideEvictor {
	if reg == nil || overrides == nil {
		return nil
	}
	e := &RecordingOverrideEvictor{overrides: overrides, logger: logger}
	e.removeObserver = reg.OnRegisterDeclared(wiring.Seam{
		Name:         "store.recording_override_eviction",
		Collaborator: "*adapter.RecordingOverrideEvictor",
		Phase:        wiring.PhasePerCentral,
		Why:          "an unpaired device keeps its recording overrides forever, and a device re-paired at the same address inherits them",
	}, e.StartCentral)
	return e
}
