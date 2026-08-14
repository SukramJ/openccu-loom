// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package calculated

import (
	"sync"

	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// DerivedBinarySensor emits a boolean derived from an upstream enum
// parameter. A value is "on" when it matches one of [OnValues]; when
// [OffValues] is empty, everything not in OnValues counts as off.
//
// This covers the WINDOW_OPEN / SMOKE_ALARM / INTRUSION_ALARM
// calculated parameters from SPECIFICATION §7.3 as well as any
// future derived-binary mapping the profile generator produces.
//
// The inner [generic.BinarySensor] carries the canonical
// `<central>:<channelAddress>:CALCULATED/<calcParam>` UniqueID via
// [generic.Spec.KeyNameOverride]; no outer BaseDataPointFields
// embed (V2 fix, PR-32 — see [DewPointSensor] for the rationale).
// [SourceParameter] is the wire parameter the
// [DerivedBinarySensor.Subscribe] hook subscribes to.
//
// [StateUncertain] aggregates over registered source DPs via the
// embedded [sourceSink].
type DerivedBinarySensor struct {
	*generic.BinarySensor
	sourceSink

	SourceParameter hmenum.Parameter
	OnValues        map[string]struct{}
	OffValues       map[string]struct{} // optional; nil means "on's complement"

	calcParam hmenum.CalculatedParameter

	// mu guards the dedup slots below. OnLabel runs on whichever goroutine
	// delivered the upstream enum update — a device that relays a peer's alarm
	// can deliver two labels at once — while IsRefreshed is read from
	// north-bound payload assembly.
	mu      sync.Mutex
	last    bool
	hasLast bool
}

// NewDerivedBinarySensor constructs a sensor with the given on/off
// value sets. Both sets may be nil; at minimum [OnValues] should be
// populated for the sensor to ever report true.
//
// The default identity is empty (`::CALCULATED/<param>`); use
// [NewDerivedBinarySensorWithIdentity] from a multi-CCU call site so
// the [datapoint.BaseDataPointFields.UniqueID] is rooted at the
// owning channel.
func NewDerivedBinarySensor(calcParam hmenum.CalculatedParameter, onValues, offValues []string) *DerivedBinarySensor {
	return NewDerivedBinarySensorWithIdentity("", "", calcParam, hmenum.Parameter(""), onValues, offValues)
}

// NewDerivedBinarySensorWithIdentity constructs a sensor rooted at
// `<central>:<channelAddress>:CALCULATED/<calcParam>`. `source` is
// the wire parameter the [DerivedBinarySensor.Subscribe] hook taps
// into; pass empty when the source is wired manually by the caller
// (e.g. test fixtures invoking [DerivedBinarySensor.OnLabel] directly).
func NewDerivedBinarySensorWithIdentity(
	centralName, channelAddress string,
	calcParam hmenum.CalculatedParameter,
	source hmenum.Parameter,
	onValues, offValues []string,
) *DerivedBinarySensor {
	s := &DerivedBinarySensor{
		BinarySensor:    newDerivedBinarySensor(calcParam, centralName, channelAddress),
		SourceParameter: source,
		OnValues:        toSet(onValues),
		OffValues:       toSet(offValues),
		calcParam:       calcParam,
	}
	installSourceValidityGate(s.BinarySensor, &s.sourceSink)
	return s
}

// CalculatedParameter returns the calculated parameter id this sensor
// emits. Mirrors the climate-derived sensors so the [Sensor] interface
// is uniform.
func (s *DerivedBinarySensor) CalculatedParameter() hmenum.CalculatedParameter {
	return s.calcParam
}

// IsRefreshed reports whether the sensor has classified at least one
// upstream label.
func (s *DerivedBinarySensor) IsRefreshed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.hasLast
}

// StateUncertain aggregates over the registered source DPs via the
// embedded [sourceSink]. When [Subscribe] was called and the channel
// exposed the [SourceParameter], the source DP is registered and any
// optimistic write on it makes the derived sensor uncertain.
func (s *DerivedBinarySensor) StateUncertain() bool {
	return s.sourceSink.StateUncertain()
}

// NewWindowOpenSensor is a convenience constructor pre-configured with
// the CCU's WINDOW_STATE enum labels.
func NewWindowOpenSensor() *DerivedBinarySensor {
	return NewDerivedBinarySensor(
		hmenum.CalculatedParameterWindowOpen,
		[]string{"OPEN", "TILTED"},
		[]string{"CLOSED"},
	)
}

// NewSmokeAlarmSensor is a convenience constructor for SMOKE_DETECTOR
// Derived sensors. The on-set strictly mirrors
// `SMOKE_DETECTOR_ALARM_STATUS` mapping (derived_binary_sensor.py:194):
// PRIMARY_ALARM and SECONDARY_ALARM (the device alarms or relays a
// peer's primary alarm). INTRUSION_ALARM is *not* a smoke alarm — it
// is exposed by [NewIntrusionAlarmSensor] separately.
func NewSmokeAlarmSensor() *DerivedBinarySensor {
	return NewDerivedBinarySensor(
		hmenum.CalculatedParameterSmokeAlarm,
		[]string{"PRIMARY_ALARM", "SECONDARY_ALARM"},
		[]string{"IDLE_OFF", "IDLE_ON", "INTRUSION_ALARM"},
	)
}

// NewIntrusionAlarmSensor is a convenience constructor for the HmIP-ASIR
// family's INTRUSION state.
func NewIntrusionAlarmSensor() *DerivedBinarySensor {
	return NewDerivedBinarySensor(
		hmenum.CalculatedParameterIntrusionAlarm,
		[]string{"INTRUSION_ALARM"},
		[]string{"IDLE_OFF", "IDLE_ON", "PRIMARY_ALARM", "SECONDARY_ALARM"},
	)
}

// DerivedBinaryMapping records a per-model derived-binary registration.
type DerivedBinaryMapping struct {
	// Models is the prefix-match list of device models this mapping
	// applies to (case-insensitive). Empty means "any model".
	Models []string
	// SourceParameter is the wire parameter the mapping subscribes to.
	SourceParameter hmenum.Parameter
	// SourceChannelNo restricts the mapping to a specific channel number on the
	// device. -1 (the default zero value's negation shape; we use 0 here)
	// accepts every channel that exposes SourceParameter — historic registry
	// behaviour preserved for the existing mappings.
	//
	// 0 in the underlying CCU paramset is a *real* channel number (the
	// maintenance channel); we therefore use a pointer-style "negative-zero is
	// opt-out" convention via [SourceChannelNoOpen] — see
	// [DerivedBinaryMapping.AppliesToChannel].
	SourceChannelNo int
	// CalculatedParameter is the synthetic parameter id the derived
	// sensor exposes.
	CalculatedParameter hmenum.CalculatedParameter
	// OnValues / OffValues mirror the corresponding fields on
	// DerivedBinarySensor.
	OnValues  []string
	OffValues []string
}

// SourceChannelNoOpen is the [DerivedBinaryMapping.SourceChannelNo]
// sentinel that disables channel filtering (any channel exposing
// SourceParameter qualifies). The value -1 cannot collide with a
// real CCU channel number.
const SourceChannelNoOpen = -1

// AppliesToChannel reports whether the mapping should be applied to the
// channel of `chNo`.
func (m DerivedBinaryMapping) AppliesToChannel(chNo int) bool {
	if m.SourceChannelNo == SourceChannelNoOpen {
		return true
	}
	return m.SourceChannelNo == chNo
}

// derivedBinaryRegistry is the per-CalculatedParameter mapping table.
var derivedBinaryRegistry = []DerivedBinaryMapping{
	{
		Models:              []string{"HmIP-SRH", "HM-Sec-RHS"},
		SourceParameter:     hmenum.ParameterState,
		SourceChannelNo:     1,
		CalculatedParameter: hmenum.CalculatedParameterWindowOpen,
		OnValues:            []string{"OPEN", "TILTED"},
		OffValues:           []string{"CLOSED"},
	},
	{
		Models:              []string{"HmIP-SWSD"},
		SourceParameter:     hmenum.ParameterSmokeDetectorAlarmStatus,
		SourceChannelNo:     1,
		CalculatedParameter: hmenum.CalculatedParameterSmokeAlarm,
		OnValues:            []string{"PRIMARY_ALARM", "SECONDARY_ALARM"},
		OffValues:           []string{"IDLE_OFF", "IDLE_ON", "INTRUSION_ALARM"},
	},
	{
		Models:              []string{"HmIP-SWSD"},
		SourceParameter:     hmenum.ParameterSmokeDetectorAlarmStatus,
		SourceChannelNo:     1,
		CalculatedParameter: hmenum.CalculatedParameterIntrusionAlarm,
		OnValues:            []string{"INTRUSION_ALARM"},
		OffValues:           []string{"IDLE_OFF", "IDLE_ON", "PRIMARY_ALARM", "SECONDARY_ALARM"},
	},
}

// LookupDerivedBinaryMappings returns every derived-binary mapping applicable
// to the given model.
func LookupDerivedBinaryMappings(model string) []DerivedBinaryMapping {
	var out []DerivedBinaryMapping
	for _, m := range derivedBinaryRegistry {
		if modelMatches(model, m.Models) {
			out = append(out, m)
		}
	}
	return out
}

// LookupDerivedBinaryMappingByParam returns the first mapping in the registry
// whose CalculatedParameter equals param. Returns the mapping and true when
// found, or a zero value and false when absent.
func LookupDerivedBinaryMappingByParam(param hmenum.CalculatedParameter) (DerivedBinaryMapping, bool) {
	for _, m := range derivedBinaryRegistry {
		if m.CalculatedParameter == param {
			return m, true
		}
	}
	return DerivedBinaryMapping{}, false
}

// IsRelevantForMapping reports whether the mapping applies to the given
// channel number AND the model string.
func IsRelevantForMapping(m DerivedBinaryMapping, model string, chNo int) bool {
	return m.AppliesToChannel(chNo) && modelMatches(model, m.Models)
}

// IsRelevantForModel reports whether any mapping in the registry applies to
// the given model + channel-number pair.
func IsRelevantForModel(model string, chNo int) bool {
	for _, m := range derivedBinaryRegistry {
		if IsRelevantForMapping(m, model, chNo) {
			return true
		}
	}
	return false
}

// MakeDerivedBinarySensor constructs a [DerivedBinarySensor] from a
// registry mapping.
func MakeDerivedBinarySensor(m DerivedBinaryMapping) *DerivedBinarySensor {
	return NewDerivedBinarySensor(m.CalculatedParameter, m.OnValues, m.OffValues)
}

// OnLabel feeds a new enum label from the upstream parameter.
func (s *DerivedBinarySensor) OnLabel(label string) {
	value, ok := s.classify(label)
	if !ok {
		return
	}
	// The compare-and-set stays atomic; OnEvent fans out to subscribers and must
	// not run under the sensor lock.
	s.mu.Lock()
	if s.hasLast && s.last == value {
		s.mu.Unlock()
		return
	}
	s.last, s.hasLast = value, true
	s.mu.Unlock()
	s.OnEvent(value)
}

// classify returns the derived value and whether it is meaningful.
// Unknown labels report (false, false), which [OnLabel] interprets as
// "hold previous value".
func (s *DerivedBinarySensor) classify(label string) (value, ok bool) {
	if _, on := s.OnValues[label]; on {
		return true, true
	}
	if s.OffValues == nil {
		return false, true
	}
	if _, off := s.OffValues[label]; off {
		return false, true
	}
	// Unknown label — hold the previous value.
	return false, false
}

func toSet(values []string) map[string]struct{} {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]struct{}, len(values))
	for _, v := range values {
		out[v] = struct{}{}
	}
	return out
}

// newDerivedBinarySensor constructs a generic.BinarySensor for a
// derived parameter. There is no wire channel or Writer, only a
// Parameter tag; the sensor lives purely as an observable surface.
//
// `central` + `channelAddress` scope the embedded
// [datapoint.BaseDataPointFields.UniqueID]; the keyName is fixed to
// `CALCULATED/<param>` via [generic.Spec.KeyNameOverride] so the
// inner DataPoint produces the family-prefixed UniqueID directly
// no outer BaseDataPointFields embed needed on the calculated
// sensor type.
func newDerivedBinarySensor(p hmenum.CalculatedParameter, centralName, channelAddress string) *generic.BinarySensor {
	return generic.NewBinarySensor(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: channelAddress,
			Parameter:      string(p),
		},
		CentralName:     centralName,
		KeyNameOverride: calculatedKeyName(p),
		// Stamp KindBinarySensor so (*DataPoint).Category() returns
		// DataPointCategory.BINARY_SENSOR. Same fix as the
		// resolveDataPoint pipeline; without it the calculated
		// derived-binary surfaces as `category: undefined`.
		Kind:       generic.KindBinarySensor,
		Descriptor: hmproto.ParameterData{Type: hmenum.ParameterTypeBool, Operations: hmenum.OperationsRead | hmenum.OperationsEvent},
	})
}
