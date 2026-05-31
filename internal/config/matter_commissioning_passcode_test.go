// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package config

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

// strictDecode mirrors the REST section-PUT validation path
// (handlers.strictUnmarshal): a JSON decoder with DisallowUnknownFields.
func strictDecode(t *testing.T, raw string, target any) error {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader([]byte(raw)))
	dec.DisallowUnknownFields()
	return dec.Decode(target)
}

// TestNorthMatterCommissioning_PasscodeAcceptsStringOrNumber locks the fix
// for the config-UI 400: the SPA renders the setup code as a secret text
// field and PUTs commissioning.passcode as a JSON string, which must not
// fail strict decoding into the uint32 field.
func TestNorthMatterCommissioning_PasscodeAcceptsStringOrNumber(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want uint32
	}{
		{"string", `{"passcode":"20202021"}`, 20202021},
		{"number", `{"passcode":20202021}`, 20202021},
		{"leading-zeros string", `{"passcode":"00020202"}`, 20202},
		{"empty string -> 0", `{"passcode":""}`, 0},
		{"null -> 0", `{"passcode":null}`, 0},
		{"absent -> 0", `{"salt":"abc"}`, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var c NorthMatterCommissioning
			if err := strictDecode(t, tc.raw, &c); err != nil {
				t.Fatalf("decode %q: %v", tc.raw, err)
			}
			if c.Passcode != tc.want {
				t.Fatalf("Passcode = %d, want %d", c.Passcode, tc.want)
			}
		})
	}
}

// TestNorthMatterCommissioning_PasscodeRejectsGarbage verifies a
// non-numeric string is still an error (the field is a numeric code).
func TestNorthMatterCommissioning_PasscodeRejectsGarbage(t *testing.T) {
	var c NorthMatterCommissioning
	err := strictDecode(t, `{"passcode":"not-a-number"}`, &c)
	if err == nil {
		t.Fatal("expected error for non-numeric passcode string, got nil")
	}
}

// TestNorthMatterCommissioning_RejectsUnknownField confirms the custom
// UnmarshalJSON preserves the strict section-validation contract: unknown
// keys inside the commissioning object are still rejected.
func TestNorthMatterCommissioning_RejectsUnknownField(t *testing.T) {
	var c NorthMatterCommissioning
	err := strictDecode(t, `{"passcode":"1","bogus":true}`, &c)
	if err == nil || !strings.Contains(err.Error(), "bogus") {
		t.Fatalf("expected unknown-field error mentioning 'bogus', got %v", err)
	}
}

// TestNorthMatter_SectionPutWithStringPasscode exercises the full section
// shape the REST handler decodes (north.matter), with the nested
// commissioning passcode quoted — the exact payload that previously 400'd.
func TestNorthMatter_SectionPutWithStringPasscode(t *testing.T) {
	raw := `{"commissioning":{"passcode":"20202021","iterations":1000}}`
	var m NorthMatter
	if err := strictDecode(t, raw, &m); err != nil {
		t.Fatalf("decode north.matter section: %v", err)
	}
	if m.Commissioning.Passcode != 20202021 {
		t.Fatalf("Passcode = %d, want 20202021", m.Commissioning.Passcode)
	}
	if m.Commissioning.Iterations != 1000 {
		t.Fatalf("Iterations = %d, want 1000", m.Commissioning.Iterations)
	}
}
