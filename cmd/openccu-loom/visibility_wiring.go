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
	dsn := "file:" + filepath.Join(dataDir, "openccu-loom.db") + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(2000)"
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

// applyVisibilityUnIgnore reads the per-central un_ignore list from
// SQLite (seeding it from config.yaml on first start), then writes the
// union into the shared visibility.Registry. Called once after the
// device pipeline materialised every central so the suppression marks
// land on the freshly-built devices.
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
	//    registry.
	var union []string
	seen := make(map[string]struct{})
	appliedCount := 0
	for _, name := range reg.Names() {
		patterns, err := store.Patterns(ctx, name)
		if err != nil {
			logger.Warn("visibility.unignore.read_failed",
				slog.String("central", name),
				slog.String("err", err.Error()))
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
	if len(union) == 0 {
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
	for _, c := range reg.List() {
		if c == nil || c.ModelRegistry == nil {
			continue
		}
		for _, d := range c.ModelRegistry.List() {
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
