// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package main

import (
	"context"
	"log/slog"
	"path/filepath"
	"time"

	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/internal/store/sqlite"
)

// wireValuesCacheStore opens the shared SQLite DB and returns the
// persistent VALUES cache. Returns nil when the DB cannot be opened
// — the device pipeline then degrades to a cache-less mode where
// every cold boot starts with all DPs unobserved until the live
// fetch_all_device_data round populates them.
func wireValuesCacheStore(cfg *config.Config, logger *slog.Logger) *sqlite.ValuesCacheStore {
	if cfg == nil {
		return nil
	}
	if cfg.Persistence.ValuesCache.Enabled != nil && !*cfg.Persistence.ValuesCache.Enabled {
		logger.Info("values_cache.disabled_by_config")
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
		logger.Warn("values_cache.store_open_failed",
			slog.String("dsn", dsn),
			slog.String("err", err.Error()))
		return nil
	}
	return sqlite.NewValuesCacheStore(db)
}
