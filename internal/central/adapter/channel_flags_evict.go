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
	"github.com/SukramJ/openccu-loom/internal/channelflags"
	"github.com/SukramJ/openccu-loom/internal/store/sqlite"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
)

// channelFlagsEvictTimeout bounds the single-device DELETE the eviction
// handler runs when a device is removed.
const channelFlagsEvictTimeout = 30 * time.Second

// ChannelFlagsEvictor is the handle [WireChannelFlagsEviction] returns. It
// mirrors [ValuesCacheEvictor], for the operator-set per-channel Hidden/
// Locked overrides (G12): without it, a device unpaired and later re-paired
// at the same address — a factory reset is the common path, since the CCU
// keeps the serial as the address — silently inherits the previous
// pairing's overrides instead of starting with none.
//
// loom:reachable:reason="returned by WireChannelFlagsEviction and held by the daemon's teardown list in cmd/openccu-loom/daemon_southbound.go; the analyzer resolves the constructor call but not the type it yields"
type ChannelFlagsEvictor struct {
	store   *sqlite.ChannelFlagsStore
	overlay *channelflags.Overlay
	logger  *slog.Logger

	// removeObserver detaches the registry observer and every per-central
	// subscription it attached.
	removeObserver func()
	once           sync.Once
}

// StartCentral subscribes one central's EventBus to DeviceRemovedEvent. It is
// the observer the registry runs per central, for boot-time and
// runtime-adopted CCUs alike: a central that joined after wiring time would
// otherwise keep every unpaired device's flag rows forever, since nothing
// else on the removal path touches the store or the overlay.
//
// A [hmevent.DeviceRemovedEvent] with ModelTeardown set is skipped: a
// cache-clear re-init drops the whole model and re-pulls it, and the
// operator never asked for that device's flags to go — purging here would
// take every device's Hidden/Locked overrides with it on every teardown.
//
// The returned closure releases the subscription; nil-safe and idempotent.
func (e *ChannelFlagsEvictor) StartCentral(u *central.Unit) func() {
	if e == nil || u == nil || u.EventBus == nil {
		return func() {}
	}
	unsub := events.Subscribe(u.EventBus, func(ev hmevent.DeviceRemovedEvent) {
		if ev.ModelTeardown {
			return
		}
		e.overlay.DeleteDevice(ev.CentralName, ev.Address)
		//nolint:contextcheck // bus handlers carry no ctx; the removal DELETE is bounded by channelFlagsEvictTimeout
		ctx, cancel := context.WithTimeout(context.Background(), channelFlagsEvictTimeout)
		defer cancel()
		if err := e.store.DeleteDevice(ctx, ev.CentralName, ev.Address); err != nil && e.logger != nil {
			e.logger.Warn("channel_flags.evict_err",
				slog.String("central", ev.CentralName),
				slog.String("address", ev.Address),
				slog.String("err", err.Error()))
		}
	})
	var once sync.Once
	return func() { once.Do(unsub) }
}

// Stop releases every subscription. Idempotent and nil-safe.
func (e *ChannelFlagsEvictor) Stop() {
	if e == nil {
		return
	}
	e.once.Do(func() {
		if e.removeObserver != nil {
			e.removeObserver()
		}
	})
}

// WireChannelFlagsEviction subscribes every central's EventBus to
// DeviceRemovedEvent and deletes the removed device's Hidden/Locked
// overrides from both the persistent store and the in-memory overlay. Pass
// a nil store, overlay or registry to disable — the returned handle is nil
// and every method on it is a no-op.
func WireChannelFlagsEviction(
	reg *central.Registry,
	store *sqlite.ChannelFlagsStore,
	overlay *channelflags.Overlay,
	logger *slog.Logger,
) *ChannelFlagsEvictor {
	if reg == nil || store == nil || overlay == nil {
		return nil
	}
	e := &ChannelFlagsEvictor{store: store, overlay: overlay, logger: logger}
	e.removeObserver = reg.OnRegister(e.StartCentral)
	return e
}
