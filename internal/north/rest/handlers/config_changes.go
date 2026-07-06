// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package handlers

import (
	"context"
	"net/http"

	"github.com/SukramJ/openccu-loom/internal/north/rest/problem"
)

// ConfigChangesProvider reports the config field paths whose persisted
// value differs from the running boot config — i.e. what changed since
// the daemon started, not what differs from the built-in default.
// Computed on demand, so reverting an edit drops it from the list.
type ConfigChangesProvider interface {
	Changes(ctx context.Context) (fields []string, err error)
}

// ConfigChangesResponse is the GET /system/config-changes body.
type ConfigChangesResponse struct {
	// Fields are the dotted config paths that differ from the boot
	// config; empty right after a clean start.
	Fields []string `json:"fields"`
}

// GetConfigChanges lists the config fields edited since the daemon
// started. The SPA's "Changed settings" overview renders these (reading
// the current values from the effective config), each revertible via
// DELETE /config/fields/{path}. A nil provider degrades to "no changes".
func GetConfigChanges(p ConfigChangesProvider) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if p == nil {
			JSON(w, http.StatusOK, ConfigChangesResponse{Fields: []string{}})
			return
		}
		fields, err := p.Changes(r.Context())
		if err != nil {
			writeServerError(w, r, http.StatusInternalServerError, problem.TypeInternal, "Config-changes check failed", err)
			return
		}
		if fields == nil {
			fields = []string{}
		}
		JSON(w, http.StatusOK, ConfigChangesResponse{Fields: fields})
	}
}
