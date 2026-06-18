// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"strings"

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
	// correlate it against the matching InterfaceClient's tracker. One closure
	// handles all interfaces — the ifID argument dispatches to the right
	// client. Repeated calls from each interface's WirePingPongBus invocation
	// are harmless: the last installed closure wins and is identical.
	//
	// The CCU echoes the ping caller_id as the PONG value and broadcasts PONG
	// events to EVERY registered logic-layer client — so on a shared CCU we
	// also receive other daemons' PONGs (e.g. "OtherLoom-OttoLoom-HmIP-RF#<n>")
	// plus the bare-name liveness probes (no token) on our own interface.
	// Correlate ONLY when the caller_id carries a '#' token AND its prefix
	// equals this client's own wire-boundary id — the `<instance>-<central>-
	// <interface>` triple it embeds in its own pings. Matching on the bare
	// interface name would be blind to a second daemon (which sends the same
	// bare prefix), so its PONGs would be filed as unmatched "unknown"
	// mismatches and decay interface health. Mirrors the reference
	// v_interface_id == interface_id guard (central/coordinators/event.py:211).
	if unit.Events != nil && unit.Clients != nil {
		unit.Events.SetPingPongTracker(func(ifID, callerID string) {
			entry, ok := unit.Clients.Get(ifID)
			if !ok || entry == nil || entry.Client == nil {
				return
			}
			prefix, token, hasToken := strings.Cut(callerID, "#")
			if !hasToken || prefix != entry.Client.WireBoundaryID() {
				return
			}
			entry.Client.RecordPong(token)
		})
	}
}
