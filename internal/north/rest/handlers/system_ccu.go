// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package handlers

import (
	"context"
	"net/http"
)

// SystemCCUReader is the read facade `GET /api/v1/system/ccu` pulls
// from. The adapter walks the daemon's central registry and emits one
// SystemCCUEntry per configured CCU.
type SystemCCUReader interface {
	List(ctx context.Context) []SystemCCUEntry
}

// SystemCCUEntry surfaces the CCU-side metadata HA Repair-Flows and
// the SPA's diagnostics view need: identity (name, central-id,
// hostname, URL), software (model, version), and the configured
// interface list. Empty-string fields mean "not yet discovered" —
// the daemon populates `SystemInformation` after the first connect
// round; until then everything except `name` and `configured_interfaces`
// may be empty.
type SystemCCUEntry struct {
	// Name is the daemon-local central name (config-driven).
	Name string `json:"name"`
	// Host is the configured CCU hostname / IP.
	Host string `json:"host"`
	// Available reports the central's last-known connectivity
	// (true after a successful XML-RPC init handshake).
	Available bool `json:"available"`
	// Model surfaces SystemInfo.Model (CCU2 / CCU3 / RaspberryMatic / …).
	Model string `json:"model,omitempty"`
	// Version surfaces SystemInfo.Version (CCU software version).
	Version string `json:"version,omitempty"`
	// Hostname is the CCU-reported hostname (separate from the
	// configured Host above — they often differ when the CCU is
	// reached via reverse-proxy or alt DNS).
	Hostname string `json:"hostname,omitempty"`
	// Serial is the CCU's serial number.
	Serial string `json:"serial,omitempty"`
	// URL is the CCU's configuration URL (typically the WebUI root).
	URL string `json:"url,omitempty"`
	// IsHaApp reports whether the CCU runs the openCCU/HA-app firmware
	// variant — useful for HA-Discovery debugging.
	IsHaApp bool `json:"is_ha_app"`
	// ConfiguredInterfaces lists the InterfaceSpec.Name values from
	// the daemon's config (HmIP-RF, BidCos-RF, …) so HA can validate
	// that the daemon manages the same interfaces the operator
	// expects.
	ConfiguredInterfaces []string `json:"configured_interfaces"`
}

// SystemCCU serves the multi-central CCU metadata at
// `GET /api/v1/system/ccu`. The endpoint always returns 200 with an
// `entries` array — empty for daemons that have no central
// configured.
func SystemCCU(reader SystemCCUReader) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var entries []SystemCCUEntry
		if reader != nil {
			entries = reader.List(r.Context())
		}
		if entries == nil {
			entries = []SystemCCUEntry{}
		}
		JSON(w, http.StatusOK, map[string]any{"entries": entries})
	}
}
