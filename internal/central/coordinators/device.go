// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package coordinators

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"runtime/debug"
	"slices"
	"strings"
	"sync"

	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/internal/central/registry"
	"github.com/SukramJ/openccu-loom/internal/observability"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// DeviceLister is the south-bound contract the coordinator calls during the
// initial pull / re-init pipeline. The CCU's XML-RPC `listDevices` returns
// both the top-level device entries and their child channels; the lister
// wraps that round-trip per interface.
type DeviceLister interface {
	ListDevices(ctx context.Context, iface hmtypes.WireInterfaceID) ([]hmproto.DeviceDescription, error)
}

// DeviceCoordinator reconciles device-level events against the
// [registry.DeviceRegistry] and [registry.DeviceDescriptionRegistry].
type DeviceCoordinator struct {
	centralName string
	bus         *events.Bus
	devices     *registry.DeviceRegistry
	descs       *registry.DeviceDescriptionRegistry
	paramsets   *registry.ParamsetRegistry
	logger      *slog.Logger
	recorder    observability.Recorder

	// mu guards delayedDescs, which stores device descriptions that
	// have been announced (newDevices callback) but not yet accepted.
	// They are keyed by interface → device address → descriptions.
	mu           sync.Mutex
	delayedDescs map[string]map[string][]hmproto.DeviceDescription

	// nameOverrideChecker is optional; when wired, RenameNewDeviceFromOverride
	// uses it to look up operator-configured device name overrides.
	nameOverrideChecker DeviceNameOverrideChecker

	// wg tracks background goroutines spawned by
	// [ScheduleParamsetConsistencyCheck] so [Stop] can wait for them to drain
	// during central shutdown instead of leaking a goroutine past the
	// coordinator's lifetime.
	wg sync.WaitGroup
}

// NewDeviceCoordinator wires the coordinator.
func NewDeviceCoordinator(
	centralName string,
	bus *events.Bus,
	devices *registry.DeviceRegistry,
	descs *registry.DeviceDescriptionRegistry,
	paramsets *registry.ParamsetRegistry,
	logger *slog.Logger,
) *DeviceCoordinator {
	if logger == nil {
		logger = slog.Default()
	}
	return &DeviceCoordinator{
		centralName:  centralName,
		bus:          bus,
		devices:      devices,
		descs:        descs,
		paramsets:    paramsets,
		logger:       logger,
		recorder:     observability.NoopRecorder{},
		delayedDescs: make(map[string]map[string][]hmproto.DeviceDescription),
	}
}

// SetRecorder rewires the observability recorder. Returns the receiver
// for chaining.
func (c *DeviceCoordinator) SetRecorder(rec observability.Recorder) *DeviceCoordinator {
	if rec == nil {
		rec = observability.NoopRecorder{}
	}
	c.recorder = rec
	return c
}

// Stop waits for every in-flight background goroutine spawned by
// [ScheduleParamsetConsistencyCheck] to finish. Call it during central
// shutdown so a consistency-check run in progress never outlives the
// coordinator.
func (c *DeviceCoordinator) Stop() {
	c.wg.Wait()
}

// PullReport summarises one InitialPull or RefreshAfterPair run. The
// counts are independent — a device that gained a new channel registers
// as one Updated entry, not as Created + Removed.
type PullReport struct {
	Interface hmtypes.WireInterfaceID
	// Created counts top-level device entries that were not previously
	// known to the registry.
	Created int
	// Updated counts existing entries whose description changed (model, firmware
	// revision, child channel count).
	Updated int
	// Removed counts entries the snapshot omits — the CCU no longer
	// serves them.
	Removed int
}

// InitialPull walks the lister for iface, ingests every description as if it
// were a fresh DeviceCreatedEvent, and emits DeviceRemovedEvents for entries
// the snapshot omits.
//
// The pipeline is intentionally idempotent: re-running it after a successful
// pull is a no-op (Created/Updated/Removed all 0). That invariant is the unit
// of work the contract test pins.
func (c *DeviceCoordinator) InitialPull(ctx context.Context, lister DeviceLister, iface hmtypes.WireInterfaceID) (PullReport, error) {
	return observability.InstrumentValue(ctx, c.recorder, "device_coordinator.initial_pull", observability.ScopeCoordinator,
		func(ctx context.Context) (PullReport, error) {
			rep := PullReport{Interface: iface}
			if lister == nil {
				return rep, errors.New("device_coordinator: lister is nil")
			}
			snapshot, err := lister.ListDevices(ctx, iface)
			if err != nil {
				return rep, fmt.Errorf("device_coordinator: list devices: %w", err)
			}
			c.applyPull(iface, snapshot, &rep)
			return rep, nil
		})
}

// applyPull merges snapshot into the registry. Pre-existing entries
// missing from the snapshot are dropped + emitted as DeviceRemovedEvent
// so subscribers (MQTT discovery retraction, the value-cache eviction and
// the WebSocket device-lifecycle plane) can react. It is NOT an audit
// source: nothing in the audit domain subscribes to the bus, so a device
// unpaired at the CCU leaves no change-log row.
func (c *DeviceCoordinator) applyPull(iface hmtypes.WireInterfaceID, snapshot []hmproto.DeviceDescription, rep *PullReport) {
	seen := make(map[string]struct{}, len(snapshot))
	for i := range snapshot {
		desc := snapshot[i]
		seen[desc.Address] = struct{}{}
		prev, existed := c.descs.Get(iface, desc.Address)
		c.descs.Put(iface, desc)
		if !desc.IsDevice() {
			if existed && !sameDescription(prev, desc) {
				rep.Updated++
			}
			continue
		}
		entry := registry.DeviceEntry{
			Interface: iface,
			Address:   desc.Address,
			Model:     desc.Type,
		}
		c.devices.Put(entry)
		switch {
		case !existed:
			rep.Created++
			events.Publish(c.bus, hmevent.DeviceCreatedEvent{
				Base:        hmevent.NewBase(),
				CentralName: c.centralName,
				InterfaceID: string(iface),
				Address:     desc.Address,
				Model:       desc.Type,
				Source:      hmenum.SourceOfDeviceCreationNew,
			})
		case !sameDescription(prev, desc):
			rep.Updated++
		}
	}
	// Anything the registry has but the snapshot drops is gone on the
	// CCU side — propagate the deletion through the same path
	// HandleDeleteDevices uses.
	allPrev := c.descs.All(iface)
	for i := range allPrev {
		addr := allPrev[i].Address
		if _, ok := seen[addr]; ok {
			continue
		}
		c.paramsets.DeleteChannel(iface, addr)
		c.descs.Delete(iface, addr)
		if c.devices.Remove(iface, addr) {
			rep.Removed++
			events.Publish(c.bus, hmevent.DeviceRemovedEvent{
				Base:        hmevent.NewBase(),
				CentralName: c.centralName,
				InterfaceID: string(iface),
				Address:     addr,
			})
		}
	}
}

// RefreshAfterPair re-pulls the snapshot after a fresh pair so the
// newly-added device's description and child channels land in the
// registry. The function is a thin wrapper over InitialPull (full
// re-pull is cheaper than a targeted single-device pull on the CCU
// Side and matches what
func (c *DeviceCoordinator) RefreshAfterPair(ctx context.Context, lister DeviceLister, iface hmtypes.WireInterfaceID) (PullReport, error) {
	return observability.InstrumentValue(ctx, c.recorder, "device_coordinator.refresh_after_pair", observability.ScopeCoordinator,
		func(ctx context.Context) (PullReport, error) {
			return c.InitialPull(ctx, lister, iface)
		})
}

// RefreshAfterUnpair drops the device + every channel under it and
// emits the matching removal event. Idempotent: dropping a missing
// address is a no-op (returns false).
func (c *DeviceCoordinator) RefreshAfterUnpair(ctx context.Context, iface hmtypes.WireInterfaceID, address string) bool {
	removed := false
	_ = observability.Instrument(ctx, c.recorder, "device_coordinator.refresh_after_unpair", observability.ScopeCoordinator,
		func(ctx context.Context) error {
			c.paramsets.DeleteChannel(iface, address)
			c.descs.Delete(iface, address)
			if c.devices.Remove(iface, address) {
				removed = true
				events.Publish(c.bus, hmevent.DeviceRemovedEvent{
					Base:        hmevent.NewBase(),
					CentralName: c.centralName,
					InterfaceID: string(iface),
					Address:     address,
				})
			}
			return nil
		})
	return removed
}

// sameDescription answers the "did this description change?" question
// the diff path needs. It is intentionally narrow — only the fields
// that drive downstream behaviour (model, firmware, parent, channel
// list) are compared. Addresses are equal by precondition.
func sameDescription(a, b hmproto.DeviceDescription) bool {
	if a.Type != b.Type {
		return false
	}
	if a.Firmware != b.Firmware {
		return false
	}
	if a.Parent != b.Parent {
		return false
	}
	if len(a.Children) != len(b.Children) {
		return false
	}
	for i := range a.Children {
		if a.Children[i] != b.Children[i] {
			return false
		}
	}
	return true
}

// HandleNewDevices ingests freshly-announced devices. It stores their
// normalised descriptions and emits a [hmevent.DeviceCreatedEvent] for
// each top-level device. After all descriptions are processed it emits
// a [hmevent.DataFetchCompletedEvent] so the cache coordinator marks
// its dirty bit and persists the updated state.
//
// Descriptions that are not yet present in the local cache are identified
// via IdentifyMissingDeviceDescriptions before storing — these correspond
// to factory-reset re-pair scenarios where a known device reappears with
// channel addresses the cache has never seen.
func (c *DeviceCoordinator) HandleNewDevices(_ context.Context, iface hmtypes.WireInterfaceID, descriptions []hmproto.DeviceDescription) {
	// Identify descriptions missing from the cache before storing them,
	// so factory-reset re-pair scenarios are detected correctly.
	missing := c.IdentifyMissingDeviceDescriptions(iface, descriptions)
	missingSet := make(map[string]struct{}, len(missing))
	for i := range missing {
		missingSet[missing[i].Address] = struct{}{}
	}
	c.ingestDescriptions(iface, descriptions, func(address string) hmenum.SourceOfDeviceCreation {
		if _, wasMissing := missingSet[address]; wasMissing {
			// Device address was unknown to the cache: factory-reset
			// re-pair path. Use Refresh source so north-bound adapters
			// can distinguish re-pairs from genuine new devices.
			return hmenum.SourceOfDeviceCreationRefresh
		}
		return hmenum.SourceOfDeviceCreationNew
	})
}

// ingestDescriptions stores the descriptions, registers every top-level
// device and publishes one [hmevent.DeviceCreatedEvent] per device with
// the source the caller derives per address. It carries the shared body
// of [DeviceCoordinator.HandleNewDevices] and
// [DeviceCoordinator.HandleAcceptedDevices].
func (c *DeviceCoordinator) ingestDescriptions(
	iface hmtypes.WireInterfaceID,
	descriptions []hmproto.DeviceDescription,
	sourceOf func(address string) hmenum.SourceOfDeviceCreation,
) {
	deviceCount := 0
	for i := range descriptions {
		desc := descriptions[i]
		c.descs.Put(iface, desc)
		if desc.IsDevice() {
			entry := registry.DeviceEntry{
				Interface: iface,
				Address:   desc.Address,
				Model:     desc.Type,
			}
			c.devices.Put(entry)
			events.Publish(c.bus, hmevent.DeviceCreatedEvent{
				Base:        hmevent.NewBase(),
				CentralName: c.centralName,
				InterfaceID: string(iface),
				Address:     desc.Address,
				Model:       desc.Type,
				Source:      sourceOf(desc.Address),
			})
			deviceCount++
		}
	}
	if len(descriptions) > 0 {
		events.Publish(c.bus, hmevent.DataFetchCompletedEvent{
			Base:        hmevent.NewBase(),
			CentralName: c.centralName,
			InterfaceID: string(iface),
			Operation:   "new_devices",
			Count:       deviceCount,
			Success:     true,
		})
	}
}

// IdentifyMissingDeviceDescriptions returns the subset of descs whose ADDRESS
// is not present in the local description cache for iface. This is useful for
// detecting factory-reset re-pair scenarios: a device with a known parent
// address may arrive with entirely new channel addresses that were never
// stored in the cache.
//
// Unlike CheckForNewDeviceAddresses (which checks the top-level device
// registry), this method checks the description cache directly so both
// device-level and channel-level addresses are covered.
func (c *DeviceCoordinator) IdentifyMissingDeviceDescriptions(iface hmtypes.WireInterfaceID, descs []hmproto.DeviceDescription) []hmproto.DeviceDescription {
	all := c.descs.All(iface)
	known := make(map[string]struct{}, len(all))
	for i := range all {
		known[all[i].Address] = struct{}{}
	}
	out := make([]hmproto.DeviceDescription, 0)
	for i := range descs {
		if _, ok := known[descs[i].Address]; !ok {
			out = append(out, descs[i])
		}
	}
	return out
}

// CheckForNewDeviceAddresses compares a fresh wire-side `listDevices`
// snapshot against the in-memory registry and returns addresses that the CCU
// reports but the registry has not yet seen.
//
// The returned slice contains both top-level device addresses and child
// channel addresses. Order matches the wire snapshot.
func (c *DeviceCoordinator) CheckForNewDeviceAddresses(iface hmtypes.WireInterfaceID, snapshot []hmproto.DeviceDescription) []string {
	allKnown := c.descs.All(iface)
	known := make(map[string]struct{}, len(allKnown))
	for i := range allKnown {
		known[allKnown[i].Address] = struct{}{}
	}
	out := make([]string, 0)
	for i := range snapshot {
		if _, ok := known[snapshot[i].Address]; ok {
			continue
		}
		out = append(out, snapshot[i].Address)
	}
	return out
}

// isVirtualRemoteType reports whether deviceType identifies a CCU virtual-
// remote device. The virtual-remote channel receives click events that should
// not be routed to a real device.
//
// Three model families are recognised across the three bus protocols:
// BidCos-RF (HM-RCV-50), BidCos-Wired (HMW-RCV-50), and HmIP-RF
// (HmIP-RCV-50). All three must be matched so that GetVirtualRemoteAddresses
// returns the correct set for Wired-Bus and IP-RF interfaces.
func isVirtualRemoteType(deviceType string) bool {
	return strings.HasPrefix(deviceType, "HM-RCV-") ||
		strings.HasPrefix(deviceType, "HMW-RCV-") ||
		strings.HasPrefix(deviceType, "HmIP-RCV-")
}

// VirtualRemoteEntry carries the address and child-channel list for one
// virtual-remote device. Mirrors the richer return type that
// Get_virtual_remotes returns
// (DeviceProtocol objects).
type VirtualRemoteEntry struct {
	// Address is the top-level device address (no channel suffix).
	Address string
	// DeviceType is the CCU device type (e.g. "HM-RCV-50").
	DeviceType string
	// ChannelAddresses contains the child channel addresses reported by
	// the CCU (e.g. ["VRT0001:1", "VRT0001:2"]).
	ChannelAddresses []string
}

// GetVirtualRemoteAddresses returns the top-level device addresses of
// all virtual-remote devices registered for iface. Returns nil when
// there are none. This is the original string-slice variant preserved
// for backward compatibility; new code should prefer [GetVirtualRemotes].
func (c *DeviceCoordinator) GetVirtualRemoteAddresses(iface hmtypes.WireInterfaceID) []string {
	all := c.descs.All(iface)
	var out []string
	for i := range all {
		d := all[i]
		if !strings.Contains(d.Address, ":") && isVirtualRemoteType(d.Type) {
			out = append(out, d.Address)
		}
	}
	return out
}

// GetVirtualRemotes returns enriched [VirtualRemoteEntry] values for every
// virtual-remote device registered for iface. Returns nil when there are
// none.
func (c *DeviceCoordinator) GetVirtualRemotes(iface hmtypes.WireInterfaceID) []VirtualRemoteEntry {
	all := c.descs.All(iface)
	var out []VirtualRemoteEntry
	for i := range all {
		d := all[i]
		if strings.Contains(d.Address, ":") || !isVirtualRemoteType(d.Type) {
			continue
		}
		entry := VirtualRemoteEntry{
			Address:          d.Address,
			DeviceType:       d.Type,
			ChannelAddresses: append([]string(nil), d.Children...),
		}
		out = append(out, entry)
	}
	return out
}

// IdentifyChannel performs a simple substring-match lookup over all channel
// addresses for iface and returns the first address that contains text.
// Returns ("", false) when text is empty or no match is found.
func (c *DeviceCoordinator) IdentifyChannel(iface hmtypes.WireInterfaceID, text string) (string, bool) {
	if text == "" {
		return "", false
	}
	all := c.descs.All(iface)
	for i := range all {
		if strings.Contains(all[i].Address, text) {
			return all[i].Address, true
		}
	}
	return "", false
}

// DeleteDevice removes a single device and all its child channel addresses.
// The device is located by its top-level address; child channel addresses are
// derived from the description registry and combined into one
// HandleDeleteDevices call.
func (c *DeviceCoordinator) DeleteDevice(ctx context.Context, iface hmtypes.WireInterfaceID, deviceAddress string) {
	// Collect device address + all child channel addresses.
	allDescs := c.descs.All(iface)
	addresses := make([]string, 0, len(allDescs)+1)
	addresses = append(addresses, deviceAddress)
	for i := range allDescs {
		d := allDescs[i]
		if d.Parent == deviceAddress && strings.Contains(d.Address, ":") {
			addresses = append(addresses, d.Address)
		}
	}
	c.HandleDeleteDevices(ctx, iface, addresses)
}

// HandleDeleteDevices removes records for the given addresses.
func (c *DeviceCoordinator) HandleDeleteDevices(_ context.Context, iface hmtypes.WireInterfaceID, addresses []string) {
	for _, addr := range addresses {
		c.paramsets.DeleteChannel(iface, addr)
		c.descs.Delete(iface, addr)
		if c.devices.Remove(iface, addr) {
			events.Publish(c.bus, hmevent.DeviceRemovedEvent{
				Base:        hmevent.NewBase(),
				CentralName: c.centralName,
				InterfaceID: string(iface),
				Address:     addr,
			})
		}
	}
}

// CheckAndCreateDevicesFromCache materialises devices from the persisted
// description cache. This is the cache-based restart path: after a reboot the
// descriptions are already in the DeviceDescriptionRegistry (loaded from
// store), so we can create Device objects without issuing a new ListDevices
// pull to the CCU.
//
// Concurrency: c.mu serialises this method against itself and against
// [HandleNewDevices] — the two production entry points that turn a
// known-but-not-yet-materialised address into a device-registry entry (this
// method for the cache-based restart path, HandleNewDevices for the live
// `newDevices` callback). It does NOT cover every device-mutation method on
// this coordinator: applyPull/[InitialPull]/[RefreshAfterPair],
// [HandleDeleteDevices], [RefreshDeviceDescriptionsAndCreateMissingDevices],
// [ReplaceDevice], [ReaddDevice] and [RefreshFirmwareData] all read/write the
// same [registry.DeviceRegistry] / [registry.DeviceDescriptionRegistry]
// without taking c.mu (see their own doc comments) and are not used
// concurrently with this method in production today. The registries
// themselves are internally thread-safe for individual Get/Put/Remove/Has
// calls, so no call ever corrupts state; the residual risk of a race between
// this method and one of the uncovered paths is a benign duplicate
// [hmevent.DeviceCreatedEvent] for the same address — subscribers must treat
// the event as at-least-once, not exactly-once.
//
// New-device events are collected while c.mu is held and published only
// after it is released, so a subscriber that calls back into the coordinator
// (e.g. [RenameNewDeviceFromOverride]) cannot deadlock against this method —
// [events.Publish] dispatches every handler synchronously on the calling
// goroutine.
func (c *DeviceCoordinator) CheckAndCreateDevicesFromCache(ctx context.Context) error {
	type newDeviceEntry struct {
		iface hmtypes.WireInterfaceID
		addr  string
		model string
	}
	var newlyCreated []newDeviceEntry

	c.mu.Lock()
	// Walk all interfaces tracked in the description registry.
	for _, iface := range c.descs.GetInterfaceIDs() {
		for _, addr := range c.descs.GetAddresses(iface) {
			// Only top-level devices (no colon → not a channel).
			if strings.Contains(addr, ":") {
				continue
			}
			// Already in the device registry? Skip.
			if c.devices.Has(iface, addr) {
				continue
			}
			desc, ok := c.descs.Get(iface, addr)
			if !ok {
				continue
			}
			entry := registry.DeviceEntry{
				Interface: iface,
				Address:   addr,
				Model:     desc.Type,
			}
			c.devices.Put(entry)
			newlyCreated = append(newlyCreated, newDeviceEntry{iface: iface, addr: addr, model: desc.Type})
		}
	}
	c.mu.Unlock()

	for _, nd := range newlyCreated {
		events.Publish(c.bus, hmevent.DeviceCreatedEvent{
			Base:        hmevent.NewBase(),
			CentralName: c.centralName,
			InterfaceID: string(nd.iface),
			Address:     nd.addr,
			Model:       nd.model,
			Source:      hmenum.SourceOfDeviceCreationCache,
		})
	}
	return nil
}

// DeviceDescriptionFetcher is the south-bound contract the coordinator
// uses during RefreshDeviceDescriptionsAndCreateMissingDevices to pull
// fresh descriptions from the CCU.
type DeviceDescriptionFetcher interface {
	// ListDevices returns all device and channel descriptions for iface.
	ListDevices(ctx context.Context, iface hmtypes.WireInterfaceID) ([]hmproto.DeviceDescription, error)
}

// RefreshDeviceDescriptionsAndCreateMissingDevices re-pulls all descriptions
// from the CCU for iface, updates the registry, and creates Device entries
// for any address that is now in the description registry but not yet in the
// device registry.
//
// This is the post-reconnect path: after a circuit-breaker recovery the
// daemon may have missed newDevices callbacks while offline. Calling this
// method closes the gap.
func (c *DeviceCoordinator) RefreshDeviceDescriptionsAndCreateMissingDevices(
	ctx context.Context,
	fetcher DeviceDescriptionFetcher,
	iface hmtypes.WireInterfaceID,
) error {
	if fetcher == nil {
		return errors.New("device_coordinator: refresh_device_descriptions: fetcher is nil")
	}
	descs, err := fetcher.ListDevices(ctx, iface)
	if err != nil {
		return fmt.Errorf("device_coordinator: refresh_device_descriptions: list devices: %w", err)
	}
	// Store the refreshed descriptions.
	for i := range descs {
		c.descs.Put(iface, descs[i])
	}
	// Create Device entries for any newly-known top-level devices.
	for i := range descs {
		d := descs[i]
		if !d.IsDevice() {
			continue
		}
		if c.devices.Has(iface, d.Address) {
			continue
		}
		entry := registry.DeviceEntry{
			Interface: iface,
			Address:   d.Address,
			Model:     d.Type,
		}
		c.devices.Put(entry)
		events.Publish(c.bus, hmevent.DeviceCreatedEvent{
			Base:        hmevent.NewBase(),
			CentralName: c.centralName,
			InterfaceID: string(iface),
			Address:     d.Address,
			Model:       d.Type,
			Source:      hmenum.SourceOfDeviceCreationRefresh,
		})
	}
	return nil
}

// ChannelParamsetFetcher is the south-bound contract used by
// [DeviceCoordinator.ReloadChannelConfig] to re-pull a single channel's
// paramset descriptions and current MASTER values from the CCU.
type ChannelParamsetFetcher interface {
	// GetParamsetDescription reads the descriptor for one paramset on the
	// given channel (VALUES / MASTER / LINK).
	GetParamsetDescription(ctx context.Context, channelAddress string, key hmenum.ParamsetKey) (map[string]hmproto.ParameterData, error)
	// GetParamset reads the current values of one paramset on the channel.
	GetParamset(ctx context.Context, channelAddress string, key hmenum.ParamsetKey) (map[string]any, error)
}

// channelReloadParamsetKeys lists the paramset descriptions re-pulled for a
// channel reload. Mirrors the channel-config refresh in
// model/device.py:1448 (reload_channel_config → on_config_changed →
// _reload_paramset_descriptions): VALUES, MASTER and LINK.
var channelReloadParamsetKeys = []hmenum.ParamsetKey{
	hmenum.ParamsetKeyValues,
	hmenum.ParamsetKeyMaster,
	hmenum.ParamsetKeyLink,
}

// ReloadChannelConfig re-pulls the paramset descriptions (VALUES, MASTER,
// LINK) for a single channel from the CCU, re-stores them in the paramset
// registry (applying device-type patches), and re-reads the channel's
// current MASTER values. It mirrors model/device.py:1448
// (Channel.reload_channel_config → on_config_changed), scoped to one
// channel instead of a whole device.
//
// deviceModel carries the device TYPE field so the paramset patches match
// (see [registry.ParamsetRegistry.Add]); pass "" when the model is unknown.
//
// A missing paramset on the channel (e.g. a channel without a LINK set) is
// logged and skipped, not treated as a fatal error — only a wholesale fetch
// failure aborts. MASTER values are read after the descriptions so the
// caller's subsequent data-point refresh sees fresh values.
func (c *DeviceCoordinator) ReloadChannelConfig(
	ctx context.Context,
	fetcher ChannelParamsetFetcher,
	iface hmtypes.WireInterfaceID,
	channelAddress string,
	deviceModel string,
) error {
	if fetcher == nil {
		return errors.New("device_coordinator: reload_channel_config: fetcher is nil")
	}
	if channelAddress == "" {
		return errors.New("device_coordinator: reload_channel_config: empty channel address")
	}

	var fetched int
	for _, key := range channelReloadParamsetKeys {
		desc, err := fetcher.GetParamsetDescription(ctx, channelAddress, key)
		if err != nil {
			c.logger.Debug("reload_channel_config: paramset description skipped",
				"channel", channelAddress,
				"paramset", string(key),
				"error", err)
			continue
		}
		ps := make(hmproto.Paramset, len(desc))
		for name := range desc {
			ps[name] = desc[name]
		}
		if c.paramsets != nil {
			c.paramsets.Add(iface, channelAddress, key, ps, deviceModel)
		}
		fetched++
	}
	if fetched == 0 {
		return fmt.Errorf("device_coordinator: reload_channel_config: no paramset descriptions for %s", channelAddress)
	}

	// Re-read the current MASTER values so a follow-up data-point refresh
	// observes fresh master state. A read failure here is non-fatal — the
	// descriptions were already refreshed.
	if _, err := fetcher.GetParamset(ctx, channelAddress, hmenum.ParamsetKeyMaster); err != nil {
		c.logger.Debug("reload_channel_config: master values read skipped",
			"channel", channelAddress,
			"error", err)
	}
	return nil
}

// PendingDevice identifies one device parked in the deferred-creation
// queue: announced over a newDevices callback while
// `delay_new_device_creation` is enabled, waiting for an operator to
// accept it.
//
// loom:reachable:reason="element type of PendingDevices(), which PublishPendingDevices calls from the newDevices callback to populate the operator inbox surface; a method-less data struct the analyzer's type heuristic (reachable only via its methods) cannot see used"
type PendingDevice struct {
	// Interface is the canonical wire interface id the device was
	// announced on.
	Interface hmtypes.WireInterfaceID
	// Address is the top-level device address.
	Address string
	// Model is the device type the CCU reported.
	Model string
}

// PendingDevices returns a snapshot of the deferred-creation queue,
// sorted by interface and address, so the north-bound inbox surface can
// show what is waiting for an operator. Only top-level devices are
// listed; their channels stay in the queue and are materialised
// together with the device on accept.
func (c *DeviceCoordinator) PendingDevices() []PendingDevice {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []PendingDevice
	for ifaceID, byAddress := range c.delayedDescs {
		iface := hmtypes.WireInterfaceID(ifaceID)
		for address, descs := range byAddress {
			model := ""
			for i := range descs {
				if descs[i].Address == address && descs[i].IsDevice() {
					model = descs[i].Type
					break
				}
			}
			if model == "" && c.descs != nil {
				// A known device re-announcing new channels parks no
				// device-level description; the cached one carries the model.
				if cached, ok := c.descs.Get(iface, address); ok {
					model = cached.Type
				}
			}
			out = append(out, PendingDevice{Interface: iface, Address: address, Model: model})
		}
	}
	slices.SortFunc(out, func(a, b PendingDevice) int {
		if a.Interface != b.Interface {
			return strings.Compare(string(a.Interface), string(b.Interface))
		}
		return strings.Compare(a.Address, b.Address)
	})
	return out
}

// TakeDelayedDeviceDescriptions removes the parked descriptions of one
// device from the deferred-creation queue and returns them, so the
// caller can run the same materialisation the immediate path runs. A
// device that is not (or no longer) parked yields nil, which is how a
// caller tells "nothing to accept here" from "accepted".
//
// The descriptions are handed out, not registered: registration and the
// DeviceCreatedEvent happen in [DeviceCoordinator.HandleAcceptedDevices]
// after the device has been materialised, so a north-bound subscriber
// resolves the device in the model when the event fires.
func (c *DeviceCoordinator) TakeDelayedDeviceDescriptions(
	iface hmtypes.WireInterfaceID, address string,
) []hmproto.DeviceDescription {
	ifaceID := string(iface)
	c.mu.Lock()
	defer c.mu.Unlock()
	byAddress, ok := c.delayedDescs[ifaceID]
	if !ok {
		return nil
	}
	descs, found := byAddress[address]
	if !found {
		return nil
	}
	delete(byAddress, address)
	if len(byAddress) == 0 {
		delete(c.delayedDescs, ifaceID)
	}
	return descs
}

// HandleAcceptedDevices performs the registry bookkeeping and event
// publication for devices an operator accepted out of the
// deferred-creation queue. It is [DeviceCoordinator.HandleNewDevices]
// with the creation source pinned to MANUAL, which is what north-bound
// consumers use to tell an operator-driven accept from a hot-plug.
func (c *DeviceCoordinator) HandleAcceptedDevices(iface hmtypes.WireInterfaceID, descriptions []hmproto.DeviceDescription) {
	c.ingestDescriptions(iface, descriptions, func(string) hmenum.SourceOfDeviceCreation {
		return hmenum.SourceOfDeviceCreationManual
	})
}

// maxDelayedDevicesPerInterface bounds the number of distinct devices the
// pending-accept inbox holds per interface. A CCU fleet is a few hundred
// devices in total, so the cap is far above any real installation; it exists
// because the callback listener accepts newDevices announcements without
// authentication, and every entry stays until an operator accepts it.
const maxDelayedDevicesPerInterface = 1024

// StoreDelayedDeviceDescriptions stores device descriptions that have been
// received via a newDevices callback but not yet manually accepted. The
// descriptions are keyed by device address so the accept flow can look
// them up later.
//
// The store is idempotent per description address: a re-announcement replaces
// the pending description instead of stacking a second copy on top of it.
// This matters because the daemon answers listDevices with an empty array, so
// the CCU re-announces its complete inventory after every reconnect — an
// append-only inbox grew by another full copy of the fleet each time and
// nothing but the manual-accept flow ever removed anything.
//
// A description that adds nothing — its device exists and the description
// itself is already cached — is skipped for the same reason: the
// re-announcement after a reconnect covers the whole fleet, and parking it
// would present every device in the installation to the operator as "waiting
// for approval". A known device announcing an address the cache has never
// seen (the factory-reset re-pair) is still parked: that one does need an
// operator decision.
func (c *DeviceCoordinator) StoreDelayedDeviceDescriptions(iface hmtypes.WireInterfaceID, descriptions []hmproto.DeviceDescription) {
	ifaceID := string(iface)
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.delayedDescs[ifaceID]; !ok {
		c.delayedDescs[ifaceID] = make(map[string][]hmproto.DeviceDescription)
	}
	pending := c.delayedDescs[ifaceID]
	for i := range descriptions {
		d := descriptions[i]
		// Key by the top-level device address (PARENT or ADDRESS itself).
		key := d.Parent
		if key == "" {
			key = d.Address
		}
		if c.devices != nil && c.devices.Has(iface, key) && c.descs != nil {
			if _, cached := c.descs.Get(iface, d.Address); cached {
				continue
			}
		}
		entry, known := pending[key]
		if !known && len(pending) >= maxDelayedDevicesPerInterface {
			c.logger.Warn("StoreDelayedDeviceDescriptions: pending-accept inbox is full",
				"interface", ifaceID,
				"address", key,
				"pending", len(pending))
			continue
		}
		if idx := slices.IndexFunc(entry, func(e hmproto.DeviceDescription) bool {
			return e.Address == d.Address
		}); idx >= 0 {
			entry[idx] = d
			continue
		}
		pending[key] = append(entry, d)
	}
	if len(pending) == 0 {
		delete(c.delayedDescs, ifaceID)
	}
}

// ParamsetConsistencyChecker is the south-bound contract used by
// CheckParamsetConsistency to read live paramset values from the CCU.
type ParamsetConsistencyChecker interface {
	GetParamset(ctx context.Context, channelAddress string, key hmenum.ParamsetKey) (map[string]any, error)
}

// ParamsetInconsistency records a detected mismatch between the
// persisted paramset description and the live CCU values for a device.
type ParamsetInconsistency struct {
	// DeviceAddress is the top-level device address.
	DeviceAddress string
	// InterfaceID is the CCU interface this inconsistency was found on.
	InterfaceID string
	// MissingParameters is the list of "channelAddress:parameterName"
	// pairs that appear in the description but are absent from the live
	// CCU paramset. This is the symptom of the HmIPServer stale-files
	// bug after a firmware update.
	MissingParameters []string
}

// CheckParamsetConsistency verifies that the persisted MASTER paramset
// descriptions match the live values returned by the CCU for the given device
// addresses. Mismatches indicate stale descriptor files on the HmIPServer
// (crRFD) — a known firmware-update side-effect. Detected inconsistencies are
// returned so the caller can record incidents publish integration issues.
//
// Only MASTER paramsets are checked; VALUES are volatile. Only HmIP devices
// are checked because the stale-files bug is HmIPServer-specific.
//
// The comparison is driven entirely by the paramset registry: it supplies both
// the channels to visit and the description each is measured against. That
// keeps the check independent of when the device descriptions arrive, which is
// a different point in the boot for a first-ever start (CCU callback, after
// init) than for a restart with a populated store (hydration, before wiring).
//
// The two interface arguments live in different identifier spaces and both are
// required: iface is the BARE interface the CCU names, which decides whether
// this is the HmIP service at all, while ifaceKey is the canonical
// `<central>-<iface>` wire id the description and paramset registries are
// keyed by. Passing the bare interface for both made every registry lookup
// miss on a named central, so the check reported a clean bill of health for a
// device it never looked at.
func (c *DeviceCoordinator) CheckParamsetConsistency(
	ctx context.Context,
	iface hmenum.Interface,
	ifaceKey hmtypes.WireInterfaceID,
	deviceAddresses []string,
	checker ParamsetConsistencyChecker,
) ([]ParamsetInconsistency, error) {
	if checker == nil {
		return nil, errors.New("device_coordinator: check_paramset_consistency: nil checker")
	}

	var result []ParamsetInconsistency
	ifaceID := string(ifaceKey)

	for _, deviceAddr := range deviceAddresses {
		// Only HmIP devices are affected by the HmIPServer stale-files bug.
		// Both HmIP-RF and HmIP-Wired devices share the HmIP-RF service on
		// the CCU, so a single interface check covers both flavours.
		if iface != hmenum.InterfaceHmIPRF {
			continue
		}

		// Collect the device's channels from the paramset registry rather
		// than from the device descriptions. Both hold the same channels once
		// the daemon has been running for a while, but only the paramset
		// registry holds them at the moment this check is scheduled: the
		// device-hydration pass fills it as it fetches each description,
		// whereas the description registry is filled by the CCU's newDevices
		// callback, which only arrives after init() — several steps later on a
		// first-ever boot, and never before the check on that boot. Reading
		// the wrong one made the whole check silently do nothing there.
		//
		// It is also the registry the check is actually about: a channel
		// without a cached MASTER description is skipped below anyway, so the
		// set of channels that can produce a finding is exactly the set the
		// paramset registry knows.
		var channelAddresses []string
		for _, chAddr := range c.paramsets.GetChannelAddressesByParamsetKey(ifaceKey, deviceAddr)[hmenum.ParamsetKeyMaster] {
			// Device-level paramsets (address without a channel suffix) stay
			// out of scope: the stale-files symptom is reported per channel.
			if strings.Contains(chAddr, ":") {
				channelAddresses = append(channelAddresses, chAddr)
			}
		}

		var missingForDevice []string
		for _, chAddr := range channelAddresses {
			// Get cached MASTER paramset description.
			masterDesc, ok := c.paramsets.Get(ifaceKey, chAddr, hmenum.ParamsetKeyMaster)
			if !ok || len(masterDesc) == 0 {
				continue
			}

			// Filter to parameters with operations > 0 (visible/writable).
			expectedParams := make(map[string]struct{})
			for name := range masterDesc {
				if masterDesc[name].Operations > 0 {
					expectedParams[name] = struct{}{}
				}
			}
			if len(expectedParams) == 0 {
				continue
			}

			// Fetch live paramset — skip channel on any transport error.
			actualValues, err := checker.GetParamset(ctx, chAddr, hmenum.ParamsetKeyMaster)
			if err != nil {
				c.logger.Debug("CheckParamsetConsistency: skipping channel on fetch error",
					"channel", chAddr, "error", err)
				continue
			}

			// Detect missing parameters.
			for param := range expectedParams {
				if _, exists := actualValues[param]; !exists {
					missingForDevice = append(missingForDevice, chAddr+":"+param)
				}
			}
		}

		if len(missingForDevice) > 0 {
			c.logger.Warn("CheckParamsetConsistency: stale paramset detected",
				"device", deviceAddr, "interface", ifaceID,
				"missing_count", len(missingForDevice))
			result = append(result, ParamsetInconsistency{
				DeviceAddress:     deviceAddr,
				InterfaceID:       ifaceID,
				MissingParameters: missingForDevice,
			})
		}
	}
	return result, nil
}

// ReaddDevice handles re-pairing of a known device that was put into
// learn-mode while installation mode was active. The device may have changed
// parameters, so for each address: 1. The existing description/paramset cache
// entries are removed. 2. The device is dropped from the registry
// (DeviceRemovedEvent fires). 3.
// RefreshDeviceDescriptionsAndCreateMissingDevices is called to recreate the
// device with up-to-date descriptions.
//
// Non-fatal errors (fetch failure for one address) are logged and skipped.
func (c *DeviceCoordinator) ReaddDevice(
	ctx context.Context,
	fetcher DeviceDescriptionFetcher,
	iface hmtypes.WireInterfaceID,
	deviceAddresses []string,
) error {
	if fetcher == nil {
		return errors.New("device_coordinator: readd_device: fetcher is nil")
	}
	for _, addr := range deviceAddresses {
		// Evict caches for this device.
		allDescs := c.descs.All(iface)
		for i := range allDescs {
			if allDescs[i].Address == addr || allDescs[i].Parent == addr {
				c.paramsets.DeleteChannel(iface, allDescs[i].Address)
				c.descs.Delete(iface, allDescs[i].Address)
			}
		}
		// Remove device from registry — DeviceRemovedEvent fires so the
		// cache coordinator evicts value-cache entries.
		if c.devices.Remove(iface, addr) {
			events.Publish(c.bus, hmevent.DeviceRemovedEvent{
				Base:        hmevent.NewBase(),
				CentralName: c.centralName,
				InterfaceID: string(iface),
				Address:     addr,
			})
		}
	}
	// Recreate devices from fresh descriptions.
	if err := c.RefreshDeviceDescriptionsAndCreateMissingDevices(ctx, fetcher, iface); err != nil {
		return fmt.Errorf("device_coordinator: readd_device: refresh: %w", err)
	}
	return nil
}

// LinkPeerFetcher is the south-bound contract used by
// RefreshDeviceLinkPeers to re-fetch link peer addresses from the CCU
// for each channel of a device.
//
// The InterfaceClient implements this via its GetLinkPeers method.
type LinkPeerFetcher interface {
	// GetLinkPeers returns the peer channel addresses linked to
	// channelAddress on the given interface. Returns an empty slice
	// (not an error) when no peers are configured.
	GetLinkPeers(ctx context.Context, iface hmtypes.WireInterfaceID, channelAddress string) ([]string, error)
}

// RefreshDeviceLinkPeers re-fetches the link peer addresses for every channel
// of the given device and publishes a [hmevent.LinkPeerChangedEvent] for each
// channel that has at least one peer. This is triggered by the CCU's
// `updateDevice` callback with hint=LINKS (link partner change).
//
// Non-fatal errors (CCU unreachable for one channel) are logged and skipped
// so a single bad channel does not abort the whole refresh.
func (c *DeviceCoordinator) RefreshDeviceLinkPeers(
	ctx context.Context,
	fetcher LinkPeerFetcher,
	iface hmtypes.WireInterfaceID,
	deviceAddress string,
) {
	if fetcher == nil {
		c.logger.Debug("RefreshDeviceLinkPeers: nil fetcher, skipping",
			"interface", string(iface), "device", deviceAddress)
		return
	}
	if !c.devices.Has(iface, deviceAddress) {
		c.logger.Debug("RefreshDeviceLinkPeers: device not in registry",
			"interface", string(iface), "device", deviceAddress)
		return
	}

	// Walk all channel descriptions for this device. We use All + filter
	// by Parent so the result does not depend on the Children list being
	// populated on the device description (it may be absent when the
	// description came from the cache).
	allDescs := c.descs.All(iface)
	for i := range allDescs {
		if allDescs[i].Parent != deviceAddress {
			continue
		}
		channelAddr := allDescs[i].Address
		peers, err := fetcher.GetLinkPeers(ctx, iface, channelAddr)
		if err != nil {
			c.logger.Debug("RefreshDeviceLinkPeers: GetLinkPeers error",
				"interface", string(iface),
				"channel", channelAddr,
				"error", err)
			continue
		}
		if len(peers) == 0 {
			continue
		}
		events.Publish(c.bus, hmevent.LinkPeerChangedEvent{
			Base:        hmevent.NewBase(),
			CentralName: c.centralName,
			Address:     channelAddr,
			Peers:       peers,
		})
	}
}

// FirmwareStateReader is the south-bound contract used by
// RefreshFirmwareDataByState to query the current firmware-update state
// for all devices on an interface.
//
// The adapter layer implements this by reading from the domain model
// registry.
type FirmwareStateReader interface {
	// DeviceFirmwareStates returns a map of device address →
	// DeviceFirmwareState for all devices on iface. Addresses missing
	// from the map are treated as DeviceFirmwareStateUnknown.
	DeviceFirmwareStates(iface hmtypes.WireInterfaceID) map[string]hmenum.DeviceFirmwareState
}

// RefreshFirmwareDataByState filters all known devices on iface to
// those whose firmware-update state is in the given set, then calls
// RefreshDeviceDescriptionsAndCreateMissingDevices for each matching
// device so the daemon's view of the device's description is
// up-to-date.
//
// This is called by the scheduler jobs for the
// DELIVER_FIRMWARE_IMAGE and PERFORMING_UPDATE state groups.
//
// Non-fatal errors per device are logged and skipped.
func (c *DeviceCoordinator) RefreshFirmwareDataByState(
	ctx context.Context,
	fetcher DeviceDescriptionFetcher,
	stateReader FirmwareStateReader,
	iface hmtypes.WireInterfaceID,
	states []hmenum.DeviceFirmwareState,
) error {
	if fetcher == nil {
		return errors.New("device_coordinator: refresh_firmware_data_by_state: nil fetcher")
	}
	if stateReader == nil || len(states) == 0 {
		return nil
	}

	stateSet := make(map[hmenum.DeviceFirmwareState]struct{}, len(states))
	for _, s := range states {
		stateSet[s] = struct{}{}
	}

	deviceStates := stateReader.DeviceFirmwareStates(iface)
	for addr, state := range deviceStates {
		if _, match := stateSet[state]; !match {
			continue
		}
		if err := c.RefreshDeviceDescriptionsAndCreateMissingDevices(ctx, fetcher, iface); err != nil {
			c.logger.Warn("RefreshFirmwareDataByState: refresh failed",
				"interface", string(iface),
				"device", addr,
				"state", string(state),
				"error", err)
		}
		// One call per iface is sufficient — the description pull covers
		// all devices. Break after the first match.
		break
	}
	return nil
}

// RefreshFirmwareData re-pulls device descriptions for all known interfaces
// so that firmware-version fields in the description registry reflect the
// current CCU state. Unlike [RefreshFirmwareDataByState], no state filter is
// applied — every interface is refreshed unconditionally.
//
// Non-fatal per-interface errors are logged and skipped so a single
// unreachable interface does not abort the sweep.
func (c *DeviceCoordinator) RefreshFirmwareData(ctx context.Context, fetcher DeviceDescriptionFetcher) error {
	if fetcher == nil {
		return errors.New("device_coordinator: refresh_firmware_data: nil fetcher")
	}
	for _, iface := range c.descs.GetInterfaceIDs() {
		if err := c.RefreshDeviceDescriptionsAndCreateMissingDevices(ctx, fetcher, iface); err != nil {
			c.logger.Warn("RefreshFirmwareData: refresh failed",
				"interface", string(iface),
				"error", err)
		}
	}
	return nil
}

// ReplaceDevice handles a CCU device-replacement event: the old device is
// evicted from every registry layer and a DeviceRemovedEvent is emitted, then
// the replacement device is fetched and ingested via the standard pipeline.
//
// Substitution constraint: both devices are on the same interface. The
// CCU (rfd / hs485d) owns the type-compatibility check and legitimately
// approves compatible cross-type swaps, so a differing model string is
// logged and proceeds rather than aborting — refusing here would strand
// the model after the CCU has already performed the swap. If the old
// device is unknown the method returns a descriptive error without
// modifying any state.
func (c *DeviceCoordinator) ReplaceDevice(
	ctx context.Context,
	fetcher DeviceDescriptionFetcher,
	iface hmtypes.WireInterfaceID,
	oldAddr, newAddr string,
) error {
	if fetcher == nil {
		return errors.New("device_coordinator: replace_device: nil fetcher")
	}

	// Validate old device exists.
	oldEntry, ok := c.devices.Get(iface, oldAddr)
	if !ok {
		return fmt.Errorf("device_coordinator: replace_device: old device %q not found on interface %s", oldAddr, iface)
	}

	// A cross-type but CCU-approved replacement is legitimate; note it
	// and proceed rather than reject a swap the CCU already made.
	if newDesc, ok := c.descs.Get(iface, newAddr); ok && newDesc.Type != "" && newDesc.Type != oldEntry.Model {
		c.logger.Info("device_coordinator.replace_device.cross_type",
			slog.String("interface", string(iface)),
			slog.String("old", oldAddr),
			slog.String("old_model", oldEntry.Model),
			slog.String("new", newAddr),
			slog.String("new_model", newDesc.Type))
	}

	// Evict old device: remove all channel descriptions + paramsets, then the device entry.
	allDescs := c.descs.All(iface)
	for i := range allDescs {
		if allDescs[i].Address == oldAddr || allDescs[i].Parent == oldAddr {
			c.paramsets.DeleteChannel(iface, allDescs[i].Address)
			c.descs.Delete(iface, allDescs[i].Address)
		}
	}
	if c.devices.Remove(iface, oldAddr) {
		events.Publish(c.bus, hmevent.DeviceRemovedEvent{
			Base:        hmevent.NewBase(),
			CentralName: c.centralName,
			InterfaceID: string(iface),
			Address:     oldAddr,
		})
	}

	// Ingest replacement device via the standard refresh pipeline.
	if err := c.RefreshDeviceDescriptionsAndCreateMissingDevices(ctx, fetcher, iface); err != nil {
		return fmt.Errorf("device_coordinator: replace_device: refresh: %w", err)
	}
	return nil
}

// InvalidateFirmwareCache evicts all cached descriptions and paramset data
// for deviceAddress after a successful firmware update, ensuring the daemon
// will re-pull fresh metadata on the next access rather than serving stale
// pre-update data.
//
// Called by the adapter layer when a firmware update completes
// (DeviceFirmwareStateChanged event with To == DeviceFirmwareStateUpToDate).
func (c *DeviceCoordinator) InvalidateFirmwareCache(iface hmtypes.WireInterfaceID, deviceAddress string) {
	allDescs := c.descs.All(iface)
	for i := range allDescs {
		d := allDescs[i]
		if d.Address == deviceAddress || d.Parent == deviceAddress {
			c.paramsets.DeleteChannel(iface, d.Address)
			c.descs.Delete(iface, d.Address)
		}
	}
}

// RefreshDeviceDescriptions re-pulls device descriptions for iface from the
// CCU. When refreshOnlyExisting is true, only devices already present in the
// description registry are updated (descriptions for unknown addresses are
// skipped); when false, all descriptions returned by the CCU are applied.
//
// This is the parameterised variant of
// [RefreshDeviceDescriptionsAndCreateMissingDevices]; the boolean flag avoids
// a full re-apply when the operator only wants to refresh known state.
func (c *DeviceCoordinator) RefreshDeviceDescriptions(
	ctx context.Context,
	fetcher DeviceDescriptionFetcher,
	iface hmtypes.WireInterfaceID,
	refreshOnlyExisting bool,
) error {
	if fetcher == nil {
		return errors.New("device_coordinator: refresh_device_descriptions: nil fetcher")
	}
	descs, err := fetcher.ListDevices(ctx, iface)
	if err != nil {
		return fmt.Errorf("device_coordinator: refresh_device_descriptions: %w", err)
	}
	for i := range descs {
		if refreshOnlyExisting {
			if _, ok := c.descs.Get(iface, descs[i].Address); !ok {
				continue
			}
		}
		c.descs.Put(iface, descs[i])
	}
	return nil
}

// IdentifyDevicesMissingParamsets scans the device registry for iface and
// returns the addresses of devices that have no MASTER or VALUES paramset
// entries in the paramset registry. These are devices that were created from
// cache or a partial pull and still need a full paramset fetch.
func (c *DeviceCoordinator) IdentifyDevicesMissingParamsets(iface hmtypes.WireInterfaceID) []string {
	allDescs := c.descs.All(iface)
	var missing []string
	for i := range allDescs {
		d := allDescs[i]
		// Only check channel addresses (colon-separated), not top-level devices.
		if !strings.Contains(d.Address, ":") {
			continue
		}
		_, hasMaster := c.paramsets.Get(iface, d.Address, hmenum.ParamsetKeyMaster)
		_, hasValues := c.paramsets.Get(iface, d.Address, hmenum.ParamsetKeyValues)
		if !hasMaster && !hasValues {
			missing = append(missing, d.Address)
		}
	}
	return missing
}

// DeviceNameOverrideChecker is the optional hook called by
// [RenameNewDeviceFromOverride] to look up an operator-configured name
// override for a device address. Wire it via
// [DeviceCoordinator.SetDeviceNameOverrideChecker].
type DeviceNameOverrideChecker interface {
	// GetNameOverride returns (name, true) when an override exists for
	// deviceAddress. (""، false) when no override is configured.
	GetNameOverride(deviceAddress string) (string, bool)
}

// SetDeviceNameOverrideChecker wires the override-lookup hook. Nil disables
// the feature (RenameNewDeviceFromOverride becomes a no-op). Returns the
// receiver for chaining.
func (c *DeviceCoordinator) SetDeviceNameOverrideChecker(ch DeviceNameOverrideChecker) *DeviceCoordinator {
	c.mu.Lock()
	c.nameOverrideChecker = ch
	c.mu.Unlock()
	return c
}

// RenameNewDeviceFromOverride checks whether deviceAddress has an
// operator-configured name override (via the wired [DeviceNameOverrideChecker])
// and, if so, invokes the rename callback so the operator-assigned name is
// applied immediately when the device first appears.
//
// Called by the device-lifecycle path when a [hmevent.DeviceCreatedEvent]
// arrives for a new device. The rename callback is wired by the adapter
// layer and typically calls the domain model's rename path.
func (c *DeviceCoordinator) RenameNewDeviceFromOverride(iface hmtypes.WireInterfaceID, deviceAddress string, renameCallback func(address, name string)) {
	c.mu.Lock()
	ch := c.nameOverrideChecker
	c.mu.Unlock()
	if ch == nil || renameCallback == nil {
		return
	}
	name, ok := ch.GetNameOverride(deviceAddress)
	if !ok || name == "" {
		return
	}
	renameCallback(deviceAddress, name)
	c.logger.Debug(
		"RenameNewDeviceFromOverride: applied override name",
		"address", deviceAddress,
		"interface", string(iface),
		"name", name,
	)
}

// ScheduleParamsetConsistencyCheck launches a background goroutine that runs
// CheckParamsetConsistency for the given device addresses. The check runs
// asynchronously so it does not delay device creation for the caller.
//
// The goroutine is tracked in c.wg so [Stop] can wait for it to drain during
// shutdown, and recovers from a panic in the checker or onResult callback so
// a single misbehaving implementation cannot take down the daemon.
//
// iface / ifaceKey carry the same split as [CheckParamsetConsistency]: the
// bare interface gates the HmIP-only check, the wire id keys the registries.
func (c *DeviceCoordinator) ScheduleParamsetConsistencyCheck(
	ctx context.Context,
	iface hmenum.Interface,
	ifaceKey hmtypes.WireInterfaceID,
	deviceAddresses []string,
	checker ParamsetConsistencyChecker,
	onResult func([]ParamsetInconsistency),
) {
	if checker == nil || len(deviceAddresses) == 0 {
		return
	}
	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		defer func() {
			if r := recover(); r != nil {
				c.logger.Error("ScheduleParamsetConsistencyCheck: goroutine panicked",
					"interface", string(iface),
					"panic", r,
					"stack", string(debug.Stack()))
			}
		}()
		inconsistencies, err := c.CheckParamsetConsistency(ctx, iface, ifaceKey, deviceAddresses, checker)
		if err != nil {
			c.logger.Warn("ScheduleParamsetConsistencyCheck: check failed",
				"interface", string(iface), "error", err)
			return
		}
		if onResult != nil && len(inconsistencies) > 0 {
			onResult(inconsistencies)
		}
	}()
}
