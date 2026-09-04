// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package interfaces

import (
	"context"
	"fmt"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// MatterEndpointSource is implemented by Custom DPs that materialise
// as their own bridged endpoint in the v1.1 Matter bridge.
//
// The endpoint assembler in internal/north/matter/endpoint walks the
// model and creates one Matter endpoint per implementer. The bridge
// owns no DP-specific code — it iterates the model and dispatches via
// this interface and [MatterClusterServer]. See ADR 0012 §"Source
// surface" for the rich-model / dumb-bridge rationale, and
// SPECIFICATION.md §6.2 / §6.3.
type MatterEndpointSource interface {
	// MatterDeviceType reports the Matter Device Type ID
	// (e.g., 0x010A OnOffPlugInUnit, 0x0301 Thermostat) the bridged
	// endpoint advertises.
	MatterDeviceType() uint16

	// MatterClusterServers returns the cluster-server contributions
	// the DP exposes on its endpoint. Order does not matter; the
	// endpoint assembler deduplicates by cluster ID.
	MatterClusterServers() []MatterClusterServer
}

// MatterClusterServer is implemented by anything that contributes a
// Matter cluster — a Custom DP, a Generic DP grouped onto an
// endpoint, or a Calculated DP exposing a derived attribute.
//
// The bridge handles TLV encoding; implementations work in
// cluster-native Go types (bool, uint8, int16, ...) and rely on the
// bridge to type-assert based on the cluster spec's attribute /
// command schema. Use of `any` is justified here: Matter attribute
// and command payloads vary per (cluster, attribute) pair and cannot
// be expressed in a single Go type without fragmenting the interface
// into one method per cluster.
type MatterClusterServer interface {
	// MatterClusterID identifies the cluster
	// (e.g., 0x0006 OnOff, 0x0008 LevelControl, 0x0102 WindowCovering).
	MatterClusterID() uint32

	// MatterRead resolves an attribute value at read time. The
	// returned value uses the cluster's native Go type. Returns
	// (nil, false) when the underlying DP has not been observed yet
	// — the bridge maps that to a stale-data Matter status response.
	MatterRead(attrID uint32) (value any, ok bool)

	// MatterWrite applies an attribute write. The bridge has already
	// decoded the TLV; `value` carries the cluster-native type. The
	// priority is forwarded to the southbound command queue.
	MatterWrite(ctx context.Context, attrID uint32, value any, priority hmenum.CommandPriority) error

	// MatterInvoke dispatches a cluster command. `fields` is the
	// cluster-native struct (or nil for parameterless commands) for
	// the request payload; `response` is the cluster-native struct
	// for the response, or nil for status-only commands.
	MatterInvoke(ctx context.Context, cmdID uint32, fields any, priority hmenum.CommandPriority) (response any, err error)

	// MatterReportable lists attribute IDs that emit Matter reports
	// when the underlying DP fires OnEvent. Empty slice = none.
	MatterReportable() []uint32
}

// FabricScopedReader is the optional capability a [MatterClusterServer]
// can implement when one or more of its attributes are fabric-sensitive
// (Matter §7.5.2 — fabric-scoped fields). When a cluster server
// implements this interface the endpoint dispatcher calls
// MatterReadFiltered instead of MatterRead so that the FabricFiltered
// flag + the requesting FabricIndex (both stamped into the context by
// [im.WithFabricFilter]) can be honoured.
//
// Mirrors matter.js's OnlineContext.forFabricFilteredRead pattern in
// packages/protocol/src/interaction/InteractionServer.ts:startReadInteraction
// — the context carries the filter rather than propagating via a
// separate call-stack argument.
//
// Cluster servers that do NOT implement FabricScopedReader return their
// full attribute value from MatterRead — the safe default that matches
// the FabricFiltered=false path.
type FabricScopedReader interface {
	// MatterReadFiltered resolves an attribute value with awareness of
	// the FabricFiltered flag. ctx carries the filter via
	// [im.FabricFilterFromContext]; implementations extract it and, when
	// filtered=true and fabricIndex != 0, return only the entries that
	// belong to fabricIndex.
	MatterReadFiltered(ctx context.Context, attrID uint32) (value any, ok bool)
}

// MatterClusterAttributeLister is the optional capability a
// [MatterClusterServer] can implement to advertise its full
// attribute set. Used by the IM dispatcher to expand wildcard
// reads (Matter §8.4.3.1) — a Read with `HasAttribute=false` then
// returns one ReadResult per advertised attribute instead of just
// the two universal globals (FeatureMap + ClusterRevision).
//
// The returned list MUST contain every attribute the cluster
// surfaces via MatterRead, but MUST NOT contain the universal
// globals — the dispatcher always merges those in. Order matters
// for AttributeList (§10.4.1.4) responses; sort by attribute ID.
type MatterClusterAttributeLister interface {
	MatterAttributes() []uint32
}

// MatterClusterCommandLister is the optional capability a
// [MatterClusterServer] can implement to advertise the cluster's
// command surface. The IM dispatcher consults it to synthesise the
// global `AcceptedCommandList` (0xFFF9) and `GeneratedCommandList`
// (0xFFF8) attributes per Matter Core Spec §7.13.2.{2,3}. Apple Home's
// HAP service rebuild reads both lists to wire HomeKit characteristics
// to the cluster commands; clusters that don't implement this
// interface fall back to empty lists, which is spec-compliant for
// command-less clusters (e.g. measurement, BasicInformation).
type MatterClusterCommandLister interface {
	// MatterAcceptedCommands returns the cluster command IDs the
	// server handles via MatterInvoke. Order is irrelevant for the
	// wire protocol but stable ordering helps deterministic snapshots.
	MatterAcceptedCommands() []uint32
	// MatterGeneratedCommands returns the cluster command IDs the
	// server may emit as InvokeResponse payloads (typically the
	// "Response" suffix variant of an Accepted command — e.g.
	// `ArmFailSafeResponse` for `ArmFailSafe`).
	MatterGeneratedCommands() []uint32
}

// MatterClusterDataVersion is the optional capability a
// [MatterClusterServer] can implement to expose its current DataVersion.
// The IM read layer reads it once per cluster per request and stamps it
// on every AttributeDataIB returned for that cluster. Without this
// capability, the IM layer falls back to a constant 1.
//
// Mirrors matter.js packages/protocol/src/interaction/InteractionServer.ts
// startReadInteraction DataVersion stamping on AttributeReportPayload.
type MatterClusterDataVersion interface {
	// MatterDataVersion returns the current per-cluster monotonic counter.
	// Matter §10.6.5: "A DataVersion of zero is reserved for absent or
	// invalid"; implementations MUST return ≥ 1.
	MatterDataVersion() uint32
}

// MatterClusterEventLister is the optional capability a
// [MatterClusterServer] can implement to advertise the cluster's
// event surface. The dispatcher consults it to synthesise the global
// `EventList` (0xFFFA) attribute per Matter Core Spec §7.13.2.5.
// Clusters that don't implement this interface report an empty
// EventList, which is spec-compliant for clusters that emit no events.
type MatterClusterEventLister interface {
	// MatterEvents returns the cluster event IDs the server may
	// surface via subscribed event reports.
	MatterEvents() []uint32
}

// MatterClusterAttributeReadPrivilege is the optional capability a
// [MatterClusterServer] can implement when one or more of its attributes
// require a read privilege higher than View (1). The IM read layer
// calls MinReadPrivilege for the attribute ID before calling Read; if
// the method returns a value > 1 the ACLChecker is consulted with that
// higher privilege, resulting in UnsupportedAccess (0x7e) for
// insufficiently privileged sessions.
//
// Example: AccessControl (0x001F) attributes ACL (0x0000) and Extension
// (0x0001) require Administer (5) per Matter §9.10.5.3. Mirrors chip
// src/app/clusters/access-control-server/access-control-server.cpp which
// checks ADMINISTER privilege for those two attribute reads, and
// matter.js packages/model/src/standard/elements/access-control.element.ts
// (acl + extension access: "administer").
type MatterClusterAttributeReadPrivilege interface {
	// MinReadPrivilege returns the minimum Matter privilege level
	// required to read the given attribute. Return 1 (View) when the
	// attribute has no elevated requirement (which is the common case
	// and matches the default read behaviour). The privilege constants
	// mirror Matter §9.10.4.4: 1=View, 3=Operate, 4=Manage, 5=Administer.
	MinReadPrivilege(attrID uint32) uint8
}

// MatterClusterAttributeWritePrivilege is the optional capability a
// [MatterClusterServer] can implement when one or more of its writable
// attributes require a write privilege higher than the Operate (3)
// default. The IM write layer calls MinWritePrivilege for the attribute
// ID before dispatching the write; the ACLChecker is consulted with the
// returned privilege, resulting in UnsupportedAccess (0x7e) for
// insufficiently privileged sessions.
//
// Example: AccessControl (0x001F) attributes ACL (0x0000) and Extension
// (0x0001) require Administer (5) per Matter §9.10.5.3 (access "RW … A");
// BasicInformation (0x0028) NodeLabel/Location require Manage (4) per
// access "RW VM". Mirrors matter.js
// packages/model/src/standard/elements/*.element.ts writeAccess bits.
type MatterClusterAttributeWritePrivilege interface {
	// MinWritePrivilege returns the minimum Matter privilege level
	// required to write the given attribute. Return 3 (Operate) when the
	// attribute has no elevated requirement (the common case, matching
	// the default write behaviour). Constants mirror Matter §9.10.4.4:
	// 1=View, 3=Operate, 4=Manage, 5=Administer.
	MinWritePrivilege(attrID uint32) uint8
}

// MatterClusterCommandInvokePrivilege is the optional capability a
// [MatterClusterServer] can implement when one or more of its commands
// require an invoke privilege higher than the Operate (3) default. The
// IM invoke layer calls MinInvokePrivilege for the command ID before
// dispatching; the ACLChecker is consulted with the returned privilege,
// resulting in UnsupportedAccess (0x7e) for insufficiently privileged
// sessions.
//
// Example: OperationalCredentials (0x003E) commands (AddNOC, UpdateNOC,
// RemoveFabric, …) and AdministratorCommissioning (0x003C)
// OpenCommissioningWindow require Administer (5) per Matter §11.18 /
// §11.19 (access "A"). Mirrors matter.js
// packages/model/src/standard/elements/*.element.ts invokeAccess bits.
type MatterClusterCommandInvokePrivilege interface {
	// MinInvokePrivilege returns the minimum Matter privilege level
	// required to invoke the given command. Return 3 (Operate) when the
	// command has no elevated requirement (the common case, matching the
	// default invoke behaviour). Constants mirror Matter §9.10.4.4:
	// 1=View, 3=Operate, 4=Manage, 5=Administer.
	MinInvokePrivilege(cmdID uint32) uint8
}

// MatterMeasurementClass classifies Generic.Sensor / BinarySensor and
// Calculated DP instances by Matter cluster without name-matching at
// publish time. The model layer computes this once at materialisation
// from the same [hmenum.Parameter] classifier that already drives MQTT
// payload routing; the bridge consumes it.
type MatterMeasurementClass int

// MatterMeasurementClass values. Each constant corresponds to the
// Matter cluster the DP projects to. [MatterMeasurementNone] opts the
// DP out of the Matter surface entirely (used for opaque-string
// sensors, weather data without a Matter cluster, etc.).
const (
	MatterMeasurementNone            MatterMeasurementClass = iota
	MatterMeasurementTemperature                            // 0x0402 TemperatureMeasurement
	MatterMeasurementHumidity                               // 0x0405 RelativeHumidityMeasurement
	MatterMeasurementIlluminance                            // 0x0400 IlluminanceMeasurement
	MatterMeasurementPressure                               // 0x0403 PressureMeasurement
	MatterMeasurementCO2                                    // 0x040D CarbonDioxideConcentrationMeasurement
	MatterMeasurementPM25                                   // 0x042A PM2_5ConcentrationMeasurement
	MatterMeasurementPM10                                   // 0x042D PM10ConcentrationMeasurement
	MatterMeasurementOccupancy                              // 0x0406 OccupancySensing
	MatterMeasurementContact                                // 0x0045 BooleanState (ContactSensor endpoint)
	MatterMeasurementLeak                                   // 0x0045 BooleanState (ContactSensor endpoint; see MatterMeasurementClassDeviceType)
	MatterMeasurementBattery                                // 0x002F PowerSource
	MatterMeasurementPower                                  // 0x0090 ElectricalPowerMeasurement
	MatterMeasurementEnergy                                 // 0x0091 ElectricalEnergyMeasurement
	MatterMeasurementMomentarySwitch                        // 0x003B Switch (Generic Switch endpoint)
	MatterMeasurementElectrical                             // 0x0090 + 0x0091 + 0x009C (ElectricalSensor endpoint)
)

// MatterElectricalReadings is the typed read surface of a consolidated
// electrical measurement group. One CCU channel reports POWER, VOLTAGE,
// CURRENT, FREQUENCY and ENERGY_COUNTER as separate parameters, while Matter
// groups the first four into ElectricalPowerMeasurement (0x0090) attributes
// and the fifth into ElectricalEnergyMeasurement (0x0091) — both on one
// ElectricalSensor endpoint. Implemented by [generic.ElectricalGroup].
//
// Every accessor returns (value, false) when the device does not report that
// parameter, which the cluster layer renders as a Matter null rather than as
// an unsupported attribute: the attribute is specified for the cluster, the
// reading simply is not there.
//
// Units are the ones the CCU reports, converted at the cluster boundary:
// watts, volts, milliamperes, hertz, watt-hours.
//
// loom:reachable:reason="held as a struct field type in production — measurement.ElectricalPowerServer.readings and measurement.energyOf.r — which the analyzer's reachability walk does not count as a use of the interface itself"
type MatterElectricalReadings interface {
	ActivePower() (value float64, observed bool)
	Voltage() (value float64, observed bool)
	Current() (value float64, observed bool)
	Frequency() (value float64, observed bool)
	Energy() (value float64, observed bool)

	// HasEnergy reports whether the source carries an energy counter at all.
	// The cluster layer decides the endpoint's ServerList from this, never
	// from Energy()'s observed flag: a Matter endpoint's cluster set is
	// quasi-static, so a cluster gated on a not-yet-reported value would
	// appear mid-session after controllers cached the list.
	HasEnergy() bool
}

// MatterMeasurementSource is implemented by Generic / Calculated DPs
// that project to a single Matter measurement cluster. The endpoint
// assembler uses this to decide whether to build a standalone sensor
// endpoint or attach an extra cluster to an existing host endpoint.
type MatterMeasurementSource interface {
	MatterMeasurementClass() MatterMeasurementClass
}

// MatterFloatMeasurementSource is the typed read surface for scalar
// measurement classes (Temperature, Humidity, Illuminance, Pressure,
// CO2, PM2.5, PM10). Implemented by Generic.Sensor[float64] and the
// equivalent calculated-DP types.
//
// MatterFloatValue returns the current observed value in the model's
// native unit (°C for temperature, % RH for humidity, lux for
// illuminance, hPa for pressure, ppm for CO2, µg/m³ for particulates).
// `observed` is false when no measurement has been received yet — the
// bridge maps that to a Matter-spec NULL response (e.g. -32768 sentinel
// for nullable int16 attributes).
//
// Unit conversion to the Matter wire scale is done by the cluster
// server, not the model — the model's unit is the canonical one.
type MatterFloatMeasurementSource interface {
	MatterMeasurementSource
	MatterFloatValue() (value float64, observed bool)
}

// MatterBoolMeasurementSource is the typed read surface for boolean
// measurement classes (Contact, Leak, Occupancy). Implemented by
// Generic.BinarySensor.
//
// MatterBoolValue returns the current observed boolean state.
// `observed` is false until the first event arrives.
//
// Polarity: per Matter spec, BooleanState.StateValue=true means
// "active" / "alarm" / "contact closed" depending on endpoint type;
// OccupancySensing.Occupancy bit 0 means "occupied". The classifier
// in `internal/model/generic/matter.go` chose the parameter set so
// the boolean polarity matches Matter's expectation; cluster servers
// therefore pass the value through verbatim.
type MatterBoolMeasurementSource interface {
	MatterMeasurementSource
	MatterBoolValue() (value, observed bool)
}

// MatterChangeNotifier is the push-side complement to the read-side
// MatterFloatMeasurementSource / MatterBoolMeasurementSource pull
// interfaces. Implementations fire `cb` whenever their observable
// Matter value changes; the bridge wires this at endpoint-mount time
// so the Subscribe engine marks the corresponding attribute path
// dirty and the next tick ships a ReportData with the new value.
//
// Mirrors matter.js's reactor-style observation of
// `events.<attr>$Changed` (see matter.js
// packages/node/src/behaviors/thermostat/ThermostatServer.ts:450 for
// the canonical `reactTo(...measuredValue$Changed, handler)` pattern).
// Without this push path the Subscribe engine emits only empty
// heartbeats and Apple Home shows bridged sensors as "not responding"
// shortly after the initial ReportData expires.
//
// Returns an unsubscribe closure; idempotent — calling it more than
// once is a no-op. The closure is the only safe way to detach the
// callback; do not retain `cb` references elsewhere.
type MatterChangeNotifier interface {
	OnMatterValueChanged(cb func()) (unsubscribe func())
}

// MatterEventPriority mirrors the Matter §10.6.6.1 priority enum.
type MatterEventPriority uint8

// MatterEventPriority values.
const (
	// MatterEventPriorityDebug — least important; controllers may drop.
	MatterEventPriorityDebug MatterEventPriority = 0
	// MatterEventPriorityInfo — informational.
	MatterEventPriorityInfo MatterEventPriority = 1
	// MatterEventPriorityCritical — must be delivered;
	// bypasses MinIntervalFloor when subscribed.
	MatterEventPriorityCritical MatterEventPriority = 2
)

// MatterEventEmitter is the bridge-side hook a cluster server (or
// model-package-side cluster server) calls when its underlying DP
// fires an event that should surface to subscribers. The bridge wires
// this to [subscription.Manager.OnEventFired], fanning the event out
// to every subscription whose EventPaths cover the (endpoint, cluster,
// event) triple.
//
// `data` is the cluster-native event payload (struct, scalar, …).
// `priority` drives the urgency gate per Matter §10.6.6.
type MatterEventEmitter interface {
	MatterEmitEvent(endpoint uint16, cluster, event uint32, data any, priority MatterEventPriority)
}

// MatterEventReceiver is implemented by cluster servers that emit
// events. The bridge calls SetMatterEventEmitter at endpoint-assembly
// time so the cluster can fire events at any later moment without
// holding a reference to the bridge.
type MatterEventReceiver interface {
	SetMatterEventEmitter(emitter MatterEventEmitter)
}

// MatterEligibilityState classifies a model source as
// mappable / partially mappable / unmappable for the operator-facing
// allowlist UI. Stored on the source itself (rich model, dumb bridge)
// so the UI does not have to maintain a parallel classification table.
type MatterEligibilityState uint8

// MatterEligibilityState values. Stable string forms in
// `(MatterEligibilityState).String()` mirror the JSON tokens the
// REST API uses (`mappable`, `partially_mappable`, `unmappable`).
const (
	// MatterEligibilityUnmappable means no Matter cluster covers the
	// source. UI shows ⛔; the allowlist toggle is permanently disabled.
	MatterEligibilityUnmappable MatterEligibilityState = iota
	// MatterEligibilityMappable means the source has a complete Matter
	// projection. UI toggle is active; the assembler bridges the source
	// when the operator enables it.
	MatterEligibilityMappable
	// MatterEligibilityPartial means a partial projection exists —
	// some clusters map, some features stay MQTT-only (e.g. siren
	// tones, light effect playlists). UI shows ⚠ with the reason; the
	// toggle remains active.
	MatterEligibilityPartial
)

// String returns a stable lowercase token matching the JSON / log
// shape used by `/api/v1/matter/exposable`.
func (s MatterEligibilityState) String() string {
	switch s {
	case MatterEligibilityMappable:
		return "mappable"
	case MatterEligibilityPartial:
		return "partially_mappable"
	case MatterEligibilityUnmappable:
		return "unmappable"
	default:
		return "unknown"
	}
}

// MatterEligibilityVerdict is the per-source classification result
// the UI renders. DeviceType + Clusters are zero / empty when
// State == Unmappable; Reason is non-empty for Partial / Unmappable
// (UI-renderable explanation).
type MatterEligibilityVerdict struct {
	State      MatterEligibilityState
	DeviceType uint16
	Clusters   []uint32
	Reason     string
}

// MatterEligibilitySource is implemented by every model DP whose
// Matter eligibility is known at construction time. The default
// implementation `DeriveMatterEligibility` (in
// internal/north/matter/eligibility) handles the common case for any
// type that already implements MatterEndpointSource or
// MatterMeasurementSource. DPs with caveats (Siren tone selection,
// EffectLight effect dispatch, FixedColorLight palette quantisation)
// override the method to return MatterEligibilityPartial with a
// human-readable reason.
//
// Sources that do not implement this interface are treated as
// Unmappable by the eligibility classifier — the model then must
// either implement MatterEndpointSource / MatterMeasurementSource
// (and inherit the default) or surface explicitly as Unmappable
// (textdisplay.TextDisplay, valve.Irrigation, etc.).
type MatterEligibilitySource interface {
	MatterEligibility() MatterEligibilityVerdict
}

// MatterMeasurementClassDeviceType returns the standalone Matter
// Device Type (uint16) that best wraps the given measurement class
// when the source is materialised as its own sensor endpoint. Zero
// for `MatterMeasurementNone` and any value with no standalone
// device-type counterpart (Battery / Power / Energy roll up to a
// host endpoint instead).
//
// Single source of truth for the measurement-class → device-type
// mapping; both the assembler's standalone-endpoint path and the
// eligibility classifier's verdict-derivation read through here.
func MatterMeasurementClassDeviceType(class MatterMeasurementClass) uint16 {
	switch class {
	case MatterMeasurementTemperature:
		return 0x0302 // TemperatureSensor
	case MatterMeasurementHumidity:
		return 0x0307 // HumiditySensor
	case MatterMeasurementIlluminance:
		return 0x0106 // LightSensor
	case MatterMeasurementPressure:
		return 0x0305 // PressureSensor
	case MatterMeasurementCO2, MatterMeasurementPM25, MatterMeasurementPM10:
		return 0x002C // AirQualitySensor
	case MatterMeasurementOccupancy:
		return 0x0107 // OccupancySensor
	case MatterMeasurementContact, MatterMeasurementLeak:
		// Leak deliberately materialises as ContactSensor (0x0015)
		// instead of the dedicated WaterLeakDetector (0x0043, a
		// Matter-1.3-introduced detector type; matter.js
		// packages/model/src/standard/elements/water-leak-detector.element.ts).
		// Ecosystem ceiling: Amazon Alexa's bridge support predates the
		// detector device types, and a single endpoint advertising
		// 0x0043 renders the whole bridged node unresponsive there.
		// Wire shape mirrors matter.js
		// packages/model/src/standard/elements/contact-sensor.element.ts
		// (device type 0x15, mandatory BooleanState 0x45 server).
		// Polarity is non-inverted alarm semantics: the model's boolean
		// passes through verbatim, so a detected leak reports
		// StateValue=true (which ContactSensor renders as
		// "closed/contact" per cluster §1.7.5.1, matter.js
		// packages/model/src/standard/resources/boolean-state.resource.ts)
		// and dry reports StateValue=false ("open/no contact").
		// Divergence from matter.js device-type selection is recorded
		// in notes/parity/by_design.md.
		return 0x0015 // ContactSensor
	case MatterMeasurementMomentarySwitch:
		return 0x000F // GenericSwitch
	case MatterMeasurementElectrical:
		// The Device Library's carrier for ElectricalPowerMeasurement +
		// ElectricalEnergyMeasurement, with PowerTopology mandatory
		// alongside them (matter.js electrical-sensor.element.ts). The
		// per-parameter Power / Energy classes below never reach an
		// endpoint of their own: the assembler consolidates them into one
		// ElectricalGroup, exactly as it does for press parameters.
		return 0x0510 // ElectricalSensor
	default:
		// Battery, and the per-parameter Power / Energy classes that the
		// assembler folds into an ElectricalGroup before an endpoint is
		// built. PowerSource rides on the bridged endpoint of the device
		// it powers, which BridgedNode (0x0013) specifies for it.
		return 0
	}
}

// MatterDeviceTypeName returns the operator-facing name for a Matter
// Device Type ID. Returns the empty string for `0` (no device type)
// and a hex fallback like "0x0123" for IDs the model does not project
// to — the UI then still has something stable to render and to filter
// on.
//
// Single source of truth for the device-type → human label mapping;
// the REST layer surfaces the result as `device_type_label` on each
// `/api/v1/matter/exposable` row so the SPA does not have to maintain
// a parallel map. Because the SPA groups, filters and text-searches
// the exposure list by that string, a device type reaching the hex
// fallback is a device an operator cannot find by name.
//
// Every device type the model can advertise — from a MatterDeviceType
// method or from [MatterMeasurementClassDeviceType] — must therefore
// have a case here. That is measured, not asserted, by
// TestW2PkgMatterDeviceTypeNameCoversEveryAdvertisedType in
// tests/contract, which also checks each ID against the matter.js HEAD
// device-type table in internal/north/matter/schema.
//
// The labels spell out what the matter.js name compresses
// ("OnOffPlugInUnit" → "On/Off Plug-in Unit"); the IDs and the
// existence of each type come from that generated table, never from a
// reading of the specification.
func MatterDeviceTypeName(id uint16) string {
	switch id {
	case 0:
		return ""
	case 0x000A:
		return "Door Lock"
	case 0x000F:
		return "Generic Switch"
	case 0x0015:
		return "Contact Sensor"
	case 0x002C:
		return "Air Quality Sensor"
	case 0x0043:
		return "Water Leak Detector"
	case 0x0076:
		return "Smoke / CO Alarm"
	case 0x0100:
		return "On/Off Light"
	case 0x0101:
		return "Dimmable Light"
	case 0x0106:
		return "Light Sensor"
	case 0x0107:
		return "Occupancy Sensor"
	case 0x010A:
		return "On/Off Plug-in Unit"
	case 0x010C:
		return "Color Temperature Light"
	case 0x010D:
		return "Extended Color Light"
	case 0x0202:
		return "Window Covering"
	case 0x0230:
		// Advertised by cover.Garage. matter.js HEAD names it
		// "Closure" (schema/devicetypes.go, 0x0230).
		return "Closure"
	case 0x0301:
		return "Thermostat"
	case 0x0302:
		return "Temperature Sensor"
	case 0x0305:
		return "Pressure Sensor"
	case 0x0307:
		return "Humidity Sensor"
	case 0x0510:
		// Advertised by MatterMeasurementElectrical. matter.js HEAD
		// names it "ElectricalSensor" (schema/devicetypes.go, 0x0510).
		return "Electrical Sensor"
	default:
		return fmt.Sprintf("0x%04X", id)
	}
}

// MatterMeasurementClassClusterID returns the cluster ID the given
// measurement class projects to. Counterpart to
// [MatterMeasurementClassDeviceType] for the cluster slot.
func MatterMeasurementClassClusterID(class MatterMeasurementClass) uint32 {
	switch class {
	case MatterMeasurementTemperature:
		return 0x0402
	case MatterMeasurementHumidity:
		return 0x0405
	case MatterMeasurementIlluminance:
		return 0x0400
	case MatterMeasurementPressure:
		return 0x0403
	case MatterMeasurementCO2:
		return 0x040D
	case MatterMeasurementPM25:
		return 0x042A
	case MatterMeasurementPM10:
		return 0x042D
	case MatterMeasurementOccupancy:
		return 0x0406
	case MatterMeasurementContact, MatterMeasurementLeak:
		return 0x0045 // BooleanState
	case MatterMeasurementBattery:
		return 0x002F // PowerSource
	case MatterMeasurementPower:
		return 0x0090 // ElectricalPowerMeasurement
	case MatterMeasurementEnergy:
		return 0x0091 // ElectricalEnergyMeasurement
	case MatterMeasurementMomentarySwitch:
		return 0x003B // Switch (GenericSwitch's cluster)
	case MatterMeasurementElectrical:
		// The group mounts three clusters; this names the headline one so
		// eligibility.Classify has a single id to report. The full set is
		// built by measurement.FromMeasurementClass.
		return 0x0090 // ElectricalPowerMeasurement
	default:
		return 0
	}
}
