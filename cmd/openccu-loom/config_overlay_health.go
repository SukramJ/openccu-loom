// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package main

import (
	"strings"

	"github.com/SukramJ/openccu-loom/internal/health"
	"github.com/SukramJ/openccu-loom/internal/metrics"
	sqlitestore "github.com/SukramJ/openccu-loom/internal/store/sqlite"
)

const (
	// configOverlayHealthComponent is the /health component reporting whether
	// the DB-tier config sections were merged onto the boot config.
	configOverlayHealthComponent = "config.overlay"
	// configOverlayFailedMetric is 1 when the DB-tier overlay failed and the
	// daemon runs on the config file alone, else 0.
	configOverlayFailedMetric = "config_overlay_failed"
	// configCentralsHealthComponent is the /health component reporting stored
	// centrals the daemon refused to start because their name is not routable.
	configCentralsHealthComponent = "config.centrals"
	// configUnroutableCentralsMetric counts the stored central rows skipped
	// because the callback router cannot match their name.
	configUnroutableCentralsMetric = "config_unroutable_centrals"
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
//
// Recorded exactly once, at boot, with no periodic refresher — Sticky so it
// does not decay to StatusUnknown 90s later and drag a genuinely healthy
// daemon's /health to "unknown" for the rest of the process lifetime.
func recordConfigOverlayHealth(tracker *health.Tracker, reg *metrics.Registry, err error) {
	if tracker != nil {
		// Sticky: a one-shot boot-time fact recorded exactly once — see
		// the matching comment in recordSecretHealth for why the decay
		// must not apply to it.
		if err == nil {
			tracker.Record(configOverlayHealthComponent, health.Sample{
				Healthy: true, Note: "database sections applied", Sticky: true,
			})
		} else {
			tracker.Record(configOverlayHealthComponent, health.Sample{
				Healthy: false,
				Note:    "database config sections not applied: " + err.Error(),
				Sticky:  true,
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

// recordUnroutableCentralHealth surfaces the stored centrals the daemon left
// out of its config because the callback router cannot match their name. Such
// a CCU used to be started anyway: it connected, reported healthy on every
// surface, and received not one push event, so every data point stayed
// unobserved until somebody compared the callback URL with the router's
// allowlist by hand.
//
// The component is non-critical — the rest of the daemon, including any other
// CCU, runs normally — so it must not turn liveness probes red. It names the
// rows so the operator can find and re-create them.
//
// Recorded exactly once, at boot, with no periodic refresher — Sticky for
// the same reason as [recordConfigOverlayHealth]: it reports a boot-time
// fact, not a heartbeat, and must not decay to StatusUnknown 90s later.
func recordUnroutableCentralHealth(tracker *health.Tracker, reg *metrics.Registry, names []string) {
	if tracker != nil {
		// Sticky: a one-shot boot-time fact recorded exactly once — see
		// the matching comment in recordSecretHealth for why the decay
		// must not apply to it.
		if len(names) == 0 {
			tracker.Record(configCentralsHealthComponent, health.Sample{
				Healthy: true, Note: "all stored centrals are routable", Sticky: true,
			})
		} else {
			tracker.Record(configCentralsHealthComponent, health.Sample{
				Healthy: false,
				Sticky:  true,
				Note: "not started, the CCU callback would be rejected: " + strings.Join(names, ", ") +
					" — re-add each CCU with a name of letters, digits, \"-\" and \"_\"",
			})
		}
	}
	if reg != nil {
		reg.Gauge(configUnroutableCentralsMetric,
			"number of stored centrals not started because their name is not a routable callback path segment").
			Set(float64(len(names)))
	}
}

// recordSQLiteOpenFailureHealth registers the critical `sqlite` /health
// component as unhealthy when the shared <DataDir>/openccu-loom.db handle
// never opened (openLoomDB returned a nil db).
//
// Without this, a nil db leaves the component unregistered rather than
// unhealthy: sqlite.StartHealthProbe only starts when the handle is non-nil
// (daemon_rest.go), and recordSecretHealth / recordConfigOverlayHealth /
// recordUnroutableCentralHealth are all guarded on the same nil check — so
// the one component health.isCriticalComponent maps to HTTP 503 is simply
// absent, and ServiceAvailability only inspects components present in the
// snapshot. GET /health answers 200 "healthy" while every CCU list, every
// SPA-saved config section and every audit row is permanently inactive.
//
// Sticky: there is no periodic refresher for this verdict (a failed boot
// open is never retried), matching recordSecretHealth /
// recordConfigOverlayHealth for the same reason — it must not decay to
// StatusUnknown 90s later, which would only trade one silently-wrong
// "healthy" reading for an equally wrong "unknown" one instead of the
// unhealthy/503 the condition actually warrants.
func recordSQLiteOpenFailureHealth(tracker *health.Tracker, openErr error) {
	if tracker == nil {
		return
	}
	note := "shared database failed to open at boot"
	if openErr != nil {
		note += ": " + openErr.Error()
	}
	tracker.Record(sqlitestore.StoreComponentName, health.Sample{
		Healthy: false, Sticky: true, Note: note,
	})
}
