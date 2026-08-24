// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package main

import (
	"context"
	gosql "database/sql"
	"log/slog"

	"github.com/SukramJ/openccu-loom/internal/alarm"
	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/internal/i18n"
	"github.com/SukramJ/openccu-loom/internal/security"
	sqlitestore "github.com/SukramJ/openccu-loom/internal/store/sqlite"
	"github.com/SukramJ/openccu-loom/internal/wiring"
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
			// The fault ledger answers the same question about the same
			// installation as the alarm journal, so it shares its window
			// rather than adding a second knob to get wrong.
			RetentionDays: cfg.Alarm.JournalRetentionDays,
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
// per-central hook pair.
//
// The detach half is load-bearing rather than symmetric bookkeeping:
// without it a removed central leaves ghost sources in the aggregate
// that pin their class permanently active, so `smoke` would stay on for
// a CCU that is no longer configured. It is reachable by name alone so a
// boot-registered central can be detached without running AttachCentral —
// whose index rebuild has no business firing while a CCU is being deleted.
//
//nolint:contextcheck // central-adoption hooks detach from the request ctx by design
func securityCentralHook(svc *security.Service) perCentralHook {
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

// wireSecurityIndexRefresh makes an alarm-config change reach the
// Security & Safety classification index.
//
// Enrollment decides which data points the domain treats as
// security-relevant and which zone owns them, so a sensor added through
// the alarm surface must re-enter the index immediately. Without this a
// newly enrolled sensor stayed outside every aggregate until the next
// daemon restart.
//
// Nil-safe on both sides: either service may be absent.
func wireSecurityIndexRefresh(m *wiring.Manifest, alarmSvc *alarm.Service, securitySvc *security.Service, logger *slog.Logger) {
	if alarmSvc == nil || securitySvc == nil {
		return
	}
	m.Attach(wiring.Seam{
		Name:         "security.index_refresh",
		Collaborator: "config-changed hook on *alarm.Service",
		Phase:        wiring.PhaseOnce,
		Why:          "the hazard-classification index is never rebuilt after any alarm-config write — enrollment, zone edit or class override alike — so a sensor the operator just re-assigned keeps its old class and opens faults on the wrong one until the daemon restarts",
	}, func() { wireSecurityIndexRefreshHook(alarmSvc, securitySvc, logger) })
}

// wireSecurityIndexRefreshHook installs the hook, so the seam above wraps
// the handover and nothing else.
func wireSecurityIndexRefreshHook(alarmSvc *alarm.Service, securitySvc *security.Service, logger *slog.Logger) {
	alarmSvc.SetConfigChangedHook(func(ctx context.Context) {
		if err := securitySvc.RebuildIndex(ctx); err != nil {
			logger.Error("security: rebuild index after alarm config change",
				slog.String("err", err.Error()))
		}
	})
}
