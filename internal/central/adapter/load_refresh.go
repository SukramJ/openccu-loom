// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"context"
	"log/slog"
	"strings"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// wireLoadAndRefresh installs the central.refresh_client_data handler: a
// per-interface bulk value sweep (the push-event-first reconciliation safety
// net). Wired once the hub session offers a value seeder, as part of the
// gated southbound bring-up.
func wireLoadAndRefresh(unit *central.Unit, pipeline *DevicePipeline, ifaces []config.InterfaceSpec, seeder ValueSeeder, logger *slog.Logger) {
	if unit == nil || pipeline == nil || seeder == nil {
		return
	}
	unit.SetLoadAndRefreshFn(func(ctx context.Context) error {
		var firstErr error
		for _, ifaceSpec := range ifaces {
			id := strings.TrimSpace(ifaceSpec.Name)
			if id == "" {
				continue
			}
			if err := pipeline.Reseed(ctx, hmenum.Interface(id), seeder, SeedCheap, logger); err != nil && firstErr == nil {
				firstErr = err
			}
		}
		return firstErr
	})
}
