// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package matteradapter

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/internal/north/matter/endpoint"
	"github.com/SukramJ/openccu-loom/internal/north/matter/store"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
	"github.com/SukramJ/openccu-loom/pkg/mattercontract"
)

// Config tunes the model walk and the topology assembly it feeds. The
// zero value is *not* valid — at minimum, a non-zero VendorID +
// ProductID + non-empty NodeLabel must be supplied (the root
// BasicInformation cluster mandates them).
//
// The first three fields are handed straight to [endpoint.Config]; the
// rest govern the walk and have no meaning on the Matter side.
// loom:reachable:reason="constructed by the daemon when it builds the assembler (cmd/openccu-loom/daemon_matter.go, startMatterBridge)"
type Config struct {
	// VendorID is the bridge's IANA-assigned vendor identifier.
	VendorID uint16
	// ProductID is the bridge's vendor-assigned product identifier.
	ProductID uint16
	// NodeLabel is the user-visible bridge label.
	NodeLabel string
	// IncludeMeasurements toggles whether DPs that implement
	// [mattercontract.MeasurementSource] (but not EndpointSource)
	// produce standalone sensor endpoints. Off by default; operators
	// turn it on with `north.matter.include_measurements`.
	//
	// The eligibility surface reports these DPs as mappable whatever this
	// flag says, because it answers what the model can project rather than
	// what the current configuration assembles. The two must not be
	// conflated: for one release the flag had no config key at all, so
	// every derived sensor an operator allowlisted was offered, accepted
	// and then silently dropped here.
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
	// ChannelLabel is the localized word for a channel ("Channel", "Kanal")
	// used as the NodeLabel channel-number fallback ("Channel N"). Empty
	// falls back to the English "Channel": translation belongs to the host,
	// which resolves the catalogue and supplies the finished word.
	ChannelLabel string
	// NameResolver is the naming authority the device walk asks for every
	// operator-facing label. Nil selects the model-backed resolver built
	// from the walked devices, which routes through the same naming
	// primitives as the MQTT discovery builder and the REST data-point
	// handler. Supplying one is how an owner with a different model keeps
	// its own names on the Matter plane instead of having them re-derived.
	NameResolver endpoint.NameResolver
}

// ExposureChecker is the walk's allowlist gate. Implementations
// return true when the source identified by key is on the operator's
// allowlist (`matter_exposures.enabled = 1`). Sources that fail the
// check are silently dropped from the topology.
//
// A nil ExposureChecker means "allow everything" — the legacy
// behaviour for tests + dev setups that have not yet wired the
// allowlist UI. Production daemons must inject a real checker.
// loom:reachable:reason="taken by Assembler.SetExposureChecker and satisfied host-side by the matter store (cmd/openccu-loom/daemon_matter.go, wireMatterRuntime); interface satisfaction is invisible to the analyzer"
type ExposureChecker interface {
	IsExposed(ctx context.Context, key store.EndpointKey) (bool, error)
}

// allowAllExposureChecker is the nil-safe fallback.
type allowAllExposureChecker struct{}

func (allowAllExposureChecker) IsExposed(_ context.Context, _ store.EndpointKey) (bool, error) {
	return true, nil
}

// Assembler walks one or more device fleets and produces an
// [endpoint.Topology] from them. Multi-call-safe; concurrent calls
// serialise through the underlying store transactions.
type Assembler struct {
	// inner owns everything that is not model-shaped: endpoint-id
	// allocation and persistence, the root/aggregator scaffolding, and
	// the per-endpoint state that survives a reassembly.
	inner     *endpoint.Assembler
	exposures ExposureChecker
	cfg       Config
}

// New returns an assembler. logger may be nil; the assembler then
// uses [slog.Default]. Until [Assembler.SetExposureChecker] wires a
// checker the assembler permits every source (legacy / test default);
// production callers wire a `matter/store.Store`-backed checker so the
// allowlist is enforced.
func New(s endpoint.Store, cfg Config, logger *slog.Logger) (*Assembler, error) {
	inner, err := endpoint.New(s, endpoint.Config{
		VendorID:  cfg.VendorID,
		ProductID: cfg.ProductID,
		NodeLabel: cfg.NodeLabel,
	}, logger)
	if err != nil {
		return nil, err
	}
	return &Assembler{
		inner:     inner,
		exposures: allowAllExposureChecker{},
		cfg:       cfg,
	}, nil
}

// SetExposureChecker wires the allowlist checker. Pass nil to revert
// to the allow-all default (test convenience). Safe to call between
// assemblies; not safe to call concurrently with [Assembler.AssembleDevices].
func (a *Assembler) SetExposureChecker(c ExposureChecker) {
	if c == nil {
		a.exposures = allowAllExposureChecker{}
		return
	}
	a.exposures = c
}

// channelLabel returns the localized word for a channel ("Channel" / "Kanal"),
// used as the NodeLabel channel-number fallback. Falls back to the English
// word when the caller supplied none.
func (a *Assembler) channelLabel() string {
	if a.cfg.ChannelLabel == "" {
		return "Channel"
	}
	return a.cfg.ChannelLabel
}

// DeviceSnapshot is one central's device fleet as the daemon holds it.
// It is the model-walking counterpart of [endpoint.Snapshot]: the walk in
// [Assembler.AssembleDevices] turns it into the flat [endpoint.Spec]
// values the assembly itself consumes.
// loom:reachable:reason="constructed per registered central by the daemon snapshotter (cmd/openccu-loom/daemon_matter.go:3358)"
type DeviceSnapshot struct {
	// CentralName scopes every endpoint produced from Devices to this
	// central — required for multi-CCU correctness.
	CentralName string
	// Devices is the list of model devices visible on this central at
	// snapshot time. nil-safe — an empty slice produces zero endpoints.
	Devices []*device.Device
	// ModelComplete reports whether this central's initial device load
	// has finished. Carried through to [endpoint.Snapshot.ModelComplete],
	// which documents what it gates.
	ModelComplete bool
}

// AssembleDevices walks the model fleets in snapshots, describes every
// endpoint they should project as an [endpoint.Spec], and assembles the
// topology from those.
func (a *Assembler) AssembleDevices(ctx context.Context, snapshots []DeviceSnapshot) (*endpoint.Topology, error) {
	specs := make([]endpoint.Snapshot, 0, len(snapshots))
	for _, snap := range snapshots {
		s, err := a.snapshotSpecs(ctx, snap)
		if err != nil {
			return nil, err
		}
		specs = append(specs, s)
	}
	return a.inner.Assemble(ctx, specs)
}

// snapshotSpecs describes one central's fleet as endpoint specs.
func (a *Assembler) snapshotSpecs(ctx context.Context, snap DeviceSnapshot) (endpoint.Snapshot, error) {
	if snap.CentralName == "" {
		return endpoint.Snapshot{}, errors.New("matteradapter: snapshot CentralName is required")
	}
	names := a.cfg.NameResolver
	if names == nil {
		names = newModelNameResolver(snap.Devices, a.cfg.Labels, a.channelLabel())
	}
	out := endpoint.Snapshot{CentralName: snap.CentralName, ModelComplete: snap.ModelComplete}
	for _, dev := range snap.Devices {
		if dev == nil {
			continue
		}
		deviceSpecs := make([]endpoint.Spec, 0, 4)
		for _, ch := range dev.Channels() {
			if ch == nil {
				continue
			}
			specs, err := a.channelSpecs(ctx, snap.CentralName, names, dev, ch)
			if err != nil {
				return endpoint.Snapshot{}, err
			}
			deviceSpecs = append(deviceSpecs, specs...)
		}
		attachPowerSource(dev, deviceSpecs)
		out.Endpoints = append(out.Endpoints, deviceSpecs...)
	}
	return out, nil
}

// attachPowerSource binds the device's battery reading to exactly one of its
// endpoint specs, so the PowerSource cluster (0x002F) is served where the
// Device Library puts it.
//
// PowerSource is a property of the physical device, not of any one function it
// exposes, and BridgedNode (0x0013) — the secondary device type every bridged
// endpoint carries — specifies it as a server cluster. Mounting it on a
// bridged endpoint is therefore conformant whatever that endpoint's primary
// type is, and it is what matter.js's ecosystem notes prescribe for a bridge:
// power-source information belongs at the bridged-node level, not on an
// endpoint whose device type does not specify it.
//
// The alternative — letting the battery data point materialise as an endpoint
// of its own — produced an endpoint with device type 0, whose DeviceTypeList
// was [BridgedNode] alone and which Apple files under its "Other" fallback.
//
// Exactly one endpoint gets it. A device with a switch and a metering channel
// has one battery, and advertising it twice would have controllers show two
// battery levels for one device. The target is the lowest-numbered channel's
// first endpoint, which is the device's primary function: channel order is the
// CCU's own, and channelSpecs appends in a stable order within a channel.
func attachPowerSource(dev *device.Device, specs []endpoint.Spec) {
	if dev == nil || len(specs) == 0 {
		return
	}
	var battery mattercontract.MeasurementSource
	for _, ch := range dev.Channels() {
		if ch == nil {
			continue
		}
		for _, gdp := range ch.DataPoints() {
			meas, ok := gdp.(mattercontract.MeasurementSource)
			if !ok || meas.MatterMeasurementClass() != mattercontract.MeasurementBattery {
				continue
			}
			battery = meas
			break
		}
		if battery != nil {
			break
		}
	}
	if battery == nil {
		return
	}
	target := 0
	for i := 1; i < len(specs); i++ {
		if specs[i].StableKey.ChannelNo < specs[target].StableKey.ChannelNo {
			target = i
		}
	}
	specs[target].PowerSource = battery
}

func (a *Assembler) channelSpecs(ctx context.Context, centralName string, names endpoint.NameResolver, dev *device.Device, ch *device.Channel) ([]endpoint.Spec, error) { //nolint:gocognit,gocyclo,funlen // single-purpose channel assembly with many device-type/cluster branches
	out := make([]endpoint.Spec, 0, 4)

	// An operator-hidden channel projects no endpoint, mirroring the
	// candidate enumeration that already drops it. Both gates are needed:
	// hiding does not touch the persisted allowlist row, so without this
	// one a channel exposed BEFORE it was hidden keeps its endpoint in
	// every controller while vanishing from the allowlist surface that
	// could switch it off — the exposure would only be revocable by
	// un-hiding the channel first.
	if ch.IsHidden() {
		return out, nil
	}

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
			return false, fmt.Errorf("matteradapter: allowlist probe: %w", err)
		}
		return exposed, nil
	}

	// Custom DP (max one per channel).
	if cdp := ch.CustomDataPoint(); cdp != nil {
		if src, ok := cdp.(mattercontract.EndpointSource); ok {
			ok, err := allow(store.DPKindCustom, dpKey(cdp))
			if err != nil {
				return nil, err
			}
			if ok {
				out = append(out, a.makeSpec(centralName, names, dev, ch, store.DPKindCustom, dpKey(cdp), src))
			}
		}
	}

	// Calculated DPs.
	for _, calc := range ch.CalculatedDataPoints() {
		key := dpKey(calc)
		if src, ok := calc.(mattercontract.EndpointSource); ok {
			allowed, err := allow(store.DPKindCalculated, key)
			if err != nil {
				return nil, err
			}
			if !allowed {
				continue
			}
			out = append(out, a.makeSpec(centralName, names, dev, ch, store.DPKindCalculated, key, src))
			continue
		}
		if !a.cfg.IncludeMeasurements {
			continue
		}
		meas, ok := calc.(mattercontract.MeasurementSource)
		if !ok || meas.MatterMeasurementClass() == mattercontract.MeasurementNone {
			continue
		}
		// The allowlist row is keyed by the kind the candidate
		// enumeration emits for the source — "calculated" for every
		// calculated DP, whichever Matter projection it ends up taking.
		// Probing a different kind here can never match the row the
		// operator switched on, so the exposure would stay inert while
		// the UI reports it as enabled.
		allowed, err := allow(store.DPKindCalculated, key)
		if err != nil {
			return nil, err
		}
		if !allowed {
			continue
		}
		out = append(out, a.makeMeasurementSpec(centralName, names, dev, ch, store.DPKindCalculated, key, meas))
	}

	// Combined DPs.
	for _, comb := range ch.CombinedDataPoints() {
		src, ok := comb.(mattercontract.EndpointSource)
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
		out = append(out, a.makeSpec(centralName, names, dev, ch, store.DPKindCombined, dpKey(comb), src))
	}

	// Generic DPs project to Matter on two paths:
	//
	//   1. EndpointSource — the DP is its own actor (today only
	//      [generic.Switch] on STATE → OnOffPlugInUnit). Skipped on
	//      channels that already host a Custom-DP wrapper (the wrapper
	//      owns the channel's Matter projection and would double up).
	//   2. MeasurementSource — Button / Action PRESS_*,
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
	// Electrical DPs (POWER / VOLTAGE / CURRENT / FREQUENCY /
	// ENERGY_COUNTER) are consolidated the same way: Matter groups them into
	// the attributes of one ElectricalSensor endpoint, so one metering socket
	// is one accessory rather than five. See [ElectricalGroupDPKey].
	var electricalMembers []device.ParameterDataPoint
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
			if src, ok := gdp.(mattercontract.EndpointSource); ok {
				if servers := src.MatterClusterServers(); len(servers) > 0 {
					allowed, err := allow(store.DPKindGeneric, key)
					if err != nil {
						return nil, err
					}
					if !allowed {
						continue
					}
					out = append(out, a.makeSpec(centralName, names, dev, ch, store.DPKindGeneric, key, src))
					continue
				}
			}
		}
		// Path 2: Generic-DP measurement source.
		meas, ok := gdp.(mattercontract.MeasurementSource)
		if !ok || meas.MatterMeasurementClass() == mattercontract.MeasurementNone {
			continue
		}
		allowed, err := allow(store.DPKindGeneric, key)
		if err != nil {
			return nil, err
		}
		if !allowed {
			continue
		}
		if meas.MatterMeasurementClass() == mattercontract.MeasurementMomentarySwitch {
			// Defer press DPs to the per-channel consolidation below.
			// The allowlist stays per-parameter: only allowed members
			// join the group, so an operator can e.g. expose the short
			// press while keeping the long-press events private.
			pressMembers = append(pressMembers, gdp)
			continue
		}
		if meas.MatterMeasurementClass() == mattercontract.MeasurementBattery {
			// PowerSource is mounted on one of the device's endpoints by
			// attachPowerSource, never as an endpoint of its own — it has no
			// device type, and an endpoint without one advertises
			// DeviceTypeList=[BridgedNode] alone.
			continue
		}
		switch meas.MatterMeasurementClass() {
		case mattercontract.MeasurementPower, mattercontract.MeasurementEnergy:
			// Defer to the per-channel consolidation below. The allowlist
			// stays per-parameter, so an operator can expose consumption
			// while keeping voltage private.
			electricalMembers = append(electricalMembers, gdp)
			continue
		default:
		}
		out = append(out, a.makeMeasurementSpec(centralName, names, dev, ch, store.DPKindGeneric, key, meas))
	}

	if len(pressMembers) > 0 {
		if spec, ok := a.makeButtonGroupSpec(centralName, names, dev, ch, pressMembers); ok {
			out = append(out, spec)
		}
	}

	if len(electricalMembers) > 0 {
		if spec, ok := a.makeElectricalGroupSpec(centralName, names, dev, ch, electricalMembers); ok {
			out = append(out, spec)
		}
	}

	return out, nil
}

// ElectricalGroupDPKey is the synthetic dp_key persisted for the per-channel
// consolidated ElectricalSensor endpoint.
//
// A metering plug reports POWER, VOLTAGE, CURRENT, FREQUENCY and
// ENERGY_COUNTER as five parameters; Matter models the first four as
// attributes of ONE ElectricalPowerMeasurement cluster and the fifth as
// ElectricalEnergyMeasurement, both on a single ElectricalSensor device type
// (0x0510, matter.js electrical-sensor.element.ts). No single member
// parameter can serve as the row key, so the group gets its own — the same
// reasoning as [ButtonGroupDPKey].
//
// These clusters used to be attached to the OnOff endpoint of the switch on
// the same device instead. The Device Library specifies neither of them for
// OnOffPlugInUnit (0x010A) in any role, which made that endpoint
// non-conformant; ElectricalSensor is their specified carrier.
const ElectricalGroupDPKey = "ELECTRICAL"

// makeElectricalGroupSpec describes the single ElectricalSensor endpoint for
// one channel from its allowed electrical DPs. The second result is false
// when no member survives the group's parameter filter.
func (a *Assembler) makeElectricalGroupSpec(
	centralName string,
	names endpoint.NameResolver,
	dev *device.Device,
	ch *device.Channel,
	members []device.ParameterDataPoint,
) (endpoint.Spec, bool) {
	srcs := make([]generic.ElectricalGroupMember, 0, len(members))
	for _, m := range members {
		src, ok := m.(generic.ElectricalGroupMember)
		if !ok {
			continue
		}
		srcs = append(srcs, src)
	}
	group := generic.NewElectricalGroup(srcs...)
	if group == nil {
		return endpoint.Spec{}, false
	}
	sourceKey := store.EndpointKey{
		CentralName:   centralName,
		DeviceAddress: dev.Address,
		ChannelNo:     ch.Number,
		DPKind:        store.DPKindGeneric,
		DPKey:         ElectricalGroupDPKey,
	}
	return endpoint.Spec{
		StableKey:      sourceKey,
		DeviceType:     measurementDeviceType(mattercontract.MeasurementElectrical),
		FriendlyName:   endpoint.ComposeNodeLabel(names.EndpointLabel(sourceKey), names.ParameterLabel(sourceKey)),
		ChannelAddress: ch.Address,
		Availability:   dev.Available,
		Measurement:    group,
	}, true
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

// makeButtonGroupSpec describes the single GenericSwitch endpoint for
// one physical button from the channel's allowed press DPs. The second
// result is false when no member survives the group's press-family
// filter — defensive only, since the MomentarySwitch classification and
// the group's press family enumerate the same parameters.
func (a *Assembler) makeButtonGroupSpec(
	centralName string,
	names endpoint.NameResolver,
	dev *device.Device,
	ch *device.Channel,
	members []device.ParameterDataPoint,
) (endpoint.Spec, bool) {
	srcs := make([]generic.PressEventSource, 0, len(members))
	for _, m := range members {
		srcs = append(srcs, m)
	}
	group := generic.NewButtonGroup(srcs...)
	if group == nil {
		return endpoint.Spec{}, false
	}
	sourceKey := store.EndpointKey{
		CentralName:   centralName,
		DeviceAddress: dev.Address,
		ChannelNo:     ch.Number,
		DPKind:        store.DPKindGeneric,
		DPKey:         ButtonGroupDPKey,
	}
	return endpoint.Spec{
		StableKey:  sourceKey,
		DeviceType: measurementDeviceType(mattercontract.MeasurementMomentarySwitch),
		// The endpoint stands for the whole physical button (the
		// channel), not one PRESS_* parameter — no parameter suffix.
		FriendlyName:   endpoint.ComposeNodeLabel(names.EndpointLabel(sourceKey), ""),
		ChannelAddress: ch.Address,
		Availability:   dev.Available,
		Measurement:    group,
	}, true
}

// hideFromMatter reports whether a generic DP should be dropped from the
// Matter projection, aligning it with the entity-creation gate the
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
// The candidate walk in [CollectCandidates] gates on this same function, so
// the operator-facing exposable list and the assembled topology cannot
// disagree about what the projection hides — they were two copies of this
// rule until the walk moved into this package.
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

// makeSpec describes one source-backed bridged endpoint.
func (a *Assembler) makeSpec(
	centralName string,
	names endpoint.NameResolver,
	dev *device.Device,
	ch *device.Channel,
	kind store.DPKind,
	key string,
	src mattercontract.EndpointSource,
) endpoint.Spec {
	sourceKey := store.EndpointKey{
		CentralName:   centralName,
		DeviceAddress: dev.Address,
		ChannelNo:     ch.Number,
		DPKind:        kind,
		DPKey:         key,
	}
	return endpoint.Spec{
		StableKey:      sourceKey,
		DeviceType:     src.MatterDeviceType(),
		FriendlyName:   endpoint.ComposeNodeLabel(names.EndpointLabel(sourceKey), ""),
		ChannelAddress: ch.Address,
		Availability:   dev.Available,
		Source:         src,
	}
}

// makeMeasurementSpec describes one standalone sensor endpoint. Its
// label carries the parameter suffix, because several measurements of
// one channel are otherwise indistinguishable in a controller's UI.
func (a *Assembler) makeMeasurementSpec(
	centralName string,
	names endpoint.NameResolver,
	dev *device.Device,
	ch *device.Channel,
	kind store.DPKind,
	key string,
	meas mattercontract.MeasurementSource,
) endpoint.Spec {
	sourceKey := store.EndpointKey{
		CentralName:   centralName,
		DeviceAddress: dev.Address,
		ChannelNo:     ch.Number,
		DPKind:        kind,
		DPKey:         key,
	}
	return endpoint.Spec{
		StableKey:      sourceKey,
		DeviceType:     measurementDeviceType(meas.MatterMeasurementClass()),
		FriendlyName:   endpoint.ComposeNodeLabel(names.EndpointLabel(sourceKey), names.ParameterLabel(sourceKey)),
		ChannelAddress: ch.Address,
		Availability:   dev.Available,
		Measurement:    meas,
	}
}

// measurementDeviceType is a thin alias for
// [mattercontract.MeasurementClassDeviceType], the canonical
// MeasurementClass → DeviceType mapping. Kept so the walk reads in one
// vocabulary; the contract package remains the single source of truth
// (ADR 0012 "rich model, dumb bridge").
func measurementDeviceType(class mattercontract.MeasurementClass) uint16 {
	return mattercontract.MeasurementClassDeviceType(class)
}

// dpKey extracts the persistence key from a DP. The Parameter field
// of the DP's hmtypes.DataPointKey is the canonical machine-readable
// identifier for a DP within its channel — for Custom DPs it is the
// profile token (e.g. "RGBW_LIGHT"), for Generic / Calculated /
// Combined DPs it is the parameter / classifier name. The walk
// uses it as the dp_key column value in matter_endpoints.
func dpKey(dp device.AttachableDataPoint) string {
	return dp.DataPointKey().Parameter
}
