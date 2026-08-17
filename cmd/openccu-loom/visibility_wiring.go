// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package main

import (
	"context"
	"log/slog"
	"path/filepath"
	"strings"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/internal/north/rest/handlers"
	"github.com/SukramJ/openccu-loom/internal/store/sqlite"
	"github.com/SukramJ/openccu-loom/internal/store/visibility"
)

// wireVisibilityUnIgnoreStore opens the SQLite DB and returns the
// store. Returns nil when the DB cannot be opened — the REST endpoints
// then degrade to 503 service_unready.
func wireVisibilityUnIgnoreStore(cfg *config.Config, logger *slog.Logger) *sqlite.VisibilityUnIgnoreStore {
	if cfg == nil {
		return nil
	}
	dataDir := cfg.DataDir
	if dataDir == "" {
		dataDir = "./var"
	}
	dsn := sqlite.FileDSN(filepath.Join(dataDir, "openccu-loom.db"))
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	db, err := sqlite.Open(ctx, dsn)
	if err != nil {
		logger.Warn("visibility.unignore.store_open_failed",
			slog.String("dsn", dsn),
			slog.String("err", err.Error()))
		return nil
	}
	return sqlite.NewVisibilityUnIgnoreStore(db)
}

// visibilityUnIgnoreStoreFrom boxes the concrete store into the handler
// interface, keeping a TRUE nil interface when no store was opened.
//
// Assigning the nil pointer directly would produce a non-nil interface holding
// a nil pointer, so the handlers' `if store == nil { 503 }` guard never fires:
// GET would answer 200 with an empty pattern list for every central (the
// store's nil-receiver guards return no rows), and PUT would run the whole
// validate-and-persist path against a no-op Replace. A degraded database has
// to read as unavailable, not as configured-empty.
func visibilityUnIgnoreStoreFrom(s *sqlite.VisibilityUnIgnoreStore) handlers.VisibilityUnIgnoreStore {
	if s == nil {
		return nil
	}
	return s
}

// wireVisibilityUnIgnore keeps the shared visibility registry in step with the
// fleet. It registers a [central.Registry] observer, which replays over every
// central already present — the boot-time apply — and runs again for every CCU
// adopted at runtime, plus once more when one leaves.
//
// A single boot-time pass was not enough: the un_ignore union is computed over
// the centrals registered at that instant, so a CCU adopted through the SPA
// kept every parameter it had un-ignored suppressed on REST, MQTT and the SPA
// until the next daemon restart re-ran the walk — with nothing anywhere
// reporting it, which reads as "the setting needs a restart" rather than a bug.
// The un-register half matters too: a removed CCU's patterns must stop
// widening what the other centrals expose.
//
// The returned remove detaches the observer; the caller runs it at shutdown.
func wireVisibilityUnIgnore(
	ctx context.Context,
	cfg *config.Config,
	reg *central.Registry,
	store *sqlite.VisibilityUnIgnoreStore,
	visReg *visibility.Registry,
	logger *slog.Logger,
) (remove func()) {
	if reg == nil {
		return func() {}
	}
	apply := func() { applyVisibilityUnIgnore(ctx, cfg, reg, store, visReg, logger) }
	return reg.OnRegister(func(_ *central.Unit) func() {
		apply()
		return apply
	})
}

// applyVisibilityUnIgnore reads the per-central un_ignore list from
// SQLite (seeding it from config.yaml on first start), then writes the
// union into the shared visibility.Registry. Runs after the device pipeline
// materialised a central so the suppression marks land on the built devices;
// [wireVisibilityUnIgnore] is what re-runs it as the fleet changes.
//
// Returns the count of centrals whose patterns were applied (zero is
// not an error — empty lists are valid).
func applyVisibilityUnIgnore(
	ctx context.Context,
	cfg *config.Config,
	reg *central.Registry,
	store *sqlite.VisibilityUnIgnoreStore,
	visReg *visibility.Registry,
	logger *slog.Logger,
) int {
	if cfg == nil || reg == nil || store == nil || visReg == nil {
		return 0
	}

	// 1. Seed every central's row set from config.yaml when SQLite is empty.
	for i := range cfg.Centrals {
		cc := &cfg.Centrals[i]
		if len(cc.Visibility.UnIgnore) == 0 {
			continue
		}
		if err := store.SeedIfEmpty(ctx, cc.Name, cc.Visibility.UnIgnore); err != nil {
			logger.Warn("visibility.unignore.seed_failed",
				slog.String("central", cc.Name),
				slog.String("err", err.Error()))
		}
	}

	// 2. Read every central's persisted list and union into the shared
	//    registry. A read failure on any one central must not shrink the
	//    union: proceeding with the patterns that DID read successfully
	//    would replace the registry with a subset and un-apply patterns
	//    that are still persisted, on nothing worse than a transient
	//    SQLite error (SQLITE_BUSY, a cancelled context). Abort the whole
	//    apply instead and keep the registry's previous contents; the next
	//    OnRegister run (the next adopt/remove, or the periodic re-apply
	//    the caller may schedule) retries the full read.
	var union []string
	seen := make(map[string]struct{})
	appliedCount := 0
	readFailed := false
	for _, name := range reg.Names() {
		patterns, err := store.Patterns(ctx, name)
		if err != nil {
			logger.Warn("visibility.unignore.read_failed",
				slog.String("central", name),
				slog.String("err", err.Error()))
			readFailed = true
			continue
		}
		if len(patterns) == 0 {
			continue
		}
		appliedCount++
		for _, p := range patterns {
			if _, dup := seen[p]; dup {
				continue
			}
			seen[p] = struct{}{}
			union = append(union, p)
		}
	}
	if readFailed {
		logger.Warn("visibility.unignore.apply_aborted",
			slog.String("reason", "a per-central pattern read failed; keeping the registry's previous contents"))
		return appliedCount
	}
	// An empty union still has to be loaded once something was loaded before:
	// that is what withdraws the patterns of a central that left the fleet.
	// With nothing loaded either way there is no work — the common boot with
	// no un_ignore rules at all skips the full-fleet re-mark below.
	if len(union) == 0 && len(visReg.Parameter().UnIgnoreEntries()) == 0 {
		return appliedCount
	}

	if err := visReg.LoadUnIgnore(strings.NewReader(strings.Join(union, "\n"))); err != nil {
		logger.Warn("visibility.unignore.load_failed",
			slog.String("err", err.Error()),
			slog.Int("pattern_count", len(union)))
		return appliedCount
	}

	// 3. Re-run the suppression-mark pass on every device of every
	//    central. The pipeline already ran during start, but it used the
	//    empty default un-ignore set; now we replay it so the per-DP
	//    IsUnIgnored bits reflect the persisted rules.
	decider := visReg.Parameter()
	deviceCount := 0
	for _, u := range reg.List() {
		if u == nil || u.ModelRegistry == nil {
			continue
		}
		for _, d := range u.ModelRegistry.List() {
			visibility.ApplyUnIgnoredMarks(d, decider)
			deviceCount++
		}
	}
	logger.Info("visibility.unignore.applied",
		slog.Int("pattern_count", len(union)),
		slog.Int("centrals_with_patterns", appliedCount),
		slog.Int("devices_touched", deviceCount))
	return appliedCount
}
