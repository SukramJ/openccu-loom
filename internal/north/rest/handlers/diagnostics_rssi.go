// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

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
	// RSSIInfo returns { "devices": [...] } — per-device reception data
	// from the device model, across every central.
	RSSIInfo(ctx context.Context) (map[string]any, error)
}

// DiagnosticsRSSI serves GET /diagnostics/rssi — per-device reception data
// read from the in-memory device model's maintenance channel: RSSI_DEVICE and
// RSSI_PEER (dBm), battery level and low-battery state, and reachability, for
// every device that reports an RSSI reading, across all centrals. A reading
// the model does not hold is null. No CCU round-trip, so it works for HmIP and
// BidCos alike and is safe on a live CCU.
func DiagnosticsRSSI(svc RSSIMatrixService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			problem.Write(w, http.StatusServiceUnavailable,
				problem.New(problem.TypeServiceUnready, r, "Diagnostics unavailable", "no RSSI source"))
			return
		}
		matrix, err := svc.RSSIInfo(r.Context())
		if err != nil {
			writeServerError(w, r, http.StatusInternalServerError, problem.TypeInternal, "RSSI query failed", err)
			return
		}
		JSON(w, http.StatusOK, matrix)
	}
}
