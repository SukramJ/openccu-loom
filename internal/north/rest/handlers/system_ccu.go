// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

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
	// AuthEnabled reports whether the CCU requires authentication on its
	// own interfaces. False both when the CCU runs unauthenticated and
	// when its firmware does not answer the query, so treat it as a hint
	// on a status page rather than as a security guarantee.
	AuthEnabled bool `json:"auth_enabled"`
	// HTTPSRedirectEnabled reports whether the CCU redirects plain HTTP
	// to HTTPS. Same caveat as AuthEnabled.
	HTTPSRedirectEnabled bool `json:"https_redirect_enabled"`
	// Longitude and Latitude are the CCU's astro reference position in
	// decimal degrees, Timezone its configured IANA zone. Pointers so an
	// unresolved position (older firmware, or bring-up not finished) is
	// null rather than 0/0 - which is a real place in the Atlantic and
	// would read as a plausible answer.
	Longitude *float64 `json:"longitude,omitempty"`
	Latitude  *float64 `json:"latitude,omitempty"`
	Timezone  string   `json:"timezone,omitempty"`
	// RecoveryModeSupported reports whether this CCU offers a recovery
	// system. Derived from the product label rather than probed: recovery
	// is an OpenCCU / RaspberryMatic feature, and a stock CCU3 has no such
	// JSON-RPC method. False while the product is still unknown, so a
	// client hides the action instead of offering one that cannot work.
	RecoveryModeSupported bool `json:"recovery_mode_supported"`
	// CCUInterfaces lists the interface adapters the CCU reports for
	// itself — the CCU-side counterpart to ConfiguredInterfaces above.
	// Omitted until the first successful connect round; a difference
	// between the two lists is the interesting signal.
	CCUInterfaces []SystemCCUInterface `json:"ccu_interfaces,omitempty"`
	// Readiness reports where the central is in its readiness-gated
	// southbound bring-up (see CentralReadiness).
	Readiness CentralReadiness `json:"readiness"`
}

// SystemCCUInterface is one interface adapter as the CCU itself reports
// it, including the XML-RPC port and endpoint URL it listens on.
type SystemCCUInterface struct {
	Type    string `json:"type"`
	Address string `json:"address"`
	Port    int    `json:"port"`
	URL     string `json:"url,omitempty"`
}

// CentralReadiness surfaces where a central is in its readiness-gated southbound
// bring-up so the SPA can distinguish "still initializing" from "offline".
type CentralReadiness struct {
	Phase            string `json:"phase"` // unknown|waiting_for_ccu|loading_hub|loading_devices|ready
	Ready            bool   `json:"ready"` // southbound bring-up latched complete
	InterfacesLoaded int    `json:"interfaces_loaded"`
	InterfacesTotal  int    `json:"interfaces_total"`
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
		if hideCCUCoordinates(r.Context()) {
			entries = withoutCCUCoordinates(entries)
		}
		JSON(w, http.StatusOK, map[string]any{"entries": entries})
	}
}

// withoutCCUCoordinates returns a copy of entries with every network
// coordinate blanked, leaving the parts a viewer legitimately needs on a
// status page: the daemon-local name, availability, readiness, the CCU
// model and firmware version, and the astro position the SPA renders.
//
// The reader hands back its own snapshot, so this copies before blanking —
// mutating in place would strip the coordinates for every later admin read
// as well.
func withoutCCUCoordinates(entries []SystemCCUEntry) []SystemCCUEntry {
	out := make([]SystemCCUEntry, len(entries))
	copy(out, entries)
	for i := range out {
		out[i].Host = ""
		out[i].Hostname = ""
		out[i].Serial = ""
		out[i].URL = ""
		// The adapter list itself is not sensitive — which radio the CCU
		// carries is already implied by ConfiguredInterfaces — but the port
		// and endpoint URL are the reachable coordinates.
		if len(out[i].CCUInterfaces) > 0 {
			ifaces := make([]SystemCCUInterface, len(out[i].CCUInterfaces))
			copy(ifaces, out[i].CCUInterfaces)
			for j := range ifaces {
				ifaces[j].Port = 0
				ifaces[j].URL = ""
				ifaces[j].Address = ""
			}
			out[i].CCUInterfaces = ifaces
		}
	}
	return out
}
