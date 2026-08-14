// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package main

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
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
	dsn := sqlite.FileDSN(filepath.Join(dataDir, "history.db"))
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

// wireHistoryRetention keeps the retention purge alive while recording is
// switched off. It opens the existing history database, starts the
// rollup + retention loop with no recorder attached, and returns a closer
// that stops the loop and closes the handle. Returns a no-op closer when
// history is enabled (the recorder already runs the loop) or when no
// history database exists — a disabled feature must not create one.
//
// The store handle deliberately stays local instead of landing in
// [sharedInfra.historyStore]: that field is what advertises the /history
// REST surface and the `history` runtime capability, and an operator who
// turned recording off has not asked for either. Only the eviction runs.
func wireHistoryRetention(cfg *config.Config, logger *slog.Logger) func() {
	noop := func() {}
	if cfg == nil || cfg.Persistence.History.HistoryFeatureEnabled() {
		return noop
	}
	dataDir := cfg.DataDir
	if dataDir == "" {
		dataDir = "./var"
	}
	path := filepath.Join(dataDir, "history.db")
	if _, err := os.Stat(path); err != nil {
		return noop
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	db, err := sqlite.OpenHistory(ctx, sqlite.FileDSN(path))
	if err != nil {
		logger.Warn("history.retention_open_failed",
			slog.String("db", path),
			slog.String("err", err.Error()))
		return noop
	}
	store := sqlite.NewMeasurementStore(db)
	hc := cfg.Persistence.History
	stop := history.New(store, history.Options{
		Retention:       hc.Retention,
		RetentionHourly: hc.RetentionHourlyOrDefault(),
		RetentionDaily:  hc.RetentionDailyOrDefault(),
		Logger:          logger,
	}).StartRetention()
	logger.Info("history.retention_only", slog.String("db", path))
	return func() {
		stop()
		_ = db.Close()
	}
}

// wireRecordingOverrides builds the per-datapoint recording overlay and
// its store from the history DB handle, then loads the sparse override
// set so the recorder hot path never touches disk. Returns (nil, nil)
// when history is off. Bounds its own context.
func wireRecordingOverrides(
	historyStore *sqlite.MeasurementStore, cfg *config.Config, logger *slog.Logger,
) (*sqlite.RecordingOverrideStore, *history.RecordingOverrides) {
	if historyStore == nil || cfg == nil {
		return nil, nil
	}
	store := sqlite.NewRecordingOverrideStore(historyStore.DB())
	hc := cfg.Persistence.History
	overlay := history.NewRecordingOverrides(store, hc.Include, hc.Exclude)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := overlay.Load(ctx); err != nil {
		logger.Warn("history.recording_overrides_load_failed", slog.String("err", err.Error()))
	}
	return store, overlay
}

// wireHistoryRecorder builds the measurement recorder from config and
// subscribes it to every enabled central. Returns a teardown that stops
// the recorder (with a final flush). No-op when the store is nil
// (feature off) so callers can wire unconditionally.
//
// It also returns a per-central hook the live-adopt orchestrator installs:
// Recorder.Wire walks the registry exactly once, so a CCU adopted at runtime
// recorded no measurement history at all and its charts stayed permanently
// empty — indistinguishable from a CCU whose data points never change.
func wireHistoryRecorder(
	cfg *config.Config,
	reg *central.Registry,
	store *sqlite.MeasurementStore,
	overrides *history.RecordingOverrides,
	healthTracker *health.Tracker,
	logger *slog.Logger,
) (centralHook func(u *central.Unit) (unwire func()), teardown func()) {
	if store == nil || cfg == nil || reg == nil {
		return nil, func() {}
	}
	hc := cfg.Persistence.History
	exporter := buildHistoryExporter(hc.Export, logger)
	rec := history.New(store, history.Options{
		EnabledFor:      hc.HistoryEnabled,
		Include:         hc.Include,
		Exclude:         hc.Exclude,
		Overrides:       overrides,
		FlushInterval:   hc.FlushInterval,
		Retention:       hc.Retention,
		RetentionHourly: hc.RetentionHourlyOrDefault(),
		RetentionDaily:  hc.RetentionDailyOrDefault(),
		Exporter:        exporter,
		Logger:          logger,
	})
	stop := rec.Wire(reg)
	centralHook = func(u *central.Unit) func() { return rec.WireCentral(u) }

	if healthTracker != nil && exporter != nil {
		if infl, ok := exporter.(*history.InfluxExporter); ok {
			healthTracker.RegisterGauge("history.export_exported",
				func() float64 { return float64(infl.Metrics().Exported) })
			healthTracker.RegisterGauge("history.export_failures",
				func() float64 { return float64(infl.Metrics().Failures) })
			healthTracker.RegisterGauge("history.export_dropped",
				func() float64 { return float64(infl.Metrics().Dropped) })
		}
	}

	if healthTracker != nil {
		healthTracker.RegisterGauge("history.recorded",
			func() float64 { return float64(rec.Metrics().Recorded) })
		healthTracker.RegisterGauge("history.dropped",
			func() float64 { return float64(rec.Metrics().Dropped) })
		healthTracker.RegisterGauge("history.flush_errors",
			func() float64 { return float64(rec.Metrics().FlushErrors) })
		healthTracker.RegisterGauge("history.rows_written",
			func() float64 { return float64(store.MetricsSnapshot().RowsWritten) })
		healthTracker.RegisterGauge("history.retention_deleted",
			func() float64 { return float64(store.MetricsSnapshot().RetentionDeleted) })
	}
	return centralHook, stop
}

// buildHistoryExporter constructs the opt-in push exporter from config,
// or returns nil when export is disabled or misconfigured. The write
// token is read from the named environment variable (ADR 0027) — never
// from inline config.
func buildHistoryExporter(ec config.HistoryExportConfig, logger *slog.Logger) history.MeasurementExporter {
	if !ec.ExportEnabled() {
		return nil
	}
	switch strings.ToLower(ec.Kind) {
	case "", "influxdb":
		var token string
		if ec.TokenEnv != "" {
			token = os.Getenv(ec.TokenEnv)
		}
		if ec.Endpoint == "" || token == "" {
			logger.Warn("history.export.misconfigured",
				slog.String("reason", "endpoint or token missing"),
				slog.String("token_env", ec.TokenEnv))
			return nil
		}
		logger.Info("history.export.enabled",
			slog.String("kind", "influxdb"),
			slog.String("endpoint", ec.Endpoint),
			slog.String("bucket", ec.Bucket))
		return history.NewInfluxExporter(history.InfluxConfig{
			Endpoint: ec.Endpoint,
			Org:      ec.Org,
			Bucket:   ec.Bucket,
			Token:    token,
			Logger:   logger,
		})
	default:
		logger.Warn("history.export.unknown_kind", slog.String("kind", ec.Kind))
		return nil
	}
}
