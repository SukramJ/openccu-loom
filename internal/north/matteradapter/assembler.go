// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package matteradapter

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"sync"

	"github.com/SukramJ/go-fabric/contract"
	fabricdevice "github.com/SukramJ/go-fabric/device"
	"github.com/SukramJ/go-fabric/endpoint"
	"github.com/SukramJ/go-fabric/schema"
	"github.com/SukramJ/go-fabric/surface"

	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	matterendpoint "github.com/SukramJ/openccu-loom/internal/store/matterendpoint"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
	"github.com/SukramJ/openccu-loom/pkg/interfaces"
)

// Config tunes the model walk. The zero value walks every channel of
// every device with the English channel word and the model's names.
// loom:reachable:reason="constructed by the daemon when it builds the assembler (cmd/openccu-loom/daemon_matter.go, startMatterBridge)"
type Config struct {
	// IncludeMeasurements toggles whether DPs that implement
	// [contract.MeasurementSource] (but not EndpointSource)
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
	NameResolver NameResolver
	// Build is what a device source builds its device under — the
	// operator's opt-ins and the ports they need. The eligibility listing
	// builds the same devices and takes the same value
	// ([CollectCandidates]), because a source keeps the device it built
	// first.
	Build interfaces.MatterBuildOptions
}

// NameResolver is this daemon's naming authority for endpoint labels.
//
// Labels are never derived on the Matter side. The name a device carries
// is a product decision every north-bound surface has to agree on, and
// re-deriving it for Matter alone makes the same device show up under
// two names in two places. The walk therefore asks for finished strings
// and only applies the Matter-specific parts through
// [endpoint.ComposeNodeLabel]: the parameter suffix and the 32-byte
// NodeLabel cap (Matter §9.13.6.5).
//
// Both methods are addressed by [matterendpoint.SourceKey] — the
// daemon's own coordinates — which is why the interface lives here and
// not in the bridge module.
// loom:reachable:reason="satisfied by modelNameResolver and injected through Config; interface satisfaction is invisible to the analyzer"
type NameResolver interface {
	// EndpointLabel returns the operator-facing base label of the source
	// identified by key — before any parameter suffix and before the
	// NodeLabel cap.
	EndpointLabel(key matterendpoint.SourceKey) string
	// ParameterLabel returns the operator-facing label of the parameter
	// key names, or "" when that parameter is the source's primary one
	// and therefore adds nothing to the base label.
	ParameterLabel(key matterendpoint.SourceKey) string
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
	IsExposed(ctx context.Context, key matterendpoint.SourceKey) (bool, error)
}

// allowAllExposureChecker is the nil-safe fallback.
type allowAllExposureChecker struct{}

func (allowAllExposureChecker) IsExposed(_ context.Context, _ matterendpoint.SourceKey) (bool, error) {
	return true, nil
}

// Assembler walks one or more device fleets and describes every endpoint
// they project as an [endpoint.Spec]; the node's Aggregator assembles
// them ([Reconciler]). Multi-call-safe.
type Assembler struct {
	exposures ExposureChecker
	cfg       Config
	logger    *slog.Logger
	// fallbackTypes records, once each, a device type the generated
	// device layer does not carry, so the hand-built Spec path is visible
	// in the log rather than silent; refusedDevices records, once each, a
	// source whose own device the kit refused.
	fallbackTypes  sync.Map
	refusedDevices sync.Map
}

// bridgedVendorName is the manufacturer served as the
// BridgedDeviceBasicInformation VendorName of every bridged endpoint:
// every one is a Homematic device, and Apple Home relies on the vendor
// fields being filled. It is stated per endpoint, since the node's own
// assembler would fall back to the bridge's vendor name.
const bridgedVendorName = "eQ-3"

// New returns an assembler. logger may be nil; the assembler then
// uses [slog.Default]. Until [Assembler.SetExposureChecker] wires a
// checker the assembler permits every source (legacy / test default);
// production callers wire a `matter/store.Store`-backed checker so the
// allowlist is enforced.
func New(cfg Config, logger *slog.Logger) *Assembler {
	if logger == nil {
		logger = slog.Default()
	}
	return &Assembler{
		exposures: allowAllExposureChecker{},
		cfg:       cfg,
		logger:    logger,
	}
}

// SetExposureChecker wires the allowlist checker. Pass nil to revert
// to the allow-all default (test convenience). Safe to call between
// assemblies; not safe to call concurrently with [Assembler.DescribeDevices].
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

// DeviceSnapshot is one central's device fleet as the daemon holds it:
// what [Assembler.DescribeDevices] turns into the flat [endpoint.Spec]
// values the node's Aggregator assembles.
// loom:reachable:reason="constructed per registered central by matterSnapshotter in cmd/openccu-loom/daemon_matter.go"
type DeviceSnapshot struct {
	// CentralName scopes every endpoint produced from Devices to this
	// central — required for multi-CCU correctness.
	CentralName string
	// Devices is the list of model devices visible on this central at
	// snapshot time. nil-safe — an empty slice produces zero endpoints.
	Devices []*device.Device
}

// Described is one central's fleet as the walk describes it: the
// endpoints the central projects, under the central's scope, and the keys
// an earlier composition stored for its devices that no composition
// produces any more ([supersededKeys]).
type Described struct {
	Scope      string
	Endpoints  []endpoint.Spec
	Superseded []matterendpoint.SourceKey
}

// DescribeDevices walks the model fleets in snapshots and describes every
// endpoint they project as an [endpoint.Spec], one [Described] per
// snapshot in order.
func (a *Assembler) DescribeDevices(ctx context.Context, snapshots []DeviceSnapshot) ([]Described, error) {
	out := make([]Described, 0, len(snapshots))
	for _, snap := range snapshots {
		d, err := a.snapshotSpecs(ctx, snap)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, nil
}

// snapshotSpecs describes one central's fleet as endpoint specs.
func (a *Assembler) snapshotSpecs(ctx context.Context, snap DeviceSnapshot) (Described, error) {
	if snap.CentralName == "" {
		return Described{}, errors.New("matteradapter: snapshot CentralName is required")
	}
	names := a.cfg.NameResolver
	if names == nil {
		names = newModelNameResolver(snap.Devices, a.cfg.Labels, a.channelLabel())
	}
	out := Described{Scope: snap.CentralName}
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
				return Described{}, err
			}
			deviceSpecs = append(deviceSpecs, specs...)
			out.Superseded = append(out.Superseded, supersededKeys(snap.CentralName, dev, ch)...)
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
// battery levels for one device. The target is searched for explicitly rather
// than taken from slice order: the spec with the smallest channel number wins,
// read back through [specChannelNo], because the lowest-numbered channel is
// the device's primary function. Ties keep the earliest spec, which is the
// order channelSpecs appended within that one channel.
func attachPowerSource(dev *device.Device, specs []endpoint.Spec) {
	if dev == nil || len(specs) == 0 {
		return
	}
	var battery contract.MeasurementSource
	for _, ch := range dev.Channels() {
		if ch == nil {
			continue
		}
		for _, gdp := range ch.DataPoints() {
			meas, ok := gdp.(contract.MeasurementSource)
			if !ok || meas.MatterMeasurementClass() != contract.MeasurementBattery {
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
		if specChannelNo(specs[i]) < specChannelNo(specs[target]) {
			target = i
		}
	}
	specs[target].PowerSource = battery
}

// specChannelNo reads the channel number back out of a spec's stable
// key. The bridge module carries the key opaquely, so the assertion —
// not a parse of its rendered form — is how this package recovers its
// own coordinates. A spec this package did not build has no channel and
// sorts last, which keeps [attachPowerSource] deterministic instead of
// panicking.
func specChannelNo(spec endpoint.Spec) int {
	if k, ok := spec.StableKey.(matterendpoint.SourceKey); ok {
		return k.ChannelNo
	}
	return math.MaxInt
}

func (a *Assembler) channelSpecs(ctx context.Context, centralName string, names NameResolver, dev *device.Device, ch *device.Channel) ([]endpoint.Spec, error) { //nolint:gocognit,gocyclo,funlen // single-purpose channel assembly with many device-type/cluster branches
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

	allow := func(kind matterendpoint.DPKind, key string) (bool, error) {
		exposed, err := a.exposures.IsExposed(ctx, matterendpoint.SourceKey{
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
		if src, ok := projectionOf(cdp); ok {
			ok, err := allow(matterendpoint.DPKindCustom, dpKey(cdp))
			if err != nil {
				return nil, err
			}
			if ok {
				if spec, built := a.makeSpec(centralName, names, dev, ch, matterendpoint.DPKindCustom, dpKey(cdp), src); built {
					out = append(out, spec)
				}
			}
		}
	}

	// Calculated DPs.
	for _, calc := range ch.CalculatedDataPoints() {
		key := dpKey(calc)
		if src, ok := projectionOf(calc); ok {
			allowed, err := allow(matterendpoint.DPKindCalculated, key)
			if err != nil {
				return nil, err
			}
			if !allowed {
				continue
			}
			if spec, built := a.makeSpec(centralName, names, dev, ch, matterendpoint.DPKindCalculated, key, src); built {
				out = append(out, spec)
			}
			continue
		}
		if !a.cfg.IncludeMeasurements {
			continue
		}
		meas, ok := calc.(contract.MeasurementSource)
		if !ok || meas.MatterMeasurementClass() == contract.MeasurementNone {
			continue
		}
		// The allowlist row is keyed by the kind the candidate
		// enumeration emits for the source — "calculated" for every
		// calculated DP, whichever Matter projection it ends up taking.
		// Probing a different kind here can never match the row the
		// operator switched on, so the exposure would stay inert while
		// the UI reports it as enabled.
		allowed, err := allow(matterendpoint.DPKindCalculated, key)
		if err != nil {
			return nil, err
		}
		if !allowed {
			continue
		}
		if spec, ok := a.makeMeasurementSpec(centralName, names, dev, ch, matterendpoint.DPKindCalculated, key, meas); ok {
			out = append(out, spec)
		}
	}

	// Combined DPs.
	for _, comb := range ch.CombinedDataPoints() {
		src, ok := projectionOf(comb)
		if !ok {
			continue
		}
		allowed, err := allow(matterendpoint.DPKindCombined, dpKey(comb))
		if err != nil {
			return nil, err
		}
		if !allowed {
			continue
		}
		if spec, built := a.makeSpec(centralName, names, dev, ch, matterendpoint.DPKindCombined, dpKey(comb), src); built {
			out = append(out, spec)
		}
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
			if src, ok := projectionOf(gdp); ok {
				// A generic source projects nothing for most of its
				// parameters (a generic Switch serves STATE alone), so the
				// projection is probed before the allowlist and the spec —
				// with its persisted-label read — is described after it.
				if src.projects(a.sourceOptions(centralName, names, dev, ch, matterendpoint.DPKindGeneric, key), a.cfg.Build) {
					allowed, err := allow(matterendpoint.DPKindGeneric, key)
					if err != nil {
						return nil, err
					}
					if !allowed {
						continue
					}
					if spec, built := a.makeSpec(centralName, names, dev, ch, matterendpoint.DPKindGeneric, key, src); built {
						out = append(out, spec)
					}
					continue
				}
			}
		}
		// Path 2: Generic-DP measurement source.
		meas, ok := gdp.(contract.MeasurementSource)
		if !ok || meas.MatterMeasurementClass() == contract.MeasurementNone {
			continue
		}
		allowed, err := allow(matterendpoint.DPKindGeneric, key)
		if err != nil {
			return nil, err
		}
		if !allowed {
			continue
		}
		if meas.MatterMeasurementClass() == contract.MeasurementMomentarySwitch {
			// Defer press DPs to the per-channel consolidation below.
			// The allowlist stays per-parameter: only allowed members
			// join the group, so an operator can e.g. expose the short
			// press while keeping the long-press events private.
			pressMembers = append(pressMembers, gdp)
			continue
		}
		if meas.MatterMeasurementClass() == contract.MeasurementBattery {
			// PowerSource is mounted on one of the device's endpoints by
			// attachPowerSource, never as an endpoint of its own — it has no
			// device type, and an endpoint without one advertises
			// DeviceTypeList=[BridgedNode] alone.
			continue
		}
		switch meas.MatterMeasurementClass() {
		case contract.MeasurementPower, contract.MeasurementEnergy:
			// Defer to the per-channel consolidation below. The allowlist
			// stays per-parameter, so an operator can expose consumption
			// while keeping voltage private.
			electricalMembers = append(electricalMembers, gdp)
			continue
		default:
		}
		if spec, ok := a.makeMeasurementSpec(centralName, names, dev, ch, matterendpoint.DPKindGeneric, key, meas); ok {
			out = append(out, spec)
		}
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
// (0x0510, matter.js packages/node/src/endpoints/electrical-sensor.ts).
// No single member
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
	names NameResolver,
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
	sourceKey := matterendpoint.SourceKey{
		CentralName:   centralName,
		DeviceAddress: dev.Address,
		ChannelNo:     ch.Number,
		DPKind:        matterendpoint.DPKindGeneric,
		DPKey:         ElectricalGroupDPKey,
	}
	return a.deviceSpec(specInput{
		key:          sourceKey,
		dev:          dev,
		ch:           ch,
		deviceType:   measurementDeviceType(contract.MeasurementElectrical),
		friendlyName: endpoint.ComposeNodeLabel(names.EndpointLabel(sourceKey), names.ParameterLabel(sourceKey)),
		measurement:  group,
	})
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
// appear in the assembled set; they stay stored until the device is
// forgotten ([Reconciler.QueueForget] finds them by its address). The
// consolidated endpoint is allocated a fresh endpoint id, so controllers
// re-learn button devices once.
const ButtonGroupDPKey = "BUTTON"

// makeButtonGroupSpec describes the single GenericSwitch endpoint for
// one physical button from the channel's allowed press DPs. The second
// result is false when no member survives the group's press-family
// filter — defensive only, since the MomentarySwitch classification and
// the group's press family enumerate the same parameters.
func (a *Assembler) makeButtonGroupSpec(
	centralName string,
	names NameResolver,
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
	sourceKey := matterendpoint.SourceKey{
		CentralName:   centralName,
		DeviceAddress: dev.Address,
		ChannelNo:     ch.Number,
		DPKind:        matterendpoint.DPKindGeneric,
		DPKey:         ButtonGroupDPKey,
	}
	return a.deviceSpec(specInput{
		key:        sourceKey,
		dev:        dev,
		ch:         ch,
		deviceType: measurementDeviceType(contract.MeasurementMomentarySwitch),
		// The endpoint stands for the whole physical button (the
		// channel), not one PRESS_* parameter — no parameter suffix.
		friendlyName: endpoint.ComposeNodeLabel(names.EndpointLabel(sourceKey), ""),
		measurement:  group,
	})
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

// supersededKeys are the keys an earlier composition stored for ch that no
// composition produces any more, whatever the exposure, the hidden flag or
// the configuration: a number reserved under one of them is never served
// again while the device lives, so the reconciler frees it.
//
// The register has one entry. Up to 0.32.x every press parameter of a
// button channel was its own endpoint, keyed like any generic measurement
// (DPKindGeneric, the parameter); since 0.33.0 the channel's press
// parameters are one GenericSwitch endpoint under [ButtonGroupDPKey], and
// the per-parameter key is only an exposure row. A future change that
// retires a key shape adds it here, with the release it ended in.
//
// Nothing that may come back belongs here: an unexposed, hidden or not
// yet loaded endpoint, or one a setting or the specification level leaves
// out, keeps its number so a controller sees the same accessory again.
func supersededKeys(centralName string, dev *device.Device, ch *device.Channel) []matterendpoint.SourceKey {
	var out []matterendpoint.SourceKey
	for _, gdp := range ch.DataPoints() {
		meas, ok := gdp.(contract.MeasurementSource)
		if !ok || meas.MatterMeasurementClass() != contract.MeasurementMomentarySwitch {
			continue
		}
		key := genericDPKeyForMeasurement(gdp)
		if key == "" {
			continue
		}
		out = append(out, matterendpoint.SourceKey{
			CentralName:   centralName,
			DeviceAddress: dev.Address,
			ChannelNo:     ch.Number,
			DPKind:        matterendpoint.DPKindGeneric,
			DPKey:         key,
		})
	}
	return out
}

// projection is how a data point reaches the Matter plane as an endpoint
// of its own: a device it builds from its cluster declarations, or the
// cluster servers it still serves itself. Exactly one is set.
type projection struct {
	device interfaces.MatterDeviceSource
	source contract.EndpointSource
}

// projectionOf reports the data point's projection; the device form wins
// over the server form for a source that offers both. dp is any because
// the model's data points share no type: the walk probes each for the
// two projection interfaces.
func projectionOf(dp any) (projection, bool) {
	if d, ok := dp.(interfaces.MatterDeviceSource); ok {
		return projection{device: d}, true
	}
	if s, ok := dp.(contract.EndpointSource); ok {
		return projection{source: s}, true
	}
	return projection{}, false
}

// buildDevice is src's device, built under build when the source's device
// depends on it. Every path that builds a device goes through here, so a
// source sees the same options whichever path builds it first, and every
// device is composed under the bridge's surface policies — the node
// refuses one composed under other ones.
func buildDevice(src interfaces.MatterDeviceSource, opts fabricdevice.Options, build interfaces.MatterBuildOptions) (*fabricdevice.Device, error) {
	opts.Surface = &build.Surface
	if c, ok := src.(interfaces.MatterConfiguredDeviceSource); ok {
		return c.MatterDeviceWith(opts, build)
	}
	return src.MatterDevice(opts)
}

// projects reports whether the source projects an endpoint at all: a
// device source once its device is built with opts (the build is kept by
// the source, so the later spec reuses it), a server source when it
// serves a cluster.
func (p projection) projects(opts fabricdevice.Options, build interfaces.MatterBuildOptions) bool {
	if p.device != nil {
		d, err := buildDevice(p.device, opts, build)
		return err == nil && d != nil
	}
	return len(p.source.MatterClusterServers()) > 0
}

// deviceType is the projection's advertised Device Library type.
func (p projection) deviceType() uint16 {
	if p.device != nil {
		return p.device.MatterDeviceType()
	}
	return p.source.MatterDeviceType()
}

// customPostfixLabel is the NodeLabel suffix of a custom data point that
// names itself with a postfix — the operating lock's BUTTON_LOCK, which
// sits on a thermostat's channel 0 beside nothing else. REST and MQTT
// append that postfix's label to the channel name; the endpoint takes
// the same label through the same route ([NameResolver.ParameterLabel]),
// so a controller shows "Thermostat (Bediensperre)" rather than a door
// lock under the thermostat's bare name. A source without a postfix
// adds nothing, as before.
func customPostfixLabel(names NameResolver, key matterendpoint.SourceKey, src projection) string {
	var dp any = src.device
	if src.device == nil {
		dp = src.source
	}
	pf, ok := dp.(interface{ NamePostfix() string })
	if !ok || pf.NamePostfix() == "" {
		return ""
	}
	key.DPKey = pf.NamePostfix()
	return names.ParameterLabel(key)
}

// makeSpec describes one source-backed bridged endpoint. The second
// result is false when the source projects nothing.
func (a *Assembler) makeSpec(
	centralName string,
	names NameResolver,
	dev *device.Device,
	ch *device.Channel,
	kind matterendpoint.DPKind,
	key string,
	src projection,
) (endpoint.Spec, bool) {
	sourceKey := matterendpoint.SourceKey{
		CentralName:   centralName,
		DeviceAddress: dev.Address,
		ChannelNo:     ch.Number,
		DPKind:        kind,
		DPKey:         key,
	}
	in := specInput{
		key:          sourceKey,
		dev:          dev,
		ch:           ch,
		deviceType:   src.deviceType(),
		friendlyName: endpoint.ComposeNodeLabel(names.EndpointLabel(sourceKey), customPostfixLabel(names, sourceKey, src)),
	}
	if src.device != nil {
		return a.declaredDeviceSpec(in, src.device)
	}
	if len(src.source.MatterClusterServers()) == 0 && kind == matterendpoint.DPKindGeneric {
		return endpoint.Spec{}, false
	}
	in.source = src.source
	return a.deviceSpec(in)
}

// sourceOptions are the device options of a source-backed endpoint: the
// key, the composed label, the addresses and the live availability — what
// [Assembler.declaredDeviceSpec] hands the source, so a device built from
// the projection probe is the one the spec reuses.
func (a *Assembler) sourceOptions(centralName string, names NameResolver, dev *device.Device, ch *device.Channel, kind matterendpoint.DPKind, key string) fabricdevice.Options {
	sourceKey := matterendpoint.SourceKey{
		CentralName:   centralName,
		DeviceAddress: dev.Address,
		ChannelNo:     ch.Number,
		DPKind:        kind,
		DPKey:         key,
	}
	return fabricdevice.Options{
		StableKey:      sourceKey,
		Name:           endpoint.ComposeNodeLabel(names.EndpointLabel(sourceKey), ""),
		Vendor:         bridgedVendorName,
		DeviceAddress:  dev.Address,
		ChannelAddress: ch.Address,
		Reachable:      dev.Available,
	}
}

// declaredDeviceSpec describes the endpoint of a source that builds its
// device itself from its cluster declarations. The device is the
// source's and lives as long as the source; the name, addresses and
// availability are this walk's, applied on the device's Spec (a
// controller-written NodeLabel is the node's persistence and overrides
// the name there). A source that projects nothing, or whose device the
// kit refuses, yields no endpoint — there is no hand-built fallback,
// because the servers exist only inside the device.
func (a *Assembler) declaredDeviceSpec(in specInput, src interfaces.MatterDeviceSource) (endpoint.Spec, bool) {
	d, err := buildDevice(src, fabricdevice.Options{
		StableKey:      in.key,
		Name:           in.friendlyName,
		Vendor:         bridgedVendorName,
		DeviceAddress:  in.dev.Address,
		ChannelAddress: in.ch.Address,
		Reachable:      in.dev.Available,
	}, a.cfg.Build)
	if err != nil {
		// Once per key: the source retries the build on every assembly,
		// and the finding does not change between two of them.
		if _, seen := a.refusedDevices.LoadOrStore(in.key.String(), struct{}{}); !seen {
			if errors.Is(err, surface.ErrWithheld) {
				a.logWithheld(in.deviceType, in.key.String(), err)
			} else {
				a.logger.Warn("matter.assembler.device_refused",
					slog.String("device_type", fmt.Sprintf("0x%04X", in.deviceType)),
					slog.String("key", in.key.String()),
					slog.String("err", err.Error()))
			}
		}
		return endpoint.Spec{}, false
	}
	if d == nil {
		return endpoint.Spec{}, false
	}
	a.refusedDevices.Delete(in.key.String())
	spec := d.Spec()
	spec.StableKey = in.key
	spec.FriendlyName = in.friendlyName
	spec.VendorName = bridgedVendorName
	spec.DeviceAddress = in.dev.Address
	spec.ChannelAddress = in.ch.Address
	spec.Availability = in.dev.Available
	return spec, true
}

// makeMeasurementSpec describes one standalone sensor endpoint. Its
// label carries the parameter suffix, because several measurements of
// one channel are otherwise indistinguishable in a controller's UI.
func (a *Assembler) makeMeasurementSpec(
	centralName string,
	names NameResolver,
	dev *device.Device,
	ch *device.Channel,
	kind matterendpoint.DPKind,
	key string,
	meas contract.MeasurementSource,
) (endpoint.Spec, bool) {
	sourceKey := matterendpoint.SourceKey{
		CentralName:   centralName,
		DeviceAddress: dev.Address,
		ChannelNo:     ch.Number,
		DPKind:        kind,
		DPKey:         key,
	}
	return a.deviceSpec(specInput{
		key:          sourceKey,
		dev:          dev,
		ch:           ch,
		deviceType:   measurementDeviceType(meas.MatterMeasurementClass()),
		friendlyName: endpoint.ComposeNodeLabel(names.EndpointLabel(sourceKey), names.ParameterLabel(sourceKey)),
		measurement:  meas,
	})
}

// specInput is what one bridged endpoint is built from: the key, the
// physical device and channel, the advertised device type, the label and
// exactly one of a cluster-serving source or a measurement.
type specInput struct {
	key          matterendpoint.SourceKey
	dev          *device.Device
	ch           *device.Channel
	deviceType   uint16
	friendlyName string
	source       contract.EndpointSource
	measurement  contract.MeasurementSource
}

// deviceSpec builds the endpoint through go-fabric's device layer: the
// generated type for the advertised device type id carries what
// matter.js's devices/*.ts state (mandatory clusters, mandated features,
// constraints). A source's own cluster servers are declared with
// [fabricdevice.HostServer] exactly as they were served before, and its
// change notifier with [fabricdevice.WithChangeNotifier] so the bridge
// keeps hearing the model's confirmed updates; a measurement is declared
// with [fabricdevice.Measurement], whose clusters the endpoint assembler
// materialises once the endpoint number is known. The cluster set,
// revisions, FeatureMaps and the UniqueID input are unchanged; what the
// device adds is device-type validation and the type's client clusters.
//
// A device type the generated layer does not carry, or a composition it
// refuses, falls back to the hand-built Spec and is logged once per
// type, so the assembly never loses an endpoint over a conformance
// finding — the finding is reported through the bridge's device-type
// validator, which judges the hand-built Spec too. A device the
// specification level withholds yields no endpoint (false): the
// hand-built Spec carries no surface report, so the node would serve it
// past the level.
func (a *Assembler) deviceSpec(in specInput) (endpoint.Spec, bool) {
	handBuilt := endpoint.Spec{
		StableKey:      in.key,
		DeviceAddress:  in.dev.Address,
		DeviceType:     in.deviceType,
		VendorName:     bridgedVendorName,
		FriendlyName:   in.friendlyName,
		ChannelAddress: in.ch.Address,
		Availability:   in.dev.Available,
		Source:         in.source,
		Measurement:    in.measurement,
	}
	t, ok := fabricdevice.TypeByID(schema.DeviceTypeID(in.deviceType))
	if !ok {
		a.logFallback(in.deviceType, "device type not in the generated device layer", nil)
		return handBuilt, true
	}
	opts := fabricdevice.Options{
		StableKey:      in.key,
		Name:           in.friendlyName,
		Vendor:         bridgedVendorName,
		DeviceAddress:  in.dev.Address,
		ChannelAddress: in.ch.Address,
		Reachable:      in.dev.Available,
		Surface:        &a.cfg.Build.Surface,
	}
	var decls []fabricdevice.Declaration
	if in.source != nil {
		for _, srv := range in.source.MatterClusterServers() {
			if srv == nil {
				continue
			}
			decls = append(decls, fabricdevice.HostServer(srv))
		}
		if n, ok := in.source.(contract.ChangeNotifier); ok {
			decls = append(decls, fabricdevice.WithChangeNotifier(n))
		}
	}
	if in.measurement != nil {
		decls = append(decls, fabricdevice.Measurement(in.measurement))
	}
	d, err := fabricdevice.New(t, opts, decls...)
	if errors.Is(err, surface.ErrWithheld) {
		if _, seen := a.refusedDevices.LoadOrStore(in.key.String(), struct{}{}); !seen {
			a.logWithheld(in.deviceType, in.key.String(), err)
		}
		return endpoint.Spec{}, false
	}
	if err != nil {
		a.logFallback(in.deviceType, "device layer refused the composition", err)
		return handBuilt, true
	}
	return d.Spec(), true
}

// logWithheld reports a device the configured specification level
// withholds (north.matter.spec_level). It is the operator's choice, not a
// defect, so it is logged at info.
func (a *Assembler) logWithheld(deviceType uint16, key string, err error) {
	a.logger.Info("matter.assembler.device_withheld",
		slog.String("device_type", fmt.Sprintf("0x%04X", deviceType)),
		slog.String("key", key),
		slog.String("err", err.Error()))
}

// logFallback reports a hand-built Spec once per device type.
func (a *Assembler) logFallback(deviceType uint16, reason string, err error) {
	if _, seen := a.fallbackTypes.LoadOrStore(deviceType, struct{}{}); seen {
		return
	}
	attrs := []any{slog.String("device_type", fmt.Sprintf("0x%04X", deviceType)), slog.String("reason", reason)}
	if err != nil {
		attrs = append(attrs, slog.String("err", err.Error()))
	}
	a.logger.Warn("matter.assembler.device_layer_fallback", attrs...)
}

// measurementDeviceType is a thin alias for
// [contract.MeasurementClassDeviceType], the canonical
// MeasurementClass → DeviceType mapping. Kept so the walk reads in one
// vocabulary; the contract package remains the single source of truth
// (ADR 0012 "rich model, dumb bridge").
func measurementDeviceType(class contract.MeasurementClass) uint16 {
	return contract.MeasurementClassDeviceType(class)
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
