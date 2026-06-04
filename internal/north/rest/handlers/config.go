// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package handlers

import (
	"net/http"

	"github.com/SukramJ/openccu-loom/internal/north/rest/problem"
)

// ConfigSnapshot is whatever the domain layer publishes as a
// sanitized view of the effective configuration. Fields are
// deliberately omitempty so the daemon can grow the shape without
// breaking clients.
type ConfigSnapshot struct {
	Locale        string            `json:"locale,omitempty"`
	Centrals      []ConfigCentral   `json:"centrals,omitempty"`
	CallbackPorts ConfigPorts       `json:"callback_ports,omitzero"`
	Features      map[string]bool   `json:"features,omitempty"`
	Extras        map[string]string `json:"extras,omitempty"`
	// Policies surfaces static daemon-side behaviour switches that
	// external clients (HA in particular) ask about: which hub
	// content gets surfaced, whether invisible devices show up,
	// etc. The current MVP exposes a fixed policy set; future
	// revisions may add operator-configurable knobs without
	// breaking the wire shape. Keys are stable; values are the
	// current effective setting. See `/config` description in
	// openapi.yaml for the enumerated keys.
	Policies map[string]bool `json:"policies,omitempty"`
}

// ConfigCentral describes one configured CCU.
type ConfigCentral struct {
	Name       string   `json:"name"`
	Host       string   `json:"host"`
	Interfaces []string `json:"interfaces"`
}

// ConfigPorts surfaces the effective callback server ports.
type ConfigPorts struct {
	XMLRPC int `json:"xmlrpc,omitempty"`
	BINRPC int `json:"binrpc,omitempty"`
}

// ConfigReader is the facade `GET /api/v1/config` depends on.
type ConfigReader interface {
	SanitizedConfig() ConfigSnapshot
}

// Config returns a handler rendering the sanitized daemon config.
// A nil reader degrades to 503 so the SPA can render a clear status
// instead of crashing the daemon.
func Config(reader ConfigReader) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if reader == nil {
			problem.Write(w, http.StatusServiceUnavailable,
				problem.New(problem.TypeServiceUnready, r, "Config reader unavailable", ""))
			return
		}
		JSON(w, http.StatusOK, reader.SanitizedConfig())
	}
}
