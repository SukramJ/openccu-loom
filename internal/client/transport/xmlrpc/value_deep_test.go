// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package xmlrpc

// value_deep_test.go contains additional codec edge-case tests that
// complement value_test.go. Before adding new cases here, check
// value_test.go to avoid duplication.
//
// Covered by existing value_test.go and therefore SKIPPED here:
// TestLeafValueMarshalling — NilValue marshal form (<value><nil></nil></value>)
// TestStructGetMissing — StructValue.Get returns !ok for unknown key
// TestDateTimeMarshalling — ISO8601 format (2026-04-23T10:00:00 case)
// TestRoundTripLeafKinds — Base64Value round-trip
// TestDecodeStructRoundTrip — nested struct round-trip via marshal+decode

import (
	"bytes"
	"encoding/xml"
	"strings"
	"testing"
	"time"
)

// TestStructValueGetCaseSensitive verifies that StructValue.Get treats
// key names as case-sensitive. The CCU exposes ADDRESS, LEVEL, etc., in
// exact uppercase; a caller using the wrong case should get !ok, not a
// silent mismatch.
func TestStructValueGetCaseSensitive(t *testing.T) {
	t.Parallel()
	s := StructValue{Members: []Member{
		{Name: "LEVEL", Value: IntValue(5)},
	}}
	if _, ok := s.Get("LEVEL"); !ok {
		t.Error("Get('LEVEL') should succeed")
	}
	if _, ok := s.Get("level"); ok {
		t.Error("Get('level') should fail — keys are case-sensitive")
	}
	if _, ok := s.Get("Level"); ok {
		t.Error("Get('Level') should fail — keys are case-sensitive")
	}
}

// TestArrayValueMarshalEmpty verifies that an empty ArrayValue produces
// a structurally valid <array><data></data></array> without panicking,
// and round-trips back to an empty ArrayValue.
func TestArrayValueMarshalEmpty(t *testing.T) {
	t.Parallel()
	got := marshalToString(t, ArrayValue{})
	// Must contain the array/data framing.
	if !strings.Contains(got, "<array>") {
		t.Errorf("empty array missing <array>: %q", got)
	}
	if !strings.Contains(got, "<data>") {
		t.Errorf("empty array missing <data>: %q", got)
	}
	// Must round-trip through the decoder.
	dec := xml.NewDecoder(strings.NewReader(got))
	start, err := findStart(dec, "value")
	if err != nil {
		t.Fatalf("findStart: %v", err)
	}
	v, err := DecodeValue(dec, start)
	if err != nil {
		t.Fatalf("DecodeValue: %v", err)
	}
	arr, err := AsArray(v)
	if err != nil {
		t.Fatalf("AsArray: %v", err)
	}
	if len(arr) != 0 {
		t.Errorf("empty array round-trip: got %d elements, want 0", len(arr))
	}
}

// TestStructValueMarshalNestedStruct verifies that a StructValue whose
// member value is itself a StructValue round-trips through
// marshal→decode with the nested shape intact.
func TestStructValueMarshalNestedStruct(t *testing.T) {
	t.Parallel()
	inner := StructValue{Members: []Member{
		{Name: "x", Value: IntValue(99)},
	}}
	outer := StructValue{Members: []Member{
		{Name: "inner", Value: inner},
	}}
	raw := marshalToString(t, outer)
	dec := xml.NewDecoder(strings.NewReader(raw))
	start, err := findStart(dec, "value")
	if err != nil {
		t.Fatalf("findStart: %v", err)
	}
	got, err := DecodeValue(dec, start)
	if err != nil {
		t.Fatalf("DecodeValue: %v", err)
	}
	outerS, err := AsStruct(got)
	if err != nil {
		t.Fatalf("AsStruct outer: %v", err)
	}
	innerRaw, ok := outerS.Get("inner")
	if !ok {
		t.Fatal("decoded struct missing key 'inner'")
	}
	innerS, err := AsStruct(innerRaw)
	if err != nil {
		t.Fatalf("AsStruct inner: %v", err)
	}
	xVal, err := StructField[IntValue](innerS, "x")
	if err != nil {
		t.Fatalf("StructField 'x': %v", err)
	}
	if xVal != 99 {
		t.Errorf("inner.x = %d, want 99", xVal)
	}
}

// TestDateTimeValueMarshalsISO8601 verifies the exact layout
// "YYYYMMDDTHH:MM:SS" used by the CCU, for the specific timestamp
// 2026-04-28T12:00:00 UTC. The existing TestDateTimeMarshalling
// uses 2026-04-23T10:00:00 UTC; this case exercises a different
// wall-clock instant to guard against accidental date-hardcoding.
func TestDateTimeValueMarshalsISO8601(t *testing.T) {
	t.Parallel()
	ts := time.Date(2026, 4, 28, 12, 0, 0, 0, time.UTC)
	got := marshalToString(t, DateTimeValue(ts))
	want := "<value><dateTime.iso8601>20260428T12:00:00</dateTime.iso8601></value>"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// TestBase64ValueRoundTripsBinary verifies that bytes containing
// null (0x00) and high-byte values (0xff, 0xfe) survive the
// marshal→decode round-trip without corruption. The existing
// TestRoundTripLeafKinds tests Base64Value([]byte("abc")) which is
// all ASCII; this test targets non-printable bytes.
func TestBase64ValueRoundTripsBinary(t *testing.T) {
	t.Parallel()
	input := Base64Value([]byte{0x00, 0xff, 0xfe, 0x01, 0x7f})
	raw := marshalToString(t, input)
	dec := xml.NewDecoder(strings.NewReader(raw))
	start, err := findStart(dec, "value")
	if err != nil {
		t.Fatalf("findStart: %v", err)
	}
	got, err := DecodeValue(dec, start)
	if err != nil {
		t.Fatalf("DecodeValue: %v", err)
	}
	gotBytes, err := AsBytes(got)
	if err != nil {
		t.Fatalf("AsBytes: %v", err)
	}
	if !bytes.Equal(gotBytes, []byte(input)) {
		t.Errorf("round-trip mismatch: got %v, want %v", gotBytes, []byte(input))
	}
}

// TestStringValueWithControlCharsEscaped verifies that a StringValue
// containing XML-special characters (<, >, &) is properly escaped on
// marshal and decoded back to the original string.
func TestStringValueWithControlCharsEscaped(t *testing.T) {
	t.Parallel()
	original := `<script>alert("&boom&")</script>`
	raw := marshalToString(t, StringValue(original))

	// The raw XML must NOT contain the unescaped < or & (other than in XML framing).
	// We strip the known-good wrapper first, then verify no raw specials remain.
	inner := raw
	inner = strings.ReplaceAll(inner, "<value>", "")
	inner = strings.ReplaceAll(inner, "</value>", "")
	inner = strings.ReplaceAll(inner, "<string>", "")
	inner = strings.ReplaceAll(inner, "</string>", "")
	// After stripping framing, only escaped forms should remain.
	if strings.ContainsAny(inner, "<>&") {
		// Allow &amp;, &lt;, &gt; encoded forms but not raw characters.
		// A raw < in the content (not framing) would be a bug.
		if strings.Contains(inner, "<s") || strings.Contains(inner, "< ") {
			t.Errorf("unescaped '<' in marshalled string content: %q", raw)
		}
	}

	// Round-trip must recover the original string.
	dec := xml.NewDecoder(strings.NewReader(raw))
	start, err := findStart(dec, "value")
	if err != nil {
		t.Fatalf("findStart: %v", err)
	}
	got, err := DecodeValue(dec, start)
	if err != nil {
		t.Fatalf("DecodeValue: %v", err)
	}
	s, err := AsString(got)
	if err != nil {
		t.Fatalf("AsString: %v", err)
	}
	if s != original {
		t.Errorf("round-trip mismatch:\ngot  %q\nwant %q", s, original)
	}
}

// TestKindStringer verifies that every exported Kind constant has a
// non-empty String() that does not fall back to "unknown". This guards
// against new Kind values being added without updating the switch in
// value.go.
func TestKindStringer(t *testing.T) {
	t.Parallel()
	kinds := []Kind{
		KindNil,
		KindInt,
		KindBool,
		KindString,
		KindDouble,
		KindDateTime,
		KindBase64,
		KindStruct,
		KindArray,
	}
	for _, k := range kinds {
		s := k.String()
		if s == "" {
			t.Errorf("Kind(%d).String() = empty string", int(k))
		}
		if s == "unknown" {
			t.Errorf("Kind(%d).String() = %q — not mapped in switch", int(k), s)
		}
	}
}

// TestFormatStringification verifies that Format(v) produces stable,
// non-empty output for every concrete Value type, and spot-checks a
// few known shapes. The existing tests verify Kind() and MarshalXML;
// this test guards the debug-stringification path (stringify in value.go).
func TestFormatStringification(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		v       Value
		contain string // substring that must appear in the output
	}{
		{"nil", NilValue{}, "nil"},
		{"int zero", IntValue(0), "0"},
		{"int negative", IntValue(-7), "-7"},
		{"bool true", BoolValue(true), "true"},
		{"bool false", BoolValue(false), "false"},
		{"string", StringValue("world"), "world"},
		{"double", DoubleValue(2.5), "2.5"},
		{"datetime", DateTimeValue(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)), "20260101"},
		{"base64", Base64Value([]byte("abc")), "YWJj"},
		{"struct", StructValue{Members: []Member{{Name: "k", Value: IntValue(3)}}}, "k:3"},
		{"array", ArrayValue{IntValue(1), IntValue(2)}, "["},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			s := Format(c.v)
			if s == "" {
				t.Errorf("Format(%T) returned empty string", c.v)
			}
			if c.contain != "" && !strings.Contains(s, c.contain) {
				t.Errorf("Format(%T) = %q, want substring %q", c.v, s, c.contain)
			}
		})
	}
}
