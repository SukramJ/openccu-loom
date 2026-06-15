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

// WireValuesCacheEviction subscribes every central's EventBus to
// DeviceRemovedEvent and deletes the removed device's rows from the
// persistent values cache. Without it, unpairing a device leaves its cached
// rows behind indefinitely: nothing on the device-removal path
// (coordinators/device.go) touches the SQLite store, which only ever
// overwrites a row when the same address is re-paired and its current
// channels are flushed again. Pass a nil store or registry to disable. The
// returned closer unsubscribes every handler.
func WireValuesCacheEviction(
	reg *central.Registry,
	store *sqlite.ValuesCacheStore,
	logger *slog.Logger,
) func() {
	if reg == nil || store == nil {
		return func() {}
	}
	var unsubs []func()
	for _, unit := range reg.List() {
		if unit == nil || unit.EventBus == nil {
			continue
		}
		bus := unit.EventBus
		unsub := events.Subscribe(bus, func(ev hmevent.DeviceRemovedEvent) {
			//nolint:contextcheck // bus handlers carry no ctx; the removal DELETE is bounded by valuesCacheEvictTimeout
			ctx, cancel := context.WithTimeout(context.Background(), valuesCacheEvictTimeout)
			defer cancel()
			if err := store.DeleteDevice(ctx, ev.CentralName, ev.InterfaceID, ev.Address); err != nil && logger != nil {
				logger.Warn("values_cache.evict_err",
					slog.String("central", ev.CentralName),
					slog.String("interface", ev.InterfaceID),
					slog.String("address", ev.Address),
					slog.String("err", err.Error()))
			}
		})
		unsubs = append(unsubs, unsub)
	}

	var once sync.Once
	return func() {
		once.Do(func() {
			for _, u := range unsubs {
				u()
			}
		})
	}
}
