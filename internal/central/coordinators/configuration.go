// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package coordinators

import (
	"sort"
	"sync"

	"github.com/SukramJ/openccu-loom/internal/central/registry"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
)

// ConfigurationCoordinator centralises the read paths north-bound adapters
// need to render configuration UIs (form schema generators, channel-config
// dashboards, paramset diagnostics).
//
// The coordinator owns no state; it is a thin facade over the existing
// description / paramset registries. That keeps the wire pipeline as the
// single producer (registry writes happen during device ingest) while every
// consumer (REST schema endpoint, WebSocket session backend, future
// FormSchemaGenerator) reads through the coordinator's stable surface.
//
// Thread-safety: every method is safe for concurrent use because the
// underlying registries are.
type ConfigurationCoordinator struct {
	descriptions *registry.DeviceDescriptionRegistry
	paramsets    *registry.ParamsetRegistry
	devices      *registry.DeviceRegistry

	// Optional override hook — when populated, [GetParameterData] looks up
	// patches before falling back to the registry. Empty by default.
	mu      sync.RWMutex
	patches map[paramKey]hmproto.ParameterData
}

type paramKey struct {
	channelAddress string
	paramsetKey    hmenum.ParamsetKey
	parameter      string
}

// NewConfigurationCoordinator wires the coordinator against the
// registries owned by a [central.Unit]. Either registry may be
// nil — calls that need an absent registry simply report "not found".
func NewConfigurationCoordinator(
	descriptions *registry.DeviceDescriptionRegistry,
	paramsets *registry.ParamsetRegistry,
	devices *registry.DeviceRegistry,
) *ConfigurationCoordinator {
	return &ConfigurationCoordinator{
		descriptions: descriptions,
		paramsets:    paramsets,
		devices:      devices,
		patches:      make(map[paramKey]hmproto.ParameterData),
	}
}

// GetParameterData returns the descriptor for a single parameter on a
// specific channel + paramset, applying any registered patch first.
//
// The boolean reports whether the lookup hit. Callers should not
// depend on a zero-valued [hmproto.ParameterData] when it is false
// downstream code may interpret an empty ValueList as "no constraint"
// even though no descriptor was found.
func (c *ConfigurationCoordinator) GetParameterData(
	iface hmenum.Interface,
	channelAddress string,
	paramsetKey hmenum.ParamsetKey,
	parameter string,
) (hmproto.ParameterData, bool) {
	c.mu.RLock()
	if patch, ok := c.patches[paramKey{channelAddress, paramsetKey, parameter}]; ok {
		c.mu.RUnlock()
		return patch, true
	}
	c.mu.RUnlock()

	if c.paramsets == nil {
		return hmproto.ParameterData{}, false
	}
	ps, ok := c.paramsets.Get(iface, channelAddress, paramsetKey)
	if !ok {
		return hmproto.ParameterData{}, false
	}
	pd, ok := ps[parameter]
	return pd, ok
}

// GetChannelParamset returns the full parameter map for a channel + paramset
// key.
//
// Patches override individual parameters — the returned map is a fresh copy,
// safe to mutate by the caller.
func (c *ConfigurationCoordinator) GetChannelParamset(
	iface hmenum.Interface,
	channelAddress string,
	paramsetKey hmenum.ParamsetKey,
) (hmproto.Paramset, bool) {
	if c.paramsets == nil {
		return nil, false
	}
	ps, ok := c.paramsets.Get(iface, channelAddress, paramsetKey)
	if !ok {
		return nil, false
	}
	out := make(hmproto.Paramset, len(ps))
	for k := range ps {
		out[k] = ps[k]
	}
	c.mu.RLock()
	for key := range c.patches {
		if key.channelAddress == channelAddress && key.paramsetKey == paramsetKey {
			out[key.parameter] = c.patches[key]
		}
	}
	c.mu.RUnlock()
	return out, true
}

// HasParameter reports whether the parameter is declared on the given channel
// + paramset (registry or patch).
func (c *ConfigurationCoordinator) HasParameter(
	iface hmenum.Interface,
	channelAddress string,
	paramsetKey hmenum.ParamsetKey,
	parameter string,
) bool {
	_, ok := c.GetParameterData(iface, channelAddress, paramsetKey, parameter)
	return ok
}

// PatchParameter overrides the descriptor returned by
// [GetParameterData]/[GetChannelParamset] for a specific parameter. firmware
// reports MIN=0 but the device actually rejects below 1) get fixed up on read
// without rewriting the persisted registry.
func (c *ConfigurationCoordinator) PatchParameter(
	channelAddress string,
	paramsetKey hmenum.ParamsetKey,
	parameter string,
	override hmproto.ParameterData,
) {
	c.mu.Lock()
	c.patches[paramKey{channelAddress, paramsetKey, parameter}] = override
	c.mu.Unlock()
}

// ClearPatch removes a previously registered patch. Returns true when
// a patch existed for the key.
func (c *ConfigurationCoordinator) ClearPatch(
	channelAddress string,
	paramsetKey hmenum.ParamsetKey,
	parameter string,
) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	key := paramKey{channelAddress, paramsetKey, parameter}
	if _, ok := c.patches[key]; !ok {
		return false
	}
	delete(c.patches, key)
	return true
}

// ConfigurableChannel summarises a channel that exposes at least one
// configurable parameter — i.e. has a non-empty MASTER paramset.
type ConfigurableChannel struct {
	DeviceAddress  string
	ChannelAddress string
	ChannelType    string
	ParamCount     int
}

// ConfigurableDevice is the aggregate device view returned by
// [GetConfigurableDevices].
type ConfigurableDevice struct {
	Address     string
	InterfaceID string
	Model       string
	Name        string
	Channels    []ConfigurableDeviceChannel
}

// ConfigurableDeviceChannel is a per-channel entry inside
// [ConfigurableDevice].
type ConfigurableDeviceChannel struct {
	Address      string
	ChannelType  string
	ParamsetKeys []hmenum.ParamsetKey
}

// MaintenanceData holds readings from a device's maintenance channel (channel
// :0).
type MaintenanceData struct {
	// UnreachCount is the number of times the device was unreachable.
	UnreachCount int
	// StickyUnreach is true when the device's sticky-unreachable bit is set.
	StickyUnreach bool
	// Error is the last device-reported error code, or 0.
	Error int
	// LowBat is true when the device reports a low battery.
	LowBat bool
	// RSSI is the last reported signal strength.
	RSSI int
}

// GetAllParamsetDescriptions returns every cached paramset description for a
// specific channel, keyed by paramset key.
func (c *ConfigurationCoordinator) GetAllParamsetDescriptions(
	iface hmenum.Interface,
	channelAddress string,
) map[hmenum.ParamsetKey]hmproto.Paramset {
	if c.paramsets == nil {
		return nil
	}
	return c.paramsets.GetChannelParamsetDescriptions(iface, channelAddress)
}

// GetConfigurableDevices returns every device that has at least one channel
// with configurable paramset parameters.
func (c *ConfigurationCoordinator) GetConfigurableDevices(iface hmenum.Interface) []ConfigurableDevice {
	channels := c.ConfigurableChannels(iface)
	if len(channels) == 0 {
		return nil
	}
	// Group channels by device address.
	byDevice := make(map[string]*ConfigurableDevice)
	for _, ch := range channels {
		dev, ok := byDevice[ch.DeviceAddress]
		if !ok {
			model := c.descriptions.GetModel(ch.DeviceAddress)
			dev = &ConfigurableDevice{
				Address:     ch.DeviceAddress,
				InterfaceID: string(iface),
				Model:       model,
			}
			byDevice[ch.DeviceAddress] = dev
		}
		// Find the paramset keys for this channel.
		psKeys := c.paramsets.GetParamsetKeys(iface, ch.ChannelAddress)
		dev.Channels = append(dev.Channels, ConfigurableDeviceChannel{
			Address:      ch.ChannelAddress,
			ChannelType:  ch.ChannelType,
			ParamsetKeys: psKeys,
		})
	}
	out := make([]ConfigurableDevice, 0, len(byDevice))
	for _, dev := range byDevice {
		out = append(out, *dev)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Address < out[j].Address })
	return out
}

// ConfigurableChannels returns every channel on iface whose MASTER
// paramset carries at least one parameter. Sorted by channel address
// for deterministic UI rendering.
func (c *ConfigurationCoordinator) ConfigurableChannels(
	iface hmenum.Interface,
) []ConfigurableChannel {
	if c.descriptions == nil || c.paramsets == nil {
		return nil
	}
	devs := c.descriptions.All(iface)
	out := make([]ConfigurableChannel, 0, len(devs))
	for i := range devs {
		d := devs[i]
		if !d.IsChannel() {
			continue
		}
		ps, ok := c.paramsets.Get(iface, d.Address, hmenum.ParamsetKeyMaster)
		if !ok || len(ps) == 0 {
			continue
		}
		out = append(out, ConfigurableChannel{
			DeviceAddress:  d.Parent,
			ChannelAddress: d.Address,
			ChannelType:    d.Type,
			ParamCount:     len(ps),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ChannelAddress < out[j].ChannelAddress })
	return out
}
