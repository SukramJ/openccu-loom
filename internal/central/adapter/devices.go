// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/client"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// DevicesAdapter implements handlers.DeviceIndex across every
// registered Unit.
type DevicesAdapter struct {
	registry *central.Registry
	writer   *client.ValueWriter
}

// NewDevicesAdapter constructs an adapter.
func NewDevicesAdapter(r *central.Registry) *DevicesAdapter {
	return &DevicesAdapter{registry: r}
}

// WithWriter attaches the per-central value writer so the adapter can
// reach individual backends (used by RefreshDevices). Optional —
// without a writer the refresh path is a no-op.
func (a *DevicesAdapter) WithWriter(w *client.ValueWriter) *DevicesAdapter {
	a.writer = w
	return a
}

// Devices returns every device across every central, sorted by
// address.
func (a *DevicesAdapter) Devices() []*device.Device {
	if a.registry == nil {
		return nil
	}
	var out []*device.Device
	for _, u := range a.registry.List() {
		out = append(out, u.ModelRegistry.List()...)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Address < out[j].Address })
	return out
}

// Device resolves a device by address, provided exactly one central holds it.
//
// An address is unique within one CCU but not across a registry holding
// several: the virtual-remote roots (BidCoS-RF, BidCos-Wir, HmIP-RCV-1) and
// the INT000* group devices repeat verbatim on every CCU, which is why the
// routing keys namespace themselves per central (see internal/routingkey).
// [central.Registry.List] walks in central-name order, so first-match served
// the alphabetically first CCU's device for those addresses — one
// installation's data under a bare address, and the later CCUs unreachable
// through every address-keyed surface.
//
// Resolving them correctly needs the central alongside the address, which the
// callers of this facade do not carry. Until they do, an ambiguous address
// resolves to nothing: answering "not found" is wrong in a way the caller can
// see, while answering with an arbitrary CCU's device is wrong in a way it
// cannot. Single-CCU installations are unaffected, and so is every address
// only one central holds.
func (a *DevicesAdapter) Device(address string) (*device.Device, bool) {
	if a.registry == nil {
		return nil, false
	}
	d, _, err := resolveUniqueDevice(a.registry, address)
	if err != nil {
		return nil, false
	}
	return d, d != nil
}

// RefreshDevices triggers a fresh ListDevices on every wired backend
// so the registry picks up CCU-side pairing/room changes without
// waiting for the next periodic sweep.
func (a *DevicesAdapter) RefreshDevices(ctx context.Context) error {
	if a.registry == nil {
		return errors.New("adapter: no registry wired")
	}
	if a.writer == nil {
		// Without the value writer we have no backend handle. Still
		// return success — the periodic pipeline will catch up.
		return nil
	}
	for _, u := range a.registry.List() {
		// The registry knows interface ids only via devices; iterate
		// the unique interfaces seen on this central.
		seen := make(map[string]struct{})
		for _, dev := range u.ModelRegistry.List() {
			if _, dup := seen[dev.InterfaceID]; dup {
				continue
			}
			seen[dev.InterfaceID] = struct{}{}
			backend, ok := a.writer.Backend(u.Name(), hmtypes.ParseWireInterfaceID(dev.InterfaceID))
			if !ok {
				continue
			}
			if _, err := backend.ListDevices(ctx); err != nil {
				continue // best-effort
			}
		}
	}
	return nil
}

// CentralOf returns the name of the central that owns the device.
// Empty string when the device is unknown. It resolves by address and
// therefore carries the same multi-CCU limitation as [DevicesAdapter.Device]:
// an address that exists on several CCUs is attributed to the first one by
// name.
func (a *DevicesAdapter) CentralOf(address string) string {
	if a.registry == nil {
		return ""
	}
	for _, u := range a.registry.List() {
		if _, ok := u.ModelRegistry.Get(address); ok {
			return u.Name()
		}
	}
	return ""
}

// ChannelMeta implements handlers.ChannelInfoReader: it resolves the
// device address, model, channel type and owning central for a channel
// address, so the config-export endpoint can stamp them onto the
// exported snapshot. Reports false when no central holds the channel.
func (a *DevicesAdapter) ChannelMeta(channelAddress string) (deviceAddress, model, channelType, centralName string, ok bool) {
	if a.registry == nil {
		return "", "", "", "", false
	}
	devAddr := deviceAddressOf(channelAddress)
	for _, u := range a.registry.List() {
		dev, found := u.ModelRegistry.Get(devAddr)
		if !found {
			continue
		}
		ch := dev.Channel(channelAddress)
		if ch == nil {
			// The device exists but not that channel number — the export
			// would name a channel the CCU does not have.
			return "", "", "", "", false
		}
		return dev.Address, dev.Model, ch.Type, u.Name(), true
	}
	return "", "", "", "", false
}

// SerialSuffix delegates to the registry's central → serial-suffix mapping
// so the device endpoints can stamp the canonical `unique_id` onto their
// summaries. Empty string when the central is unknown.
func (a *DevicesAdapter) SerialSuffix(centralName string) string {
	if a.registry == nil {
		return ""
	}
	return a.registry.SerialSuffix(centralName)
}

// Released delegates the onboarding-release state to the registry, so the
// REST surface can tell an ecosystem consumer which devices are finished.
//
// This surface deliberately still LISTS an unreleased device — the Config
// UI must see it to configure it — so the state travels as a field rather
// than as a filter. An unknown address reports released.
func (a *DevicesAdapter) Released(address string) bool {
	if a.registry == nil {
		return true
	}
	return a.registry.Released(address)
}

// DataPointWriterAdapter routes SetValue calls to the right
// central's InterfaceClient via [ValueWriter] per central.
type DataPointWriterAdapter struct {
	registry *central.Registry
	writer   ValueWriter
}

// ValueWriter is the write-path contract every central's client
// layer exposes. Implementations live in the client package.
type ValueWriter interface {
	SetValue(ctx context.Context, centralName, interfaceID, channelAddress string,
		parameter hmenum.Parameter, value any, priority hmenum.CommandPriority) error
}

// ParamsetValueWriter is the optional extension a [ValueWriter] carries
// when it can write several parameters of one paramset in a single call.
//
// It is what lets a data point reach the device atomically where the
// semantics require it — a bounded switch-on has to carry its auto-off
// in the same write, or the two travel as separate radio transmissions
// out of a duty-cycle budget the following stop command needs. Writers
// without the capability keep working; the data point falls back to an
// ordered pair of single writes.
type ParamsetValueWriter interface {
	ValueWriter
	PutParamset(ctx context.Context, centralName, interfaceID, channelAddress string,
		paramsetKey hmenum.ParamsetKey, values map[string]any, priority hmenum.CommandPriority) error
}

// NewDataPointWriterAdapter wires the adapter. `writer` may be nil
// when the daemon runs read-only (e.g. during setup).
func NewDataPointWriterAdapter(r *central.Registry, w ValueWriter) *DataPointWriterAdapter {
	return &DataPointWriterAdapter{registry: r, writer: w}
}

// ErrNoWriter is returned when the adapter was constructed without a
// concrete ValueWriter.
var ErrNoWriter = errors.New("adapter: no value writer wired")

// SetValue implements handlers.DataPointWriter. It finds the device that owns
// channelAddress and dispatches through that central's writer.
//
// The lookup is address-keyed, so it carries the constraint documented on
// [DevicesAdapter.Device]: a channel whose address exists on several CCUs is
// refused rather than written on an arbitrary one. Delivering a command to
// the wrong installation's hardware is the one outcome that cannot be
// noticed from the outside.
func (a *DataPointWriterAdapter) SetValue(
	ctx context.Context, channelAddress string, parameter hmenum.Parameter,
	value any, priority hmenum.CommandPriority,
) error {
	if a.writer == nil {
		return ErrNoWriter
	}
	deviceAddr := deviceAddressOf(channelAddress)
	dev, centralName, err := resolveUniqueDevice(a.registry, deviceAddr)
	if err != nil {
		return err
	}
	if dev == nil {
		return fmt.Errorf("adapter: device %s not found", deviceAddr)
	}
	return a.writer.SetValue(ctx, centralName, dev.InterfaceID, channelAddress, parameter, value, priority)
}

// resolveUniqueDevice finds the device with the given address and the name of
// the central that holds it.
//
// Returns (nil, "", nil) when no central holds the address, and an error
// naming every candidate when more than one does — the caller has not said
// which installation it means, and picking one silently routes reads and
// writes to a foreign CCU.
func resolveUniqueDevice(reg *central.Registry, deviceAddress string) (*device.Device, string, error) {
	if reg == nil {
		return nil, "", nil
	}
	var (
		found      *device.Device
		foundOn    string
		candidates []string
	)
	for _, u := range reg.List() {
		d, ok := u.ModelRegistry.Get(deviceAddress)
		if !ok {
			continue
		}
		candidates = append(candidates, u.Name())
		if found == nil {
			found, foundOn = d, u.Name()
		}
	}
	if len(candidates) > 1 {
		return nil, "", fmt.Errorf("adapter: device %s exists on several centrals (%s); the address alone does not say which",
			deviceAddress, strings.Join(candidates, ", "))
	}
	return found, foundOn, nil
}

func deviceAddressOf(channel string) string {
	for i := len(channel) - 1; i >= 0; i-- {
		if channel[i] == ':' {
			return channel[:i]
		}
	}
	return channel
}
