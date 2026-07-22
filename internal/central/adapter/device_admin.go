// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/client"
	"github.com/SukramJ/openccu-loom/internal/client/backends"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/pkg/interfaces"
)

// DeviceAdminDomain is the live implementation of handlers.DeviceAdmin.
// It walks the central registry, locates the device's owning central
// and dispatches the operation. Most calls are XML-RPC bound; the
// renaming path persists through the central's JSON-RPC rename hook
// (Device.setName / Channel.setName) wired in ccu_wiring.go.
type DeviceAdminDomain struct {
	registry *central.Registry
	writer   *client.ValueWriter
}

// NewDeviceAdminDomain wires the live adapter.
func NewDeviceAdminDomain(r *central.Registry, w *client.ValueWriter) *DeviceAdminDomain {
	return &DeviceAdminDomain{registry: r, writer: w}
}

// ErrNoDeviceBackend bubbles when the CCU backend cannot be resolved.
var ErrNoDeviceBackend = errors.New("device-admin: no backend for device")

// resolve walks every central, finds the owning device + backend.
// Returns the backend so callers can issue XML-RPC operations
// against the device's interface.
func (a *DeviceAdminDomain) resolve(deviceAddress string) (backends.Operations, error) {
	if a.registry == nil || a.writer == nil {
		return nil, ErrNoDeviceBackend
	}
	for _, u := range a.registry.List() {
		dev, ok := u.ModelRegistry.Get(deviceAddress)
		if !ok {
			continue
		}
		backend, ok := a.writer.Backend(u.Name(), dev.InterfaceID)
		if !ok {
			return nil, fmt.Errorf("%w: %s/%s", ErrNoDeviceBackend, u.Name(), dev.InterfaceID)
		}
		return backend, nil
	}
	return nil, fmt.Errorf("%w: device %s", ErrNoDeviceBackend, deviceAddress)
}

// UnpairDevice asks the CCU to unpair the device. Maps to the CCU's
// XML-RPC `deleteDevice(address, flags)` call via the backend's
// [backends.Operations.DeleteDevice]. reset factory-resets the device as
// part of the removal ([backends.DeleteFlagReset]); force removes an
// unreachable device even when the CCU cannot complete the handshake
// ([backends.DeleteFlagForce]).
//
// On success the in-memory caches (paramset, description, model
// registry) drop the device so the SPA does not see a stale entry
// while the CCU's `deleteDevices` callback catches up. Several
// backends do not expose unpair (CUxD, JSON-only) — they surface
// ErrUnsupported through the underlying Operations contract; the
// handler returns 422 in that case.
func (a *DeviceAdminDomain) UnpairDevice(ctx context.Context, address string, reset, force bool) error {
	if a.registry == nil || a.writer == nil {
		return ErrNoDeviceBackend
	}
	flags := 0
	if reset {
		flags |= backends.DeleteFlagReset
	}
	if force {
		flags |= backends.DeleteFlagForce
	}
	for _, u := range a.registry.List() {
		dev, ok := u.ModelRegistry.Get(address)
		if !ok {
			continue
		}
		backend, ok := a.writer.Backend(u.Name(), dev.InterfaceID)
		if !ok {
			return fmt.Errorf("%w: %s/%s", ErrNoDeviceBackend, u.Name(), dev.InterfaceID)
		}
		if err := backend.DeleteDevice(ctx, address, flags); err != nil {
			return err
		}
		// Drop the local caches. The CCU's `deleteDevices` callback
		// will re-publish the deletion event; doing it eagerly here
		// keeps the SPA snappy.
		u.RemoveDevice(address)
		u.DeviceRegistry.Remove(dev.Interface, address)
		u.DescRegistry.Delete(dev.Interface, address)
		u.ParamsetReg.DeleteChannel(dev.Interface, address)
		return nil
	}
	return fmt.Errorf("%w: device %s", ErrNoDeviceBackend, address)
}

// RenameDevice updates the device name and persists it to the CCU via
// the central's rename hook (JSON-RPC `Device.setName`). When
// includeChannels is true every channel is renamed along with the
// "<name>:<channelNo>" pattern. The persistent call's error is
// propagated — a failed CCU rename is not silently swallowed.
func (a *DeviceAdminDomain) RenameDevice(ctx context.Context, address, name string, includeChannels bool) error {
	if a.registry == nil {
		return ErrNoDeviceBackend
	}
	for _, u := range a.registry.List() {
		if _, ok := u.ModelRegistry.Get(address); !ok {
			continue
		}
		return u.RenameDeviceWithChannels(ctx, address, name, includeChannels)
	}
	return fmt.Errorf("%w: device %s", ErrNoDeviceBackend, address)
}

// RenameChannel updates a single channel name and persists it to the
// CCU via the central's rename hook (JSON-RPC `Channel.setName`). The
// channel address is resolved as deviceAddr + ":" + channelNo. The
// persistent call's error is propagated.
func (a *DeviceAdminDomain) RenameChannel(ctx context.Context, deviceAddr string, channelNo int, name string) error {
	if a.registry == nil {
		return ErrNoDeviceBackend
	}
	channelAddress := deviceAddr + ":" + strconv.Itoa(channelNo)
	for _, u := range a.registry.List() {
		if _, ok := u.ModelRegistry.Get(deviceAddr); !ok {
			continue
		}
		return u.RenameChannel(ctx, channelAddress, name)
	}
	return fmt.Errorf("%w: device %s", ErrNoDeviceBackend, deviceAddr)
}

// AcceptInboxDevice promotes a pending device from the hub inbox
// into the running registry. The Rega script flips the device's
// `ReadyConfig` flag; the periodic ListDevices sweep picks the new
// pairing up afterwards. We additionally call ListDevices once
// here so the SPA sees the registry update without having to wait
// for the sweep.
//
// opts carries optional first-time configuration (name, rooms,
// functions) applied best-effort against the accepting central right
// after the promotion. Those follow-up steps run only once the accept
// itself succeeded; if any of them fails the returned error wraps
// [interfaces.ErrAcceptConfigIncomplete] so the caller can tell the
// device WAS accepted and only the configuration needs to be re-applied.
func (a *DeviceAdminDomain) AcceptInboxDevice(
	ctx context.Context, address string, opts interfaces.AcceptInboxOptions,
) error {
	if a.registry == nil {
		return ErrNoDeviceBackend
	}
	for _, u := range a.registry.List() {
		if u.HubModel == nil {
			continue
		}
		if err := u.HubModel.AcceptInboxDeviceRemote(ctx, address); err != nil {
			// Try the next central — the inbox may live on another CCU.
			continue
		}
		// Refresh device list on the matching central. Best-effort:
		// errors here are non-fatal because the periodic sweep will
		// eventually pick up the new device anyway.
		if a.writer != nil {
			if dev, ok := u.ModelRegistry.Get(address); ok {
				if backend, ok := a.writer.Backend(u.Name(), dev.InterfaceID); ok {
					_, _ = backend.ListDevices(ctx)
				}
			}
		}
		// Apply the optional first-time configuration on the same central
		// that accepted the device. The accept has already happened, so a
		// follow-up failure is wrapped (never swallowed) to signal a
		// partial success.
		if err := applyInitialConfig(ctx, u, address, opts); err != nil {
			return fmt.Errorf("%w: %w", interfaces.ErrAcceptConfigIncomplete, err)
		}
		return nil
	}
	return fmt.Errorf("%w: device %s", ErrNoDeviceBackend, address)
}

// applyInitialConfig runs the optional first-time configuration steps
// (rename, rooms, functions) against the central that just accepted an
// inbox device. Every requested step is attempted even when an earlier
// one fails, and every error is joined into the return value so a
// partial failure is neither hidden nor short-circuits the remaining
// steps. The rooms / functions writes go straight to the hub remotes
// (Rega `set_device_rooms` / `set_device_functions`) rather than the
// [DeviceAdminDomain.SetRooms] wrapper: a freshly accepted device may
// not have materialised in the model registry yet, and the Rega scripts
// address the device on the CCU directly.
func applyInitialConfig(
	ctx context.Context, u *central.Unit, address string, opts interfaces.AcceptInboxOptions,
) error {
	var errs []error
	if opts.Name != "" {
		if err := u.RenameDeviceWithChannels(ctx, address, opts.Name, opts.IncludeChannels); err != nil {
			errs = append(errs, fmt.Errorf("rename: %w", err))
		}
	}
	if opts.Rooms != nil && u.HubModel != nil {
		if err := u.HubModel.SetDeviceRoomsRemote(ctx, address, opts.Rooms); err != nil {
			errs = append(errs, fmt.Errorf("rooms: %w", err))
		}
	}
	if opts.Functions != nil && u.HubModel != nil {
		if err := u.HubModel.SetDeviceFunctionsRemote(ctx, address, opts.Functions); err != nil {
			errs = append(errs, fmt.Errorf("functions: %w", err))
		}
	}
	return errors.Join(errs...)
}

// UpdateFirmware triggers an OTA update on the CCU. Maps to
// `Interface.updateFirmware` (XML-RPC). The CCU runs the transfer
// asynchronously; this call returns once the request was accepted.
func (a *DeviceAdminDomain) UpdateFirmware(ctx context.Context, address string) error {
	backend, err := a.resolve(address)
	if err != nil {
		return err
	}
	return backend.UpdateFirmware(ctx, address)
}

// InterfaceDutyCycle returns the transmit duty cycle in percent (0..100)
// of the radio interface the device is paired to, read from the owning
// central's per-interface BidCos utilisation cache (populated by the
// periodic listBidcosInterfaces poll). The bool is false when the device
// is unknown, the central has no hub coordinator, the interface carries
// no BidCos gateway (HmIP), or the poll has not run yet. It performs no
// CCU round trip so the firmware-update handler can gate on it inline.
func (a *DeviceAdminDomain) InterfaceDutyCycle(address string) (int, bool) {
	if a.registry == nil {
		return 0, false
	}
	for _, u := range a.registry.List() {
		dev, ok := u.ModelRegistry.Get(address)
		if !ok {
			continue
		}
		if u.Hub == nil {
			return 0, false
		}
		info, ok := u.Hub.BidcosInterface(dev.InterfaceID)
		if !ok || info.DutyCycle < 0 {
			return 0, false
		}
		return info.DutyCycle, true
	}
	return 0, false
}

// SetInstallMode opens a per-device pairing window via the backend's
// XML-RPC `setInstallMode(true, durationSecs, mode=1, address)` call.
// `mode=1` is the CCU's "normal" install mode (mode=2 means "ready
// for re-pairing"); 0.1.0 only exposes the normal mode.
func (a *DeviceAdminDomain) SetInstallMode(ctx context.Context, address string, durationSecs int) error {
	backend, err := a.resolve(address)
	if err != nil {
		return err
	}
	return backend.SetInstallMode(ctx, true, durationSecs, 1, address)
}

// SetRooms replaces the device's room assignments via the central's
// hub-writer (Rega `set_device_rooms`).
func (a *DeviceAdminDomain) SetRooms(
	ctx context.Context, address string, rooms []string,
) error {
	if a.registry == nil {
		return ErrNoDeviceBackend
	}
	for _, u := range a.registry.List() {
		dev, ok := u.ModelRegistry.Get(address)
		if !ok {
			continue
		}
		if u.HubModel == nil {
			return fmt.Errorf("%w: hub not wired for %s", ErrNoDeviceBackend, u.Name())
		}
		if err := u.HubModel.SetDeviceRoomsRemote(ctx, address, rooms); err != nil {
			return err
		}
		dev.Rooms = append([]string(nil), rooms...)
		return nil
	}
	return fmt.Errorf("%w: device %s", ErrNoDeviceBackend, address)
}

// SetFunctions replaces the device's function (Gewerk) assignments
// via the central's hub-writer (Rega `set_device_functions`).
func (a *DeviceAdminDomain) SetFunctions(
	ctx context.Context, address string, functions []string,
) error {
	if a.registry == nil {
		return ErrNoDeviceBackend
	}
	for _, u := range a.registry.List() {
		dev, ok := u.ModelRegistry.Get(address)
		if !ok {
			continue
		}
		if u.HubModel == nil {
			return fmt.Errorf("%w: hub not wired for %s", ErrNoDeviceBackend, u.Name())
		}
		if err := u.HubModel.SetDeviceFunctionsRemote(ctx, address, functions); err != nil {
			return err
		}
		dev.Functions = append([]string(nil), functions...)
		return nil
	}
	return fmt.Errorf("%w: device %s", ErrNoDeviceBackend, address)
}

// SetChannelRooms replaces a single channel's room assignments via the
// central's hub-writer. The Rega script resolves channel addresses the
// same way it resolves device addresses, so the write reuses the
// device-level set_device_rooms path with the channel address. The live
// model is stamped eagerly: the channel gets the new set verbatim and
// the parent device's Rooms are recomputed as the union over all
// channels; device-direct assignments reappear with the next periodic
// assignment refresh.
func (a *DeviceAdminDomain) SetChannelRooms(
	ctx context.Context, deviceAddr string, channelNo int, rooms []string,
) error {
	return a.setChannelAssignment(ctx, deviceAddr, channelNo,
		func(ctx context.Context, u *central.Unit, channelAddress string) error {
			return u.HubModel.SetDeviceRoomsRemote(ctx, channelAddress, rooms)
		},
		func(dev *device.Device, ch *device.Channel) {
			ch.Rooms = append([]string(nil), rooms...)
			dev.Rooms = unionChannelAssignments(dev, func(c *device.Channel) []string { return c.Rooms })
		})
}

// SetChannelFunctions replaces a single channel's function (Gewerk)
// assignments, mirroring [DeviceAdminDomain.SetChannelRooms].
func (a *DeviceAdminDomain) SetChannelFunctions(
	ctx context.Context, deviceAddr string, channelNo int, functions []string,
) error {
	return a.setChannelAssignment(ctx, deviceAddr, channelNo,
		func(ctx context.Context, u *central.Unit, channelAddress string) error {
			return u.HubModel.SetDeviceFunctionsRemote(ctx, channelAddress, functions)
		},
		func(dev *device.Device, ch *device.Channel) {
			ch.Functions = append([]string(nil), functions...)
			dev.Functions = unionChannelAssignments(dev, func(c *device.Channel) []string { return c.Functions })
		})
}

// setChannelAssignment locates the owning central + channel, dispatches
// the CCU write and, on success, stamps the live model so reads stay
// coherent until the next periodic assignment refresh reconciles with
// the CCU.
func (a *DeviceAdminDomain) setChannelAssignment(
	ctx context.Context, deviceAddr string, channelNo int,
	write func(ctx context.Context, u *central.Unit, channelAddress string) error,
	stamp func(dev *device.Device, ch *device.Channel),
) error {
	if a.registry == nil {
		return ErrNoDeviceBackend
	}
	channelAddress := deviceAddr + ":" + strconv.Itoa(channelNo)
	for _, u := range a.registry.List() {
		dev, ok := u.ModelRegistry.Get(deviceAddr)
		if !ok {
			continue
		}
		ch := dev.Channel(channelAddress)
		if ch == nil {
			return fmt.Errorf("%w: %s", interfaces.ErrChannelNotFound, channelAddress)
		}
		if u.HubModel == nil {
			return fmt.Errorf("%w: hub not wired for %s", ErrNoDeviceBackend, u.Name())
		}
		if err := write(ctx, u, channelAddress); err != nil {
			return err
		}
		stamp(dev, ch)
		return nil
	}
	return fmt.Errorf("%w: device %s", ErrNoDeviceBackend, deviceAddr)
}

// unionChannelAssignments collects the sorted union of a per-channel
// assignment slice across every channel of the device.
func unionChannelAssignments(dev *device.Device, pick func(*device.Channel) []string) []string {
	seen := map[string]struct{}{}
	var out []string
	for _, ch := range dev.Channels() {
		for _, name := range pick(ch) {
			if _, ok := seen[name]; ok {
				continue
			}
			seen[name] = struct{}{}
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}
