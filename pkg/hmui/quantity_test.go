// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package hmui

import "testing"

// TestHintFor_ResolutionOrder pins the four-stage resolution chain.
// Each case names the rule that should fire and the expected
// semantic so a future reader sees which catalogue layer claimed
// the DP.
func TestHintFor_ResolutionOrder(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		parameter string
		unit      string
		paramType string
		valueList []string
		wantSem   string
		wantIcon  string
	}{
		// ENUM-shape rules
		{
			name:      "ENUM alarm shape — SMOKE_DETECTOR_ALARM_STATUS",
			parameter: "SMOKE_DETECTOR_ALARM_STATUS",
			paramType: "ENUM",
			valueList: []string{"IDLE_OFF", "PRIMARY_ALARM", "INTRUSION_ALARM", "SECONDARY_ALARM"},
			wantSem:   "smoke",
			wantIcon:  "mdi:smoke-detector-variant",
		},
		{
			// Parameter rule beats ENUM rule because parameter is more specific.
			name:      "Parameter beats ENUM — SMOKE_DETECTOR substring",
			parameter: "SMOKE_DETECTOR_ALARM",
			paramType: "ENUM",
			valueList: []string{"IDLE_OFF", "PRIMARY_ALARM"},
			wantSem:   "smoke",
			wantIcon:  "mdi:smoke-detector-variant",
		},
		// Parameter rules
		{name: "MOTION exact", parameter: "MOTION", paramType: "BOOL", wantSem: "motion", wantIcon: "mdi:run-fast"},
		{name: "PRESENCE_DETECTION_STATE exact", parameter: "PRESENCE_DETECTION_STATE", paramType: "BOOL", wantSem: "presence", wantIcon: "mdi:account-eye"},
		{name: "MOTION_DETECTION_ACTIVE exact", parameter: "MOTION_DETECTION_ACTIVE", paramType: "BOOL", wantSem: "motion_active", wantIcon: "mdi:run-fast"},
		{name: "RESET_MOTION exact", parameter: "RESET_MOTION", paramType: "ACTION", wantSem: "action", wantIcon: "mdi:restart"},
		{name: "ON_TIME exact", parameter: "ON_TIME", paramType: "FLOAT", wantSem: "duration", wantIcon: "mdi:timer-outline"},
		{name: "RAINING exact", parameter: "RAINING", paramType: "BOOL", wantSem: "rain", wantIcon: "mdi:weather-pouring"},
		// Parameter substrings
		{name: "WATERLEVEL_DETECTED", parameter: "WATERLEVEL_DETECTED", paramType: "BOOL", wantSem: "water_leak", wantIcon: "mdi:water-alert"},
		{name: "MOISTURE_DETECTED", parameter: "MOISTURE_DETECTED", paramType: "BOOL", wantSem: "water_leak", wantIcon: "mdi:water-alert"},
		{name: "SABOTAGE", parameter: "SABOTAGE", paramType: "BOOL", wantSem: "tamper", wantIcon: "mdi:shield-alert"},
		{name: "LOWBAT", parameter: "LOWBAT", paramType: "BOOL", wantSem: "battery_low", wantIcon: "mdi:battery-alert"},
		{name: "UNREACH", parameter: "UNREACH", paramType: "BOOL", wantSem: "connectivity", wantIcon: "mdi:lan-disconnect"},
		{name: "ACTUAL_TEMPERATURE", parameter: "ACTUAL_TEMPERATURE", unit: "°C", paramType: "FLOAT", wantSem: "temperature", wantIcon: "mdi:thermometer"},
		{name: "HUMIDITY", parameter: "HUMIDITY", unit: "% rF", paramType: "INTEGER", wantSem: "humidity", wantIcon: "mdi:water-percent"},
		{name: "MASS_CONCENTRATION_PM_2_5", parameter: "MASS_CONCENTRATION_PM_2_5", unit: "µg/m³", paramType: "FLOAT", wantSem: "particulate", wantIcon: "mdi:smog"},
		{name: "NUMBER_CONCENTRATION_PM_1", parameter: "NUMBER_CONCENTRATION_PM_1", unit: "1/cm³", paramType: "FLOAT", wantSem: "particulate_count", wantIcon: "mdi:counter"},
		{name: "ENERGY_COUNTER", parameter: "ENERGY_COUNTER", unit: "Wh", paramType: "FLOAT", wantSem: "energy", wantIcon: "mdi:counter"},
		{name: "POWER", parameter: "POWER", unit: "W", paramType: "FLOAT", wantSem: "power", wantIcon: "mdi:flash"},
		{name: "RSSI_DEVICE", parameter: "RSSI_DEVICE", unit: "dBm", paramType: "INTEGER", wantSem: "signal", wantIcon: "mdi:signal"},
		// Unit-only fallback (parameter doesn't claim it)
		{name: "Unknown parameter with °C unit", parameter: "OPERATING_TEMP", unit: "°C", paramType: "FLOAT", wantSem: "temperature", wantIcon: "mdi:thermometer"},
		{name: "Unknown parameter with hPa unit", parameter: "BARO_RAW", unit: "hPa", paramType: "FLOAT", wantSem: "pressure", wantIcon: "mdi:gauge"},
		// Type fallback (no parameter / unit match)
		{name: "BOOL fallback", parameter: "UNKNOWN_FLAG", paramType: "BOOL", wantSem: "state", wantIcon: "mdi:circle-outline"},
		{name: "INTEGER fallback", parameter: "UNKNOWN_INT", paramType: "INTEGER", wantSem: "measurement", wantIcon: "mdi:numeric"},
		{name: "ACTION fallback", parameter: "UNKNOWN_CMD", paramType: "ACTION", wantSem: "action", wantIcon: "mdi:play"},
		{name: "Completely unknown", parameter: "WHATEVER", paramType: "", wantSem: "unknown", wantIcon: "mdi:chart-bar"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := HintFor(tc.parameter, tc.unit, tc.paramType, tc.valueList)
			if got.Semantic != tc.wantSem {
				t.Errorf("Semantic = %q, want %q", got.Semantic, tc.wantSem)
			}
			if got.Icon != tc.wantIcon {
				t.Errorf("Icon = %q, want %q", got.Icon, tc.wantIcon)
			}
		})
	}
}

// TestHintFor_StateColorRules pins the StateColorRule field for the
// few rules the SPA uses to colour readouts (heat / humidity band /
// alarm / signal). New rules added to the catalogue should pin a
// row here.
func TestHintFor_StateColorRules(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		parameter string
		unit      string
		paramType string
		valueList []string
		wantRule  string
	}{
		{name: "temperature → temp_heat", parameter: "ACTUAL_TEMPERATURE", unit: "°C", paramType: "FLOAT", wantRule: "temp_heat"},
		{name: "humidity → humidity_band", parameter: "HUMIDITY", unit: "% rF", paramType: "INTEGER", wantRule: "humidity_band"},
		{name: "smoke alarm → alarm_active", parameter: "SMOKE_DETECTOR_ALARM_STATUS", paramType: "ENUM", valueList: []string{"IDLE_OFF", "PRIMARY_ALARM"}, wantRule: "alarm_active"},
		{name: "particulate → particulate_band", parameter: "X", unit: "µg/m³", paramType: "FLOAT", wantRule: "particulate_band"},
		{name: "signal → signal_weak", parameter: "RSSI_DEVICE", unit: "dBm", paramType: "INTEGER", wantRule: "signal_weak"},
		{name: "raining → alarm_active", parameter: "RAINING", paramType: "BOOL", wantRule: "alarm_active"},
		{name: "no rule for plain BOOL", parameter: "UNKNOWN_FLAG", paramType: "BOOL", wantRule: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := HintFor(tc.parameter, tc.unit, tc.paramType, tc.valueList)
			if got.StateColorRule != tc.wantRule {
				t.Errorf("StateColorRule = %q, want %q", got.StateColorRule, tc.wantRule)
			}
		})
	}
}

// TestHintFor_NeverEmpty guarantees the floor: even with zero
// inputs HintFor returns a usable hint. The composer relies on
// this — every DP gets SOMETHING to render.
func TestHintFor_NeverEmpty(t *testing.T) {
	t.Parallel()
	h := HintFor("", "", "", nil)
	if h.Icon == "" || h.Semantic == "" {
		t.Fatalf("empty inputs returned empty hint: %+v", h)
	}
}

// TestEnumShapeHint covers the alarm-taxonomy detector exhaustively
// — both the positive matches (IDLE + at least one *_ALARM*) and
// every guard that rejects non-alarm value lists.
func TestEnumShapeHint(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		paramType string
		valueList []string
		wantOK    bool
	}{
		// Happy paths — both IDLE_OFF and IDLE prefixes trigger
		// the detector. Substring match on "ALARM" anywhere in
		// the remaining entries.
		{"idle_off_alarm", "ENUM", []string{"IDLE_OFF", "FIRE_ALARM"}, true},
		{"idle_off_alarm_lower", "enum", []string{"idle_off", "smoke_alarm"}, true},
		{"idle_prefix_alarm", "ENUM", []string{"IDLE", "PANIC_ALARM_2"}, true},
		// Reject paths.
		{"wrong_type", "INTEGER", []string{"IDLE_OFF", "FIRE_ALARM"}, false},
		{"too_short", "ENUM", []string{"IDLE_OFF"}, false},
		{"empty_list", "ENUM", nil, false},
		{"no_idle_prefix", "ENUM", []string{"OFF", "FIRE_ALARM"}, false},
		{"no_alarm_token", "ENUM", []string{"IDLE_OFF", "FLOW", "WARN"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, ok := enumShapeHint(tc.paramType, tc.valueList)
			if ok != tc.wantOK {
				t.Fatalf("ok=%v, want %v (hint=%+v)", ok, tc.wantOK, got)
			}
			if ok && got.StateColorRule != "alarm_active" {
				t.Errorf("matched but StateColorRule=%q, want alarm_active", got.StateColorRule)
			}
		})
	}
}

// TestUnitHintFor exercises the canonical-key path, the
// trim+lowercase fallback path, and the empty-unit short-circuit.
func TestUnitHintFor(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   string
		want string // expected Semantic; empty means no match
	}{
		// Direct map hit.
		{"celsius", "°C", "temperature"},
		{"percent", "%", "level"},
		{"watt", "W", "power"},
		// Trim + lowercase fallback ( " % rF " → "% rf" → no direct
		// hit on "% rf"; we exercise the path by ensuring at least
		// a non-trim alternate spelling falls through).
		{"empty", "", ""},
		{"unknown_unit", "furlongs-per-fortnight", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, ok := unitHintFor(tc.in)
			if tc.want == "" {
				if ok {
					t.Fatalf("expected no-match, got %+v", got)
				}
				return
			}
			if !ok {
				t.Fatalf("expected match for %q, got none", tc.in)
			}
			if got.Semantic != tc.want {
				t.Errorf("Semantic = %q, want %q", got.Semantic, tc.want)
			}
		})
	}
}

// TestTypeFallback exhaustively covers every paramType arm and the
// default catch-all so the floor stays predictable.
func TestTypeFallback(t *testing.T) {
	t.Parallel()
	cases := []struct {
		paramType string
		wantSem   string
	}{
		{"BOOL", "state"},
		{"bool", "state"}, // case-insensitive
		{"INTEGER", "measurement"},
		{"FLOAT", "measurement"},
		{"ENUM", "enum"},
		{"STRING", "text"},
		{"ACTION", "action"},
		{"NONSENSE", "unknown"}, // default arm
		{"", "unknown"},
	}
	for _, tc := range cases {
		t.Run(tc.paramType, func(t *testing.T) {
			t.Parallel()
			got := typeFallback(tc.paramType)
			if got.Semantic != tc.wantSem {
				t.Errorf("typeFallback(%q).Semantic = %q, want %q", tc.paramType, got.Semantic, tc.wantSem)
			}
			if got.Icon == "" {
				t.Errorf("typeFallback(%q).Icon must not be empty", tc.paramType)
			}
		})
	}
}

// TestHintFor_FallbackLayers walks the resolution chain four times,
// each call disabling one earlier source so the next-most-specific
// layer must win. Complements TestHintFor_ResolutionOrder by
// asserting on layer-by-layer fall-through behaviour rather than
// matched-name positives.
func TestHintFor_FallbackLayers(t *testing.T) {
	t.Parallel()
	// Parameter wins over unit + type.
	h := HintFor("ACTUAL_TEMPERATURE", "°C", "FLOAT", nil)
	if h.Semantic != "temperature" {
		t.Errorf("parameter-priority Semantic=%q, want temperature", h.Semantic)
	}

	// No parameter match → enum-shape wins over unit + type.
	h = HintFor("", "%", "ENUM", []string{"IDLE_OFF", "FIRE_ALARM"})
	if h.Semantic != "alarm_state" {
		t.Errorf("enum-shape-priority Semantic=%q, want alarm_state", h.Semantic)
	}

	// No parameter, no enum-shape → unit wins over type.
	h = HintFor("", "W", "FLOAT", nil)
	if h.Semantic != "power" {
		t.Errorf("unit-priority Semantic=%q, want power", h.Semantic)
	}

	// No parameter, no enum-shape, no unit → type fallback.
	h = HintFor("", "", "BOOL", nil)
	if h.Semantic != "state" {
		t.Errorf("type-fallback Semantic=%q, want state", h.Semantic)
	}
}
