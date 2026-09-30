// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"context"
	"log/slog"

	"github.com/SukramJ/openccu-loom/internal/central/coordinators"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// newRecoveryLoadData builds the DATA_LOADING step of one interface's recovery
// pipeline: the hub refresh first, then a full value reseed of exactly that
// interface.
//
// The reseed is what re-measures the devices after an outage. While the CCU
// was unreachable no event could arrive, so every value (and UNREACH) that
// changed in that window stays at its pre-outage state until the next event
// or the periodic reconciliation sweep. Reading the interface's values once
// the reconnect has succeeded closes that window.
//
// Both halves are best-effort, like the hub refresh itself: a failed reseed is
// logged and does not fail the stage, because a recovery that never completes
// leaves the interface's devices hidden, while a missed reseed is repaired by
// the periodic sweep. The hub step's own result is returned unchanged.
//
// A nil seeder (a system without a bulk value source) skips the reseed.
func newRecoveryLoadData(
	hubRefresh coordinators.RecoveryStep,
	pipeline *DevicePipeline,
	iface hmenum.Interface,
	seeder ValueSeeder,
	logger *slog.Logger,
) coordinators.RecoveryStep {
	if logger == nil {
		logger = slog.Default()
	}
	return func(ctx context.Context) error {
		var hubErr error
		if hubRefresh != nil {
			hubErr = hubRefresh(ctx)
		}
		if seeder != nil && pipeline != nil {
			if err := pipeline.Reseed(ctx, iface, seeder, SeedFull, logger); err != nil {
				logger.Warn("recovery.reseed.best_effort_failed",
					slog.String("interface", string(iface)),
					slog.String("err", err.Error()))
			}
		}
		return hubErr
	}
}
