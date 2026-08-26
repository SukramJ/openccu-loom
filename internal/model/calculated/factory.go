// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package calculated

import (
	"github.com/SukramJ/openccu-loom/internal/model/device"
)

// CreateCalculatedDataPoints walks the calculated-DP catalogue and
// instantiates every sensor whose `Is…Relevant` predicate matches the channel
// + device model. Each match is attached to the channel via
// [device.Channel.AttachCalculatedDataPoint], which in turn invokes the
// sensor's `Subscribe` to wire its source parameters.
//
// Returns the attached sensors in attach order so callers (the device
// pipeline) can register downstream listeners (event-bus bridge, MQTT
// publisher) on them without having to enumerate the channel state.
//
// The function is idempotent on a channel that already carries the derived
// sensors — `AttachCalculatedDataPoint` replaces an entry with the same key
// and re-runs `Subscribe`.
//
// `model` is the parent device's CCU model name; pass `dev.Model`. Empty
// `model` skips device-model-gated sensors (frost-point,
// apparent-temperature, operating-voltage-level) but still creates the
// temperature/humidity-gated ones (dew-point, dew-point-spread, enthalpy,
// vapor-concentration) for compatibility with test fixtures that don't carry
// a model string.
func CreateCalculatedDataPoints(ch *device.Channel, model string) []Sensor {
	if ch == nil {
		return nil
	}
	centralName := ch.CentralName()
	channelAddr := ch.Address

	var out []Sensor

	// Climate-derived: temperature + humidity always required.
	if IsTemperatureHumiditySensorRelevant(ch) {
		out = append(
			out,
			NewDewPointSensorWithIdentity(centralName, channelAddr),
			NewDewPointSpreadSensorWithIdentity(centralName, channelAddr),
			NewVaporConcentrationSensorWithIdentity(centralName, channelAddr),
			NewEnthalpySensorWithIdentity(centralName, channelAddr),
		)
	}

	// FrostPoint: temperature + humidity + model whitelist (HmIP-STHO,
	// HmIP-SWO).
	if IsFrostPointRelevant(ch, model) {
		out = append(out, NewFrostPointSensorWithIdentity(centralName, channelAddr))
	}

	// ApparentTemperature: temperature + humidity + wind speed + model
	// whitelist (HmIP-SWO).
	if IsApparentTemperatureRelevant(ch, model) {
		out = append(out, NewApparentTemperatureSensorWithIdentity(centralName, channelAddr))
	}

	// OperatingVoltageLevel: per-model battery table + OPERATING_VOLTAGE
	// (or BATTERY_STATE) on the channel.
	if IsOperatingVoltageLevelRelevant(ch, model) {
		out = append(out, NewOperatingVoltageLevelSensorWithIdentity(centralName, channelAddr))
	}

	// DerivedBinary mappings: per-model registry filtered by channel number. A
	// mapping with SourceChannelNo == SourceChannelNoOpen applies to every
	// channel exposing SourceParameter; otherwise the channel number must match
	// exactly.
	for _, m := range LookupDerivedBinaryMappings(model) {
		if !m.AppliesToChannel(ch.Number) {
			continue
		}
		if !ch.HasParameter(string(m.SourceParameter)) {
			continue
		}
		out = append(out, NewDerivedBinarySensorWithIdentity(
			centralName, channelAddr,
			m.CalculatedParameter, m.SourceParameter,
			m.OnValues, m.OffValues,
		))
	}

	for _, s := range out {
		// AttachCalculatedDataPoint runs Subscribe under the channel
		// lock and stores the unsubscribe closure for re-attach safety.
		if att, ok := s.(device.AttachableDataPoint); ok {
			ch.AttachCalculatedDataPoint(att)
		}
	}
	return out
}
