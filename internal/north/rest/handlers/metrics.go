// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package handlers

import (
	"net/http"

	"github.com/SukramJ/openccu-loom/internal/metrics"
)

// MetricsHandler renders the registry in Prometheus exposition format.
func MetricsHandler(reg *metrics.Registry) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		if reg == nil {
			return
		}
		_ = reg.Render(w)
	}
}
