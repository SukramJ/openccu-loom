// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package calculated

import (
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// stubChannel implements ChannelInspector only (no voltageChannelInspector).
// Used for tests that exercise the VALUES-only fallback path.
type stubChannel struct {
	params map[string]bool
}

func (s *stubChannel) HasParameter(name string) bool { return s.params[name] }

func channelWith(params ...string) *stubChannel {
	m := make(map[string]bool, len(params))
	for _, p := range params {
		m[p] = true
	}
	return &stubChannel{params: m}
}

// fullStubChannel implements both ChannelInspector and voltageChannelInspector,
// enabling MASTER-paramset checks in IsOperatingVoltageLevelRelevant.
type fullStubChannel struct {
	params       map[string]bool
	masterParams map[string]bool
	deviceMaster map[string]bool
}

func (s *fullStubChannel) HasParameter(name string) bool { return s.params[name] }

// HasMasterParameter satisfies voltageChannelInspector.
func (s *fullStubChannel) HasMasterParameter(name string) bool {
	return s.masterParams[name]
}

// HasDeviceMasterParameter satisfies voltageChannelInspector.
func (s *fullStubChannel) HasDeviceMasterParameter(name string) bool {
	return s.deviceMaster[name]
}

// channelWithMaster builds a stub that also provides MASTER-paramset parameters
// at channel level and optionally at device-root level.
func channelWithMaster(valuesParams, masterParams, deviceMaster []string) *fullStubChannel {
	ch := &fullStubChannel{
		params:       make(map[string]bool, len(valuesParams)),
		masterParams: make(map[string]bool, len(masterParams)),
		deviceMaster: make(map[string]bool, len(deviceMaster)),
	}
	for _, p := range valuesParams {
		ch.params[p] = true
	}
	for _, p := range masterParams {
		ch.masterParams[p] = true
	}
	for _, p := range deviceMaster {
		ch.deviceMaster[p] = true
	}
	return ch
}

func TestRelevanceTempHumidity(t *testing.T) {
	if IsTemperatureHumiditySensorRelevant(nil) {
		t.Fatal("nil channel must not be relevant")
	}
	if IsTemperatureHumiditySensorRelevant(channelWith(string(hmenum.ParameterActualTemperature))) {
		t.Fatal("missing humidity must reject")
	}
	if !IsTemperatureHumiditySensorRelevant(channelWith(
		string(hmenum.ParameterActualTemperature),
		string(hmenum.ParameterHumidity),
	)) {
		t.Fatal("temperature + humidity must be relevant")
	}
	if !IsDewPointRelevant(channelWith(
		string(hmenum.ParameterActualTemperature),
		string(hmenum.ParameterHumidity),
	)) {
		t.Fatal("DewPoint relevance must mirror temperature+humidity")
	}
}

func TestRelevanceApparentTemperature(t *testing.T) {
	noWind := channelWith(
		string(hmenum.ParameterActualTemperature),
		string(hmenum.ParameterHumidity),
	)
	if IsApparentTemperatureRelevant(noWind, "HmIP-SWO") {
		t.Fatal("ApparentTemperature requires WIND_SPEED")
	}
	full := channelWith(
		string(hmenum.ParameterActualTemperature),
		string(hmenum.ParameterHumidity),
		"WIND_SPEED",
	)
	if !IsApparentTemperatureRelevant(full, "HmIP-SWO") {
		t.Fatal("ApparentTemperature with all three sources must be relevant")
	}
	if IsApparentTemperatureRelevant(full, "HmIP-STHO") {
		t.Fatal("ApparentTemperature must reject non-whitelisted models (HmIP-STHO)")
	}
	if IsApparentTemperatureRelevant(full, "") {
		t.Fatal("ApparentTemperature with empty model must reject")
	}
}

func TestRelevanceApparentTemperatureRequiresExactParams(t *testing.T) {
	// V2-04: ApparentTemperature requires ACTUAL_TEMPERATURE (not the
	// TEMPERATURE fallback), HUMIDITY (not ACTUAL_HUMIDITY), and WIND_SPEED.
	// Using the fallback TEMPERATURE instead of ACTUAL_TEMPERATURE must not
	// satisfy the requirement.
	withFallbackTemp := channelWith(
		string(hmenum.ParameterTemperature), // fallback — not accepted
		string(hmenum.ParameterHumidity),
		string(hmenum.ParameterWindSpeed),
	)
	if IsApparentTemperatureRelevant(withFallbackTemp, "HmIP-SWO") {
		t.Fatal("ApparentTemperature must reject TEMPERATURE fallback — ACTUAL_TEMPERATURE required")
	}
	// ACTUAL_HUMIDITY fallback also not accepted.
	withFallbackHum := channelWith(
		string(hmenum.ParameterActualTemperature),
		string(hmenum.ParameterActualHumidity), // fallback — not accepted
		string(hmenum.ParameterWindSpeed),
	)
	if IsApparentTemperatureRelevant(withFallbackHum, "HmIP-SWO") {
		t.Fatal("ApparentTemperature must reject ACTUAL_HUMIDITY fallback — HUMIDITY required")
	}
}

func TestRelevanceFrostPoint(t *testing.T) {
	ch := channelWith(
		string(hmenum.ParameterActualTemperature),
		string(hmenum.ParameterHumidity),
	)
	if !IsFrostPointRelevant(ch, "HmIP-STHO") {
		t.Fatal("FrostPoint must accept HmIP-STHO")
	}
	if !IsFrostPointRelevant(ch, "HmIP-SWO") {
		t.Fatal("FrostPoint must accept HmIP-SWO")
	}
	if IsFrostPointRelevant(ch, "HmIP-eTRV") {
		t.Fatal("FrostPoint must reject HmIP-eTRV")
	}
	if IsFrostPointRelevant(ch, "") {
		t.Fatal("FrostPoint with empty model must reject")
	}
}

func TestRelevanceEnthalpyToleratesMissingPressure(t *testing.T) {
	ch := channelWith(
		string(hmenum.ParameterActualTemperature),
		string(hmenum.ParameterHumidity),
	)
	if !IsEnthalpyRelevant(ch) {
		t.Fatal("Enthalpy must be relevant without pressure (default applies)")
	}
}

func TestIsOperatingVoltageLevelRelevantNilChannel(t *testing.T) {
	if IsOperatingVoltageLevelRelevant(nil, "HM-CC-RT-DN") {
		t.Fatal("nil channel must return false")
	}
}

func TestIsOperatingVoltageLevelRelevantUnknownModel(t *testing.T) {
	ch := channelWith("OPERATING_VOLTAGE")
	if IsOperatingVoltageLevelRelevant(ch, "UNKNOWN") {
		t.Fatal("unknown model must return false")
	}
}

func TestIsOperatingVoltageLevelRelevantMissingParameter(t *testing.T) {
	ch := channelWith("SOMETHING_ELSE")
	if IsOperatingVoltageLevelRelevant(ch, "HM-CC-RT-DN") {
		t.Fatal("missing parameter must return false")
	}
}

func TestIsOperatingVoltageLevelRelevantOperatingVoltage(t *testing.T) {
	// Minimal stub (no voltageChannelInspector): falls back to VALUES-only check.
	ch := channelWith("OPERATING_VOLTAGE")
	if !IsOperatingVoltageLevelRelevant(ch, "HM-CC-RT-DN") {
		t.Fatal("model with OPERATING_VOLTAGE must be relevant (fallback path)")
	}
}

func TestIsOperatingVoltageLevelRelevantBatteryState(t *testing.T) {
	// Minimal stub (no voltageChannelInspector): falls back to VALUES-only check.
	ch := channelWith("BATTERY_STATE")
	if !IsOperatingVoltageLevelRelevant(ch, "HM-CC-RT-DN") {
		t.Fatal("model with BATTERY_STATE must be relevant (fallback path)")
	}
}

func TestIsOperatingVoltageLevelRelevantRequiresLowBatLimit(t *testing.T) {
	// V2-05: when voltageChannelInspector is implemented, LOW_BAT_LIMIT in
	// MASTER is required alongside OPERATING_VOLTAGE.
	withLowBat := channelWithMaster(
		[]string{"OPERATING_VOLTAGE"},
		[]string{"LOW_BAT_LIMIT"},
		nil,
	)
	if !IsOperatingVoltageLevelRelevant(withLowBat, "HM-CC-RT-DN") {
		t.Fatal("OPERATING_VOLTAGE + LOW_BAT_LIMIT-MASTER must be relevant")
	}

	withoutLowBat := channelWithMaster(
		[]string{"OPERATING_VOLTAGE"},
		nil,
		nil,
	)
	if IsOperatingVoltageLevelRelevant(withoutLowBat, "HM-CC-RT-DN") {
		t.Fatal("OPERATING_VOLTAGE without LOW_BAT_LIMIT-MASTER must not be relevant")
	}
}

func TestIsOperatingVoltageLevelRelevantBatteryStateRequiresDeviceLowBatLimit(t *testing.T) {
	// V2-05: BATTERY_STATE path requires LOW_BAT_LIMIT on the device-root
	// channel (HasDeviceMasterParameter), not the channel itself.
	withDeviceLowBat := channelWithMaster(
		[]string{"BATTERY_STATE"},
		nil,                       // no channel-level MASTER LOW_BAT_LIMIT
		[]string{"LOW_BAT_LIMIT"}, // device-root MASTER LOW_BAT_LIMIT present
	)
	if !IsOperatingVoltageLevelRelevant(withDeviceLowBat, "HM-CC-RT-DN") {
		t.Fatal("BATTERY_STATE + device-root LOW_BAT_LIMIT-MASTER must be relevant")
	}

	withoutDeviceLowBat := channelWithMaster(
		[]string{"BATTERY_STATE"},
		nil,
		nil, // no device-root LOW_BAT_LIMIT
	)
	if IsOperatingVoltageLevelRelevant(withoutDeviceLowBat, "HM-CC-RT-DN") {
		t.Fatal("BATTERY_STATE without device-root LOW_BAT_LIMIT-MASTER must not be relevant")
	}
}

func TestIsDewPointSpreadRelevant(t *testing.T) {
	if IsDewPointSpreadRelevant(nil) {
		t.Fatal("nil channel must return false")
	}
	ch := channelWith(
		string(hmenum.ParameterActualTemperature),
		string(hmenum.ParameterHumidity),
	)
	if !IsDewPointSpreadRelevant(ch) {
		t.Fatal("temp+humidity channel must be relevant for DewPointSpread")
	}
}

func TestIsVaporConcentrationRelevant(t *testing.T) {
	if IsVaporConcentrationRelevant(nil) {
		t.Fatal("nil channel must return false")
	}
	ch := channelWith(
		string(hmenum.ParameterActualTemperature),
		string(hmenum.ParameterHumidity),
	)
	if !IsVaporConcentrationRelevant(ch) {
		t.Fatal("temp+humidity channel must be relevant for VaporConcentration")
	}
}

func TestModelMatchesEmptyAllowed(t *testing.T) {
	if !modelMatches("anything", nil) {
		t.Fatal("empty allowed must match any model")
	}
	if !modelMatches("anything", []string{}) {
		t.Fatal("empty allowed slice must match any model")
	}
}

func TestModelMatchesEmptyStringInAllowed(t *testing.T) {
	result := modelMatches("HmIP-STHO", []string{"", "HmIP-STHO"})
	if !result {
		t.Fatal("should match despite empty element in allowed list")
	}
}

func TestIsApparentTemperatureRelevantNilChannel(t *testing.T) {
	if IsApparentTemperatureRelevant(nil, "HmIP-SWO") {
		t.Fatal("nil channel must return false")
	}
}

func TestIsFrostPointRelevantNilChannel(t *testing.T) {
	if IsFrostPointRelevant(nil, "HmIP-STHO") {
		t.Fatal("nil channel must return false")
	}
}
