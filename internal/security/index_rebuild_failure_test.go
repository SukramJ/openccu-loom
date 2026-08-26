// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package security

import (
	"context"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/internal/clock"
	"github.com/SukramJ/openccu-loom/internal/i18n"
	sqlitestore "github.com/SukramJ/openccu-loom/internal/store/sqlite"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
)

// TestRebuildIndexFailureReportsDegradedNotAllClear pins the security-index
// fix: when RebuildIndex's store reads fail (here: the database is closed
// under it, standing in for lock contention / disk-full / a WAL stall) the
// domain must surface a degraded / unknown state, not collapse into a
// coherent "all clear" (severity OK, IndexHealthy true) that would report
// smoke / water / door / duress monitoring as fine while it knows nothing.
func TestRebuildIndexFailureReportsDegradedNotAllClear(t *testing.T) {
	ctx := context.Background()
	db, err := sqlitestore.Open(ctx, sqlitestore.FileDSN(filepath.Join(t.TempDir(), "test.db")))
	if err != nil {
		t.Fatalf("sqlitestore.Open: %v", err)
	}
	cats, err := i18n.NewCatalogs()
	if err != nil {
		t.Fatalf("i18n.NewCatalogs: %v", err)
	}
	svc, err := New(Deps{
		Registry: central.NewRegistry(),
		Stores: &Stores{
			Faults:  sqlitestore.NewSecurityFaultStore(db),
			Sources: sqlitestore.NewSecuritySourceStore(db),
			Sensors: sqlitestore.NewAlarmSensorStore(db),
			Zones:   sqlitestore.NewAlarmZoneStore(db),
		},
		AlarmBus: events.NewBus(),
		Clock:    clock.NewFake(time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)),
		Logger:   slog.New(slog.DiscardHandler),
		Catalogs: cats,
	})
	if err != nil {
		t.Fatalf("security.New: %v", err)
	}

	// Baseline: a fresh domain is healthy and all-clear.
	if base := svc.Snapshot(); base.Severity != hmenum.SecuritySeverityOK || !base.IndexHealthy {
		t.Fatalf("baseline snapshot severity=%v indexHealthy=%v; want OK/true", base.Severity, base.IndexHealthy)
	}

	var states []hmevent.SecurityStateChangedEvent
	unsub := events.Subscribe(svc.Bus(), func(e hmevent.SecurityStateChangedEvent) {
		states = append(states, e)
	})
	defer unsub()

	// Close the database so every store read in RebuildIndex errors.
	_ = db.Close()

	if err := svc.RebuildIndex(ctx); err == nil {
		t.Fatal("RebuildIndex must return an error when its store reads fail")
	}

	snap := svc.Snapshot()
	if snap.IndexHealthy {
		t.Error("IndexHealthy must be false after a failed rebuild")
	}
	if snap.Severity == hmenum.SecuritySeverityOK {
		t.Errorf("severity must not be all-clear after a failed rebuild, got %v", snap.Severity)
	}
	if len(states) == 0 {
		t.Error("a degraded state must be published so the retained north-bound planes drop the false all-clear")
	}
}
