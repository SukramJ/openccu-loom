// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"context"
	"errors"
	"fmt"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/coordinators"
	"github.com/SukramJ/openccu-loom/internal/client"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
)

// FirmwareDomain is the live implementation of the WS `firmware.refresh`
// contract (ws.FirmwareRefresher). It re-pulls device descriptions — and with
// them the firmware-version fields — from the CCU across every central and
// interface. Mirrors the Python `ws_refresh_firmware_data` command.
type FirmwareDomain struct {
	registry *central.Registry
	writer   *client.ValueWriter
}

// NewFirmwareDomain wires the live adapter from the registry + value writer
// (the latter resolves the per-interface backend that lists devices).
func NewFirmwareDomain(r *central.Registry, w *client.ValueWriter) *FirmwareDomain {
	return &FirmwareDomain{registry: r, writer: w}
}

// RefreshFirmwareData refreshes firmware data for every configured central.
// Per-central errors are aggregated (first one returned) but do not abort the
// sweep, so one unreachable CCU does not block the others. The coordinator
// itself already logs+skips per-interface failures.
func (d *FirmwareDomain) RefreshFirmwareData(ctx context.Context) error {
	if d.registry == nil || d.writer == nil {
		return errors.New("firmware-domain: not wired")
	}
	var firstErr error
	for _, u := range d.registry.List() {
		fetcher := &writerDescFetcher{writer: d.writer, central: u.Name()}
		if err := u.Devices.RefreshFirmwareData(ctx, fetcher); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("firmware-domain: %s: %w", u.Name(), err)
		}
	}
	return firstErr
}

// writerDescFetcher is a multi-interface [coordinators.DeviceDescriptionFetcher]
// for a single central: it resolves the per-interface backend from the value
// writer on each call (unlike backendDescFetcher, which is bound to one
// interface). Needed because RefreshFirmwareData sweeps every interface of a
// central with one fetcher.
type writerDescFetcher struct {
	writer  *client.ValueWriter
	central string
}

var _ coordinators.DeviceDescriptionFetcher = (*writerDescFetcher)(nil)

// ListDevices satisfies [coordinators.DeviceDescriptionFetcher].
func (f *writerDescFetcher) ListDevices(ctx context.Context, iface hmenum.Interface) ([]hmproto.DeviceDescription, error) {
	backend, ok := f.writer.Backend(f.central, string(iface))
	if !ok {
		return nil, fmt.Errorf("firmware-domain: no backend for %s/%s", f.central, iface)
	}
	return backend.ListDevices(ctx)
}
