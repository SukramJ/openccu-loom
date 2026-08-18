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
	"github.com/SukramJ/openccu-loom/internal/model/hub"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
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
		backend, ok := a.writer.Backend(u.Name(), hmtypes.ParseWireInterfaceID(dev.InterfaceID))
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
		backend, ok := a.writer.Backend(u.Name(), hmtypes.ParseWireInterfaceID(dev.InterfaceID))
		if !ok {
			return fmt.Errorf("%w: %s/%s", ErrNoDeviceBackend, u.Name(), dev.InterfaceID)
		}
		if err := backend.DeleteDevice(ctx, address, flags); err != nil {
			return err
		}
		// Drop the local caches. The CCU's `deleteDevices` callback
		// will re-publish the deletion event; doing it eagerly here
		// keeps the SPA snappy.
		//
		// The description and paramset registries are keyed by the canonical
		// wire id (`<central>-<iface>`) and carry one entry per CHANNEL, not
		// one per device: deleting the device address under the bare interface
		// matched nothing, so every channel's descriptions survived the unpair
		// and the persistence sink was never asked to drop the SQLite rows —
		// which the next boot then rehydrated into a device the CCU no longer
		// reports. Snapshot the channels before RemoveDevice tears them down.
		iface := hmtypes.ParseWireInterfaceID(dev.InterfaceID)
		channels := dev.Channels()
		u.RemoveDevice(address)
		u.DeviceRegistry.Remove(iface, address)
		u.DescRegistry.Delete(iface, address)
		u.ParamsetReg.DeleteChannel(iface, address)
		for _, ch := range channels {
			u.DescRegistry.Delete(iface, ch.Address)
			u.ParamsetReg.DeleteChannel(iface, ch.Address)
		}
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

// AcceptInboxDevice promotes a pending device into the running
// registry. Two things can hold a device back and the operator sees
// both as one inbox entry, so the accept handles both:
//
//   - The CCU's own inbox: the Rega script flips the device's
//     `ReadyConfig` flag; the periodic ListDevices sweep picks the new
//     pairing up afterwards. We additionally call ListDevices once here
//     so the SPA sees the registry update without having to wait for
//     the sweep.
//   - The daemon's deferred-creation queue (`delay_new_device_creation`):
//     the announced descriptions are parked until this call materialises
//     them. Accepting only on the CCU left such a device without a
//     single data point until the next daemon restart.
//
// opts carries optional first-time configuration (name, rooms,
// functions) applied best-effort against the accepting central right
// after the promotion, and before the deferred materialisation so the
// new device is built with its final name. Those follow-up steps run
// only once the accept itself succeeded; if any of them fails the
// returned error wraps [interfaces.ErrAcceptConfigIncomplete] so the
// caller can tell the device WAS accepted and only the configuration
// needs to be re-applied.
func (a *DeviceAdminDomain) AcceptInboxDevice(
	ctx context.Context, address string, opts interfaces.AcceptInboxOptions,
) error {
	if a.registry == nil {
		return ErrNoDeviceBackend
	}
	var notInInbox bool
	for _, u := range a.registry.List() {
		if u.HubModel == nil {
			continue
		}
		_, deferred := pendingInterfaceOf(u, address)
		if err := u.HubModel.AcceptInboxDeviceRemote(ctx, address); err != nil {
			if errors.Is(err, interfaces.ErrInboxDeviceNotFound) {
				if deferred {
					// The CCU has already configured the device; only this
					// daemon is still holding it back. That is a complete
					// accept, not a stale entry.
					return a.finishAccept(ctx, u, address, opts)
				}
				// This central no longer has the device in its inbox (it
				// settled or was removed on the CCU). Drop the stale entry so
				// the SPA updates immediately, and remember it in case no
				// other central holds the device either.
				u.HubModel.Inbox.Remove(address)
				notInInbox = true
			}
			if deferred {
				if errors.Is(err, hub.ErrNoInboxAccepter) {
					// This central has no CCU-side inbox at all (a backend
					// without the pairing concept). Only the deferred queue
					// holds the device, so accepting it here is the whole job.
					return a.finishAccept(ctx, u, address, opts)
				}
				// The CCU-side accept failed on the very central that parked
				// the device. Materialising it now would hide that failure,
				// so surface it and leave the entry pending.
				return fmt.Errorf("accept inbox device %s: %w", address, err)
			}
			// Try the next central — the inbox may live on another CCU.
			continue
		}
		// Refresh device list on the matching central. Best-effort:
		// errors here are non-fatal because the periodic sweep will
		// eventually pick up the new device anyway.
		if a.writer != nil {
			if dev, ok := u.ModelRegistry.Get(address); ok {
				if backend, ok := a.writer.Backend(u.Name(), hmtypes.ParseWireInterfaceID(dev.InterfaceID)); ok {
					_, _ = backend.ListDevices(ctx)
				}
			}
		}
		return a.finishAccept(ctx, u, address, opts)
	}
	if notInInbox {
		// At least one central reported the device gone and none accepted it:
		// the inbox entry is stale. Surface the 404-mapped sentinel rather than
		// the generic no-backend error (502).
		return fmt.Errorf("%w: device %s", interfaces.ErrInboxDeviceNotFound, address)
	}
	return fmt.Errorf("%w: device %s", ErrNoDeviceBackend, address)
}

// finishAccept runs the two steps that follow a successful promotion on
// the accepting central: the optional first-time configuration and the
// materialisation of a deferred device. The materialisation runs even
// when the configuration failed — the device was accepted either way,
// and leaving it parked would strand it — but a configuration failure is
// still reported so the operator re-applies it.
func (a *DeviceAdminDomain) finishAccept(
	ctx context.Context, u *central.Unit, address string, opts interfaces.AcceptInboxOptions,
) error {
	configErr := applyInitialConfig(ctx, u, address, opts)
	if _, err := AcceptPendingDevice(ctx, u, address); err != nil {
		return err
	}
	if configErr != nil {
		return fmt.Errorf("%w: %w", interfaces.ErrAcceptConfigIncomplete, configErr)
	}
	return nil
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

// RestoreDeviceConfig re-transmits the centrally stored configuration
// (MASTER paramsets of every channel plus link peerings) to the device
// via `restoreConfigToDevice` (XML-RPC). Only rfd (BidCos-RF) and
// HMIPServer (HmIP-RF) expose the method; devices on any other
// interface answer [backends.ErrUnsupported] before a wire call is
// made. The CCU runs the transfer asynchronously (CONFIG_PENDING).
func (a *DeviceAdminDomain) RestoreDeviceConfig(ctx context.Context, address string) error {
	if a.registry == nil || a.writer == nil {
		return ErrNoDeviceBackend
	}
	for _, u := range a.registry.List() {
		dev, ok := u.ModelRegistry.Get(address)
		if !ok {
			continue
		}
		if !dev.Interface.SupportsConfigRestore() {
			return fmt.Errorf("restore config: interface %s: %w", dev.Interface, backends.ErrUnsupported)
		}
		backend, ok := a.writer.Backend(u.Name(), hmtypes.ParseWireInterfaceID(dev.InterfaceID))
		if !ok {
			return fmt.Errorf("%w: %s/%s", ErrNoDeviceBackend, u.Name(), dev.InterfaceID)
		}
		return backend.RestoreConfigToDevice(ctx, address)
	}
	return fmt.Errorf("%w: device %s", ErrNoDeviceBackend, address)
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
		dev.SetRooms(rooms)
		u.PublishDeviceMetadataChanged(dev)
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
		dev.SetFunctions(functions)
		u.PublishDeviceMetadataChanged(dev)
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
			ch.SetRooms(rooms)
			dev.SetRooms(unionChannelAssignments(dev, func(c *device.Channel) []string { return c.Rooms() }))
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
			ch.SetFunctions(functions)
			dev.SetFunctions(unionChannelAssignments(dev, func(c *device.Channel) []string { return c.Functions() }))
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
		u.PublishDeviceMetadataChanged(dev)
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
