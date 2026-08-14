// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/coordinators"
	clientpkg "github.com/SukramJ/openccu-loom/internal/client"
	"github.com/SukramJ/openccu-loom/internal/client/backends"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// DeviceReloaderAdapter implements ws.DeviceReloader. It locates the
// device by address across all registered centrals, resolves its
// interface, and delegates to
// DeviceCoordinator.RefreshDeviceDescriptionsAndCreateMissingDevices
// scoped to that interface.
//
// ReloadDeviceConfig fetches only the target device and its channels via
// GetDeviceDescription, so the coordinator refresh touches exactly one
// device rather than the whole interface.
type DeviceReloaderAdapter struct {
	registry *central.Registry
	writer   *clientpkg.ValueWriter
}

// NewDeviceReloaderAdapter wires the live adapter.
func NewDeviceReloaderAdapter(r *central.Registry, w *clientpkg.ValueWriter) *DeviceReloaderAdapter {
	return &DeviceReloaderAdapter{registry: r, writer: w}
}

// ReloadDeviceConfig re-fetches the device description from the CCU for
// the interface that owns deviceAddress and recreates any missing
// channels/data-points. Returns an error when the device is unknown or
// no backend can be resolved.
func (a *DeviceReloaderAdapter) ReloadDeviceConfig(ctx context.Context, deviceAddress string) error {
	if a.registry == nil || a.writer == nil {
		return errors.New("device_reloader: adapter not fully wired")
	}
	for _, unit := range a.registry.List() {
		dev, ok := unit.ModelRegistry.Get(deviceAddress)
		if !ok {
			continue
		}
		b, ok := a.writer.Backend(unit.Name(), hmtypes.ParseWireInterfaceID(dev.InterfaceID))
		if !ok {
			return fmt.Errorf("device_reloader: no backend for %s/%s", unit.Name(), dev.InterfaceID)
		}
		// The coordinator uses iface as a registry key, and the device /
		// description / paramset registries are keyed by the canonical wire id
		// (`<central>-<iface>`) — that is what the callback path writes under.
		// Passing the bare interface writes a second, duplicate key space that
		// no other reader ever finds, and leaves RefreshDeviceLinkPeers looking
		// for descriptions that are not there.
		iface := hmtypes.ParseWireInterfaceID(dev.InterfaceID)
		fetcher := &singleDeviceDescFetcher{ops: b, address: deviceAddress}
		if err := unit.Devices.RefreshDeviceDescriptionsAndCreateMissingDevices(ctx, fetcher, iface); err != nil {
			return err
		}
		// Also re-pull link-peer addresses on demand. Folding this into the
		// (already RPC-bound) reload keeps a manually-reloaded device's link
		// data current without a boot-time per-device RPC sweep over the whole
		// fleet. RefreshDeviceLinkPeers logs+skips per-channel errors and never
		// returns one, so it cannot fail the reload.
		unit.Devices.RefreshDeviceLinkPeers(ctx, &backendLinkPeerFetcher{ops: b}, iface, deviceAddress)
		return nil
	}
	return fmt.Errorf("device_reloader: device not found: %s", deviceAddress)
}

// ReloadChannelConfig re-pulls the paramset descriptions (VALUES, MASTER,
// LINK) and current MASTER values for a single channel, then re-materialises
// the channel's data points so description changes (e.g. patched MIN/MAX)
// propagate. It locates the channel's device by address across all
// registered centrals, resolves the backend, re-pulls the channel's paramset
// config via DeviceCoordinator.ReloadChannelConfig, and recreates the
// channel's data points via the device-level refresh path.
//
// Mirrors Channel.reload_channel_config (model/device.py:1448 →
// on_config_changed), scoped to one channel.
//
// channelAddress is the "DDDDDDDDDD:n" form; the device address is derived by
// stripping the ":n" suffix. Returns an error when the channel is unknown or
// no backend can be resolved.
func (a *DeviceReloaderAdapter) ReloadChannelConfig(ctx context.Context, channelAddress string) error {
	if a.registry == nil || a.writer == nil {
		return errors.New("channel_reloader: adapter not fully wired")
	}
	if channelAddress == "" {
		return errors.New("channel_reloader: empty channel address")
	}
	deviceAddr := hmtypes.DeviceAddress(channelAddress)
	for _, unit := range a.registry.List() {
		dev, ok := unit.ModelRegistry.Get(deviceAddr)
		if !ok {
			continue
		}
		b, ok := a.writer.Backend(unit.Name(), hmtypes.ParseWireInterfaceID(dev.InterfaceID))
		if !ok {
			return fmt.Errorf("channel_reloader: no backend for %s/%s", unit.Name(), dev.InterfaceID)
		}
		// The paramset + description registries are keyed by the canonical
		// wire id, not the bare interface — see ReloadDeviceConfig.
		iface := hmtypes.ParseWireInterfaceID(dev.InterfaceID)
		// Re-pull the channel's paramset descriptions + MASTER values.
		if err := unit.Devices.ReloadChannelConfig(ctx, b, iface, channelAddress, dev.Model); err != nil {
			return err
		}
		// Re-materialise the channel's data points so the refreshed
		// descriptions take effect. The single-channel materialisation
		// path is not yet factored out of the device pipeline, so we run
		// the device-level refresh for the channel's owning device — the
		// observable result for the target channel is identical.
		fetcher := &backendDescFetcher{ops: b}
		return unit.Devices.RefreshDeviceDescriptionsAndCreateMissingDevices(ctx, fetcher, iface)
	}
	return fmt.Errorf("channel_reloader: channel not found: %s", channelAddress)
}

// backendDescFetcher wraps a [backends.Operations] as a
// coordinators.DeviceDescriptionFetcher. The backend's ListDevices call
// is already interface-scoped (each backend is wired to one interface),
// so the iface parameter is accepted but not forwarded — it is used
// only by the coordinator for registry writes.
type backendDescFetcher struct {
	ops backends.Operations
}

// ListDevices satisfies coordinators.DeviceDescriptionFetcher.
func (f *backendDescFetcher) ListDevices(ctx context.Context, _ hmtypes.WireInterfaceID) ([]hmproto.DeviceDescription, error) {
	return f.ops.ListDevices(ctx)
}

// backendLinkPeerFetcher wraps a [backends.Operations] as a
// coordinators.LinkPeerFetcher. The backend's GetLinkPeers is already
// interface-scoped (one backend per interface), so the iface parameter is
// accepted but not forwarded.
type backendLinkPeerFetcher struct {
	ops backends.Operations
}

// GetLinkPeers satisfies coordinators.LinkPeerFetcher.
func (f *backendLinkPeerFetcher) GetLinkPeers(ctx context.Context, _ hmtypes.WireInterfaceID, channelAddress string) ([]string, error) {
	return f.ops.GetLinkPeers(ctx, channelAddress)
}

var _ coordinators.LinkPeerFetcher = (*backendLinkPeerFetcher)(nil)

// singleDeviceDescFetcher fetches a description for exactly one device
// and its channels by address, satisfying coordinators.DeviceDescriptionFetcher.
// It uses GetDeviceDescription for the device and each child address from
// the CHILDREN field instead of ListDevices, scoping the refresh to the
// target device only.
type singleDeviceDescFetcher struct {
	ops     backends.Operations
	address string
}

// ListDevices satisfies coordinators.DeviceDescriptionFetcher. It fetches
// the target device description and each of its channel descriptions via
// GetDeviceDescription. A failure at the device level is propagated; a
// failure fetching an individual channel is logged and skipped so that one
// unreachable channel cannot abort the entire reload.
func (f *singleDeviceDescFetcher) ListDevices(ctx context.Context, _ hmtypes.WireInterfaceID) ([]hmproto.DeviceDescription, error) {
	rawDevice, err := f.ops.GetDeviceDescription(ctx, f.address)
	if err != nil {
		return nil, fmt.Errorf("device_reloader: GetDeviceDescription %s: %w", f.address, err)
	}
	if rawDevice == nil {
		return nil, fmt.Errorf("device_reloader: nil description returned for %s", f.address)
	}
	deviceDescs := backends.ParseDeviceDescriptions([]any{rawDevice})
	if len(deviceDescs) == 0 {
		return nil, fmt.Errorf("device_reloader: failed to parse description for %s", f.address)
	}
	dev := deviceDescs[0]
	// Capacity hint sized to the child count only; the leading device element
	// may trigger one extra grow, which is cheap. Avoid `1 + len(...)` here so
	// the allocation size carries no arithmetic over the CCU-supplied child
	// list (go/allocation-size-overflow).
	result := make([]hmproto.DeviceDescription, 0, len(dev.Children))
	result = append(result, dev)
	for _, childAddr := range dev.Children {
		rawChild, err := f.ops.GetDeviceDescription(ctx, childAddr)
		if err != nil {
			slog.Default().Warn("device_reloader: skipping channel fetch error",
				slog.String("device", f.address),
				slog.String("channel", childAddr),
				slog.String("err", err.Error()))
			continue
		}
		if rawChild == nil {
			continue
		}
		childDescs := backends.ParseDeviceDescriptions([]any{rawChild})
		if len(childDescs) == 0 {
			continue
		}
		result = append(result, childDescs[0])
	}
	return result, nil
}

var _ coordinators.DeviceDescriptionFetcher = (*singleDeviceDescFetcher)(nil)
