// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package main

import (
	"bytes"
	"context"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/internal/health"
	sqlitestore "github.com/SukramJ/openccu-loom/internal/store/sqlite"
)

// TestBootSkipsAndReportsAStoredCentralWithAnUnroutableName pins the boot
// behaviour for a central row that predates the name allowlist.
//
// The name is a path segment of the callback URL the daemon announces to the
// CCU, and the callback router refuses any segment outside the allowlist. A
// row like `CCU Wohnzimmer` therefore produced a CCU that connected, loaded
// its devices, reported healthy on /health — and received not one push event,
// so every data point stayed unobserved for the life of the process. The write
// path has validated names since the allowlist landed, but a row persisted by
// an earlier version is never re-checked, and the post-overlay validation was
// a single warning nobody reads on an install that looks fine.
//
// The row is written with raw SQL on purpose: the store's own Put rejects it,
// which is exactly why the only way to hold one is to have persisted it before
// that gate existed.
func TestBootSkipsAndReportsAStoredCentralWithAnUnroutableName(t *testing.T) {
	dataDir := t.TempDir()
	ctx := context.Background()

	gooseMigrateMu.Lock()
	db, err := sqlitestore.Open(ctx, sqlitestore.FileDSN(filepath.Join(dataDir, "openccu-loom.db")))
	gooseMigrateMu.Unlock()
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	const insert = `INSERT INTO centrals (name, host, enabled) VALUES (?, ?, 1)`
	if _, err := db.ExecContext(ctx, insert, "CCU Wohnzimmer", "192.0.2.10"); err != nil {
		t.Fatalf("seed legacy row: %v", err)
	}
	if _, err := db.ExecContext(ctx, insert, "ccu-ok", "192.0.2.11"); err != nil {
		t.Fatalf("seed routable row: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close database: %v", err)
	}

	cfg := config.Default()
	cfg.DataDir = dataDir
	var logBuf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	gooseMigrateMu.Lock()
	ov, teardown := wireAuditOverlay(ctx, cfg, logger)
	gooseMigrateMu.Unlock()
	t.Cleanup(teardown)

	names := make([]string, 0, len(cfg.Centrals))
	for _, c := range cfg.Centrals {
		names = append(names, c.Name)
	}
	if len(names) != 1 || names[0] != "ccu-ok" {
		t.Errorf("assembled centrals = %v, want only [ccu-ok] — an unroutable name must not be brought up, "+
			"it would look healthy and never deliver an event", names)
	}
	if len(ov.unroutableCentrals) != 1 || ov.unroutableCentrals[0] != "CCU Wohnzimmer" {
		t.Errorf("unroutableCentrals = %v, want [CCU Wohnzimmer]", ov.unroutableCentrals)
	}
	out := logBuf.String()
	if !strings.Contains(out, "configstore.centrals.unroutable") || !strings.Contains(out, "CCU Wohnzimmer") {
		t.Errorf("the boot log does not name the skipped central:\n%s", out)
	}

	// The log alone was the old behaviour's whole signal. Assert the operator
	// also gets it on the surface they look at.
	tracker := health.NewTracker()
	recordUnroutableCentralHealth(tracker, nil, ov.unroutableCentrals)
	comp, ok := healthComponent(tracker, configCentralsHealthComponent)
	if !ok {
		t.Fatalf("/health has no %s component: %+v", configCentralsHealthComponent, tracker.Snapshot())
	}
	if comp.Status == health.StatusHealthy {
		t.Error("the health component reports healthy while a stored CCU cannot receive a single event")
	}
	if !strings.Contains(comp.LastSample.Note, "CCU Wohnzimmer") {
		t.Errorf("health note %q does not name the offending central", comp.LastSample.Note)
	}
}

// healthComponent returns the named component from the tracker snapshot.
func healthComponent(tracker *health.Tracker, name string) (health.Component, bool) {
	for _, c := range tracker.Snapshot() {
		if c.Name == name {
			return c, true
		}
	}
	return health.Component{}, false
}

// TestBootReportsEveryStoredCentralRoutableWhenNoneOffends is the negative
// half: a database whose rows all carry routable names must leave the health
// component green, so the surface stays a signal rather than a permanent
// warning.
func TestBootReportsEveryStoredCentralRoutableWhenNoneOffends(t *testing.T) {
	tracker := health.NewTracker()
	recordUnroutableCentralHealth(tracker, nil, nil)
	comp, ok := healthComponent(tracker, configCentralsHealthComponent)
	if !ok {
		t.Fatalf("/health has no %s component", configCentralsHealthComponent)
	}
	if comp.Status != health.StatusHealthy {
		t.Errorf("component not healthy with no offending row: %s", comp.LastSample.Note)
	}
}
