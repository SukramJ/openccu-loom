// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package mqtt

// hubRulesMaps is a compile-time anchor that prevents the hub entity-description
// maps from being flagged as unused while they await their consumer in the hub
// Discovery builder (next milestone). The maps mirror the Python reference
// implementation's hub.py and must not be silently dropped.
//
//nolint:unused // anchor — hub discovery consumer pending; maps must not drop
var _ = [5]map[string]EntityDescription{
	hubButtonRulesByName,
	hubSensorRulesByName,
	hubSysvarSensorRulesByName,
	hubMetricSensorRulesByName,
	hubBinarySensorRulesByName,
}

// Hub-entity description rules ported from
// hub.py.
//
// The Python source uses EntityDescriptionRule with a `var_name_contains`
// field for substring-matching on the sysvar/hub-entity name. The maps
// below preserve those match strings as keys. Each map covers exactly one
// DataPointCategory bucket from the Python source:
//
//   HUB_BUTTON         → hubButtonRulesByName       (2 entries)
//   HUB_SENSOR / plain → hubSensorRulesByName       (9 entries)
//   HUB_SENSOR / sysvar→ hubSysvarSensorRulesByName (8 entries)
//   HUB_SENSOR / metric→ hubMetricSensorRulesByName (3 entries)
//   HUB_BINARY_SENSOR  → hubBinarySensorRulesByName (1 entry)
//
// All unit-of-measurement string constants follow Home Assistant literals
// (mirroring Python's UnitOfEnergy / UnitOfLength / UnitOfTime imports).

// hubButtonRulesByName holds EntityDescription overrides for
// hub button data points. Key = var_name_contains value from Python.
// Mirrors hub.py lines 26–41 (INSTALL_MODE_HMIP_BUTTON / INSTALL_MODE_BIDCOS_BUTTON).
//
// Buttons have no device_class, state_class, or unit; entity_category is
// absent (primary action). EnabledByDefault follows the Python factory
// default for HmButtonEntityDescription (enabled_default not set → true).
var hubButtonRulesByName = map[string]EntityDescription{
	"INSTALL_MODE_HMIP_BUTTON": {
		Key:                       "INSTALL_MODE_HMIP_BUTTON",
		EnabledByDefault:          true,
		SuggestedDisplayPrecision: -1,
	},
	"INSTALL_MODE_BIDCOS_BUTTON": {
		Key:                       "INSTALL_MODE_BIDCOS_BUTTON",
		EnabledByDefault:          true,
		SuggestedDisplayPrecision: -1,
	},
}

// hubSensorRulesByName holds EntityDescription overrides for
// plain HUB_SENSOR data points whose names contain one of these substrings.
// Mirrors hub.py lines 43–87 (ALARM_MESSAGES, SERVICE_MESSAGES,
// INSTALL_MODE_HMIP, INSTALL_MODE_BIDCOS, INBOX_SENSOR_NAME="inbox").
var hubSensorRulesByName = map[string]EntityDescription{
	"ALARM_MESSAGES": {
		Key:                       "ALARM_MESSAGES",
		StateClass:                "measurement",
		EnabledByDefault:          true,
		SuggestedDisplayPrecision: -1,
	},
	"SERVICE_MESSAGES": {
		Key:                       "SERVICE_MESSAGES",
		StateClass:                "measurement",
		EnabledByDefault:          true,
		SuggestedDisplayPrecision: -1,
	},
	"INSTALL_MODE_HMIP": {
		Key:                       "INSTALL_MODE_HMIP",
		EnabledByDefault:          true,
		SuggestedDisplayPrecision: -1,
	},
	"INSTALL_MODE_BIDCOS": {
		Key:                       "INSTALL_MODE_BIDCOS",
		EnabledByDefault:          true,
		SuggestedDisplayPrecision: -1,
	},
	// INBOX_SENSOR_NAME = "inbox".
	"inbox": {
		Key:                       "INBOX",
		EnabledByDefault:          true,
		SuggestedDisplayPrecision: -1,
	},
}

// hubSysvarSensorRulesByName holds EntityDescription overrides for
// sysvar-backed HUB_SENSOR data points whose variable names contain one of
// these substrings. Mirrors hub.py lines 97–183.
//
// Units mirror Home Assistant literals:
//   - Wh  = UnitOfEnergy.WATT_HOUR
//   - mm  = UnitOfLength.MILLIMETERS
//   - min = UnitOfTime.MINUTES
var hubSysvarSensorRulesByName = map[string]EntityDescription{
	// Energy counter (total_increasing, Wh)
	// Note: svEnergyCounterFeedIn must be matched before svEnergyCounter
	// (longer substring first) when doing contains-checks at runtime.
	"svEnergyCounterFeedIn": {
		Key:                       "ENERGY_COUNTER_FEED_IN",
		DeviceClass:               "energy",
		UnitOfMeasurement:         "Wh",
		StateClass:                "total_increasing",
		EnabledByDefault:          true,
		SuggestedDisplayPrecision: -1,
	},
	"svEnergyCounter": {
		Key:                       "ENERGY_COUNTER",
		DeviceClass:               "energy",
		UnitOfMeasurement:         "Wh",
		StateClass:                "total_increasing",
		EnabledByDefault:          true,
		SuggestedDisplayPrecision: -1,
	},
	// Rain counter (total_increasing, mm)
	// Order matters for contains-matching: longer keys first.
	"svHmIPRainCounterToday": {
		Key:                       "RAIN_COUNTER_TODAY",
		UnitOfMeasurement:         "mm",
		StateClass:                "total_increasing",
		EnabledByDefault:          true,
		SuggestedDisplayPrecision: -1,
	},
	"svHmIPRainCounterYesterday": {
		Key:                       "RAIN_COUNTER_YESTERDAY",
		UnitOfMeasurement:         "mm",
		StateClass:                "total_increasing",
		EnabledByDefault:          true,
		SuggestedDisplayPrecision: -1,
	},
	"svHmIPRainCounter": {
		Key:                       "RAIN_COUNTER",
		UnitOfMeasurement:         "mm",
		StateClass:                "total_increasing",
		EnabledByDefault:          true,
		SuggestedDisplayPrecision: -1,
	},
	// Sunshine counter (total_increasing, min, device_class=duration)
	"svHmIPSunshineCounterToday": {
		Key:                       "SUNSHINE_COUNTER_TODAY",
		DeviceClass:               "duration",
		UnitOfMeasurement:         "min",
		StateClass:                "total_increasing",
		EnabledByDefault:          true,
		SuggestedDisplayPrecision: -1,
	},
	"svHmIPSunshineCounterYesterday": {
		Key:                       "SUNSHINE_COUNTER_YESTERDAY",
		DeviceClass:               "duration",
		UnitOfMeasurement:         "min",
		StateClass:                "total_increasing",
		EnabledByDefault:          true,
		SuggestedDisplayPrecision: -1,
	},
	"svHmIPSunshineCounter": {
		Key:                       "SUNSHINE_COUNTER",
		DeviceClass:               "duration",
		UnitOfMeasurement:         "min",
		StateClass:                "total_increasing",
		EnabledByDefault:          true,
		SuggestedDisplayPrecision: -1,
	},
}

// hubMetricSensorRulesByName holds EntityDescription overrides for
// the three openccu-loom-internal metrics sensors exposed as HUB_SENSOR
// data points. Key = var_name_contains / METRICS_SENSOR_*_NAME constant:
//
//	METRICS_SENSOR_SYSTEM_HEALTH_NAME      = "system_health"
//	METRICS_SENSOR_CONNECTION_LATENCY_NAME = "connection_latency"
//	METRICS_SENSOR_LAST_EVENT_AGE_NAME     = "last_event_age"
//
// All three use the diagnostic_sensor factory in Python (entity_category=
// DIAGNOSTIC, state_class=MEASUREMENT, enabled_default=True because
// enabled_default is passed explicitly as True). Mirrors hub.py lines
// 185–222.
var hubMetricSensorRulesByName = map[string]EntityDescription{
	// system_health — percentage, no device_class
	"system_health": {
		Key:                       "SYSTEM_HEALTH",
		EntityCategory:            EntityCategoryDiagnostic,
		Icon:                      "mdi:heart-pulse",
		StateClass:                "measurement",
		EnabledByDefault:          true,
		SuggestedDisplayPrecision: 1,
	},
	// connection_latency — duration in milliseconds
	"connection_latency": {
		Key:                       "CONNECTION_LATENCY",
		DeviceClass:               "duration",
		UnitOfMeasurement:         "ms",
		EntityCategory:            EntityCategoryDiagnostic,
		Icon:                      "mdi:timer-outline",
		StateClass:                "measurement",
		EnabledByDefault:          true,
		SuggestedDisplayPrecision: 1,
	},
	// last_event_age — duration in seconds
	"last_event_age": {
		Key:                       "LAST_EVENT_AGE",
		DeviceClass:               "duration",
		UnitOfMeasurement:         "s",
		EntityCategory:            EntityCategoryDiagnostic,
		Icon:                      "mdi:clock-alert-outline",
		StateClass:                "measurement",
		EnabledByDefault:          true,
		SuggestedDisplayPrecision: 1,
	},
}

// hubBinarySensorRulesByName holds EntityDescription overrides for
// hub binary-sensor data points. Key = var_name_contains value.
//
// CONNECTIVITY_SENSOR_PREFIX = "Connectivity".
// Mirrors hub.py lines 88–95.
var hubBinarySensorRulesByName = map[string]EntityDescription{
	"Connectivity": {
		Key:                       "CONNECTIVITY_SENSOR",
		DeviceClass:               "connectivity",
		EnabledByDefault:          true,
		SuggestedDisplayPrecision: -1,
	},
}
