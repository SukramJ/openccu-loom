// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// init.go registers Constructor functions for every climate
// DeviceProfile in the process-wide custom registry ( D.12).
//
// Five profiles are covered:
//
// - IPThermostat / IPThermostatGroup → KindIP
// - RfThermostat / RfThermostatGroup → KindRF
// - SimpleRfThermostat → KindSimpleRF
//
// Each constructor reads the SETPOINT field from the rebased
// ChannelGroupConfig (so the kind-specific setpoint parameter is
// resolved at registration time from the generated ProfileConfig),
// then calls [New] with the resulting [Config].
//
// The IP capabilities (SupportsAuto, SupportsHeat, SupportsOff,
// SupportsBoost, SupportsProfile, SupportsAway, temperature bounds)
// RF capabilities
// mirror RF_THERMOSTAT_CAPABILITIES. SimpleRF uses BASIC_CLIMATE_CAPABILITIES.

package climate

import (
	"sort"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/payload"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

func init() {
	reg := custom.DefaultRegistry()

	reg.MustRegisterConstructor(hmenum.DeviceProfileIPThermostat, ipThermostatConstructor)
	reg.MustRegisterConstructor(hmenum.DeviceProfileIPThermostatGroup, ipThermostatGroupConstructor)
	reg.MustRegisterConstructor(hmenum.DeviceProfileRfThermostat, rfThermostatConstructor)
	reg.MustRegisterConstructor(hmenum.DeviceProfileRfThermostatGroup, rfThermostatGroupConstructor)
	reg.MustRegisterConstructor(hmenum.DeviceProfileSimpleRfThermostat, simpleRfThermostatConstructor)

	payload.RegisterGlobalScalarArgKey("set_temperature", "temperature")
	payload.RegisterGlobalScalarArgKey("set_mode", "mode")
	payload.RegisterGlobalScalarArgKey("set_profile", "profile")
	payload.RegisterGlobalScalarArgKey("set_temperature_offset", "offset")
}

// Predefined capability presets mirror
// capabilities/climate.py — exported so north-bound adapters and tests can
// reference them by name rather than reconstructing the struct literal.

// BasicClimateCapabilities mirrors
// minimal thermostat with heat/off only, no profiles, no boost, no away.
// Mirrors capabilities/climate.py:38.
var BasicClimateCapabilities = custom.ClimateCapabilities{
	SupportsHeat:    true,
	SupportsOff:     true,
	MinTemperature:  4.5,
	MaxTemperature:  30.5,
	TemperatureStep: 0.5,
	TemperatureUnit: "°C",
}

// IPThermostatCapabilities mirrors
// full mode set, profile support, away and boost, 5–30 °C range, 0.5 °C step.
// Mirrors capabilities/climate.py:39.
var IPThermostatCapabilities = custom.ClimateCapabilities{
	SupportsAuto:    true,
	SupportsHeat:    true,
	SupportsCool:    false,
	SupportsOff:     true,
	SupportsBoost:   true,
	SupportsProfile: true,
	SupportsAway:    true,
	MinTemperature:  5.0,
	MaxTemperature:  30.0,
	TemperatureStep: 0.5,
	TemperatureUnit: "°C",
}

// RFThermostatCapabilities mirrors
// heat, away, profile support, boost, COMFORT and ECO presets, 4.5–30.5 °C.
var RFThermostatCapabilities = custom.ClimateCapabilities{
	SupportsAuto:    true,
	SupportsHeat:    true,
	SupportsCool:    false,
	SupportsOff:     true,
	SupportsBoost:   true,
	SupportsProfile: true,
	SupportsAway:    true,
	SupportsComfort: true,
	SupportsEco:     true,
	MinTemperature:  4.5,
	MaxTemperature:  30.5,
	TemperatureStep: 0.5,
	TemperatureUnit: "°C",
}

// Python-exact sentinel names — exported aliases matching
// module-level constant names for parity and north-bound adapter use.

// BASIC_CLIMATE_CAPABILITIES is the Python-parity alias for [BasicClimateCapabilities].
var BASIC_CLIMATE_CAPABILITIES = BasicClimateCapabilities //nolint:revive // Python-exact name required for parity

// IP_THERMOSTAT_CAPABILITIES is the Python-parity alias for [IPThermostatCapabilities].
var IP_THERMOSTAT_CAPABILITIES = IPThermostatCapabilities //nolint:revive // Python-exact name required for parity

// IpCapabilities mirrors
// Full mode set
// profile support, away and boost, 5–30 °C range, 0.5 °C step.
var ipCapabilities = custom.ClimateCapabilities{
	SupportsAuto:    true,
	SupportsHeat:    true,
	SupportsCool:    false,
	SupportsOff:     true,
	SupportsBoost:   true,
	SupportsProfile: true,
	SupportsAway:    true,
	MinTemperature:  5.0,
	MaxTemperature:  30.0,
	TemperatureStep: 0.5,
	TemperatureUnit: "°C",
}

// RfCapabilities mirrors
// heat and away, profile support, boost, 4.5–30.5 °C. RF thermostats
// also expose the COMFORT and ECO preset profiles which are not
// Available on IP thermostats.
// (climate.py:514) and `_PROFILES` set on `CustomDpRfThermostat`
// (climate.py:531-538).
var rfCapabilities = custom.ClimateCapabilities{
	SupportsAuto:    true,
	SupportsHeat:    true,
	SupportsCool:    false,
	SupportsOff:     true,
	SupportsBoost:   true,
	SupportsProfile: true,
	SupportsAway:    true,
	SupportsComfort: true,
	SupportsEco:     true,
	MinTemperature:  4.5,
	MaxTemperature:  30.5,
	TemperatureStep: 0.5,
	TemperatureUnit: "°C",
}

// SimpleRfCapabilities mirrors
// Simple RF thermostats (HM-CC-TC, ZEL_STG_RM_FWT) do not expose an
// Explicit OFF mode — only AUTO and MANU. Temperature
// bounds 6.0..30.0 mirror the MASTER-paramset TEMPERATUR_COMFORT_VALUE
// weekly-schedule descriptor range; the SimpleRF wire family has no
// SET_TEMPERATURE DP whose descriptor would otherwise drive these.
var simpleRfCapabilities = custom.ClimateCapabilities{
	SupportsAuto:    false,
	SupportsHeat:    true,
	SupportsCool:    false,
	SupportsOff:     false,
	SupportsBoost:   false,
	SupportsProfile: false,
	SupportsAway:    false,
	MinTemperature:  6.0,
	MaxTemperature:  30.0,
	TemperatureStep: 0.5,
	TemperatureUnit: "°C",
}

// activityStateChannels extracts the absolute channel numbers whose
// profile-mapped STATE field acts as the heating-activity source for
// an IP thermostat — e.g. channel offset 8 on the IPThermostat schema
// (HmIP-BWTH relay channel 9) or offset 3 on the heating-group schema.
// Entries under the [custom.AnyChannelOffset] sentinel apply to the
// primary channel itself and are excluded here; channels the concrete
// device does not carry are skipped at resolution time.
func activityStateChannels(group custom.RebasedChannelGroupConfig) []int {
	var out []int
	for chNo, fields := range group.ChannelFields {
		if chNo == custom.AnyChannelOffset {
			continue
		}
		if _, ok := fields[hmenum.FieldState]; ok {
			out = append(out, chNo)
		}
	}
	sort.Ints(out)
	return out
}

func ipThermostatConstructor(channel *device.Channel, group custom.RebasedChannelGroupConfig) (device.AttachableDataPoint, error) {
	return New(Config{
		Channel:               channel,
		Writer:                channel.Writer(),
		Capabilities:          ipCapabilities,
		Kind:                  KindIP,
		ActivityStateChannels: activityStateChannels(group),
	}), nil
}

func ipThermostatGroupConstructor(channel *device.Channel, group custom.RebasedChannelGroupConfig) (device.AttachableDataPoint, error) {
	return New(Config{
		Channel:               channel,
		Writer:                channel.Writer(),
		Capabilities:          ipCapabilities,
		Kind:                  KindIP,
		ActivityStateChannels: activityStateChannels(group),
	}), nil
}

func rfThermostatConstructor(channel *device.Channel, _ custom.RebasedChannelGroupConfig) (device.AttachableDataPoint, error) {
	return New(Config{
		Channel:      channel,
		Writer:       channel.Writer(),
		Capabilities: rfCapabilities,
		Kind:         KindRF,
	}), nil
}

func rfThermostatGroupConstructor(channel *device.Channel, _ custom.RebasedChannelGroupConfig) (device.AttachableDataPoint, error) {
	return New(Config{
		Channel:      channel,
		Writer:       channel.Writer(),
		Capabilities: rfCapabilities,
		Kind:         KindRF,
	}), nil
}

func simpleRfThermostatConstructor(channel *device.Channel, _ custom.RebasedChannelGroupConfig) (device.AttachableDataPoint, error) {
	return New(Config{
		Channel:      channel,
		Writer:       channel.Writer(),
		Capabilities: simpleRfCapabilities,
		Kind:         KindSimpleRF,
	}), nil
}

// Compile-time assertion: *Climate satisfies [device.AttachableDataPoint].
var _ device.AttachableDataPoint = (*Climate)(nil)
