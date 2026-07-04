// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package main

import (
	"context"
	"database/sql"
	"log/slog"
	"path/filepath"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central/adapter"
	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/internal/store/sqlite"
)

// wireDescriptorStores opens the SQLite DB shared with the other
// runtime stores and returns the persistent device- and
// paramset-description caches plus the handle to close at teardown.
// Returns the zero DescriptorStores when the DB cannot be opened —
// the registries then run in-memory only (the legacy behaviour: every
// boot re-pulls all descriptions from the CCU).
func wireDescriptorStores(cfg *config.Config, logger *slog.Logger) (adapter.DescriptorStores, *sql.DB) {
	if cfg == nil {
		return adapter.DescriptorStores{}, nil
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
		logger.Warn("descriptor.store_open_failed",
			slog.String("dsn", dsn),
			slog.String("err", err.Error()))
		return adapter.DescriptorStores{}, nil
	}
	return adapter.DescriptorStores{
		Devices:   sqlite.NewDeviceStore(db),
		Paramsets: sqlite.NewParamsetStore(db),
	}, db
}
