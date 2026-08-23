// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package handlers

import (
	"net/http"

	"github.com/SukramJ/openccu-loom/internal/north/rest/problem"
	"github.com/SukramJ/openccu-loom/pkg/hmapi"
	"github.com/SukramJ/openccu-loom/pkg/interfaces"
)

// ConfigSnapshot is an alias for the canonical DTO in pkg/hmapi.
type ConfigSnapshot = hmapi.ConfigSnapshot

// ConfigCentral is an alias for the canonical DTO in pkg/hmapi.
type ConfigCentral = hmapi.ConfigCentral

// ConfigPorts is an alias for the canonical DTO in pkg/hmapi.
type ConfigPorts = hmapi.ConfigPorts

// ConfigReader is an alias for the canonical interface in pkg/interfaces.
type ConfigReader = interfaces.ConfigReader

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
		cfg := reader.SanitizedConfig()
		if hideCCUCoordinates(r.Context()) {
			// Copy before blanking: SanitizedConfig may hand back a slice
			// backed by the reader's own snapshot, and blanking in place
			// would strip the host for every later admin read too.
			centrals := make([]hmapi.ConfigCentral, len(cfg.Centrals))
			copy(centrals, cfg.Centrals)
			for i := range centrals {
				centrals[i].Host = ""
			}
			cfg.Centrals = centrals
		}
		JSON(w, http.StatusOK, cfg)
	}
}
