// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// Package hmui carries the data-point UI classification catalogue
// the daemon serves through `DataPointSummary.UIHint`. The SPA's
// AutoTile composer reads the hint verbatim — there is no parallel
// classifier on the JS side. See notes/concepts/ui/auto-tile-concept.md §7.
//
// Resolution order in [HintFor]:
//
//  1. ENUM-shape rules (alarm-state value lists, on/off shapes).
//  2. Parameter-name substring (MOTION, STATE, RAINING, …).
//  3. Unit-string (°C → temperature, lx → illuminance, …).
//  4. Type fallback (BOOL / INTEGER / FLOAT / STRING / ACTION).
//
// Adding a row is a one-line edit + a unit-test row. New devices
// the registry adds tomorrow render coherently as long as either
// their unit or their parameter name matches one of the rows.
package hmui

import "strings"

// Hint is the per-data-point classification envelope the daemon
// emits on every REST `DataPointSummary`. The SPA renders Icon +
// SemanticKind verbatim and looks up StateColorRule against its
// own rule registry.
//
// Icon names follow the `mdi:<token>` convention (Material Design
// Icons). Semantic is a stable token consumed by the composer to
// group readouts of the same physical quantity into one bucket
// (e.g. three µg/m³ readouts cluster as "particulate"). StateColor
// rules are named so the SPA can apply a value-threshold colour
// without per-DP JS logic.
type Hint struct {
	Icon           string `json:"icon"`
	Semantic       string `json:"semantic"`
	StateColorRule string `json:"state_color_rule,omitempty"`
}

// HintFor classifies a data point. None of the inputs are required;
// callers pass whatever the descriptor surfaces. The function never
// returns an empty hint — `typeFallback` is the floor.
//
// Resolution order is specificity-descending: parameter-name rules
// (most specific knowledge) win over enum-shape rules (generic
// alarm-taxonomy detection), which win over unit rules, which win
// over the type fallback.
func HintFor(parameter, unit, paramType string, valueList []string) Hint {
	if h, ok := parameterHint(parameter); ok {
		return h
	}
	if h, ok := enumShapeHint(paramType, valueList); ok {
		return h
	}
	if h, ok := unitHintFor(unit); ok {
		return h
	}
	return typeFallback(paramType)
}

// enumShapeHint inspects an ENUM's value_list and returns an
// alarm-state hint when the values look like an alarm taxonomy
// (`IDLE_OFF` + at least one `*_ALARM*` entry — matches HmIP-SWSD,
// HmIP-SAM, smoke detectors, water leak detectors).
func enumShapeHint(paramType string, valueList []string) (Hint, bool) {
	if !strings.EqualFold(paramType, "ENUM") || len(valueList) < 2 {
		return Hint{}, false
	}
	if !strings.EqualFold(valueList[0], "IDLE_OFF") && !strings.EqualFold(valueList[0], "IDLE") {
		return Hint{}, false
	}
	for _, v := range valueList[1:] {
		if strings.Contains(strings.ToUpper(v), "ALARM") {
			return Hint{
				Icon:           "mdi:bell-alert",
				Semantic:       "alarm_state",
				StateColorRule: "alarm_active",
			}, true
		}
	}
	return Hint{}, false
}

// parameterHint maps boolean-typed or untyped DPs to a semantic by
// recognising parameter-name substrings. Rules are ordered most-
// specific first; the first match wins.
func parameterHint(parameter string) (Hint, bool) {
	if parameter == "" {
		return Hint{}, false
	}
	p := strings.ToUpper(parameter)

	// Exact-match table for short, frequently-named parameters.
	if h, ok := parameterExact[p]; ok {
		return h, true
	}

	// Substring table for prefix / suffix / family matches. Order
	// matters — more specific tokens come first.
	for _, rule := range parameterSubstrings {
		if strings.Contains(p, rule.match) {
			return rule.hint, true
		}
	}
	return Hint{}, false
}

// parameterExact is the exact-match short-circuit. Faster than
// the substring loop for the common cases.
var parameterExact = map[string]Hint{
	"STATE":                     {Icon: "mdi:circle", Semantic: "state"},
	"MOTION":                    {Icon: "mdi:run-fast", Semantic: "motion"},
	"PRESENCE_DETECTION_STATE":  {Icon: "mdi:account-eye", Semantic: "presence"},
	"PRESENCE_DETECTION_ACTIVE": {Icon: "mdi:account-eye-outline", Semantic: "presence_active"},
	"MOTION_DETECTION_ACTIVE":   {Icon: "mdi:run-fast", Semantic: "motion_active"},
	"RESET_MOTION":              {Icon: "mdi:restart", Semantic: "action"},
	"RESET_PRESENCE":            {Icon: "mdi:restart", Semantic: "action"},
	"ON_TIME":                   {Icon: "mdi:timer-outline", Semantic: "duration"},
	"BOOST_TIME_PERIOD":         {Icon: "mdi:fire", Semantic: "duration"},
	"RAINING":                   {Icon: "mdi:weather-pouring", Semantic: "rain", StateColorRule: "alarm_active"},
	"WINDOW_STATE":              {Icon: "mdi:window-open-variant", Semantic: "contact"},
	"VALVE_STATE":               {Icon: "mdi:valve", Semantic: "valve_position"},
	"BURST_RX":                  {Icon: "mdi:radio-tower", Semantic: "diagnostic"},
	"CONFIG_PENDING":            {Icon: "mdi:cog-clockwise", Semantic: "diagnostic"},
	"DUTY_CYCLE":                {Icon: "mdi:speedometer", Semantic: "diagnostic"},
	"DUTY_CYCLE_LEVEL":          {Icon: "mdi:speedometer", Semantic: "diagnostic"},
	"CARRIER_SENSE_LEVEL":       {Icon: "mdi:speedometer-medium", Semantic: "diagnostic"},
	"UPDATE_PENDING":            {Icon: "mdi:download", Semantic: "diagnostic"},
	"IP_ADDRESS":                {Icon: "mdi:ip-network", Semantic: "diagnostic"},
}

var parameterSubstrings = []struct {
	match string
	hint  Hint
}{
	{"SMOKE_DETECTOR", Hint{Icon: "mdi:smoke-detector-variant", Semantic: "smoke", StateColorRule: "alarm_active"}},
	{"WATERLEVEL_DETECTED", Hint{Icon: "mdi:water-alert", Semantic: "water_leak", StateColorRule: "alarm_active"}},
	{"MOISTURE_DETECTED", Hint{Icon: "mdi:water-alert", Semantic: "water_leak", StateColorRule: "alarm_active"}},
	{"SABOTAGE", Hint{Icon: "mdi:shield-alert", Semantic: "tamper", StateColorRule: "alarm_active"}},
	{"LOWBAT", Hint{Icon: "mdi:battery-alert", Semantic: "battery_low", StateColorRule: "alarm_active"}},
	{"LOW_BAT", Hint{Icon: "mdi:battery-alert", Semantic: "battery_low", StateColorRule: "alarm_active"}},
	{"STICKY_UNREACH", Hint{Icon: "mdi:lan-disconnect", Semantic: "connectivity", StateColorRule: "alarm_active"}},
	{"UNREACH", Hint{Icon: "mdi:lan-disconnect", Semantic: "connectivity", StateColorRule: "alarm_active"}},
	{"OPERATING_VOLTAGE", Hint{Icon: "mdi:flash-outline", Semantic: "voltage"}},
	{"MASS_CONCENTRATION", Hint{Icon: "mdi:smog", Semantic: "particulate"}},
	{"NUMBER_CONCENTRATION", Hint{Icon: "mdi:counter", Semantic: "particulate_count"}},
	{"TYPICAL_PARTICLE_SIZE", Hint{Icon: "mdi:dots-grid", Semantic: "particle_size"}},
	{"ENERGY_COUNTER", Hint{Icon: "mdi:counter", Semantic: "energy"}},
	{"GAS_VOLUME", Hint{Icon: "mdi:fire-circle", Semantic: "gas_volume"}},
	{"GAS_ENERGY", Hint{Icon: "mdi:fire-circle", Semantic: "gas_energy"}},
	{"ACTUAL_TEMPERATURE", Hint{Icon: "mdi:thermometer", Semantic: "temperature", StateColorRule: "temp_heat"}},
	{"SET_POINT_TEMPERATURE", Hint{Icon: "mdi:thermometer-check", Semantic: "temperature_setpoint", StateColorRule: "temp_heat"}},
	{"SETPOINT", Hint{Icon: "mdi:thermometer-check", Semantic: "temperature_setpoint", StateColorRule: "temp_heat"}},
	{"HUMIDITY", Hint{Icon: "mdi:water-percent", Semantic: "humidity", StateColorRule: "humidity_band"}},
	{"ILLUMINATION", Hint{Icon: "mdi:weather-sunny", Semantic: "illuminance"}},
	{"BRIGHTNESS", Hint{Icon: "mdi:brightness-6", Semantic: "brightness"}},
	{"COLOR_TEMPERATURE", Hint{Icon: "mdi:thermometer-lines", Semantic: "color_temperature"}},
	{"COLOR", Hint{Icon: "mdi:palette", Semantic: "color"}},
	{"LEVEL", Hint{Icon: "mdi:percent", Semantic: "level"}},
	{"POWER", Hint{Icon: "mdi:flash", Semantic: "power"}},
	{"VOLTAGE", Hint{Icon: "mdi:flash-outline", Semantic: "voltage"}},
	{"CURRENT", Hint{Icon: "mdi:current-ac", Semantic: "current"}},
	{"FREQUENCY", Hint{Icon: "mdi:sine-wave", Semantic: "frequency"}},
	{"WIND_SPEED", Hint{Icon: "mdi:weather-windy", Semantic: "wind_speed"}},
	{"RAIN_COUNTER", Hint{Icon: "mdi:weather-pouring", Semantic: "rain_volume"}},
	{"AIR_PRESSURE", Hint{Icon: "mdi:gauge", Semantic: "pressure"}},
	{"FILLING_LEVEL", Hint{Icon: "mdi:gauge", Semantic: "fill_level"}},
	{"VAPOR", Hint{Icon: "mdi:water-opacity", Semantic: "vapor"}},
	{"DEW_POINT", Hint{Icon: "mdi:water-thermometer", Semantic: "dew_point"}},
	{"CO2_CONCENTRATION", Hint{Icon: "mdi:molecule-co2", Semantic: "co2"}},
	{"PRESSURE", Hint{Icon: "mdi:gauge", Semantic: "pressure"}},
	{"DISTANCE", Hint{Icon: "mdi:ruler", Semantic: "distance"}},
	{"FLOW", Hint{Icon: "mdi:waves-arrow-right", Semantic: "flow"}},
	{"ACCELERATION", Hint{Icon: "mdi:axis-arrow", Semantic: "acceleration"}},
	{"DOOR", Hint{Icon: "mdi:door", Semantic: "door"}},
	{"WINDOW", Hint{Icon: "mdi:window-open-variant", Semantic: "contact"}},
	{"BUTTON", Hint{Icon: "mdi:gesture-tap-button", Semantic: "button"}},
	{"PRESS_SHORT", Hint{Icon: "mdi:gesture-tap", Semantic: "button"}},
	{"PRESS_LONG", Hint{Icon: "mdi:gesture-tap-hold", Semantic: "button"}},
	{"RSSI", Hint{Icon: "mdi:signal", Semantic: "signal", StateColorRule: "signal_weak"}},
	{"BATTERY", Hint{Icon: "mdi:battery", Semantic: "battery"}},
}

// unitHintFor falls back to the unit string when no parameter rule
// claims the DP. Units use the exact CCU-shipped notation
// (e.g. "% rF", not "%RH").
func unitHintFor(unit string) (Hint, bool) {
	if unit == "" {
		return Hint{}, false
	}
	if h, ok := unitHints[unit]; ok {
		return h, true
	}
	// Stripped trailing whitespace + lowercase fallback so e.g.
	// "% rF" matches when the descriptor ships " % rF" or "% RF".
	if h, ok := unitHints[strings.ToLower(strings.TrimSpace(unit))]; ok {
		return h, true
	}
	return Hint{}, false
}

var unitHints = map[string]Hint{
	"°C":    {Icon: "mdi:thermometer", Semantic: "temperature", StateColorRule: "temp_heat"},
	"K":     {Icon: "mdi:thermometer", Semantic: "temperature"},
	"% rF":  {Icon: "mdi:water-percent", Semantic: "humidity", StateColorRule: "humidity_band"},
	"%":     {Icon: "mdi:percent", Semantic: "level"},
	"lx":    {Icon: "mdi:weather-sunny", Semantic: "illuminance"},
	"W":     {Icon: "mdi:flash", Semantic: "power"},
	"kW":    {Icon: "mdi:flash", Semantic: "power"},
	"Wh":    {Icon: "mdi:counter", Semantic: "energy"},
	"kWh":   {Icon: "mdi:counter", Semantic: "energy"},
	"V":     {Icon: "mdi:flash-outline", Semantic: "voltage"},
	"mV":    {Icon: "mdi:flash-outline", Semantic: "voltage"},
	"A":     {Icon: "mdi:current-ac", Semantic: "current"},
	"mA":    {Icon: "mdi:current-ac", Semantic: "current"},
	"Hz":    {Icon: "mdi:sine-wave", Semantic: "frequency"},
	"dBm":   {Icon: "mdi:signal", Semantic: "signal", StateColorRule: "signal_weak"},
	"µg/m³": {Icon: "mdi:smog", Semantic: "particulate", StateColorRule: "particulate_band"},
	"1/cm³": {Icon: "mdi:counter", Semantic: "particulate_count"},
	"µm":    {Icon: "mdi:dots-grid", Semantic: "particle_size"},
	"ppm":   {Icon: "mdi:molecule-co2", Semantic: "concentration"},
	"hPa":   {Icon: "mdi:gauge", Semantic: "pressure"},
	"mm":    {Icon: "mdi:ruler", Semantic: "distance"},
	"m":     {Icon: "mdi:ruler", Semantic: "distance"},
	"km":    {Icon: "mdi:ruler", Semantic: "distance"},
	"m/s":   {Icon: "mdi:weather-windy", Semantic: "wind_speed"},
	"km/h":  {Icon: "mdi:weather-windy", Semantic: "wind_speed"},
	"l":     {Icon: "mdi:cup-water", Semantic: "volume"},
	"m³":    {Icon: "mdi:cube-outline", Semantic: "volume"},
	"l/m²":  {Icon: "mdi:weather-pouring", Semantic: "rainfall"},
	"s":     {Icon: "mdi:timer-outline", Semantic: "duration"},
	"min":   {Icon: "mdi:timer-outline", Semantic: "duration"},
	"h":     {Icon: "mdi:timer-outline", Semantic: "duration"},
	"°":     {Icon: "mdi:angle-acute", Semantic: "angle"},
	"100%":  {Icon: "mdi:percent", Semantic: "level"},
}

// typeFallback is the floor. Anything that didn't match a more
// specific rule lands here so the SPA always has SOMETHING to
// render.
func typeFallback(paramType string) Hint {
	switch strings.ToUpper(paramType) {
	case "BOOL":
		return Hint{Icon: "mdi:circle-outline", Semantic: "state"}
	case "INTEGER":
		return Hint{Icon: "mdi:numeric", Semantic: "measurement"}
	case "FLOAT":
		return Hint{Icon: "mdi:chart-line-variant", Semantic: "measurement"}
	case "ENUM":
		return Hint{Icon: "mdi:format-list-bulleted", Semantic: "enum"}
	case "STRING":
		return Hint{Icon: "mdi:text", Semantic: "text"}
	case "ACTION":
		return Hint{Icon: "mdi:play", Semantic: "action"}
	}
	return Hint{Icon: "mdi:chart-bar", Semantic: "unknown"}
}
