// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package main

import (
	gosql "database/sql"
	"log/slog"

	"github.com/SukramJ/openccu-loom/internal/alarm"
	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/internal/health"
)

// wireAlarmService constructs the alarm service on the shared daemon
// database. Returns nil when the persistence tier is unavailable —
// the alarm engine without its stores would violate every restore
// guarantee, so it stays off rather than degrading silently (the
// condition is logged and the health tracker records it).
func wireAlarmService(cfg *config.Config, reg *central.Registry, db *gosql.DB, tracker *health.Tracker, logger *slog.Logger) *alarm.Service {
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
// central-added hook so runtime-adopted centrals feed the engine like
// boot-time ones.
func alarmCentralHook(svc *alarm.Service) func(u *central.Unit) (unwire func()) {
	if svc == nil {
		return nil
	}
	return func(u *central.Unit) (unwire func()) {
		name := u.Name()
		svc.AttachCentral(name)
		return func() { svc.DetachCentral(name) }
	}
}
