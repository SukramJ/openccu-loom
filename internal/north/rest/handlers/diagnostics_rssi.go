// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package handlers

import (
	"context"
	"net/http"

	"github.com/SukramJ/openccu-loom/internal/north/rest/problem"
)

// RSSIMatrixService is the read surface behind GET /diagnostics/rssi. The
// same implementation (adapter.RSSIInfoDomain) also backs the
// `ccu.get_rssi_info` WS command.
type RSSIMatrixService interface {
	// RSSIInfo returns { "devices": [...] } — the CCU's pairwise RF
	// reception matrix across every central and RF interface.
	RSSIInfo(ctx context.Context) (map[string]any, error)
}

// DiagnosticsRSSI serves GET /diagnostics/rssi — the CCU's pairwise RF
// reception matrix (device ↔ communication-partner RSSI pairs) read from the
// XML-RPC `rssiInfo` method, with the 65536 "no data" sentinel normalised to
// null. Read-only; safe on a live CCU.
func DiagnosticsRSSI(svc RSSIMatrixService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			problem.Write(w, http.StatusServiceUnavailable,
				problem.New(problem.TypeServiceUnready, r, "Diagnostics unavailable", "no RSSI source"))
			return
		}
		matrix, err := svc.RSSIInfo(r.Context())
		if err != nil {
			problem.Write(w, http.StatusInternalServerError,
				problem.New(problem.TypeInternal, r, "RSSI query failed", err.Error()))
			return
		}
		JSON(w, http.StatusOK, matrix)
	}
}
