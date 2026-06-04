// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package calculated

import "github.com/SukramJ/openccu-loom/pkg/hmenum"

// ChannelInspector is the minimal channel-shape view that calculated sensors
// need to decide whether they apply to a channel. Both `*device.Channel` and
// lightweight test stubs implement it through a `HasParameter(name string)
// bool` method on the receiver.
type ChannelInspector interface {
	HasParameter(name string) bool
}

// voltageChannelInspector extends [ChannelInspector] with MASTER-paramset
// parameter lookups required by [IsOperatingVoltageLevelRelevant]. A
// concrete implementation is optional; [IsOperatingVoltageLevelRelevant]
// probes for the interface via a type assertion and falls back to a
// VALUES-only check when the channel does not implement it.
type voltageChannelInspector interface {
	// HasMasterParameter reports whether the channel exposes a MASTER-paramset
	// data point with the given parameter name.
	HasMasterParameter(name string) bool
	// HasDeviceMasterParameter reports whether the device-root channel exposes
	// a MASTER-paramset data point with the given parameter name. Mirrors the
	// Python channel.device.get_generic_data_point(channel_address=device.address, ...)
	// lookup used by OperatingVoltageLevel.is_relevant_for_model
	// (operating_voltage_level.py).
	HasDeviceMasterParameter(name string) bool
}

// Model-whitelist tables. Mirrors
//   - climate.py:268 _RELEVANT_MODELS_APPARENT_TEMPERATURE
//   - climate.py:271 _RELEVANT_MODELS_FROST_POINT
//
// Empty / nil whitelist means "any model", matching Python's
// `relevant_models is None` branch.
var (
	relevantModelsApparentTemperature = []string{"HmIP-SWO"}
	relevantModelsFrostPoint          = []string{"HmIP-STHO", "HmIP-SWO"}
)

// modelMatches reports whether `model` matches any prefix in `allowed`. Empty
// `allowed` returns true (= "any model").
func modelMatches(model string, allowed []string) bool {
	if len(allowed) == 0 {
		return true
	}
	for _, a := range allowed {
		if a == "" {
			continue
		}
		if hasPrefixFold(model, a) {
			return true
		}
	}
	return false
}

func hasPrefixFold(s, prefix string) bool {
	if len(s) < len(prefix) {
		return false
	}
	for i := range len(prefix) {
		ca := s[i]
		cb := prefix[i]
		if ca >= 'A' && ca <= 'Z' {
			ca += 'a' - 'A'
		}
		if cb >= 'A' && cb <= 'Z' {
			cb += 'a' - 'A'
		}
		if ca != cb {
			return false
		}
	}
	return true
}

// IsTemperatureHumiditySensorRelevant reports whether a derived
// temperature/humidity sensor (DewPoint, DewPointSpread, VaporConcentration,
// basic Enthalpy) makes sense for the channel — i.e. both temperature *and*
// humidity are exposed.
func IsTemperatureHumiditySensorRelevant(ch ChannelInspector) bool {
	if ch == nil {
		return false
	}
	hasTemp := ch.HasParameter(string(hmenum.ParameterActualTemperature)) ||
		ch.HasParameter(string(hmenum.ParameterTemperature))
	hasHum := ch.HasParameter(string(hmenum.ParameterHumidity)) ||
		ch.HasParameter(string(hmenum.ParameterActualHumidity))
	return hasTemp && hasHum
}

// IsApparentTemperatureRelevant requires ACTUAL_TEMPERATURE + HUMIDITY +
// WIND_SPEED (exact parameter names, no fallbacks) AND the device model must
// be in the _RELEVANT_MODELS_APPARENT_TEMPERATURE whitelist (HmIP-SWO).
// Mirrors ApparentTemperature.is_relevant_for_model (climate.py).
func IsApparentTemperatureRelevant(ch ChannelInspector, model string) bool {
	if ch == nil {
		return false
	}
	if !modelMatches(model, relevantModelsApparentTemperature) {
		return false
	}
	return ch.HasParameter(string(hmenum.ParameterActualTemperature)) &&
		ch.HasParameter(string(hmenum.ParameterHumidity)) &&
		ch.HasParameter(string(hmenum.ParameterWindSpeed))
}

// IsDewPointRelevant — alias for [IsTemperatureHumiditySensorRelevant]
// kept as a stable name so adapters can register sensors by symbolic
// purpose.
func IsDewPointRelevant(ch ChannelInspector) bool { return IsTemperatureHumiditySensorRelevant(ch) }

// IsDewPointSpreadRelevant — temperature + humidity required.
func IsDewPointSpreadRelevant(ch ChannelInspector) bool {
	return IsTemperatureHumiditySensorRelevant(ch)
}

// IsFrostPointRelevant — temperature + humidity required AND the device model
// must be in the `_RELEVANT_MODELS_FROST_POINT` whitelist (HmIP-STHO,
// HmIP-SWO).
func IsFrostPointRelevant(ch ChannelInspector, model string) bool {
	if ch == nil {
		return false
	}
	if !modelMatches(model, relevantModelsFrostPoint) {
		return false
	}
	return IsTemperatureHumiditySensorRelevant(ch)
}

// IsVaporConcentrationRelevant — alias for [IsTemperatureHumiditySensorRelevant].
func IsVaporConcentrationRelevant(ch ChannelInspector) bool {
	return IsTemperatureHumiditySensorRelevant(ch)
}

// IsEnthalpyRelevant — temperature + humidity required; pressure is
// optional (sensor uses [DefaultPressureHPa] when missing).
func IsEnthalpyRelevant(ch ChannelInspector) bool { return IsTemperatureHumiditySensorRelevant(ch) }
