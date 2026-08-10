// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package main

import (
	"context"
	"log/slog"
	"runtime/debug"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
)

// hubDiscoveryReadyDebounce coalesces a burst of CentralSouthboundReadyEvents
// (staggered multi-CCU bring-ups) into a single hub-publisher re-Start.
const hubDiscoveryReadyDebounce = 750 * time.Millisecond

// wireHubDiscoveryOnReady re-runs the hub publisher once each central's
// southbound bring-up has completed. The hub-discovery plane (sysvars,
// programs, the named central hub device, alarm/service messages, connectivity,
// install-mode, …) is gated on the CCU serial, which is resolved only inside
// the async, readiness-gated bring-up — AFTER the eager boot-time Start has
// already run with an empty serial and skipped every hub payload (raw state,
// not serial-gated, keeps flowing, which is why the plane looked half-alive).
// Subscribing to CentralSouthboundReadyEvent closes that gap: the re-Start
// re-stamps each central's serial from the registry (see hubInfoFromUnit) and
// re-publishes the hub discovery with it, so the central device is named — no
// "unknown device" parent — and sysvar/device assignments surface.
//
// The event handler only signals (non-blocking) so it never blocks the
// serialized bus dispatch; the Start runs on a dedicated debounce goroutine
// that stops when ctx is cancelled. Returns the subscription closers plus the
// non-blocking trigger, so the live-adopt hook can subscribe a runtime-added
// central's bus onto the same pipeline later. The debounce goroutine starts
// whenever restart is non-nil — even with zero boot-time buses — because a
// daemon can boot with no configured centrals and adopt its first one at
// runtime.
func wireHubDiscoveryOnReady(
	ctx context.Context,
	buses []*events.Bus,
	restart func(context.Context),
	debounce time.Duration,
	logger *slog.Logger,
) (closers []func(), trigger func()) {
	if restart == nil {
		return nil, nil
	}
	if logger == nil {
		logger = slog.Default()
	}
	signal := make(chan struct{}, 1)
	trigger = func() {
		select {
		case signal <- struct{}{}:
		default:
		}
	}
	for _, bus := range buses {
		if unsub := subscribeHubReadyTrigger(bus, trigger); unsub != nil {
			closers = append(closers, unsub)
		}
	}
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-signal:
			}
			// Debounce: wait for a quiet window so a staggered multi-CCU boot
			// coalesces into one re-Start instead of one per central.
			for settling := true; settling; {
				select {
				case <-ctx.Done():
					return
				case <-signal:
				case <-time.After(debounce):
					settling = false
				}
			}
			runHubDiscoveryRestart(ctx, restart, logger)
		}
	}()
	return closers, trigger
}

// subscribeHubReadyTrigger subscribes trigger to bus's
// CentralSouthboundReadyEvent. Shared by the boot-time wiring
// ([wireHubDiscoveryOnReady]) and the live-adopt path so both feed the same
// debounce pipeline. Returns nil when there is nothing to wire.
func subscribeHubReadyTrigger(bus *events.Bus, trigger func()) func() {
	if bus == nil || trigger == nil {
		return nil
	}
	return events.Subscribe(bus, func(hmevent.CentralSouthboundReadyEvent) {
		trigger()
	})
}

// runHubDiscoveryRestart invokes restart with panic isolation so a fault in the
// publisher re-wire cannot crash the daemon from the debounce goroutine.
//
// The stack goes into the record because this is the only trace that survives:
// the re-wire runs on its own goroutine, the recover consumes the panic, and
// the daemon carries on with a hub plane that Start had already torn down and
// then failed to rebuild. Without the stack an operator report of this line
// carries no way to locate the fault.
func runHubDiscoveryRestart(ctx context.Context, restart func(context.Context), logger *slog.Logger) {
	defer func() {
		if r := recover(); r != nil {
			logger.Error("mqtt.hub_discovery.restart_on_ready.panic",
				slog.Any("panic", r),
				slog.String("stack", string(debug.Stack())))
		}
	}()
	restart(ctx)
}
