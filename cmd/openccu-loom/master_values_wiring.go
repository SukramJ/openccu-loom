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

// wireMasterValuesStore opens the SQLite DB shared with the other
// runtime stores and returns the master-values cache. Returns nil when
// the DB cannot be opened — the device pipeline then degrades to a
// cache-less mode where every channel hits the CCU at hydration time
// (the legacy behaviour, with the duty-cycle burst on cold boot).
func wireMasterValuesStore(cfg *config.Config, logger *slog.Logger) *sqlite.MasterValuesStore {
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
		logger.Warn("master_values.store_open_failed",
			slog.String("dsn", dsn),
			slog.String("err", err.Error()))
		return nil
	}
	return sqlite.NewMasterValuesStore(db)
}
