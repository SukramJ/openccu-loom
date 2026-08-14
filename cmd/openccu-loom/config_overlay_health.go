// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package main

import (
	"github.com/SukramJ/openccu-loom/internal/health"
	"github.com/SukramJ/openccu-loom/internal/metrics"
)

const (
	// configOverlayHealthComponent is the /health component reporting whether
	// the DB-tier config sections were merged onto the boot config.
	configOverlayHealthComponent = "config.overlay"
	// configOverlayFailedMetric is 1 when the DB-tier overlay failed and the
	// daemon runs on the config file alone, else 0.
	configOverlayFailedMetric = "config_overlay_failed"
)

// recordConfigOverlayHealth surfaces whether the boot-time merge of the
// database config sections succeeded. When it did not, every section the
// operator saved in the SPA is not in effect and GET /api/v1/config fails
// with the same error, so the SPA cannot even show — let alone repair — the
// broken section. A degraded /health component and a gauge make the state
// observable without reading the boot log.
//
// The component is non-critical: the daemon runs correctly on the config
// file, so this must not turn liveness probes red.
func recordConfigOverlayHealth(tracker *health.Tracker, reg *metrics.Registry, err error) {
	if tracker != nil {
		if err == nil {
			tracker.Record(configOverlayHealthComponent, health.Sample{
				Healthy: true, Note: "database sections applied",
			})
		} else {
			tracker.Record(configOverlayHealthComponent, health.Sample{
				Healthy: false,
				Note:    "database config sections not applied: " + err.Error(),
			})
		}
	}
	if reg != nil {
		g := reg.Gauge(configOverlayFailedMetric,
			"1 when the database config sections could not be applied at boot, else 0")
		if err == nil {
			g.Set(0)
		} else {
			g.Set(1)
		}
	}
}
