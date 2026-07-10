// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package custom

import (
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// VALUES→MASTER fallback rationale -------------------------------- Custom-DP
// wrappers (e.g. Climate) reference parameters that may live in either the
// VALUES paramset (live state — ACTUAL_TEMPERATURE, SET_POINT_TEMPERATURE,
// HEATING_COOLING) or the MASTER paramset (configuration —
// TEMPERATURE_MINIMUM, TEMPERATURE_MAXIMUM, TEMPERATURE_OFFSET,
// OPTIMUM_START_STOP).
//
// We mirror that contract here so MASTER-only parameters are not silently
// dropped during custom-DP construction. Without the fallback, fields like
// `temperatureMinimum` on a thermostat would stay nil even when
// seedMasterValues populated the underlying DP.

// resolveDP returns the channel's data point for parameter p, trying
// VALUES first and falling back to MASTER. Returns nil for a nil
// channel or when neither paramset carries p.
func resolveDP(ch *device.Channel, p hmenum.Parameter) device.ParameterDataPoint {
	if ch == nil {
		return nil
	}
	if dp := ch.Parameter(p); dp != nil {
		return dp
	}
	return ch.MasterParameter(p)
}

// FloatField returns the channel's *generic.Float for parameter p, trying
// VALUES first then MASTER, or nil when the channel does not carry a
// Float-typed DP for p. DataPointType.FLOAT)` helper.
func FloatField(ch *device.Channel, p hmenum.Parameter) *generic.Float {
	dp, _ := resolveDP(ch, p).(*generic.Float)
	return dp
}

// IntegerField returns the channel's *generic.Integer for parameter p
// (VALUES-then-MASTER), or nil when absent / wrong type.
func IntegerField(ch *device.Channel, p hmenum.Parameter) *generic.Integer {
	dp, _ := resolveDP(ch, p).(*generic.Integer)
	return dp
}

// SwitchField returns the channel's *generic.Switch for parameter p
// (VALUES-then-MASTER), or nil when absent / wrong type.
func SwitchField(ch *device.Channel, p hmenum.Parameter) *generic.Switch {
	dp, _ := resolveDP(ch, p).(*generic.Switch)
	return dp
}

// BinarySensorField returns the channel's *generic.BinarySensor for
// parameter p (VALUES-then-MASTER), or nil when absent / wrong type.
func BinarySensorField(ch *device.Channel, p hmenum.Parameter) *generic.BinarySensor {
	dp, _ := resolveDP(ch, p).(*generic.BinarySensor)
	return dp
}

// FloatSensorField returns the channel's *generic.Sensor[float64] for
// parameter p (VALUES-then-MASTER), or nil when absent / wrong type.
func FloatSensorField(ch *device.Channel, p hmenum.Parameter) *generic.Sensor[float64] {
	dp, _ := resolveDP(ch, p).(*generic.Sensor[float64])
	return dp
}

// IntegerSensorField returns the channel's *generic.Sensor[int32] for
// parameter p (VALUES-then-MASTER), or nil when absent / wrong type.
func IntegerSensorField(ch *device.Channel, p hmenum.Parameter) *generic.Sensor[int32] {
	dp, _ := resolveDP(ch, p).(*generic.Sensor[int32])
	return dp
}

// StringSensorField returns the channel's *generic.Sensor[string] for
// parameter p (VALUES-then-MASTER), or nil when absent / wrong type.
//
// Only genuine STRING-typed parameters resolve to a *generic.Sensor[string].
// A read-only ENUM parameter resolves to *generic.Sensor[int32] (raw 0-based
// index) and a writable ENUM to *generic.Select, so StringSensorField returns
// nil for either — a custom DP that needs an ENUM's string label must read the
// index sensor via [EnumSensorField] + [EnumLabelValue] instead, not this.
func StringSensorField(ch *device.Channel, p hmenum.Parameter) *generic.Sensor[string] {
	dp, _ := resolveDP(ch, p).(*generic.Sensor[string])
	return dp
}

// EnumSensorField returns the channel's *generic.Sensor[int32] for a read-only
// ENUM (or INTEGER) parameter p (VALUES-then-MASTER), or nil when absent /
// wrong type. It is the correct field accessor for read-only ENUM status
// parameters (LOCK_STATE, SMOKE_DETECTOR_ALARM_STATUS, DOOR_STATE, DIRECTION,
// …): the resolver projects those onto Sensor[int32] carrying the raw index,
// and the string label is resolved on demand via [EnumLabelValue]. Custom DPs
// previously read these through [StringSensorField], whose *Sensor[string] cast
// never matched an int32 sensor and silently resolved to nil — the projected
// value stayed absent and the Matter attribute reported TLV-null forever.
func EnumSensorField(ch *device.Channel, p hmenum.Parameter) *generic.Sensor[int32] {
	return IntegerSensorField(ch, p)
}

// EnumLabelValue resolves an index-valued ENUM sensor's current value to its
// VALUE_LIST label, mirroring [generic.Select.Label]. Returns ("", false) for a
// nil sensor, an unobserved value, or an index outside the VALUE_LIST.
func EnumLabelValue(dp *generic.Sensor[int32]) (string, bool) {
	if dp == nil {
		return "", false
	}
	v, ok := dp.Value()
	if !ok {
		return "", false
	}
	vl := dp.ParameterData().ValueList
	if int(v) < 0 || int(v) >= len(vl) {
		return "", false
	}
	return vl[v], true
}

// EnumLabelIndex is the inverse of [EnumLabelValue]: it resolves a VALUE_LIST
// label back to its 0-based index for an optimistic-echo write onto an
// index-valued ENUM sensor. Returns (-1, false) when the label is not in the
// sensor's VALUE_LIST.
func EnumLabelIndex(dp *generic.Sensor[int32], label string) (int32, bool) {
	if dp == nil {
		return -1, false
	}
	for i, v := range dp.ParameterData().ValueList {
		if v == label {
			return int32(i), true //nolint:gosec // VALUE_LIST length is small; index fits int32
		}
	}
	return -1, false
}

// SelectField returns the channel's *generic.Select for parameter p
// (VALUES-then-MASTER), or nil when absent / wrong type.
func SelectField(ch *device.Channel, p hmenum.Parameter) *generic.Select {
	dp, _ := resolveDP(ch, p).(*generic.Select)
	return dp
}

// ActionSelectField returns the channel's *generic.ActionSelect for parameter
// p (VALUES-then-MASTER), or nil when absent / wrong type.
func ActionSelectField(ch *device.Channel, p hmenum.Parameter) *generic.ActionSelect {
	dp, _ := resolveDP(ch, p).(*generic.ActionSelect)
	return dp
}
