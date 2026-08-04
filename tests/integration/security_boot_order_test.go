//go:build integration

// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package integration

import (
	"context"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/i18n"
	"github.com/SukramJ/openccu-loom/internal/security"
	sqlitestore "github.com/SukramJ/openccu-loom/internal/store/sqlite"
)

// TestSecurityDomainBootsBeforeItsCentral pins the production start
// order: the service comes up first and its data source arrives later.
//
// This is the order a real daemon uses. `northBridges.StartAll` runs
// milliseconds after the southbound bring-up is launched, and the
// bring-up returns immediately — the device model is populated by a
// goroutine that only finishes once the CCU has answered. The service
// therefore starts against a registry that has nothing in it yet.
//
// The domain built its classification index exactly once, in Start, and
// never again. In production that index was permanently empty: every
// wire event was discarded at the first lookup, no class ever became
// active, no fault ever opened, and `Start` logged success throughout.
// The whole classification half of the domain was silently inert.
//
// It stayed invisible because the existing integration harness
// registers a fully loaded central *before* Start — the exact inverse of
// production. A test that arranges its collaborators in an order the
// daemon never uses proves only that the code can work, never that it
// does.
//
// Scope, stated precisely so the next reader does not over-trust it.
// This exercises the adoption path, where the model is already loaded by
// the time AttachCentral runs, so the rebuild inside AttachCentral is
// what populates the index.
//
// KNOWN GAP: the remaining production window — AttachCentral running
// *before* the model loads, which is what
// cmd/openccu-loom/central_adopt.go actually does — is not covered here.
// Closing it needs a harness that can hand out a central whose model
// arrives after registration; the godevccu harness loads eagerly. The
// production fix for that window is the CentralSouthboundReadyEvent /
// DeviceCreatedEvent subscription in internal/security/subscribe.go, and
// it is currently unguarded. Do not read a green run of this file as
// covering it.
func TestSecurityDomainBootsBeforeItsCentral(t *testing.T) {
	h := newSPAHarness(t, securityModels)

	// Production order, step 1: an empty registry. The central exists
	// but the service has never seen it.
	reg := central.NewRegistry()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	db, err := sqlitestore.Open(ctx, sqlitestore.FileDSN(filepath.Join(t.TempDir(), "openccu-loom.db")))
	if err != nil {
		t.Fatalf("sqlitestore.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	cats, err := i18n.NewCatalogs()
	if err != nil {
		t.Fatalf("i18n.NewCatalogs: %v", err)
	}

	svc, err := security.New(security.Deps{
		Registry: reg,
		Stores: &security.Stores{
			Faults:  sqlitestore.NewSecurityFaultStore(db),
			Sources: sqlitestore.NewSecuritySourceStore(db),
			Sensors: sqlitestore.NewAlarmSensorStore(db),
			Zones:   sqlitestore.NewAlarmZoneStore(db),
		},
		Logger:   slog.New(slog.DiscardHandler),
		Catalogs: cats,
	})
	if err != nil {
		t.Fatalf("security.New: %v", err)
	}

	// Production order, step 2: start before any data source exists.
	if err := svc.Start(context.Background()); err != nil {
		t.Fatalf("security.Service.Start: %v", err)
	}
	t.Cleanup(func() { _ = svc.Stop(context.Background()) })

	if got := len(svc.Sources(context.Background())); got != 0 {
		t.Fatalf("inventory has %d sources before any central is known; the test is not "+
			"reproducing the production order", got)
	}
	if got := len(svc.Snapshot().Classes); got != 0 {
		t.Fatalf("snapshot has %d classes before any central is known", got)
	}

	// Production order, step 3: the central arrives, model and all.
	if err := reg.Register(h.central); err != nil {
		t.Fatalf("registry.Register: %v", err)
	}
	svc.AttachCentral(h.central.Name())

	// The assertion that fails without the rebuild trigger.
	sources := svc.Sources(context.Background())
	if len(sources) == 0 {
		t.Fatal("the inventory is still empty after the central was adopted: the domain " +
			"built its index once at Start and never rebuilt it, so every wire event is " +
			"discarded at the first lookup and the whole classification half is inert")
	}
	if got := len(svc.Snapshot().Classes); got == 0 {
		t.Fatal("no hazard class is known after the central was adopted")
	}
}
