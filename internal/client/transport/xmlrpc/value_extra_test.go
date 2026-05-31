// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package xmlrpc

import (
	"bytes"
	"encoding/xml"
	"testing"
	"time"
)

// TestDateTimeValueTime verifies the Time() convenience accessor.
func TestDateTimeValueTime(t *testing.T) {
	t.Parallel()

	ts := time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC)
	v := DateTimeValue(ts)
	if !v.Time().Equal(ts) {
		t.Fatalf("Time()=%v, want %v", v.Time(), ts)
	}
}

// TestStructValueKind verifies that StructValue.Kind returns KindStruct.
func TestStructValueKind(t *testing.T) {
	t.Parallel()

	sv := StructValue{}
	if sv.Kind() != KindStruct {
		t.Fatalf("Kind()=%v, want KindStruct", sv.Kind())
	}
}

// TestWriteTaggedEmptyContent exercises the path in writeTagged where
// content is empty (the <nil> self-close and similar cases that skip
// the CharData emission).
func TestWriteTaggedEmptyContent(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	enc := xml.NewEncoder(&buf)
	if err := writeTagged(enc, "string", ""); err != nil {
		t.Fatalf("writeTagged(empty): %v", err)
	}
	if err := enc.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	// Should produce <value><string></string></value>
	got := buf.String()
	if got == "" {
		t.Fatal("writeTagged produced empty output")
	}
}

// TestWriteBareElementEmptyContent exercises the empty-content path in
// writeBareElement (the <name/> case that can occur for unnamed members).
func TestWriteBareElementEmptyContent(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	enc := xml.NewEncoder(&buf)
	if err := writeBareElement(enc, "name", ""); err != nil {
		t.Fatalf("writeBareElement(empty): %v", err)
	}
	if err := enc.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if buf.Len() == 0 {
		t.Fatal("writeBareElement produced no output")
	}
}

// TestStructValueMarshalXMLNilMember exercises the nil-member guard
// inside StructValue.MarshalXML (line 214+).
func TestStructValueMarshalXMLNilMember(t *testing.T) {
	t.Parallel()

	sv := StructValue{Members: []Member{
		{Name: "A", Value: IntValue(1)},
		{Name: "B", Value: nil}, // nil triggers the guard
	}}
	var buf bytes.Buffer
	enc := xml.NewEncoder(&buf)
	if err := sv.MarshalXML(enc, xml.StartElement{Name: xml.Name{Local: "value"}}); err == nil {
		t.Fatal("MarshalXML with nil member value must return error")
	}
}

// TestArrayValueMarshalXMLEmpty exercises marshalling an empty ArrayValue.
func TestArrayValueMarshalXMLEmpty(t *testing.T) {
	t.Parallel()

	av := ArrayValue(nil)
	got := marshalToString(t, av)
	if got == "" {
		t.Fatal("ArrayValue(nil) marshal produced empty output")
	}
}

// TestKindStringKnownValues exercises Kind.String() for all defined
// Kind constants to ensure no drift in the string representations.
func TestKindStringKnownValues(t *testing.T) {
	t.Parallel()

	cases := []struct {
		k    Kind
		want string
	}{
		{KindNil, "nil"},
		{KindInt, "int"},
		{KindBool, "boolean"},
		{KindString, "string"},
		{KindDouble, "double"},
		{KindDateTime, "dateTime.iso8601"},
		{KindBase64, "base64"},
		{KindStruct, "struct"},
		{KindArray, "array"},
	}
	for _, tc := range cases {
		if got := tc.k.String(); got != tc.want {
			t.Errorf("Kind(%d).String()=%q, want %q", int(tc.k), got, tc.want)
		}
	}
}

// TestNilValueKindIsKindNil confirms NilValue.Kind() returns KindNil.
func TestNilValueKindIsKindNil(t *testing.T) {
	t.Parallel()

	v := NilValue{}
	got := v.Kind()
	if got != KindNil {
		t.Fatalf("NilValue.Kind()=%v, want KindNil", got)
	}
}
