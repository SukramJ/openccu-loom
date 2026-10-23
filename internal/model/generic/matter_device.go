// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package generic

import (
	"fmt"
	"sync"

	"github.com/SukramJ/go-fabric/contract"
	fabricdevice "github.com/SukramJ/go-fabric/device"
	"github.com/SukramJ/go-fabric/schema"
)

// MatterDeviceHolder keeps the one go-fabric device a source is on the
// Matter plane (ADR 0084). The assembler asks for the device on every
// assembly and the bridge keeps the servers it already serves; a device
// rebuilt each time would lose its servers' timers, data versions and
// the subscription state behind them.
type MatterDeviceHolder struct {
	mu  sync.Mutex
	dev *fabricdevice.Device
}

// Device returns the held device, building it through build on first
// use. A build that fails is not held, so the next assembly tries again;
// a build that returns a nil device holds nothing either.
func (h *MatterDeviceHolder) Device(build func() (*fabricdevice.Device, error)) (*fabricdevice.Device, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.dev != nil {
		return h.dev, nil
	}
	d, err := build()
	if err != nil {
		return nil, err
	}
	h.dev = d
	return d, nil
}

// Current returns the held device, or nil before the first successful
// build.
func (h *MatterDeviceHolder) Current() *fabricdevice.Device {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.dev
}

// Quiesce stops the timers of the held device's servers when the source
// retires, so no countdown outlives the data point it would write to. A
// source that never served Matter has nothing to stop.
func (h *MatterDeviceHolder) Quiesce() {
	h.mu.Lock()
	d := h.dev
	h.mu.Unlock()
	if d == nil {
		return
	}
	for _, srv := range d.Servers() {
		if q, ok := srv.(contract.ClusterQuiescer); ok {
			q.MatterQuiesce()
		}
	}
}

// NewMatterDevice builds the device of source: the generated device type
// for deviceType, opts, and the source's cluster declarations. source is
// any because the profiles share no type and only one capability of it
// is read: a source that implements [contract.ChangeNotifier] is declared
// as the device's change notifier, so the bridge keeps hearing the
// model's confirmed updates for the clusters whose servers read the
// source.
func NewMatterDevice(source any, deviceType uint16, opts fabricdevice.Options, decls ...fabricdevice.Declaration) (*fabricdevice.Device, error) {
	if n, ok := source.(contract.ChangeNotifier); ok {
		decls = append(decls, fabricdevice.WithChangeNotifier(n))
	}
	d, err := fabricdevice.FromSpec(fabricdevice.Spec{
		Type:         schema.DeviceTypeID(deviceType),
		Options:      opts,
		Declarations: decls,
	})
	if err != nil {
		return nil, fmt.Errorf("matter device 0x%04X: %w", deviceType, err)
	}
	return d, nil
}
