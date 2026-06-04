// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package main

import (
	"io"
	"log/slog"

	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/pkg/hmlog"
)

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
