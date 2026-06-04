// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package main

import (
	"io"
	"log/slog"
	"time"

	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/internal/diagnostics"
	"github.com/SukramJ/openccu-loom/pkg/hmlog"
)

// logDaemonStart runs the post-logging startup-capture phase and emits the
// `daemon.start` log line. If the operator enabled the startup-capture toggle
// in the SPA (writes to <data_dir>/startup_capture.json) the daemon opens a
// capture as the very first post-logging step so the bootstrap phase — XML-RPC
// init, paramset hydration, callback setup — ends up in the archive. Failure
// is logged but does not abort boot; capture is a diagnostic affordance, not a
// hard dependency.
func logDaemonStart(cfg *config.Config, captureManager *diagnostics.Manager, levels *hmlog.LevelRegistry, logger *slog.Logger) {
	if sc, err := diagnostics.LoadStartupCapture(cfg.DataDir); err != nil {
		logger.Warn("diagnostics.startup_capture.load_failed",
			slog.String("err", err.Error()))
	} else if sc.Enabled {
		opts := diagnostics.StartOptions{
			Duration:  time.Duration(sc.DurationS) * time.Second,
			Anonymise: sc.Anonymise,
			Triggered: "daemon.startup",
		}
		if summary, err := captureManager.Start(opts); err != nil {
			logger.Warn("diagnostics.startup_capture.start_failed",
				slog.String("err", err.Error()))
		} else {
			logger.Info("diagnostics.startup_capture.started",
				slog.String("id", summary.ID),
				slog.Duration("duration", summary.EndsAt.Sub(summary.StartedAt)))
			// One-shot semantics: clear the Enabled flag now that the
			// capture is running. The operator's intent ("record the
			// next boot") was satisfied; the persisted duration /
			// anonymise values stay so the next toggle re-uses them.
			// Doing this right after Start (not at capture stop) is
			// crash-safe: a daemon that dies mid-capture does not boot
			// into a second capture on the next launch.
			cleared := sc
			cleared.Enabled = false
			if err := diagnostics.SaveStartupCapture(cfg.DataDir, cleared); err != nil {
				logger.Warn("diagnostics.startup_capture.clear_failed",
					slog.String("err", err.Error()))
			}
		}
	}

	startAttrs := []any{
		slog.String("locale", cfg.Locale),
		slog.String("log_level", cfg.Logging.Level),
	}
	if overrides := levels.Snapshot(); len(overrides) > 0 {
		paths := make([]string, 0, len(overrides))
		for _, ov := range overrides {
			paths = append(paths, ov.Path+"="+hmlog.FormatLevel(ov.Level))
		}
		startAttrs = append(startAttrs, slog.Any("log_overrides", paths))
	}
	logger.Info("daemon.start", startAttrs...)
}

func newLogger(lc config.LoggingConfig, w io.Writer) *slog.Logger {
	logger, _, _ := newLoggerStack(lc, w)
	return logger
}

// newLoggerStack builds the root logger plus the [hmlog.LevelRegistry]
// that gates it. Subsystem loggers obtained via
// [hmlog.ForSubsystem] share the same handler chain and registry, so
// runtime overrides set via the diagnostics REST endpoint take effect
// across every subsystem without rebuilding loggers.
//
// The returned error surfaces only when the static `logging.overrides`
// map in the config carries an invalid level string. The root logger
// and registry are non-nil regardless.
func newLoggerStack(lc config.LoggingConfig, w io.Writer) (*slog.Logger, *hmlog.LevelRegistry, error) {
	stack, err := newFullLoggerStack(lc, w)
	if err != nil {
		return stack.Logger, stack.Levels, err
	}
	return stack.Logger, stack.Levels, nil
}

// newFullLoggerStack is the new entry point used by daemon code paths
// that also need the [hmlog.TeeHandler] for the diagnostics capture
// endpoint. The legacy [newLoggerStack] stays as a thin wrapper so
// the test surface in [daemon_io_test.go] continues to compile.
func newFullLoggerStack(lc config.LoggingConfig, w io.Writer) (hmlog.Stack, error) {
	defaultLevel, _ := hmlog.ParseLevel(lc.Level)
	opts := hmlog.StackOptions{
		Writer: w,
		Format: hmlog.ParseFormat(lc.Format),
	}
	stack := hmlog.BuildFullStack(opts, defaultLevel)
	if len(lc.Overrides) > 0 {
		if err := stack.Levels.ApplyConfig(lc.Overrides); err != nil {
			return stack, err
		}
	}
	return stack, nil
}
