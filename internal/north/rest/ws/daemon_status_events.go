// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package ws

import (
	"context"
	"time"
)

// daemonStatusTopic is the single WebSocket topic the daemon's own
// liveness rides. The daemon is not per-central — one process serves
// every configured CCU — so, like the add-on-update plane, the topic
// carries no <central> segment. Mirrors the wsapi.json
// `system.daemon_status` topic.
const daemonStatusTopic = "system.daemon_status"

// broadcastDaemonStatusChanged is the wire-level `type` string clients
// switch on. Mirrors wsapi.json's `daemon_status.changed`.
const broadcastDaemonStatusChanged = "daemon_status.changed"

// Daemon status values. They are the same two words the MQTT bridge
// retains on `<base>/bridge/status`, so a client bridging both planes
// does not have to translate between them.
const (
	DaemonStatusOnline  = "online"
	DaemonStatusOffline = "offline"
)

// DaemonStatusPayload is the body of a `daemon_status.changed`
// broadcast.
type DaemonStatusPayload struct {
	// Status is "online" or "offline".
	Status string `json:"status"`
	// Reason is a short machine-readable cause for an offline
	// announcement ("shutdown"), empty otherwise. It separates "the
	// daemon is stopping" from "the connection dropped", which is the
	// distinction a client cannot otherwise draw: both look like a
	// socket that stopped answering.
	Reason  string    `json:"reason,omitempty"`
	EventAt time.Time `json:"event_at"`
}

// DaemonStatusTopic returns the canonical topic daemon-liveness
// broadcasts ride, for clients and tests that subscribe to it.
func DaemonStatusTopic() string { return daemonStatusTopic }

// PublishDaemonShuttingDown announces that this daemon is stopping, on
// the daemon-status topic, and waits briefly for the announcement to
// reach the wire.
//
// It is the WebSocket counterpart of the MQTT bridge's AnnounceOffline.
// On the MQTT plane a broker holds the daemon's last will, so a client
// learns about a stopped daemon whether the stop was graceful or not.
// A WebSocket client has no such third party: all it observes is a
// socket that stopped answering, which is indistinguishable from its own
// network dropping — the state reported in #591, where a CCU reboot
// showed up only as reconnect warnings in a log while every entity
// stayed available.
//
// This closes the graceful half of that gap: an add-on stop, a
// `systemctl stop`, a CCU reboot that shuts services down in order. The
// ungraceful half — SIGKILL, a power cut — cannot be announced by the
// process that is being killed, and stays the client's own job to detect
// from its connection state.
//
// The wait exists because the fan-out is asynchronous: Publish hands the
// event to each client's writer goroutine, and the HTTP server's
// shutdown does not wait for hijacked WebSocket connections. Without it
// the announcement would be a race against teardown that it usually
// loses.
func (h *Hub) PublishDaemonShuttingDown(ctx context.Context, when time.Time) {
	h.Publish(Event{
		Topic: daemonStatusTopic,
		Type:  broadcastDaemonStatusChanged,
		When:  when,
		Payload: DaemonStatusPayload{
			Status:  DaemonStatusOffline,
			Reason:  "shutdown",
			EventAt: when,
		},
	})
	h.drainPending(ctx)
}

// drainPendingPollInterval is how often [Hub.drainPending] re-checks the
// outbound queues. Short enough that a drained hub is not held up
// noticeably, long enough not to spin.
const drainPendingPollInterval = 2 * time.Millisecond

// drainPending blocks until every connected client's outbound queue is
// empty or ctx ends, whichever comes first.
//
// An empty queue means the writer goroutine has taken the event, not
// that the peer has acknowledged it — there is no acknowledgement on
// this plane. That is the strongest guarantee available here, and it is
// the difference between an announcement that is written to the socket
// and one that is still sitting in a channel when the process exits.
func (h *Hub) drainPending(ctx context.Context) {
	for {
		if h.pendingWrites() == 0 {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(drainPendingPollInterval):
		}
	}
}

// pendingWrites is the total number of events queued across every
// connected client and not yet taken by a writer.
func (h *Hub) pendingWrites() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	n := 0
	for c := range h.clients {
		if c.isClosed() {
			continue
		}
		n += len(c.out)
	}
	return n
}
