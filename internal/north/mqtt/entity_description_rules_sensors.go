// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package mqtt

// sensorRulesByDeviceAndParam holds per-(device-prefix, parameter)
// EntityDescription overrides ported from the the upstream HA-integration reference Python project
// (custom_components/sensors/).
//
// Each Python EntityDescriptionRule with a non-empty `devices` tuple and
// priority=10 produces one map entry per (device-prefix × parameter) pair here.
//
// Source files converted:
//   - temperature.py  (_TEMPERATURE_DIAGNOSTIC_DEVICES × ACTUAL_TEMPERATURE)
//   - level.py        (_THERMOSTAT_DEVICES, _COVER_DEVICES, _LIGHT_DEVICES,
//     _BLIND_DEVICES × LEVEL/LEVEL_2/COLOR, VALVE_STATE)
//   - energy.py       (HMW-IO-12-Sw14-DR × FREQUENCY)
//   - misc.py         (HmIP-WKP × CODE_STATE, STATE tri-state devices,
//     HM-Sec-Key × DIRECTION/ERROR,
//     HM-Sec-WDS × STATE,
//     HM-Sec-Win × STATUS/DIRECTION/ERROR,
//     HmIP-SWSD × TIME_OF_OPERATION)
//
// Notes on special cases:
//   - Python's `multiplier` field has no counterpart in EntityDescription —
//     level.py entries use multiplier=100 internally but the field is omitted.
//   - `translation_key` is a HA-specific concept with no equivalent field here;
//     omitted from all entries.
//   - `device_class=ENUM` (enum_sensor) maps to DeviceClass: "enum".
//   - `device_class=DURATION` maps to DeviceClass: "duration".
//   - `state_class=TOTAL_INCREASING` maps to StateClass: "total_increasing".
//   - diagnostic_sensor default: EnabledByDefault=false, EntityCategory=diagnostic,
//     StateClass="measurement" — unless overridden by the rule's enabled_default.
//   - simple_sensor / enum_sensor: no StateClass.
//   - HmSensorEntityDescription entries (level.py) that use
//     entity_registry_enabled_default=False are mapped to EnabledByDefault: false.
var sensorRulesByDeviceAndParam = map[devParam]EntityDescription{
	// -------------------------------------------------------------------------
	// temperature.py — ACTUAL_TEMPERATURE as diagnostic on switch devices
	// -------------------------------------------------------------------------
	{"ELV-SH-BS", "ACTUAL_TEMPERATURE"}:    {Key: "ACTUAL_TEMPERATURE", DeviceClass: "temperature", StateClass: "measurement", UnitOfMeasurement: "°C", EntityCategory: EntityCategoryDiagnostic, EnabledByDefault: false, SuggestedDisplayPrecision: -1},
	{"HmIP-BB", "ACTUAL_TEMPERATURE"}:      {Key: "ACTUAL_TEMPERATURE", DeviceClass: "temperature", StateClass: "measurement", UnitOfMeasurement: "°C", EntityCategory: EntityCategoryDiagnostic, EnabledByDefault: false, SuggestedDisplayPrecision: -1},
	{"HmIP-BD", "ACTUAL_TEMPERATURE"}:      {Key: "ACTUAL_TEMPERATURE", DeviceClass: "temperature", StateClass: "measurement", UnitOfMeasurement: "°C", EntityCategory: EntityCategoryDiagnostic, EnabledByDefault: false, SuggestedDisplayPrecision: -1},
	{"HmIP-BR", "ACTUAL_TEMPERATURE"}:      {Key: "ACTUAL_TEMPERATURE", DeviceClass: "temperature", StateClass: "measurement", UnitOfMeasurement: "°C", EntityCategory: EntityCategoryDiagnostic, EnabledByDefault: false, SuggestedDisplayPrecision: -1},
	{"HmIP-BS", "ACTUAL_TEMPERATURE"}:      {Key: "ACTUAL_TEMPERATURE", DeviceClass: "temperature", StateClass: "measurement", UnitOfMeasurement: "°C", EntityCategory: EntityCategoryDiagnostic, EnabledByDefault: false, SuggestedDisplayPrecision: -1},
	{"HmIP-DR", "ACTUAL_TEMPERATURE"}:      {Key: "ACTUAL_TEMPERATURE", DeviceClass: "temperature", StateClass: "measurement", UnitOfMeasurement: "°C", EntityCategory: EntityCategoryDiagnostic, EnabledByDefault: false, SuggestedDisplayPrecision: -1},
	{"HmIP-FB", "ACTUAL_TEMPERATURE"}:      {Key: "ACTUAL_TEMPERATURE", DeviceClass: "temperature", StateClass: "measurement", UnitOfMeasurement: "°C", EntityCategory: EntityCategoryDiagnostic, EnabledByDefault: false, SuggestedDisplayPrecision: -1},
	{"HmIP-FD", "ACTUAL_TEMPERATURE"}:      {Key: "ACTUAL_TEMPERATURE", DeviceClass: "temperature", StateClass: "measurement", UnitOfMeasurement: "°C", EntityCategory: EntityCategoryDiagnostic, EnabledByDefault: false, SuggestedDisplayPrecision: -1},
	{"HmIP-FR", "ACTUAL_TEMPERATURE"}:      {Key: "ACTUAL_TEMPERATURE", DeviceClass: "temperature", StateClass: "measurement", UnitOfMeasurement: "°C", EntityCategory: EntityCategoryDiagnostic, EnabledByDefault: false, SuggestedDisplayPrecision: -1},
	{"HmIP-FS", "ACTUAL_TEMPERATURE"}:      {Key: "ACTUAL_TEMPERATURE", DeviceClass: "temperature", StateClass: "measurement", UnitOfMeasurement: "°C", EntityCategory: EntityCategoryDiagnostic, EnabledByDefault: false, SuggestedDisplayPrecision: -1},
	{"HmIP-MOD-OC8", "ACTUAL_TEMPERATURE"}: {Key: "ACTUAL_TEMPERATURE", DeviceClass: "temperature", StateClass: "measurement", UnitOfMeasurement: "°C", EntityCategory: EntityCategoryDiagnostic, EnabledByDefault: false, SuggestedDisplayPrecision: -1},
	{"HmIP-PCB", "ACTUAL_TEMPERATURE"}:     {Key: "ACTUAL_TEMPERATURE", DeviceClass: "temperature", StateClass: "measurement", UnitOfMeasurement: "°C", EntityCategory: EntityCategoryDiagnostic, EnabledByDefault: false, SuggestedDisplayPrecision: -1},
	{"HmIP-PD", "ACTUAL_TEMPERATURE"}:      {Key: "ACTUAL_TEMPERATURE", DeviceClass: "temperature", StateClass: "measurement", UnitOfMeasurement: "°C", EntityCategory: EntityCategoryDiagnostic, EnabledByDefault: false, SuggestedDisplayPrecision: -1},
	{"HmIP-PS", "ACTUAL_TEMPERATURE"}:      {Key: "ACTUAL_TEMPERATURE", DeviceClass: "temperature", StateClass: "measurement", UnitOfMeasurement: "°C", EntityCategory: EntityCategoryDiagnostic, EnabledByDefault: false, SuggestedDisplayPrecision: -1},
	{"HmIP-USB", "ACTUAL_TEMPERATURE"}:     {Key: "ACTUAL_TEMPERATURE", DeviceClass: "temperature", StateClass: "measurement", UnitOfMeasurement: "°C", EntityCategory: EntityCategoryDiagnostic, EnabledByDefault: false, SuggestedDisplayPrecision: -1},
	{"HmIPW-DR", "ACTUAL_TEMPERATURE"}:     {Key: "ACTUAL_TEMPERATURE", DeviceClass: "temperature", StateClass: "measurement", UnitOfMeasurement: "°C", EntityCategory: EntityCategoryDiagnostic, EnabledByDefault: false, SuggestedDisplayPrecision: -1},
	{"HmIPW-FIO", "ACTUAL_TEMPERATURE"}:    {Key: "ACTUAL_TEMPERATURE", DeviceClass: "temperature", StateClass: "measurement", UnitOfMeasurement: "°C", EntityCategory: EntityCategoryDiagnostic, EnabledByDefault: false, SuggestedDisplayPrecision: -1},

	// -------------------------------------------------------------------------
	// level.py — LEVEL on thermostat/valve devices (pipe_level)
	// -------------------------------------------------------------------------
	{"HmIP-eTRV", "LEVEL"}:        {Key: "LEVEL", StateClass: "measurement", UnitOfMeasurement: "%", EnabledByDefault: false, SuggestedDisplayPrecision: -1},
	{"HmIP-HEATING", "LEVEL"}:     {Key: "LEVEL", StateClass: "measurement", UnitOfMeasurement: "%", EnabledByDefault: false, SuggestedDisplayPrecision: -1},
	{"HmIP-FALMOT-C12", "LEVEL"}:  {Key: "LEVEL", StateClass: "measurement", UnitOfMeasurement: "%", EnabledByDefault: false, SuggestedDisplayPrecision: -1},
	{"HmIPW-FALMOT-C12", "LEVEL"}: {Key: "LEVEL", StateClass: "measurement", UnitOfMeasurement: "%", EnabledByDefault: false, SuggestedDisplayPrecision: -1},

	// level.py — LEVEL on cover/shutter devices (cover_level)
	{"HmIP-BROLL", "LEVEL"}:  {Key: "LEVEL", StateClass: "measurement", UnitOfMeasurement: "%", EnabledByDefault: false, SuggestedDisplayPrecision: -1},
	{"HmIP-FROLL", "LEVEL"}:  {Key: "LEVEL", StateClass: "measurement", UnitOfMeasurement: "%", EnabledByDefault: false, SuggestedDisplayPrecision: -1},
	{"HmIP-BBL", "LEVEL"}:    {Key: "LEVEL", StateClass: "measurement", UnitOfMeasurement: "%", EnabledByDefault: false, SuggestedDisplayPrecision: -1},
	{"HmIP-DRBLI4", "LEVEL"}: {Key: "LEVEL", StateClass: "measurement", UnitOfMeasurement: "%", EnabledByDefault: false, SuggestedDisplayPrecision: -1},
	{"HmIPW-DRBL4", "LEVEL"}: {Key: "LEVEL", StateClass: "measurement", UnitOfMeasurement: "%", EnabledByDefault: false, SuggestedDisplayPrecision: -1},
	{"HmIP-FBL", "LEVEL"}:    {Key: "LEVEL", StateClass: "measurement", UnitOfMeasurement: "%", EnabledByDefault: false, SuggestedDisplayPrecision: -1},

	// level.py — LEVEL on light/dimmer devices (light_level)
	{"HmIP-BSL", "LEVEL"}:     {Key: "LEVEL", StateClass: "measurement", UnitOfMeasurement: "%", EnabledByDefault: false, SuggestedDisplayPrecision: -1},
	{"HmIP-BDT", "LEVEL"}:     {Key: "LEVEL", StateClass: "measurement", UnitOfMeasurement: "%", EnabledByDefault: false, SuggestedDisplayPrecision: -1},
	{"HmIP-DRDI3", "LEVEL"}:   {Key: "LEVEL", StateClass: "measurement", UnitOfMeasurement: "%", EnabledByDefault: false, SuggestedDisplayPrecision: -1},
	{"HmIP-FDT", "LEVEL"}:     {Key: "LEVEL", StateClass: "measurement", UnitOfMeasurement: "%", EnabledByDefault: false, SuggestedDisplayPrecision: -1},
	{"HmIPW-PDT", "LEVEL"}:    {Key: "LEVEL", StateClass: "measurement", UnitOfMeasurement: "%", EnabledByDefault: false, SuggestedDisplayPrecision: -1},
	{"HmIP-RGBW", "LEVEL"}:    {Key: "LEVEL", StateClass: "measurement", UnitOfMeasurement: "%", EnabledByDefault: false, SuggestedDisplayPrecision: -1},
	{"HmIP-SCTH230", "LEVEL"}: {Key: "LEVEL", StateClass: "measurement", UnitOfMeasurement: "%", EnabledByDefault: false, SuggestedDisplayPrecision: -1},
	{"HmIPW-DRD3", "LEVEL"}:   {Key: "LEVEL", StateClass: "measurement", UnitOfMeasurement: "%", EnabledByDefault: false, SuggestedDisplayPrecision: -1},
	{"HmIPW-WRC6", "LEVEL"}:   {Key: "LEVEL", StateClass: "measurement", UnitOfMeasurement: "%", EnabledByDefault: false, SuggestedDisplayPrecision: -1},

	// level.py — LEVEL_2 (tilt) on blind devices (cover_tilt)
	{"HmIP-BBL", "LEVEL_2"}:    {Key: "LEVEL", StateClass: "measurement", UnitOfMeasurement: "%", EnabledByDefault: false, SuggestedDisplayPrecision: -1},
	{"HmIP-DRBLI4", "LEVEL_2"}: {Key: "LEVEL", StateClass: "measurement", UnitOfMeasurement: "%", EnabledByDefault: false, SuggestedDisplayPrecision: -1},
	{"HmIPW-DRBL4", "LEVEL_2"}: {Key: "LEVEL", StateClass: "measurement", UnitOfMeasurement: "%", EnabledByDefault: false, SuggestedDisplayPrecision: -1},
	{"HmIP-FBL", "LEVEL_2"}:    {Key: "LEVEL", StateClass: "measurement", UnitOfMeasurement: "%", EnabledByDefault: false, SuggestedDisplayPrecision: -1},

	// level.py — COLOR on RGB/light devices (hidden; surfaced via Light entity)
	{"HmIP-BSL", "COLOR"}:   {Key: "COLOR", EnabledByDefault: false, SuggestedDisplayPrecision: -1},
	{"HmIP-RGBW", "COLOR"}:  {Key: "COLOR", EnabledByDefault: false, SuggestedDisplayPrecision: -1},
	{"HmIPW-WRC6", "COLOR"}: {Key: "COLOR", EnabledByDefault: false, SuggestedDisplayPrecision: -1},

	// level.py — VALVE_STATE on HM-CC-RT-DN and HM-CC-VD (pipe_level)
	{"HM-CC-RT-DN", "VALVE_STATE"}: {Key: "VALVE_STATE", StateClass: "measurement", UnitOfMeasurement: "%", EnabledByDefault: true, SuggestedDisplayPrecision: -1},
	{"HM-CC-VD", "VALVE_STATE"}:    {Key: "VALVE_STATE", StateClass: "measurement", UnitOfMeasurement: "%", EnabledByDefault: true, SuggestedDisplayPrecision: -1},

	// -------------------------------------------------------------------------
	// energy.py — HMW-IO-12-Sw14-DR uses mHz for FREQUENCY (no state_class)
	// -------------------------------------------------------------------------
	{"HMW-IO-12-Sw14-DR", "FREQUENCY"}: {Key: "FREQUENCY", UnitOfMeasurement: "mHz", EnabledByDefault: true, SuggestedDisplayPrecision: -1},

	// -------------------------------------------------------------------------
	// misc.py — HmIP-WKP CODE_STATE (enum)
	// -------------------------------------------------------------------------
	{"HmIP-WKP", "CODE_STATE"}: {Key: "WKP_CODE_STATE", DeviceClass: "enum", EnabledByDefault: true, SuggestedDisplayPrecision: -1},

	// misc.py — Tri-state sensors (window handles): STATE (enum)
	{"HmIP-SRH", "STATE"}:   {Key: "TRI_STATE", DeviceClass: "enum", EnabledByDefault: true, SuggestedDisplayPrecision: -1},
	{"HmIP-STV", "STATE"}:   {Key: "TRI_STATE", DeviceClass: "enum", EnabledByDefault: true, SuggestedDisplayPrecision: -1},
	{"HM-Sec-RHS", "STATE"}: {Key: "TRI_STATE", DeviceClass: "enum", EnabledByDefault: true, SuggestedDisplayPrecision: -1},
	{"HM-Sec-xx", "STATE"}:  {Key: "TRI_STATE", DeviceClass: "enum", EnabledByDefault: true, SuggestedDisplayPrecision: -1},
	// Note: "ZEL STG RM FDK" has a space in the device name; kept as-is from Python source.
	{"ZEL STG RM FDK", "STATE"}: {Key: "TRI_STATE", DeviceClass: "enum", EnabledByDefault: true, SuggestedDisplayPrecision: -1},

	// misc.py — HM-Sec-Key DIRECTION and ERROR (enum)
	{"HM-Sec-Key", "DIRECTION"}: {Key: "SEC-KEY_DIRECTION", DeviceClass: "enum", EnabledByDefault: true, SuggestedDisplayPrecision: -1},
	{"HM-Sec-Key", "ERROR"}:     {Key: "SEC-KEY_ERROR", DeviceClass: "enum", EnabledByDefault: true, SuggestedDisplayPrecision: -1},

	// misc.py — HM-Sec-WDS STATE (enum)
	{"HM-Sec-WDS", "STATE"}: {Key: "STATE", DeviceClass: "enum", EnabledByDefault: true, SuggestedDisplayPrecision: -1},

	// misc.py — HM-Sec-Win STATUS, DIRECTION, ERROR (enum)
	{"HM-Sec-Win", "STATUS"}:    {Key: "SEC-WIN_STATUS", DeviceClass: "enum", EnabledByDefault: true, SuggestedDisplayPrecision: -1},
	{"HM-Sec-Win", "DIRECTION"}: {Key: "SEC-WIN_DIRECTION", DeviceClass: "enum", EnabledByDefault: true, SuggestedDisplayPrecision: -1},
	{"HM-Sec-Win", "ERROR"}:     {Key: "SEC-WIN_ERROR", DeviceClass: "enum", EnabledByDefault: true, SuggestedDisplayPrecision: -1},

	// misc.py — HmIP-SWSD TIME_OF_OPERATION
	// Python: multiplier=1/86400 to convert seconds→days; multiplier omitted here.
	{"HmIP-SWSD", "TIME_OF_OPERATION"}: {Key: "TIME_OF_OPERATION", DeviceClass: "duration", StateClass: "total_increasing", UnitOfMeasurement: "d", EntityCategory: EntityCategoryDiagnostic, EnabledByDefault: false, SuggestedDisplayPrecision: -1},
}

// sensorRulesByParam holds generic per-parameter EntityDescription
// overrides ported from the the upstream HA-integration reference Python project for rules without
// a `devices` constraint (priority < 10 or no priority, treated as generic).
//
// Source files:
//   - air_quality.py  — CO2, humidity, vapor, enthalpy, PM1/2.5/10 mass+number,
//     typical particle size, air pressure, dirt level, smoke level
//   - battery.py      — BATTERY_STATE/OPERATING_VOLTAGE, OPERATING_VOLTAGE_LEVEL
//   - energy.py       — power, IEC power, energy counter, IEC energy, voltage,
//     current, frequency, gas energy/flow/power/volume,
//     water flow/volume/volume_since_open
//   - fallback.py     — unit-based fallback rules (very low priority, kept as
//     generic param rules)
//   - level.py        — generic LEVEL/LEVEL_2, FILLING_LEVEL
//   - misc.py         — RSSI, carrier sense, duty cycle, IP address, CODE_ID,
//     ACTIVITY_STATE/DIRECTION, DOOR_STATE, LOCK_STATE,
//     LOCK_STATE_REASON, SMOKE_DETECTOR_ALARM_STATUS, VALUE
//   - temperature.py  — ACTUAL_TEMPERATURE/TEMPERATURE, DEWPOINT/DEW_POINT,
//     DEW_POINT_SPREAD, APPARENT_TEMPERATURE/FROST_POINT
//   - weather.py      — BRIGHTNESS, illumination, wind direction, wind speed,
//     rain counter, sunshine duration
//
// When a Python rule has multiple parameters, each parameter gets its own entry.
// The Key field is set to the canonical key from the Python description.
var sensorRulesByParam = map[string]EntityDescription{
	// -------------------------------------------------------------------------
	// air_quality.py
	// -------------------------------------------------------------------------

	// CO2 concentration (ppm)
	"CONCENTRATION": {Key: "CONCENTRATION", DeviceClass: "carbon_dioxide", StateClass: "measurement", UnitOfMeasurement: "ppm", EnabledByDefault: true, SuggestedDisplayPrecision: -1},

	// Humidity
	"HUMIDITY":        {Key: "HUMIDITY", DeviceClass: "humidity", StateClass: "measurement", UnitOfMeasurement: "%", EnabledByDefault: true, SuggestedDisplayPrecision: -1},
	"ACTUAL_HUMIDITY": {Key: "HUMIDITY", DeviceClass: "humidity", StateClass: "measurement", UnitOfMeasurement: "%", EnabledByDefault: true, SuggestedDisplayPrecision: -1},

	// Vapor concentration (absolute humidity, disabled by default)
	"VAPOR_CONCENTRATION": {Key: "VAPOR_CONCENTRATION", DeviceClass: "absolute_humidity", StateClass: "measurement", UnitOfMeasurement: "g/m³", EnabledByDefault: false, SuggestedDisplayPrecision: -1},

	// Enthalpy (no device_class, custom icon, disabled by default)
	"ENTHALPY": {Key: "ENTHALPY", StateClass: "measurement", UnitOfMeasurement: "kJ/kg", Icon: "mdi:fire", EnabledByDefault: false, SuggestedDisplayPrecision: -1},

	// PM1 mass concentration (μg/m³)
	"MASS_CONCENTRATION_PM_1":             {Key: "MASS_CONCENTRATION_PM_1", DeviceClass: "pm1", StateClass: "measurement", UnitOfMeasurement: "μg/m³", EnabledByDefault: true, SuggestedDisplayPrecision: -1},
	"MASS_CONCENTRATION_PM_1_24H_AVERAGE": {Key: "MASS_CONCENTRATION_PM_1", DeviceClass: "pm1", StateClass: "measurement", UnitOfMeasurement: "μg/m³", EnabledByDefault: true, SuggestedDisplayPrecision: -1},

	// PM10 mass concentration (μg/m³)
	"MASS_CONCENTRATION_PM_10":             {Key: "MASS_CONCENTRATION_PM_10", DeviceClass: "pm10", StateClass: "measurement", UnitOfMeasurement: "μg/m³", EnabledByDefault: true, SuggestedDisplayPrecision: -1},
	"MASS_CONCENTRATION_PM_10_24H_AVERAGE": {Key: "MASS_CONCENTRATION_PM_10", DeviceClass: "pm10", StateClass: "measurement", UnitOfMeasurement: "μg/m³", EnabledByDefault: true, SuggestedDisplayPrecision: -1},

	// PM2.5 mass concentration (μg/m³)
	"MASS_CONCENTRATION_PM_2_5":             {Key: "MASS_CONCENTRATION_PM_2_5", DeviceClass: "pm25", StateClass: "measurement", UnitOfMeasurement: "μg/m³", EnabledByDefault: true, SuggestedDisplayPrecision: -1},
	"MASS_CONCENTRATION_PM_2_5_24H_AVERAGE": {Key: "MASS_CONCENTRATION_PM_2_5", DeviceClass: "pm25", StateClass: "measurement", UnitOfMeasurement: "μg/m³", EnabledByDefault: true, SuggestedDisplayPrecision: -1},

	// PM number concentrations (1/cm³)
	"NUMBER_CONCENTRATION_PM_1":   {Key: "NUMBER_CONCENTRATION_PM_1", StateClass: "measurement", UnitOfMeasurement: "1/cm³", EnabledByDefault: true, SuggestedDisplayPrecision: -1},
	"NUMBER_CONCENTRATION_PM_10":  {Key: "NUMBER_CONCENTRATION_PM_10", StateClass: "measurement", UnitOfMeasurement: "1/cm³", EnabledByDefault: true, SuggestedDisplayPrecision: -1},
	"NUMBER_CONCENTRATION_PM_2_5": {Key: "NUMBER_CONCENTRATION_PM_2_5", StateClass: "measurement", UnitOfMeasurement: "1/cm³", EnabledByDefault: true, SuggestedDisplayPrecision: -1},

	// Typical particle size (μm)
	"TYPICAL_PARTICLE_SIZE": {Key: "TYPICAL_PARTICLE_SIZE", StateClass: "measurement", UnitOfMeasurement: "µm", EnabledByDefault: true, SuggestedDisplayPrecision: -1},

	// Air pressure (hPa)
	"AIR_PRESSURE": {Key: "AIR_PRESSURE", DeviceClass: "pressure", StateClass: "measurement", UnitOfMeasurement: "hPa", EnabledByDefault: true, SuggestedDisplayPrecision: -1},

	// Dirt level (%)
	"DIRT_LEVEL": {Key: "DIRT_LEVEL", StateClass: "measurement", UnitOfMeasurement: "%", EnabledByDefault: true, SuggestedDisplayPrecision: -1},

	// Smoke level (%)
	"SMOKE_LEVEL": {Key: "SMOKE_LEVEL", StateClass: "measurement", UnitOfMeasurement: "%", EnabledByDefault: true, SuggestedDisplayPrecision: -1},

	// -------------------------------------------------------------------------
	// battery.py
	// -------------------------------------------------------------------------

	// Operating voltage / battery state (V, diagnostic, disabled by default)
	"BATTERY_STATE":     {Key: "OPERATING_VOLTAGE", DeviceClass: "voltage", StateClass: "measurement", UnitOfMeasurement: "V", EntityCategory: EntityCategoryDiagnostic, EnabledByDefault: false, SuggestedDisplayPrecision: 1},
	"OPERATING_VOLTAGE": {Key: "OPERATING_VOLTAGE", DeviceClass: "voltage", StateClass: "measurement", UnitOfMeasurement: "V", EntityCategory: EntityCategoryDiagnostic, EnabledByDefault: false, SuggestedDisplayPrecision: 1},

	// Operating voltage level (%, diagnostic, disabled by default)
	"OPERATING_VOLTAGE_LEVEL": {Key: "OPERATING_VOLTAGE_LEVEL", DeviceClass: "battery", StateClass: "measurement", UnitOfMeasurement: "%", EntityCategory: EntityCategoryDiagnostic, EnabledByDefault: false, SuggestedDisplayPrecision: -1},

	// -------------------------------------------------------------------------
	// energy.py
	// -------------------------------------------------------------------------

	// Power (W)
	"POWER":     {Key: "POWER", DeviceClass: "power", StateClass: "measurement", UnitOfMeasurement: "W", EnabledByDefault: true, SuggestedDisplayPrecision: -1},
	"IEC_POWER": {Key: "IEC_POWER", DeviceClass: "power", StateClass: "measurement", UnitOfMeasurement: "W", EnabledByDefault: true, SuggestedDisplayPrecision: -1},

	// Energy counter (Wh, total_increasing)
	"ENERGY_COUNTER":         {Key: "ENERGY_COUNTER", DeviceClass: "energy", StateClass: "total_increasing", UnitOfMeasurement: "Wh", EnabledByDefault: true, SuggestedDisplayPrecision: -1},
	"ENERGY_COUNTER_FEED_IN": {Key: "ENERGY_COUNTER", DeviceClass: "energy", StateClass: "total_increasing", UnitOfMeasurement: "Wh", EnabledByDefault: true, SuggestedDisplayPrecision: -1},

	// IEC energy counter (kWh, total_increasing)
	"IEC_ENERGY_COUNTER": {Key: "IEC_ENERGY_COUNTER", DeviceClass: "energy", StateClass: "total_increasing", UnitOfMeasurement: "kWh", EnabledByDefault: true, SuggestedDisplayPrecision: -1},

	// Voltage (V)
	"VOLTAGE": {Key: "VOLTAGE", DeviceClass: "voltage", StateClass: "measurement", UnitOfMeasurement: "V", EnabledByDefault: true, SuggestedDisplayPrecision: -1},

	// Current (mA)
	"CURRENT": {Key: "CURRENT", DeviceClass: "current", StateClass: "measurement", UnitOfMeasurement: "mA", EnabledByDefault: true, SuggestedDisplayPrecision: -1},

	// Frequency (Hz) — generic; HMW-IO-12-Sw14-DR overrides via device map above
	"FREQUENCY": {Key: "FREQUENCY", DeviceClass: "frequency", StateClass: "measurement", UnitOfMeasurement: "Hz", EnabledByDefault: true, SuggestedDisplayPrecision: -1},

	// Gas sensors
	"GAS_ENERGY_COUNTER": {Key: "GAS_ENERGY_COUNTER", DeviceClass: "gas", StateClass: "total_increasing", UnitOfMeasurement: "m³", EnabledByDefault: true, SuggestedDisplayPrecision: -1},
	"GAS_FLOW":           {Key: "GAS_FLOW", DeviceClass: "volume_flow_rate", StateClass: "measurement", UnitOfMeasurement: "m³/h", EnabledByDefault: true, SuggestedDisplayPrecision: -1},
	// GAS_POWER: simple_sensor with unit=m³ — no state_class
	"GAS_POWER":  {Key: "GAS_POWER", UnitOfMeasurement: "m³", EnabledByDefault: true, SuggestedDisplayPrecision: -1},
	"GAS_VOLUME": {Key: "GAS_VOLUME", DeviceClass: "gas", StateClass: "total_increasing", UnitOfMeasurement: "m³", EnabledByDefault: true, SuggestedDisplayPrecision: -1},

	// Water sensors
	"WATER_FLOW":              {Key: "WATER_FLOW", DeviceClass: "volume_flow_rate", StateClass: "measurement", UnitOfMeasurement: "L/min", EnabledByDefault: true, SuggestedDisplayPrecision: 1},
	"WATER_VOLUME":            {Key: "WATER_VOLUME", DeviceClass: "water", StateClass: "total_increasing", UnitOfMeasurement: "L", EnabledByDefault: true, SuggestedDisplayPrecision: -1},
	"WATER_VOLUME_SINCE_OPEN": {Key: "WATER_VOLUME_SINCE_OPEN", DeviceClass: "water", StateClass: "total", UnitOfMeasurement: "L", EnabledByDefault: true, SuggestedDisplayPrecision: -1},

	// -------------------------------------------------------------------------
	// fallback.py — very low priority unit-based fallbacks (kept as param rules)
	// -------------------------------------------------------------------------

	// Note: fallback.py rules match by unit, not parameter name, and have
	// priority=-100. They are included here as catch-all parameter entries for
	// parameters not covered by more-specific rules. Lookup callers should treat
	// these as last-resort defaults.
	//
	// The fallback key names ("PERCENTAGE_FALLBACK", etc.) are kept from Python
	// to make provenance clear. The BAR pressure fallback is omitted here since
	// its key ("PRESSURE_BAR_FALLBACK") has no real parameter name to key on.
	// The absolute humidity fallback ("CONCENTRATION_FALLBACK") also has no
	// concrete parameter; it is omitted.

	// -------------------------------------------------------------------------
	// level.py — generic LEVEL/LEVEL_2 and FILLING_LEVEL
	// -------------------------------------------------------------------------

	// Generic LEVEL (fallback when no device-specific override exists)
	"LEVEL":   {Key: "LEVEL", StateClass: "measurement", UnitOfMeasurement: "%", EnabledByDefault: true, SuggestedDisplayPrecision: -1},
	"LEVEL_2": {Key: "LEVEL", StateClass: "measurement", UnitOfMeasurement: "%", EnabledByDefault: true, SuggestedDisplayPrecision: -1},

	// Filling level
	"FILLING_LEVEL": {Key: "FILLING_LEVEL", StateClass: "measurement", UnitOfMeasurement: "%", EnabledByDefault: true, SuggestedDisplayPrecision: -1},

	// -------------------------------------------------------------------------
	// misc.py — generic (non-device-specific) rules
	// -------------------------------------------------------------------------

	// RSSI signal strength (dBm, diagnostic, disabled by default)
	"RSSI_DEVICE": {Key: "RSSI", DeviceClass: "signal_strength", StateClass: "measurement", UnitOfMeasurement: "dBm", EntityCategory: EntityCategoryDiagnostic, EnabledByDefault: false, SuggestedDisplayPrecision: -1},
	"RSSI_PEER":   {Key: "RSSI", DeviceClass: "signal_strength", StateClass: "measurement", UnitOfMeasurement: "dBm", EntityCategory: EntityCategoryDiagnostic, EnabledByDefault: false, SuggestedDisplayPrecision: -1},

	// Carrier sense level (%, diagnostic, enabled by default per Python enabled_default=True)
	"CARRIER_SENSE_LEVEL": {Key: "CARRIER_SENSE_LEVEL", StateClass: "measurement", UnitOfMeasurement: "%", EntityCategory: EntityCategoryDiagnostic, Icon: "mdi:radio-tower", EnabledByDefault: true, SuggestedDisplayPrecision: -1},

	// Duty cycle level (%, diagnostic, enabled by default)
	"DUTY_CYCLE_LEVEL": {Key: "DUTY_CYCLE_LEVEL", StateClass: "measurement", UnitOfMeasurement: "%", EntityCategory: EntityCategoryDiagnostic, Icon: "mdi:radio-tower", EnabledByDefault: true, SuggestedDisplayPrecision: -1},

	// IP Address (simple_sensor, diagnostic, no state_class)
	"IP_ADDRESS": {Key: "IP_ADDRESS", EntityCategory: EntityCategoryDiagnostic, Icon: "mdi:ip-network", EnabledByDefault: true, SuggestedDisplayPrecision: -1},

	// Code ID (simple_sensor, no special attributes)
	"CODE_ID": {Key: "CODE_ID", EnabledByDefault: true, SuggestedDisplayPrecision: -1},

	// Activity state / direction (enum)
	"ACTIVITY_STATE": {Key: "DIRECTION", DeviceClass: "enum", EnabledByDefault: true, SuggestedDisplayPrecision: -1},
	"DIRECTION":      {Key: "DIRECTION", DeviceClass: "enum", EnabledByDefault: true, SuggestedDisplayPrecision: -1},

	// Door state (enum)
	"DOOR_STATE": {Key: "DOOR_STATE", DeviceClass: "enum", EnabledByDefault: true, SuggestedDisplayPrecision: -1},

	// Lock state (enum)
	"LOCK_STATE": {Key: "LOCK_STATE", DeviceClass: "enum", EnabledByDefault: true, SuggestedDisplayPrecision: -1},

	// Lock state reason (enum, disabled by default)
	"LOCK_STATE_REASON": {Key: "LOCK_STATE_REASON", DeviceClass: "enum", EnabledByDefault: false, SuggestedDisplayPrecision: -1},

	// Smoke detector alarm status (enum)
	"SMOKE_DETECTOR_ALARM_STATUS": {Key: "SMOKE_DETECTOR_ALARM_STATUS", DeviceClass: "enum", EnabledByDefault: true, SuggestedDisplayPrecision: -1},

	// VALUE (generic measurement, no unit or device_class)
	"VALUE": {Key: "VALUE", StateClass: "measurement", EnabledByDefault: true, SuggestedDisplayPrecision: -1},

	// -------------------------------------------------------------------------
	// temperature.py — generic rules (no device constraint)
	// -------------------------------------------------------------------------

	// Generic temperature
	"ACTUAL_TEMPERATURE": {Key: "TEMPERATURE", DeviceClass: "temperature", StateClass: "measurement", UnitOfMeasurement: "°C", EnabledByDefault: true, SuggestedDisplayPrecision: -1},
	"TEMPERATURE":        {Key: "TEMPERATURE", DeviceClass: "temperature", StateClass: "measurement", UnitOfMeasurement: "°C", EnabledByDefault: true, SuggestedDisplayPrecision: -1},

	// Dew point (disabled by default)
	"DEWPOINT":  {Key: "DEW_POINT", DeviceClass: "temperature", StateClass: "measurement", UnitOfMeasurement: "°C", EnabledByDefault: false, SuggestedDisplayPrecision: -1},
	"DEW_POINT": {Key: "DEW_POINT", DeviceClass: "temperature", StateClass: "measurement", UnitOfMeasurement: "°C", EnabledByDefault: false, SuggestedDisplayPrecision: -1},

	// Dew point spread (Kelvin, disabled by default)
	"DEW_POINT_SPREAD": {Key: "DEW_POINT_SPREAD", DeviceClass: "temperature", StateClass: "measurement", UnitOfMeasurement: "K", EnabledByDefault: false, SuggestedDisplayPrecision: -1},

	// Apparent temperature / frost point (disabled by default)
	"APPARENT_TEMPERATURE": {Key: "APPARENT_TEMPERATURE", DeviceClass: "temperature", StateClass: "measurement", UnitOfMeasurement: "°C", EnabledByDefault: false, SuggestedDisplayPrecision: -1},
	"FROST_POINT":          {Key: "APPARENT_TEMPERATURE", DeviceClass: "temperature", StateClass: "measurement", UnitOfMeasurement: "°C", EnabledByDefault: false, SuggestedDisplayPrecision: -1},

	// -------------------------------------------------------------------------
	// weather.py
	// -------------------------------------------------------------------------

	// Brightness (no device_class, no unit — custom translation)
	"BRIGHTNESS": {Key: "BRIGHTNESS", StateClass: "measurement", EnabledByDefault: true, SuggestedDisplayPrecision: -1},

	// Illuminance (lx)
	"ILLUMINATION":         {Key: "ILLUMINATION", DeviceClass: "illuminance", StateClass: "measurement", UnitOfMeasurement: "lx", EnabledByDefault: true, SuggestedDisplayPrecision: -1},
	"AVERAGE_ILLUMINATION": {Key: "ILLUMINATION", DeviceClass: "illuminance", StateClass: "measurement", UnitOfMeasurement: "lx", EnabledByDefault: true, SuggestedDisplayPrecision: -1},
	"CURRENT_ILLUMINATION": {Key: "ILLUMINATION", DeviceClass: "illuminance", StateClass: "measurement", UnitOfMeasurement: "lx", EnabledByDefault: true, SuggestedDisplayPrecision: -1},
	"HIGHEST_ILLUMINATION": {Key: "ILLUMINATION", DeviceClass: "illuminance", StateClass: "measurement", UnitOfMeasurement: "lx", EnabledByDefault: true, SuggestedDisplayPrecision: -1},
	"LOWEST_ILLUMINATION":  {Key: "ILLUMINATION", DeviceClass: "illuminance", StateClass: "measurement", UnitOfMeasurement: "lx", EnabledByDefault: true, SuggestedDisplayPrecision: -1},
	"LUX":                  {Key: "ILLUMINATION", DeviceClass: "illuminance", StateClass: "measurement", UnitOfMeasurement: "lx", EnabledByDefault: true, SuggestedDisplayPrecision: -1},

	// Wind direction (°, no device_class)
	"WIND_DIR":             {Key: "WIND_DIR", StateClass: "measurement", UnitOfMeasurement: "°", EnabledByDefault: true, SuggestedDisplayPrecision: -1},
	"WIND_DIR_RANGE":       {Key: "WIND_DIR", StateClass: "measurement", UnitOfMeasurement: "°", EnabledByDefault: true, SuggestedDisplayPrecision: -1},
	"WIND_DIRECTION":       {Key: "WIND_DIR", StateClass: "measurement", UnitOfMeasurement: "°", EnabledByDefault: true, SuggestedDisplayPrecision: -1},
	"WIND_DIRECTION_RANGE": {Key: "WIND_DIR", StateClass: "measurement", UnitOfMeasurement: "°", EnabledByDefault: true, SuggestedDisplayPrecision: -1},

	// Wind speed (km/h)
	"WIND_SPEED": {Key: "WIND_SPEED", DeviceClass: "wind_speed", StateClass: "measurement", UnitOfMeasurement: "km/h", EnabledByDefault: true, SuggestedDisplayPrecision: -1},

	// Rain counter (mm, total_increasing)
	"RAIN_COUNTER": {Key: "RAIN_COUNTER", StateClass: "total_increasing", UnitOfMeasurement: "mm", EnabledByDefault: true, SuggestedDisplayPrecision: -1},

	// Sunshine duration (min, total_increasing)
	"SUNSHINEDURATION": {Key: "SUNSHINEDURATION", StateClass: "total_increasing", UnitOfMeasurement: "min", EnabledByDefault: true, SuggestedDisplayPrecision: -1},
}
