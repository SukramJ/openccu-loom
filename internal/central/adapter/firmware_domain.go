// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"context"
	"errors"
	"fmt"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/coordinators"
	"github.com/SukramJ/openccu-loom/internal/central/registry"
	"github.com/SukramJ/openccu-loom/internal/client"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
)

// FirmwareDomain is the live implementation of the firmware-refresh
// contract behind the WS `firmware.refresh` command and the REST
// `POST /devices/firmware/refresh` endpoint. It re-pulls device
// descriptions — and with them the firmware-version fields — from the
// CCU across every central and interface, then propagates the fresh
// fields onto the live device models. Mirrors the Python
// `ws_refresh_firmware_data` command.
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
		if err := RefreshCentralFirmwareData(ctx, u, d.writer); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("firmware-domain: %s: %w", u.Name(), err)
		}
	}
	return firstErr
}

// RefreshCentralFirmwareData re-pulls the device descriptions of one central
// (all interfaces) and applies the refreshed firmware fields to the live
// device models. This is the shared refresh primitive behind the WS/REST
// force-refresh and the periodic firmware scheduler jobs — without the
// apply step a description re-pull only updates the description registry
// and every surface (REST detail, MQTT update entity, SPA firmware
// overview) keeps serving the firmware state from device-materialisation
// time. Mirrors the Python device coordinator's `refresh_firmware_data`
// (central/coordinators/device.py:705), which calls
// `device.refresh_firmware_data()` after the description re-pull.
func RefreshCentralFirmwareData(ctx context.Context, u *central.Unit, w *client.ValueWriter) error {
	if u == nil || w == nil {
		return errors.New("firmware-refresh: not wired")
	}
	fetcher := &writerDescFetcher{writer: w, central: u.Name()}
	err := u.Devices.RefreshFirmwareData(ctx, fetcher)
	applyFirmwareFromDescriptions(u)
	return err
}

// RefreshCentralFirmwareDataByState re-pulls descriptions only for
// interfaces where at least one live device currently sits in one of the
// given firmware lifecycle states, then applies the refreshed fields to
// the live models. This backs the fast-cadence delivery/updating scheduler
// jobs: they poll aggressively while an update transaction is running and
// stay no-ops otherwise. Mirrors the Python
// `refresh_firmware_data_by_state` (central/coordinators/device.py:733).
func RefreshCentralFirmwareDataByState(ctx context.Context, u *central.Unit, w *client.ValueWriter, states []hmenum.DeviceFirmwareState) error {
	if u == nil || w == nil {
		return errors.New("firmware-refresh: not wired")
	}
	if u.ModelRegistry == nil || u.DescRegistry == nil {
		return nil
	}
	fetcher := &writerDescFetcher{writer: w, central: u.Name()}
	reader := modelFirmwareStateReader{reg: u.ModelRegistry}
	var firstErr error
	for _, iface := range u.DescRegistry.GetInterfaceIDs() {
		if err := u.Devices.RefreshFirmwareDataByState(ctx, fetcher, reader, iface, states); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	applyFirmwareFromDescriptions(u)
	return firstErr
}

// modelFirmwareStateReader implements [coordinators.FirmwareStateReader]
// over the live device models, so the state gate reflects what the
// surfaces actually serve (not the description registry, which the
// refresh itself is about to overwrite).
type modelFirmwareStateReader struct {
	reg *registry.ModelRegistry
}

// DeviceFirmwareStates implements [coordinators.FirmwareStateReader].
//
// iface is the canonical wire id (`<central>-<iface>`): the only caller drives
// it from [registry.DeviceDescriptionRegistry.GetInterfaceIDs], which is keyed
// by that id. Matching against the device's bare Interface instead would make
// the returned set empty on every named central and turn the state-gated
// refresh into a silent no-op.
func (r modelFirmwareStateReader) DeviceFirmwareStates(iface hmenum.Interface) map[string]hmenum.DeviceFirmwareState {
	out := map[string]hmenum.DeviceFirmwareState{}
	for _, dev := range r.reg.List() {
		if dev == nil || dev.InterfaceID != string(iface) || dev.Firmware() == nil {
			continue
		}
		out[dev.Address] = dev.Firmware().Info().UpdateState
	}
	return out
}

// applyFirmwareFromDescriptions copies the firmware fields of the (just
// refreshed) description registry onto every live device model. Only the
// CCU-sourced fields (current / available version, update lifecycle state)
// move; the Updatable capability is bound at device materialisation (it
// gates whether the update entity exists at all) and stays untouched.
// Firmware.Set fires the registered OnChange handlers, so the MQTT update
// entity republishes automatically on an effective change.
func applyFirmwareFromDescriptions(u *central.Unit) {
	if u.ModelRegistry == nil || u.DescRegistry == nil {
		return
	}
	for _, dev := range u.ModelRegistry.List() {
		if dev == nil || dev.Firmware() == nil {
			continue
		}
		// The description registry is keyed by the canonical wire id
		// (`<central>-<iface>`) — that is what the callback handlers and the
		// hydration path Put under. The device's bare Interface is the
		// operator-facing form and misses every entry on a named central.
		dd, ok := u.DescRegistry.Get(hmenum.Interface(dev.InterfaceID), dev.Address)
		if !ok {
			continue
		}
		prev := dev.Firmware().Info()
		dev.Firmware().Set(device.FirmwareInfo{
			Current:     dd.Firmware,
			Available:   dd.AvailableFirmware,
			Updatable:   prev.Updatable,
			UpdateState: hmenum.DeviceFirmwareState(dd.FirmwareUpdateState),
		})
	}
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
