// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package handlers

import (
	"context"
	"net/http"

	"github.com/SukramJ/openccu-loom/internal/north/rest/problem"
)

// RestartPendingProvider reports whether saved config changes require a
// daemon restart to take effect. Computed on demand (persisted vs. the
// running boot config over the restart-required field set), so it clears
// automatically once the values are reverted or the daemon restarts.
type RestartPendingProvider interface {
	Pending(ctx context.Context) (pending bool, fields []string, err error)
}

// RestartPendingResponse is the GET /system/restart-pending body.
type RestartPendingResponse struct {
	// Pending is true while at least one restart-required field has a
	// persisted value the running daemon has not adopted.
	Pending bool `json:"pending"`
	// Fields lists the affected config paths (e.g. "north.matter.enabled")
	// so the SPA can name them in the banner detail.
	Fields []string `json:"fields"`
}

// GetRestartPending answers whether a saved restart-required config
// change is staged but not yet active. The SPA surfaces a persistent
// banner while pending is true. A nil provider degrades to "not pending"
// rather than erroring — the banner simply never shows.
func GetRestartPending(p RestartPendingProvider) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if p == nil {
			JSON(w, http.StatusOK, RestartPendingResponse{Pending: false, Fields: []string{}})
			return
		}
		pending, fields, err := p.Pending(r.Context())
		if err != nil {
			writeServerError(w, r, http.StatusInternalServerError, problem.TypeInternal, "Restart-pending check failed", err)
			return
		}
		if fields == nil {
			fields = []string{}
		}
		JSON(w, http.StatusOK, RestartPendingResponse{Pending: pending, Fields: fields})
	}
}
