// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package coordinators

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

// CopyParamsetResult is the result of a copy-paramset operation.
type CopyParamsetResult struct {
	Success           bool
	Validated         bool
	ValidationErrors  map[string]string
	ParametersCopied  int
	ParametersSkipped int
}

// PutParamsetResult is the result of a put-paramset operation.
type PutParamsetResult struct {
	Success           bool
	ParametersWritten int
	ValidationErrors  map[string]string
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

// LiveParamsetReader is the south-bound contract for reading live
// paramset values from the CCU. Satisfied by ParamsetsDomain
// the adapter layer — not the registry (which holds cached descriptions
// only, not live values).
type LiveParamsetReader interface {
	GetParamset(ctx context.Context, channelAddress string, key hmenum.ParamsetKey) (map[string]any, error)
}

// LiveParamsetWriter is the south-bound contract for writing a
// paramset to the CCU.
type LiveParamsetWriter interface {
	PutParamset(ctx context.Context, channelAddress string, key hmenum.ParamsetKey, values map[string]any) error
}

// LinkParamsetDescriptionFetcher is the south-bound contract for fetching a
// LINK paramset description on demand. LINK descriptions are not cached during
// device discovery; they must be fetched directly when a caller needs to
// inspect or render direct-link configuration parameters.
type LinkParamsetDescriptionFetcher interface {
	GetLinkParamsetDescription(ctx context.Context, channelAddress string) (hmproto.Paramset, error)
}

// LinkParamsetDescription is the result of an on-demand LINK paramset
// description fetch. It carries the raw parameter map plus the channel
// address and paramset key that were used to fetch it.
type LinkParamsetDescription struct {
	ChannelAddress string
	ParamsetKey    hmenum.ParamsetKey
	Parameters     hmproto.Paramset
}

// GetParamset reads the live paramset values for a channel from the CCU via
// the supplied reader. It does not consult the registry — the registry holds
// cached descriptions, not current values.
//
// The reader is typically the InterfaceClient that backs the given interface.
// Passing a nil reader is an error.
func (c *ConfigurationCoordinator) GetParamset(
	ctx context.Context,
	channelAddress string,
	paramsetKey hmenum.ParamsetKey,
	reader LiveParamsetReader,
) (map[string]any, error) {
	if reader == nil {
		return nil, errors.New("configuration_coordinator: get_paramset: nil reader")
	}
	values, err := reader.GetParamset(ctx, channelAddress, paramsetKey)
	if err != nil {
		return nil, fmt.Errorf("configuration_coordinator: get_paramset %s/%s: %w", channelAddress, paramsetKey, err)
	}
	return values, nil
}

// PutParamset writes paramset values to the CCU via the supplied writer.
// When the registry holds a description for the channel, each value is
// checked against the parameter's min/max bounds (numeric) or value-list
// (enum) before the write is dispatched. Validation failures are collected
// per-parameter and returned in PutParamsetResult.ValidationErrors without
// sending any values to the CCU.
//
// Pass validate=false to skip the bounds check (e.g. operator emergency
// override). Validation reads only the registry's cached descriptions —
// no live CCU round-trip happens before the write.
func (c *ConfigurationCoordinator) PutParamset(
	ctx context.Context,
	iface hmenum.Interface,
	channelAddress string,
	paramsetKey hmenum.ParamsetKey,
	values map[string]any,
	validate bool,
	writer LiveParamsetWriter,
) (PutParamsetResult, error) {
	if writer == nil {
		return PutParamsetResult{}, errors.New("configuration_coordinator: put_paramset: nil writer")
	}

	if validate {
		desc, ok := c.GetChannelParamset(iface, channelAddress, paramsetKey)
		if ok && len(desc) > 0 {
			validationErrors := make(map[string]string)
			for param, value := range values {
				pd, exists := desc[param]
				if !exists {
					continue
				}
				if msg := validateParamValue(pd, value); msg != "" {
					validationErrors[param] = msg
				}
			}
			if len(validationErrors) > 0 {
				return PutParamsetResult{
					Success:          false,
					ValidationErrors: validationErrors,
				}, nil
			}
		}
	}

	if err := writer.PutParamset(ctx, channelAddress, paramsetKey, values); err != nil {
		return PutParamsetResult{
			Success: false,
		}, fmt.Errorf("configuration_coordinator: put_paramset %s/%s: %w", channelAddress, paramsetKey, err)
	}
	return PutParamsetResult{
		Success:           true,
		ParametersWritten: len(values),
	}, nil
}

// GetLinkParamsetDescription fetches the LINK paramset description for a
// channel on demand via the supplied fetcher. LINK descriptions are not
// persisted in the registry and must be retrieved directly from the CCU.
//
// Returns an error when the fetcher is nil or the RPC call fails.
func (c *ConfigurationCoordinator) GetLinkParamsetDescription(
	ctx context.Context,
	channelAddress string,
	fetcher LinkParamsetDescriptionFetcher,
) (LinkParamsetDescription, error) {
	if fetcher == nil {
		return LinkParamsetDescription{}, errors.New("configuration_coordinator: get_link_paramset_description: nil fetcher")
	}
	params, err := fetcher.GetLinkParamsetDescription(ctx, channelAddress)
	if err != nil {
		return LinkParamsetDescription{}, fmt.Errorf("configuration_coordinator: get_link_paramset_description %s: %w", channelAddress, err)
	}
	return LinkParamsetDescription{
		ChannelAddress: channelAddress,
		ParamsetKey:    hmenum.ParamsetKeyLink,
		Parameters:     params,
	}, nil
}

// validateParamValue checks a single value against its parameter descriptor.
// Returns an empty string when the value is valid, or a human-readable reason
// when it violates a constraint. Only basic numeric bounds and value-list
// membership are checked.
func validateParamValue(pd hmproto.ParameterData, value any) string {
	if pd.Type == hmenum.ParameterTypeEnum {
		if idx, ok := value.(int); ok {
			if len(pd.ValueList) > 0 && (idx < 0 || idx >= len(pd.ValueList)) {
				return fmt.Sprintf("value %d out of enum range [0, %d]", idx, len(pd.ValueList)-1)
			}
		}
		return ""
	}
	if pd.Type == hmenum.ParameterTypeInteger || pd.Type == hmenum.ParameterTypeFloat {
		switch v := value.(type) {
		case int:
			if len(pd.Min) > 0 {
				var minVal int
				if err := unmarshalJSON(pd.Min, &minVal); err == nil && v < minVal {
					return fmt.Sprintf("value %d below minimum %d", v, minVal)
				}
			}
			if len(pd.Max) > 0 {
				var maxVal int
				if err := unmarshalJSON(pd.Max, &maxVal); err == nil && v > maxVal {
					return fmt.Sprintf("value %d above maximum %d", v, maxVal)
				}
			}
		case float64:
			if len(pd.Min) > 0 {
				var minVal float64
				if err := unmarshalJSON(pd.Min, &minVal); err == nil && v < minVal {
					return fmt.Sprintf("value %g below minimum %g", v, minVal)
				}
			}
			if len(pd.Max) > 0 {
				var maxVal float64
				if err := unmarshalJSON(pd.Max, &maxVal); err == nil && v > maxVal {
					return fmt.Sprintf("value %g above maximum %g", v, maxVal)
				}
			}
		}
	}
	return ""
}

// CopyParamset copies writable paramset values from source channel to target
// channel. Both channels may belong to different interfaces within the same
// Unit.
//
// The copy filter is: only parameters that exist in the target description
// AND are writable (OPERATIONS & WRITE) are forwarded. Parameters missing
// from the target descriptor, or that are read-only or event-only, are
// silently skipped and counted in ParametersSkipped.
//
// Returns (result, oldValues, copiedValues, err). oldValues holds the
// target's paramset before the write (for change-tracking and audit logging).
// copiedValues holds the filtered parameters that were actually sent to the
// target. Both maps are empty on early-exit paths (nil reader/writer, no
// description, empty filtered set, or write error).
//
// The reader and writer typically point at the same ParamsetsDomain but are
// separated to make the method testable without a live CCU.
func (c *ConfigurationCoordinator) CopyParamset(
	ctx context.Context,
	srcIface hmenum.Interface,
	srcChannelAddress string,
	dstIface hmenum.Interface,
	dstChannelAddress string,
	paramsetKey hmenum.ParamsetKey,
	reader LiveParamsetReader,
	writer LiveParamsetWriter,
) (result CopyParamsetResult, srcValues, dstValues map[string]any, err error) {
	if reader == nil {
		return CopyParamsetResult{}, nil, nil, errors.New("configuration_coordinator: copy_paramset: nil reader")
	}
	if writer == nil {
		return CopyParamsetResult{}, nil, nil, errors.New("configuration_coordinator: copy_paramset: nil writer")
	}

	// 1. Read live source values.
	srcValues, err = reader.GetParamset(ctx, srcChannelAddress, paramsetKey)
	if err != nil {
		return CopyParamsetResult{}, nil, nil, fmt.Errorf("configuration_coordinator: copy_paramset: get source: %w", err)
	}

	// 2. Look up target description (from cached registry).
	dstDesc, ok := c.GetChannelParamset(dstIface, dstChannelAddress, paramsetKey)
	if !ok {
		// No description for target — nothing we can safely copy.
		return CopyParamsetResult{
			Success:           true,
			Validated:         false,
			ValidationErrors:  nil,
			ParametersCopied:  0,
			ParametersSkipped: len(srcValues),
		}, nil, nil, nil
	}

	// 3. Filter: only writable parameters that exist in target.
	filtered := make(map[string]any, len(srcValues))
	skipped := 0
	for param, value := range srcValues {
		paramDesc, exists := dstDesc[param]
		if !exists || !paramDesc.IsWritable() {
			skipped++
			continue
		}
		filtered[param] = value
	}

	if len(filtered) == 0 {
		return CopyParamsetResult{
			Success:           true,
			Validated:         false,
			ValidationErrors:  nil,
			ParametersCopied:  0,
			ParametersSkipped: skipped,
		}, nil, nil, nil
	}

	// 4. Read old target values for change-tracking / audit logging.
	oldValues, readErr := reader.GetParamset(ctx, dstChannelAddress, paramsetKey)
	if readErr != nil {
		// A failed pre-read must not abort the copy — log it as a
		// degraded change-tracking situation and continue.
		oldValues = nil
	}

	// 5. Write to target.
	if err := writer.PutParamset(ctx, dstChannelAddress, paramsetKey, filtered); err != nil {
		return CopyParamsetResult{
			Success:           false,
			Validated:         false,
			ValidationErrors:  nil,
			ParametersCopied:  0,
			ParametersSkipped: skipped,
		}, nil, nil, fmt.Errorf("configuration_coordinator: copy_paramset: put target: %w", err)
	}

	return CopyParamsetResult{
		Success:           true,
		Validated:         true,
		ValidationErrors:  nil,
		ParametersCopied:  len(filtered),
		ParametersSkipped: skipped,
	}, oldValues, filtered, nil
}

// unmarshalJSON is a thin wrapper around json.Unmarshal used by validateParamValue.
func unmarshalJSON(data json.RawMessage, v any) error {
	return json.Unmarshal(data, v)
}
