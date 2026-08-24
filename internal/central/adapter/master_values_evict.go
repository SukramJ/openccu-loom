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

// masterValuesEvictTimeout bounds the single-device DELETE the eviction
// handler runs when a device is removed.
const masterValuesEvictTimeout = 30 * time.Second

// MasterValuesEvictor is the handle [WireMasterValuesEviction] returns. It
// mirrors [ValuesCacheEvictor] exactly, for the persisted MASTER-paramset
// cache: [DevicePipeline]'s MASTER hydration is cache-first and returns on a
// SQLite hit without ever contacting the CCU (see
// [DevicePipeline.seedMasterValues]), so a device removed and later
// re-paired at the same address — a factory reset is the common path,
// since the CCU keeps the serial as the address — was seeded straight
// from the previous pairing's stale configuration instead of the CCU's
// current one.
// loom:reachable:reason="the return type of WireMasterValuesEviction, which the daemon calls at cmd/openccu-loom/daemon_southbound.go to arm the eviction; the analyzer does not count a type reached only as a function result"
type MasterValuesEvictor struct {
	store  *sqlite.MasterValuesStore
	logger *slog.Logger

	// removeObserver detaches the registry observer and every per-central
	// subscription it attached.
	removeObserver func()
	once           sync.Once
}

// StartCentral subscribes one central's EventBus to DeviceRemovedEvent. It is
// the observer the registry runs per central, for boot-time and
// runtime-adopted CCUs alike: a central that joined after wiring time would
// otherwise keep every unpaired device's persisted MASTER values forever.
//
// The returned closure releases the subscription; nil-safe and idempotent.
func (e *MasterValuesEvictor) StartCentral(u *central.Unit) func() {
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
		//nolint:contextcheck // bus handlers carry no ctx; the removal DELETE is bounded by masterValuesEvictTimeout
		ctx, cancel := context.WithTimeout(context.Background(), masterValuesEvictTimeout)
		defer cancel()
		if err := e.store.DeleteDevice(ctx, ev.CentralName, ev.InterfaceID, ev.Address); err != nil && e.logger != nil {
			e.logger.Warn("master_values.evict_err",
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
func (e *MasterValuesEvictor) Stop() {
	if e == nil {
		return
	}
	e.once.Do(func() {
		if e.removeObserver != nil {
			e.removeObserver()
		}
	})
}

// WireMasterValuesEviction subscribes every central's EventBus to
// DeviceRemovedEvent and deletes the removed device's rows from the
// persisted MASTER-values store. Without it, unpairing a device (with or
// without a factory reset) leaves its cached MASTER paramset behind
// indefinitely; a re-pair at the same address then hydrates cache-first
// from the stale rows and never re-reads the device's current
// configuration from the CCU. Pass a nil store or registry to disable —
// the returned handle is nil and every method on it is a no-op. Mirrors
// [WireValuesCacheEviction]; wire both from the same call site.
func WireMasterValuesEviction(
	reg *central.Registry,
	store *sqlite.MasterValuesStore,
	logger *slog.Logger,
) *MasterValuesEvictor {
	if reg == nil || store == nil {
		return nil
	}
	e := &MasterValuesEvictor{store: store, logger: logger}
	e.removeObserver = reg.OnRegisterDeclared(wiring.Seam{
		Name:         "store.master_values_eviction",
		Collaborator: "*adapter.MasterValuesEvictor",
		Phase:        wiring.PhasePerCentral,
		Why:          "an unpaired device keeps its cached MASTER paramset, and a re-pair at the same address hydrates cache-first from the stale rows",
	}, e.StartCentral)
	return e
}
