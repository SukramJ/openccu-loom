// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

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
// The device_class of these rules is deliberately absent: it is the domain's
// answer, resolved from parameter.BinarySensorQuantityFor and translated by
// quantityToBinarySensorDeviceClass. This table used to carry it too and,
// being applied second, overwrote the domain's — so a correction in
// internal/parameter reached REST, WS and Matter while MQTT kept publishing
// the stale class for every model named here.
//
// What stays is what only this plane knows: which entity key to use, whether
// HA enables it by default, and the display precision. The by-parameter table
// below still carries its own device classes; those were not measured against
// the domain and are left alone.
var binarySensorRulesByDeviceAndParam = map[devParam]HARegistryDescription{
	// HmIP-DLP — door sensor (magnetic contact), STATE → door
	{"HmIP-DLP", "STATE"}: {
		Key: "STATE",
	},

	// HmIP-DSD-PCB — occupancy detector, STATE → occupancy
	{"HmIP-DSD-PCB", "STATE"}: {
		Key: "STATE",
	},

	// HmIP-SCI — contact sensor, STATE → opening
	{"HmIP-SCI", "STATE"}: {
		Key: "STATE",
	},

	// HmIP-FCI1 — multi-channel contact input, STATE → opening
	{"HmIP-FCI1", "STATE"}: {
		Key: "STATE",
	},

	// HmIP-FCI6 — 6-channel contact input, STATE → opening
	{"HmIP-FCI6", "STATE"}: {
		Key: "STATE",
	},

	// HM-Sec-SD — smoke detector, STATE → smoke
	{"HM-Sec-SD", "STATE"}: {
		Key: "STATE",
	},

	// Window/door contact sensors — STATE → window.
	// HmIP-SWD is deliberately absent: it is the water sensor, it has no
	// STATE parameter, and hasModelPrefix requires a "-" separator so the
	// entry could not serve HmIP-SWDM*/HmIP-SWDO* as a prefix either.
	// Divergence from the ported table, see notes/parity/by_design.md.
	{"HmIP-SWDO", "STATE"}: {
		Key: "STATE",
	},
	{"HmIP-SWDM", "STATE"}: {
		Key: "STATE",
	},
	{"HM-Sec-SC", "STATE"}: {
		Key: "STATE",
	},
	{"HM-SCI-3-FM", "STATE"}: {
		Key: "STATE",
	},
	// "ZEL STG RM FFK" contains spaces — kept as-is; Go map keys may
	// contain spaces.
	{"ZEL STG RM FFK", "STATE"}: {
		Key: "STATE",
	},

	// HM-Sen-RD-O — rain detector, STATE → moisture
	{"HM-Sen-RD-O", "STATE"}: {
		Key: "STATE",
	},

	// HM-Sec-Win — working/motion flag; disabled by default (diagnostic)
	{"HM-Sec-Win", "WORKING"}: {
		Key:              "WORKING",
		EnabledByDefault: entityBoolPtr(false),
	},

	// HmIP-SRH — rotary handle, WINDOW_OPEN → window
	{"HmIP-SRH", "WINDOW_OPEN"}: {
		Key: "WINDOW_OPEN",
	},

	// HM-Sec-RHS — rotary handle, WINDOW_OPEN → window
	{"HM-Sec-RHS", "WINDOW_OPEN"}: {
		Key: "WINDOW_OPEN",
	},

	// HmIP-SWSD — combined smoke/intrusion detector
	// SMOKE_ALARM → smoke
	{"HmIP-SWSD", "SMOKE_ALARM"}: {
		Key: "SMOKE_ALARM",
	},
	// INTRUSION_ALARM → safety
	{"HmIP-SWSD", "INTRUSION_ALARM"}: {
		Key: "INTRUSION_ALARM",
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
var binarySensorRulesByParam = map[string]HARegistryDescription{
	// Safety / alarm sensors
	"ALARMSTATE": {
		Key:         "ALARMSTATE",
		DeviceClass: "safety",
	},
	"ACOUSTIC_ALARM_ACTIVE": {
		Key:         "ACOUSTIC_ALARM_ACTIVE",
		DeviceClass: "safety",
	},
	"OPTICAL_ALARM_ACTIVE": {
		Key:         "OPTICAL_ALARM_ACTIVE",
		DeviceClass: "safety",
	},
	// EMERGENCY_OPERATION: safety, disabled
	"EMERGENCY_OPERATION": {
		Key:              "EMERGENCY_OPERATION",
		DeviceClass:      "safety",
		EnabledByDefault: entityBoolPtr(false),
	},

	// Problem sensors (diagnostic)
	"BLOCKED_PERMANENT": {
		Key:              "BLOCKED",
		DeviceClass:      "problem",
		EntityCategory:   EntityCategoryDiagnostic,
		EnabledByDefault: entityBoolPtr(false),
	},
	"BLOCKED_TEMPORARY": {
		Key:              "BLOCKED",
		DeviceClass:      "problem",
		EntityCategory:   EntityCategoryDiagnostic,
		EnabledByDefault: entityBoolPtr(false),
	},
	"BURST_LIMIT_WARNING": {
		Key:              "BURST_LIMIT_WARNING",
		DeviceClass:      "problem",
		EntityCategory:   EntityCategoryDiagnostic,
		EnabledByDefault: entityBoolPtr(false),
	},
	// DUTYCYCLE and DUTY_CYCLE share a key; diagnostic with icon
	"DUTYCYCLE": {
		Key:              "DUTY_CYCLE",
		DeviceClass:      "problem",
		EntityCategory:   EntityCategoryDiagnostic,
		Icon:             "mdi:radio-tower",
		EnabledByDefault: entityBoolPtr(false),
	},
	"DUTY_CYCLE": {
		Key:              "DUTY_CYCLE",
		DeviceClass:      "problem",
		EntityCategory:   EntityCategoryDiagnostic,
		Icon:             "mdi:radio-tower",
		EnabledByDefault: entityBoolPtr(false),
	},
	// DEW_POINT_ALARM: problem, disabled
	"DEW_POINT_ALARM": {
		Key:              "DEW_POINT_ALARM",
		DeviceClass:      "problem",
		EnabledByDefault: entityBoolPtr(false),
	},
	// ERROR_JAMMED: problem, disabled
	"ERROR_JAMMED": {
		Key:              "ERROR_JAMMED",
		DeviceClass:      "problem",
		EnabledByDefault: entityBoolPtr(false),
	},

	// Battery (diagnostic, enabled — battery state is important)
	"LOWBAT": {
		Key:            "LOW_BAT",
		DeviceClass:    "battery",
		EntityCategory: EntityCategoryDiagnostic,
	},
	"LOW_BAT": {
		Key:            "LOW_BAT",
		DeviceClass:    "battery",
		EntityCategory: EntityCategoryDiagnostic,
	},
	"LOWBAT_SENSOR": {
		Key:            "LOW_BAT",
		DeviceClass:    "battery",
		EntityCategory: EntityCategoryDiagnostic,
	},

	// Heat
	"HEATER_STATE": {
		Key:         "HEATER_STATE",
		DeviceClass: "heat",
	},

	// Moisture
	"MOISTURE_DETECTED": {
		Key:         "MOISTURE_DETECTED",
		DeviceClass: "moisture",
	},
	"RAINING": {
		Key:         "RAINING",
		DeviceClass: "moisture",
	},
	"WATERLEVEL_DETECTED": {
		Key:         "WATERLEVEL_DETECTED",
		DeviceClass: "moisture",
	},

	// Motion
	"MOTION": {
		Key:         "MOTION",
		DeviceClass: "motion",
	},

	// Presence
	"PRESENCE_DETECTION_STATE": {
		Key:         "PRESENCE_DETECTION_STATE",
		DeviceClass: "presence",
	},

	// Power
	"POWER_MAINS_FAILURE": {
		Key:         "POWER_MAINS_FAILURE",
		DeviceClass: "power",
	},

	// Running / process — PROCESS and WORKING share a key
	"PROCESS": {
		Key:         "PROCESS",
		DeviceClass: "running",
	},
	// Note: generic WORKING (no devices=) also maps to "running".
	// The device-specific HM-Sec-Win/WORKING override in
	// binarySensorRulesByDeviceAndParam takes priority for
	// that device and sets enabled_default=false.
	"WORKING": {
		Key:         "PROCESS",
		DeviceClass: "running",
	},

	// Tamper / sabotage (diagnostic)
	"SABOTAGE": {
		Key:              "SABOTAGE",
		DeviceClass:      "tamper",
		EntityCategory:   EntityCategoryDiagnostic,
		EnabledByDefault: entityBoolPtr(false),
	},
	"SABOTAGE_STICKY": {
		Key:              "SABOTAGE",
		DeviceClass:      "tamper",
		EntityCategory:   EntityCategoryDiagnostic,
		EnabledByDefault: entityBoolPtr(false),
	},
	"SABOTAGE_ACCELERATION": {
		Key:              "SABOTAGE",
		DeviceClass:      "tamper",
		EntityCategory:   EntityCategoryDiagnostic,
		EnabledByDefault: entityBoolPtr(false),
	},
	"SABOTAGE_BATTERY": {
		Key:              "SABOTAGE",
		DeviceClass:      "tamper",
		EntityCategory:   EntityCategoryDiagnostic,
		EnabledByDefault: entityBoolPtr(false),
	},
	"SABOTAGE_MAGNETIC_FIELD": {
		Key:              "SABOTAGE",
		DeviceClass:      "tamper",
		EntityCategory:   EntityCategoryDiagnostic,
		EnabledByDefault: entityBoolPtr(false),
	},
	"SABOTAGE_VERTICAL": {
		Key:              "SABOTAGE",
		DeviceClass:      "tamper",
		EntityCategory:   EntityCategoryDiagnostic,
		EnabledByDefault: entityBoolPtr(false),
	},

	// Window
	"WINDOW_STATE": {
		Key:         "WINDOW_STATE",
		DeviceClass: "window",
	},
}
