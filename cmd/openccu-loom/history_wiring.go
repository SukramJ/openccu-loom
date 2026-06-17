// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package main

import (
	"context"
	"log/slog"
	"path/filepath"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/internal/health"
	"github.com/SukramJ/openccu-loom/internal/history"
	"github.com/SukramJ/openccu-loom/internal/store/sqlite"
)

// wireHistoryStore opens the dedicated measurement-history database when
// the feature is enabled. Returns nil when history is off (the default)
// or when the DB cannot be opened — both make the recorder wiring a
// no-op. The history DB lives in its own file so the append-heavy
// recorder never contends with the config/session store. See ADR 0040.
func wireHistoryStore(cfg *config.Config, logger *slog.Logger) *sqlite.MeasurementStore {
	if cfg == nil || !cfg.Persistence.History.HistoryFeatureEnabled() {
		return nil
	}
	dataDir := cfg.DataDir
	if dataDir == "" {
		dataDir = "./var"
	}
	dsn := "file:" + filepath.Join(dataDir, "history.db") + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(2000)"
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	db, err := sqlite.OpenHistory(ctx, dsn)
	if err != nil {
		logger.Warn("history.store_open_failed",
			slog.String("dsn", dsn),
			slog.String("err", err.Error()))
		return nil
	}
	logger.Info("history.enabled", slog.String("db", dsn))
	return sqlite.NewMeasurementStore(db)
}

// wireHistoryRecorder builds the measurement recorder from config and
// subscribes it to every enabled central. Returns a teardown that stops
// the recorder (with a final flush). No-op when the store is nil
// (feature off) so callers can wire unconditionally.
func wireHistoryRecorder(
	cfg *config.Config,
	reg *central.Registry,
	store *sqlite.MeasurementStore,
	healthTracker *health.Tracker,
	logger *slog.Logger,
) func() {
	if store == nil || cfg == nil || reg == nil {
		return func() {}
	}
	hc := cfg.Persistence.History
	rec := history.New(store, history.Options{
		EnabledFor:    hc.HistoryEnabled,
		Include:       hc.Include,
		Exclude:       hc.Exclude,
		FlushInterval: hc.FlushInterval,
		Retention:     hc.Retention,
		Logger:        logger,
	})
	stop := rec.Wire(reg)

	if healthTracker != nil {
		healthTracker.RegisterGauge("history.recorded",
			func() float64 { return float64(rec.Metrics().Recorded) })
		healthTracker.RegisterGauge("history.dropped",
			func() float64 { return float64(rec.Metrics().Dropped) })
		healthTracker.RegisterGauge("history.rows_written",
			func() float64 { return float64(store.MetricsSnapshot().RowsWritten) })
		healthTracker.RegisterGauge("history.retention_deleted",
			func() float64 { return float64(store.MetricsSnapshot().RetentionDeleted) })
	}
	return stop
}
