// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

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
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
)

// newTestService builds a Service over a real SQLite file and the real
// stores.
//
// The stores are real rather than faked on purpose: every finding this
// file guards against lived in the seam between the in-memory aggregate
// and the persisted ledger, and a fake store would reproduce whichever
// side the test author had in mind.
func newTestService(t *testing.T, mut ...func(*Deps)) (*Service, *Stores, *clock.Fake) {
	t.Helper()
	ctx := context.Background()
	db, err := sqlitestore.Open(ctx, sqlitestore.FileDSN(filepath.Join(t.TempDir(), "test.db")))
	if err != nil {
		t.Fatalf("sqlitestore.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	cats, err := i18n.NewCatalogs()
	if err != nil {
		t.Fatalf("i18n.NewCatalogs: %v", err)
	}
	stores := &Stores{
		Faults:  sqlitestore.NewSecurityFaultStore(db),
		Sources: sqlitestore.NewSecuritySourceStore(db),
		Sensors: sqlitestore.NewAlarmSensorStore(db),
		Zones:   sqlitestore.NewAlarmZoneStore(db),
	}
	clk := clock.NewFake(time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC))
	deps := Deps{
		Registry: central.NewRegistry(),
		Stores:   stores,
		AlarmBus: events.NewBus(),
		Clock:    clk,
		Logger:   slog.New(slog.DiscardHandler),
		Catalogs: cats,
	}
	for _, m := range mut {
		m(&deps)
	}
	svc, err := New(deps)
	if err != nil {
		t.Fatalf("security.New: %v", err)
	}
	return svc, stores, clk
}

// collectFaultEvents records every fault transition the domain
// publishes, so a test can assert on the wire shape a consumer sees
// rather than on internal state.
func collectFaultEvents(t *testing.T, svc *Service) *[]hmevent.SecurityFaultChangedEvent {
	t.Helper()
	var got []hmevent.SecurityFaultChangedEvent
	unsub := events.Subscribe(svc.Bus(), func(e hmevent.SecurityFaultChangedEvent) {
		got = append(got, e)
	})
	t.Cleanup(unsub)
	return &got
}
