// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

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

// valuesCacheEvictTimeout bounds the single-device DELETE the eviction
// handler runs when a device is removed.
const valuesCacheEvictTimeout = 30 * time.Second

// ValuesCacheEvictor is the handle [WireValuesCacheEviction] returns. It holds
// the per-central DeviceRemovedEvent subscriptions that delete a removed
// device's persisted cache rows.
//
// loom:reachable:reason="returned by WireValuesCacheEviction to the daemon's southbound bring-up, which calls Stop as a teardown and StartCentral for every central it adopts; the analyzer resolves a type's methods per loaded package variant, so the reachable instance is not the one it classifies"
type ValuesCacheEvictor struct {
	store  *sqlite.ValuesCacheStore
	logger *slog.Logger

	// removeObserver detaches the registry observer and every per-central
	// subscription it attached.
	removeObserver func()
	once           sync.Once
}

// StartCentral subscribes one central's EventBus to DeviceRemovedEvent. It is
// the observer the registry runs per central, for boot-time and
// runtime-adopted CCUs alike: a central that joined after wiring time would
// otherwise keep every unpaired device's rows forever, since nothing else on
// the removal path touches the store.
//
// The returned closure releases the subscription; nil-safe and idempotent.
func (e *ValuesCacheEvictor) StartCentral(u *central.Unit) func() {
	if e == nil || u == nil || u.EventBus == nil {
		return func() {}
	}
	unsub := events.Subscribe(u.EventBus, func(ev hmevent.DeviceRemovedEvent) {
		if ev.ModelTeardown {
			// A cache-clear re-init drops the whole model and re-pulls it. The
			// operator's requested scope was already deleted by
			// cachereset.Service.Clear; evicting here would take every other
			// device's cache with it (ADR 0042).
			return
		}
		//nolint:contextcheck // bus handlers carry no ctx; the removal DELETE is bounded by valuesCacheEvictTimeout
		ctx, cancel := context.WithTimeout(context.Background(), valuesCacheEvictTimeout)
		defer cancel()
		if err := e.store.DeleteDevice(ctx, ev.CentralName, ev.InterfaceID, ev.Address); err != nil && e.logger != nil {
			e.logger.Warn("values_cache.evict_err",
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
func (e *ValuesCacheEvictor) Stop() {
	if e == nil {
		return
	}
	e.once.Do(func() {
		if e.removeObserver != nil {
			e.removeObserver()
		}
	})
}

// WireValuesCacheEviction subscribes every central's EventBus to
// DeviceRemovedEvent and deletes the removed device's rows from the
// persistent values cache. Without it, unpairing a device leaves its cached
// rows behind indefinitely: nothing on the device-removal path
// (coordinators/device.go) touches the SQLite store, which only ever
// overwrites a row when the same address is re-paired and its current
// channels are flushed again. Pass a nil store or registry to disable — the
// returned handle is nil and every method on it is a no-op.
func WireValuesCacheEviction(
	reg *central.Registry,
	store *sqlite.ValuesCacheStore,
	logger *slog.Logger,
) *ValuesCacheEvictor {
	if reg == nil || store == nil {
		return nil
	}
	e := &ValuesCacheEvictor{store: store, logger: logger}
	e.removeObserver = reg.OnRegisterDeclared(wiring.Seam{
		Name:         "store.values_cache_eviction",
		Collaborator: "*adapter.ValuesCacheEvictor",
		Phase:        wiring.PhasePerCentral,
		Why:          "an unpaired device leaves its values-cache rows behind indefinitely; nothing else on the removal path touches the store",
	}, e.StartCentral)
	return e
}
