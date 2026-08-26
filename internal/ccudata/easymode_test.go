// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

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

// TestMaterializeSubsetGroupIDs covers the derivation that runs on every
// easymode load: a sender type that defines subsets but ships no
// pre-computed group ids gets one group per subset, and every member
// parameter is mapped to "subset_<first member>" — the same id the UI-schema
// builder gives the group derived from that SubsetDef, so a consumer can
// resolve a parameter to its group inside one payload. Archives that already
// carry the ids are left alone, because overwriting them would discard
// whatever the extractor decided.
func TestMaterializeSubsetGroupIDs(t *testing.T) {
	t.Parallel()

	t.Run("derives ids from subsets", func(t *testing.T) {
		t.Parallel()
		e := &Easymode{ChannelMetadata: map[string]ChannelMetadata{
			"SWITCH_VIRTUAL_RECEIVER": {SenderTypes: map[string]SenderTypeMetadata{
				"KEY_TRANSCEIVER": {Subsets: []SubsetDef{
					{ID: 1, MemberParams: []string{"SHORT_ON_TIME", "SHORT_OFF_TIME"}},
					{ID: 7, MemberParams: []string{"LONG_ON_TIME"}},
				}},
			}},
		}}
		materializeSubsetGroupIDs(e)

		got := e.ChannelMetadata["SWITCH_VIRTUAL_RECEIVER"].SenderTypes["KEY_TRANSCEIVER"].SubsetGroupIDs
		want := map[string]string{
			"SHORT_ON_TIME":  "subset_SHORT_ON_TIME",
			"SHORT_OFF_TIME": "subset_SHORT_ON_TIME",
			"LONG_ON_TIME":   "subset_LONG_ON_TIME",
		}
		if len(got) != len(want) {
			t.Fatalf("got %d group ids, want %d: %v", len(got), len(want), got)
		}
		for param, group := range want {
			if got[param] != group {
				t.Errorf("%s = %q, want %q", param, got[param], group)
			}
		}
	})

	t.Run("keeps ids the archive already carries", func(t *testing.T) {
		t.Parallel()
		e := &Easymode{ChannelMetadata: map[string]ChannelMetadata{
			"DIMMER": {SenderTypes: map[string]SenderTypeMetadata{
				"KEY_TRANSCEIVER": {
					Subsets:        []SubsetDef{{ID: 1, MemberParams: []string{"LEVEL"}}},
					SubsetGroupIDs: map[string]string{"LEVEL": "curated"},
				},
			}},
		}}
		materializeSubsetGroupIDs(e)

		if got := e.ChannelMetadata["DIMMER"].SenderTypes["KEY_TRANSCEIVER"].SubsetGroupIDs["LEVEL"]; got != "curated" {
			t.Errorf("LEVEL = %q, want the archive's own %q", got, "curated")
		}
	})

	t.Run("leaves a sender type without subsets untouched", func(t *testing.T) {
		t.Parallel()
		e := &Easymode{ChannelMetadata: map[string]ChannelMetadata{
			"SWITCH": {SenderTypes: map[string]SenderTypeMetadata{
				"KEY_TRANSCEIVER": {ParameterOrder: []string{"STATE"}},
			}},
		}}
		materializeSubsetGroupIDs(e)

		if ids := e.ChannelMetadata["SWITCH"].SenderTypes["KEY_TRANSCEIVER"].SubsetGroupIDs; ids != nil {
			t.Errorf("SubsetGroupIDs = %v, want nil", ids)
		}
	})
}

// TestLoadEasymodeRejectsUnusableInput covers the failure paths of the
// on-disk loader: the operator-supplied path is not trusted to exist, to be
// gzip, or to hold the expected JSON.
func TestLoadEasymodeRejectsUnusableInput(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	notGzip := filepath.Join(dir, "plain.json.gz")
	if err := os.WriteFile(notGzip, []byte(`{"channel_metadata":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	badJSON := filepath.Join(dir, "bad.json.gz")
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	if _, err := gz.Write([]byte("not json at all")); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(badJSON, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name string
		path string
	}{
		{"empty path", ""},
		{"missing file", filepath.Join(dir, "absent.json.gz")},
		{"not gzip", notGzip},
		{"gzip holding non-JSON", badJSON},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if _, err := LoadEasymode(tc.path); err == nil {
				t.Errorf("LoadEasymode(%q) succeeded, want an error", tc.path)
			}
		})
	}
}
