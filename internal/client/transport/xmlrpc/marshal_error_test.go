// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package xmlrpc

import (
	"bytes"
	"encoding/xml"
	"testing"
)

// TestStructValueMarshalXMLNilMemberDetected verifies that MarshalXML
// returns an error when a member value is nil (no writer needed).
func TestStructValueMarshalXMLNilMemberDetected(t *testing.T) {
	t.Parallel()

	sv := StructValue{Members: []Member{
		{Name: "A", Value: IntValue(1)},
		{Name: "B", Value: nil},
	}}
	var buf bytes.Buffer
	enc := xml.NewEncoder(&buf)
	if err := sv.MarshalXML(enc, xml.StartElement{}); err == nil {
		t.Fatal("MarshalXML with nil member value must return error")
	}
}

// TestArrayValueMarshalXMLWithElements exercises the happy path of
// ArrayValue.MarshalXML to increase coverage of the non-error branches.
func TestArrayValueMarshalXMLWithElements(t *testing.T) {
	t.Parallel()

	av := ArrayValue{StringValue("a"), IntValue(1), BoolValue(true)}
	var buf bytes.Buffer
	enc := xml.NewEncoder(&buf)
	if err := av.MarshalXML(enc, xml.StartElement{Name: xml.Name{Local: "value"}}); err != nil {
		t.Fatalf("ArrayValue.MarshalXML: %v", err)
	}
	if err := enc.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if buf.Len() == 0 {
		t.Fatal("ArrayValue.MarshalXML produced empty output")
	}
}
