// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package main

import (
	gosql "database/sql"
	"log/slog"

	"github.com/SukramJ/openccu-loom/internal/alarm"
	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/internal/health"
	"github.com/SukramJ/openccu-loom/internal/i18n"
)

// wireAlarmService constructs the alarm service on the shared daemon
// database. Returns nil when the persistence tier is unavailable —
// the alarm engine without its stores would violate every restore
// guarantee, so it stays off rather than degrading silently (the
// condition is logged and the health tracker records it).
func wireAlarmService(cfg *config.Config, reg *central.Registry, db *gosql.DB, tracker *health.Tracker, catalogs *i18n.Catalogs, logger *slog.Logger) *alarm.Service {
	if db == nil {
		logger.Warn("alarm service unavailable: persistence tier missing")
		if tracker != nil {
			tracker.Record("alarm", health.Sample{Healthy: false, Note: "persistence-unavailable"})
		}
		return nil
	}
	var healthFn func(bool, string)
	if tracker != nil {
		healthFn = func(healthy bool, note string) {
			tracker.Record("alarm", health.Sample{Healthy: healthy, Note: note})
		}
	}
	svc, err := alarm.NewService(alarm.Deps{
		Settings: alarm.Settings{
			Enabled:                       cfg.Alarm.AlarmEnabled(),
			DefaultSirenSeconds:           cfg.Alarm.DefaultSirenSeconds,
			MaxAcousticPerIncidentSeconds: cfg.Alarm.MaxAcousticPerIncidentSeconds,
			StopVerifySeconds:             cfg.Alarm.StopVerifySeconds,
			JournalRetentionDays:          cfg.Alarm.JournalRetentionDays,
			RestartLoopBreaker:            cfg.Alarm.RestartLoopBreaker,
			MasterPanelName:               masterPanelName(catalogs, cfg.Locale),
		},
		Registry: reg,
		Stores:   alarm.NewStores(db),
		Logger:   logger,
		Health:   healthFn,
	})
	if err != nil {
		logger.Error("alarm service construction failed", slog.String("err", err.Error()))
		if tracker != nil {
			tracker.Record("alarm", health.Sample{Healthy: false, Note: "construction-failed"})
		}
		return nil
	}
	return svc
}

// alarmCentralHook adapts the alarm service onto the orchestrator's
// per-central hook pair so runtime-adopted centrals feed the engine like
// boot-time ones. The attach fires from the adoption flow and the
// service's reconcile pass runs on its own lifetime — deliberately
// detached from the adopting request's context.
//
// The detach half is name-keyed and stands on its own so a central this
// orchestrator never adopted can be torn down without an attach in front of
// it: AttachCentral reconciles every zone of every central, which on a
// removal would adopt or stop sirens belonging to the CCUs that stay.
//
//nolint:contextcheck // central-adoption hooks detach from the request ctx by design (see Service lifecycle)
func alarmCentralHook(svc *alarm.Service) perCentralHook {
	if svc == nil {
		return perCentralHook{}
	}
	return perCentralHook{
		attach: func(u *central.Unit) (unwire func()) {
			name := u.Name()
			svc.AttachCentral(name)
			return func() { svc.DetachCentral(name) }
		},
		detach: svc.DetachCentral,
	}
}

// masterPanelName resolves the aggregate panel's localized display
// name once at wiring time so every surface renders the same string.
//
// A missing catalogue row is treated as no name at all, because
// [i18n.Catalogs.T] answers a miss with the key itself. Returning that
// would publish the raw key "discovery.alarm_system" on REST and WS while
// MQTT published "Alarm system" — the publisher already tests for the
// echo (AlarmMQTTPublisher.localized), and this is the same test on the
// other side. An empty result falls through to the core's own fallback.
func masterPanelName(catalogs *i18n.Catalogs, locale string) string {
	if catalogs == nil {
		return ""
	}
	name := catalogs.T(locale, "discovery.alarm_system")
	if name == "discovery.alarm_system" {
		return ""
	}
	return name
}
