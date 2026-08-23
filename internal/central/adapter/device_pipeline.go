// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/SukramJ/openccu-loom/internal/ccudata"
	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/internal/central/registry"
	"github.com/SukramJ/openccu-loom/internal/channelflags"
	"github.com/SukramJ/openccu-loom/internal/client/backends"
	"github.com/SukramJ/openccu-loom/internal/client/rega"
	"github.com/SukramJ/openccu-loom/internal/model/calculated"
	"github.com/SukramJ/openccu-loom/internal/model/custom"
	switchdev "github.com/SukramJ/openccu-loom/internal/model/custom/switch"

	// Blank-import the builtins aggregator so every custom-DP
	// sub-package (climate, cover, light, lock, siren, switch,
	// textdisplay, valve) has its `init()` run before the pipeline
	// materialises devices. Without this side-effect import the
	// constructor catalogue would be empty whenever the pipeline is
	// used outside a `main` that imports the daemon ( E.13).
	_ "github.com/SukramJ/openccu-loom/internal/model/custom/builtins"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	modevent "github.com/SukramJ/openccu-loom/internal/model/event"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/internal/model/naming"
	"github.com/SukramJ/openccu-loom/internal/store/sqlite"
	"github.com/SukramJ/openccu-loom/internal/store/visibility"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// namingInitializer is the construction-time setter contract every concrete
// data point that embeds [datapoint.BaseDataPointFields] satisfies. The
// pipeline uses it to install the cached presentation surface (NameData,
// PathData, IsInMultipleChannels) once per DP, before the DP is published to
// the channel.
//
// The interface is anonymous to the datapoint package — it is the pipeline's
// local view of the SetNameData / SetPathData SetIsInMultipleChannels
// promotion chain.
type namingInitializer interface {
	SetNameData(nd naming.NameData)
	SetPathData(pd naming.PathData)
	SetIsInMultipleChannels(v bool)
}

// DevicePipeline turns a backend's `listDevices` output into a
// populated [*registry.ModelRegistry]. It sits between the client
// layer and the domain model — the device coordinator fires it
// whenever a fresh snapshot arrives.
//
// Translations and a locale can be attached via [WithTranslations] so
// devices ingested through [Ingest] / [IngestFromBackend] get their
// human-readable model label + icon filled in from the CCU translation
// archive. When unset the raw model strings are used.
type DevicePipeline struct {
	unit         *central.Unit
	translations *ccudata.Translations
	locale       string
	// names maps device and channel addresses to their CCU-assigned
	// display names. Populated by the hub wiring from
	// Device.listAllDetail. An empty map means no names were pulled
	// devices render by address, channels by their number.
	names map[string]string
	// rooms maps a device or channel address to the names of every CCU
	// room the entity is assigned to. Sourced from Room.getAll during
	// hub wiring. Nil tolerated — devices render without room metadata.
	rooms map[string][]string
	// functions maps a device or channel address to the names of every
	// CCU function ("Gewerk") the entity is assigned to. Sourced from
	// Subsection.getAll during hub wiring. Nil tolerated.
	functions map[string][]string
	// masterRefreshHooks resolves the post-MASTER-write hook for the
	// interface a channel is being hydrated for. Classic HM interfaces
	// (BidCos-RF, BidCos-Wired, VirtualDevices, CUxD) each wire their own
	// poller, which is bound to that interface's backend; HmIP interfaces
	// register nothing and their channels stay hookless because the
	// CONFIG_PENDING event path covers them.
	//
	// It is a per-interface map and not a single hook because one pipeline
	// serves every interface of a central: a scalar hook was overwritten by
	// each interface in turn, and the hot-plug ingestor — built after the
	// whole interface loop — then stamped whichever interface happened to be
	// wired last onto every device paired at runtime, sending its MASTER
	// read-backs through the wrong backend.
	masterRefreshHooks *masterRefreshHookSet

	// cdpLightLastBrightness / cdpUseGroupChannelForCover are the
	// per-central custom-DP rendering toggles stamped onto each device
	// before materialisation. Both default to true (see
	// [NewDevicePipeline]); set via [WithCustomDPBehavior] from the
	// central's config.
	cdpLightLastBrightness     bool
	cdpUseGroupChannelForCover bool
	// cdpEnableFirmwareCheck gates the per-device firmware-update
	// surface (default true). Set via [WithFirmwareCheck].
	cdpEnableFirmwareCheck bool

	// visibility is the optional visibility gate consulted before each
	// data-point is stored on a channel. When nil, all parameters pass
	// through (backwards-compatible behaviour for tests/tooling that
	// create a pipeline without a gate). Set via [WithVisibility].
	visibility *visibility.Registry

	// masterValues is the optional persistent cache consulted by
	// [seedMasterValues] before issuing getParamset(MASTER) against the
	// CCU. When nil, every channel hits the CCU at hydration time —
	// the default for tests/tooling. Set via [WithMasterValuesStore].
	//
	// The cache is the only mechanism the daemon has to survive a
	// CCU+daemon-cold-boot without flooding the CCU duty-cycle with
	// per-channel MASTER reads — there is no funk-free server-side
	// alternative (no ReGa bulk path for MASTER; see
	// occu/WebUI/.../heating_control.fn for proof that even the CCU's
	// own WebUI takes the per-channel getParamset path).
	masterValues *sqlite.MasterValuesStore
	centralName  string

	// valuesCache, when non-nil, is consulted by the restore pass that
	// runs between hydrateDataPoints and seedValues. Cached wire-DP
	// values land on the channel data points as `cache`-sourced
	// snapshots; the subsequent fetch_all_device_data pass overwrites
	// them with live data wherever the CCU returns a fresh value.
	valuesCache *sqlite.ValuesCacheStore

	// channelFlags, when non-nil, carries the operator-set per-channel
	// overrides (G12). The second pass re-applies them onto every rebuilt
	// channel so a hidden/locked channel survives a re-ingest / reconnect.
	channelFlags *channelflags.Overlay

	// ingestMu serialises every ingest run — the interface bring-up
	// ([IngestFromBackend]) and the hot-plug path ([IngestNewDevices]).
	// The CCU re-announces its whole inventory via `newDevices` on every
	// reconnect, so a callback can race the initial bring-up; without
	// this lock both paths would hydrate the same channels concurrently
	// and [device.Device.AddChannel] (register-or-replace) would drop
	// freshly built data points. A pointer so [scopedTo]'s shallow copy
	// shares the same lock instead of copying it.
	ingestMu *sync.Mutex

	// addressScope, when non-nil, narrows every interface-wide pass to
	// the listed device-root addresses (see [devicesFor]). Set only on
	// the shallow copy [scopedTo] returns for hot-plug ingests; the
	// bring-up pipeline keeps it nil and sees every device.
	addressScope map[string]struct{}
}

// NewDevicePipeline constructs a pipeline bound to c.
func NewDevicePipeline(u *central.Unit) *DevicePipeline {
	return &DevicePipeline{
		unit: u,
		// Custom-DP rendering toggles default to true; WireCentrals
		// overrides them per central via [WithCustomDPBehavior]. The
		// true default keeps the historical behavior for every pipeline
		// built without the builder (tests, tooling).
		cdpLightLastBrightness:     true,
		cdpUseGroupChannelForCover: true,
		cdpEnableFirmwareCheck:     true,
		ingestMu:                   &sync.Mutex{},
		masterRefreshHooks:         newMasterRefreshHookSet(),
	}
}

// masterRefreshHookSet holds one post-MASTER-write hook per interface wire
// id. It is a pointer field on [DevicePipeline] so the shallow copies
// [DevicePipeline.scopedTo] hands the hot-plug path share the same set, and
// it carries its own lock because a background interface activation retry can
// hydrate while a later interface is still being wired.
type masterRefreshHookSet struct {
	mu      sync.RWMutex
	byIface map[string]func(addr string, key hmenum.ParamsetKey)
}

func newMasterRefreshHookSet() *masterRefreshHookSet {
	return &masterRefreshHookSet{byIface: make(map[string]func(addr string, key hmenum.ParamsetKey))}
}

// set registers (or, with a nil fn, clears) the hook for one interface.
func (s *masterRefreshHookSet) set(interfaceID string, fn func(addr string, key hmenum.ParamsetKey)) {
	if s == nil || interfaceID == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if fn == nil {
		delete(s.byIface, interfaceID)
		return
	}
	s.byIface[interfaceID] = fn
}

// get returns the hook registered for interfaceID, or nil when the interface
// has none (every HmIP interface, and any interface not yet wired).
func (s *masterRefreshHookSet) get(interfaceID string) func(addr string, key hmenum.ParamsetKey) {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.byIface[interfaceID]
}

// scopedTo returns a shallow copy of the pipeline whose interface-wide
// passes only see the given device-root addresses. The copy shares the
// unit, the lookup maps, and the ingest mutex with the original — it
// differs only in [DevicePipeline.addressScope], so a hot-plug ingest
// can reuse the exact bring-up sequence without re-hydrating devices
// that already exist.
func (p *DevicePipeline) scopedTo(addresses map[string]struct{}) *DevicePipeline {
	scoped := *p
	scoped.addressScope = addresses
	return &scoped
}

// devicesFor returns the ModelRegistry devices that belong to
// interfaceID, narrowed to the pipeline's address scope when one is set.
// Every interface-wide pass iterates through this helper so the hot-plug
// path touches only the freshly materialised devices — re-running a pass
// over the whole interface would re-issue per-channel CCU reads and burn
// radio duty-cycle on every `newDevices` callback.
func (p *DevicePipeline) devicesFor(interfaceID string) []*device.Device {
	if p.unit == nil {
		return nil
	}
	all := p.unit.ModelRegistry.List()
	out := make([]*device.Device, 0, len(all))
	for _, d := range all {
		if d == nil || d.InterfaceID != interfaceID {
			continue
		}
		if p.addressScope != nil {
			if _, ok := p.addressScope[d.Address]; !ok {
				continue
			}
		}
		out = append(out, d)
	}
	return out
}

// WithCustomDPBehavior sets the per-central custom-DP rendering toggles
// the pipeline stamps onto each device before custom-DP
// materialisation: light last-brightness and cover group-channel
// state. Both default to true (see [NewDevicePipeline]).
func (p *DevicePipeline) WithCustomDPBehavior(lightLastBrightness, useGroupChannelForCover bool) *DevicePipeline {
	p.cdpLightLastBrightness = lightLastBrightness
	p.cdpUseGroupChannelForCover = useGroupChannelForCover
	return p
}

// WithFirmwareCheck gates the per-device firmware-update surface
// (default true). When false, devices are materialised as
// non-updatable and no firmware-update entity spawns.
func (p *DevicePipeline) WithFirmwareCheck(enabled bool) *DevicePipeline {
	p.cdpEnableFirmwareCheck = enabled
	return p
}

// WithTranslations attaches an optional translation set + locale used
// to enrich device metadata during [Ingest]. Nil / empty locale means
// "no translations" and leaves labels empty. Returns the receiver so
// the call composes with [NewDevicePipeline].
func (p *DevicePipeline) WithTranslations(t *ccudata.Translations, locale string) *DevicePipeline {
	p.translations = t
	p.locale = locale
	return p
}

// WithNames attaches the operator-assigned device / channel names
// fetched via Device.listAllDetail. Missing or nil is tolerated
// the pipeline then simply leaves the display names empty.
func (p *DevicePipeline) WithNames(names map[string]string) *DevicePipeline {
	p.names = names
	return p
}

// WithRooms attaches the address → []room-name map fetched via
// Room.getAll during hub wiring. The pipeline stamps the device's
// Rooms slice during ingest; channels render with the union of their
// own rooms when present (already pre-aggregated by the hub-wiring
// resolver). Passing nil leaves devices' Rooms empty.
func (p *DevicePipeline) WithRooms(rooms map[string][]string) *DevicePipeline {
	p.rooms = rooms
	return p
}

// WithFunctions attaches the address → []function-name map fetched
// via Subsection.getAll during hub wiring. Stamps Device.Functions
// during ingest. Passing nil leaves Functions empty.
func (p *DevicePipeline) WithFunctions(functions map[string][]string) *DevicePipeline {
	p.functions = functions
	return p
}

// WithMasterRefreshHook wires the post-MASTER-write hook of ONE interface,
// keyed by its wire id. The hook is installed on every channel that interface
// hydrates via [device.Channel.SetMasterRefreshHook] so that a successful
// [device.Channel.Set] / [device.Channel.SetMany] with
// [hmenum.ParamsetKeyMaster] schedules a delayed read-back via
// [backends.MasterPoller]. Pass nil to clear (HmIP interfaces leave this
// unset — they use the CONFIG_PENDING signal instead).
//
// The poller behind fn dispatches through one interface's backend, so the
// registration has to name the interface it belongs to: the same pipeline
// hydrates every interface of the central and later hot-plugs devices for any
// of them.
func (p *DevicePipeline) WithMasterRefreshHook(interfaceID string, fn func(addr string, key hmenum.ParamsetKey)) *DevicePipeline {
	if p.masterRefreshHooks == nil {
		p.masterRefreshHooks = newMasterRefreshHookSet()
	}
	p.masterRefreshHooks.set(interfaceID, fn)
	return p
}

// WithMasterValuesStore wires the persistent cache that [seedMasterValues]
// consults before issuing getParamset(MASTER) against the CCU. centralName
// scopes the cache rows so multi-CCU deployments do not collide. Pass a nil
// store to disable the cache (tests / one-shot tools).
func (p *DevicePipeline) WithMasterValuesStore(store *sqlite.MasterValuesStore, centralName string) *DevicePipeline {
	p.masterValues = store
	p.centralName = centralName
	return p
}

// WithValuesCacheStore wires the persistent VALUES cache. The
// pipeline applies cached wire values between hydration and the
// fetch_all_device_data seed so the SPA / MQTT / Matter surfaces see
// the last known state immediately on boot. Pass nil to disable.
func (p *DevicePipeline) WithValuesCacheStore(store *sqlite.ValuesCacheStore, centralName string) *DevicePipeline {
	p.valuesCache = store
	if centralName != "" {
		p.centralName = centralName
	}
	return p
}

// WithChannelFlags attaches the operator per-channel override overlay
// (G12) so the ingest re-applies hidden/locked onto every rebuilt channel.
// Pass nil to disable (tests / one-shot tools).
func (p *DevicePipeline) WithChannelFlags(overlay *channelflags.Overlay) *DevicePipeline {
	p.channelFlags = overlay
	return p
}

// WithVisibility attaches a visibility registry to the pipeline.
// During [hydrateParamset] every parameter is checked via
// [visibility.Registry.IsAllowedForChannel]; parameters that fail the check
// are silently dropped (no data point is created).
//
// The required-parameter whitelist carried by the registry is used as a
// whitelist override: parameters declared required are never filtered, even if
// they appear in IGNORED_PARAMETERS. The whitelist should be populated by the
// caller via [visibility.Registry.SetRequiredParameters] with the result of
// [custom.Registry.RequiredParameters] before passing the registry here.
//
// Passing nil is a no-op (gate disabled — all parameters pass through).
// Returns the receiver for method chaining.
func (p *DevicePipeline) WithVisibility(v *visibility.Registry) *DevicePipeline {
	p.visibility = v
	return p
}

// Ingest normalises descriptions and instantiates Device / Channel
// objects on the central's [*registry.ModelRegistry]. The pipeline
// deduplicates by address — existing records are refreshed in
// place so callers don't lose subscriptions.
//
// `interfaceID` tags every device; it is typically the backend's
// logical id (e.g. "HmIP-RF").
func (p *DevicePipeline) Ingest(ctx context.Context, interfaceID string, iface hmenum.Interface, descs []hmproto.DeviceDescription) error {
	if p.unit == nil {
		return errors.New("pipeline: no central")
	}
	// First pass: build devices (only entries without PARENT — these
	// are the device roots; children are channels).
	byAddress := map[string]*device.Device{}
	for i := range descs {
		dd := &descs[i]
		if dd.Parent != "" {
			continue
		}
		if _, isChannel := splitChannel(dd.Address); isChannel {
			continue
		}
		d := p.ensureDevice(dd, interfaceID, iface)
		byAddress[dd.Address] = d
		// The device registry is keyed by the canonical wire id
		// (`<central>-<iface>`) — that is what the callback path
		// (DeviceCoordinator.HandleNewDevices) and every coordinator lookup
		// use. Registering under the bare interface here made the same device
		// appear twice after a hot-plug and made every coordinator that
		// resolves a device by its description key miss it. ProductGroup keeps
		// the bare enum: it classifies the radio technology, not a registry
		// key.
		p.unit.DeviceRegistry.Put(registry.DeviceEntry{
			Interface:    hmtypes.ParseWireInterfaceID(interfaceID),
			Address:      dd.Address,
			Model:        dd.Type,
			ProductGroup: hmenum.ProductGroupForModel(dd.Type, iface),
		})
	}

	// Second pass: attach channels.
	for i := range descs {
		dd := &descs[i]
		if dd.Parent == "" {
			continue
		}
		parent, ok := byAddress[dd.Parent]
		if !ok {
			continue
		}
		// Build the channel fully BEFORE publishing it on the parent device.
		// The parent is already in the model registry (first pass), so every
		// north-bound reader can reach it; a channel installed empty and filled
		// afterwards is a torn read for those readers, and on a mid-life
		// re-ingest the replacement channel goes live blank while the previous
		// one is already gone. [device.Device.PutChannel] takes the device lock
		// that [device.Device.Channels] reads under, so it is the release edge
		// for the whole object.
		chNum := channelNumber(dd.Address)
		ch := device.NewChannel(dd.Address, chNum, dd.Type, hmenum.ParamsetKeyValues)
		// Carry the raw CCU LINK_SOURCE_ROLES / LINK_TARGET_ROLES onto the
		// channel so the direct-link role-matching filter can intersect them
		// without a CCU roundtrip. Every (re)ingest rebuilds a fresh channel,
		// so this re-applies on hot-plug / reconnect.
		ch.SetLinkRoles([]string(dd.LinkSourceRoles), []string(dd.LinkTargetRoles))
		// Re-apply the operator per-channel overrides (G12) for the same
		// reason: a hidden/locked channel would otherwise revert on a
		// reconnect / hot-plug.
		if p.channelFlags != nil {
			f := p.channelFlags.Get(p.centralName, dd.Address)
			ch.SetOperatorFlags(f.Hidden, f.Locked)
		}
		name, rooms, functions := p.channelAssignments(dd.Address)
		ch.SetName(name)
		ch.SetRooms(rooms)
		ch.SetFunctions(functions)
		// Stamp the channel's CCU ise_id from the DeviceDetails cache (seeded
		// in WireHub before ingest). It lets a system variable / program whose
		// name carries the channel identifier be linked to this channel — see
		// [device.Device.IdentifyChannel] and the Python reference's
		// `model/device.py:1012` (Channel.ise_id via get_address_id).
		if p.unit.DeviceDetails != nil {
			ch.SetIseID(p.unit.DeviceDetails.GetAddressID(dd.Address))
		}
		parent.PutChannel(ch)
	}
	return ctx.Err()
}

// channelAssignments resolves a channel's operator-assigned name, rooms and
// functions.
//
// Hot-plugged devices are absent from the bring-up snapshot maps (names /
// rooms / functions were pulled once during hub wiring). The DeviceDetails
// cache is the living source — refreshed by the periodic loader and
// force-refreshed before each hot-plug ingest — so it fills in whenever the
// snapshot has no entry.
func (p *DevicePipeline) channelAssignments(address string) (name string, rooms, functions []string) {
	if p.names != nil {
		name = p.names[address]
	}
	if p.rooms != nil {
		rooms = p.rooms[address]
	}
	if p.functions != nil {
		functions = p.functions[address]
	}
	if p.unit.DeviceDetails == nil {
		return name, rooms, functions
	}
	if name == "" {
		name = p.unit.DeviceDetails.GetName(address)
	}
	if len(rooms) == 0 {
		rooms = p.unit.DeviceDetails.GetChannelRooms(address)
	}
	if len(functions) == 0 {
		functions = p.unit.DeviceDetails.GetFunctions(address)
	}
	return name, rooms, functions
}

func (p *DevicePipeline) ensureDevice(dd *hmproto.DeviceDescription, interfaceID string, iface hmenum.Interface) *device.Device {
	if existing, ok := p.unit.ModelRegistry.Get(dd.Address); ok {
		return existing
	}
	var displayName string
	if p.names != nil {
		displayName = p.names[dd.Address]
	}
	var rooms, functions []string
	if p.rooms != nil {
		rooms = p.rooms[dd.Address]
	}
	if p.functions != nil {
		functions = p.functions[dd.Address]
	}
	// Hot-plugged devices are absent from the bring-up snapshot maps; the
	// DeviceDetails cache is the living source (periodic loader + a forced
	// refresh before each hot-plug ingest), so fall back to it whenever
	// the snapshot has no entry.
	if p.unit.DeviceDetails != nil {
		if displayName == "" {
			displayName = p.unit.DeviceDetails.GetName(dd.Address)
		}
		if len(rooms) == 0 {
			rooms = p.unit.DeviceDetails.GetDeviceRooms(dd.Address)
		}
		if len(functions) == 0 {
			functions = p.unit.DeviceDetails.GetFunctions(dd.Address)
		}
	}
	// The device's CCU ise_id from the DeviceDetails cache (seeded in WireHub
	// before ingest). It lets a system variable / program whose name carries
	// the device identifier be linked to this device — see
	// [device.Device.IdentifyChannel] and the Python reference's
	// `model/device.py:308` (Device.ise_id via get_address_id).
	var deviceISEID int
	if p.unit.DeviceDetails != nil {
		deviceISEID = p.unit.DeviceDetails.GetAddressID(dd.Address)
	}
	// Derive schema version from the wire VERSION field (a *int that is nil /
	// absent on older CCU firmwares).
	var schemaVersion int
	if dd.Version != nil {
		schemaVersion = *dd.Version
	}
	// The firmware-update surface (the per-device Update entity) is
	// gated by the per-central firmware-check toggle. When disabled the
	// device is treated as non-updatable so no update entity spawns.
	updatable := dd.FirmwareUpdatable != nil && *dd.FirmwareUpdatable && p.cdpEnableFirmwareCheck
	d := device.New(device.Config{
		InterfaceID: interfaceID,
		Interface:   iface,
		Address:     dd.Address,
		Model:       dd.Type,
		// SubModel mirrors the CCU's SUBTYPE field. Required so the translation
		// lookup chain has a fallback key when the full model name (e.g.
		// "HmIP-PSM") is not in the OCCU `device_models_<locale>.json` table — most
		// HmIP devices only register under the SUBTYPE key ("PSM" → "psm"). Without
		// SubModel populated, [Translations.DeviceModelLabel] returns empty for the
		// majority of HmIP devices and HA-Discovery's `model_id` field stays unset.
		SubModel:     dd.Subtype,
		Name:         displayName,
		Manufacturer: hmenum.ManufacturerEQ3,
		ProductGroup: hmenum.ProductGroupForModel(dd.Type, iface),
		Rooms:        rooms,
		Functions:    functions,
		// RX_MODE is the device's receive-mode bitmask; carrying it lets the
		// REST device DTO tell WAKEUP / LAZY_CONFIG battery devices apart from
		// mains devices, so the SPA can surface a "pending wakeup" hint after a
		// link/config write.
		RxMode:        hmenum.RxMode(dd.RXMode),
		IseID:         deviceISEID,
		SchemaVersion: schemaVersion,
		Firmware: device.FirmwareInfo{
			Current:   dd.Firmware,
			Available: dd.AvailableFirmware,
			Updatable: updatable,
			// The installable-update gate reads FIRMWARE_UPDATE_STATE (the
			// HmIP update lifecycle), not FIRMWARE_STATE (firmware health).
			UpdateState: hmenum.DeviceFirmwareState(dd.FirmwareUpdateState),
		},
		Updatable: updatable,
	})
	if p.translations != nil {
		d.ModelLabel = p.translations.DeviceModelLabel(p.locale, d.Model, d.SubModel)
		d.ModelIcon = p.translations.DeviceModelIcon(d.Model)
	}
	p.unit.ModelRegistry.Put(d)
	return d
}

// IngestFromBackend is the main entry point for the southbound
// bootstrap: pull the device list, attach channels, and — when a
// writer is supplied — load VALUES-paramset descriptions per channel
// to populate typed data points. `writer` may be nil; data points are
// still built but stay read-only (SetValue returns [ErrNoWriter]).
//
// When `runner` is non-nil, the pipeline also pulls initial values in
// a single Rega call via `fetch_all_device_data` and seeds every
// matching data point. Without the runner the data points exist but
// stay unobserved until the first event arrives.
//
// The logger receives a debug entry per paramset-load failure so
// the daemon keeps going when a single CCU channel mis-behaves.
func (p *DevicePipeline) IngestFromBackend(
	ctx context.Context,
	interfaceID string,
	iface hmenum.Interface,
	b backends.Operations,
	writer ValueWriter,
	runner *rega.Runner,
	logger *slog.Logger,
) error {
	p.ingestMu.Lock()
	defer p.ingestMu.Unlock()
	descs, err := b.ListDevices(ctx)
	if err != nil {
		return fmt.Errorf("pipeline: ListDevices: %w", err)
	}
	if err := p.Ingest(ctx, interfaceID, iface, descs); err != nil {
		return err
	}
	// After live ListDevices, also materialise any devices whose descriptions
	// were already in the registry (from a previous run's persisted cache) but
	// whose Device objects were not yet created — covers the warm-start path
	// where the description registry outlives a reconnect cycle.
	if p.unit != nil && p.unit.Devices != nil {
		_ = p.unit.Devices.CheckAndCreateDevicesFromCache(ctx)
	}
	return p.finishIngest(ctx, interfaceID, iface, b, writer, runner, logger)
}

// IngestNewDevices is the hot-plug entry point: it materialises devices
// announced by a live `newDevices` callback without re-pulling or
// re-hydrating the whole interface. The CCU re-announces its entire
// inventory on every reconnect (the callback server's listDevices reply
// is deliberately empty — see [CallbackHandlers.ListDevices]), so the
// method first narrows descs to device roots the ModelRegistry does not
// know yet; when nothing is new it returns without touching the CCU.
// The shared ingest mutex serialises against [IngestFromBackend], so a
// callback racing the interface bring-up waits and then no-ops — every
// address is materialised by then. All post-ingest passes run scoped to
// the new device roots via [scopedTo].
//
// Returns the addresses of the newly materialised device roots (empty
// when every announced device already existed).
func (p *DevicePipeline) IngestNewDevices(
	ctx context.Context,
	interfaceID string,
	iface hmenum.Interface,
	b backends.Operations,
	writer ValueWriter,
	runner *rega.Runner,
	descs []hmproto.DeviceDescription,
	logger *slog.Logger,
) ([]string, error) {
	if p.unit == nil {
		return nil, errors.New("pipeline: no central")
	}
	if b == nil {
		return nil, errors.New("pipeline: no backend")
	}
	p.ingestMu.Lock()
	defer p.ingestMu.Unlock()

	// Narrow to device roots the model does not know yet. Channel-only
	// announcements (factory-reset re-pairs of a known device) are out of
	// scope here — the existing device keeps its channels and the next
	// bring-up reconciles them.
	newRoots := make(map[string]struct{})
	for i := range descs {
		dd := &descs[i]
		if dd.Parent != "" {
			continue
		}
		if _, isChannel := splitChannel(dd.Address); isChannel {
			continue
		}
		if _, exists := p.unit.ModelRegistry.Get(dd.Address); exists {
			continue
		}
		newRoots[dd.Address] = struct{}{}
	}
	if len(newRoots) == 0 {
		return nil, nil
	}
	scopedDescs := make([]hmproto.DeviceDescription, 0, len(descs))
	for i := range descs {
		dd := descs[i]
		root := dd.Address
		if dd.Parent != "" {
			root = dd.Parent
		}
		if _, ok := newRoots[root]; ok {
			scopedDescs = append(scopedDescs, dd)
		}
	}
	if err := p.Ingest(ctx, interfaceID, iface, scopedDescs); err != nil {
		return nil, err
	}
	scoped := p.scopedTo(newRoots)
	if err := scoped.finishIngest(ctx, interfaceID, iface, b, writer, runner, logger); err != nil {
		return nil, err
	}
	addrs := make([]string, 0, len(newRoots))
	for a := range newRoots {
		addrs = append(addrs, a)
	}
	sort.Strings(addrs)
	return addrs, nil
}

// finishIngest runs the shared post-ingest sequence — data-point
// hydration, custom/calculated/combined-DP materialisation, lifecycle
// wiring, week-profile handling, value seeding, and every visibility
// mark pass — over [devicesFor]'s view of interfaceID. Both the
// interface bring-up and the hot-plug path funnel through this method
// so the two can never drift apart; the hot-plug caller narrows the
// view via [scopedTo] first. Callers must hold ingestMu.
func (p *DevicePipeline) finishIngest(
	ctx context.Context,
	interfaceID string,
	iface hmenum.Interface,
	b backends.Operations,
	writer ValueWriter,
	runner *rega.Runner,
	logger *slog.Logger,
) error {
	if err := p.hydrateDataPoints(ctx, interfaceID, b, writer, logger); err != nil {
		return err
	}
	// Recompute the cached names now that every channel of the interface
	// carries its data points — see [DevicePipeline.restampDataPointNames].
	p.restampDataPointNames(interfaceID)
	// Materialise custom data points for every device that belongs to
	// this interface. The materializer walks the registered profiles
	// (DefaultRegistry, populated via the [builtins] blank import) and
	// instantiates the per-profile [custom.Constructor] on each
	// relevant channel. Errors are logged but never aborted — a single
	// device whose constructor returns an error still gets every
	// other custom DP and continues to operate via its generic DPs
	// ( E.13).
	p.materialiseCustomDataPoints(interfaceID, logger)
	// Calculated DPs (DewPoint / Enthalpy / VaporConcentration / FrostPoint
	// ApparentTemperature / OperatingVoltageLevel / DerivedBinary) are
	// channel-scoped derivations that subscribe to live wire-level parameters.
	// Materialise them after the custom-DP pass so the `additional_data_points`
	// visibility marks have been applied otherwise a calculated sensor's source
	// DP could still report the wrong usage.
	p.materialiseCalculatedDataPoints(interfaceID, logger)
	// Combined DPs (LevelCombined / HSColor / Timer / …) are attached by the
	// custom-DP constructors above; bridge their OnAnyUpdate into the event bus
	// here so MQTT / WS / REST receive live combined-state changes.
	p.materialiseCombinedDataPoints(interfaceID, logger)
	// Wire optimistic-rollback → event-bus producers for every DP on
	// every channel of every device in the interface scope. Mirrors
	// Python `data_point.py:1493-1504 _publish_rollback_event`; without
	// this hop the [hmevent.DataPointOptimisticRolledBackEvent] type
	// exists in the catalogue but no producer surfaces it to north-bound
	// consumers.
	if p.unit != nil && p.unit.EventBus != nil {
		for _, d := range p.devicesFor(interfaceID) {
			bridgeDataPointRollbacksToBus(p.unit.EventBus, d)
		}
	}
	// Wire availability provider and event publisher on every DP so that:
	// - Available() reflects the owning device's radio reachability, not
	//   a hard-coded true; north-bound surfaces (MQTT, REST) can expose the
	//   "unavailable" state correctly when the device drops off the air.
	// - WeekProfile DPs publish WeekProfileChangedEvent on FireScheduleUpdated.
	//   Nothing subscribes to it: north-bound schedule state travels through
	//   the profile's own OnChange callbacks, and the silence is declared in
	//   the subscriber-coverage guard. The event is kept as the bus-shaped
	//   twin of that callback.
	if p.unit != nil && p.unit.EventBus != nil {
		centralName := p.unit.Name()
		for _, d := range p.devicesFor(interfaceID) {
			wireDataPointLifecycle(p.unit.EventBus, centralName, d)
		}
	}
	// Refine every attached week-profile descriptor with the device-
	// level metadata we now have access to: the SET_POINT_TEMPERATURE
	// descriptor MIN / MAX (live temperature bounds) and the
	// ACTIVE_PROFILE / WEEK_PROGRAM_POINTER descriptor MAX (per-device
	// profile cap, P1..P3 for RF / P1..P6 for IP).
	//
	// `attachWeekProfileToChannel` (called from inside hydrateParamset
	// when the first P*_* slot is seen) installs conservative defaults
	// because the VALUES side of the channel is not hydrated yet at
	// that point. Refinement runs once VALUES is hydrated, so by the
	// time MQTT-Discovery / REST consumers query the descriptor it
	// reflects the device's real bounds.
	// Reconcile the slot-parameter-heuristic climate week-profile attachment
	// with the reference has_schedule gate and the channel the loom-client
	// probes: detach profiles on devices that declare no schedule channel
	// (ALPHA-IP-RBG) and relocate the rest onto the canonical schedule channel
	// (WEEK_PROFILE channel, else the climate custom-DP channel — HM-TC-IT,
	// HmIP-WGTC). Runs after custom-DP materialisation so the registry +
	// custom-DP channels are authoritative, and before refinement so a pruned
	// profile is never bound to backend IO.
	p.normalizeClimateWeekProfiles(interfaceID)
	p.refineAttachedWeekProfiles(interfaceID, logger)
	// Non-climate schedule devices: detect WEEK_PROGRAM_CHANNEL_LOCKS
	// channels and install a Default-type ProfileDataPoint + per-channel
	// ScheduleChannelSwitches.
	p.attachNonClimateWeekProfiles(interfaceID)
	// Restore cached wire values onto the data points before the live
	// seedValues round overwrites them. The order is intentional: cache
	// fills the gap while fetch_all_device_data is in flight, then
	// every value the CCU returns supersedes the cached one with the
	// live source. Values the CCU does not return (sleeping battery
	// devices, etc.) keep their cached source so the UI never shows
	// an empty surface on cold boot.
	p.restoreValuesFromCache(ctx, interfaceID, logger)
	if runner != nil {
		// Pass the BARE interface name ("HmIP-RF" / "BidCos-RF") to
		// the Rega `fetch_all_device_data` script — the CCU's
		// `interfaces.Get(<name>)` only knows raw interface labels,
		// NOT the openccu-loom-internal `<central>-<iface>` wire-id
		// composite. Without this fix the script's
		// `interfaces.Get("GoOtto-HmIP-RF")` returns null and the
		// outer `if (oInterface)` branch never enters → empty `{}`
		// JSON → no value seeded → every climate-internal DP
		// (SET_POINT_MODE, ACTIVE_PROFILE, BOOST_MODE,
		// SET_POINT_TEMPERATURE, …) stays unobserved at boot. The
		// device-side ModelRegistry filtering inside seedValues
		// uses the wire-id (`p.central.ModelRegistry.Get(addr)`)
		// independently of this argument.
		if err := p.seedValues(ctx, string(iface), runner, logger); err != nil {
			// Seeding is best-effort: without it points have no value
			// but the daemon still works, so we log and move on.
			logger.Warn("pipeline.seed.failed",
				slog.String("interface", string(iface)),
				slog.String("err", err.Error()))
		}
	}
	// Apply the `_SWITCH_DP_TO_SENSOR` overrides FIRST so HmIP-eTRV.LEVEL
	// and HmIP-HEATING.LEVEL surface as read-only sensors across every
	// adapter (MQTT classifies via [generic.IsForceSensorParameter]
	// already, REST/WS pick this flag up through `IsWritable`).
	//
	// Order rationale (V5 fix, PR-33): the IsForcedSensor head in
	// [generic.DataPoint.Usage] takes precedence over ForcedUsage. Run
	// force-sensor marking BEFORE channel-operation-mode gating so the
	// gating pass sees the final precedence layout — any DP already
	// flagged force-sensor will surface as DataPoint regardless of the
	// gating verdict, by design (force-sensor is a static device-model
	// classification; channel-op-mode is a dynamic configuration). With
	// the previous order (gating first) the relationship between the
	// two passes was implicit; running force-sensor first makes the
	// precedence explicit and removes a fragile cross-pass invariant.
	p.applyForceSensorMarks(interfaceID)
	// Apply CHANNEL_OPERATION_MODE-driven visibility gating now that every
	// channel's MASTER paramset has been hydrated and seedValues has populated
	// CHANNEL_OPERATION_MODE on multi-mode channels.
	p.applyChannelOperationModeGating(interfaceID)
	// Propagate the operator's `un_ignore` configuration onto each data point.
	p.applyUnIgnoredMarks(interfaceID)
	// Suppress every undefined generic data point on devices whose custom DPs
	// deny the `allow_undefined_generic_data_points` flag. Without this pass
	// HmIP-BWTH channels 10-12 STATE-DPs would surface as switches alongside the
	// channel-9 switch the IP_THERMOSTAT profile explicitly marks visible. The
	// pass runs after the un-ignore marks (rather than right after the
	// materialiser) because [custom.SuppressUndefinedGenericDataPoints] skips
	// every DP whose `IsUnIgnored()` mark is set — an operator un-ignore only
	// survives this suppression walk if the mark it depends on already exists.
	// SetForcedUsage has no clearing counterpart, so running the two passes in
	// the other order left every un-ignored parameter on a custom-DP device
	// permanently forced to NoCreate.
	p.suppressUndefinedGenericDataPoints(interfaceID)
	// Force NoCreate on every parameter the visibility decider reports
	// as ignored (ignoredParameters / wildcard pattern
	// ignoreParametersByDevice / RELEVANT_MASTER_PARAMSETS_BY_DEVICE
	// off-list). DPs are kept in the channel maps so diagnostics +
	// custom-DP composition still see them; the no_create mark only
	// suppresses UI / MQTT surfaces. Runs after un-ignored marks so
	// an operator override can re-promote any of them.
	p.applyIgnoredParameterMarks(interfaceID)
	// Force NoCreate on every parameter in the HIDDEN_PARAMETERS set
	// (CONFIG_PENDING, UNREACH, UPDATE_PENDING, CHANNEL_OPERATION_MODE,
	// LOW_BAT_LIMIT, …).
	p.applyHiddenParameterMarks(interfaceID)
	// Force usage=event on click-event parameters (PRESS_*) of physical
	// devices: the reference stack models them as events (HA spawns keypress
	// event groups, never generic button entities). Virtual remotes are
	// the exception — their press parameters are real clickable actions.
	p.applyClickEventMarks(interfaceID)
	// Force NoCreate on every parameter whose FLAGS bitmask carries
	// `Flag.INTERNAL`, unless it appears in the
	// [generic.AllowedInternalParameters] whitelist or the operator has
	// Un-ignored it. Mirrors the second branch.
	// `_should_skip_data_point` (`model/__init__.py:180-189`). Without
	// this pass INTERNAL-flagged parameters such as INSTALL_TEST surface
	// as full data points despite Python suppressing them.
	p.applyInternalParameterMarks(interfaceID)
	// Force NoCreate on every parameter whose OPERATIONS bitmask carries
	// neither EVENT nor WRITE (e.g. NEXT_TRANSMISSION, OPS=0): the reference
	// stack's first skip branch (`_should_skip_data_point`,
	// model/__init__.py:183-184) drops read-only/no-op parameters that carry no
	// user-actionable state. Without this pass they surface as standalone
	// sensors the direct-CCU twin never creates.
	p.applyNoEventNoWriteMarks(interfaceID)
	// Close the channel lifecycle: build event groups from all registered
	// generic events so EventGroups() returns a populated slice and the
	// MQTT/REST surfaces can iterate over them. Must run after every DP,
	// custom-DP, and event attachment step above.
	p.finalizeChannelInit(interfaceID)
	return nil
}

// restampDataPointNames recomputes the cached name quadruple and the
// multi-channel flag for every data point of interfaceID.
//
// [device.BuildDataPointName] appends the disambiguating " chN" postfix
// only when a *sibling* channel already exposes the same parameter, and
// that answer is derived from the data points the channels currently
// hold. hydrateParamset names a data point before it puts it on the
// channel, and channels are hydrated one after another, so a
// two-channel switch names both its STATE points while no sibling
// carries STATE yet and both cache the same postfix-free name. The
// third channel onwards sees two siblings and does get a postfix, which
// is why the damage shows as asymmetric names rather than none.
//
// Re-stamping once the whole interface is hydrated — and still before
// custom-DP materialisation, value seeding and any north-bound
// subscription — is what makes the cached name agree with the live
// recompute the REST data-point handler performs per request. MQTT
// discovery reads the cache, REST recomputes, and the two must not
// disagree about the name of the same data point.
func (p *DevicePipeline) restampDataPointNames(interfaceID string) {
	if p.unit == nil {
		return
	}
	for _, d := range p.devicesFor(interfaceID) {
		for _, ch := range d.Channels() {
			restampChannelDataPointNames(ch, ch.DataPoints())
			restampChannelDataPointNames(ch, ch.MasterDataPoints())
		}
	}
}

// restampChannelDataPointNames re-runs the name factory for dps, all of
// which belong to ch.
func restampChannelDataPointNames(ch *device.Channel, dps []device.ParameterDataPoint) {
	for _, dp := range dps {
		init, ok := dp.(namingInitializer)
		if !ok {
			continue
		}
		parameter := dp.DataPointKey().Parameter
		init.SetNameData(device.BuildDataPointName(ch, parameter, ""))
		init.SetIsInMultipleChannels(ch.IsParameterInMultipleChannels(parameter))
	}
}

// finalizeChannelInit calls [device.Channel.FinalizeInit] on every channel
// of every device belonging to interfaceID. This closes the channel lifecycle
// by building event groups from all registered generic events so that
// EventGroups() returns a populated slice. Must be called after all DP,
// custom-DP, and event attachment passes have completed.
func (p *DevicePipeline) finalizeChannelInit(interfaceID string) {
	if p.unit == nil {
		return
	}
	centralName := p.unit.Name()
	for _, d := range p.devicesFor(interfaceID) {
		for _, ch := range d.Channels() {
			ch.FinalizeInit(centralName)
		}
	}
}

// applyInternalParameterMarks walks every channel of every device on
// interfaceID and runs
// [visibility.ApplyInternalParameterMarksWithDecider]. The decider
// instance is passed through so built-in `unIgnoreParametersByDevice`
// rules (HM-Sec-Key/HM-Sec-Win ERROR, HmIP-DLD/HmIP-DLP ERROR_JAMMED)
// bypass the FLAG.INTERNAL filter without forcing the per-DP
// `IsUnIgnored()` flag (which mirrors `_is_un_ignored` with
// `custom_only=True`). Idempotent.
func (p *DevicePipeline) applyInternalParameterMarks(interfaceID string) {
	if p.unit == nil {
		return
	}
	var decider *visibility.ParameterDecider
	if p.visibility != nil {
		decider = p.visibility.Parameter()
	}
	for _, d := range p.devicesFor(interfaceID) {
		visibility.ApplyInternalParameterMarksWithDecider(d, decider)
	}
}

// applyNoEventNoWriteMarks walks every channel of every device on interfaceID
// and runs [visibility.ApplyNoEventNoWriteMarks], force-marking every VALUES
// parameter whose OPERATIONS bitmask has neither EVENT nor WRITE to
// NoCreate. Idempotent; preserves custom-DP DataPoint promotions and operator
// un-ignore overrides.
func (p *DevicePipeline) applyNoEventNoWriteMarks(interfaceID string) {
	if p.unit == nil {
		return
	}
	for _, d := range p.devicesFor(interfaceID) {
		visibility.ApplyNoEventNoWriteMarks(d)
	}
}

// applyUnIgnoredMarks walks every channel of every device on
// interfaceID and propagates the visibility decider's un-ignored
// answer onto each VALUES DP via [BaseDataPointFields.MarkUnIgnored].
// No-op when the pipeline carries no decider (test fixtures).
func (p *DevicePipeline) applyUnIgnoredMarks(interfaceID string) {
	if p.unit == nil || p.visibility == nil {
		return
	}
	decider := p.visibility.Parameter()
	for _, d := range p.devicesFor(interfaceID) {
		visibility.ApplyUnIgnoredMarks(d, decider)
	}
}

// applyForceSensorMarks walks every channel of every device on interfaceID
// and runs [visibility.ApplyForceSensorMarks].
func (p *DevicePipeline) applyForceSensorMarks(interfaceID string) {
	if p.unit == nil {
		return
	}
	for _, d := range p.devicesFor(interfaceID) {
		visibility.ApplyForceSensorMarks(d)
	}
}

// applyIgnoredParameterMarks walks every channel of every device on
// interfaceID and force-usages every DP whose parameter the
// visibility decider reports as ignored (`ignoredParameters`
// wildcard pattern / `ignoreParametersByDevice`
// `RELEVANT_MASTER_PARAMSETS_BY_DEVICE` off-list). Mirrors the
// "DP exists but is invisible" model — openccu-loom always creates
// the DP (every wire parameter gets one), the mark only governs
// the UI / MQTT surface.
//
// Idempotent. Honours un-ignored marks via the decider chain.
func (p *DevicePipeline) applyIgnoredParameterMarks(interfaceID string) {
	if p.unit == nil || p.visibility == nil {
		return
	}
	decider := p.visibility.Parameter()
	for _, d := range p.devicesFor(interfaceID) {
		visibility.ApplyIgnoredParameterMarks(d, decider)
	}
}

// applyHiddenParameterMarks walks every channel of every device on
// interfaceID and force-usages [visibility.hiddenParameters] entries to
// NoCreate. Runs after un-ignored marks so an operator override
// (un_ignore.txt) re-promotes a hidden parameter before the suppression
// fires.
func (p *DevicePipeline) applyHiddenParameterMarks(interfaceID string) {
	if p.unit == nil {
		return
	}
	var decider *visibility.ParameterDecider
	if p.visibility != nil {
		decider = p.visibility.Parameter()
	}
	for _, d := range p.devicesFor(interfaceID) {
		visibility.ApplyHiddenParameterMarksWithDecider(d, decider)
	}
}

// applyClickEventMarks decides the usage of click-event parameters
// (PRESS_SHORT, PRESS_LONG, PRESS, …) on non-virtual-remote devices, mirroring
// the reference stack's two-object model: each click parameter is BOTH a button
// (DpButton) and a keypress event, each with its own usage. The daemon carries
// one data point per parameter, so its single usage is mapped as:
//
//   - data_point — button entity AND keypress event (data_point is not a
//     suppressed event-group usage, so the event survives);
//   - event      — no button, keypress event only;
//   - ignored    — neither (left in place by an earlier visibility pass).
//
// Button-vs-event follows the reference type resolver: a click parameter
// becomes a button only when it is WRITABLE (its OPERATIONS bitmask carries the
// WRITE bit — e.g. a HM-RC KEY channel's PRESS_SHORT, OPS=6). A purely
// event-driven press (OPS=4, every KEY_TRANSCEIVER / MULTI_MODE_INPUT_TRANSMITTER
// transmitter and the central/long-press parameters) resolves to a read-only
// click and never spawns a button — it stays an event source only. Writable
// presses on devices that suppress generic data points (a custom-DP profile that
// does not allow undefined generics — e.g. a blind actuator's local KEY
// channels) are withheld from the button surface, matching the reference base
// usage NO_CREATE-for-button-but-EVENT-for-event — UNLESS the custom-DP pipeline
// explicitly promoted the parameter through additional_data_points (forced
// data_point), as the dimmer-with-inputs profiles do for their local KEY channel
// presses; such a promotion is preserved and keeps its button.
//
// An existing Ignored mark (the operation-mode gate hid the parameter in the
// current mode, or a hidden/ignored pass suppressed it) is preserved: such a
// parameter has neither a button nor an event.
//
// Runs after [applyChannelOperationModeGating] and the ignored/hidden passes, so
// it re-decides the gate's visible-parameter verdict by writability and respects
// their suppression marks.
func (p *DevicePipeline) applyClickEventMarks(interfaceID string) {
	if p.unit == nil {
		return
	}
	for _, d := range p.devicesFor(interfaceID) {
		if d.IsVirtualRemote() {
			continue
		}
		// The reference base usage withholds the generic button when the device
		// has a bound custom-DP profile and does not allow undefined generics.
		suppressGenericButton := len(d.CustomDataPoints()) > 0 && !d.AllowUndefinedGenericDataPoints()
		for _, ch := range d.Channels() {
			for _, dp := range ch.DataPoints() {
				param := dp.Parameter()
				if !param.IsClickEvent() {
					continue
				}
				// IGNORE_DEVICES_FOR_DATA_POINT_EVENTS (HmIP-PS* click
				// parameters) are left untouched: the reference stack neither
				// fires their events nor spawns their buttons, and a custom-DP
				// suppression may already have marked them NoCreate.
				if visibility.IsParameterIgnoredForDataPointEvent(d.Model, param) {
					continue
				}
				setter, ok := dp.(interface {
					SetForcedUsage(hmenum.DataPointUsage)
				})
				if !ok {
					continue
				}
				forced, hasForced := hmenum.DataPointUsage(""), false
				if reader, ok := dp.(interface {
					ForcedUsage() (hmenum.DataPointUsage, bool)
				}); ok {
					forced, hasForced = reader.ForcedUsage()
				}
				// Preserve an Ignored verdict from the operation-mode gate (the
				// parameter is hidden in the current mode) or a hidden/ignored
				// pass: it has neither a button nor an event.
				if hasForced && forced == hmenum.DataPointUsageIgnored {
					continue
				}
				// A purely event-driven press (OPS without the WRITE bit) is never a
				// button: it resolves to a read-only click and surfaces as an event
				// source only. This also overrides the operation-mode gate's
				// visible-parameter data_point verdict for event-only transmitters.
				if !dp.ParameterData().Operations.IsWritable() {
					setter.SetForcedUsage(hmenum.DataPointUsageEvent)
					continue
				}
				// Writable press on a device that suppresses generic data points
				// (custom-DP profile, no undefined generics): keep the button only
				// when the custom-DP pipeline explicitly promoted it through
				// additional_data_points (forced data_point); otherwise withhold the
				// button and keep the keypress event.
				if suppressGenericButton {
					if hasForced && forced == hmenum.DataPointUsageDataPoint {
						continue
					}
					setter.SetForcedUsage(hmenum.DataPointUsageEvent)
					continue
				}
				setter.SetForcedUsage(hmenum.DataPointUsageDataPoint)
			}
		}
	}
}

// applyChannelOperationModeGating walks every channel of every device on
// interfaceID and runs [visibility.ApplyChannelOperationModeGating] against
// it. Idempotent.
func (p *DevicePipeline) applyChannelOperationModeGating(interfaceID string) {
	if p.unit == nil {
		return
	}
	for _, d := range p.devicesFor(interfaceID) {
		visibility.ApplyChannelOperationModeGatingDevice(d)
	}
}

// seedValues runs fetch_all_device_data on the CCU and applies the
// resulting values to the matching data points on this central. The
// script returns a single JSON object keyed by `<iface>.<channel>.<param>`
// (URL-encoded); every recognised key is resolved via the ModelRegistry
// and the value is passed to [generic.DataPoint.OnWireValue] for
// typed coercion.
func (p *DevicePipeline) seedValues(
	ctx context.Context,
	interfaceID string,
	runner *rega.Runner,
	logger *slog.Logger,
) error {
	var values map[string]json.RawMessage
	if err := runner.RunJSON(ctx, hmenum.RegaScriptFetchAllDeviceData,
		map[string]string{"interface": interfaceID}, &values); err != nil {
		return err
	}
	applied := 0
	for rawKey, raw := range values {
		key, err := url.QueryUnescape(rawKey)
		if err != nil {
			continue
		}
		parts := strings.SplitN(key, ".", 3)
		if len(parts) != 3 {
			continue
		}
		channelAddr, parameter := parts[1], parts[2]
		// The script emits every DP that carries a valid Timestamp(), which a
		// button acquires on its first press and keeps forever. Seeding that
		// value would mark the data point observed and hand the boot-time
		// snapshot a keypress to replay — see the edge-trigger exclusion in
		// the values cache for the same reason.
		if hmenum.IsEdgeTriggerParameter(hmenum.Parameter(parameter)) {
			continue
		}
		deviceAddr := deviceAddressOf(channelAddr)
		dev, ok := p.unit.ModelRegistry.Get(deviceAddr)
		if !ok {
			continue
		}
		ch := dev.Channel(channelAddr)
		if ch == nil {
			continue
		}
		dp := ch.Parameter(hmenum.Parameter(parameter))
		if dp == nil {
			continue
		}
		// Unmarshal the raw value; the script emits bare booleans,
		// numbers, or double-quoted strings — json.Unmarshal into any
		// handles all three without further branching.
		var v any
		if err := json.Unmarshal(raw, &v); err != nil {
			continue
		}
		// String-valued data points are wrapped in UriEncode() by the
		// script (fetch_all_device_data.fn) so an embedded quote or
		// control character cannot break the surrounding JSON envelope —
		// only booleans and numbers are emitted unencoded. That encoding
		// survives json.Unmarshal untouched (it is just the string's
		// content), so a string value must be decoded here the same way
		// the key already is above; skipping it left values such as an
		// IP_ADDRESS data point's "172.18.4.40" seeded into the model as
		// the literal "172%2E18%2E4%2E40".
		//
		// Decoding goes through the package's canonical ReGa decoder, not a
		// bare unescape: the CCU emits ISO-8859-1, so "Sp%FCle" unescapes to
		// a raw 0xFC byte that is invalid UTF-8. The value is seeded into the
		// live model and re-encoded by every north-bound plane, where
		// json.Marshal replaces it with U+FFFD — irreversible corruption of a
		// value the hub path renders correctly.
		if s, ok := v.(string); ok {
			v = decodeRegaField(s)
		}
		if setter, ok := dp.(interface{ OnWireValue(any) bool }); ok && setter.OnWireValue(v) {
			applied++
		}
	}
	logger.Info("pipeline.seed.ok",
		slog.String("interface", interfaceID),
		slog.Int("applied", applied),
		slog.Int("delivered", len(values)))
	return nil
}

// materialiseCustomDataPoints walks every device on interfaceID and invokes
// [custom.CreateCustomDataPoints] against the process-wide
// [custom.DefaultRegistry]. The call is best-effort: every per-device error
// is logged at WARN level so the daemon keeps materialising the rest of the
// registry even when a single device's constructor or profile mapping
// misbehaves.
//
// The registry is the [DefaultRegistry] populated by the [builtins] blank
// import — every sub-package's `init()` has run by the time this method
// executes. suppressUndefinedGenericDataPoints walks every device on
// interfaceID and runs [custom.SuppressUndefinedGenericDataPoints] against
// it. Idempotent.
func (p *DevicePipeline) suppressUndefinedGenericDataPoints(interfaceID string) {
	if p.unit == nil {
		return
	}
	for _, d := range p.devicesFor(interfaceID) {
		// SuppressUndefinedGenericDataPoints relies on the per-DP
		// `IsUnIgnored()` mark (custom_only=True). Built-in
		// `unIgnoreParametersByDevice` rules are deliberately NOT
		// Honoured here for snapshot-symmetry
		// (Python emits `is_un_ignored=False` for built-in rules)
		// the resulting DPs surface as `usage=no_create` which matches
		// Users who want the
		// built-in rules to surface DPs as data_point should add the
		// parameter to their `un_ignore.txt`.
		custom.SuppressUndefinedGenericDataPoints(d)
	}
}

func (p *DevicePipeline) materialiseCustomDataPoints(interfaceID string, logger *slog.Logger) {
	if p.unit == nil {
		return
	}
	customReg := custom.DefaultRegistry()
	for _, d := range p.devicesFor(interfaceID) {
		// Stamp the per-central rendering toggles before materialising
		// so the light / cover factories read the operator's choice.
		d.SetCustomDPBehavior(p.cdpLightLastBrightness, p.cdpUseGroupChannelForCover)
		if err := custom.CreateCustomDataPoints(d, customReg); err != nil {
			if logger != nil {
				logger.Warn("custom data points materialization had errors",
					slog.String("device", d.Address),
					slog.String("model", d.Model),
					slog.String("err", err.Error()))
			}
			// No hard fail — the device still goes online with whatever
			// custom DPs landed plus all generic DPs.
		}
		// Post-discovery hook: bind cross-channel POWER /
		// ENERGY_COUNTER sources to any Switch custom DP. Runs after
		// the materialise loop so every channel's generic DPs are
		// already live and the sibling lookup succeeds for HmIP-PSM-
		// style devices where the SWITCH_VIRTUAL_RECEIVER channel and
		// the energy-counter channel are distinct.
		switchdev.AttachPowerEnergySources(d)
	}
}

// materialiseCalculatedDataPoints walks every channel of every device on
// interfaceID and invokes [calculated.CreateCalculatedDataPoints] against it.
// Each attached sensor is wired through the channel's own
// [device.Channel.AttachCalculatedDataPoint], which calls `Subscribe` so the
// sensor's source-DP listeners are installed.
//
// In addition to the per-sensor wiring this method bridges the calculated
// sensor's float / bool sink into the central event bus as a
// [hmevent.DataPointValueChangedEvent], so the existing EventBridge fan-out
// (WebSocket + MQTT discovery + raw plane) emits topics for derived sensors
// without each adapter needing a separate enumeration step. The bridged event
// uses the calculated parameter id as the Parameter (e.g. `DEW_POINT`) and
// the channel address as the address — adapters can therefore treat derived
// sensors uniformly with wire-level data points.
func (p *DevicePipeline) materialiseCalculatedDataPoints(interfaceID string, logger *slog.Logger) {
	if p.unit == nil {
		return
	}
	bus := p.unit.EventBus
	centralName := p.unit.Name()
	for _, d := range p.devicesFor(interfaceID) {
		for _, ch := range d.Channels() {
			sensors := calculated.CreateCalculatedDataPoints(ch, d.Model)
			if len(sensors) == 0 {
				continue
			}
			for _, s := range sensors {
				bridgeCalculatedSensorToBus(bus, centralName, d.InterfaceID, ch.Address, s, logger)
			}
		}
	}
}

// materialiseCombinedDataPoints walks every channel of every device on
// interfaceID and, for each combined DP attached to the channel, wires
// [BridgeCombinedDataPoint] so value changes propagate as
// [hmevent.DataPointValueChangedEvent] on the central event bus. This makes
// WS / MQTT / REST consumers receive live combined-state updates for
// LevelCombined (blind level+slats) and HSColor (hue+saturation) without each
// adapter needing its own enumeration step.
//
// The combined DPs are already attached to channels by the custom-DP
// constructors (NewBlind / NewColorLight); this pass only installs the
// event-bus bridge. The unsubscribe is intentionally not captured: combined
// DP lifetime equals channel lifetime, and re-attach replaces the registration.
func (p *DevicePipeline) materialiseCombinedDataPoints(interfaceID string, logger *slog.Logger) {
	if p.unit == nil {
		return
	}
	bus := p.unit.EventBus
	for _, d := range p.devicesFor(interfaceID) {
		for _, ch := range d.Channels() {
			for _, rawDP := range ch.CombinedDataPoints() {
				dp, ok := rawDP.(CombinedDataPoint)
				if !ok {
					continue
				}
				param := rawDP.DataPointKey().Parameter
				BridgeCombinedDataPoint(bus, dp, interfaceID, ch.Address, param, logger)
			}
		}
	}
}

// bridgeCalculatedSensorToBus registers an OnUpdate listener on the
// sensor's underlying [generic.Sensor] sink (float64 for the climate
// + voltage sensors, bool for the derived-binary sensor) and
// translates each emission into a [hmevent.DataPointValueChangedEvent]
// on bus. The event reuses the calculated parameter id as the wire
// `Parameter` so MQTT topics render as
// `<base>/<central>/<iface>/<address>/<channel>/<calc_param>`
// uniform with regular data point topics.
//
// Returning the unsubscribe is intentionally skipped: the sensors'
// lifetime equals the channel's lifetime, and the channel re-attaches
// (via [device.Channel.AttachCalculatedDataPoint]) replace the
// listener registration on next materialise.
func bridgeCalculatedSensorToBus(
	bus *events.Bus,
	centralName, interfaceID, channelAddr string,
	s calculated.Sensor,
	logger *slog.Logger,
) {
	if bus == nil || s == nil {
		return
	}
	param := string(s.CalculatedParameter())
	publish := func(value any) {
		newVal, err := hmtypes.NewParamValue(value)
		if err != nil {
			if logger != nil {
				logger.Debug("calculated.bridge.skip",
					slog.String("central", centralName),
					slog.String("channel", channelAddr),
					slog.String("param", param),
					slog.String("err", err.Error()))
			}
			return
		}
		events.Publish(bus, hmevent.DataPointValueChangedEvent{
			Base: hmevent.NewBase(),
			Key: hmtypes.DataPointKey{
				InterfaceID:    interfaceID,
				ChannelAddress: channelAddr,
				ParamsetKey:    hmenum.ParamsetKeyValues,
				Parameter:      param,
			},
			OldValue: hmtypes.NoneValue(),
			NewValue: newVal,
		})
	}
	switch sensor := s.(type) {
	case interface {
		OnUpdate(fn func(old, next float64)) func()
	}:
		sensor.OnUpdate(func(_, next float64) { publish(next) })
	case interface {
		OnUpdate(fn func(old, next bool)) func()
	}:
		sensor.OnUpdate(func(_, next bool) { publish(next) })
	default:
		// Unknown sensor variant — log + drop silently. The sensor
		// still emits to its own subscribers; only the bus bridge is
		// missing.
		if logger != nil {
			logger.Debug("calculated.bridge.unsupported",
				slog.String("param", param))
		}
	}
}

// rollbackPublisher is the optional surface a data point exposes to
// route its rollback events onto the central event bus. Every
// [generic.DataPoint] satisfies it via OnAnyRollback — the type-erased
// counterpart lets heterogeneous subscribers hold a single registration
// across multiple typed DPs.
type rollbackPublisher interface {
	OnAnyRollback(fn func(reason generic.RollbackReason, rolledBack, restored any, restoredSet bool)) func()
}

// bridgeDataPointRollbacksToBus walks every VALUES + MASTER DP on
// every channel of dev and registers an OnAnyRollback callback that
// publishes a [hmevent.DataPointOptimisticRolledBackEvent] on bus.
// Mirrors Python `data_point.py:1493-1504 _publish_rollback_event`,
// which emits the same event via `event_bus.publish_sync`. North-bound
// consumers (REST long-poll, MQTT) can subscribe to surface the
// rollback to operators.
//
// No-op when bus or dev is nil. The returned unsubscribe is
// intentionally dropped: rollback subscriptions live as long as the
// device — re-ingest after a device-disappear/reappear wipes the DP
// objects, so a fresh batch of subscriptions is wired automatically.
func bridgeDataPointRollbacksToBus(bus *events.Bus, dev *device.Device) {
	if bus == nil || dev == nil {
		return
	}
	for _, ch := range dev.Channels() {
		for _, dp := range ch.DataPoints() {
			pub, ok := dp.(rollbackPublisher)
			if !ok {
				continue
			}
			key := dp.DataPointKey()
			pub.OnAnyRollback(func(reason generic.RollbackReason, rolledBack, restored any, restoredSet bool) {
				sent, _ := hmtypes.NewParamValue(rolledBack)
				var present hmtypes.ParamValue
				if restoredSet {
					present, _ = hmtypes.NewParamValue(restored)
				} else {
					present = hmtypes.NoneValue()
				}
				events.Publish(bus, hmevent.DataPointOptimisticRolledBackEvent{
					Base:    hmevent.NewBase(),
					Key:     key,
					Reason:  hmenum.RollbackReason(reason),
					Sent:    sent,
					Present: present,
				})
			})
		}
		for _, dp := range ch.MasterDataPoints() {
			pub, ok := dp.(rollbackPublisher)
			if !ok {
				continue
			}
			key := dp.DataPointKey()
			pub.OnAnyRollback(func(reason generic.RollbackReason, rolledBack, restored any, restoredSet bool) {
				sent, _ := hmtypes.NewParamValue(rolledBack)
				var present hmtypes.ParamValue
				if restoredSet {
					present, _ = hmtypes.NewParamValue(restored)
				} else {
					present = hmtypes.NoneValue()
				}
				events.Publish(bus, hmevent.DataPointOptimisticRolledBackEvent{
					Base:    hmevent.NewBase(),
					Key:     key,
					Reason:  hmenum.RollbackReason(reason),
					Sent:    sent,
					Present: present,
				})
			})
		}
	}
}

// dpAvailabilitySetter is the construction-time setter contract for the
// availability provider slot. Every type that embeds
// [datapoint.BaseDataPointFields] satisfies this via promotion.
type dpAvailabilitySetter interface {
	SetAvailabilityProvider(fn func() bool)
}

// updatePublisher is the optional surface a data point exposes to route its
// value changes onto the central event bus. Every [generic.DataPoint]
// satisfies it via OnAnyUpdate — the type-erased counterpart lets a single
// registration span the typed specialisations.
type updatePublisher interface {
	OnAnyUpdate(fn func(old, next any)) func()
}

// bridgeMasterDataPointToBus publishes a [hmevent.DataPointValueChangedEvent]
// carrying the MASTER paramset key whenever dp's value changes.
//
// MASTER is a read/write north-bound plane: the boot snapshot declares each
// MASTER data point the visibility rules expose as a writable HA entity with
// `optimistic: false` plus its retained `<addr>/<channel>/master/<param>` slot
// topic, and writes arrive from MQTT `.../master/<param>/set`, from REST/MCP
// and from the config edit session. Nothing re-published the state after a
// write: the CCU emits no event() callback for a MASTER write, and the only
// runtime producer of the value-changed event is the CCU push path, which
// hard-codes the VALUES key. The HA entity therefore snapped back to the boot
// value after every write and stayed there until the daemon restarted, and a
// second UI session never saw the change either.
//
// Hooking the data point rather than one write path covers every producer at
// once — the paramset domain's post-write re-read, the optimistic write of the
// MQTT command sink, the CONFIG_PENDING settle refresh and the classic-HM
// MasterPoller all land on the same data point.
//
// Only real changes are forwarded. Those refreshes re-apply the whole paramset
// and fire the update callback for every parameter whether or not its value
// moved; publishing those would put one bus event per configuration parameter
// per channel on the wire after every settle.
//
// The returned unsubscribe is intentionally dropped, for the same reason
// [bridgeDataPointRollbacksToBus] drops it: the subscription lives as long as
// the data point, and a re-ingest builds fresh data points that are wired
// again.
func bridgeMasterDataPointToBus(bus *events.Bus, dp device.ParameterDataPoint) {
	pub, ok := dp.(updatePublisher)
	if !ok {
		return
	}
	key := dp.DataPointKey()
	if key.ParamsetKey != hmenum.ParamsetKeyMaster {
		return
	}
	pub.OnAnyUpdate(func(old, next any) {
		newVal, err := hmtypes.NewParamValue(next)
		if err != nil {
			return
		}
		oldVal, oldErr := hmtypes.NewParamValue(old)
		if oldErr != nil {
			oldVal = hmtypes.NoneValue()
		}
		if oldVal.Equal(newVal) {
			return
		}
		events.Publish(bus, hmevent.DataPointValueChangedEvent{
			Base:     hmevent.NewBase(),
			Key:      key,
			OldValue: oldVal,
			NewValue: newVal,
		})
	})
}

// weekProfileBusPublisher implements [datapoint.EventPublisher] for
// weekprofile ProfileDataPoints. On each [PublishUpdate] call it fires a
// [hmevent.WeekProfileChangedEvent] on the event bus.
//
// Nothing subscribes to that event. The signal that actually reaches
// MQTT is the data point's own change callback, which the event bridge
// registers on — see eventbridge.go's ProfileDataPoint OnChange wiring —
// and that callback only publishes to MQTT: there is no WS schedule
// broadcast, so an SPA schedule view that is already open does not
// learn about a CCU-side or second-operator schedule change until the
// user navigates away and back. The bus event is a second announcement
// of the same fact with no listener, kept because removing a published
// event type is a wire-visible change; it is declared as unconsumed in
// the contract test that enumerates them.
//
// The struct captures bus + centralName at construction time; the channel
// address is obtained from the ProfileDataPoint itself through the closure
// installed in wireDataPointLifecycle.
type weekProfileBusPublisher struct {
	bus         *events.Bus
	centralName string
	channelAddr func() string // lazy lookup so the ProfileDataPoint address is always current
}

// PublishUpdate implements [datapoint.EventPublisher]. The key argument is the
// DP UniqueID (ignored here — channel address is resolved via channelAddr).
func (w *weekProfileBusPublisher) PublishUpdate(_ context.Context, _ string, _ any) {
	events.Publish(w.bus, hmevent.WeekProfileChangedEvent{
		Base:           hmevent.NewBase(),
		CentralName:    w.centralName,
		ChannelAddress: w.channelAddr(),
	})
}

// wireDataPointLifecycle walks every channel of every device in the interface
// scope and installs two lifecycle hooks on each DP:
//
//  1. SetAvailabilityProvider — wires the DP's Available() to the owning
//     device's Available() so MQTT/REST surfaces the device's radio
//     reachability rather than a hard-coded true.
//
//  2. SetPublisher (weekprofile DPs only) — installs a bus publisher that
//     fires [hmevent.WeekProfileChangedEvent] on [weekprofile.ProfileDataPoint.FireScheduleUpdated].
//     Generic DPs already publish changes via their OnUpdate callback bridge
//     (see bridgeDataPointRollbacksToBus + bridgeCalculatedSensorToBus) and
//     do not need a separate SetPublisher path.
//
//  3. OnAnyUpdate (MASTER DPs only) — forwards configuration-value changes
//     onto the bus so the MQTT master state topic and the WebSocket stream
//     follow a write. VALUES changes reach the bus from the CCU push path;
//     MASTER has no such callback, so the data point is the only place the
//     change can be observed. See [bridgeMasterDataPointToBus].
//
// It must run after hydration: the MASTER seed pushes the CCU's current
// configuration through the same callback, and wiring earlier would announce
// the whole paramset of every channel as a change at boot.
func wireDataPointLifecycle(bus *events.Bus, centralName string, dev *device.Device) {
	if bus == nil || dev == nil {
		return
	}
	avail := dev.Available
	for _, ch := range dev.Channels() {
		// Wire availability on all VALUES DPs.
		for _, dp := range ch.DataPoints() {
			if as, ok := dp.(dpAvailabilitySetter); ok {
				as.SetAvailabilityProvider(avail)
			}
		}
		// Wire availability on all MASTER DPs, and forward their changes.
		for _, dp := range ch.MasterDataPoints() {
			if as, ok := dp.(dpAvailabilitySetter); ok {
				as.SetAvailabilityProvider(avail)
			}
			bridgeMasterDataPointToBus(bus, dp)
		}
		// Wire the weekprofile ProfileDataPoint (not a ParameterDataPoint; held
		// separately via Channel.WeekProfile). Wire both availability and the bus
		// publisher so FireScheduleUpdated pushes WeekProfileChangedEvent.
		if wp := ch.WeekProfile(); wp != nil {
			wp.SetAvailabilityProvider(avail)
			addr := wp.Address()
			wp.SetPublisher(&weekProfileBusPublisher{
				bus:         bus,
				centralName: centralName,
				channelAddr: func() string { return addr },
			})
		}
	}
}

// hydrateDataPoints walks every device that belongs to interfaceID,
// loads the VALUES paramset per channel, and builds typed data points
// via the shared resolver. Channels without a paramset (CUxD virtual
// channels, legacy BidCos channels) are skipped with a debug log.
func (p *DevicePipeline) hydrateDataPoints(
	ctx context.Context,
	interfaceID string,
	b backends.Operations,
	writer ValueWriter,
	logger *slog.Logger,
) error {
	if b == nil {
		return nil
	}
	bw := newBoundWriter(p.unit.Name(), interfaceID, writer)
	for _, d := range p.devicesFor(interfaceID) {
		// Hydrate the device-root MASTER paramset first. Classic HM thermostats
		// (HM-CC-RT-DN family) carry their week-profile schedule on the device
		// address itself (no `:N` suffix); without this pass those schedules would
		// never reach the model.
		p.hydrateDeviceRoot(ctx, interfaceID, d, b, bw, logger)
		for _, ch := range d.Channels() {
			p.hydrateChannel(ctx, interfaceID, d, ch, b, bw, logger)
		}
	}
	return nil
}

// hydrateDeviceRoot loads the device-level MASTER paramset (no
// channel suffix) and stamps its DPs onto the synthetic root channel
// returned by [device.Device.EnsureRootChannel]. Best-effort: a
// failed paramset description is debug-logged and the device
// continues — most devices have no device-level paramset and the
// CCU returns an empty / "channel-unknown" error for them.
func (p *DevicePipeline) hydrateDeviceRoot(
	ctx context.Context,
	interfaceID string,
	d *device.Device,
	b backends.Operations,
	bw generic.Writer,
	logger *slog.Logger,
) {
	if d == nil || b == nil {
		return
	}
	// Probe first via GetParamsetDescription — when the device has
	// no device-level MASTER paramset (the common case) this
	// returns an error and we skip the rest cheaply without
	// allocating a root channel that will never carry a DP.
	desc, err := b.GetParamsetDescription(ctx, d.Address, hmenum.ParamsetKeyMaster)
	if err != nil || len(desc) == 0 {
		if logger != nil && err != nil {
			logger.Debug("pipeline.device_root.skip",
				slog.String("address", d.Address),
				slog.String("err", err.Error()))
		}
		return
	}
	root := d.EnsureRootChannel()
	root.SetCentralName(p.unit.Name())
	// Install the write + refresh dispatchers BEFORE the data points are
	// built: the data points capture their write path at construction time,
	// and [dataPointWriter] can only hand out the lock-enforcing wrapper once
	// the channel has a writer.
	if bw != nil {
		root.SetWriter(&channelWriterAdapter{bw: bw, backend: b})
		root.SetRefresher(b)
	}
	p.hydrateParamset(ctx, interfaceID, root, b, dataPointWriter(root, bw), hmenum.ParamsetKeyMaster, logger)
	// Always assign: a re-ingest of an interface that lost its poller must
	// clear the stale hook rather than keep the previous generation's.
	root.SetMasterRefreshHook(p.masterRefreshHooks.get(interfaceID))
}

func (p *DevicePipeline) hydrateChannel(
	ctx context.Context,
	interfaceID string,
	d *device.Device,
	ch *device.Channel,
	b backends.Operations,
	bw generic.Writer,
	logger *slog.Logger,
) {
	_ = d // reserved for future per-device labeling

	// Stamp the Unit name on the channel so custom-DP
	// constructors (valve, switch) can propagate it into the
	// generic.Spec.CentralName of any sub-DPs they allocate.
	if p.unit != nil {
		ch.SetCentralName(p.unit.Name())
	}

	// Wire the channel's write + refresh dispatchers so Channel.Set
	// SetMany / Refresh route through the correct backend without the
	// north-bound layer needing a direct backend reference.
	//
	// This runs BEFORE the paramsets are hydrated: the data points built
	// there capture their write path once, at construction time, and
	// [dataPointWriter] can only hand out the lock-enforcing wrapper once
	// the channel has a writer installed.
	if bw != nil && b != nil {
		ch.SetWriter(&channelWriterAdapter{bw: bw, backend: b})
		ch.SetRefresher(b)
	}
	dpWriter := dataPointWriter(ch, bw)
	p.hydrateParamset(ctx, interfaceID, ch, b, dpWriter, hmenum.ParamsetKeyValues, logger)
	p.hydrateParamset(ctx, interfaceID, ch, b, dpWriter, hmenum.ParamsetKeyMaster, logger)
	// Wire the post-MASTER-write hook of the interface this channel belongs
	// to. It is nil for HmIP channels (CONFIG_PENDING covers their refresh
	// path); assigning unconditionally also clears a hook left over from an
	// earlier generation of the same interface.
	ch.SetMasterRefreshHook(p.masterRefreshHooks.get(interfaceID))
}

// hydrateParamset loads one paramset (VALUES or MASTER) for the
// channel and registers the resolved data points on the appropriate
// side of the channel. Errors on the MASTER side are common on
// CUxD virtual channels and are logged at debug level.
//
// For MASTER, the pipeline additionally fetches the current values
// through `GetParamset` and seeds every data point with its observed
// value. VALUES values are seeded separately via the Rega
// `fetch_all_device_data` script in [seedValues]; MASTER is not part
// of that script's output so we call GetParamset directly.
func (p *DevicePipeline) hydrateParamset(
	ctx context.Context,
	interfaceID string,
	ch *device.Channel,
	b backends.Operations,
	bw generic.Writer,
	key hmenum.ParamsetKey,
	logger *slog.Logger,
) {
	paramset, err := b.GetParamsetDescription(ctx, ch.Address, key)
	if err != nil {
		if logger != nil {
			logger.Debug("pipeline.paramset.skip",
				slog.String("channel", ch.Address),
				slog.String("paramset", string(key)),
				slog.String("err", err.Error()))
		}
		return
	}
	// Apply paramset patches to the descriptor map BEFORE we build
	// Data points off the entries.
	// `ParamsetPatchMatcher.apply_patches` (`store/patches/paramset_patches.py`)
	// which is invoked at every paramset-description-store call. The
	// openccu-loom equivalent lives in the runtime's
	// `ParamsetRegistry.Add` path, but the device-hydration pipeline
	// builds DPs directly from the wire `paramset` map and would
	// otherwise bypass the patch step.
	// (HM-CC-VG-1 SET_TEMPERATURE wire MIN/MAX 5.0/30.0 → patched
	// 4.5/30.5 to match.
	if reg := p.unit.ParamsetReg; reg != nil {
		reg.ApplyPatches(ch.Device().Model, ch.Address, key, paramset)
		// Store the patched descriptor so the registry serves
		// channel-paramset reads for every hydrated channel (and, via
		// the persistence sink, fills the warm-boot descriptor cache)
		// instead of only for per-channel reloads. Put copies during
		// normalisation, so the map used for data-point construction
		// below stays untouched. Entity naming reads the device model
		// rather than the registry (Channel.IsParameterInMultipleChannels
		// walks the parent device's channels), so it is unaffected.
		reg.Put(hmtypes.ParseWireInterfaceID(interfaceID), ch.Address, key, paramset)
	}

	// Parameter-name set for the paired `<param>_STATUS` lookup below.
	// Built once so the per-parameter detection stays O(1).
	paramNames := make(map[string]struct{}, len(paramset))
	for name := range paramset {
		paramNames[name] = struct{}{}
	}

	for name := range paramset {
		pd := paramset[name]

		// MASTER OPERATIONS=0 → 3 fix.
		// Some CCU firmwares return OPERATIONS=0 for MASTER parameters,
		// which would cause them to be treated as neither readable nor
		// writable. Python fixes this inline:
		// if paramset_key == ParamsetKey.MASTER and parameter_data["OPERATIONS"] == 0:
		// parameter_data["OPERATIONS"] = 3
		// We apply the same fix here before building the data point.
		if key == hmenum.ParamsetKeyMaster && pd.Operations == hmenum.OperationsNone {
			pd.Operations = hmenum.OperationsRead | hmenum.OperationsWrite
		}

		// Attach an event source for every parameter that carries the EVENT
		// operation and classifies as one (PRESS_*, SEQUENCE_OK, ERROR*).
		// This runs before the data-point construction below and independently
		// of it, because the two are separate surfaces: a parameter can be
		// suppressed as a data point by a later visibility pass and still owe
		// a keypress event. `finalizeChannelInit` groups whatever has been
		// attached here once every channel is hydrated.
		//
		// Restricted to VALUES although the reference applies the same test to
		// both paramsets: [modevent.Source] keys itself as VALUES, so a MASTER
		// parameter would be attached under a key that names the wrong
		// paramset. No MASTER parameter carries EVENT in practice — the
		// OPERATIONS fix-up above grants read+write, never event — so the two
		// readings select the same parameters.
		if key == hmenum.ParamsetKeyValues && pd.Operations.IsEvent() {
			if src := modevent.NewSource(ch.Address, hmenum.Parameter(name)); src != nil {
				ch.AttachGenericEvent(src)
			}
		}

		// Skip week-profile slot parameters (P1_*, P2_*, …, P6_*) on MASTER. They
		// are the per-day per-slot fields of the CCU week-program (e.g.
		// P1_TEMPERATURE_MONDAY_1, P3_ENDTIME_TUESDAY_4). 6 profiles × 7 days × ~14
		// slots = 84+ parameters per climate channel that surface as individual
		// MQTT topics if we materialise them as DPs none of which is useful in
		// isolation. The week-profile is a single logical entity; it will surface
		// as one WeekProfileDataPoint in a follow-up wave (PR 6 deep work). For now
		// we drop these from MASTER hydration so neither the REST/UI nor
		// MQTT-Discovery sees ~84 ghost topics per thermostat.
		if key == hmenum.ParamsetKeyMaster && isWeekProfileSlotParameter(name) {
			// Spotting at least one P*_* slot parameter is the
			// authoritative signal that this channel carries a
			// week-profile schedule. Attach a single
			// [weekprofile.ProfileDataPoint] descriptor (idempotent
			// repeated calls just replace the previous attachment with
			// an equivalent one). The attached DP is what
			// [device.Device.HasWeekProfile] consults; the slot leaves
			// themselves are intentionally dropped here.
			attachWeekProfileToChannel(ch, p.unit.Name())
			continue
		}

		// No visibility gate here — openccu-loom's architecture says
		// every parameter becomes a DP. Filtering happens after the
		// fact via the visibility-mark passes (applyIgnoredParameterMarks,
		// applyHiddenParameterMarks, applyChannelOperationModeGating)
		// which set forced_usage=NoCreate so the UI / MQTT surface
		// stays clean while the daemon still knows about every wire
		// parameter (needed for diagnostics, custom-DP composition,
		// operator override via un_ignore.txt).

		dpKey, err := hmtypes.NewDataPointKey(interfaceID, ch.Address, key, name)
		if err != nil {
			continue
		}
		cfg := generic.Spec{
			Key:        dpKey,
			Descriptor: pd,
			Writer:     bw,
			// The device model drives the device-aware quantity lookup
			// ([generic.DataPoint.Quantity]) and the signature the data
			// point renders. Both documented it as pipeline-supplied while
			// nothing set it, so every per-model override — HmIP-SWDO.STATE
			// → window and its twelve siblings — resolved to QuantityNone
			// and signatures collided across device models.
			DeviceModel:   ch.Device().Model,
			CentralName:   p.unit.Name(),
			NoPushUpdates: !b.Capabilities().RPCCallback,
		}
		// HM-Sec-Key/HM-Sec-Win ERROR, HmIP-DLD/HmIP-DLP ERROR_JAMMED). Without
		// this propagation 4 lock-family devices lose their ERROR* DP — measured in
		// snapshot diff.
		parameterIsUnIgnored := false
		if p.visibility != nil {
			// customOnly=false → consult both user-provided and
			// built-in `unIgnoreParametersByDevice` rules
			// (e.g. HM-Sec-Key ERROR / HmIP-DLD ERROR_JAMMED).
			parameterIsUnIgnored = p.visibility.Parameter().IsUnIgnoredCustomOnly(
				ch.Device().Model, ch.Type, key, hmenum.Parameter(name), false,
			)
		}
		dp := resolveDataPointWithUnIgnore(cfg, parameterIsUnIgnored)
		if dp == nil {
			continue
		}
		// Pair the data point with its `<param>_STATUS` sibling and cache
		// that parameter's VALUE_LIST. The CCU reports a measurement's
		// quality as an ENUM index on the paired parameter, and
		// [generic.DataPoint.UpdateStatusFromWire] can only turn that index
		// into a ParameterStatus with the list in hand — without it every
		// OVERFLOW / UNDERFLOW / ERROR report is dropped and the base data
		// point keeps publishing an unusable reading as available.
		if statusName, hasStatus := generic.DetectStatusParameter(name, paramNames); hasStatus {
			if sp, ok := dp.(interface {
				SetStatusParameter(paramName string, valueList []string)
			}); ok {
				sp.SetStatusParameter(statusName, paramset[statusName].ValueList)
			}
		}
		// Cluster 1 — Cache the presentation surface on the DP at construction
		// time. Translation is intentionally not resolved here — the locale-aware
		// OCCU translation lives in the north-bound bridge layer and is applied
		// per-event. What gets cached are the structural strings (device name,
		// channel name, parameter name, multi-channel postfix, set/ state paths)
		// that never depend on locale and would otherwise be recomputed on every
		// MQTT event.
		if init, ok := dp.(namingInitializer); ok {
			init.SetNameData(device.BuildDataPointName(ch, name, ""))
			bucket := naming.BucketValues
			if key == hmenum.ParamsetKeyMaster {
				bucket = naming.BucketMaster
			}
			init.SetPathData(naming.NewDataPointPathData(
				p.unit.Name(), hmtypes.ParseWireInterfaceID(interfaceID),
				ch.Address, ch.Number, bucket, name,
			))
			init.SetIsInMultipleChannels(ch.IsParameterInMultipleChannels(name))
		}
		switch key { //nolint:exhaustive // only VALUES + MASTER are stored on channels
		case hmenum.ParamsetKeyValues:
			ch.Put(dp)
		case hmenum.ParamsetKeyMaster:
			ch.PutMaster(dp)
		}
	}
	if key == hmenum.ParamsetKeyMaster {
		p.seedMasterValues(ctx, ch, b, logger)
	}
}

// seedMasterValues loads the current MASTER-paramset values for ch
// and pushes them through [generic.DataPoint.OnWireValue] so the UI
// can display the current configuration.
//
// Cache-first: when a [sqlite.MasterValuesStore] is attached, the
// persisted snapshot is applied without issuing any RPC. This is the
// only way to survive a cold CCU+daemon-reboot without flooding the
// CCU duty-cycle (a freshly booted interface daemon validates each
// getParamset against the device by radio while it rebuilds its sync
// state from the on-disk file cache).
//
// Cache-miss / no store falls back to the CCU read; on success the
// values are persisted so subsequent restarts skip the RPC.
//
// A failed call is logged at debug level — the daemon keeps running
// with an empty MASTER view rather than aborting hydration.
func (p *DevicePipeline) seedMasterValues(
	ctx context.Context,
	ch *device.Channel,
	b backends.Operations,
	logger *slog.Logger,
) {
	interfaceID := ch.Device().InterfaceID
	if p.masterValues != nil && p.centralName != "" {
		if values, hit, err := p.masterValues.LoadChannel(ctx, p.centralName, interfaceID, ch.Address); err != nil {
			if logger != nil {
				logger.Debug("pipeline.master.values.cache_err",
					slog.String("channel", ch.Address),
					slog.String("err", err.Error()))
			}
		} else if hit {
			applied := p.applyMasterValues(ch, values)
			if logger != nil {
				logger.Debug("pipeline.master.values.cache_hit",
					slog.String("channel", ch.Address),
					slog.Int("applied", applied),
					slog.Int("delivered", len(values)))
			}
			return
		}
	}

	values, err := b.GetParamset(ctx, ch.Address, hmenum.ParamsetKeyMaster)
	if err != nil {
		if logger != nil {
			logger.Debug("pipeline.master.values.skip",
				slog.String("channel", ch.Address),
				slog.String("err", err.Error()))
		}
		return
	}
	applied := p.applyMasterValues(ch, values)
	if p.masterValues != nil && p.centralName != "" && len(values) > 0 {
		if err := p.masterValues.SaveChannel(ctx, p.centralName, interfaceID, ch.Address, values); err != nil && logger != nil {
			logger.Debug("pipeline.master.values.cache_save_err",
				slog.String("channel", ch.Address),
				slog.String("err", err.Error()))
		}
	}
	if logger != nil {
		logger.Debug("pipeline.master.values.ok",
			slog.String("channel", ch.Address),
			slog.Int("applied", applied),
			slog.Int("delivered", len(values)))
	}
}

// applyMasterValues writes the parameter→value map onto the channel's
// MASTER data points. Returns the number of values that landed (a
// parameter that no longer exists in the device profile is silently
// dropped — that handles schema-drift on the description side).
func (p *DevicePipeline) applyMasterValues(ch *device.Channel, values map[string]any) int {
	applied := 0
	for name, v := range values {
		dp := ch.MasterParameter(hmenum.Parameter(name))
		if dp == nil {
			continue
		}
		if setter, ok := dp.(interface{ OnWireValue(any) bool }); ok && setter.OnWireValue(v) {
			applied++
		}
	}
	return applied
}

// valueCacheRestorer is the optional interface a wire data point can
// implement to receive a cached value together with its persisted
// timestamps. The Phase-C lifecycle layer adds this method to the
// generic DataPoint so the restored value is tagged
// [sqlite.SourceCache] until a live event arrives.
//
// Pipeline falls back to OnWireValue when a data point does not
// implement the restorer yet — the value still lands, but without
// the source-tag distinction.
type valueCacheRestorer interface {
	RestoreCachedValue(v any, lastSeen, lastChanged time.Time) bool
}

// restoreValuesFromCache reads the persisted wire-DP snapshot for
// interfaceID and applies it onto the channel data points the pipeline
// has just built. Cache entries for parameters that no longer exist in
// the current paramset description are silently dropped (the GC pass
// will remove the dead row on its next run).
//
// Runs strictly between hydrateDataPoints and seedValues so the
// pipeline order is: paramset description → data points exist →
// cached values applied → live values overwrite.
func (p *DevicePipeline) restoreValuesFromCache(
	ctx context.Context, interfaceID string, logger *slog.Logger,
) {
	if p == nil || p.valuesCache == nil || p.centralName == "" || p.unit == nil || p.unit.ModelRegistry == nil {
		return
	}
	applied, skipped := 0, 0
	for _, dev := range p.devicesFor(interfaceID) {
		for _, ch := range dev.Channels() {
			if ch == nil {
				continue
			}
			rows, err := p.valuesCache.LoadChannel(ctx, p.centralName, interfaceID, ch.Address)
			if err != nil {
				if logger != nil {
					logger.Debug("pipeline.values_cache.load_err",
						slog.String("channel", ch.Address),
						slog.String("err", err.Error()))
				}
				continue
			}
			for _, row := range rows {
				// Rows written before edge-trigger parameters were excluded
				// from the cache are still on disk, so the restore side has
				// to reject them too — otherwise an existing installation
				// keeps replaying its last keypress on every boot until the
				// GC pass happens to clear the row.
				if hmenum.IsEdgeTriggerParameter(hmenum.Parameter(row.Parameter)) {
					continue
				}
				dp := ch.Parameter(hmenum.Parameter(row.Parameter))
				if dp == nil {
					skipped++
					continue
				}
				if restorer, ok := dp.(valueCacheRestorer); ok {
					if restorer.RestoreCachedValue(row.Value, row.LastSeenAt, row.LastChangedAt) {
						applied++
					} else {
						p.valuesCache.IncCastFailures(1)
					}
					continue
				}
				if setter, ok := dp.(interface{ OnWireValue(any) bool }); ok && setter.OnWireValue(row.Value) {
					applied++
				} else {
					p.valuesCache.IncCastFailures(1)
				}
			}
		}
	}
	p.valuesCache.IncRestoredRows(int64(applied))
	if logger != nil && (applied > 0 || skipped > 0) {
		logger.Info("pipeline.values_cache.restored",
			slog.String("interface", interfaceID),
			slog.String("central", p.centralName),
			slog.Int("applied", applied),
			slog.Int("skipped_unknown_parameter", skipped))
	}
}

// splitChannel reports whether addr is a channel address (contains
// ":<n>"). Returns (channel_no, true) on hit.
func splitChannel(addr string) (int, bool) {
	i := strings.LastIndexByte(addr, ':')
	if i < 0 {
		return 0, false
	}
	n, err := strconv.Atoi(addr[i+1:])
	if err != nil {
		return 0, false
	}
	return n, true
}

func channelNumber(addr string) int {
	if n, ok := splitChannel(addr); ok {
		return n
	}
	return 0
}
