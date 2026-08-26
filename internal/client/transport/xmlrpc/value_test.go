// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package xmlrpc

import (
	"bytes"
	"encoding/xml"
	"strings"
	"testing"
	"time"
)

func marshalToString(t *testing.T, v Value) string {
	t.Helper()
	var buf bytes.Buffer
	enc := xml.NewEncoder(&buf)
	if err := v.MarshalXML(enc, xml.StartElement{Name: xml.Name{Local: "value"}}); err != nil {
		t.Fatalf("marshal %T: %v", v, err)
	}
	if err := enc.Flush(); err != nil {
		t.Fatalf("flush: %v", err)
	}
	return buf.String()
}

func TestLeafValueMarshalling(t *testing.T) {
	cases := []struct {
		name string
		v    Value
		want string
	}{
		{"nil", NilValue{}, "<value><nil></nil></value>"},
		{"int zero", IntValue(0), "<value><int>0</int></value>"},
		{"int negative", IntValue(-42), "<value><int>-42</int></value>"},
		{"bool true", BoolValue(true), "<value><boolean>1</boolean></value>"},
		{"bool false", BoolValue(false), "<value><boolean>0</boolean></value>"},
		{"string", StringValue("hello"), "<value><string>hello</string></value>"},
		{"double", DoubleValue(3.5), "<value><double>3.5</double></value>"},
		{"base64", Base64Value([]byte("abc")), "<value><base64>YWJj</base64></value>"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := marshalToString(t, c.v); got != c.want {
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}
}

func TestDateTimeMarshalling(t *testing.T) {
	ts := time.Date(2026, 4, 23, 10, 0, 0, 0, time.UTC)
	got := marshalToString(t, DateTimeValue(ts))
	want := "<value><dateTime.iso8601>20260423T10:00:00</dateTime.iso8601></value>"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestStructMarshalling(t *testing.T) {
	v := StructValue{Members: []Member{
		{Name: "ADDRESS", Value: StringValue("ABC")},
		{Name: "LEVEL", Value: IntValue(5)},
	}}
	got := marshalToString(t, v)
	if !strings.Contains(got, "<name>ADDRESS</name>") {
		t.Errorf("struct missing ADDRESS member: %s", got)
	}
	// Order must be preserved.
	if strings.Index(got, "ADDRESS") > strings.Index(got, "LEVEL") {
		t.Errorf("struct member order not preserved: %s", got)
	}
}

func TestArrayMarshalling(t *testing.T) {
	a := ArrayValue{IntValue(1), StringValue("x")}
	got := marshalToString(t, a)
	want := "<value><array><data><value><int>1</int></value><value><string>x</string></value></data></array></value>"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestStructMarshalFailsOnNilMember(t *testing.T) {
	v := StructValue{Members: []Member{{Name: "X", Value: nil}}}
	var buf bytes.Buffer
	enc := xml.NewEncoder(&buf)
	if err := v.MarshalXML(enc, xml.StartElement{}); err == nil {
		t.Fatal("expected error for nil member value")
	}
}

func TestArrayMarshalFailsOnNilElement(t *testing.T) {
	a := ArrayValue{IntValue(1), nil}
	var buf bytes.Buffer
	enc := xml.NewEncoder(&buf)
	if err := a.MarshalXML(enc, xml.StartElement{}); err == nil {
		t.Fatal("expected error for nil array element")
	}
}

func TestRoundTripLeafKinds(t *testing.T) {
	ts := time.Date(2026, 4, 23, 10, 0, 0, 0, time.UTC)
	inputs := []Value{
		NilValue{},
		IntValue(7),
		BoolValue(true),
		StringValue("hello"),
		DoubleValue(1.5),
		DateTimeValue(ts),
		Base64Value([]byte("abc")),
	}
	for _, in := range inputs {
		t.Run(in.Kind().String(), func(t *testing.T) {
			raw := marshalToString(t, in)
			dec := xml.NewDecoder(strings.NewReader(raw))
			start, err := findStart(dec, "value")
			if err != nil {
				t.Fatalf("findStart: %v", err)
			}
			got, err := DecodeValue(dec, start)
			if err != nil {
				t.Fatalf("DecodeValue: %v", err)
			}
			if got.Kind() != in.Kind() {
				t.Fatalf("kind: got %s, want %s", got.Kind(), in.Kind())
			}
			if Format(got) != Format(in) {
				t.Fatalf("format: got %s, want %s", Format(got), Format(in))
			}
		})
	}
}

func TestDecodeStringShorthand(t *testing.T) {
	raw := `<value>abc</value>`
	dec := xml.NewDecoder(strings.NewReader(raw))
	start, _ := findStart(dec, "value")
	v, err := DecodeValue(dec, start)
	if err != nil {
		t.Fatal(err)
	}
	s, err := AsString(v)
	if err != nil {
		t.Fatal(err)
	}
	if s != "abc" {
		t.Fatalf("got %q, want %q", s, "abc")
	}
}

func TestDecodeStructRoundTrip(t *testing.T) {
	in := StructValue{Members: []Member{
		{Name: "a", Value: IntValue(1)},
		{Name: "b", Value: ArrayValue{StringValue("x"), StringValue("y")}},
	}}
	raw := marshalToString(t, in)
	dec := xml.NewDecoder(strings.NewReader(raw))
	start, _ := findStart(dec, "value")
	got, err := DecodeValue(dec, start)
	if err != nil {
		t.Fatal(err)
	}
	s, err := AsStruct(got)
	if err != nil {
		t.Fatal(err)
	}
	a, err := StructField[IntValue](s, "a")
	if err != nil {
		t.Fatal(err)
	}
	if a != 1 {
		t.Fatalf("a=%d", a)
	}
	bArr, err := StructField[ArrayValue](s, "b")
	if err != nil {
		t.Fatal(err)
	}
	if len(bArr) != 2 {
		t.Fatalf("b len=%d", len(bArr))
	}
}

func TestDecodeUnknownKindFails(t *testing.T) {
	raw := `<value><weird>x</weird></value>`
	dec := xml.NewDecoder(strings.NewReader(raw))
	start, _ := findStart(dec, "value")
	if _, err := DecodeValue(dec, start); err == nil {
		t.Fatal("expected error for unknown kind")
	}
}

func TestExtractorsRejectWrongKind(t *testing.T) {
	if _, err := AsInt(StringValue("x")); err == nil {
		t.Error("AsInt should reject StringValue")
	}
	if _, err := AsString(IntValue(1)); err == nil {
		t.Error("AsString should reject IntValue")
	}
	if _, err := AsStruct(ArrayValue{}); err == nil {
		t.Error("AsStruct should reject ArrayValue")
	}
	if _, err := AsStrings(ArrayValue{IntValue(1)}); err == nil {
		t.Error("AsStrings should reject int element")
	}
}

func TestStructGetMissing(t *testing.T) {
	s := StructValue{Members: []Member{{Name: "a", Value: IntValue(1)}}}
	if _, ok := s.Get("zzz"); ok {
		t.Error("Get should return !ok for missing field")
	}
}

func TestStructFieldTypeMismatch(t *testing.T) {
	s := StructValue{Members: []Member{{Name: "n", Value: StringValue("x")}}}
	if _, err := StructField[IntValue](s, "n"); err == nil {
		t.Error("StructField should fail on type mismatch")
	}
}

// TestDecodeValueRejectsMalformed sweeps the negative-decode pathways in
// decode.go. Each fragment trips a specific guard: stray tags, missing
// member parts, garbage chardata for typed leaves. Adding a new error
// branch in DecodeValue should add one row here.
func TestDecodeValueRejectsMalformed(t *testing.T) {
	cases := []struct {
		name string
		raw  string
	}{
		{"struct stray child", `<value><struct><wat/></struct></value>`},
		{"struct member missing name", `<value><struct><member><value><i4>1</i4></value></member></struct></value>`},
		{"struct member missing value", `<value><struct><member><name>x</name></member></struct></value>`},
		{"struct member stray child", `<value><struct><member><wat/></member></struct></value>`},
		{"array stray child", `<value><array><wat/></array></value>`},
		{"array data stray value-child", `<value><array><data><wat/></data></array></value>`},
		{"int with chardata garbage", `<value><i4>oops</i4></value>`},
		{"int overflows int32", `<value><i4>9999999999</i4></value>`},
		{"boolean rejects two", `<value><boolean>2</boolean></value>`},
		{"boolean rejects yes", `<value><boolean>yes</boolean></value>`},
		{"double garbage", `<value><double>a</double></value>`},
		{"datetime garbage", `<value><dateTime.iso8601>not a time</dateTime.iso8601></value>`},
		{"base64 garbage", `<value><base64>!!!</base64></value>`},
		{"chardata-only typed leaf", `<value><i4>1<extra/></i4></value>`},
		{"unknown kind", `<value><weird>x</weird></value>`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dec := xml.NewDecoder(strings.NewReader(c.raw))
			start, err := findStart(dec, "value")
			if err != nil {
				t.Fatalf("findStart: %v", err)
			}
			if v, err := DecodeValue(dec, start); err == nil {
				t.Fatalf("expected error, got %T %v", v, v)
			}
		})
	}
}
