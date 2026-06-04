// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package main

import (
	"log/slog"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/coordinators"
	"github.com/SukramJ/openccu-loom/internal/config"
)

// registerStandardJobs registers the standard per-central background jobs
// BEFORE StartAll fires the scheduler. Without this, the
// `central.health_heartbeat` job never runs and the per-central
// "central" component decays to UNKNOWN ~90 s after boot via the
// tracker's StaleAfter rule. `central.check_connection` is the
// other unconditional job — it advances each interface's
// circuit-breaker OPEN → HALF_OPEN → CLOSED on its own probe
// cadence, no caller need invoke it.
func registerStandardJobs(reg *central.Registry, cfg *config.Config, logger *slog.Logger) {
	for _, u := range reg.List() {
		jobs := central.StandardJobs{}
		// Apply per-central overrides from the configuration. Currently
		// only check_connection_interval is overridable; zero means "use
		// the compiled-in default".
		for i := range cfg.Centrals {
			if cfg.Centrals[i].Name == u.Name() && cfg.Centrals[i].CheckConnectionInterval > 0 {
				jobs.CheckConnectionInterval = cfg.Centrals[i].CheckConnectionInterval
			}
		}
		if u.Hub != nil {
			// Hub-Refresh-Hooks delegate through the HubCoordinator's
			// RefreshXxx methods. The inner hooks (loadPrograms,
			// loadSysvars, …) are wired by WireHub after the JSON-RPC
			// session comes up; until then RefreshXxx returns nil. By
			// registering the jobs unconditionally here, the scheduler
			// picks up the cadence and starts firing the moment WireHub
			// installs the closures.
			jobs.ProgramRefresh = u.Hub.RefreshPrograms
			jobs.SysvarRefresh = u.Hub.RefreshSysvars
			jobs.InboxRefresh = u.Hub.RefreshInbox
			jobs.ServiceMessagesRefresh = u.Hub.RefreshServiceMessages
			jobs.AlarmMessagesRefresh = u.Hub.RefreshAlarmMessages
			jobs.SystemUpdateRefresh = u.Hub.RefreshSystemUpdate
			jobs.InstallModeRefresh = u.Hub.RefreshInstallMode
			jobs.HubMetricsRefresh = u.Hub.RefreshMetrics
			jobs.HubConnectivityRefresh = u.Hub.RefreshConnectivity
		}
		// Wire and register the Reconciler so the slow-cadence
		// connectivity/health pass emits ConnectivityChangedEvent on
		// drift. The Connectivity and Metrics slots come from the Hub
		// aggregate (wired by WireHub once the JSON-RPC session is up,
		// nil-tolerant before then). Without these the per-job
		// reconcileConnectivity / reconcileSystemHealth passes would
		// land on nil and short-circuit — the slow drift sweep would
		// never fire even though the job slot was registered.
		if u.Reconciler == nil {
			u.Reconciler = &coordinators.Reconciler{
				CentralName:  u.Name(),
				Bus:          u.EventBus,
				Connectivity: u.HubModel.ConnectivityDataPoints(),
				Metrics:      u.HubModel.Metrics,
			}
		}
		jobs.Reconcile = u.Reconciler.Reconcile
		if _, err := central.RegisterStandardJobs(u, jobs); err != nil {
			logger.Warn("central.standard_jobs.register_failed",
				slog.String("central", u.Name()),
				slog.String("err", err.Error()))
		}
	}
}
