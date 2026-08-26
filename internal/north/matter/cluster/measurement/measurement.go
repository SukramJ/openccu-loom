// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// Package measurement contains generic, Source-driven cluster server
// implementations for the read-only Matter measurement clusters
// (Temperature, Humidity, Illuminance, Pressure, BooleanState,
// OccupancySensing, AirQuality). They project a typed measurement source
// from the rich-model layer (`internal/model/generic`, `internal/model/calculated`)
// onto Matter wire format without depending on any specific source
// type — a single MatterFloatMeasurementSource backs Temperature /
// Humidity / Illuminance / Pressure indistinguishably.
//
// Materialisation: the bridge calls [Materialize] for an
// [endpoint.Endpoint] whose Measurement field is non-nil and gets
// back a slice of [interfaces.MatterClusterServer] ready to attach
// to the dispatch table.
package measurement

import (
	"context"
	"errors"
	"fmt"
	"math"

	"github.com/SukramJ/openccu-loom/internal/north/matter/cluster"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/interfaces"
)

// Cluster IDs handled by this package per Matter Application Cluster
// Specification 1.5.1. Listed here for cross-reference; cluster
// servers expose them via [interfaces.MatterClusterServer.MatterClusterID].
const (
	ClusterTemperatureMeasurement uint32 = 0x0402
	ClusterHumidityMeasurement    uint32 = 0x0405
	ClusterIlluminanceMeasurement uint32 = 0x0400
	ClusterPressureMeasurement    uint32 = 0x0403
	ClusterBooleanState           uint32 = 0x0045
	ClusterOccupancySensing       uint32 = 0x0406
	ClusterAirQuality             uint32 = 0x005B // mandatory on AirQualitySensor (0x002C)
	ClusterCO2Concentration       uint32 = 0x040D // ADR 0012 §"Tier P2"
	ClusterPM25Concentration      uint32 = 0x042A // ADR 0012 §"Tier P2"
	ClusterPM10Concentration      uint32 = 0x042D // ADR 0012 §"Tier P2"
	ClusterPowerSource            uint32 = 0x002F
	ClusterElectricalPower        uint32 = 0x0090 // ADR 0012 §"Tier P2"
	ClusterElectricalEnergy       uint32 = 0x0091 // ADR 0012 §"Tier P2"
)

// Common attribute IDs. Most measurement clusters carry the same set:
// MeasuredValue (0x0000), MinMeasuredValue (0x0001), MaxMeasuredValue
// (0x0002), Tolerance (0x0003).
const (
	attrMeasuredValue    uint32 = 0x0000
	attrMinMeasuredValue uint32 = 0x0001
	attrMaxMeasuredValue uint32 = 0x0002
	attrTolerance        uint32 = 0x0003

	// BooleanState (0x0045) attribute IDs.
	attrBoolStateValue uint32 = 0x0000

	// OccupancySensing (0x0406) attribute IDs.
	attrOccupancy           uint32 = 0x0000
	attrOccupancySensorType uint32 = 0x0001
	attrOccupancySensorBmp  uint32 = 0x0002
)

// OccupancySensing FeatureMap bit for the passive-infrared sensor type.
// matter.js `occupancy-sensing.element.ts` gives OTHER constraint "0" and
// PIR constraint "1", so PIR is bit 1. Bit 0 would advertise OTHER and
// contradict the two sensor-type attributes, both of which report PIR.
const occupancyFeaturePIR uint32 = 1 << 1

// Cluster revisions. Matched against the model-layer constants in
// `internal/model/custom/.../matter*.go`.
const (
	tempMeasClusterRevision      uint16 = 6 // matter.js HEAD `temperature-measurement.element.ts:14` default=6
	humidityClusterRevision      uint16 = 5 // matter.js HEAD `relative-humidity-measurement.element.ts:14` default=5
	illuminanceClusterRevision   uint16 = 5 // matter.js HEAD `illuminance-measurement.element.ts:19` default=5
	pressureClusterRevision      uint16 = 5 // matter.js HEAD `pressure-measurement.element.ts:18` default=5
	booleanStateClusterRevision  uint16 = 3 // matter.js HEAD `boolean-state.element.ts:19` default=3
	occupancyClusterRevision     uint16 = 7 // matter.js HEAD `occupancy-sensing.element.ts:20` default=7
	concentrationClusterRevision uint16 = 5 // CO2 / PM2.5 / PM10 base cluster — matter.js HEAD `concentration-measurement.element.ts:19` default=5
	airQualityClusterRevision    uint16 = 1 // matter.js HEAD `air-quality.element.ts:19` default=1
	powerSourceClusterRevision   uint16 = 3 // matter.js HEAD (@matter/model 0.16.11)
)

// AirQuality (0x005B) attribute ID per Matter §2.9.6.1.
const attrAirQualityLevel uint32 = 0x0000

// AirQualityEnum members per Matter §2.9.5.1, mirroring matter.js
// `packages/model/src/standard/elements/air-quality.element.ts`. Only
// Unknown, Good and Poor carry conformance "M" — Fair, Moderate,
// VeryPoor and ExtremelyPoor are each gated on an optional FeatureMap
// bit (FAIR / MOD / VPOOR / XPOOR) that [AirQualityServer] does not
// advertise, so they never appear on the wire.
const (
	airQualityUnknown uint8 = 0
	airQualityGood    uint8 = 1
	airQualityPoor    uint8 = 4
)

// Concentration Measurement (CO2 / PM2.5 / PM10) attribute IDs per
// Matter §2.10.5 (the three clusters share the cluster-shape).
const (
	attrConcMeasuredValue     uint32 = 0x0000
	attrConcMinMeasuredValue  uint32 = 0x0001
	attrConcMaxMeasuredValue  uint32 = 0x0002
	attrConcMeasurementUnit   uint32 = 0x0008
	attrConcMeasurementMedium uint32 = 0x0009
)

// Concentration cluster MeasurementUnit enum values per Matter §2.10.7.1.
const (
	concUnitPPM                    uint8 = 0
	concUnitMicroGramPerCubicMeter uint8 = 4
)

// Concentration cluster MeasurementMedium enum values per §2.10.7.2.
const (
	concMediumAir uint8 = 0
)

// Concentration cluster FeatureMap bits per §2.10.4.
//   - MEA (Numeric Measurement) at bit 0 — what we always advertise
//     when projecting a Generic.Sensor[float64] onto a concentration
//     cluster. Other bits (LEV, MED, NUM) stay off.
const concFeatureMEA uint32 = 1 << 0

// PowerSource (0x002F) attribute IDs per Matter §11.7.6.
// EndpointList (0x001F) is mandatory per matter.js
// packages/model/src/standard/elements/power-source.element.ts — the
// list identifies which endpoints this power source serves.
const (
	attrPwrStatus              uint32 = 0x0000
	attrPwrOrder               uint32 = 0x0001
	attrPwrDescription         uint32 = 0x0002
	attrPwrBatPercentRemaining uint32 = 0x000C
	// attrPwrBatChargeLevel and the two IDs below it are conformance
	// "BAT" (unconditionally mandatory once the BAT feature is set);
	// BatPercentRemaining above is conformance "[BAT]" (optional even
	// with BAT set) per matter.js power-source-cluster.element.ts:68-71
	// — only advertised by [PowerSourceServer] instances constructed via
	// [NewPowerSourceServerFromFloat].
	attrPwrBatChargeLevel       uint32 = 0x000E
	attrPwrBatReplacementNeeded uint32 = 0x000F
	attrPwrBatReplaceability    uint32 = 0x0010
	attrPwrEndpointList         uint32 = 0x001F // mandatory — list of endpoints served by this source
)

// PowerSource Status enum (Matter §11.7.6.5.1).
const (
	pwrStatusUnspecified uint8 = 0
	pwrStatusActive      uint8 = 1
)

// PowerSource BatChargeLevel enum (§11.7.6.5.4).
const (
	batChargeOK       uint8 = 0
	batChargeWarning  uint8 = 1
	batChargeCritical uint8 = 2
)

// PowerSource BatReplaceability enum (§11.7.6.5.6) — HM batteries are
// always user-replaceable.
const batReplaceUserReplaceable uint8 = 2

// PowerSource FeatureMap bit for a battery-backed source. matter.js
// `power-source-cluster.element.ts` gives WIRED constraint "0" and BAT
// constraint "1"; RECHG ("2") and REPLC ("3") stay clear because each
// makes attributes mandatory that a LOWBAT-driven projection cannot
// fill — REPLC requires BatReplacementDescription (0x13) and BatQuantity
// (0x19), neither of which the CCU reports. BatReplaceability (0x10) is
// served under the BAT feature: matter.js records its conformance as
// "BAT", not "REPLC".
const pwrFeatureBAT uint32 = 1 << 1

// ElectricalPowerMeasurement (0x0090) attribute IDs per Matter §2.13.6.
const (
	attrElPwrPowerMode                uint32 = 0x0000
	attrElPwrNumberOfMeasurementTypes uint32 = 0x0001 // count of AccuracyStruct entries in Accuracy list (NOT "number of phases") — matter.js electrical-power-measurement.element.ts:25
	attrElPwrAccuracy                 uint32 = 0x0002
	attrElPwrActivePower              uint32 = 0x0008 // int64 mW
	attrElPwrVoltage                  uint32 = 0x0004 // int64 mV — spec ElectricalPowerMeasurement §2.13.6.4
	attrElPwrActiveCurrnt             uint32 = 0x0005 // int64 mA — spec §2.13.6.5
	attrElPwrFrequency                uint32 = 0x000E // int64 mHz — spec §2.13.6.14 (0x000A collides with ApparentPower)
)

// ElectricalEnergyMeasurement (0x0091) attribute IDs per Matter §2.14.6.
// CumulativeEnergyExported (0x0002) is deliberately absent: matter.js
// gates it on "EXPE & CUME" and HM metering hardware has no exported-energy
// path, so serving it would advertise an attribute whose feature is clear.
const (
	attrElEnAccuracy           uint32 = 0x0000
	attrElEnCumulativeImported uint32 = 0x0001 // int64 mWh, struct EnergyMeasurementStruct.energy
)

// ElectricalPower / ElectricalEnergy ClusterRevisions per matter.js HEAD
// (@matter/model 0.16.11). ElectricalPowerMeasurement was bumped 1→3
// in Matter 1.4 with the addition of harmonics + per-phase reporting;
// ElectricalEnergyMeasurement was bumped 1→2 in Matter 1.5.
const (
	electricalPowerClusterRevision  uint16 = 3
	electricalEnergyClusterRevision uint16 = 2
)

// ElectricalPower FeatureMap bits per Matter §2.13.4 — only the
// ALTC (Alternating-Current) feature is advertised for HmIP-PSM and
// similar mains-AC switch-meters; DCV/DCM are off because HM has no
// DC measurement hardware.
const elPwrFeatureAltC uint32 = 1 << 1

// ElectricalEnergy FeatureMap bits. matter.js
// `electrical-energy-measurement.element.ts` puts IMPE at constraint "0"
// and CUME at constraint "2". Both are advertised: the imported/exported
// pair and the cumulative/periodic pair form two separate
// at-least-one-required choice groups, and CumulativeEnergyImported —
// the only value-bearing attribute the bridge serves — has conformance
// "IMPE & CUME". EXPE stays clear because HM metering hardware has no
// exported-energy path, PERE because no periodic accumulator exists.
const (
	elEnFeatureIMPE uint32 = 1 << 0
	elEnFeatureCUME uint32 = 1 << 2
)

// AccuracyRangeStruct is one range entry inside an [AccuracyStruct].
// Fields follow matter.js
// packages/model/src/standard/elements/measurement-accuracy-range-struct.element.ts:
// RangeMin(0) int64, RangeMax(1) int64, FixedMax(5) uint64. PercentMax(2)
// and FixedMax form an at-least-one-required choice group, so one of the
// two has to be present for the struct to be conformant; we carry
// FixedMax because HM meters quantise, they do not specify a relative
// error.
type AccuracyRangeStruct struct {
	RangeMin int64
	RangeMax int64
	FixedMax uint64
}

// AccuracyStruct encodes one MeasurementAccuracyStruct entry — the
// payload of ElectricalPowerMeasurement.Accuracy (0x0090:0x0002) and
// ElectricalEnergyMeasurement.Accuracy (0x0091:0x0000). Field tag numbers
// and types follow matter.js
// packages/model/src/standard/elements/measurement-accuracy-struct.element.ts:
// MeasurementType(0) enum16, Measured(1) bool, MinMeasuredValue(2) int64,
// MaxMeasuredValue(3) int64, AccuracyRanges(4) list[AccuracyRangeStruct].
//
// Tags 2 and 3 are the measurement *range*, not an accuracy percentage —
// encoding them as unsigned made a typed decoder (chip's
// TLVReader::Get(int64_t&) rejects unsigned elements) fail on a
// conformance-M attribute of both clusters.
//
// The spec requires at least one AccuracyStruct in the Accuracy list and
// at least one range inside it. HM hardware publishes no manufacturer
// uncertainty specification, so the entry mirrors the placeholder shape
// matter.js itself demonstrates in its measuring-socket template: the
// full permitted measurement range with a one-unit fixed accuracy.
type AccuracyStruct struct {
	MeasurementType  uint16 // enum16: 0x0008=ActivePower, 0x0009=ActiveEnergyImported
	Measured         bool
	MinMeasuredValue int64
	MaxMeasuredValue int64
	AccuracyRanges   []AccuracyRangeStruct
}

// Measurement-range bounds for the stub accuracy entry. matter.js
// constrains MinMeasuredValue / MaxMeasuredValue / RangeMin / RangeMax to
// "-2^62 to 2^62"; math.MinInt64 / math.MaxInt64 sit outside that and are
// rejected by a constraint-checking controller.
const (
	accuracyRangeMin int64 = -(1 << 62)
	accuracyRangeMax int64 = 1 << 62
	// accuracyFixedMax claims a one-unit (mW / mWh) absolute error. It is
	// the smallest non-vacuous claim that satisfies the PercentMax/FixedMax
	// choice group; matter.js uses the same placeholder.
	accuracyFixedMax uint64 = 1
)

// EnergyMeasurementStruct is the wire payload of the
// ElectricalEnergyMeasurement Cumulative/PeriodicEnergy* attributes per
// Matter §2.14.5.2. Only the mandatory Energy field (tag 0, int64 mWh,
// "0 to 2^62") is emitted: the StartTimestamp/EndTimestamp/StartSystime/
// EndSystime fields (tags 1-4) describe the recording period of PERIODIC
// measurements and are omitted for cumulative readings per their field
// descriptions. matter.js ref: packages/model/src/standard/elements/
// electrical-energy-measurement.element.ts:88-96 (EnergyMeasurementStruct).
type EnergyMeasurementStruct struct {
	Energy int64
}

// errReadOnly surfaces from MatterWrite — every server in this
// package is read-only at the cluster level (writes flow through the
// underlying DP via a different path).
var errReadOnly = errors.New("measurement: cluster is read-only at the wire layer")

// errNoCommands surfaces from MatterInvoke — measurement clusters
// have no Matter commands.
var errNoCommands = errors.New("measurement: cluster has no commands")

// --- TemperatureMeasurement (0x0402) -----------------------------------

// TemperatureServer projects a [interfaces.MatterFloatMeasurementSource]
// onto Matter TemperatureMeasurement. The model unit is °C; the wire
// unit is int16 in 0.01 °C per Matter §2.3.5.1. Saturates at int16
// boundaries; absent observations surface as `(nil, true)` paired
// with the spec NULL sentinel (0x8000).
//
// TemperatureServer embeds [cluster.DataVersionTracker] and implements
// [interfaces.MatterClusterDataVersion] so the IM dispatcher stamps a
// per-cluster monotonic DataVersion on every AttributeDataIB. Apple
// Home's MTRDevice cache persists cluster state only when the
// DataVersion is non-uniform across clusters — a constant 1 (the
// no-tracker fallback) causes "Storing cluster information count: 3"
// log lines and forces a full re-read on every reconnect. See
// [cluster.DataVersionTracker] for the full rationale.
type TemperatureServer struct {
	cluster.DataVersionTracker
	src interfaces.MatterFloatMeasurementSource
}

// NewTemperatureServer wraps src.
func NewTemperatureServer(src interfaces.MatterFloatMeasurementSource) *TemperatureServer {
	return &TemperatureServer{src: src}
}

// MatterDataVersion implements [interfaces.MatterClusterDataVersion].
// Returns the current per-cluster monotonic counter so the IM
// dispatcher stamps non-uniform DataVersions on AttributeDataIBs.
// Mirrors matter.js InteractionServer.ts DataVersion per-cluster init
// (packages/protocol/src/interaction/InteractionServer.ts).
func (s *TemperatureServer) MatterDataVersion() uint32 {
	return s.Current()
}

// MatterClusterID returns the Matter Temperature Measurement cluster ID (0x0402).
func (s *TemperatureServer) MatterClusterID() uint32 { return ClusterTemperatureMeasurement }

// MatterRead resolves an attribute by ID against the underlying source.
func (s *TemperatureServer) MatterRead(attrID uint32) (any, bool) {
	switch attrID {
	case attrMeasuredValue:
		// Value temporarily unavailable (e.g. CCU circuit-breaker open): return
		// (nil, true) so the dispatcher encodes TLV null + Success. Apple Home
		// tolerates null as "transiently unknown" and continues building the HAP
		// service. (nil, false) would signal UnsupportedAttribute and abort the
		// HAP build with HAPErrorDomain Code=24. See climate/matter.go for the
		// full rationale.
		v, ok := s.src.MatterFloatValue()
		if !ok {
			return nil, true
		}
		return celsiusToInt16(v), true
	case attrMinMeasuredValue:
		return int16(-27315), true // -273.15 °C — physical absolute zero
	case attrMaxMeasuredValue:
		// chip's `TemperatureMeasurementCluster.cpp:27-28` defines
		// `kMaxMeasuredValueRange = 32766` — 32767 is reserved as the
		// `NULL` marker per Matter §2.6.4.4 nullable int16. Keep saturated
		// at the spec ceiling, not the int16 max.
		return int16(32766), true
	case attrTolerance:
		return uint16(0), true
	case cluster.AttrGlobalFeatureMap:
		return uint32(0), true
	case cluster.AttrGlobalClusterRevision:
		return tempMeasClusterRevision, true
	}
	return nil, false
}

// MatterWrite returns errReadOnly — the Matter Temperature Measurement cluster is read-only at the wire layer.
func (s *TemperatureServer) MatterWrite(_ context.Context, _ uint32, _ any, _ hmenum.CommandPriority) error {
	return errReadOnly
}

// MatterInvoke returns errNoCommands — the Matter Temperature Measurement cluster has no commands.
func (s *TemperatureServer) MatterInvoke(_ context.Context, cmdID uint32, _ any, _ hmenum.CommandPriority) (any, error) {
	return nil, fmt.Errorf("%w (cmd 0x%02X)", errNoCommands, cmdID)
}

// MatterReportable returns the attribute IDs that the Matter Temperature Measurement cluster reports on change.
func (s *TemperatureServer) MatterReportable() []uint32 { return []uint32{attrMeasuredValue} }

// MatterAttributes lists every TemperatureMeasurement (0x0402)
// attribute the server implements via MatterRead. Apple Home's HAP
// service rebuild reads the full attribute set; without this the
// dispatcher falls back to MatterReportable's single attribute.
func (s *TemperatureServer) MatterAttributes() []uint32 {
	return []uint32{attrMeasuredValue, attrMinMeasuredValue, attrMaxMeasuredValue, attrTolerance}
}

// celsiusToInt16 converts a Celsius temperature to the Matter wire
// encoding (int16 × 0.01 °C). Clamps to [−27315, 32766] rather than
// the raw int16 limits: 32767 is the TLV-null sentinel
// (Matter §2.3.5.1 / chip `kMaxMeasuredValueRange = 32766`) and
// −32768 falls below the physical absolute-zero floor
// (chip `kMinMeasuredValueRange = -27315 = -273.15 °C`).
// Mirrors matter.js `packages/model/src/standard/elements/
// temperature-measurement.element.ts` + chip
// `src/app/clusters/temperature-measurement-server/
// TemperatureMeasurementCluster.cpp:27-28`.
func celsiusToInt16(c float64) int16 {
	v := math.Round(c * 100)
	if v > 32766 { // 32767 is the Matter NULL sentinel — must not be emitted as a real value
		return 32766
	}
	if v < -27315 { // −273.15 °C absolute-zero floor per chip kMinMeasuredValueRange
		return -27315
	}
	return int16(v)
}

// --- RelativeHumidityMeasurement (0x0405) ------------------------------

// HumidityServer projects a [interfaces.MatterFloatMeasurementSource]
// onto Matter RelativeHumidityMeasurement. Model unit: percent (0-100);
// wire unit: uint16 in 0.01 % per Matter §2.6.5.1. Clamped to
// [0, 10000] to keep the value valid even when the source DP reports
// a slightly out-of-range humidity.
//
// HumidityServer embeds [cluster.DataVersionTracker] and implements
// [interfaces.MatterClusterDataVersion]. See TemperatureServer for the
// DataVersion tracking follows the same pattern as TemperatureServer.
type HumidityServer struct {
	cluster.DataVersionTracker
	src interfaces.MatterFloatMeasurementSource
}

// Compile-time assertion: HumidityServer satisfies MatterClusterDataVersion.
var _ interfaces.MatterClusterDataVersion = (*HumidityServer)(nil)

// NewHumidityServer constructs a HumidityServer backed by src.
func NewHumidityServer(src interfaces.MatterFloatMeasurementSource) *HumidityServer {
	return &HumidityServer{src: src}
}

// MatterDataVersion implements [interfaces.MatterClusterDataVersion].
func (s *HumidityServer) MatterDataVersion() uint32 { return s.Current() }

// MatterClusterID returns the Matter Relative Humidity Measurement cluster ID (0x0405).
func (s *HumidityServer) MatterClusterID() uint32 { return ClusterHumidityMeasurement }

// MatterRead resolves an attribute by ID against the underlying source.
func (s *HumidityServer) MatterRead(attrID uint32) (any, bool) {
	switch attrID {
	case attrMeasuredValue:
		// Value temporarily unavailable — return (nil, true); see TemperatureServer.MatterRead.
		v, ok := s.src.MatterFloatValue()
		if !ok {
			return nil, true
		}
		return humidityToUint16(v), true
	case attrMinMeasuredValue:
		return uint16(0), true
	case attrMaxMeasuredValue:
		return uint16(10000), true
	case attrTolerance:
		return uint16(0), true
	case cluster.AttrGlobalFeatureMap:
		return uint32(0), true
	case cluster.AttrGlobalClusterRevision:
		return humidityClusterRevision, true
	}
	return nil, false
}

// MatterWrite returns errReadOnly — the Matter Relative Humidity Measurement cluster is read-only at the wire layer.
func (s *HumidityServer) MatterWrite(_ context.Context, _ uint32, _ any, _ hmenum.CommandPriority) error {
	return errReadOnly
}

// MatterInvoke returns errNoCommands — the Matter Relative Humidity Measurement cluster has no commands.
func (s *HumidityServer) MatterInvoke(_ context.Context, cmdID uint32, _ any, _ hmenum.CommandPriority) (any, error) {
	return nil, fmt.Errorf("%w (cmd 0x%02X)", errNoCommands, cmdID)
}

// MatterReportable returns the attribute IDs that the Matter Relative Humidity Measurement cluster reports on change.
func (s *HumidityServer) MatterReportable() []uint32 { return []uint32{attrMeasuredValue} }

// MatterAttributes lists every RelativeHumidityMeasurement (0x0405)
// attribute the server implements via MatterRead. Apple Home's HAP
// service rebuild reads the full attribute set; without this the
// dispatcher falls back to MatterReportable's single attribute.
func (s *HumidityServer) MatterAttributes() []uint32 {
	return []uint32{attrMeasuredValue, attrMinMeasuredValue, attrMaxMeasuredValue, attrTolerance}
}

func humidityToUint16(p float64) uint16 {
	v := math.Round(p * 100)
	if v < 0 {
		return 0
	}
	if v > 10000 {
		return 10000
	}
	return uint16(v)
}

// --- IlluminanceMeasurement (0x0400) -----------------------------------

// IlluminanceServer projects a [interfaces.MatterFloatMeasurementSource]
// onto Matter IlluminanceMeasurement. Model unit: lux. Wire unit:
// uint16 = round(10000 * log10(lux) + 1), bounded to [1, 0xFFFE]
// per Matter §2.2.5.1. Sub-lux readings clamp to 1 (the spec's
// minimum representable non-null value); zero/negative input maps to
// 1 too. 0 is reserved as "below detection threshold"; 0xFFFF as null.
//
// IlluminanceServer embeds [cluster.DataVersionTracker] and implements
// [interfaces.MatterClusterDataVersion]. See TemperatureServer for the
// DataVersion tracking follows the same pattern as TemperatureServer.
type IlluminanceServer struct {
	cluster.DataVersionTracker
	src interfaces.MatterFloatMeasurementSource
}

// Compile-time assertion: IlluminanceServer satisfies MatterClusterDataVersion.
var _ interfaces.MatterClusterDataVersion = (*IlluminanceServer)(nil)

// NewIlluminanceServer constructs an IlluminanceServer backed by src.
func NewIlluminanceServer(src interfaces.MatterFloatMeasurementSource) *IlluminanceServer {
	return &IlluminanceServer{src: src}
}

// MatterDataVersion implements [interfaces.MatterClusterDataVersion].
func (s *IlluminanceServer) MatterDataVersion() uint32 { return s.Current() }

// MatterClusterID returns the Matter Illuminance Measurement cluster ID (0x0400).
func (s *IlluminanceServer) MatterClusterID() uint32 { return ClusterIlluminanceMeasurement }

// MatterRead resolves an attribute by ID against the underlying source.
func (s *IlluminanceServer) MatterRead(attrID uint32) (any, bool) {
	switch attrID {
	case attrMeasuredValue:
		// Value temporarily unavailable — return (nil, true); see TemperatureServer.MatterRead.
		v, ok := s.src.MatterFloatValue()
		if !ok {
			return nil, true
		}
		return luxToMatter(v), true
	case attrMinMeasuredValue:
		return uint16(1), true
	case attrMaxMeasuredValue:
		return uint16(0xFFFE), true
	case attrTolerance:
		return uint16(0), true
	case cluster.AttrGlobalFeatureMap:
		return uint32(0), true
	case cluster.AttrGlobalClusterRevision:
		return illuminanceClusterRevision, true
	}
	return nil, false
}

// MatterWrite returns errReadOnly — the Matter Illuminance Measurement cluster is read-only at the wire layer.
func (s *IlluminanceServer) MatterWrite(_ context.Context, _ uint32, _ any, _ hmenum.CommandPriority) error {
	return errReadOnly
}

// MatterInvoke returns errNoCommands — the Matter Illuminance Measurement cluster has no commands.
func (s *IlluminanceServer) MatterInvoke(_ context.Context, cmdID uint32, _ any, _ hmenum.CommandPriority) (any, error) {
	return nil, fmt.Errorf("%w (cmd 0x%02X)", errNoCommands, cmdID)
}

// MatterReportable returns the attribute IDs that the Matter Illuminance Measurement cluster reports on change.
func (s *IlluminanceServer) MatterReportable() []uint32 { return []uint32{attrMeasuredValue} }

// MatterAttributes lists every IlluminanceMeasurement (0x0400)
// attribute the server implements via MatterRead. Apple Home's HAP
// service rebuild reads the full attribute set; without this the
// dispatcher falls back to MatterReportable's single attribute.
func (s *IlluminanceServer) MatterAttributes() []uint32 {
	return []uint32{attrMeasuredValue, attrMinMeasuredValue, attrMaxMeasuredValue, attrTolerance}
}

func luxToMatter(lux float64) uint16 {
	if lux <= 1 {
		return 1
	}
	v := math.Round(10000*math.Log10(lux) + 1)
	if v > 0xFFFE {
		return 0xFFFE
	}
	if v < 1 {
		return 1
	}
	return uint16(v)
}

// --- PressureMeasurement (0x0403) --------------------------------------

// PressureServer projects a [interfaces.MatterFloatMeasurementSource]
// onto Matter PressureMeasurement. Model unit: hPa (= mbar = 100 Pa).
// Wire unit: int16 deci-kPa — Matter §2.4.5.1 defines
// `MeasuredValue = 10 x Pressure [kPa]`, so one wire unit is 0.1 kPa =
// 100 Pa = exactly 1 hPa and the model value passes through rounded.
// Mirrors matter.js
// `packages/model/src/standard/resources/pressure-measurement.resource.ts:27`.
// Saturates at int16 boundaries.
//
// Typical atmospheric pressure (950-1050 hPa) maps to 950-1050 wire
// units, well within int16 range.
//
// PressureServer embeds [cluster.DataVersionTracker] and implements
// [interfaces.MatterClusterDataVersion]. See TemperatureServer for the
// DataVersion tracking follows the same pattern as TemperatureServer.
type PressureServer struct {
	cluster.DataVersionTracker
	src interfaces.MatterFloatMeasurementSource
}

// Compile-time assertion: PressureServer satisfies MatterClusterDataVersion.
var _ interfaces.MatterClusterDataVersion = (*PressureServer)(nil)

// NewPressureServer constructs a PressureServer backed by src.
func NewPressureServer(src interfaces.MatterFloatMeasurementSource) *PressureServer {
	return &PressureServer{src: src}
}

// MatterDataVersion implements [interfaces.MatterClusterDataVersion].
func (s *PressureServer) MatterDataVersion() uint32 { return s.Current() }

// MatterClusterID returns the Matter Pressure Measurement cluster ID (0x0403).
func (s *PressureServer) MatterClusterID() uint32 { return ClusterPressureMeasurement }

// MatterRead resolves an attribute by ID against the underlying source.
func (s *PressureServer) MatterRead(attrID uint32) (any, bool) {
	switch attrID {
	case attrMeasuredValue:
		// Value temporarily unavailable — return (nil, true); see TemperatureServer.MatterRead.
		v, ok := s.src.MatterFloatValue()
		if !ok {
			return nil, true
		}
		return hPaToMatter(v), true
	case attrMinMeasuredValue:
		// 0 maps to 0 hPa (vacuum lower bound for atmospheric use).
		// int16(-32768) is below the physical domain and misleads strict
		// controllers; 0 is a safe, spec-compliant lower bound.
		// matter.js `packages/model/src/standard/elements/
		// pressure-measurement.element.ts:29` — MinMeasuredValue
		// constraint "max 32766"; chip
		// `src/app/clusters/pressure-measurement-server/
		// PressureMeasurementCluster.cpp:26` kMinMeasuredValueMax=32766.
		return int16(0), true
	case attrMaxMeasuredValue:
		// 32766 is the highest non-null representable value;
		// 32767 is the NULL sentinel per Matter §2.4.5.4.
		return int16(32766), true
	case attrTolerance:
		return uint16(0), true
	case cluster.AttrGlobalFeatureMap:
		return uint32(0), true
	case cluster.AttrGlobalClusterRevision:
		return pressureClusterRevision, true
	}
	return nil, false
}

// MatterWrite returns errReadOnly — the Matter Pressure Measurement cluster is read-only at the wire layer.
func (s *PressureServer) MatterWrite(_ context.Context, _ uint32, _ any, _ hmenum.CommandPriority) error {
	return errReadOnly
}

// MatterInvoke returns errNoCommands — the Matter Pressure Measurement cluster has no commands.
func (s *PressureServer) MatterInvoke(_ context.Context, cmdID uint32, _ any, _ hmenum.CommandPriority) (any, error) {
	return nil, fmt.Errorf("%w (cmd 0x%02X)", errNoCommands, cmdID)
}

// MatterReportable returns the attribute IDs that the Matter Pressure Measurement cluster reports on change.
func (s *PressureServer) MatterReportable() []uint32 { return []uint32{attrMeasuredValue} }

// MatterAttributes lists every PressureMeasurement (0x0403)
// attribute the server implements via MatterRead. Apple Home's HAP
// service rebuild reads the full attribute set; without this the
// dispatcher falls back to MatterReportable's single attribute.
func (s *PressureServer) MatterAttributes() []uint32 {
	return []uint32{attrMeasuredValue, attrMinMeasuredValue, attrMaxMeasuredValue, attrTolerance}
}

func hPaToMatter(hpa float64) int16 {
	v := math.Round(hpa) // 1 hPa = 0.1 kPa = 1 deci-kPa wire unit
	if v > math.MaxInt16 {
		return math.MaxInt16
	}
	if v < math.MinInt16 {
		return math.MinInt16
	}
	return int16(v)
}

// --- BooleanState (0x0045) ---------------------------------------------

// BooleanStateServer projects a [interfaces.MatterBoolMeasurementSource]
// onto Matter BooleanState. Used for ContactSensor and generic alarm
// endpoints (leak-class sensors also materialise as ContactSensor —
// see `pkg/interfaces/matter.go::MatterMeasurementClassDeviceType`).
// The polarity is set by the model-layer classifier (see
// `internal/model/generic/matter.go::matterMeasurementForBinaryParameter`).
//
// BooleanStateServer embeds [cluster.DataVersionTracker] and implements
// [interfaces.MatterClusterDataVersion]. See TemperatureServer for the
// DataVersion tracking follows the same pattern as TemperatureServer.
type BooleanStateServer struct {
	cluster.DataVersionTracker
	src interfaces.MatterBoolMeasurementSource
}

// Compile-time assertion: BooleanStateServer satisfies MatterClusterDataVersion.
var _ interfaces.MatterClusterDataVersion = (*BooleanStateServer)(nil)

// NewBooleanStateServer constructs a BooleanStateServer backed by src.
func NewBooleanStateServer(src interfaces.MatterBoolMeasurementSource) *BooleanStateServer {
	return &BooleanStateServer{src: src}
}

// MatterDataVersion implements [interfaces.MatterClusterDataVersion].
func (s *BooleanStateServer) MatterDataVersion() uint32 { return s.Current() }

// MatterClusterID returns the Matter Boolean State cluster ID (0x0045).
func (s *BooleanStateServer) MatterClusterID() uint32 { return ClusterBooleanState }

// MatterRead resolves an attribute by ID against the underlying source.
func (s *BooleanStateServer) MatterRead(attrID uint32) (any, bool) {
	switch attrID {
	case attrBoolStateValue:
		// BooleanState.StateValue is type bool with conformance M and NO
		// quality X (not nullable) — matter.js `packages/model/src/standard/
		// elements/boolean-state.element.ts:29` + chip
		// `zzz_generated/app-common/clusters/BooleanState/Attributes.h`.
		// Encoding TLV-null for a non-nullable bool causes strict-validator
		// rejection (chip CHIP Error 0x26). Default to false (not active)
		// before the first CCU push, matching the safe-state convention used
		// by OnOffServer for non-nullable bool attributes.
		v, ok := s.src.MatterBoolValue()
		if !ok {
			return false, true
		}
		return v, true
	case cluster.AttrGlobalFeatureMap:
		return uint32(0), true
	case cluster.AttrGlobalClusterRevision:
		return booleanStateClusterRevision, true
	}
	return nil, false
}

// MatterWrite returns errReadOnly — the Matter Boolean State cluster is read-only at the wire layer.
func (s *BooleanStateServer) MatterWrite(_ context.Context, _ uint32, _ any, _ hmenum.CommandPriority) error {
	return errReadOnly
}

// MatterInvoke returns errNoCommands — the Matter Boolean State cluster has no commands.
func (s *BooleanStateServer) MatterInvoke(_ context.Context, cmdID uint32, _ any, _ hmenum.CommandPriority) (any, error) {
	return nil, fmt.Errorf("%w (cmd 0x%02X)", errNoCommands, cmdID)
}

// MatterReportable returns the attribute IDs that the Matter Boolean State cluster reports on change.
func (s *BooleanStateServer) MatterReportable() []uint32 { return []uint32{attrBoolStateValue} }

// MatterAttributes lists every BooleanState (0x0045) attribute the
// server implements via MatterRead. Apple Home's HAP service rebuild
// reads the full attribute set; without this the dispatcher falls back
// to MatterReportable's single attribute.
func (s *BooleanStateServer) MatterAttributes() []uint32 {
	return []uint32{attrBoolStateValue}
}

// --- OccupancySensing (0x0406) -----------------------------------------

// OccupancySensingServer projects a [interfaces.MatterBoolMeasurementSource]
// onto Matter OccupancySensing. The Occupancy attribute is a bitmap8
// where bit 0 = occupied; OccupancySensorType is fixed to 0 (PIR)
// because every HM motion sensor uses PIR.
//
// OccupancySensingServer embeds [cluster.DataVersionTracker] and
// implements [interfaces.MatterClusterDataVersion]. See TemperatureServer
// DataVersion tracking follows the same pattern as TemperatureServer.
type OccupancySensingServer struct {
	cluster.DataVersionTracker
	src interfaces.MatterBoolMeasurementSource
}

// Compile-time assertion: OccupancySensingServer satisfies MatterClusterDataVersion.
var _ interfaces.MatterClusterDataVersion = (*OccupancySensingServer)(nil)

// NewOccupancySensingServer constructs an OccupancySensingServer backed by src.
func NewOccupancySensingServer(src interfaces.MatterBoolMeasurementSource) *OccupancySensingServer {
	return &OccupancySensingServer{src: src}
}

// MatterDataVersion implements [interfaces.MatterClusterDataVersion].
func (s *OccupancySensingServer) MatterDataVersion() uint32 { return s.Current() }

// MatterClusterID returns the Matter Occupancy Sensing cluster ID (0x0406).
func (s *OccupancySensingServer) MatterClusterID() uint32 { return ClusterOccupancySensing }

// MatterRead resolves an attribute by ID against the underlying source.
func (s *OccupancySensingServer) MatterRead(attrID uint32) (any, bool) {
	switch attrID {
	case attrOccupancy:
		// Value temporarily unavailable — return (nil, true); see TemperatureServer.MatterRead.
		v, ok := s.src.MatterBoolValue()
		if !ok {
			return nil, true
		}
		if v {
			return uint8(1), true
		}
		return uint8(0), true
	case attrOccupancySensorType:
		return uint8(0), true // 0 = PIR
	case attrOccupancySensorBmp:
		return uint8(1 << 0), true // bit 0 = PIR
	case cluster.AttrGlobalFeatureMap:
		// All HM motion detectors use PIR (passive infrared). From
		// cluster revision 5 on, matter.js requires one sensor-type
		// feature to be selected, and controllers that branch on it
		// (matter.js OccupancySensingServer.ts `features.passiveInfrared`)
		// classify the sensor from this bit alone.
		return occupancyFeaturePIR, true
	case cluster.AttrGlobalClusterRevision:
		return occupancyClusterRevision, true
	}
	return nil, false
}

// MatterWrite returns errReadOnly — the Matter Occupancy Sensing cluster is read-only at the wire layer.
func (s *OccupancySensingServer) MatterWrite(_ context.Context, _ uint32, _ any, _ hmenum.CommandPriority) error {
	return errReadOnly
}

// MatterInvoke returns errNoCommands — the Matter Occupancy Sensing cluster has no commands.
func (s *OccupancySensingServer) MatterInvoke(_ context.Context, cmdID uint32, _ any, _ hmenum.CommandPriority) (any, error) {
	return nil, fmt.Errorf("%w (cmd 0x%02X)", errNoCommands, cmdID)
}

// MatterReportable returns the attribute IDs that the Matter Occupancy Sensing cluster reports on change.
func (s *OccupancySensingServer) MatterReportable() []uint32 { return []uint32{attrOccupancy} }

// MatterAttributes lists every OccupancySensing (0x0406) attribute the
// server implements via MatterRead. Apple Home's HAP service rebuild
// reads the full attribute set; without this the dispatcher falls back
// to MatterReportable's single attribute.
func (s *OccupancySensingServer) MatterAttributes() []uint32 {
	// PirOccupiedToUnoccupiedDelay (0x10) is intentionally NOT advertised:
	// per matter.js occupancy-sensing.element.ts it is deprecated (D) and
	// conformance-gated on the optional HoldTime (0x3) attribute, which the
	// bridge does not serve. The rev-6 surface for a simple PIR sensor is
	// Occupancy + OccupancySensorType + OccupancySensorTypeBitmap.
	return []uint32{attrOccupancy, attrOccupancySensorType, attrOccupancySensorBmp}
}

// --- Materializer ------------------------------------------------------

// FromMeasurementClass returns the cluster server(s) that match the
// given measurement class, wrapping src as the value source. Returns
// nil when src is not the right typed flavour for class (e.g. a
// MatterFloatMeasurementSource for an Occupancy class) or when class
// is one that has no measurement-cluster materialisation (None,
// Power, Energy, MomentarySwitch).
//
// The air-quality classes (CO2 / PM2.5 / PM10) return two servers: the
// concentration cluster plus [AirQualityServer], which the
// AirQualitySensor device type mandates while listing every
// concentration cluster as optional.
//
// Power / Energy host-cluster materialisation: when a Custom DP
// (typically a switch.Switch on a HmIP-PSM) advertises a Power /
// Energy MatterMeasurementClass alongside its OnOff cluster, the
// materializer DOES return a cluster server for it. The Custom DP
// attaches the resulting server to its host endpoint; the bridge
// does NOT spin up a standalone sensor endpoint for these classes
// (Matter Device Library §11.4 expects ElectricalPowerMeasurement
// to ride on the same endpoint as the OnOff/PlugInUnit).
//
// MomentarySwitch (Switch 0x003B) is event-driven, not
// attribute-driven — its projection lives in
// `cluster/wire/genericswitch.go::GenericSwitch` and wires to the
// bridge's MatterEventEmitter (Subscribe ongoing-pump for events,
// Matter §10.6.6). The materializer here returns nil for that class
// so the endpoint assembler delegates to the GenericSwitch path
// instead of building a measurement cluster.
//
// PowerSource (Battery) IS materialised here when the source carries
// a Bool LOWBAT signal — the resulting cluster server must be
// attached to the host endpoint by the caller (typical: a
// custom-DP's MatterClusterServers() rolls it up).
func FromMeasurementClass(class interfaces.MatterMeasurementClass, src any) []interfaces.MatterClusterServer {
	switch class {
	case interfaces.MatterMeasurementTemperature:
		if f, ok := src.(interfaces.MatterFloatMeasurementSource); ok {
			return []interfaces.MatterClusterServer{NewTemperatureServer(f)}
		}
	case interfaces.MatterMeasurementHumidity:
		if f, ok := src.(interfaces.MatterFloatMeasurementSource); ok {
			return []interfaces.MatterClusterServer{NewHumidityServer(f)}
		}
	case interfaces.MatterMeasurementIlluminance:
		if f, ok := src.(interfaces.MatterFloatMeasurementSource); ok {
			return []interfaces.MatterClusterServer{NewIlluminanceServer(f)}
		}
	case interfaces.MatterMeasurementPressure:
		if f, ok := src.(interfaces.MatterFloatMeasurementSource); ok {
			return []interfaces.MatterClusterServer{NewPressureServer(f)}
		}
	case interfaces.MatterMeasurementCO2:
		if f, ok := src.(interfaces.MatterFloatMeasurementSource); ok {
			return []interfaces.MatterClusterServer{NewAirQualityServer(class, f), NewCO2ConcentrationServer(f)}
		}
	case interfaces.MatterMeasurementPM25:
		if f, ok := src.(interfaces.MatterFloatMeasurementSource); ok {
			return []interfaces.MatterClusterServer{NewAirQualityServer(class, f), NewPM25ConcentrationServer(f)}
		}
	case interfaces.MatterMeasurementPM10:
		if f, ok := src.(interfaces.MatterFloatMeasurementSource); ok {
			return []interfaces.MatterClusterServer{NewAirQualityServer(class, f), NewPM10ConcentrationServer(f)}
		}
	case interfaces.MatterMeasurementContact, interfaces.MatterMeasurementLeak:
		if b, ok := src.(interfaces.MatterBoolMeasurementSource); ok {
			return []interfaces.MatterClusterServer{NewBooleanStateServer(b)}
		}
	case interfaces.MatterMeasurementOccupancy:
		if b, ok := src.(interfaces.MatterBoolMeasurementSource); ok {
			return []interfaces.MatterClusterServer{NewOccupancySensingServer(b)}
		}
	case interfaces.MatterMeasurementBattery:
		// Two source shapes project onto PowerSource: a LOWBAT bool
		// (BatChargeLevel) or a derived battery-percentage float (e.g.
		// OperatingVoltageLevelSensor — BatPercentRemaining). Checked in
		// this order because both interfaces are structurally possible
		// on a source that also implements other measurement surfaces;
		// a bool source is the more specific / more common HM signal.
		if b, ok := src.(interfaces.MatterBoolMeasurementSource); ok {
			return []interfaces.MatterClusterServer{NewPowerSourceServer(b)}
		}
		if f, ok := src.(interfaces.MatterFloatMeasurementSource); ok {
			return []interfaces.MatterClusterServer{NewPowerSourceServerFromFloat(f)}
		}
	case interfaces.MatterMeasurementPower:
		if f, ok := src.(interfaces.MatterFloatMeasurementSource); ok {
			return []interfaces.MatterClusterServer{NewElectricalPowerServer(f)}
		}
	case interfaces.MatterMeasurementEnergy:
		if f, ok := src.(interfaces.MatterFloatMeasurementSource); ok {
			return []interfaces.MatterClusterServer{NewElectricalEnergyServer(f)}
		}
	case interfaces.MatterMeasurementNone, interfaces.MatterMeasurementMomentarySwitch:
		// None has no cluster projection by design; MomentarySwitch
		// projects via the GenericSwitch event path in
		// cluster/wire/genericswitch.go, not via a measurement cluster.
	}
	return nil
}

// --- ElectricalPowerMeasurement (0x0090) ------------------------------

// ElectricalPowerServer projects a [interfaces.MatterFloatMeasurementSource]
// onto Matter ElectricalPowerMeasurement. The model unit is Watts;
// the wire unit is int64 in milliWatts per Matter §2.13.6 (e.g.
// 1500.0 W → 1500000 wire units).
//
// v1.1 surfaces only ActivePower; Voltage / Current / Frequency
// would each need their own typed source — they're declared in the
// attribute table but a per-attribute multi-source projection is
// future work. Reading those attributes returns null until then.
//
// ElectricalPowerServer embeds [cluster.DataVersionTracker] and
// implements [interfaces.MatterClusterDataVersion]. See TemperatureServer
// DataVersion tracking follows the same pattern as TemperatureServer.
type ElectricalPowerServer struct {
	cluster.DataVersionTracker
	src interfaces.MatterFloatMeasurementSource
}

// Compile-time assertion: ElectricalPowerServer satisfies MatterClusterDataVersion.
var _ interfaces.MatterClusterDataVersion = (*ElectricalPowerServer)(nil)

// NewElectricalPowerServer wraps src.
func NewElectricalPowerServer(src interfaces.MatterFloatMeasurementSource) *ElectricalPowerServer {
	return &ElectricalPowerServer{src: src}
}

// MatterDataVersion implements [interfaces.MatterClusterDataVersion].
func (s *ElectricalPowerServer) MatterDataVersion() uint32 { return s.Current() }

// MatterClusterID returns the Matter Electrical Power Measurement cluster ID (0x0090).
func (s *ElectricalPowerServer) MatterClusterID() uint32 { return ClusterElectricalPower }

// OnMatterValueChanged implements [interfaces.MatterChangeNotifier] by
// forwarding the wrapped source's notifier. On a metering switch the POWER
// sensor lives on a sibling meter channel and is attached cross-channel, so
// without this the endpoint's OnOff notifier (which filters to its own cluster)
// would never mark ActivePower dirty and the controller would only ever see the
// value on a read. Returns a no-op unsubscribe when the source cannot notify.
func (s *ElectricalPowerServer) OnMatterValueChanged(cb func()) func() {
	if n, ok := s.src.(interfaces.MatterChangeNotifier); ok && n != nil {
		return n.OnMatterValueChanged(cb)
	}
	return func() {}
}

// MatterRead resolves an attribute by ID against the underlying source.
func (s *ElectricalPowerServer) MatterRead(attrID uint32) (any, bool) {
	switch attrID {
	case attrElPwrPowerMode:
		// 0=Unknown, 1=DC, 2=AC. HM-PSM is mains-AC.
		return uint8(2), true
	case attrElPwrNumberOfMeasurementTypes:
		return uint8(1), true
	case attrElPwrAccuracy:
		// Matter §2.13.5.2 requires AccuracyRanges to have at least ONE
		// AccuracyRangeStruct; an empty list is schema-invalid
		// and strict validators (chip CHIP Error 0x26) reject it.
		// See [AccuracyStruct] for the stub-entry rationale.
		return []AccuracyStruct{{
			MeasurementType:  0x0008, // ActivePower
			Measured:         true,
			MinMeasuredValue: accuracyRangeMin,
			MaxMeasuredValue: accuracyRangeMax,
			AccuracyRanges: []AccuracyRangeStruct{{
				RangeMin: accuracyRangeMin,
				RangeMax: accuracyRangeMax,
				FixedMax: accuracyFixedMax,
			}},
		}}, true
	case attrElPwrActivePower:
		// Value temporarily unavailable — return (nil, true); see TemperatureServer.MatterRead.
		v, ok := s.src.MatterFloatValue()
		if !ok {
			return nil, true
		}
		return wattsToMilliWatts(v), true
	case attrElPwrVoltage, attrElPwrActiveCurrnt, attrElPwrFrequency:
		// Multi-source projection (per-attribute typed source) is
		// follow-up work; surface as null so controllers parse the
		// frame structurally even when the data is missing.
		return nil, true
	case cluster.AttrGlobalFeatureMap:
		return elPwrFeatureAltC, true
	case cluster.AttrGlobalClusterRevision:
		return electricalPowerClusterRevision, true
	}
	return nil, false
}

// MatterWrite returns errReadOnly — the Matter Electrical Power Measurement cluster is read-only at the wire layer.
func (s *ElectricalPowerServer) MatterWrite(_ context.Context, _ uint32, _ any, _ hmenum.CommandPriority) error {
	return errReadOnly
}

// MatterInvoke returns errNoCommands — the Matter Electrical Power Measurement cluster has no commands.
func (s *ElectricalPowerServer) MatterInvoke(_ context.Context, cmdID uint32, _ any, _ hmenum.CommandPriority) (any, error) {
	return nil, fmt.Errorf("%w (cmd 0x%02X)", errNoCommands, cmdID)
}

// MatterReportable returns the attribute IDs that the Matter Electrical Power Measurement cluster reports on change.
func (s *ElectricalPowerServer) MatterReportable() []uint32 { return []uint32{attrElPwrActivePower} }

// MatterAttributes lists every ElectricalPowerMeasurement (0x0090)
// attribute the server implements via MatterRead. Apple Home's HAP
// service rebuild reads the full attribute set; without this the
// dispatcher falls back to MatterReportable's single attribute.
func (s *ElectricalPowerServer) MatterAttributes() []uint32 {
	return []uint32{
		attrElPwrPowerMode,
		attrElPwrNumberOfMeasurementTypes,
		attrElPwrAccuracy,
		attrElPwrVoltage,
		attrElPwrActiveCurrnt,
		attrElPwrActivePower,
		attrElPwrFrequency,
	}
}

func wattsToMilliWatts(w float64) int64 {
	v := math.Round(w * 1000)
	if v > math.MaxInt64 {
		return math.MaxInt64
	}
	if v < math.MinInt64 {
		return math.MinInt64
	}
	return int64(v)
}

// --- ElectricalEnergyMeasurement (0x0091) -----------------------------

// ElectricalEnergyServer projects a [interfaces.MatterFloatMeasurementSource]
// onto Matter ElectricalEnergyMeasurement. Model unit: Wh; wire unit:
// int64 in milliwatt-hours per Matter §2.14.6.
//
// ElectricalEnergyServer embeds [cluster.DataVersionTracker] and
// implements [interfaces.MatterClusterDataVersion]. See TemperatureServer
// DataVersion tracking follows the same pattern as TemperatureServer.
type ElectricalEnergyServer struct {
	cluster.DataVersionTracker
	src interfaces.MatterFloatMeasurementSource
}

// Compile-time assertion: ElectricalEnergyServer satisfies MatterClusterDataVersion.
var _ interfaces.MatterClusterDataVersion = (*ElectricalEnergyServer)(nil)

// NewElectricalEnergyServer wraps src.
func NewElectricalEnergyServer(src interfaces.MatterFloatMeasurementSource) *ElectricalEnergyServer {
	return &ElectricalEnergyServer{src: src}
}

// MatterDataVersion implements [interfaces.MatterClusterDataVersion].
func (s *ElectricalEnergyServer) MatterDataVersion() uint32 { return s.Current() }

// MatterClusterID returns the Matter Electrical Energy Measurement cluster ID (0x0091).
func (s *ElectricalEnergyServer) MatterClusterID() uint32 { return ClusterElectricalEnergy }

// OnMatterValueChanged implements [interfaces.MatterChangeNotifier] by
// forwarding the wrapped source's notifier, so an ENERGY_COUNTER push on the
// sibling meter channel drives a proactive CumulativeEnergyImported report. See
// [ElectricalPowerServer.OnMatterValueChanged] for the cross-channel rationale.
func (s *ElectricalEnergyServer) OnMatterValueChanged(cb func()) func() {
	if n, ok := s.src.(interfaces.MatterChangeNotifier); ok && n != nil {
		return n.OnMatterValueChanged(cb)
	}
	return func() {}
}

// MatterRead resolves an attribute by ID against the underlying source.
func (s *ElectricalEnergyServer) MatterRead(attrID uint32) (any, bool) {
	switch attrID {
	case attrElEnAccuracy:
		// Matter §2.14.5.2 requires AccuracyRanges to have at least ONE
		// AccuracyRangeStruct; an empty list is schema-invalid.
		// See [AccuracyStruct] for the stub-entry rationale.
		return []AccuracyStruct{{
			MeasurementType:  0x0009, // ActiveEnergyImported
			Measured:         true,
			MinMeasuredValue: accuracyRangeMin,
			MaxMeasuredValue: accuracyRangeMax,
			AccuracyRanges: []AccuracyRangeStruct{{
				RangeMin: accuracyRangeMin,
				RangeMax: accuracyRangeMax,
				FixedMax: accuracyFixedMax,
			}},
		}}, true
	case attrElEnCumulativeImported:
		// Value temporarily unavailable — return (nil, true); see TemperatureServer.MatterRead.
		v, ok := s.src.MatterFloatValue()
		if !ok {
			return nil, true
		}
		// The attribute type is EnergyMeasurementStruct, NOT a bare
		// energy-mWh scalar — chip-tool's typed StructDecodeIterator
		// rejects a plain int64 here with "Wrong TLV type" (the report
		// path's generic logger masked that for a while).
		return EnergyMeasurementStruct{Energy: whToMilliWattHours(v)}, true
	case cluster.AttrGlobalFeatureMap:
		return elEnFeatureIMPE | elEnFeatureCUME, true
	case cluster.AttrGlobalClusterRevision:
		return electricalEnergyClusterRevision, true
	}
	return nil, false
}

// MatterWrite returns errReadOnly — the Matter Electrical Energy Measurement cluster is read-only at the wire layer.
func (s *ElectricalEnergyServer) MatterWrite(_ context.Context, _ uint32, _ any, _ hmenum.CommandPriority) error {
	return errReadOnly
}

// MatterInvoke returns errNoCommands — the Matter Electrical Energy Measurement cluster has no commands.
func (s *ElectricalEnergyServer) MatterInvoke(_ context.Context, cmdID uint32, _ any, _ hmenum.CommandPriority) (any, error) {
	return nil, fmt.Errorf("%w (cmd 0x%02X)", errNoCommands, cmdID)
}

// MatterReportable returns the attribute IDs that the Matter Electrical Energy Measurement cluster reports on change.
func (s *ElectricalEnergyServer) MatterReportable() []uint32 {
	return []uint32{attrElEnCumulativeImported}
}

// MatterAttributes lists every ElectricalEnergyMeasurement (0x0091)
// attribute the server implements via MatterRead. Apple Home's HAP
// service rebuild reads the full attribute set; without this the
// dispatcher falls back to MatterReportable's single attribute.
func (s *ElectricalEnergyServer) MatterAttributes() []uint32 {
	return []uint32{attrElEnAccuracy, attrElEnCumulativeImported}
}

func whToMilliWattHours(wh float64) int64 {
	v := math.Round(wh * 1000)
	if v > math.MaxInt64 {
		return math.MaxInt64
	}
	if v < math.MinInt64 {
		return math.MinInt64
	}
	return int64(v)
}

// --- AirQuality (0x005B) ----------------------------------------------

// AirQualityServer projects a [interfaces.MatterFloatMeasurementSource]
// carrying a pollutant concentration onto the Matter AirQuality cluster.
//
// The AirQualitySensor device type (0x002C) mandates this cluster
// alongside Identify and lists every concentration cluster as optional —
// matter.js `packages/node/src/devices/air-quality-sensor.ts:169`
// (`mandatory: { Identify: IdentifyServer, AirQuality: AirQualityServer }`).
// An endpoint that advertises the device type while serving only a
// concentration cluster fails the controller-side requirement check, so
// the materializer mounts this server on every air-quality endpoint.
//
// Matter §2.9.5.1 states the concentration → level mapping is the
// implementer's to define ("It is up to the device manufacturer to
// determine the mapping between the measured values and their
// corresponding enumeration values"), so the bridge grades against the
// pollutant's published guideline value; see [airQualityGoodBelow].
// FeatureMap stays 0, which restricts the reportable levels to the
// conformance-mandatory Unknown / Good / Poor.
//
// The server deliberately does not implement
// [interfaces.MatterChangeNotifier]: it shares its endpoint with the
// concentration cluster it derives from, and that source already drives
// the endpoint's notifier across the endpoint's full reportable-path
// set. A second listener on the same source would only mark the same
// path dirty twice. The Electrical* servers differ because they ride on
// a host endpoint whose notifier is a different source entirely.
//
// AirQualityServer embeds [cluster.DataVersionTracker] and implements
// [interfaces.MatterClusterDataVersion], following TemperatureServer.
type AirQualityServer struct {
	cluster.DataVersionTracker
	src interfaces.MatterFloatMeasurementSource
	// goodBelow is the upper bound (inclusive) of the Good level in the
	// source's own unit; graded is false for a class with no guideline,
	// in which case the server reports Unknown.
	goodBelow float64
	graded    bool
}

// Compile-time assertion: AirQualityServer satisfies MatterClusterDataVersion.
var _ interfaces.MatterClusterDataVersion = (*AirQualityServer)(nil)

// NewAirQualityServer constructs an AirQualityServer that grades src's
// readings against the guideline value for class.
func NewAirQualityServer(class interfaces.MatterMeasurementClass, src interfaces.MatterFloatMeasurementSource) *AirQualityServer {
	goodBelow, graded := airQualityGoodBelow(class)
	return &AirQualityServer{src: src, goodBelow: goodBelow, graded: graded}
}

// airQualityGoodBelow returns the concentration at or below which the
// air still counts as Good, expressed in the unit the model carries for
// that pollutant, and whether the class has a guideline at all.
//
// CO2 is ppm and uses the 1000 ppm indoor-air limit above which a room
// is considered inadequately ventilated. PM2.5 and PM10 are µg/m³ and
// use the World Health Organization 2021 air-quality guideline levels
// for the 24-hour mean (15 and 45 µg/m³) — the model classifies exactly
// the 24-hour-average parameters onto these classes, so the averaging
// windows line up.
func airQualityGoodBelow(class interfaces.MatterMeasurementClass) (float64, bool) {
	switch class {
	case interfaces.MatterMeasurementCO2:
		return 1000, true
	case interfaces.MatterMeasurementPM25:
		return 15, true
	case interfaces.MatterMeasurementPM10:
		return 45, true
	default:
		return 0, false
	}
}

// MatterDataVersion implements [interfaces.MatterClusterDataVersion].
func (s *AirQualityServer) MatterDataVersion() uint32 { return s.Current() }

// MatterClusterID returns the Matter Air Quality cluster ID (0x005B).
func (s *AirQualityServer) MatterClusterID() uint32 { return ClusterAirQuality }

// MatterRead resolves an attribute by ID against the underlying source.
func (s *AirQualityServer) MatterRead(attrID uint32) (any, bool) {
	switch attrID {
	case attrAirQualityLevel:
		// Unlike the measurement clusters this attribute is not
		// nullable — the enum carries its own Unknown member, so a
		// source without a reading yet reports Unknown rather than nil.
		return s.level(), true
	case cluster.AttrGlobalFeatureMap:
		return uint32(0), true
	case cluster.AttrGlobalClusterRevision:
		return airQualityClusterRevision, true
	}
	return nil, false
}

// level grades the current reading into an AirQualityEnum member.
func (s *AirQualityServer) level() uint8 {
	v, ok := s.src.MatterFloatValue()
	if !ok || !s.graded {
		return airQualityUnknown
	}
	if v <= s.goodBelow {
		return airQualityGood
	}
	return airQualityPoor
}

// MatterWrite returns errReadOnly — AirQuality carries a single "R V" attribute.
func (s *AirQualityServer) MatterWrite(_ context.Context, _ uint32, _ any, _ hmenum.CommandPriority) error {
	return errReadOnly
}

// MatterInvoke returns errNoCommands — the Matter Air Quality cluster has no commands.
func (s *AirQualityServer) MatterInvoke(_ context.Context, cmdID uint32, _ any, _ hmenum.CommandPriority) (any, error) {
	return nil, fmt.Errorf("%w (cmd 0x%02X)", errNoCommands, cmdID)
}

// MatterReportable returns the attribute IDs the cluster reports on change.
func (s *AirQualityServer) MatterReportable() []uint32 { return []uint32{attrAirQualityLevel} }

// MatterAttributes lists every AirQuality (0x005B) attribute the server
// implements via MatterRead, so the dispatcher advertises the full
// AttributeList rather than falling back to MatterReportable.
func (s *AirQualityServer) MatterAttributes() []uint32 { return []uint32{attrAirQualityLevel} }

// --- Concentration Measurement (CO2 / PM2.5 / PM10) ------------------

// concentrationServer is the shared shape behind CO2ConcentrationServer,
// PM25ConcentrationServer and PM10ConcentrationServer. The clusters
// have identical attribute layouts per Matter §2.10; only the cluster
// ID and (sometimes) the MeasurementUnit differ.
//
// MeasuredValue is encoded as IEEE-754 single-precision float32 per
// spec. We simply forward the model's float64 value through float32
// truncation — the model's unit is the spec unit (PPM for CO2, µg/m³
// for PM2.5 / PM10), so no scaling is required.
//
// concentrationServer embeds [cluster.DataVersionTracker] and
// implements [interfaces.MatterClusterDataVersion] so the three named
// wrapper types (CO2, PM2.5, PM10) inherit the tracker. See
// DataVersion tracking follows the same pattern as TemperatureServer.
type concentrationServer struct {
	cluster.DataVersionTracker
	src       interfaces.MatterFloatMeasurementSource
	clusterID uint32
	unit      uint8 // MeasurementUnit enum (PPM = 0, µg/m³ = 4)
}

// MatterDataVersion implements [interfaces.MatterClusterDataVersion].
func (s *concentrationServer) MatterDataVersion() uint32 { return s.Current() }

func (s *concentrationServer) MatterClusterID() uint32 { return s.clusterID }

func (s *concentrationServer) MatterRead(attrID uint32) (any, bool) {
	switch attrID {
	case attrConcMeasuredValue:
		// Value temporarily unavailable — return (nil, true); see TemperatureServer.MatterRead.
		v, ok := s.src.MatterFloatValue()
		if !ok {
			return nil, true
		}
		return float32(v), true
	case attrConcMinMeasuredValue:
		return float32(0), true
	case attrConcMaxMeasuredValue:
		// CO2: ≤ 5000 ppm typical (OSHA limit 5000 ppm 8h-TWA);
		// PM: ≤ 1000 µg/m³ extreme. Cap at a sensible high bound;
		// real device-specific limits would tighten this when known.
		return float32(100000), true
	case attrConcMeasurementUnit:
		return s.unit, true
	case attrConcMeasurementMedium:
		return concMediumAir, true
	case cluster.AttrGlobalFeatureMap:
		return concFeatureMEA, true
	case cluster.AttrGlobalClusterRevision:
		return concentrationClusterRevision, true
	}
	return nil, false
}

func (s *concentrationServer) MatterWrite(_ context.Context, _ uint32, _ any, _ hmenum.CommandPriority) error {
	return errReadOnly
}

func (s *concentrationServer) MatterInvoke(_ context.Context, cmdID uint32, _ any, _ hmenum.CommandPriority) (any, error) {
	return nil, fmt.Errorf("%w (cmd 0x%02X)", errNoCommands, cmdID)
}

func (s *concentrationServer) MatterReportable() []uint32 { return []uint32{attrConcMeasuredValue} }

// MatterAttributes lists every concentration cluster attribute the
// server implements via MatterRead. Apple Home's HAP service rebuild
// reads the full attribute set; without this the dispatcher falls back
// to MatterReportable's single attribute.
func (s *concentrationServer) MatterAttributes() []uint32 {
	return []uint32{
		attrConcMeasuredValue,
		attrConcMinMeasuredValue,
		attrConcMaxMeasuredValue,
		attrConcMeasurementUnit,
		attrConcMeasurementMedium,
	}
}

// CO2ConcentrationServer projects a [interfaces.MatterFloatMeasurementSource]
// onto Matter CarbonDioxideConcentrationMeasurement (0x040D). Model
// unit: ppm; wire unit: float32 ppm.
// Inherits [cluster.DataVersionTracker] via concentrationServer.
type CO2ConcentrationServer struct{ concentrationServer }

// Compile-time assertion: CO2ConcentrationServer satisfies MatterClusterDataVersion.
var _ interfaces.MatterClusterDataVersion = (*CO2ConcentrationServer)(nil)

// NewCO2ConcentrationServer constructs a CO2ConcentrationServer backed by src.
func NewCO2ConcentrationServer(src interfaces.MatterFloatMeasurementSource) *CO2ConcentrationServer {
	return &CO2ConcentrationServer{concentrationServer{src: src, clusterID: ClusterCO2Concentration, unit: concUnitPPM}}
}

// PM25ConcentrationServer projects a [interfaces.MatterFloatMeasurementSource]
// onto Matter PM2_5ConcentrationMeasurement (0x042A). Model unit:
// µg/m³; wire unit: float32 µg/m³.
// Inherits [cluster.DataVersionTracker] via concentrationServer.
type PM25ConcentrationServer struct{ concentrationServer }

// Compile-time assertion: PM25ConcentrationServer satisfies MatterClusterDataVersion.
var _ interfaces.MatterClusterDataVersion = (*PM25ConcentrationServer)(nil)

// NewPM25ConcentrationServer constructs a PM25ConcentrationServer backed by src.
func NewPM25ConcentrationServer(src interfaces.MatterFloatMeasurementSource) *PM25ConcentrationServer {
	return &PM25ConcentrationServer{concentrationServer{src: src, clusterID: ClusterPM25Concentration, unit: concUnitMicroGramPerCubicMeter}}
}

// PM10ConcentrationServer projects a [interfaces.MatterFloatMeasurementSource]
// onto Matter PM10ConcentrationMeasurement (0x042D). Model unit:
// µg/m³; wire unit: float32 µg/m³.
// Inherits [cluster.DataVersionTracker] via concentrationServer.
type PM10ConcentrationServer struct{ concentrationServer }

// Compile-time assertion: PM10ConcentrationServer satisfies MatterClusterDataVersion.
var _ interfaces.MatterClusterDataVersion = (*PM10ConcentrationServer)(nil)

// NewPM10ConcentrationServer constructs a PM10ConcentrationServer backed by src.
func NewPM10ConcentrationServer(src interfaces.MatterFloatMeasurementSource) *PM10ConcentrationServer {
	return &PM10ConcentrationServer{concentrationServer{src: src, clusterID: ClusterPM10Concentration, unit: concUnitMicroGramPerCubicMeter}}
}

// --- PowerSource (0x002F) — battery-only flavour ----------------------

// PowerSourceServer projects either a [interfaces.MatterBoolMeasurementSource]
// (typically the LOWBAT binary parameter) or a
// [interfaces.MatterFloatMeasurementSource] (a derived battery-percentage
// sensor, e.g. OperatingVoltageLevelSensor) onto a battery-flavoured
// Matter PowerSource cluster. A server instance wraps exactly one of
// the two — see [NewPowerSourceServer] and
// [NewPowerSourceServerFromFloat].
//
// Bool source drives BatChargeLevel (conformance BAT, mandatory):
//
//	false → BatChargeLevel = OK (0)
//	true  → BatChargeLevel = Warning (1)
//
// HM has no Critical-level signal; devices that go fully critical
// disconnect from the network instead.
//
// Float source drives BatPercentRemaining (conformance [BAT], optional
// — matter.js power-source-cluster.element.ts:68-71, id 0xc, uint8,
// constraint "max 200", quality "X Q"): the source's 0-100 percentage
// is converted to Matter's half-percent units (0..200) and only
// advertised by instances built via [NewPowerSourceServerFromFloat] —
// a bool-source instance omits the attribute entirely rather than
// reporting null forever, matching the optional conformance.
//
// PowerSourceServer embeds [cluster.DataVersionTracker] and implements
// [interfaces.MatterClusterDataVersion]. See TemperatureServer for the
// DataVersion tracking follows the same pattern as TemperatureServer.
type PowerSourceServer struct {
	cluster.DataVersionTracker
	src      interfaces.MatterBoolMeasurementSource
	floatSrc interfaces.MatterFloatMeasurementSource
	// endpoint is the Matter endpoint this power source feeds, stamped
	// post-construction by the endpoint assembler via [SetEndpoint] so
	// EndpointList (0x001F) can name it. Zero means "unspecified", which
	// Matter §11.7.6.20 permits (an empty list is reported in that case).
	endpoint uint16
}

// Compile-time assertion: PowerSourceServer satisfies MatterClusterDataVersion.
var _ interfaces.MatterClusterDataVersion = (*PowerSourceServer)(nil)

// NewPowerSourceServer wraps a boolean LOWBAT-style source. Serves
// BatChargeLevel; BatPercentRemaining is not advertised (optional
// conformance [BAT] — a bool source has no percentage to report).
func NewPowerSourceServer(src interfaces.MatterBoolMeasurementSource) *PowerSourceServer {
	return &PowerSourceServer{src: src}
}

// NewPowerSourceServerFromFloat wraps a derived battery-percentage
// source (e.g. OperatingVoltageLevelSensor). Serves BatPercentRemaining
// from it; BatChargeLevel still reports OK — there is no boolean
// LOWBAT signal to derive Warning from, mirroring MatterRead's
// no-observation fallback for the bool path.
func NewPowerSourceServerFromFloat(src interfaces.MatterFloatMeasurementSource) *PowerSourceServer {
	return &PowerSourceServer{floatSrc: src}
}

// SetEndpoint stamps the endpoint id this power source is mounted on so
// EndpointList (0x001F) reports it. The endpoint assembler calls this after
// construction, mirroring the BasicInformation / GeneralDiagnostics pattern.
func (s *PowerSourceServer) SetEndpoint(endpoint uint16) { s.endpoint = endpoint }

// MatterDataVersion implements [interfaces.MatterClusterDataVersion].
func (s *PowerSourceServer) MatterDataVersion() uint32 { return s.Current() }

// MatterClusterID returns the Matter Power Source cluster ID (0x002F).
func (s *PowerSourceServer) MatterClusterID() uint32 { return ClusterPowerSource }

// MatterRead resolves an attribute by ID against the underlying source.
func (s *PowerSourceServer) MatterRead(attrID uint32) (any, bool) {
	switch attrID {
	case attrPwrStatus:
		return pwrStatusActive, true
	case attrPwrOrder:
		return uint8(1), true // primary source
	case attrPwrDescription:
		return "Battery", true
	case attrPwrBatChargeLevel:
		// s.src is nil on a float-constructed instance (see
		// [NewPowerSourceServerFromFloat]) — there is no boolean LOWBAT
		// signal to derive Warning from, so it falls back to OK the same
		// way an unobserved bool source does.
		if s.src == nil {
			return batChargeOK, true
		}
		v, ok := s.src.MatterBoolValue()
		if !ok {
			return batChargeOK, true
		}
		if v {
			return batChargeWarning, true
		}
		return batChargeOK, true
	case attrPwrBatPercentRemaining:
		// Optional conformance [BAT] — only present on a
		// float-constructed instance; the bool-only constructor never
		// reaches here because MatterAttributes()/MatterReportable()
		// omit this ID for it, but a wildcard read still calls MatterRead
		// directly, so guard explicitly rather than relying on that.
		if s.floatSrc == nil {
			return nil, false
		}
		v, ok := s.floatSrc.MatterFloatValue()
		if !ok {
			// Present but currently unknown — quality X permits null.
			return nil, true
		}
		return percentToHalfPercent(v), true
	case attrPwrBatReplacementNeeded:
		if s.src == nil {
			return false, true
		}
		v, ok := s.src.MatterBoolValue()
		if !ok {
			return false, true
		}
		return v, true
	case attrPwrBatReplaceability:
		return batReplaceUserReplaceable, true
	case attrPwrEndpointList:
		// EndpointList names the endpoints this power source feeds. We mount
		// one power source per bridged device endpoint, so the list is the
		// single stamped endpoint. An unset (zero) endpoint reports the empty
		// list, which Matter §11.7.6.20 permits as "unspecified".
		// matter.js: power-source.element.ts EndpointList (0x001F).
		if s.endpoint == 0 {
			return []uint16{}, true
		}
		return []uint16{s.endpoint}, true
	case cluster.AttrGlobalFeatureMap:
		// BAT (bit 1) alone — see [pwrFeatureBAT] for why REPLC stays clear.
		return pwrFeatureBAT, true
	case cluster.AttrGlobalClusterRevision:
		return powerSourceClusterRevision, true
	}
	return nil, false
}

// MatterWrite returns errReadOnly — the Matter Power Source cluster is read-only at the wire layer.
func (s *PowerSourceServer) MatterWrite(_ context.Context, _ uint32, _ any, _ hmenum.CommandPriority) error {
	return errReadOnly
}

// MatterInvoke returns errNoCommands — the Matter Power Source cluster has no commands.
func (s *PowerSourceServer) MatterInvoke(_ context.Context, cmdID uint32, _ any, _ hmenum.CommandPriority) (any, error) {
	return nil, fmt.Errorf("%w (cmd 0x%02X)", errNoCommands, cmdID)
}

// MatterReportable returns the attribute IDs that the Matter Power Source cluster reports on change.
func (s *PowerSourceServer) MatterReportable() []uint32 {
	if s.floatSrc != nil {
		return []uint32{attrPwrBatChargeLevel, attrPwrBatReplacementNeeded, attrPwrBatPercentRemaining}
	}
	return []uint32{attrPwrBatChargeLevel, attrPwrBatReplacementNeeded}
}

// MatterAttributes lists every PowerSource (0x002F) attribute the
// server implements via MatterRead. Apple Home's HAP service rebuild
// reads the full attribute set; without this the dispatcher falls back
// to MatterReportable's two-attribute subscription surface.
//
// attrPwrBatPercentRemaining is conformance [BAT] (optional even with
// BAT set) and is only listed for a float-constructed instance — see
// [NewPowerSourceServerFromFloat] — matching the way a bool-constructed
// instance genuinely does not implement it rather than reporting null
// forever.
func (s *PowerSourceServer) MatterAttributes() []uint32 {
	attrs := []uint32{
		attrPwrStatus,
		attrPwrOrder,
		attrPwrDescription,
		attrPwrBatChargeLevel,
		attrPwrBatReplacementNeeded,
		attrPwrBatReplaceability,
		attrPwrEndpointList,
	}
	if s.floatSrc != nil {
		attrs = append(attrs, attrPwrBatPercentRemaining)
	}
	return attrs
}

// percentToHalfPercent converts a 0-100 battery percentage to Matter's
// BatPercentRemaining wire encoding — half-percent units, uint8
// 0..200 — per Matter §11.7.6.5.2 / matter.js
// power-source-cluster.element.ts:68-71 (constraint "max 200"). Rounds
// first, then clamps — a value like 100.4 % rounds to 200.8 half-
// percent before the ceiling clamps it to 200, rather than clamping
// the percentage to 100 first and losing the distinction.
func percentToHalfPercent(pct float64) uint8 {
	half := math.Round(pct * 2)
	if half < 0 {
		half = 0
	}
	if half > 200 {
		half = 200
	}
	return uint8(half)
}
