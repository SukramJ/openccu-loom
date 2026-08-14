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
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
)

// valuesCacheEvictTimeout bounds the single-device DELETE the eviction
// handler runs when a device is removed.
const valuesCacheEvictTimeout = 30 * time.Second

// ValuesCacheEvictor is the handle [WireValuesCacheEviction] returns. It holds
// the per-central DeviceRemovedEvent subscriptions that delete a removed
// device's persisted cache rows.
type ValuesCacheEvictor struct {
	store  *sqlite.ValuesCacheStore
	logger *slog.Logger

	mu     sync.Mutex
	unsubs []func()
	once   sync.Once
}

// StartCentral subscribes one central's EventBus to DeviceRemovedEvent. It is
// the seam the composition root calls for a runtime-adopted CCU and the same
// call the boot-time wiring makes for every configured one: a central that
// joined after wiring time would otherwise keep every unpaired device's rows
// forever, since nothing else on the removal path touches the store.
//
// The returned closure releases the subscription; nil-safe and idempotent.
func (e *ValuesCacheEvictor) StartCentral(u *central.Unit) func() {
	if e == nil || u == nil || u.EventBus == nil {
		return func() {}
	}
	unsub := events.Subscribe(u.EventBus, func(ev hmevent.DeviceRemovedEvent) {
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
	e.mu.Lock()
	e.unsubs = append(e.unsubs, unsub)
	e.mu.Unlock()

	var once sync.Once
	return func() { once.Do(unsub) }
}

// Stop releases every subscription. Idempotent and nil-safe.
func (e *ValuesCacheEvictor) Stop() {
	if e == nil {
		return
	}
	e.once.Do(func() {
		e.mu.Lock()
		unsubs := e.unsubs
		e.unsubs = nil
		e.mu.Unlock()
		for _, u := range unsubs {
			u()
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
	for _, unit := range reg.List() {
		e.StartCentral(unit)
	}
	return e
}
