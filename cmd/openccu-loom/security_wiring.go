// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package main

import (
	gosql "database/sql"
	"log/slog"

	"github.com/SukramJ/openccu-loom/internal/alarm"
	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/internal/i18n"
	"github.com/SukramJ/openccu-loom/internal/security"
	sqlitestore "github.com/SukramJ/openccu-loom/internal/store/sqlite"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// wireSecurityService constructs the Security & Safety domain.
//
// It takes the alarm service only for its event bus, and tolerates a
// nil one: the domain reports hazards and faults with or without an
// alarm engine, and an installation with smoke and water detectors but
// no burglar alarm is exactly the case the domain exists for.
//
// Returns nil when the persistence tier is unavailable. The fault
// ledger is the reason: `since` has to survive a restart, and an
// in-memory fallback would report every long-standing fault as new on
// every boot.
func wireSecurityService(cfg *config.Config, reg *central.Registry, db *gosql.DB,
	alarmSvc *alarm.Service, catalogs *i18n.Catalogs, logger *slog.Logger,
) *security.Service {
	if db == nil {
		logger.Warn("security service unavailable: persistence tier missing")
		return nil
	}
	var bus *events.Bus
	if alarmSvc != nil {
		bus = alarmSvc.Bus()
	}
	svc, err := security.New(security.Deps{
		Settings: security.Settings{
			Locale:           cfg.Locale,
			PublicURL:        cfg.North.REST.PublicURL,
			DuressVisibility: hmenum.DuressVisibility(cfg.Alarm.DuressVisibility),
		},
		Registry: reg,
		Stores: &security.Stores{
			Faults:  sqlitestore.NewSecurityFaultStore(db),
			Sources: sqlitestore.NewSecuritySourceStore(db),
			Sensors: sqlitestore.NewAlarmSensorStore(db),
			Zones:   sqlitestore.NewAlarmZoneStore(db),
		},
		AlarmBus: bus,
		Logger:   logger,
		Catalogs: catalogs,
	})
	if err != nil {
		logger.Error("security service construction failed", slog.String("err", err.Error()))
		return nil
	}
	return svc
}

// securityCentralHook adapts the domain onto the orchestrator's
// central-added hook.
//
// The detach half is load-bearing rather than symmetric bookkeeping:
// without it a removed central leaves ghost sources in the aggregate
// that pin their class permanently active, so `smoke` would stay on for
// a CCU that is no longer configured.
//
//nolint:contextcheck // central-adoption hooks detach from the request ctx by design
func securityCentralHook(svc *security.Service) func(u *central.Unit) (unwire func()) {
	if svc == nil {
		return nil
	}
	return func(u *central.Unit) (unwire func()) {
		name := u.Name()
		svc.AttachCentral(name)
		return func() { svc.DetachCentral(name) }
	}
}
