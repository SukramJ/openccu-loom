// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package endpoint

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"

	"github.com/SukramJ/openccu-loom/internal/i18n"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/internal/north/matter/store"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
	"github.com/SukramJ/openccu-loom/pkg/interfaces"
)

// Config tunes the assembler. The zero value is *not* valid — at
// minimum, a non-zero VendorID + ProductID + non-empty NodeLabel
// must be supplied (the root BasicInformation cluster mandates them).
type Config struct {
	// VendorID is the bridge's IANA-assigned vendor identifier.
	VendorID uint16
	// ProductID is the bridge's vendor-assigned product identifier.
	ProductID uint16
	// NodeLabel is the user-visible bridge label.
	NodeLabel string
	// IncludeMeasurements toggles whether DPs that implement
	// [interfaces.MatterMeasurementSource] (but not MatterEndpointSource)
	// produce standalone sensor endpoints. Off by default.
	IncludeMeasurements bool
	// ExposeSecondaryChannels, when true, materialises a Matter endpoint
	// for a device's group-secondary channels (the status transmitter and
	// the additional virtual-receiver actors) as well as the primary
	// (group-master) channel. Off by default: a multi-channel HmIP actor
	// projects a single endpoint from its primary channel, so one physical
	// device is one Matter accessory rather than several duplicates. Mirrors
	// [config.NorthMatter.ExposeSecondaryChannels].
	ExposeSecondaryChannels bool
	// Labels resolves the locale-aware parameter label embedded as the
	// NodeLabel suffix of measurement sub-endpoints, pre-bound to the
	// daemon locale. Shares [device.TranslatedParameterLabel] +
	// [naming.EntityDisplayName] with the MQTT discovery builder and
	// the REST data-point handler so all three north-bound surfaces
	// render the same per-parameter display name. Nil is tolerated —
	// the suffix then falls back to the title-cased parameter.
	Labels device.ParameterTranslator
	// Locale selects the language of the NodeLabel channel-number fallback
	// ("Channel N" / "Kanal N"). Empty falls back to the catalogue default.
	Locale string
}

// Validate returns nil when the config is internally consistent.
func (c Config) Validate() error {
	if c.VendorID == 0 {
		return errors.New("endpoint: Config.VendorID must be non-zero")
	}
	if c.ProductID == 0 {
		return errors.New("endpoint: Config.ProductID must be non-zero")
	}
	if strings.TrimSpace(c.NodeLabel) == "" {
		return errors.New("endpoint: Config.NodeLabel must be non-empty")
	}
	return nil
}

// Store is the subset of [store.Store] the assembler depends on.
// Defined as an interface so tests substitute an in-memory fake
// without standing up a SQLite database for every assembly run.
type Store interface {
	GetEndpoint(ctx context.Context, key store.EndpointKey) (store.EndpointRecord, error)
	UpsertEndpointAssigning(ctx context.Context, rec store.EndpointRecord) (uint16, error)
	ListEndpoints(ctx context.Context, centralName string) ([]store.EndpointRecord, error)
	RemoveEndpoint(ctx context.Context, key store.EndpointKey) error
}

// ExposureChecker is the assembler's allowlist gate. Implementations
// return true when the source identified by key is on the operator's
// allowlist (`matter_exposures.enabled = 1`). Sources that fail the
// check are silently dropped from the topology.
//
// A nil ExposureChecker means "allow everything" — the legacy
// behaviour for tests + dev setups that have not yet wired the
// allowlist UI. Production daemons must inject a real checker.
type ExposureChecker interface {
	IsExposed(ctx context.Context, key store.EndpointKey) (bool, error)
}

// allowAllExposureChecker is the nil-safe fallback.
type allowAllExposureChecker struct{}

func (allowAllExposureChecker) IsExposed(_ context.Context, _ store.EndpointKey) (bool, error) {
	return true, nil
}

// Assembler walks one or more snapshots and produces a [Topology].
// Multi-call-safe; concurrent calls serialise through the underlying
// store transactions.
type Assembler struct {
	store     Store
	exposures ExposureChecker
	cfg       Config
	logger    *slog.Logger
	// translations resolves the localized NodeLabel channel-number fallback in
	// cfg.Locale. Auto-loaded in [New] (immutable embedded data); nil-tolerant.
	translations *i18n.Catalogs
	// states owns the per-endpoint state that must outlive a single
	// dispatch (DataVersion trackers, the Identify cluster server),
	// keyed by the stable [store.EndpointKey]. It lives across every
	// [Assembler.Assemble] so a bridged endpoint's DataVersion and
	// running Identify survive reassembly — see
	// [endpointStateRegistry] and [Endpoint.state].
	states *endpointStateRegistry
}

// New returns an assembler. logger may be nil; the assembler then
// uses [slog.Default]. When `exposures` is nil the assembler permits
// every source (legacy / test default); production callers wire a
// `matter/store.Store`-backed checker so the allowlist is enforced.
func New(s Store, cfg Config, logger *slog.Logger) (*Assembler, error) {
	if s == nil {
		return nil, errors.New("endpoint: store is required")
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if logger == nil {
		logger = slog.Default()
	}
	a := &Assembler{
		store:     s,
		exposures: allowAllExposureChecker{},
		cfg:       cfg,
		logger:    logger,
		states:    newEndpointStateRegistry(),
	}
	if cat, err := i18n.NewCatalogs(); err == nil {
		a.translations = cat
	}
	return a, nil
}

// channelLabel returns the localized word for a channel ("Channel" / "Kanal"),
// used as the NodeLabel channel-number fallback. Falls back to "Channel" when
// no catalogues are wired.
func (a *Assembler) channelLabel() string {
	if a.translations == nil {
		return "Channel"
	}
	return a.translations.T(a.cfg.Locale, "channel.title")
}

// SetExposureChecker wires the allowlist checker. Pass nil to revert
// to the allow-all default (test convenience). Safe to call between
// assemblies; not safe to call concurrently with [Assemble].
func (a *Assembler) SetExposureChecker(c ExposureChecker) {
	if c == nil {
		a.exposures = allowAllExposureChecker{}
		return
	}
	a.exposures = c
}

// Assemble produces the topology from the given snapshots. Endpoint
// IDs are looked up in the store; new sources receive a fresh ID
// allocated under a transaction. Vanished sources (rows in the store
// with no matching snapshot entry) are removed.
//
// Snapshots must have unique CentralName values; the assembler does
// not deduplicate. The caller is expected to pass exactly one
// snapshot per central.
func (a *Assembler) Assemble(ctx context.Context, snapshots []Snapshot) (*Topology, error) {
	// Apple-compatible three-tier topology (mirrors matter.js's
	// `BridgedDevicesNode.ts`):
	//   EP 0 = RootNode  (DeviceType 0x0016) — system services
	//   EP 1 = Aggregator(DeviceType 0x000E) — Descriptor.PartsList enumerates bridged
	//   EP ≥ 2 = bridged devices
	root := &Endpoint{
		ID:         0,
		DeviceType: deviceTypeRootNode,
		Reachable:  true,
	}
	aggregator := &Endpoint{
		ID:         1,
		DeviceType: deviceTypeAggregator,
		Reachable:  true,
	}
	topology := &Topology{
		Endpoints: []*Endpoint{root, aggregator},
		VendorID:  a.cfg.VendorID,
		ProductID: a.cfg.ProductID,
		NodeLabel: a.cfg.NodeLabel,
	}

	seen := make(map[store.EndpointKey]struct{})
	for _, snap := range snapshots {
		eps, err := a.assembleSnapshot(ctx, snap, seen)
		if err != nil {
			return nil, err
		}
		topology.Endpoints = append(topology.Endpoints, eps...)
	}

	if err := a.gcVanished(ctx, snapshots, seen); err != nil {
		return nil, err
	}

	// Release the state of sources that vanished / were de-exposed this
	// run so a later re-add gets a fresh version (matches matter.js
	// destroying the Datasource on endpoint removal), a running Identify
	// countdown stops, and the registry stays bounded to the live
	// topology. State of endpoints still present in `seen` is retained,
	// keeping their version stable.
	a.states.retain(seen)

	sort.SliceStable(topology.Endpoints, func(i, j int) bool {
		return topology.Endpoints[i].ID < topology.Endpoints[j].ID
	})
	// Stamp every bridged endpoint with the bridge-wide VID/PID so the
	// BridgedDeviceBasicInformation cluster server can read them from
	// the endpoint without a back-pointer to the topology. Skipped for
	// the root + aggregator endpoints (they only carry root-side
	// BasicInformation, not BridgedDeviceBasicInformation).
	for _, ep := range topology.Endpoints {
		if ep == nil || ep.IsRoot() || ep.IsAggregator() {
			continue
		}
		ep.BridgeVendorID = topology.VendorID
		ep.BridgeProductID = topology.ProductID
	}
	return topology, nil
}

// deviceTypeRootNode is the Matter Device Type ID for the root
// endpoint of a Matter Node. Mirrors Matter Device Library §2.1
// ("Root Node" / 0x0016) and matter.js's `RootNodeDt.id`.
const deviceTypeRootNode = 0x0016

// deviceTypeAggregator is the Matter Device Type ID for the
// Aggregator endpoint (EP 1) that hosts bridged sub-endpoints in its
// Descriptor.PartsList. Mirrors Matter Device Library §13.2
// ("Aggregator" / 0x000E) and matter.js's `AggregatorDt.id`.
const deviceTypeAggregator = 0x000E

func (a *Assembler) assembleSnapshot(ctx context.Context, snap Snapshot, seen map[store.EndpointKey]struct{}) ([]*Endpoint, error) {
	if snap.CentralName == "" {
		return nil, errors.New("endpoint: snapshot CentralName is required")
	}
	out := make([]*Endpoint, 0, 8)

	for _, dev := range snap.Devices {
		if dev == nil {
			continue
		}
		for _, ch := range dev.Channels() {
			if ch == nil {
				continue
			}
			eps, err := a.assembleChannel(ctx, snap.CentralName, dev, ch, seen)
			if err != nil {
				return nil, err
			}
			out = append(out, eps...)
		}
	}
	return out, nil
}

func (a *Assembler) assembleChannel(ctx context.Context, centralName string, dev *device.Device, ch *device.Channel, seen map[store.EndpointKey]struct{}) ([]*Endpoint, error) { //nolint:gocognit,gocyclo,funlen // single-purpose channel assembly with many device-type/cluster branches
	out := make([]*Endpoint, 0, 4)

	// Collapse a multi-channel custom-DP actor onto its primary endpoint by
	// default: a switch / dimmer / cover / lock / siren / valve spans its
	// primary channel plus extra virtual-receiver actor channels, and
	// materialising every member would surface one physical device as
	// several duplicate accessories. Only the CDP-primary channel projects
	// unless the operator opts into the secondary channels. Standalone
	// generic channels (buttons, measurements, a status transmitter's
	// BooleanState) are not custom-DP secondaries and are never affected.
	// Matter-only — every other north-bound surface still carries all
	// channels.
	if !a.cfg.ExposeSecondaryChannels && ch.IsCustomDPSecondaryChannel() {
		return out, nil
	}

	allow := func(kind store.DPKind, key string) (bool, error) {
		exposed, err := a.exposures.IsExposed(ctx, store.EndpointKey{
			CentralName:   centralName,
			DeviceAddress: dev.Address,
			ChannelNo:     ch.Number,
			DPKind:        kind,
			DPKey:         key,
		})
		if err != nil {
			return false, fmt.Errorf("endpoint: allowlist probe: %w", err)
		}
		return exposed, nil
	}

	// Custom DP (max one per channel).
	if cdp := ch.CustomDataPoint(); cdp != nil {
		if src, ok := cdp.(interfaces.MatterEndpointSource); ok {
			ok, err := allow(store.DPKindCustom, dpKey(cdp))
			if err != nil {
				return nil, err
			}
			if ok {
				ep, err := a.makeEndpoint(ctx, centralName, dev, ch, store.DPKindCustom, dpKey(cdp), src)
				if err != nil {
					return nil, err
				}
				seen[ep.SourceKey] = struct{}{}
				out = append(out, ep)
			}
		}
	}

	// Calculated DPs.
	for _, calc := range ch.CalculatedDataPoints() {
		key := dpKey(calc)
		if src, ok := calc.(interfaces.MatterEndpointSource); ok {
			allowed, err := allow(store.DPKindCalculated, key)
			if err != nil {
				return nil, err
			}
			if !allowed {
				continue
			}
			ep, err := a.makeEndpoint(ctx, centralName, dev, ch, store.DPKindCalculated, key, src)
			if err != nil {
				return nil, err
			}
			seen[ep.SourceKey] = struct{}{}
			out = append(out, ep)
			continue
		}
		if !a.cfg.IncludeMeasurements {
			continue
		}
		meas, ok := calc.(interfaces.MatterMeasurementSource)
		if !ok || meas.MatterMeasurementClass() == interfaces.MatterMeasurementNone {
			continue
		}
		allowed, err := allow(store.DPKindMeasurement, key)
		if err != nil {
			return nil, err
		}
		if !allowed {
			continue
		}
		ep, err := a.makeMeasurementEndpoint(ctx, centralName, dev, ch, store.DPKindMeasurement, key, meas)
		if err != nil {
			return nil, err
		}
		seen[ep.SourceKey] = struct{}{}
		out = append(out, ep)
	}

	// Combined DPs.
	for _, comb := range ch.CombinedDataPoints() {
		src, ok := comb.(interfaces.MatterEndpointSource)
		if !ok {
			continue
		}
		allowed, err := allow(store.DPKindCombined, dpKey(comb))
		if err != nil {
			return nil, err
		}
		if !allowed {
			continue
		}
		ep, err := a.makeEndpoint(ctx, centralName, dev, ch, store.DPKindCombined, dpKey(comb), src)
		if err != nil {
			return nil, err
		}
		seen[ep.SourceKey] = struct{}{}
		out = append(out, ep)
	}

	// Generic DPs project to Matter on two paths:
	//
	//   1. MatterEndpointSource — the DP is its own actor (today only
	//      [generic.Switch] on STATE → OnOffPlugInUnit). Skipped on
	//      channels that already host a Custom-DP wrapper (the wrapper
	//      owns the channel's Matter projection and would double up).
	//   2. MatterMeasurementSource — Button / Action PRESS_*,
	//      Sensor[float64] for standalone temperature sensors etc. The
	//      allowlist filter gates each row; default-empty allowlist =
	//      no Generic-DP endpoints surface, matching the §1 "Allowlist
	//      instead of Denylist" rule.
	channelHasCustom := ch.CustomDataPoint() != nil
	// Press-event DPs (PRESS_SHORT / PRESS_LONG / PRESS_CONT / …) never
	// project per-DP: every allowed press parameter of the channel is
	// collected here and consolidated into ONE GenericSwitch endpoint per
	// physical button after the loop. See [generic.ButtonGroup] and
	// [ButtonGroupDPKey].
	var pressMembers []device.ParameterDataPoint
	for _, gdp := range ch.DataPoints() {
		key := genericDPKeyForMeasurement(gdp)
		if key == "" {
			continue
		}
		// Align the Matter projection with the entity-creation gate the other
		// north-bound surfaces apply: drop ignored service params, the raw
		// no_create constituents of an aggregating parent, and (unless the
		// operator opted in) the ce_secondary / ce_state secondary channels.
		// See [config.NorthMatter.ExposeSecondaryChannels].
		if hideFromMatter(gdp, channelHasCustom, a.cfg.ExposeSecondaryChannels) {
			continue
		}
		// Path 1: Generic-DP actor. Only material when the channel has
		// no Custom-DP wrapper; otherwise the wrapper owns the
		// projection (the wrapper itself wires the same DP under the
		// hood).
		if !channelHasCustom {
			if src, ok := gdp.(interfaces.MatterEndpointSource); ok {
				if servers := src.MatterClusterServers(); len(servers) > 0 {
					allowed, err := allow(store.DPKindGeneric, key)
					if err != nil {
						return nil, err
					}
					if !allowed {
						continue
					}
					ep, err := a.makeEndpoint(ctx, centralName, dev, ch, store.DPKindGeneric, key, src)
					if err != nil {
						return nil, err
					}
					seen[ep.SourceKey] = struct{}{}
					out = append(out, ep)
					continue
				}
			}
		}
		// Path 2: Generic-DP measurement source.
		meas, ok := gdp.(interfaces.MatterMeasurementSource)
		if !ok || meas.MatterMeasurementClass() == interfaces.MatterMeasurementNone {
			continue
		}
		allowed, err := allow(store.DPKindGeneric, key)
		if err != nil {
			return nil, err
		}
		if !allowed {
			continue
		}
		if meas.MatterMeasurementClass() == interfaces.MatterMeasurementMomentarySwitch {
			// Defer press DPs to the per-channel consolidation below.
			// The allowlist stays per-parameter: only allowed members
			// join the group, so an operator can e.g. expose the short
			// press while keeping the long-press events private.
			pressMembers = append(pressMembers, gdp)
			continue
		}
		ep, err := a.makeMeasurementEndpoint(ctx, centralName, dev, ch, store.DPKindGeneric, key, meas)
		if err != nil {
			return nil, err
		}
		seen[ep.SourceKey] = struct{}{}
		out = append(out, ep)
	}

	if len(pressMembers) > 0 {
		ep, err := a.makeButtonGroupEndpoint(ctx, centralName, dev, ch, pressMembers)
		if err != nil {
			return nil, err
		}
		if ep != nil {
			seen[ep.SourceKey] = struct{}{}
			out = append(out, ep)
		}
	}

	return out, nil
}

// ButtonGroupDPKey is the synthetic dp_key persisted for the
// per-channel consolidated GenericSwitch endpoint. All press-event DPs
// of one channel (PRESS_SHORT / PRESS_LONG / PRESS_CONT /
// PRESS_LONG_RELEASE / …) share ONE endpoint: a physical button is one
// Matter switch, and the §1.13 press-cycle events (InitialPress →
// LongPress → LongRelease) only sequence correctly on a single cluster
// instance — so no single member parameter can serve as the row key.
// Multi-button remotes keep one endpoint per button because each
// button is its own channel; this extends the one-endpoint-per-device
// rule of docs/adr/0049-matter-one-endpoint-per-device.md to the
// per-parameter fan-out inside a button channel.
//
// Older per-parameter rows (dp_key = "PRESS_SHORT", …) no longer
// appear in the assembled set and are garbage-collected on the next
// model-complete assembly; the consolidated endpoint is allocated a
// fresh endpoint id, so controllers re-learn button devices once.
const ButtonGroupDPKey = "BUTTON"

// makeButtonGroupEndpoint builds the single GenericSwitch endpoint for
// one physical button from the channel's allowed press DPs. Returns
// (nil, nil) when no member survives the group's press-family filter —
// defensive only, since the MomentarySwitch classification and the
// group's press family enumerate the same parameters.
func (a *Assembler) makeButtonGroupEndpoint(
	ctx context.Context,
	centralName string,
	dev *device.Device,
	ch *device.Channel,
	members []device.ParameterDataPoint,
) (*Endpoint, error) {
	srcs := make([]generic.PressEventSource, 0, len(members))
	for _, m := range members {
		srcs = append(srcs, m)
	}
	group := generic.NewButtonGroup(srcs...)
	if group == nil {
		return nil, nil
	}
	sourceKey := store.EndpointKey{
		CentralName:   centralName,
		DeviceAddress: dev.Address,
		ChannelNo:     ch.Number,
		DPKind:        store.DPKindGeneric,
		DPKey:         ButtonGroupDPKey,
	}
	deviceType := measurementDeviceType(interfaces.MatterMeasurementMomentarySwitch)
	id, err := a.assignOrReuseID(ctx, sourceKey, deviceType)
	if err != nil {
		return nil, err
	}
	return &Endpoint{
		ID:         id,
		DeviceType: deviceType,
		Reachable:  dev.Available(),
		// The endpoint stands for the whole physical button (the
		// channel), not one PRESS_* parameter — no parameter suffix.
		FriendlyName:  friendlyName(dev, ch, "", a.channelLabel()),
		BridgedDevice: dev,
		Channel:       ch,
		Measurement:   group,
		SourceKey:     sourceKey,
		// Reuse the state bound to this stable source key so the
		// endpoint's per-cluster version and Identify server survive
		// reassembly.
		state: a.states.stateFor(sourceKey),
		// Bridged under the Aggregator (EP 1), same parent chain as
		// every other bridged endpoint.
		ParentEndpointID:    1,
		HasParentEndpointID: true,
	}, nil
}

// hideFromMatter reports whether a generic DP should be dropped from the
// assembled Matter topology, aligning it with the entity-creation gate the
// other north-bound surfaces apply. `channelHasCustom` is whether the DP's
// channel already hosts a custom DP that owns its projection; `exposeSecondary`
// is the operator's `north.matter.expose_secondary_channels` choice.
//
//   - `ignored` — service / status / overflow params hidden everywhere; never
//     projected, regardless of the expert flag.
//   - `no_create` — consumed by an aggregating parent; the parent projects on
//     its own channel, so the raw constituent must not duplicate it. On a bare
//     secondary channel the flag reveals it.
//   - `ce_secondary` / `ce_state` — secondary member / group-state transmitter;
//     hidden by default, revealed by the flag. Genuine ce_visible extra sensors
//     (HUMIDITY, a contact STATE) are NOT hidden.
//
// Mirrors the identically named eligibility helper so the candidate list and
// the assembled topology agree on what the projection hides.
func hideFromMatter(source any, channelHasCustom, exposeSecondary bool) bool {
	u, ok := source.(interface{ Usage() hmenum.DataPointUsage })
	if !ok {
		return false
	}
	switch u.Usage() {
	case hmenum.DataPointUsageIgnored:
		return true
	case hmenum.DataPointUsageNoCreate:
		return channelHasCustom || !exposeSecondary
	case hmenum.DataPointUsageCDPSecondary, hmenum.DataPointUsageCDPState:
		return !exposeSecondary
	default:
		return false
	}
}

// genericDPKeyForMeasurement returns the allowlist dp_key for a
// generic DP. Reads through [hmtypes.DataPointKey.Parameter] so the
// key is stable across daemon restarts and matches the row the SPA
// wrote via `PUT /api/v1/matter/exposable`.
func genericDPKeyForMeasurement(dp any) string {
	if k, ok := dp.(interface{ DataPointKey() hmtypes.DataPointKey }); ok {
		return k.DataPointKey().Parameter
	}
	return ""
}

func (a *Assembler) makeEndpoint(
	ctx context.Context,
	centralName string,
	dev *device.Device,
	ch *device.Channel,
	kind store.DPKind,
	key string,
	src interfaces.MatterEndpointSource,
) (*Endpoint, error) {
	sourceKey := store.EndpointKey{
		CentralName:   centralName,
		DeviceAddress: dev.Address,
		ChannelNo:     ch.Number,
		DPKind:        kind,
		DPKey:         key,
	}
	deviceType := src.MatterDeviceType()

	id, err := a.assignOrReuseID(ctx, sourceKey, deviceType)
	if err != nil {
		return nil, err
	}
	return &Endpoint{
		ID:            id,
		DeviceType:    deviceType,
		Reachable:     dev.Available(),
		FriendlyName:  friendlyName(dev, ch, "", a.channelLabel()),
		BridgedDevice: dev,
		Channel:       ch,
		Source:        src,
		SourceKey:     sourceKey,
		// Reuse the state bound to this stable source key so the
		// endpoint's per-cluster version and Identify server survive
		// reassembly.
		state: a.states.stateFor(sourceKey),
		// Bridged endpoints are children of the Aggregator (EP 1).
		// Mirrors chip examples/bridge-app/linux/main.cpp:261-276
		// AddDeviceEndpoint(..., parentEndpointId=1) and matter.js
		// aggregator.add(child) which establishes the same parent chain.
		ParentEndpointID:    1,
		HasParentEndpointID: true,
	}, nil
}

func (a *Assembler) makeMeasurementEndpoint(
	ctx context.Context,
	centralName string,
	dev *device.Device,
	ch *device.Channel,
	kind store.DPKind,
	key string,
	meas interfaces.MatterMeasurementSource,
) (*Endpoint, error) {
	sourceKey := store.EndpointKey{
		CentralName:   centralName,
		DeviceAddress: dev.Address,
		ChannelNo:     ch.Number,
		DPKind:        kind,
		DPKey:         key,
	}
	deviceType := measurementDeviceType(meas.MatterMeasurementClass())

	id, err := a.assignOrReuseID(ctx, sourceKey, deviceType)
	if err != nil {
		return nil, err
	}
	return &Endpoint{
		ID:            id,
		DeviceType:    deviceType,
		Reachable:     dev.Available(),
		FriendlyName:  friendlyName(dev, ch, a.parameterSuffix(ch, key), a.channelLabel()),
		BridgedDevice: dev,
		Channel:       ch,
		Source:        nil,
		Measurement:   meas,
		SourceKey:     sourceKey,
		// Reuse the state bound to this stable source key so the
		// endpoint's per-cluster version and Identify server survive
		// reassembly.
		state: a.states.stateFor(sourceKey),
		// Measurement sub-endpoints are also bridged under the Aggregator
		// (EP 1). Same parent-chain as source endpoints.
		ParentEndpointID:    1,
		HasParentEndpointID: true,
	}, nil
}

// assignOrReuseID looks up the existing endpoint_id for sourceKey;
// allocates a fresh one otherwise. Updates device_type either way so
// a profile change in the model side migrates cleanly.
func (a *Assembler) assignOrReuseID(ctx context.Context, sourceKey store.EndpointKey, deviceType uint16) (uint16, error) {
	rec, err := a.store.GetEndpoint(ctx, sourceKey)
	switch {
	case err == nil:
		// Already assigned — refresh device_type if it drifted.
		if rec.DeviceType != deviceType {
			rec.DeviceType = deviceType
			if _, err := a.store.UpsertEndpointAssigning(ctx, rec); err != nil {
				return 0, fmt.Errorf("endpoint: refresh device_type: %w", err)
			}
		}
		return rec.EndpointID, nil
	case isNotFound(err):
		// Allocate a fresh ID under a transaction.
		id, err := a.store.UpsertEndpointAssigning(ctx, store.EndpointRecord{
			Key:        sourceKey,
			DeviceType: deviceType,
		})
		if err != nil {
			return 0, fmt.Errorf("endpoint: assign new id: %w", err)
		}
		return id, nil
	default:
		return 0, fmt.Errorf("endpoint: lookup: %w", err)
	}
}

// gcVanished removes store rows for sources that no longer appear
// in any snapshot. seen contains every key produced this run; we
// list the persisted keys per central and drop the difference.
//
// Snapshots that are not model-complete are exempt: at daemon boot the
// topology is assembled before the readiness-gated CCU device load has
// populated the model, so a registered central briefly contributes an
// empty (or partial) device list. Treating that as "every device
// vanished" would delete all persisted endpoint-ID rows on each boot
// and renumber the bridged fleet — controllers key their accessory
// cache on the endpoint number, so persisted numbers must survive a
// restart. Mirrors matter.js, which reserves persisted endpoint
// numbers at initialization (packages/node/src/storage/server/
// ServerEndpointStores.ts, assignNumber) and erases one only on
// explicit endpoint deletion (packages/node/src/node/server/
// ServerEndpointInitializer.ts, eraseDescendant).
func (a *Assembler) gcVanished(ctx context.Context, snapshots []Snapshot, seen map[store.EndpointKey]struct{}) error {
	for _, snap := range snapshots {
		if !snap.ModelComplete {
			// The central has not finished its initial device load; an
			// absent source is "not loaded yet", not "vanished". Keep
			// every persisted row until a model-complete snapshot vouches
			// for the fleet.
			a.logger.Debug(
				"matter endpoint gc skipped: model incomplete",
				slog.String("central", snap.CentralName),
			)
			continue
		}
		records, err := a.store.ListEndpoints(ctx, snap.CentralName)
		if err != nil {
			return fmt.Errorf("endpoint: gc list: %w", err)
		}
		for _, rec := range records {
			if _, kept := seen[rec.Key]; kept {
				continue
			}
			if err := a.store.RemoveEndpoint(ctx, rec.Key); err != nil {
				return fmt.Errorf("endpoint: gc remove: %w", err)
			}
			a.logger.Debug(
				"matter endpoint gc",
				slog.String("central", rec.Key.CentralName),
				slog.String("device", rec.Key.DeviceAddress),
				slog.Int("channel", rec.Key.ChannelNo),
				slog.String("dp_kind", string(rec.Key.DPKind)),
				slog.String("dp_key", rec.Key.DPKey),
				slog.Int("endpoint_id", int(rec.EndpointID)),
			)
		}
	}
	return nil
}

// dpKey extracts the persistence key from a DP. The Parameter field
// of the DP's hmtypes.DataPointKey is the canonical machine-readable
// identifier for a DP within its channel — for Custom DPs it is the
// profile token (e.g. "RGBW_LIGHT"), for Generic / Calculated /
// Combined DPs it is the parameter / classifier name. The assembler
// uses it as the dp_key column value in matter_endpoints.
func dpKey(dp device.AttachableDataPoint) string {
	return dp.DataPointKey().Parameter
}
