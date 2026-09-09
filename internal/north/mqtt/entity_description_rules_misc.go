// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package mqtt

// Per-domain entity-description tables ported from
//
// (numbers.py, switches.py, covers.py, locks.py, sirens.py, valves.py,
// buttons.py, selects.py). Each domain has two maps:
//
//   - <domain>RulesByDeviceAndParam — keyed by (device-prefix,
//     parameter); only entries that carry a devices= constraint in Python.
//   - <domain>RulesByParam — keyed by parameter name only; entries
//     without a devices= constraint.
//
// Maps that would be empty are omitted.
//
// Lookup priority mirrors the upstream HA-integration reference's EntityDescriptionRule priority
// field: device-and-param entries shadow param-only entries for the same
// parameter when the device prefix matches. The existing lookup helpers in
// entity_descriptions.go use the same prefix-match strategy via
// [hasModelPrefix]; extend them to consult these tables after the

// ---------------------------------------------------------------------------
// Number
// ---------------------------------------------------------------------------

// numberRulesByDeviceAndParam holds per-device Number overrides from
// numbers.py.
//
// Source rules (priority > 0, i.e. device-specific):
//   - HMW-IO-12-Sw14-DR / FREQUENCY → mHz unit (non-standard)
//   - HmIP-eTRV / LEVEL → disabled percentage (pipe-level semantics)
//   - HmIP-HEATING / LEVEL → same as eTRV
var numberRulesByDeviceAndParam = map[devParam]HARegistryDescription{
	{"HMW-IO-12-Sw14-DR", "FREQUENCY"}: {
		Key:               "FREQUENCY",
		UnitOfMeasurement: "mHz",
	},
	{"HmIP-eTRV", "LEVEL"}: {
		Key:               "LEVEL",
		UnitOfMeasurement: "%",
		EnabledByDefault:  entityBoolPtr(false),
	},
	{"HmIP-HEATING", "LEVEL"}: {
		Key:               "LEVEL",
		UnitOfMeasurement: "%",
		EnabledByDefault:  entityBoolPtr(false),
	},
}

// numberRulesByParam holds generic Number overrides from numbers.py.
//
// Source rules (no devices= constraint): - FREQUENCY → Hz (frequency
// device-class) - LEVEL     → % (percentage) - LEVEL_2   → % (percentage,
// second axis e.g. slat angle) - Timer-style writable parameters →
// enabled_by_default=false (ON_TIME, RAMP_TIME, BOOST_TIME, …). Mirrors the
// hidden-by-default behaviour.
var numberRulesByParam = map[string]HARegistryDescription{
	"FREQUENCY": {
		Key:               "FREQUENCY",
		DeviceClass:       "frequency",
		UnitOfMeasurement: "Hz",
	},
	"LEVEL": {
		Key:               "LEVEL",
		UnitOfMeasurement: "%",
	},
	"LEVEL_2": {
		Key:               "LEVEL_2",
		UnitOfMeasurement: "%",
	},
	"ON_TIME": {
		Key:              "ON_TIME",
		EntityCategory:   "config",
		EnabledByDefault: entityBoolPtr(false),
	},
	"ON_TIME_VALUE": {
		Key:              "ON_TIME_VALUE",
		EntityCategory:   "config",
		EnabledByDefault: entityBoolPtr(false),
	},
	"RAMP_TIME": {
		Key:              "RAMP_TIME",
		EntityCategory:   "config",
		EnabledByDefault: entityBoolPtr(false),
	},
	"RAMP_TIME_VALUE": {
		Key:              "RAMP_TIME_VALUE",
		EntityCategory:   "config",
		EnabledByDefault: entityBoolPtr(false),
	},
	"BOOST_TIME_PERIOD": {
		Key:              "BOOST_TIME_PERIOD",
		EntityCategory:   "config",
		EnabledByDefault: entityBoolPtr(false),
	},
	"PARTY_TIME_PERIOD": {
		Key:              "PARTY_TIME_PERIOD",
		EntityCategory:   "config",
		EnabledByDefault: entityBoolPtr(false),
	},
}

// ---------------------------------------------------------------------------
// Switch
// ---------------------------------------------------------------------------

// switchRulesByDeviceAndParam holds per-device Switch overrides from
// switches.py.
//
// Source rule:
//   - HmIP-PS → outlet device-class (OUTLET key)
var switchRulesByDeviceAndParam = map[devParam]HARegistryDescription{
	// HmIP-PS: the switched socket is classified as an outlet.
	// The Python rule carries category=DataPointCategory.SWITCH with
	// devices=("HmIP-PS",) and no parameter constraint; the parameter
	// dimension is represented as the empty string here so callers can
	// match on device alone.
	{"HmIP-PS", ""}: {
		Key:         "OUTLET",
		DeviceClass: "outlet",
	},
}

// switchRulesByParam holds generic Switch overrides from
// switches.py.
//
// Source rules (no devices= constraint):
//   - SCHEDULE_SWITCH           → CONFIG, disabled
//   - INHIBIT                   → disabled
//   - MOTION_DETECTION_ACTIVE   → CONFIG, disabled
//   - PRESENCE_DETECTION_ACTIVE → CONFIG, disabled (aliases MOTION key)
//   - AUTO_RELOCK_STATE         → CONFIG, disabled
//   - PERMISSION_STATE          → CONFIG, disabled
var switchRulesByParam = map[string]HARegistryDescription{
	"SCHEDULE_SWITCH": {
		Key:              "SCHEDULE_SWITCH",
		DeviceClass:      "switch",
		EntityCategory:   EntityCategoryConfig,
		EnabledByDefault: entityBoolPtr(false),
	},
	"INHIBIT": {
		Key:              "INHIBIT",
		DeviceClass:      "switch",
		EnabledByDefault: entityBoolPtr(false),
	},
	"MOTION_DETECTION_ACTIVE": {
		Key:              "MOTION_DETECTION_ACTIVE",
		DeviceClass:      "switch",
		EntityCategory:   EntityCategoryConfig,
		EnabledByDefault: entityBoolPtr(false),
	},
	"PRESENCE_DETECTION_ACTIVE": {
		Key:              "MOTION_DETECTION_ACTIVE",
		DeviceClass:      "switch",
		EntityCategory:   EntityCategoryConfig,
		EnabledByDefault: entityBoolPtr(false),
	},
	"AUTO_RELOCK_STATE": {
		Key:              "AUTO_RELOCK_STATE",
		DeviceClass:      "switch",
		EntityCategory:   EntityCategoryConfig,
		EnabledByDefault: entityBoolPtr(false),
	},
	"PERMISSION_STATE": {
		Key:              "PERMISSION_STATE",
		DeviceClass:      "switch",
		EntityCategory:   EntityCategoryConfig,
		EnabledByDefault: entityBoolPtr(false),
	},
}

// ---------------------------------------------------------------------------
// Cover
// ---------------------------------------------------------------------------

// coverRulesByDeviceAndParam holds per-device Cover overrides from
// covers.py.
// All COVER_RULES carry devices= constraints, so there is no param-only map.
//
// Source rules:
//   - HmIP-BBL, HmIP-FBL, HmIP-DRBLI4, HmIPW-DRBL4 → blind
//   - HmIP-BROLL, HmIP-FROLL, HM-LC-Bl1PBU-FM        → shutter
//   - HmIP-HDM1                                        → shade
//   - HmIP-MOD-HO, HmIP-MOD-TM                        → garage
//   - HM-Sec-Win                                       → window
//
// The parameter dimension is empty ("") for all entries: cover entities
// are dispatched by device model only, no secondary parameter key.
var coverRulesByDeviceAndParam = map[devParam]HARegistryDescription{
	{"HmIP-BBL", ""}:        {Key: "BLIND", DeviceClass: "blind"},
	{"HmIP-FBL", ""}:        {Key: "BLIND", DeviceClass: "blind"},
	{"HmIP-DRBLI4", ""}:     {Key: "BLIND", DeviceClass: "blind"},
	{"HmIPW-DRBL4", ""}:     {Key: "BLIND", DeviceClass: "blind"},
	{"HmIP-BROLL", ""}:      {Key: "SHUTTER", DeviceClass: "shutter"},
	{"HmIP-FROLL", ""}:      {Key: "SHUTTER", DeviceClass: "shutter"},
	{"HM-LC-Bl1PBU-FM", ""}: {Key: "SHUTTER", DeviceClass: "shutter"},
	{"HmIP-HDM1", ""}:       {Key: "HmIP-HDM1", DeviceClass: "shade"},
	{"HmIP-MOD-HO", ""}:     {Key: "GARAGE-HO", DeviceClass: "garage"},
	{"HmIP-MOD-TM", ""}:     {Key: "GARAGE-HO", DeviceClass: "garage"},
	{"HM-Sec-Win", ""}:      {Key: "HM-Sec-Win", DeviceClass: "window"},
}

// ---------------------------------------------------------------------------
// Lock
// ---------------------------------------------------------------------------

// lockRulesByParam holds generic Lock overrides from
// locks.py.
//
// Source rule:
//   - BUTTON_LOCK postfix → CONFIG, disabled
//
// The Python rule uses postfix= rather than parameters=; the key here is
// the postfix string so callers use the same postfix-lookup pattern as
// [LookupLockByPostfix].
var lockRulesByParam = map[string]HARegistryDescription{
	"BUTTON_LOCK": {
		Key:              "BUTTON_LOCK",
		EntityCategory:   EntityCategoryConfig,
		EnabledByDefault: entityBoolPtr(false),
	},
}

// ---------------------------------------------------------------------------
// Siren
// ---------------------------------------------------------------------------

// sirenRulesByDeviceAndParam holds per-device Siren overrides from
// sirens.py.
//
// Source rule:
//   - HmIP-SWSD → disabled (smoke-detector siren; only activate on alarm)
var sirenRulesByDeviceAndParam = map[devParam]HARegistryDescription{
	{"HmIP-SWSD", ""}: {Key: "SWSD", EnabledByDefault: entityBoolPtr(false)},
}

// ---------------------------------------------------------------------------
// Valve
// ---------------------------------------------------------------------------

// valveRulesByDeviceAndParam holds per-device Valve overrides from
// valves.py.
//
// Source rule:
//   - ELV-SH-WSM (note: trailing space in Python is intentional prefix)
//   - HmIP-WSM → water device-class (irrigation valve)
//
// The Python entry uses devices=("ELV-SH-WSM ", "HmIP-WSM"). The trailing
// space on "ELV-SH-WSM " is preserved as a key; callers using
// [hasModelPrefix] will strip that via prefix matching anyway.
var valveRulesByDeviceAndParam = map[devParam]HARegistryDescription{
	{"ELV-SH-WSM", ""}: {Key: "WSM", DeviceClass: "water"},
	{"HmIP-WSM", ""}:   {Key: "WSM", DeviceClass: "water"},
}

// ---------------------------------------------------------------------------
// Button
// ---------------------------------------------------------------------------

// buttonRulesByParam holds generic Button overrides from
// buttons.py.
//
// Source rules (no devices= constraint):
//   - RESET_MOTION    → CONFIG, disabled  (config_button factory)
//   - RESET_PRESENCE  → CONFIG, disabled  (config_button factory)
//   - PRESS_LONG      → disabled, no category (button factory; default enabled=false)
//   - PRESS_SHORT     → disabled, no category (button factory; default enabled=false)
//
// Note: factories.button() defaults enabled_default=False, no entity_category.
// factories.config_button() adds entity_category=CONFIG.
var buttonRulesByParam = map[string]HARegistryDescription{
	"RESET_MOTION": {
		Key:              "RESET_MOTION",
		EntityCategory:   EntityCategoryConfig,
		EnabledByDefault: entityBoolPtr(false),
	},
	"RESET_PRESENCE": {
		Key:              "RESET_PRESENCE",
		EntityCategory:   EntityCategoryConfig,
		EnabledByDefault: entityBoolPtr(false),
	},
	"PRESS_LONG": {
		Key:              "PRESS_LONG",
		EnabledByDefault: entityBoolPtr(false),
	},
	"PRESS_SHORT": {
		Key:              "PRESS_SHORT",
		EnabledByDefault: entityBoolPtr(false),
	},
}

// ---------------------------------------------------------------------------
// Select
// ---------------------------------------------------------------------------

// selectRulesByParam holds generic Select overrides from
// selects.py.
//
// Source rule:
//   - HEATING_COOLING → CONFIG, disabled
var selectRulesByParam = map[string]HARegistryDescription{
	"HEATING_COOLING": {
		Key:              "HEATING_COOLING",
		EntityCategory:   EntityCategoryConfig,
		EnabledByDefault: entityBoolPtr(false),
	},
}
