// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package main

import (
	"context"
	"database/sql"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/internal/health"
	"github.com/SukramJ/openccu-loom/internal/north/measurements"
	"github.com/SukramJ/openccu-loom/internal/store/sqlite"
)

// legacyHistoryDBPath is where a daemon that still stored measurements kept
// them. This daemon never opens the file for writing.
func legacyHistoryDBPath(cfg *config.Config) string {
	dataDir := cfg.DataDir
	if dataDir == "" {
		dataDir = "./var"
	}
	return filepath.Join(dataDir, "history.db")
}

// wireRecordingOverrides builds the per-data-point recording overlay and
// its store on the main database, and loads the sparse override set so the
// forwarder's hot path never touches disk. Returns (nil, nil) without a
// database.
//
// Both exist whether or not forwarding is switched on: the rows outlive the
// switch, and a device or central removal has to purge them either way.
//
// On the way it carries over the overrides of a history database a previous
// version left behind, once, and says in the log that the file itself is no
// longer used: the daemon leaves it where it is, and an operator who does
// not know it is dead would keep backing it up.
func wireRecordingOverrides(
	ctx context.Context, cfg *config.Config, db *sql.DB, logger *slog.Logger,
) (*sqlite.RecordingOverrideStore, *measurements.RecordingOverrides) {
	if db == nil || cfg == nil {
		return nil, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	historyPath := legacyHistoryDBPath(cfg)
	if n, err := sqlite.TakeOverRecordingOverrides(ctx, db, historyPath); err != nil {
		logger.Warn("measurements.recording_overrides_takeover_failed",
			slog.String("db", historyPath), slog.String("err", err.Error()))
	} else if n > 0 {
		logger.Info("measurements.recording_overrides_taken_over",
			slog.String("db", historyPath), slog.Int64("rows", n))
	}
	if _, err := os.Stat(historyPath); err == nil {
		logger.Info("measurements.history_db_unused",
			slog.String("db", historyPath),
			slog.String("hint", "this daemon stores no measurements; the file is not read or written any more and can be deleted"))
	}

	store := sqlite.NewRecordingOverrideStore(db)
	mc := cfg.North.Measurements
	overlay := measurements.NewRecordingOverrides(store, mc.Include, mc.Exclude)
	if err := overlay.Load(ctx); err != nil {
		logger.Warn("measurements.recording_overrides_load_failed", slog.String("err", err.Error()))
	}
	return store, overlay
}

// wireMeasurementForwarder builds the measurement forwarder from config and
// subscribes it to every enabled central — boot-time and runtime-adopted
// alike, through the registry observer. Returns a teardown that stops the
// forwarder and flushes the exporter's tail. No-op while forwarding is off
// or has no usable target, so callers can wire unconditionally.
func wireMeasurementForwarder(
	cfg *config.Config,
	reg *central.Registry,
	overrides *measurements.RecordingOverrides,
	healthTracker *health.Tracker,
	logger *slog.Logger,
) (teardown func()) {
	if cfg == nil || reg == nil {
		return func() {}
	}
	mc := cfg.North.Measurements
	exporter := buildMeasurementExporter(mc, logger)
	if exporter == nil {
		return func() {}
	}
	fwd := measurements.New(exporter, measurements.Options{
		EnabledFor: mc.EnabledFor,
		Include:    mc.Include,
		Exclude:    mc.Exclude,
		Overrides:  overrides,
		Logger:     logger,
	})
	stop := fwd.Wire(reg)
	if healthTracker != nil {
		healthTracker.RegisterGauge("measurements.forwarded",
			func() float64 { return float64(fwd.Metrics().Forwarded) })
		if infl, ok := exporter.(*measurements.InfluxExporter); ok {
			healthTracker.RegisterGauge("measurements.exported",
				func() float64 { return float64(infl.Metrics().Exported) })
			healthTracker.RegisterGauge("measurements.export_failures",
				func() float64 { return float64(infl.Metrics().Failures) })
			healthTracker.RegisterGauge("measurements.export_dropped",
				func() float64 { return float64(infl.Metrics().Dropped) })
		}
	}
	return stop
}

// buildMeasurementExporter constructs the forwarding target from config, or
// returns nil while forwarding is off or the target is unusable. The write
// token is read from the named environment variable (ADR 0027) — never from
// config.
//
// Config validation already refuses an enabled block without an endpoint or
// with an unknown kind; the checks here cover the one thing it cannot see,
// the token in the environment.
func buildMeasurementExporter(mc config.NorthMeasurements, logger *slog.Logger) measurements.Exporter {
	if !mc.IsEnabled() {
		return nil
	}
	switch strings.ToLower(mc.Kind) {
	case "", config.MeasurementsKindInfluxDB:
		var token string
		if mc.TokenEnv != "" {
			token = os.Getenv(mc.TokenEnv)
		}
		if mc.Endpoint == "" || token == "" {
			logger.Warn("measurements.misconfigured",
				slog.String("reason", "endpoint or token missing"),
				slog.String("token_env", mc.TokenEnv))
			return nil
		}
		logger.Info("measurements.enabled",
			slog.String("kind", config.MeasurementsKindInfluxDB),
			slog.String("endpoint", mc.Endpoint),
			slog.String("bucket", mc.Bucket))
		return measurements.NewInfluxExporter(measurements.InfluxConfig{
			Endpoint:      mc.Endpoint,
			Org:           mc.Org,
			Bucket:        mc.Bucket,
			Token:         token,
			FlushInterval: mc.FlushInterval,
			Logger:        logger,
		})
	default:
		logger.Warn("measurements.unknown_kind", slog.String("kind", mc.Kind))
		return nil
	}
}
