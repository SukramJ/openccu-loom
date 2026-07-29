// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package ws

import "time"

// addonUpdateTopic is the single WebSocket topic every addon_update
// broadcast rides. The self-updater is daemon-level (not per-central),
// so — like the alarm plane — the topic carries no <central> segment.
// Mirrors the wsapi.json `system.addon_update` topic.
const addonUpdateTopic = "system.addon_update"

// broadcastAddonUpdateStateChanged is the wire-level `type` string the
// SPA switches on. Mirrors wsapi.json's `addon_update.state_changed`.
const broadcastAddonUpdateStateChanged = "addon_update.state_changed"

// AddonUpdateStatusPayload mirrors the OpenAPI AddonUpdateStatus
// schema verbatim — keep the json tags in lockstep with
// assets/openapi.yaml and internal/north/rest/handlers.AddonUpdateStatusResponse.
type AddonUpdateStatusPayload struct {
	Supported       bool   `json:"supported"`
	CurrentVersion  string `json:"current_version"`
	LatestVersion   string `json:"latest_version,omitempty"`
	UpdateAvailable bool   `json:"update_available,omitempty"`
	ReleaseURL      string `json:"release_url,omitempty"`
	LastCheck       string `json:"last_check,omitempty"`
	State           string `json:"state"`
	Error           string `json:"error,omitempty"`
}

// PublishAddonUpdateStateChanged emits the add-on self-updater's
// current status snapshot on the daemon-level system.addon_update
// topic. Callers wire this as the [addonupdate.Updater.OnChange]
// callback so every state-machine transition reaches the SPA and any
// other WebSocket client live.
func (h *Hub) PublishAddonUpdateStateChanged(payload AddonUpdateStatusPayload, when time.Time) {
	h.Publish(Event{
		Topic:   addonUpdateTopic,
		Type:    broadcastAddonUpdateStateChanged,
		When:    when,
		Payload: payload,
	})
}
