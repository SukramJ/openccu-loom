// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package generic

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/parameter"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
)

// coercionGoldenDoc is the runtime-coercion parity golden emitted from the
// reference CCU library at HEAD. The typed value is the reference's real
// model/support.py convert_value output; in_range is a faithful transcription
// of model/data_point.py:900-934 is_value_in_range (pure MIN/MAX + enum
// membership, with NO SPECIAL bypass). The fixture is self-contained — the Go
// test below re-runs loom's wire→typed conversion (parameter.ConvertReadValue)
// and range check (validateRange) over the identical inputs and asserts
// equality, so no runtime Python dependency is required.
type coercionGoldenDoc struct {
	Provenance string               `json:"_provenance"`
	Cases      []coercionGoldenCase `json:"cases"`
}

type coercionGoldenScalar struct {
	Kind string          `json:"kind"`
	V    json.RawMessage `json:"v"`
}

type coercionGoldenCase struct {
	Name           string               `json:"name"`
	Type           string               `json:"type"`
	ValueList      []string             `json:"value_list"`
	Min            *float64             `json:"min"`
	Max            *float64             `json:"max"`
	Special        json.RawMessage      `json:"special"`
	Raw            coercionGoldenScalar `json:"raw"`
	Expected       coercionGoldenScalar `json:"expected"`
	InRange        bool                 `json:"in_range"`
	CompareInRange bool                 `json:"compare_in_range"`
	Note           string               `json:"note"`
}

// TestRuntimeCoercionParityGolden replays the reference golden against loom's
// wire→typed conversion and range validity. It proves that, for a table of
// (parameter metadata + raw wire value) cases spanning
// ACTION/BOOL/ENUM/FLOAT/INTEGER/STRING including SPECIAL sentinels,
// empty-string, and value_list labels (OPEN/CLOSED), loom produces the same
// typed value and the same validity verdict as the reference.
//
// For declared SPECIAL sentinel values the fixture marks compare_in_range=false:
// the reference is_value_in_range applies pure MIN/MAX (so a special below MIN
// reads as out-of-range), whereas loom deliberately bypasses MIN/MAX for a
// declared special on every range-checking path (the CCU server itself keeps
// declared specials unclamped — the reference CCU simulator ccu.py
// _clamp_numeric_value). Those cases assert loom's bypass explicitly.
func TestRuntimeCoercionParityGolden(t *testing.T) {
	t.Parallel()

	path := filepath.Join("testdata", "coercion_parity_golden.json")
	buf, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	var doc coercionGoldenDoc
	if err := json.Unmarshal(buf, &doc); err != nil {
		t.Fatalf("decode golden: %v", err)
	}
	if len(doc.Cases) == 0 {
		t.Fatal("golden has no cases")
	}

	for _, c := range doc.Cases {
		t.Run(c.Name, func(t *testing.T) {
			t.Parallel()
			paramType := hmenum.ParameterType(c.Type)

			// 1. wire → typed value parity (loom ConvertReadValue vs
			//    the reference convert_value).
			gotTyped := parameter.ConvertReadValue(paramType, decodeGoldenScalar(t, c.Raw))
			wantTyped := decodeGoldenScalar(t, c.Expected)
			if !reflect.DeepEqual(gotTyped, wantTyped) {
				t.Fatalf("typed value mismatch (%s): got %#v (%T), want %#v (%T)",
					c.Note, gotTyped, gotTyped, wantTyped, wantTyped)
			}

			// 2. validity parity.
			desc := hmproto.ParameterData{
				Type:      paramType,
				ValueList: c.ValueList,
				Min:       floatToRaw(t, c.Min),
				Max:       floatToRaw(t, c.Max),
				Special:   normalizeSpecial(c.Special),
			}
			loomInRange := validateRange(desc, gotTyped) == nil
			if c.CompareInRange {
				if loomInRange != c.InRange {
					t.Fatalf("in-range mismatch (%s): loom=%v, reference=%v",
						c.Note, loomInRange, c.InRange)
				}
				return
			}
			// SPECIAL sentinel: reference pure-range says out-of-range,
			// loom bypasses. Assert the documented divergence holds.
			if c.InRange {
				t.Fatalf("golden bug: special case %q should be out-of-range under pure MIN/MAX", c.Name)
			}
			if !loomInRange {
				t.Fatalf("loom must bypass MIN/MAX for declared SPECIAL value (%s)", c.Note)
			}
		})
	}
}

// decodeGoldenScalar reconstructs the Go-native value the golden encodes,
// matching the dynamic type loom's converter emits: float64 / int / bool /
// string, or nil for the "none" kind.
func decodeGoldenScalar(t *testing.T, s coercionGoldenScalar) any {
	t.Helper()
	switch s.Kind {
	case "none":
		return nil
	case "float":
		var f float64
		if err := json.Unmarshal(s.V, &f); err != nil {
			t.Fatalf("decode float scalar: %v", err)
		}
		return f
	case "int":
		var f float64
		if err := json.Unmarshal(s.V, &f); err != nil {
			t.Fatalf("decode int scalar: %v", err)
		}
		return int(f)
	case "bool":
		var b bool
		if err := json.Unmarshal(s.V, &b); err != nil {
			t.Fatalf("decode bool scalar: %v", err)
		}
		return b
	case "string":
		var str string
		if err := json.Unmarshal(s.V, &str); err != nil {
			t.Fatalf("decode string scalar: %v", err)
		}
		return str
	default:
		t.Fatalf("unknown scalar kind %q", s.Kind)
		return nil
	}
}

func floatToRaw(t *testing.T, f *float64) json.RawMessage {
	t.Helper()
	if f == nil {
		return nil
	}
	return json.RawMessage(strconv.FormatFloat(*f, 'f', -1, 64))
}

// normalizeSpecial maps a JSON null (the fixture's "no special" marker) to a
// nil blob so the descriptor carries no SPECIAL entries.
func normalizeSpecial(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	return raw
}
