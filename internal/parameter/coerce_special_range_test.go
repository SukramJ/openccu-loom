// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package parameter_test

import (
	"encoding/json"
	"strconv"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/parameter"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// The write-coerce path (parameter.Coerce) and the validation path
// (parameter.Validate) must agree on whether a numeric value is acceptable,
// and both must let a declared SPECIAL sentinel bypass MIN/MAX — the same rule
// the runtime read path (internal/model/generic bounds) applies. The reference
// accepts a declared special where the plain MIN/MAX check would reject it
// (model/generic/number.py _prepare_number_for_sending) and the CCU server
// keeps declared specials unclamped (the reference CCU simulator ccu.py
// _clamp_numeric_value); a value that is neither in range nor a declared
// special is rejected on both paths.

func floatDesc(t *testing.T, lo, hi float64, special string) hmproto.ParameterData {
	t.Helper()
	desc := hmproto.ParameterData{
		Type:       hmenum.ParameterTypeFloat,
		Operations: hmenum.OperationsRead | hmenum.OperationsWrite,
		Min:        json.RawMessage(rawFloat(lo)),
		Max:        json.RawMessage(rawFloat(hi)),
	}
	if special != "" {
		desc.Special = json.RawMessage(special)
	}
	return desc
}

func intDesc(t *testing.T, lo, hi int, special string) hmproto.ParameterData {
	t.Helper()
	desc := hmproto.ParameterData{
		Type:       hmenum.ParameterTypeInteger,
		Operations: hmenum.OperationsRead | hmenum.OperationsWrite,
		Min:        json.RawMessage(rawInt(lo)),
		Max:        json.RawMessage(rawInt(hi)),
	}
	if special != "" {
		desc.Special = json.RawMessage(special)
	}
	return desc
}

func rawFloat(f float64) string {
	return strconv.FormatFloat(f, 'f', -1, 64)
}

func rawInt(i int) string {
	return strconv.Itoa(i)
}

func TestCoerceAndValidateAgreeOnSpecialAndRange(t *testing.T) {
	t.Parallel()

	// SPECIAL encoded as the object form {"NOT_USED": 0.0} (JSON-RPC /
	// metadata extracts). NOT_USED = 0.0 sits below MIN = 0.5.
	specialObject := `{"NOT_USED": 0.0}`
	// SPECIAL encoded as the list form [{"ID": ..., "VALUE": ...}]
	// (XML-RPC struct array). Same semantic sentinel.
	specialList := `[{"ID": "NOT_USED", "VALUE": 0.0}]`

	cases := []struct {
		name     string
		desc     hmproto.ParameterData
		raw      float64
		accepted bool
	}{
		{"special_below_min_object", floatDesc(t, 0.5, 15.5, specialObject), 0.0, true},
		{"special_below_min_list", floatDesc(t, 0.5, 15.5, specialList), 0.0, true},
		{"normal_below_min", floatDesc(t, 0.5, 15.5, specialObject), 0.4, false},
		{"normal_above_max", floatDesc(t, 0.5, 15.5, specialObject), 99.0, false},
		{"normal_in_range", floatDesc(t, 0.5, 15.5, specialObject), 10.0, true},
		{"no_special_below_min", floatDesc(t, 0.5, 15.5, ""), 0.0, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, coerceErr := parameter.Coerce(tc.desc, tc.raw)
			coerceAccepted := coerceErr == nil
			if coerceAccepted != tc.accepted {
				t.Fatalf("Coerce(%.2f) = accepted %v, want %v (err=%v)",
					tc.raw, coerceAccepted, tc.accepted, coerceErr)
			}

			// The validation path must reach the identical verdict on the
			// same numeric input, independent of Coerce's success.
			validateErr := parameter.Validate(tc.desc, hmtypes.FloatValue(tc.raw))
			validateAccepted := validateErr == nil
			if validateAccepted != tc.accepted {
				t.Fatalf("Coerce and Validate disagree for %.2f: coerce=%v validate=%v",
					tc.raw, coerceAccepted, validateAccepted)
			}
		})
	}
}

func TestCoerceAcceptsSpecialAboveMaxInteger(t *testing.T) {
	t.Parallel()
	// PERMANENT = 255 sits above MAX = 254 (HM CONF_BUTTON_TIME shape).
	desc := intDesc(t, 1, 254, `{"PERMANENT": 255}`)
	if _, err := parameter.Coerce(desc, 255); err != nil {
		t.Fatalf("Coerce(255) must accept the declared SPECIAL sentinel: %v", err)
	}
	if _, err := parameter.Coerce(desc, 300); err == nil {
		t.Fatal("Coerce(300) must reject a non-special value above MAX")
	}
}

func TestMatchesSpecialValueWireFormats(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		special string
		v       float64
		want    bool
	}{
		{"object_match", `{"NOT_USED": 0.0}`, 0.0, true},
		{"object_no_match", `{"NOT_USED": 0.0}`, 1.0, false},
		{"list_match", `[{"ID": "NOT_USED", "VALUE": 111600.0}]`, 111600.0, true},
		{"list_no_match", `[{"ID": "NOT_USED", "VALUE": 111600.0}]`, 0.0, false},
		{"object_mixed_with_optional_marker", `{"NOT_USED": 0.0, "OPTIONAL": true}`, 0.0, true},
		{"object_optional_marker_not_numeric", `{"OPTIONAL": true}`, 1.0, false},
		{"empty", ``, 0.0, false},
		{"malformed", `{not-json`, 0.0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			desc := hmproto.ParameterData{}
			if tc.special != "" {
				desc.Special = json.RawMessage(tc.special)
			}
			if got := parameter.MatchesSpecialValue(desc, tc.v); got != tc.want {
				t.Errorf("MatchesSpecialValue(%s, %v) = %v, want %v", tc.special, tc.v, got, tc.want)
			}
		})
	}
}
