// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package ws

import "time"

// pairingTopic is the daemon-level topic pairing broadcasts ride —
// client pairing is not per-central. Mirrors wsapi.json
// `system.pairing`.
const pairingTopic = "system.pairing"

// broadcastPairingRequestsChanged is the wire-level `type` the SPA
// switches on. Mirrors wsapi.json's `pairing.requests_changed`.
const broadcastPairingRequestsChanged = "pairing.requests_changed"

// PairingRequestsChangedPayload announces that the pending pairing list
// changed. It deliberately carries only the count: codes and request
// details stay behind the admin-gated GET /pairing-requests, which the
// card fetches on this signal.
type PairingRequestsChangedPayload struct {
	Pending int `json:"pending"`
}

// PublishPairingRequestsChanged emits the change signal; callers wire
// it as the pairing manager's OnChange so the admin card updates live.
func (h *Hub) PublishPairingRequestsChanged(pending int, when time.Time) {
	h.Publish(Event{
		Topic:   pairingTopic,
		Type:    broadcastPairingRequestsChanged,
		When:    when,
		Payload: PairingRequestsChangedPayload{Pending: pending},
	})
}
