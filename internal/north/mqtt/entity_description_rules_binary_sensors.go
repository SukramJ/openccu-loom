// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package mqtt

// binarySensorRulesByDeviceAndParam contains per-(device-prefix,
// parameter) entity-description overrides ported from
// binary_sensors.py
// (BINARY_SENSOR_RULES entries that carry a devices= tuple).
//
// Lookup follows the same prefix-matching semantics used by
// numberDescriptionsByDeviceAndParam: exact hit first, then
// hasModelPrefix walk. SuggestedDisplayPrecision is -1 for all
// binary-sensor entries (no decimal rendering).
var binarySensorRulesByDeviceAndParam = map[devParam]EntityDescription{
	// HmIP-DLP — door sensor (magnetic contact), STATE → door
	{"HmIP-DLP", "STATE"}: {
		Key:                       "STATE",
		DeviceClass:               "door",
		EnabledByDefault:          true,
		SuggestedDisplayPrecision: -1,
	},

	// HmIP-DSD-PCB — occupancy detector, STATE → occupancy
	{"HmIP-DSD-PCB", "STATE"}: {
		Key:                       "STATE",
		DeviceClass:               "occupancy",
		EnabledByDefault:          true,
		SuggestedDisplayPrecision: -1,
	},

	// HmIP-SCI — contact sensor, STATE → opening
	{"HmIP-SCI", "STATE"}: {
		Key:                       "STATE",
		DeviceClass:               "opening",
		EnabledByDefault:          true,
		SuggestedDisplayPrecision: -1,
	},

	// HmIP-FCI1 — multi-channel contact input, STATE → opening
	{"HmIP-FCI1", "STATE"}: {
		Key:                       "STATE",
		DeviceClass:               "opening",
		EnabledByDefault:          true,
		SuggestedDisplayPrecision: -1,
	},

	// HmIP-FCI6 — 6-channel contact input, STATE → opening
	{"HmIP-FCI6", "STATE"}: {
		Key:                       "STATE",
		DeviceClass:               "opening",
		EnabledByDefault:          true,
		SuggestedDisplayPrecision: -1,
	},

	// HM-Sec-SD — smoke detector, STATE → smoke
	{"HM-Sec-SD", "STATE"}: {
		Key:                       "STATE",
		DeviceClass:               "smoke",
		EnabledByDefault:          true,
		SuggestedDisplayPrecision: -1,
	},

	// Window/door contact sensors — STATE → window
	{"HmIP-SWD", "STATE"}: {
		Key:                       "STATE",
		DeviceClass:               "window",
		EnabledByDefault:          true,
		SuggestedDisplayPrecision: -1,
	},
	{"HmIP-SWDO", "STATE"}: {
		Key:                       "STATE",
		DeviceClass:               "window",
		EnabledByDefault:          true,
		SuggestedDisplayPrecision: -1,
	},
	{"HmIP-SWDM", "STATE"}: {
		Key:                       "STATE",
		DeviceClass:               "window",
		EnabledByDefault:          true,
		SuggestedDisplayPrecision: -1,
	},
	{"HM-Sec-SC", "STATE"}: {
		Key:                       "STATE",
		DeviceClass:               "window",
		EnabledByDefault:          true,
		SuggestedDisplayPrecision: -1,
	},
	{"HM-SCI-3-FM", "STATE"}: {
		Key:                       "STATE",
		DeviceClass:               "window",
		EnabledByDefault:          true,
		SuggestedDisplayPrecision: -1,
	},
	// "ZEL STG RM FFK" contains spaces — kept as-is; Go map keys may
	// contain spaces.
	{"ZEL STG RM FFK", "STATE"}: {
		Key:                       "STATE",
		DeviceClass:               "window",
		EnabledByDefault:          true,
		SuggestedDisplayPrecision: -1,
	},

	// HM-Sen-RD-O — rain detector, STATE → moisture
	{"HM-Sen-RD-O", "STATE"}: {
		Key:                       "STATE",
		DeviceClass:               "moisture",
		EnabledByDefault:          true,
		SuggestedDisplayPrecision: -1,
	},

	// HM-Sec-Win — working/motion flag; disabled by default (diagnostic)
	{"HM-Sec-Win", "WORKING"}: {
		Key:                       "WORKING",
		DeviceClass:               "running",
		EnabledByDefault:          false,
		SuggestedDisplayPrecision: -1,
	},

	// HmIP-SRH — rotary handle, WINDOW_OPEN → window
	{"HmIP-SRH", "WINDOW_OPEN"}: {
		Key:                       "WINDOW_OPEN",
		DeviceClass:               "window",
		EnabledByDefault:          true,
		SuggestedDisplayPrecision: -1,
	},

	// HM-Sec-RHS — rotary handle, WINDOW_OPEN → window
	{"HM-Sec-RHS", "WINDOW_OPEN"}: {
		Key:                       "WINDOW_OPEN",
		DeviceClass:               "window",
		EnabledByDefault:          true,
		SuggestedDisplayPrecision: -1,
	},

	// HmIP-SWSD — combined smoke/intrusion detector
	// SMOKE_ALARM → smoke
	{"HmIP-SWSD", "SMOKE_ALARM"}: {
		Key:                       "SMOKE_ALARM",
		DeviceClass:               "smoke",
		EnabledByDefault:          true,
		SuggestedDisplayPrecision: -1,
	},
	// INTRUSION_ALARM → safety
	{"HmIP-SWSD", "INTRUSION_ALARM"}: {
		Key:                       "INTRUSION_ALARM",
		DeviceClass:               "safety",
		EnabledByDefault:          true,
		SuggestedDisplayPrecision: -1,
	},
}

// binarySensorRulesByParam contains generic per-parameter
// entity-description overrides ported from
// binary_sensors.py
// (BINARY_SENSOR_RULES entries without a devices= tuple).
//
// Parameters that already appear in binarySensorDescriptionsByParameter
// (entity_descriptions.go) are included here as well so this table is
// self-contained for external parity; the caller decides which table
// takes precedence. Tuple parameter lists expand to one entry per key.
var binarySensorRulesByParam = map[string]EntityDescription{
	// Safety / alarm sensors
	"ALARMSTATE": {
		Key:                       "ALARMSTATE",
		DeviceClass:               "safety",
		EnabledByDefault:          true,
		SuggestedDisplayPrecision: -1,
	},
	"ACOUSTIC_ALARM_ACTIVE": {
		Key:                       "ACOUSTIC_ALARM_ACTIVE",
		DeviceClass:               "safety",
		EnabledByDefault:          true,
		SuggestedDisplayPrecision: -1,
	},
	"OPTICAL_ALARM_ACTIVE": {
		Key:                       "OPTICAL_ALARM_ACTIVE",
		DeviceClass:               "safety",
		EnabledByDefault:          true,
		SuggestedDisplayPrecision: -1,
	},
	// EMERGENCY_OPERATION: safety, disabled
	"EMERGENCY_OPERATION": {
		Key:                       "EMERGENCY_OPERATION",
		DeviceClass:               "safety",
		EnabledByDefault:          false,
		SuggestedDisplayPrecision: -1,
	},

	// Problem sensors (diagnostic)
	"BLOCKED_PERMANENT": {
		Key:                       "BLOCKED",
		DeviceClass:               "problem",
		EntityCategory:            EntityCategoryDiagnostic,
		EnabledByDefault:          false,
		SuggestedDisplayPrecision: -1,
	},
	"BLOCKED_TEMPORARY": {
		Key:                       "BLOCKED",
		DeviceClass:               "problem",
		EntityCategory:            EntityCategoryDiagnostic,
		EnabledByDefault:          false,
		SuggestedDisplayPrecision: -1,
	},
	"BURST_LIMIT_WARNING": {
		Key:                       "BURST_LIMIT_WARNING",
		DeviceClass:               "problem",
		EntityCategory:            EntityCategoryDiagnostic,
		EnabledByDefault:          false,
		SuggestedDisplayPrecision: -1,
	},
	// DUTYCYCLE and DUTY_CYCLE share a key; diagnostic with icon
	"DUTYCYCLE": {
		Key:                       "DUTY_CYCLE",
		DeviceClass:               "problem",
		EntityCategory:            EntityCategoryDiagnostic,
		Icon:                      "mdi:radio-tower",
		EnabledByDefault:          false,
		SuggestedDisplayPrecision: -1,
	},
	"DUTY_CYCLE": {
		Key:                       "DUTY_CYCLE",
		DeviceClass:               "problem",
		EntityCategory:            EntityCategoryDiagnostic,
		Icon:                      "mdi:radio-tower",
		EnabledByDefault:          false,
		SuggestedDisplayPrecision: -1,
	},
	// DEW_POINT_ALARM: problem, disabled
	"DEW_POINT_ALARM": {
		Key:                       "DEW_POINT_ALARM",
		DeviceClass:               "problem",
		EnabledByDefault:          false,
		SuggestedDisplayPrecision: -1,
	},
	// ERROR_JAMMED: problem, disabled
	"ERROR_JAMMED": {
		Key:                       "ERROR_JAMMED",
		DeviceClass:               "problem",
		EnabledByDefault:          false,
		SuggestedDisplayPrecision: -1,
	},

	// Battery (diagnostic, enabled — battery state is important)
	"LOWBAT": {
		Key:                       "LOW_BAT",
		DeviceClass:               "battery",
		EntityCategory:            EntityCategoryDiagnostic,
		EnabledByDefault:          true,
		SuggestedDisplayPrecision: -1,
	},
	"LOW_BAT": {
		Key:                       "LOW_BAT",
		DeviceClass:               "battery",
		EntityCategory:            EntityCategoryDiagnostic,
		EnabledByDefault:          true,
		SuggestedDisplayPrecision: -1,
	},
	"LOWBAT_SENSOR": {
		Key:                       "LOW_BAT",
		DeviceClass:               "battery",
		EntityCategory:            EntityCategoryDiagnostic,
		EnabledByDefault:          true,
		SuggestedDisplayPrecision: -1,
	},

	// Heat
	"HEATER_STATE": {
		Key:                       "HEATER_STATE",
		DeviceClass:               "heat",
		EnabledByDefault:          true,
		SuggestedDisplayPrecision: -1,
	},

	// Moisture
	"MOISTURE_DETECTED": {
		Key:                       "MOISTURE_DETECTED",
		DeviceClass:               "moisture",
		EnabledByDefault:          true,
		SuggestedDisplayPrecision: -1,
	},
	"RAINING": {
		Key:                       "RAINING",
		DeviceClass:               "moisture",
		EnabledByDefault:          true,
		SuggestedDisplayPrecision: -1,
	},
	"WATERLEVEL_DETECTED": {
		Key:                       "WATERLEVEL_DETECTED",
		DeviceClass:               "moisture",
		EnabledByDefault:          true,
		SuggestedDisplayPrecision: -1,
	},

	// Motion
	"MOTION": {
		Key:                       "MOTION",
		DeviceClass:               "motion",
		EnabledByDefault:          true,
		SuggestedDisplayPrecision: -1,
	},

	// Presence
	"PRESENCE_DETECTION_STATE": {
		Key:                       "PRESENCE_DETECTION_STATE",
		DeviceClass:               "presence",
		EnabledByDefault:          true,
		SuggestedDisplayPrecision: -1,
	},

	// Power
	"POWER_MAINS_FAILURE": {
		Key:                       "POWER_MAINS_FAILURE",
		DeviceClass:               "power",
		EnabledByDefault:          true,
		SuggestedDisplayPrecision: -1,
	},

	// Running / process — PROCESS and WORKING share a key
	"PROCESS": {
		Key:                       "PROCESS",
		DeviceClass:               "running",
		EnabledByDefault:          true,
		SuggestedDisplayPrecision: -1,
	},
	// Note: generic WORKING (no devices=) also maps to "running".
	// The device-specific HM-Sec-Win/WORKING override in
	// binarySensorRulesByDeviceAndParam takes priority for
	// that device and sets enabled_default=false.
	"WORKING": {
		Key:                       "PROCESS",
		DeviceClass:               "running",
		EnabledByDefault:          true,
		SuggestedDisplayPrecision: -1,
	},

	// Tamper / sabotage (diagnostic)
	"SABOTAGE": {
		Key:                       "SABOTAGE",
		DeviceClass:               "tamper",
		EntityCategory:            EntityCategoryDiagnostic,
		EnabledByDefault:          false,
		SuggestedDisplayPrecision: -1,
	},
	"SABOTAGE_STICKY": {
		Key:                       "SABOTAGE",
		DeviceClass:               "tamper",
		EntityCategory:            EntityCategoryDiagnostic,
		EnabledByDefault:          false,
		SuggestedDisplayPrecision: -1,
	},
	"SABOTAGE_ACCELERATION": {
		Key:                       "SABOTAGE",
		DeviceClass:               "tamper",
		EntityCategory:            EntityCategoryDiagnostic,
		EnabledByDefault:          false,
		SuggestedDisplayPrecision: -1,
	},
	"SABOTAGE_BATTERY": {
		Key:                       "SABOTAGE",
		DeviceClass:               "tamper",
		EntityCategory:            EntityCategoryDiagnostic,
		EnabledByDefault:          false,
		SuggestedDisplayPrecision: -1,
	},
	"SABOTAGE_MAGNETIC_FIELD": {
		Key:                       "SABOTAGE",
		DeviceClass:               "tamper",
		EntityCategory:            EntityCategoryDiagnostic,
		EnabledByDefault:          false,
		SuggestedDisplayPrecision: -1,
	},
	"SABOTAGE_VERTICAL": {
		Key:                       "SABOTAGE",
		DeviceClass:               "tamper",
		EntityCategory:            EntityCategoryDiagnostic,
		EnabledByDefault:          false,
		SuggestedDisplayPrecision: -1,
	},

	// Window
	"WINDOW_STATE": {
		Key:                       "WINDOW_STATE",
		DeviceClass:               "window",
		EnabledByDefault:          true,
		SuggestedDisplayPrecision: -1,
	},
}
