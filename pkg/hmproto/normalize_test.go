// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package hmproto

import (
	"encoding/json"
	"maps"
	"reflect"
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

func TestNormalizeDeviceIsIdempotent(t *testing.T) {
	in := DeviceDescription{
		Address:   " ABC001  ",
		Type:      " HM-TEST  ",
		Children:  []string{"c2", "c1", "c3"},
		Paramsets: []string{"VALUES", "MASTER"},
		Firmware:  "1.0\n",
	}
	once := NormalizeDevice(in)
	twice := NormalizeDevice(once)
	if !reflect.DeepEqual(once, twice) {
		t.Fatalf("Normalize not idempotent:\nonce=%+v\ntwice=%+v", once, twice)
	}
	if once.Address != "ABC001" || once.Firmware != "1.0" {
		t.Errorf("trim missed: %+v", once)
	}
	if got := once.Paramsets; got[0] != "MASTER" || got[1] != "VALUES" {
		t.Errorf("paramsets not sorted: %v", got)
	}
	if got := once.Children; got[0] != "c1" || got[2] != "c3" {
		t.Errorf("children not sorted: %v", got)
	}
}

func TestNormalizeDevicePreservesIdentity(t *testing.T) {
	// Already-clean input round-trips unchanged (modulo map/slice
	// allocation which does not matter for value equality).
	in := DeviceDescription{
		Address:   "ABC",
		Type:      "HM-X",
		Paramsets: []string{"MASTER", "VALUES"},
		Children:  []string{"a", "b"},
	}
	out := NormalizeDevice(in)
	if out.Address != in.Address || out.Type != in.Type {
		t.Fatalf("fields mutated: %+v", out)
	}
}

func TestHashDeviceStableAcrossEquivalentInputs(t *testing.T) {
	a := DeviceDescription{Address: "ABC", Paramsets: []string{"MASTER", "VALUES"}}
	b := DeviceDescription{Address: "ABC", Paramsets: []string{"VALUES", "MASTER"}} // order differs
	ha, err := HashDevice(a)
	if err != nil {
		t.Fatal(err)
	}
	hb, err := HashDevice(b)
	if err != nil {
		t.Fatal(err)
	}
	if ha != hb {
		t.Fatalf("hashes differ for equivalent inputs: %q vs %q", ha, hb)
	}
}

func TestHashDeviceDetectsChange(t *testing.T) {
	a := DeviceDescription{Address: "ABC", Firmware: "1.0"}
	b := DeviceDescription{Address: "ABC", Firmware: "1.1"}
	ha, _ := HashDevice(a)
	hb, _ := HashDevice(b)
	if ha == hb {
		t.Fatal("hash must change when firmware changes")
	}
}

func TestNormalizeParameterCompactsJSON(t *testing.T) {
	in := ParameterData{
		Type:       hmenum.ParameterTypeFloat,
		Operations: hmenum.OperationsRead | hmenum.OperationsWrite,
		Min:        json.RawMessage(`  0.0  `),
		Max:        json.RawMessage(`100.5`),
		Default:    json.RawMessage(`  null `),
		Unit:       " °C  ",
	}
	out := NormalizeParameter(in)
	if out.Unit != "°C" {
		t.Errorf("unit=%q", out.Unit)
	}
	if string(out.Min) != "0.0" {
		t.Errorf("min=%q", out.Min)
	}
	if string(out.Default) != "null" {
		t.Errorf("default=%q", out.Default)
	}
	// Idempotent.
	once := out
	twice := NormalizeParameter(once)
	if !reflect.DeepEqual(once, twice) {
		t.Fatal("normalize parameter not idempotent")
	}
}

func TestHashParamsetIgnoresMapOrder(t *testing.T) {
	ps1 := Paramset{
		"LEVEL":           ParameterData{Type: hmenum.ParameterTypeFloat, Min: json.RawMessage("0")},
		"SET_TEMPERATURE": ParameterData{Type: hmenum.ParameterTypeFloat, Min: json.RawMessage("4.5")},
	}
	ps2 := Paramset{}
	maps.Copy(ps2, ps1)
	h1, err := HashParamset(ps1)
	if err != nil {
		t.Fatal(err)
	}
	h2, err := HashParamset(ps2)
	if err != nil {
		t.Fatal(err)
	}
	if h1 != h2 {
		t.Fatalf("paramset hash differs: %q vs %q", h1, h2)
	}
}

func TestHashParamsetDetectsAddedParameter(t *testing.T) {
	base := Paramset{"A": ParameterData{Type: hmenum.ParameterTypeBool}}
	extended := Paramset{
		"A": ParameterData{Type: hmenum.ParameterTypeBool},
		"B": ParameterData{Type: hmenum.ParameterTypeBool},
	}
	h1, _ := HashParamset(base)
	h2, _ := HashParamset(extended)
	if h1 == h2 {
		t.Fatal("adding a parameter must change the hash")
	}
}

func TestParameterDataBitmaskHelpers(t *testing.T) {
	p := ParameterData{
		Operations: hmenum.OperationsRead | hmenum.OperationsWrite,
		Flags:      hmenum.FlagVisible | hmenum.FlagService,
	}
	if !p.IsReadable() || !p.IsWritable() || p.IsEvent() {
		t.Error("operations helpers broken")
	}
	if !p.IsVisible() || !p.IsService() || p.IsInternal() {
		t.Error("flag helpers broken")
	}
}

func TestJSONRoundTripDeviceDescription(t *testing.T) {
	raw := []byte(`{"ADDRESS":"ABC:1","TYPE":"HM-X","PARAMSETS":["VALUES","MASTER"],"INDEX":3}`)
	var d DeviceDescription
	if err := json.Unmarshal(raw, &d); err != nil {
		t.Fatal(err)
	}
	if d.Address != "ABC:1" || d.Type != "HM-X" {
		t.Fatalf("decoded wrong: %+v", d)
	}
	if d.Index == nil || *d.Index != 3 {
		t.Fatalf("INDEX round-trip failed: %v", d.Index)
	}
	if len(d.Paramsets) != 2 {
		t.Fatalf("paramsets=%v", d.Paramsets)
	}
}

// TestHashParameterRoundTrip verifies that HashParameter is deterministic and
// changes when the input changes.
func TestHashParameterRoundTrip(t *testing.T) {
	p := ParameterData{
		Unit: "°C",
		Min:  json.RawMessage("0"),
		Max:  json.RawMessage("100"),
	}
	h1, err := HashParameter(p)
	if err != nil {
		t.Fatalf("HashParameter: %v", err)
	}
	if h1 == "" {
		t.Fatal("HashParameter returned empty string")
	}
	// Same input → same hash.
	h2, err := HashParameter(p)
	if err != nil {
		t.Fatalf("second HashParameter: %v", err)
	}
	if h1 != h2 {
		t.Fatalf("HashParameter not deterministic: %q vs %q", h1, h2)
	}
	// Different input → different hash.
	p.Unit = "K"
	h3, err := HashParameter(p)
	if err != nil {
		t.Fatalf("HashParameter after mutation: %v", err)
	}
	if h1 == h3 {
		t.Fatal("HashParameter should change when parameter changes")
	}
}

// TestSortedRawMapViaNormalizeParameterExtra exercises sortedRawMap through
// NormalizeParameter's Extra path.
func TestSortedRawMapViaNormalizeParameterExtra(t *testing.T) {
	p := ParameterData{
		Extra: map[string]json.RawMessage{
			"z_key": json.RawMessage(`{"x":1}`),
			"a_key": json.RawMessage(`null`),
		},
	}
	out := NormalizeParameter(p)
	if len(out.Extra) != 2 {
		t.Fatalf("sortedRawMap lost entries: %d", len(out.Extra))
	}
	// Keys should survive trimming (none here, but verify non-nil).
	for k := range out.Extra {
		if k == "" {
			t.Fatal("empty key after sortedRawMap")
		}
	}
}

// TestSortedRawMapViaDeviceExtra exercises sortedRawMap through NormalizeDevice.
func TestSortedRawMapViaDeviceExtra(t *testing.T) {
	d := DeviceDescription{
		Address: "TEST",
		Extra: map[string]json.RawMessage{
			" padded ": json.RawMessage("1"),
		},
	}
	out := NormalizeDevice(d)
	if _, ok := out.Extra["padded"]; !ok {
		t.Fatal("sortedRawMap should trim key whitespace")
	}
}

// TestLinkRolesMarshalNilSlice exercises the MarshalJSON path with a nil slice.
func TestLinkRolesMarshalNilSlice(t *testing.T) {
	var r LinkRoles
	b, err := json.Marshal(r)
	if err != nil {
		t.Fatalf("MarshalJSON: %v", err)
	}
	if string(b) != "null" {
		t.Fatalf("MarshalJSON(nil LinkRoles) = %q, want null", b)
	}
}

// TestLinkRolesUnmarshalEmptyString exercises the empty-string branch in UnmarshalJSON.
func TestLinkRolesUnmarshalEmptyString(t *testing.T) {
	var r LinkRoles
	if err := json.Unmarshal([]byte(`""`), &r); err != nil {
		t.Fatalf("UnmarshalJSON empty string: %v", err)
	}
	if r != nil {
		t.Fatalf("empty string should yield nil: %v", r)
	}
}

// TestLinkRolesUnmarshalNull exercises the null branch.
func TestLinkRolesUnmarshalNull(t *testing.T) {
	r := LinkRoles{"existing"}
	if err := json.Unmarshal([]byte(`null`), &r); err != nil {
		t.Fatalf("UnmarshalJSON null: %v", err)
	}
	if r != nil {
		t.Fatalf("null should yield nil: %v", r)
	}
}

// TestLinkRolesUnmarshalArray exercises the JSON array branch.
func TestLinkRolesUnmarshalArray(t *testing.T) {
	var r LinkRoles
	if err := json.Unmarshal([]byte(`["A","B"]`), &r); err != nil {
		t.Fatalf("UnmarshalJSON array: %v", err)
	}
	if len(r) != 2 || r[0] != "A" {
		t.Fatalf("UnmarshalJSON array result %v", r)
	}
}

// TestNormalizeParamsetEmpty exercises NormalizeParamset with an empty
// paramset to ensure no panic and correct output.
func TestNormalizeParamsetEmpty(t *testing.T) {
	out := NormalizeParamset(Paramset{})
	if len(out) != 0 {
		t.Fatalf("NormalizeParamset(empty) = %v", out)
	}
}

// TestHashParamsetEmpty verifies HashParamset on an empty paramset.
func TestHashParamsetEmpty(t *testing.T) {
	h, err := HashParamset(Paramset{})
	if err != nil {
		t.Fatalf("HashParamset(empty): %v", err)
	}
	if h == "" {
		t.Fatal("HashParamset(empty) returned empty string")
	}
}

// TestLinkRolesMarshalNonNil exercises the non-nil MarshalJSON path.
func TestLinkRolesMarshalNonNil(t *testing.T) {
	r := LinkRoles{"SWITCH", "KEYMATIC"}
	b, err := json.Marshal(r)
	if err != nil {
		t.Fatalf("MarshalJSON: %v", err)
	}
	var got []string
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("round-trip unmarshal: %v", err)
	}
	if len(got) != 2 || got[0] != "SWITCH" {
		t.Fatalf("round-trip result %v", got)
	}
}

// TestCompactJSONInvalidInput verifies that compactJSON preserves the
// original input verbatim when the JSON is malformed (e.g., an unterminated
// object). This exercises the `if err := json.Compact` error branch.
func TestCompactJSONInvalidInput(t *testing.T) {
	// compactJSON is unexported but accessible in the same package.
	bad := json.RawMessage(`{ "unclosed": `)
	got := compactJSON(bad)
	// The function preserves the input verbatim when compaction fails.
	if string(got) != string(bad) {
		t.Fatalf("compactJSON(bad JSON) = %q, want verbatim %q", got, bad)
	}
}

// TestLinkRolesUnmarshalMalformedArray verifies that UnmarshalJSON returns
// a non-nil error when the JSON is a malformed array literal. This exercises
// the `return err` path inside the json.Unmarshal(data, &arr) call.
func TestLinkRolesUnmarshalMalformedArray(t *testing.T) {
	var r LinkRoles
	err := json.Unmarshal([]byte(`[bad`), &r)
	if err == nil {
		t.Fatal("expected error for malformed array JSON, got nil")
	}
}

// TestNormalizeParameterWithValueList verifies the ValueList clone path in
// NormalizeParameter: non-nil ValueList is copied (not sorted).
func TestNormalizeParameterWithValueList(t *testing.T) {
	p := ParameterData{
		ValueList: []string{"CLOSED", "OPEN", "TILTED"},
	}
	out := NormalizeParameter(p)
	if len(out.ValueList) != 3 {
		t.Fatalf("ValueList length = %d, want 3", len(out.ValueList))
	}
	if out.ValueList[0] != "CLOSED" {
		t.Fatalf("ValueList[0] = %q, want CLOSED", out.ValueList[0])
	}
	// Verify the clone is independent from the original.
	p.ValueList[0] = "MUTATED"
	if out.ValueList[0] == "MUTATED" {
		t.Fatal("ValueList was not cloned — mutation affected output")
	}
}
