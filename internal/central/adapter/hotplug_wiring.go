// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"context"
	"log/slog"
	"strings"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/client/backends"
	"github.com/SukramJ/openccu-loom/internal/client/rega"
	"github.com/SukramJ/openccu-loom/internal/store/devicedetails"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
)

// newHotplugIngestor builds the materialiser a central hands freshly
// announced devices to — both the newDevices callback and the accept of
// a deferred device (see [central.Unit.SetDeviceIngestFn]). It runs the same pipeline
// sequence as the interface bring-up, scoped to the devices the model
// does not know yet, and then applies the per-device post-ingest wiring
// the bring-up performs outside the pipeline (value loader, hub links,
// availability/event seeds).
//
// resolveBackend maps the canonical wire interface-id to its southbound
// backend; nil means the interface is not (yet) wired and the ingest is
// skipped — the interface's own bring-up materialises those devices.
// ddLoader force-refreshes the DeviceDetails cache before the ingest so
// a hot-plugged device carries its CCU-assigned name instead of its
// address; nil skips the refresh (the periodic loader catches up later).
func newHotplugIngestor(
	unit *central.Unit,
	pipeline *DevicePipeline,
	writer ValueWriter,
	runner *rega.Runner,
	resolveBackend func(interfaceID string) backends.Operations,
	ddLoader *devicedetails.Loader,
	logger *slog.Logger,
) func(ctx context.Context, interfaceID string, descriptions []hmproto.DeviceDescription) error {
	return func(ctx context.Context, interfaceID string, descriptions []hmproto.DeviceDescription) error {
		b := resolveBackend(interfaceID)
		if b == nil {
			if logger != nil {
				logger.Debug("hotplug.skip.no_backend",
					slog.String("interface", interfaceID))
			}
			return nil
		}
		iface := BareInterfaceFromWireID(unit.Name(), interfaceID)
		if ddLoader != nil {
			// Best-effort: without the refresh the device renders by
			// address until the periodic DeviceDetails job lands.
			if err := ddLoader.Load(ctx, true); err != nil && logger != nil {
				logger.Debug("hotplug.device_details.refresh_failed",
					slog.String("interface", interfaceID),
					slog.String("err", err.Error()))
			}
		}
		newAddrs, err := pipeline.IngestNewDevices(ctx, interfaceID, iface, b, writer, runner, descriptions, logger)
		if err != nil {
			return err
		}
		if len(newAddrs) == 0 {
			return nil
		}
		// On-demand value loader per new device — the bring-up wires this
		// on every device of the interface after its ingest.
		for _, addr := range newAddrs {
			if d, ok := unit.ModelRegistry.Get(addr); ok && d != nil {
				d.SetValueLoader(b)
			}
		}
		// Hub-DP → device links are resolved idempotently; the two seed
		// passes skip every already-observed data point, so only the new
		// devices generate CCU reads.
		assignHubChannels(unit, logger)
		seedRelevantInitParameters(ctx, unit, iface, logger)
		seedReadableEvents(ctx, unit, iface, logger)
		if logger != nil {
			logger.Info("hotplug.ingest.ok",
				slog.String("interface", interfaceID),
				slog.Int("devices", len(newAddrs)),
				slog.String("addresses", strings.Join(newAddrs, ",")))
		}
		return nil
	}
}
