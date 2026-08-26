// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"strings"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/coordinators"
	"github.com/SukramJ/openccu-loom/internal/central/events"
	clientpkg "github.com/SukramJ/openccu-loom/internal/client"
	"github.com/SukramJ/openccu-loom/internal/metrics"
	"github.com/SukramJ/openccu-loom/internal/model/hub"
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
			matched, rtt := entry.Client.RecordPong(token)
			if matched {
				observePingPongRTT(unit, ifID, rtt)
			}
		})
	}
}

// observePingPongRTT files a matched PING→PONG round-trip on the two surfaces
// that report connection latency.
//
// This is the only full round-trip the daemon can measure: the PING leaves
// over the interface's own transport (XML-RPC for the CCU interfaces, BIN-RPC
// for CUxD) and the PONG returns as an event on our callback server, so the
// sample covers the reply path a one-way RPC timing never sees.
//
//   - The metrics observer keyed by interface. [metrics.Aggregator.RPC] reads
//     the `ping_pong.rtt` prefix back for the `avg_latency_ms` / `max_latency_ms`
//     fields of the diagnostics `rpc` section, which had no producer at all
//     and therefore reported a constant zero.
//   - The hub's central-wide connection-latency metric, which backs the sensor
//     MQTT discovery declares, the hub data-point list enumerates and
//     /system/ccu reports. It previously carried the duration of one JSON-RPC
//     `Interface.listInterfaces` call — a different, one-way path measured on
//     the reconciler's slow cadence.
//
// The hub metric is central-wide by contract (one sensor per CCU, not one per
// interface), so each interface's sample overwrites the last: the sensor reads
// as the latency of the most recently confirmed round-trip. Every backend that
// declares [backends.Capabilities.PingPong] contributes, so the reading spans
// both transports rather than the JSON-RPC surface alone.
func observePingPongRTT(unit *central.Unit, interfaceID string, rtt time.Duration) {
	if unit == nil || rtt <= 0 {
		return
	}
	ms := float64(rtt.Nanoseconds()) / float64(time.Millisecond)
	if obs := unitObserver(unit); obs != nil {
		obs.ObserveLatency(metrics.MetricKeys.PingPongRTT(interfaceID).String(), ms)
	}
	if unit.HubModel != nil && unit.HubModel.Metrics != nil {
		unit.HubModel.Metrics.Observe(hub.MetricConnectionLatMs, ms)
	}
}
