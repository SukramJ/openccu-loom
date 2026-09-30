// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"context"
	"log/slog"
	"strings"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/client/backends"
	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// wireLoadAndRefresh installs the central.refresh_client_data handler, run
// per interface:
//
//  1. Paramset self-heal: every channel whose description declares MASTER or
//     VALUES but whose paramset registry holds neither gets its paramset
//     descriptions re-pulled. The post-callback reload after updateDevice /
//     readdedDevice / replaceDevice is single-shot; a failed or timed-out
//     reload would otherwise leave the channel untyped until restart.
//  2. The bulk value reseed (the push-event-first reconciliation safety net),
//     after the heal so the seed sees the fresh descriptions.
//
// ops resolves an interface's southbound backend; an interface without one
// (not wired yet, or a nil ops) skips the heal but is still reseeded. Wired
// once the hub session offers a value seeder, as part of the gated
// southbound bring-up.
func wireLoadAndRefresh(
	unit *central.Unit,
	pipeline *DevicePipeline,
	ifaces []config.InterfaceSpec,
	seeder ValueSeeder,
	ops func(iface hmenum.Interface) (backends.Operations, bool),
	logger *slog.Logger,
) {
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
			iface := hmenum.Interface(id)
			if ops != nil {
				if b, ok := ops(iface); ok && b != nil {
					if err := healMissingParamsets(ctx, unit, iface, b, logger); err != nil && firstErr == nil {
						firstErr = err
					}
				}
			}
			if err := pipeline.Reseed(ctx, iface, seeder, SeedCheap, logger); err != nil && firstErr == nil {
				firstErr = err
			}
		}
		return firstErr
	})
}

// healMissingParamsets re-pulls the paramset descriptions of every channel of
// iface that [coordinators.DeviceCoordinator.IdentifyDevicesMissingParamsets]
// reports. A failing channel is logged and does not stop the others; the
// first failure is returned.
func healMissingParamsets(
	ctx context.Context,
	unit *central.Unit,
	iface hmenum.Interface,
	b backends.Operations,
	logger *slog.Logger,
) error {
	if unit.Devices == nil {
		return nil
	}
	// The paramset + description registries are keyed by the canonical wire
	// id, not the bare interface name.
	wireID := hmtypes.ParseWireInterfaceID(WireInterfaceID(unit.Name(), iface))
	missing := unit.Devices.IdentifyDevicesMissingParamsets(wireID)
	if len(missing) == 0 {
		return nil
	}
	var firstErr error
	healed := 0
	for _, channelAddr := range missing {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := unit.Devices.ReloadChannelConfig(ctx, b, wireID, channelAddr, deviceModelOf(unit, wireID, channelAddr)); err != nil {
			if logger != nil {
				logger.Warn("refresh.paramsets.heal_failed",
					slog.String("central", unit.Name()),
					slog.String("interface", string(iface)),
					slog.String("channel", channelAddr),
					slog.String("err", err.Error()))
			}
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		healed++
	}
	if logger != nil {
		logger.Info("refresh.paramsets.healed",
			slog.String("central", unit.Name()),
			slog.String("interface", string(iface)),
			slog.Int("missing", len(missing)),
			slog.Int("healed", healed))
	}
	return firstErr
}

// deviceModelOf returns the device TYPE of channelAddr's parent description,
// the key the paramset patches match on; "" when the parent is not described.
func deviceModelOf(unit *central.Unit, wireID hmtypes.WireInterfaceID, channelAddr string) string {
	if unit.DescRegistry == nil {
		return ""
	}
	parent := hmtypes.DeviceAddress(channelAddr)
	if ch, ok := unit.DescRegistry.Get(wireID, channelAddr); ok && ch.Parent != "" {
		parent = ch.Parent
	}
	if root, ok := unit.DescRegistry.Get(wireID, parent); ok {
		return root.Type
	}
	return ""
}
