// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"context"
	"log/slog"
	"strings"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/client/rega"
	"github.com/SukramJ/openccu-loom/internal/config"
)

// wireLoadAndRefresh installs the central.refresh_client_data handler: a
// per-interface fetch-all-device-data sweep (the push-event-first
// reconciliation safety net). Wired once a Rega runner exists, as part of the
// gated southbound bring-up.
func wireLoadAndRefresh(unit *central.Unit, pipeline *DevicePipeline, ifaces []config.InterfaceSpec, runner *rega.Runner, logger *slog.Logger) {
	if unit == nil || pipeline == nil || runner == nil {
		return
	}
	unit.SetLoadAndRefreshFn(func(ctx context.Context) error {
		var firstErr error
		for _, ifaceSpec := range ifaces {
			id := strings.TrimSpace(ifaceSpec.Name)
			if id == "" {
				continue
			}
			if err := pipeline.seedValues(ctx, id, runner, logger); err != nil && firstErr == nil {
				firstErr = err
			}
		}
		return firstErr
	})
}
