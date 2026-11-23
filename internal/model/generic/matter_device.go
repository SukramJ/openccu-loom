// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package generic

import (
	"fmt"
	"sync"

	"thingfabric.dev/go-fabric/contract"
	fabricdevice "thingfabric.dev/go-fabric/device"
	"thingfabric.dev/go-fabric/schema"

	"github.com/SukramJ/openccu-loom/pkg/interfaces"
)

// MatterDeviceHolder keeps the go-fabric device a source is on the Matter
// plane (ADR 0084). The assembler asks for the device on every assembly
// and the bridge keeps the servers it already serves; a device rebuilt
// each time would lose its servers' timers, data versions and the
// subscription state behind them. The holder builds a new device only
// when the host's facts change ([interfaces.MatterHostFacts]), which
// go-fabric takes only at construction.
type MatterDeviceHolder struct {
	mu    sync.Mutex
	dev   *fabricdevice.Device
	facts interfaces.MatterHostFacts
	bound bool
}

// MatterDeviceBuild is how a source builds its device.
type MatterDeviceBuild struct {
	// New builds the device from the source's current state, with host
	// appended to the source's own declarations.
	New func(host []fabricdevice.Declaration) (*fabricdevice.Device, error)
	// Bind registers the source's model hooks, once, after the first
	// device is held. A hook reaches the device through
	// [MatterDeviceHolder.Current], so it feeds whichever device the
	// holder holds after a rebuild; nil when the source has none.
	Bind func()
	// Settle stores the source's current state into d after every build,
	// once d is held: a confirmation that landed between New's read of
	// the state and the swap is not lost, and storing an unchanged value
	// again is a no-op. nil when the source has nothing to settle.
	Settle func(d *fabricdevice.Device)
}

// Device returns the held device for req, building it through b on first
// use and building a new one when req.Host differs from the facts the
// held device was built with. A build that fails holds nothing new: the
// first build is tried again on the next assembly, and a failed rebuild
// keeps the previous device held and returns the error. A build that
// returns a nil device holds nothing either.
func (h *MatterDeviceHolder) Device(req interfaces.MatterDeviceRequest, b MatterDeviceBuild) (*fabricdevice.Device, error) {
	h.mu.Lock()
	if h.dev != nil && h.facts == req.Host {
		d := h.dev
		h.mu.Unlock()
		return d, nil
	}
	d, err := b.New(req.Host.Declarations())
	if err != nil || d == nil {
		h.mu.Unlock()
		return nil, err
	}
	h.dev, h.facts = d, req.Host
	bind := !h.bound && b.Bind != nil
	h.bound = h.bound || b.Bind != nil
	h.mu.Unlock()
	// Hooks and the settle run without the holder's lock: a hook that
	// fires on registration reads Current.
	if bind {
		b.Bind()
	}
	if b.Settle != nil {
		b.Settle(d)
	}
	return d, nil
}

// Current returns the held device, or nil before the first successful
// build.
func (h *MatterDeviceHolder) Current() *fabricdevice.Device {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.dev
}

// Quiesce stops the timers of the held device's servers, so no countdown
// outlives the data point it would write to. The Light calls it from its
// Close; for every device, go-fabric's bridge quiesces the servers of a
// topology it replaces (bridge/bridge.go:1623) or stops (:2555). A
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
