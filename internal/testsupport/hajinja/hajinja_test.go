// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package hajinja

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// oracleCases were rendered by jinja2 3.1.6's ImmutableSandboxedEnvironment
// with Home Assistant's variables (`value`, `value_json` when the payload is
// JSON) and a stripped result; "ERR <type>" marks a render that raised. They
// pin this helper to real Jinja semantics, the undefined-value cases above
// all: a helper more lenient than Jinja would bless a template that fails in
// Home Assistant.
var oracleCases = []struct{ tmpl, payload, want string }{
	{"{% if value_json is defined and value_json.val is not none %}{{ value_json.val }}{% endif %}", "{\"val\":21.5,\"ts\":1,\"lc\":1}", "21.5"},
	{"{% if value_json is defined and value_json.val is not none %}{{ value_json.val }}{% endif %}", "{\"val\":30,\"ts\":1,\"lc\":1}", "30"},
	{"{% if value_json is defined and value_json.val is not none %}{{ value_json.val }}{% endif %}", "{\"val\":null,\"ts\":1,\"lc\":1}", ""},
	{"{% if value_json is defined and value_json.val is not none %}{{ value_json.val }}{% endif %}", "", ""},
	{"{% if value_json is defined and value_json.val is not none %}{{ value_json.val | lower }}{% endif %}", "{\"val\":true}", "true"},
	{"{% if value_json is defined and value_json.val is not none %}{{ (value_json.val | float * 100) }}{% endif %}", "{\"val\":0.5}", "50.0"},
	{"{% if value_json is defined and value_json.val is not none %}{{ (value_json.val | float * 100) }}{% endif %}", "{\"val\":1}", "100.0"},
	{"{% if value_json.val.is_open %}open{% else %}closed{% endif %}", "{\"val\":{\"is_open\":true}}", "open"},
	{"{% set m = {'a': 'A', 'b': 'B'} %}{% if value_json is defined and value_json.val.preset_mode is not none %}{{ m.get(value_json.val.preset_mode, value_json.val.preset_mode) }}{% endif %}", "{\"val\":{\"preset_mode\":\"b\"}}", "B"},
	{"{% set m = {'a': 'A'} %}{{ m.get(value, value) }}", "zz", "zz"},
	{"{{ (value | float / 100) }}", "50", "0.5"},
	{"{{ 'online' if value | int(0) >= 2 else 'offline' }}", "2", "online"},
	{"{{ 'online' if value | int(0) >= 2 else 'offline' }}", "1", "offline"},
	{"{{ 'online' if value | int(0) >= 2 else 'offline' }}", "", "offline"},
	{"{{ dict(value_json.hm, event_type=value_json.val) | tojson }}", "{\"val\":\"press_short\",\"hm\":{\"available\":true}}", "{\"available\": true, \"event_type\": \"press_short\"}"},
	{"{{ dict(value_json.hm, event_type=value_json.val) | tojson }}", "{\"val\":\"press_short\"}", "ERR UndefinedError"},
	{"{{ dict(value_json.hm or {}, event_type=value_json.val) | tojson }}", "{\"val\":\"press_short\",\"hm\":null}", "{\"event_type\": \"press_short\"}"},
	{"{{ value | upper }}", "on", "ON"},
	{"{{ value_json | tojson }}", "{\"a\":1}", "{\"a\": 1}"},
	{"{{ value_json.hm.available | lower }}", "{\"val\":1,\"hm\":{\"available\":false}}", "false"},
	{"{{ value_json.val | length }}", "{\"val\":[1,2,3]}", "3"},
	{"{{ value_json.val.at | default('') }}", "{\"val\":{\"x\":1}}", ""},
	{"{{ value_json.val.state }}", "{\"val\":{\"state\":\"open\"}}", "open"},
	{"{\"messages\": {{ value_json.val | tojson }} }", "{\"val\":[]}", "{\"messages\": [] }"},
	{"{{ (value_json.val.schedule_data | default(none, true)) | tojson }}", "{\"val\":{\"schedule_data\":{}}}", "null"},
	{"{{ (\"uncertain\" if value_json.val.state_uncertain else \"valid\") | tojson }}", "{\"val\":{\"state_uncertain\":false}}", "\"valid\""},
	{"{{ value_json.val }}", "{\"val\":true}", "True"},
	{"{{ value_json.val.x }}", "{\"val\":5}", ""},
	{"{{ value_json.val.x.y }}", "{\"val\":{}}", "ERR UndefinedError"},
	{"{{ dict(value_json.hm or {}, event_type=value_json.val) | tojson }}", "{\"val\":\"press_short\"}", "{\"event_type\": \"press_short\"}"},
	{"{{ dict(value_json.hm or {}, event_type=value_json.val) | tojson }}", "{\"val\":\"press_short\",\"hm\":{\"available\":true}}", "{\"available\": true, \"event_type\": \"press_short\"}"},
	{"{{ dict(value_json.hm, event_type=value_json.val) | tojson }}", "{\"val\":\"press_short\",\"hm\":null}", "ERR TypeError"},
	{"{{ {\"val\": value} | tojson }}", "", "{\"val\": \"\"}"},
	{"{{ {\"val\": value} | tojson }}", "{foo", "{\"val\": \"{foo\"}"},
	{"{{ {\"val\": value} | tojson }}", "Hallo \"Welt\"", "{\"val\": \"Hallo \\\"Welt\\\"\"}"},
}

// TestRenderValueMatchesJinja2 compares every oracle case. JSON results are
// compared structurally, since jinja2's tojson spaces its separators.
func TestRenderValueMatchesJinja2(t *testing.T) {
	t.Parallel()
	for _, c := range oracleCases {
		got, err := RenderValue(c.tmpl, c.payload)
		if strings.HasPrefix(c.want, "ERR ") {
			if err == nil {
				t.Errorf("%q on %q rendered %q, but jinja2 raises %s", c.tmpl, c.payload, got, c.want)
			}
			continue
		}
		if err != nil {
			t.Errorf("%q on %q: %v, jinja2 renders %q", c.tmpl, c.payload, err, c.want)
			continue
		}
		if got == c.want {
			continue
		}
		var g, w any
		if json.Unmarshal([]byte(got), &g) == nil && json.Unmarshal([]byte(c.want), &w) == nil && reflect.DeepEqual(g, w) {
			continue
		}
		t.Errorf("%q on %q = %q, jinja2 renders %q", c.tmpl, c.payload, got, c.want)
	}
}
