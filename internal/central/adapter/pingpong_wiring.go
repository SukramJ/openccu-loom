// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/coordinators"
	"github.com/SukramJ/openccu-loom/internal/central/events"
	clientpkg "github.com/SukramJ/openccu-loom/internal/client"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
)

// WirePingPongBus installs the two runtime hooks that connect a
// [client.InterfaceClient]'s PingPong tracker to the central event bus and
// the connection-recovery coordinator.
//
// After this call: - Every threshold crossing (pending or unknown count >
// MismatchThreshold) publishes a [hmevent.PingPongMismatchEvent] on the
// central's event bus. The existing [WireHealth] subscriber in
// health_wiring.go picks it up and records a degraded health sample. -
// [RecordPing] is gated by [ConnectionRecoveryCoordinator.InRecovery]: while
// a known CCU outage is being recovered, PINGs are not tracked so the tracker
// does not accumulate false-alarm pending mismatches.
//
// Nil arguments are safe: missing bus → publish hook not installed; missing
// recovery → gate not installed; missing unit/iface → early return with
// no-ops.
func WirePingPongBus(
	unit *central.Unit,
	ic *clientpkg.InterfaceClient,
	interfaceID string,
	recovery *coordinators.ConnectionRecoveryCoordinator,
) {
	if unit == nil || ic == nil || interfaceID == "" {
		return
	}
	bus := unit.EventBus
	centralName := unit.Name()

	if bus != nil {
		ic.SetPublishHook(func(kind hmenum.PingPongMismatchType, count int) {
			stats := ic.PingPong().Stats()
			events.Publish(bus, hmevent.PingPongMismatchEvent{
				Base:         hmevent.NewBase(),
				CentralName:  centralName,
				InterfaceID:  interfaceID,
				MismatchType: kind,
				PendingCount: stats.Pending,
				UnknownCount: stats.Unknown,
			})
		})
	}

	if recovery != nil {
		ic.SetConnectionIssueGate(func() bool {
			return recovery.InRecoveryFor(interfaceID)
		})
	}

	// Wire the PONG ingest: when a PONG event arrives for any interface,
	// route the extracted token to the matching InterfaceClient so the
	// tracker can close the pending round-trip. One closure handles all
	// interfaces — the ifID argument dispatches to the right client.
	// Repeated calls from each interface's WirePingPongBus invocation are
	// harmless: the last installed closure wins and is identical in behaviour.
	if unit.Events != nil && unit.Clients != nil {
		unit.Events.SetPingPongTracker(func(ifID, pongToken string) {
			if entry, ok := unit.Clients.Get(ifID); ok && entry != nil && entry.Client != nil {
				entry.Client.RecordPong(pongToken)
			}
		})
	}
}
