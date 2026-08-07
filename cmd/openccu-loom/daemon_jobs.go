// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package main

import (
	"context"
	"log/slog"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/adapter"
	"github.com/SukramJ/openccu-loom/internal/central/coordinators"
	clientpkg "github.com/SukramJ/openccu-loom/internal/client"
	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/internal/model/hub"
	"github.com/SukramJ/openccu-loom/internal/scheduler"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
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
		registerStandardJobsFor(u, cfg, logger)
	}
}

// registerStandardJobsFor registers one central's standard background jobs.
// Factored out of registerStandardJobs's per-unit loop body so the live
// CCU-adopt orchestrator (central_adopt.go) can register the same jobs for a
// single runtime-added central.
// cfg.Centrals is the boot-time static array; a runtime-added central absent
// from it simply gets no override (falls back to compiled-in defaults),
// which is the same "zero means default" behavior a boot-time central with
// no override section gets.
func registerStandardJobsFor(u *central.Unit, cfg *config.Config, logger *slog.Logger) {
	jobs := central.StandardJobs{}
	// Apply per-central overrides from the configuration. Zero means
	// "use the compiled-in default".
	for i := range cfg.Centrals {
		cc := &cfg.Centrals[i]
		if cc.Name != u.Name() {
			continue
		}
		// Not "> 0": a negative interval is the documented way to switch
		// the check_connection poll off (the job builder skips it), and
		// dropping it here left that setting silently without effect.
		if cc.CheckConnectionInterval != 0 {
			jobs.CheckConnectionInterval = cc.CheckConnectionInterval
		}
		if cc.Behavior.SysvarScanInterval > 0 {
			jobs.SysvarRefreshInterval = cc.Behavior.SysvarScanInterval
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
		// HubMetricsRefresh is deliberately NOT wired: no wiring installs
		// the coordinator's inner Metrics hook (WireHub sets every other
		// hook), so the job would fire every interval as a permanent
		// no-op. The two hub metrics that exist are produced elsewhere —
		// MetricSystemHealth by central.reconcile and
		// MetricLastEventAgeSecs by hub.last_event_age_refresh. Wire the
		// slot once a real CCU metrics fetch exists.
		jobs.HubConnectivityRefresh = u.Hub.RefreshConnectivity
		// Per-BidCos-interface duty-cycle / carrier-sense poll. Delegates
		// through the HubCoordinator; the inner listBidcosInterfaces hook is
		// wired by WireHub once the JSON-RPC session is up, nil-tolerant
		// before then.
		jobs.BidcosInterfacesRefresh = u.Hub.RefreshBidcosInterfaces
	}
	// MetricLastEventAgeSecs: seconds since the most recent CCU callback
	// across the central's interfaces. The metric and its MetricHubSensor
	// are wired, but nothing observes the value without this job.
	if u.Events != nil && u.HubModel != nil {
		jobs.LastEventAgeRefresh = func(_ context.Context) error {
			if age, ok := u.Events.NewestEventAge(time.Now()); ok {
				u.HubModel.Metrics.Observe(hub.MetricLastEventAgeSecs, age)
			}
			return nil
		}
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

// registerFirmwareJobs adds the three per-central firmware scheduler jobs.
// They cannot ride registerStandardJobs: the description fetcher resolves
// through the ValueWriter, which is built after the standard jobs are
// registered — so this runs as a second registration pass (same pattern as
// registerScheduledBackupJobs). Without these jobs the daemon reads device
// firmware data exactly once (at materialisation, possibly from the SQLite
// description cache) and the firmware overview never notices an update the
// CCU performed — the slow check re-pulls descriptions AND propagates them
// onto the live device models; the delivery/updating checks poll faster
// while an update transaction is running. Cadences mirror the reference
// scheduler (central/scheduler.py:212-220).
func registerFirmwareJobs(reg *central.Registry, valueWriter *clientpkg.ValueWriter, logger *slog.Logger) {
	if valueWriter == nil {
		return
	}
	for _, u := range reg.List() {
		registerFirmwareJobsFor(u, valueWriter, logger)
	}
}

// firmwareDeliveringStates / firmwareUpdatingStates are the lifecycle
// groups the fast-cadence jobs poll. Mirrors the reference scheduler's
// _fetch_device_firmware_update_data_in_delivery / _in_update
// (central/scheduler.py:431-468).
var (
	firmwareDeliveringStates = []hmenum.DeviceFirmwareState{
		hmenum.DeviceFirmwareStateDeliverFirmwareImage,
		hmenum.DeviceFirmwareStateLiveDeliverFirmwareImage,
	}
	firmwareUpdatingStates = []hmenum.DeviceFirmwareState{
		hmenum.DeviceFirmwareStateReadyForUpdate,
		hmenum.DeviceFirmwareStateDoUpdatePending,
		hmenum.DeviceFirmwareStatePerformingUpdate,
	}
)

// registerFirmwareJobsFor registers one central's firmware jobs. Factored
// out so the live CCU-adopt orchestrator can register them for a
// runtime-added central too.
func registerFirmwareJobsFor(u *central.Unit, valueWriter *clientpkg.ValueWriter, logger *slog.Logger) {
	if u == nil || valueWriter == nil {
		return
	}
	jobs := central.StandardJobs{
		FirmwareUpdateCheck: func(ctx context.Context) error {
			return adapter.RefreshCentralFirmwareData(ctx, u, valueWriter)
		},
		FirmwareDeliveringCheck: func(ctx context.Context) error {
			return adapter.RefreshCentralFirmwareDataByState(ctx, u, valueWriter, firmwareDeliveringStates)
		},
		FirmwareUpdatingCheck: func(ctx context.Context) error {
			return adapter.RefreshCentralFirmwareDataByState(ctx, u, valueWriter, firmwareUpdatingStates)
		},
	}
	if _, err := central.RegisterFirmwareJobs(u, jobs); err != nil {
		logger.Warn("central.firmware_jobs.register_failed",
			slog.String("central", u.Name()),
			slog.String("err", err.Error()))
	}
}

// registerScheduledBackupJobs adds the per-central automatic-backup job to
// each unit's scheduler. Off unless cfg.Backup.Schedule > 0. Called AFTER the
// storage-wired backupAdapter exists (post StartAll); the scheduler launches a
// post-Start, non-RunOnStart job on its interval, so the first backup fires
// one interval in — never at boot. Each central backs up its own CCU and (when
// KeepLast > 0) prunes its own oldest backups.
func registerScheduledBackupJobs(reg *central.Registry, cfg *config.Config, backupAdapter *adapter.BackupAdapter, logger *slog.Logger) {
	if cfg == nil || cfg.Backup.Schedule <= 0 || backupAdapter == nil {
		return
	}
	for _, u := range reg.List() {
		if u == nil {
			continue
		}
		name := u.Name()
		keepLast := cfg.Backup.KeepLast
		run := func(ctx context.Context) error {
			// Await create completion (synchronous) so the new backup is
			// durably saved BEFORE pruning. The old detached trigger returned
			// immediately, so Prune ran against the pre-create fleet and left
			// KeepLast+1 in steady state.
			if _, err := backupAdapter.CreateBackupForCentral(ctx, name); err != nil {
				return err
			}
			if keepLast > 0 {
				return backupAdapter.Prune(ctx, name, keepLast)
			}
			return nil
		}
		if err := u.Scheduler.Add(scheduler.Job{
			Name:     "central.scheduled_backup",
			Interval: cfg.Backup.Schedule,
			Run:      run,
		}); err != nil {
			logger.Warn("scheduled_backup.register.failed",
				slog.String("central", name), slog.String("err", err.Error()))
		}
	}
}
