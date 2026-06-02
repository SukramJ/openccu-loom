// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/client"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
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
	for _, c := range a.registry.List() {
		out = append(out, c.ModelRegistry.List()...)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Address < out[j].Address })
	return out
}

// Device returns the first matching device across every central.
// Device addresses are globally unique on a CCU, so the first hit
// is canonical.
func (a *DevicesAdapter) Device(address string) (*device.Device, bool) {
	if a.registry == nil {
		return nil, false
	}
	for _, c := range a.registry.List() {
		if d, ok := c.ModelRegistry.Get(address); ok {
			return d, true
		}
	}
	return nil, false
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
	for _, c := range a.registry.List() {
		// The registry knows interface ids only via devices; iterate
		// the unique interfaces seen on this central.
		seen := make(map[string]struct{})
		for _, dev := range c.ModelRegistry.List() {
			if _, dup := seen[dev.InterfaceID]; dup {
				continue
			}
			seen[dev.InterfaceID] = struct{}{}
			backend, ok := a.writer.Backend(c.Name(), dev.InterfaceID)
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
// Empty string when the device is unknown.
func (a *DevicesAdapter) CentralOf(address string) string {
	if a.registry == nil {
		return ""
	}
	for _, c := range a.registry.List() {
		if _, ok := c.ModelRegistry.Get(address); ok {
			return c.Name()
		}
	}
	return ""
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

// NewDataPointWriterAdapter wires the adapter. `writer` may be nil
// when the daemon runs read-only (e.g. during setup).
func NewDataPointWriterAdapter(r *central.Registry, w ValueWriter) *DataPointWriterAdapter {
	return &DataPointWriterAdapter{registry: r, writer: w}
}

// ErrNoWriter is returned when the adapter was constructed without a
// concrete ValueWriter.
var ErrNoWriter = errors.New("adapter: no value writer wired")

// SetValue implements handlers.DataPointWriter. It walks every
// central, finds the device that owns channelAddress, and dispatches
// through the central's writer.
func (a *DataPointWriterAdapter) SetValue(
	ctx context.Context, channelAddress string, parameter hmenum.Parameter,
	value any, priority hmenum.CommandPriority,
) error {
	if a.writer == nil {
		return ErrNoWriter
	}
	deviceAddr := deviceAddressOf(channelAddress)
	for _, c := range a.registry.List() {
		dev, ok := c.ModelRegistry.Get(deviceAddr)
		if !ok {
			continue
		}
		return a.writer.SetValue(ctx, c.Name(), dev.InterfaceID, channelAddress, parameter, value, priority)
	}
	return fmt.Errorf("adapter: device %s not found", deviceAddr)
}

func deviceAddressOf(channel string) string {
	for i := len(channel) - 1; i >= 0; i-- {
		if channel[i] == ':' {
			return channel[:i]
		}
	}
	return channel
}
