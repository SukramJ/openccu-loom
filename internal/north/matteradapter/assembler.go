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
	"time"

	clusterspec "thingfabric.dev/go-fabric/cluster/spec"
	bdbidef "thingfabric.dev/go-fabric/cluster/spec/bridgeddevicebasicinformation"
	"thingfabric.dev/go-fabric/contract"
	fabricdevice "thingfabric.dev/go-fabric/device"
	"thingfabric.dev/go-fabric/endpoint"
	"thingfabric.dev/go-fabric/surface"

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
	// ([Assembler.CollectCandidates]) asks the same sources through the
	// same request ([Assembler.deviceRequest]).
	Build interfaces.MatterBuildOptions
	// Now stamps when a finding was first seen ([Finding].Since). Nil
	// selects the wall clock; tests supply a fixed one.
	Now func() time.Time
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
// they project as a go-fabric device ([Endpoint]); the node's Aggregator
// binds and assembles them ([Reconciler]). Multi-call-safe.
type Assembler struct {
	exposures ExposureChecker
	cfg       Config
	logger    *slog.Logger
	// findings holds, once each, a source the kit refused or the
	// specification level withholds — the record behind both the log line
	// and [Assembler.Findings].
	findings findingRegistry
	// power is the bridged device of each physical device that serves its
	// battery as PowerSource, as the last walk chose it.
	power powerTargets
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
// what [Assembler.DescribeDevices] turns into the bridged devices the
// node's Aggregator binds.
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
// bridged devices the central projects, under the central's scope, and
// the keys an earlier composition stored for its devices that no
// composition produces any more ([supersededKeys]).
type Described struct {
	Scope      string
	Endpoints  []Endpoint
	Superseded []matterendpoint.SourceKey
}

// Endpoint is one bridged device the walk describes: the go-fabric device
// the node binds, and what the reconciler needs to hold it.
type Endpoint struct {
	// Device is the bridged device; its Spec is what the node assembles.
	Device *fabricdevice.Device
	// Kept says the device is a model source's own
	// ([interfaces.MatterDeviceSource]), which the source keeps across
	// walks: another pointer under the same key is a device the source
	// built anew, and it replaces the held one. A device the walk builds
	// itself is new on every walk and replaces the held one only when what
	// the wire sees changed.
	Kept bool
	// Available reads whether the physical device is reachable at the
	// moment it is asked. A new device starts reachable, so the reconciler
	// states this on it before the node binds it.
	Available func() bool
}

// Spec is the endpoint the node assembles from the device.
func (e Endpoint) Spec() endpoint.Spec { return e.Device.Spec() }

// DescribeDevices walks the model fleets in snapshots and describes every
// bridged device they project, one [Described] per snapshot in order.
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

// snapshotSpecs describes one central's fleet as bridged devices.
func (a *Assembler) snapshotSpecs(ctx context.Context, snap DeviceSnapshot) (Described, error) {
	if snap.CentralName == "" {
		return Described{}, errors.New("matteradapter: snapshot CentralName is required")
	}
	names := a.cfg.NameResolver
	if names == nil {
		names = newModelNameResolver(snap.Devices, a.cfg.Labels, a.channelLabel())
	}
	gen := a.findings.beginWalk()
	out := Described{Scope: snap.CentralName}
	walked := make(map[string]struct{}, len(snap.Devices))
	for _, dev := range snap.Devices {
		if dev == nil {
			continue
		}
		walked[dev.Address] = struct{}{}
		for _, ch := range dev.Channels() {
			if ch != nil {
				out.Superseded = append(out.Superseded, supersededKeys(snap.CentralName, dev, ch)...)
			}
		}
		eps, err := a.describeDevice(ctx, snap.CentralName, names, dev)
		if err != nil {
			return Described{}, err
		}
		out.Endpoints = append(out.Endpoints, eps...)
	}
	a.power.retain(snap.CentralName, walked)
	// The central's walk is complete: a finding it did not meet again
	// belongs to a source that left the fleet (deleted, unexposed, no
	// longer projected) and must not linger as a warning.
	a.findings.prune(snap.CentralName, gen)
	return out, nil
}

// describeDevice describes the bridged devices of one physical device and
// places its battery reading on exactly one of them, as the PowerSource
// cluster (0x002F) served where the Device Library puts it.
//
// PowerSource is a property of the physical device, not of any one function it
// exposes, and BridgedNode (0x0013) — the secondary device type every bridged
// endpoint carries — specifies it as a server cluster. Mounting it on a
// bridged endpoint is therefore conformant whatever that endpoint's primary
// type is, and it is what matter.js's ecosystem notes prescribe for a bridge:
// power-source information belongs at the bridged-node level, not on an
// endpoint whose device type does not specify it.
//
// Exactly one bridged device gets it. A device with a switch and a metering
// channel has one battery, and advertising it twice would have controllers
// show two battery levels for one device. The target is the bridged device
// with the smallest channel number, read back through [keyChannelNo],
// because the lowest-numbered channel is the device's primary function; ties
// keep the earliest, which is the order the channel walk described them in.
//
// go-fabric takes the battery only when a device is built
// (fabricdevice.Measurement), and a model source keeps the device it built,
// so every request for a source must carry the same facts or the source
// builds anew. The walk therefore builds with the target the last walk
// chose ([powerTargets]); when the target it finds differs — the first walk
// of a device, or a lower channel exposed or withheld since — it records
// the new one and describes the device once more, so the two bridged
// devices the battery moves between are built with their new facts.
func (a *Assembler) describeDevice(ctx context.Context, centralName string, names NameResolver, dev *device.Device) ([]Endpoint, error) {
	battery := batteryOf(dev)
	prev := a.power.prepare(centralName, dev.Address, battery)
	eps, err := a.describeChannels(ctx, centralName, names, dev)
	if err != nil || battery == nil {
		return eps, err
	}
	target := powerTarget(eps)
	if target == prev {
		return eps, nil
	}
	a.power.choose(centralName, dev.Address, target)
	return a.describeChannels(ctx, centralName, names, dev)
}

// describeChannels describes the bridged devices of every channel of dev.
func (a *Assembler) describeChannels(ctx context.Context, centralName string, names NameResolver, dev *device.Device) ([]Endpoint, error) {
	out := make([]Endpoint, 0, 4)
	for _, ch := range dev.Channels() {
		if ch == nil {
			continue
		}
		eps, err := a.channelSpecs(ctx, centralName, names, dev, ch, nil)
		if err != nil {
			return nil, err
		}
		out = append(out, eps...)
	}
	return out, nil
}

// batteryOf is the device's battery reading: the first generic data point
// of the battery measurement class. The derived battery level is never a
// candidate (it is folded, not served).
func batteryOf(dev *device.Device) contract.MeasurementSource {
	for _, ch := range dev.Channels() {
		if ch == nil {
			continue
		}
		for _, gdp := range ch.DataPoints() {
			if meas, ok := gdp.(contract.MeasurementSource); ok && meas.MatterMeasurementClass() == contract.MeasurementBattery {
				return meas
			}
		}
	}
	return nil
}

// powerTarget is the key of the bridged device that serves the battery:
// the smallest channel number, ties to the earliest. "" for none.
func powerTarget(eps []Endpoint) string {
	target, best := "", math.MaxInt
	for _, e := range eps {
		k := e.Device.Spec().StableKey
		if n := keyChannelNo(k); n < best {
			target, best = k.String(), n
		}
	}
	return target
}

// keyChannelNo reads the channel number back out of a stable key. The
// bridge module carries the key opaquely, so the assertion — not a parse
// of its rendered form — is how this package recovers its own
// coordinates. A key this package did not build has no channel and sorts
// last, which keeps [powerTarget] deterministic instead of panicking.
func keyChannelNo(k fabricdevice.SourceKey) int {
	if sk, ok := k.(matterendpoint.SourceKey); ok {
		return sk.ChannelNo
	}
	return math.MaxInt
}

// powerTargets remembers, per central and physical device, the battery
// reading and the key of the bridged device that serves it.
type powerTargets struct {
	mu sync.Mutex
	m  map[string]map[string]batteryPlacement // central → device address
}

type batteryPlacement struct {
	battery contract.MeasurementSource
	key     string
}

// prepare records the device's battery for this walk and returns the
// target the last walk chose; a changed battery reading has none yet.
func (p *powerTargets) prepare(centralName, address string, battery contract.MeasurementSource) string {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.m == nil {
		p.m = map[string]map[string]batteryPlacement{}
	}
	devs := p.m[centralName]
	if devs == nil {
		devs = map[string]batteryPlacement{}
		p.m[centralName] = devs
	}
	t := devs[address]
	if t.battery != battery {
		t = batteryPlacement{battery: battery}
	}
	devs[address] = t
	return t.key
}

// choose records key as the device's battery target.
func (p *powerTargets) choose(centralName, address, key string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if devs := p.m[centralName]; devs != nil {
		t := devs[address]
		t.key = key
		devs[address] = t
	}
}

// batteryFor is the battery the bridged device under key serves, nil when
// another bridged device of its physical device serves it.
func (p *powerTargets) batteryFor(key matterendpoint.SourceKey) contract.MeasurementSource {
	p.mu.Lock()
	defer p.mu.Unlock()
	t := p.m[key.CentralName][key.DeviceAddress]
	if t.key == "" || t.key != key.String() {
		return nil
	}
	return t.battery
}

// retain drops the devices of centralName a walk no longer names.
func (p *powerTargets) retain(centralName string, walked map[string]struct{}) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for addr := range p.m[centralName] {
		if _, ok := walked[addr]; !ok {
			delete(p.m[centralName], addr)
		}
	}
}

// hostFacts are the facts the host states on the bridged device under key
// of the physical device dev: its battery when this one serves it, and
// what the CCU knows of the physical device.
func (a *Assembler) hostFacts(key matterendpoint.SourceKey, dev *device.Device) interfaces.MatterHostFacts {
	return interfaces.MatterHostFacts{
		Battery:  a.power.batteryFor(key),
		Identity: deviceIdentity(dev),
	}
}

// deviceRequest is the request every path that asks a model source for
// its device passes — the walk, the projection probe, the eligibility
// listing — so a source sees the same facts whichever asks first and
// builds anew only when a fact changed. name is read on a build only; the
// walk states its own name on the device ([fabricdevice.Device.Update]).
func (a *Assembler) deviceRequest(key matterendpoint.SourceKey, name string, dev *device.Device, ch *device.Channel) interfaces.MatterDeviceRequest {
	return interfaces.MatterDeviceRequest{
		Options: fabricdevice.Options{
			StableKey:      key,
			Name:           name,
			Vendor:         bridgedVendorName,
			DeviceAddress:  dev.Address,
			ChannelAddress: ch.Address,
			Surface:        a.cfg.Build.Surface,
		},
		Build: a.cfg.Build,
		Host:  a.hostFacts(key, dev),
	}
}

// deviceIdentity is what the CCU knows of the physical device, stated as
// every bridged device's BridgedDeviceBasicInformation. The node serves
// SerialNumber only when the host states it, and ProductName and
// ProductLabel fall back to the device address otherwise; matter.js leaves
// all three to the bridge:
//
//   - SerialNumber: the device's address, the hardware serial printed on the
//     device (an HmIP or BidCos address such as 0001D8A991F2DC). It stays
//     distinct from UniqueID, which a controller keys the accessory by.
//   - ProductName and ProductLabel: the device model the CCU reports
//     (HmIP-BROLL-2). matter.js's bridge example states one value for both;
//     a friendlier product label would need a static device database, and
//     the device's own name is already the NodeLabel.
//   - SoftwareVersionString: the installed firmware, when the CCU has
//     reported one; the attribute's constraint has a minimum length of 1.
//
// Each value is held to the attribute's constraint in the generated
// definition and left unstated when it does not fit: go-fabric refuses the
// device on a value that does not fit, and a missing attribute is the
// lesser loss.
func deviceIdentity(dev *device.Device) interfaces.MatterStatedIdentity {
	if dev == nil {
		return interfaces.MatterStatedIdentity{}
	}
	return interfaces.MatterStatedIdentity{
		SerialNumber:          stateString(bdbidef.AttrSerialNumber, dev.Address),
		ProductName:           stateString(bdbidef.AttrProductName, dev.Model),
		ProductLabel:          stateString(bdbidef.AttrProductLabel, dev.Model),
		SoftwareVersionString: stateString(bdbidef.AttrSoftwareVersionString, dev.SwVersion()),
	}
}

// stateString is value when it meets attribute attr's constraint, "" (not
// stated) otherwise.
func stateString(attr uint32, value string) string {
	if value == "" {
		return ""
	}
	inst, err := bridgedBasicInformationChecks()
	if err != nil {
		return ""
	}
	if _, err := inst.CheckValue(attr, value, nil); err != nil {
		return ""
	}
	return value
}

// bridgedBasicInformationChecks holds a value to the generated
// BridgedDeviceBasicInformation definition's type and constraint, the check
// the assembly runs on what the host states.
var bridgedBasicInformationChecks = sync.OnceValues(func() (*clusterspec.Instance, error) {
	return clusterspec.New(bdbidef.Definition, clusterspec.Options{})
})

func (a *Assembler) channelSpecs(ctx context.Context, centralName string, names NameResolver, dev *device.Device, ch *device.Channel, sink DecisionSink) ([]Endpoint, error) { //nolint:gocognit,gocyclo,funlen // single-purpose channel assembly with many device-type/cluster branches
	out := make([]Endpoint, 0, 4)
	note := func(kind matterendpoint.DPKind, key string, outcome SourceOutcome, reason WithheldReason, into string) {
		if sink == nil {
			return
		}
		sink(SourceDecision{
			Key: matterendpoint.SourceKey{
				CentralName: centralName, DeviceAddress: dev.Address, ChannelNo: ch.Number, DPKind: kind, DPKey: key,
			},
			Outcome: outcome, Reason: reason, Into: into,
		})
	}
	withheld := func(kind matterendpoint.DPKind, key string, reason WithheldReason) {
		note(kind, key, OutcomeWithheld, reason, "")
	}
	published := func(kind matterendpoint.DPKind, key string) { note(kind, key, OutcomePublished, "", "") }

	// An operator-hidden channel projects no endpoint, mirroring the
	// candidate enumeration that already drops it. Both gates are needed:
	// hiding does not touch the persisted allowlist row, so without this
	// one a channel exposed BEFORE it was hidden keeps its endpoint in
	// every controller while vanishing from the allowlist surface that
	// could switch it off — the exposure would only be revocable by
	// un-hiding the channel first.
	if ch.IsHidden() {
		withheld("", "", WithheldChannelHidden)
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
		withheld("", "", WithheldSecondaryChannel)
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
		key := dpKey(cdp)
		if src, ok := projectionOf(cdp); ok {
			ok, err := allow(matterendpoint.DPKindCustom, key)
			if err != nil {
				return nil, err
			}
			switch {
			case !ok:
				withheld(matterendpoint.DPKindCustom, key, WithheldNotOnAllowList)
			default:
				if spec, built := a.makeSpec(ctx, centralName, names, dev, ch, matterendpoint.DPKindCustom, key, src); built {
					out = append(out, spec)
					published(matterendpoint.DPKindCustom, key)
				} else {
					withheld(matterendpoint.DPKindCustom, key, WithheldNoMapping)
				}
			}
		} else {
			withheld(matterendpoint.DPKindCustom, key, WithheldNoMapping)
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
				withheld(matterendpoint.DPKindCalculated, key, WithheldNotOnAllowList)
				continue
			}
			if spec, built := a.makeSpec(ctx, centralName, names, dev, ch, matterendpoint.DPKindCalculated, key, src); built {
				out = append(out, spec)
				published(matterendpoint.DPKindCalculated, key)
			} else {
				withheld(matterendpoint.DPKindCalculated, key, WithheldNoMapping)
			}
			continue
		}
		meas, ok := calc.(contract.MeasurementSource)
		if !ok || meas.MatterMeasurementClass() == contract.MeasurementNone {
			withheld(matterendpoint.DPKindCalculated, key, WithheldNoMapping)
			continue
		}
		if !a.cfg.IncludeMeasurements {
			withheld(matterendpoint.DPKindCalculated, key, WithheldMeasurementsOff)
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
			withheld(matterendpoint.DPKindCalculated, key, WithheldNotOnAllowList)
			continue
		}
		if meas.MatterMeasurementClass() == contract.MeasurementBattery {
			// A battery has no device type, so it never becomes an endpoint
			// of its own (see describeDevice). The derived level does not
			// feed the PowerSource either: that reads the device's generic
			// battery data point, so this source projects nothing.
			withheld(matterendpoint.DPKindCalculated, key, WithheldNoMapping)
			continue
		}
		if spec, ok := a.makeMeasurementSpec(centralName, names, dev, ch, matterendpoint.DPKindCalculated, key, meas); ok {
			out = append(out, spec)
			published(matterendpoint.DPKindCalculated, key)
		} else {
			withheld(matterendpoint.DPKindCalculated, key, WithheldNoMapping)
		}
	}

	// Combined DPs.
	for _, comb := range ch.CombinedDataPoints() {
		key := dpKey(comb)
		src, ok := projectionOf(comb)
		if !ok {
			withheld(matterendpoint.DPKindCombined, key, WithheldNoMapping)
			continue
		}
		allowed, err := allow(matterendpoint.DPKindCombined, key)
		if err != nil {
			return nil, err
		}
		if !allowed {
			withheld(matterendpoint.DPKindCombined, key, WithheldNotOnAllowList)
			continue
		}
		if spec, built := a.makeSpec(ctx, centralName, names, dev, ch, matterendpoint.DPKindCombined, key, src); built {
			out = append(out, spec)
			published(matterendpoint.DPKindCombined, key)
		} else {
			withheld(matterendpoint.DPKindCombined, key, WithheldNoMapping)
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
		if reason := usageGate(gdp, channelHasCustom, a.cfg.ExposeSecondaryChannels); reason != "" {
			withheld(matterendpoint.DPKindGeneric, key, reason)
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
				if src.projects(a.deviceRequest(genericKey(centralName, dev, ch, key), endpoint.ComposeNodeLabel(names.EndpointLabel(genericKey(centralName, dev, ch, key)), ""), dev, ch)) {
					allowed, err := allow(matterendpoint.DPKindGeneric, key)
					if err != nil {
						return nil, err
					}
					if !allowed {
						withheld(matterendpoint.DPKindGeneric, key, WithheldNotOnAllowList)
						continue
					}
					if spec, built := a.makeSpec(ctx, centralName, names, dev, ch, matterendpoint.DPKindGeneric, key, src); built {
						out = append(out, spec)
						published(matterendpoint.DPKindGeneric, key)
					} else {
						withheld(matterendpoint.DPKindGeneric, key, WithheldNoMapping)
					}
					continue
				}
			}
		}
		// Path 2: Generic-DP measurement source.
		meas, ok := gdp.(contract.MeasurementSource)
		if !ok || meas.MatterMeasurementClass() == contract.MeasurementNone {
			withheld(matterendpoint.DPKindGeneric, key, WithheldNoMapping)
			continue
		}
		allowed, err := allow(matterendpoint.DPKindGeneric, key)
		if err != nil {
			return nil, err
		}
		if !allowed {
			withheld(matterendpoint.DPKindGeneric, key, WithheldNotOnAllowList)
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
			// describeDevice, never as an endpoint of its own — it has no
			// device type, and an endpoint without one advertises
			// DeviceTypeList=[BridgedNode] alone.
			note(matterendpoint.DPKindGeneric, key, OutcomeFolded, "", FoldedIntoPowerSource)
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
			published(matterendpoint.DPKindGeneric, key)
		} else {
			withheld(matterendpoint.DPKindGeneric, key, WithheldNoMapping)
		}
	}

	if len(pressMembers) > 0 {
		spec, ok := a.makeButtonGroupSpec(centralName, names, dev, ch, pressMembers)
		if ok {
			out = append(out, spec)
			published(matterendpoint.DPKindGeneric, ButtonGroupDPKey)
		}
		noteGroup(note, pressMembers, ok, ButtonGroupDPKey)
	}

	if len(electricalMembers) > 0 {
		spec, ok := a.makeElectricalGroupSpec(centralName, names, dev, ch, electricalMembers)
		if ok {
			out = append(out, spec)
			published(matterendpoint.DPKindGeneric, ElectricalGroupDPKey)
		}
		noteGroup(note, electricalMembers, ok, ElectricalGroupDPKey)
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
) (Endpoint, bool) {
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
		return Endpoint{}, false
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
) (Endpoint, bool) {
	srcs := make([]generic.PressEventSource, 0, len(members))
	for _, m := range members {
		srcs = append(srcs, m)
	}
	group := generic.NewButtonGroup(srcs...)
	if group == nil {
		return Endpoint{}, false
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
	return usageGate(source, channelHasCustom, exposeSecondary) != ""
}

// usageGate is [hideFromMatter] with the rule it applied: the reason a
// generic data point is withheld by its usage, or "" when it is not.
func usageGate(source any, channelHasCustom, exposeSecondary bool) WithheldReason {
	u, ok := source.(interface{ Usage() hmenum.DataPointUsage })
	if !ok {
		return ""
	}
	switch u.Usage() {
	case hmenum.DataPointUsageIgnored:
		return WithheldUsageIgnored
	case hmenum.DataPointUsageNoCreate:
		if channelHasCustom || !exposeSecondary {
			return WithheldConsumedByParent
		}
	case hmenum.DataPointUsageCDPSecondary, hmenum.DataPointUsageCDPState:
		if !exposeSecondary {
			return WithheldSecondaryMember
		}
	default:
	}
	return ""
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

// projects reports whether the source projects an endpoint at all: a
// device source once it built its device for req (the source keeps it, so
// the later spec reuses it), a server source when it serves a cluster.
func (p projection) projects(req interfaces.MatterDeviceRequest) bool {
	if p.device != nil {
		d, err := p.device.MatterDevice(req)
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
	ctx context.Context,
	centralName string,
	names NameResolver,
	dev *device.Device,
	ch *device.Channel,
	kind matterendpoint.DPKind,
	key string,
	src projection,
) (Endpoint, bool) {
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
		return a.declaredDeviceSpec(ctx, in, src.device)
	}
	if len(src.source.MatterClusterServers()) == 0 && kind == matterendpoint.DPKindGeneric {
		return Endpoint{}, false
	}
	in.source = src.source
	return a.deviceSpec(in)
}

// genericKey is the key of the generic data point key on channel ch.
func genericKey(centralName string, dev *device.Device, ch *device.Channel, key string) matterendpoint.SourceKey {
	return matterendpoint.SourceKey{
		CentralName:   centralName,
		DeviceAddress: dev.Address,
		ChannelNo:     ch.Number,
		DPKind:        matterendpoint.DPKindGeneric,
		DPKey:         key,
	}
}

// declaredDeviceSpec describes the endpoint of a source that builds its
// device itself from its cluster declarations. The device is the
// source's: it lives as long as the source and is built anew only when the
// host's facts change ([interfaces.MatterDeviceSource]). The walk's name is
// the host's statement on it ([fabricdevice.Device.Update]): served at once
// to a subscribed controller when the device is bound, unless a written
// label wins, and the name the node assembles otherwise. A source that
// projects nothing, or whose device the kit refuses, yields no endpoint —
// there is no hand-built fallback, because the servers exist only inside
// the device.
func (a *Assembler) declaredDeviceSpec(ctx context.Context, in specInput, src interfaces.MatterDeviceSource) (Endpoint, bool) {
	d, err := src.MatterDevice(a.deviceRequest(in.key, in.friendlyName, in.dev, in.ch))
	if err != nil {
		code := FindingDeviceRefused
		if errors.Is(err, surface.ErrWithheld) {
			code = FindingDeviceWithheld
		}
		a.recordFinding(in, code, err)
		return Endpoint{}, false
	}
	if d == nil {
		return Endpoint{}, false
	}
	if err := d.Update(ctx, func(st *fabricdevice.HostStatement) { st.Name = in.friendlyName }); err != nil {
		a.recordFinding(in, FindingDeviceRefused, err)
		return Endpoint{}, false
	}
	a.findings.clear(in.key.String())
	return Endpoint{Device: d, Kept: true, Available: in.dev.Available}, true
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
) (Endpoint, bool) {
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
// Every bridged endpoint is a device of that layer. A device type the
// generated layer does not carry, or a composition it refuses, yields no
// endpoint (false) and a [FindingDeviceRefused], which the Matter status
// and selection pages name; a device the specification level withholds
// yields a [FindingDeviceWithheld]. A hand-built Spec in their place would
// be the one endpoint no go-fabric device stands behind: no validation by
// [fabricdevice.New], no surface report and no
// [fabricdevice.Device.SetReachable].
func (a *Assembler) deviceSpec(in specInput) (Endpoint, bool) {
	t, ok := fabricdevice.Lookup(uint32(in.deviceType))
	if !ok {
		a.recordFinding(in, FindingDeviceRefused, fmt.Errorf("%w: 0x%04X", errDeviceTypeNotInLayer, in.deviceType))
		return Endpoint{}, false
	}
	req := a.deviceRequest(in.key, in.friendlyName, in.dev, in.ch)
	decls := req.Host.Declarations()
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
	d, err := fabricdevice.New(t, req.Options, decls...)
	if err != nil {
		code := FindingDeviceRefused
		if errors.Is(err, surface.ErrWithheld) {
			code = FindingDeviceWithheld
		}
		a.recordFinding(in, code, err)
		return Endpoint{}, false
	}
	a.findings.clear(in.key.String())
	return Endpoint{Device: d, Available: in.dev.Available}, true
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

// logRefused reports a source whose own device the kit refused. The source
// yields no endpoint, so it is logged as a warning.
func (a *Assembler) logRefused(deviceType uint16, key string, err error) {
	a.logger.Warn("matter.assembler.device_refused",
		slog.String("device_type", fmt.Sprintf("0x%04X", deviceType)),
		slog.String("key", key),
		slog.String("err", err.Error()))
}

// errDeviceTypeNotInLayer is the refusal of a device type go-fabric's
// generated device layer does not carry.
var errDeviceTypeNotInLayer = errors.New("device type not in the generated device layer")

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
