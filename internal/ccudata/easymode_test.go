// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package ccudata

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadEasymodeEmptyPath(t *testing.T) {
	if _, err := LoadEasymode(""); !errors.Is(err, ErrNoEasymode) {
		t.Fatalf("err=%v", err)
	}
}

func TestEmptyEasymodeMapsInit(t *testing.T) {
	e := EmptyEasymode()
	if e.ChannelMetadata == nil || e.OptionPresets == nil {
		t.Fatal("maps must be preallocated")
	}
}

func TestLoadEasymodeRoundTrip(t *testing.T) {
	tmp := filepath.Join(t.TempDir(), "em.json.gz")
	em := Easymode{
		ChannelMetadata: map[string]ChannelMetadata{
			"SHUTTER_TRANSMITTER": {
				ChannelType: "SHUTTER_TRANSMITTER",
				SenderTypes: map[string]SenderTypeMetadata{
					"_MASTER": {
						ParameterOrder: []string{"ON_TIME", "RAMP_TIME"},
						ParameterGroups: []ParameterGroupDef{
							{ID: "timing", LabelKey: "grp.timing", Parameters: []string{"ON_TIME", "RAMP_TIME"}},
						},
						OptionPresets: map[string]string{"MODE": "_INLINE_MODE"},
					},
				},
			},
		},
		OptionPresets: map[string]OptionPreset{
			"BOOL": {ID: "BOOL", Options: []OptionPresetVal{{Value: true, Label: "yes"}, {Value: false, Label: "no"}}},
		},
		CrossValidations: CrossValidationSet{
			Rules: []CrossValidation{
				{ID: "r1", AppliesToParams: []string{"ON_TIME", "RAMP_TIME"}, ParamA: "ON_TIME", ParamB: "RAMP_TIME", Rule: "gte"},
			},
		},
	}

	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	_ = json.NewEncoder(gz).Encode(em)
	_ = gz.Close()
	if err := os.WriteFile(tmp, buf.Bytes(), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	loaded, err := LoadEasymode(tmp)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	ch := loaded.Channel("SHUTTER_TRANSMITTER")
	if ch.ChannelType != "SHUTTER_TRANSMITTER" {
		t.Fatalf("channel=%+v", ch)
	}
	if _, ok := loaded.Preset("BOOL"); !ok {
		t.Fatal("preset missing")
	}
	if len(loaded.CrossValidations.Rules) != 1 {
		t.Fatalf("cross validations=%d", len(loaded.CrossValidations.Rules))
	}
}

func TestChannelZeroValueOnMiss(t *testing.T) {
	e := EmptyEasymode()
	ch := e.Channel("not-there")
	if ch.ChannelType != "" {
		t.Fatalf("zero expected, got %+v", ch)
	}
}

func TestCrossValidationExtendedFields(t *testing.T) {
	cv := CrossValidation{
		ID:              "between-rule",
		AppliesToParams: []string{"A", "B", "C"},
		Rule:            "between",
		Param:           "A",
		MinParam:        "B",
		MaxParam:        "C",
	}
	b, err := json.Marshal(cv)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got CrossValidation
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Param != "A" || got.MinParam != "B" || got.MaxParam != "C" {
		t.Fatalf("extended fields not preserved: %+v", got)
	}
	if got.ParamA != "" || got.ParamB != "" {
		t.Fatalf("unused ParamA/ParamB must be empty in between-rule: %+v", got)
	}
}

func TestOptionPresetAllowCustom(t *testing.T) {
	op := OptionPreset{
		ID:          "MY_PRESET",
		AllowCustom: true,
		Options:     []OptionPresetVal{{Value: 1, Label: "one"}},
	}
	b, err := json.Marshal(op)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got OptionPreset
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !got.AllowCustom {
		t.Fatal("AllowCustom not preserved after round-trip")
	}
}
