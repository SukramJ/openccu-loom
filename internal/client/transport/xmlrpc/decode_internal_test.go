// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package xmlrpc

import (
	"encoding/xml"
	"strings"
	"testing"
)

// TestConsumeCloseOrSelfCloseStartElementError exercises the branch
// inside consumeCloseOrSelfClose that fires when an unexpected child
// StartElement appears (e.g. a non-empty <nil>).
func TestConsumeCloseOrSelfCloseStartElementError(t *testing.T) {
	t.Parallel()

	// Build a decoder whose next token after the nil start element is
	// another StartElement — this triggers the "must be empty" error.
	raw := `<nil><unexpected/></nil>`
	d := xml.NewDecoder(strings.NewReader(raw))
	// Consume the outer <nil> start element.
	tok, _ := d.Token()
	start := tok.(xml.StartElement)
	// consumeCloseOrSelfClose should see <unexpected> and error.
	if err := consumeCloseOrSelfClose(d, start); err == nil {
		t.Fatal("consumeCloseOrSelfClose must return error when child element appears")
	}
}

// TestDecodeArraySelfClosing exercises the EndElement (nil array) path
// inside decodeArray — an <array></array> with no <data> child.
func TestDecodeArraySelfClosing(t *testing.T) {
	t.Parallel()

	// Build a struct with an empty array member and decode it, which
	// exercises the array EndElement branch.
	raw := `<value><array></array></value>`
	got, err := decodeValueFromString(t, raw)
	if err != nil {
		t.Fatalf("decode empty array (no <data>): %v", err)
	}
	arr, err := AsArray(got)
	if err != nil {
		t.Fatalf("AsArray: %v", err)
	}
	if arr != nil {
		t.Fatalf("array with no <data> should decode to nil, got %v", arr)
	}
}

// TestStructFieldMissingMember exercises the "member not present" error
// path inside StructField.
func TestStructFieldMissingMember(t *testing.T) {
	t.Parallel()

	sv := StructValue{Members: []Member{{Name: "LEVEL", Value: IntValue(5)}}}
	if _, err := StructField[StringValue](sv, "MISSING"); err == nil {
		t.Fatal("StructField for absent member must return error")
	}
}

// TestStructFieldWrongType exercises the "wrong concrete type" error
// path inside StructField.
func TestStructFieldWrongType(t *testing.T) {
	t.Parallel()

	sv := StructValue{Members: []Member{{Name: "LEVEL", Value: IntValue(5)}}}
	// Ask for a StringValue but the member holds an IntValue.
	if _, err := StructField[StringValue](sv, "LEVEL"); err == nil {
		t.Fatal("StructField with wrong type must return error")
	}
}

// TestDecodeParamsUnexpectedElement exercises the "unexpected element
// inside <params>" error path in decodeParams (e.g. <params><foo/>).
func TestDecodeParamsUnexpectedElement(t *testing.T) {
	t.Parallel()

	raw := `<?xml version="1.0"?><methodCall><methodName>noop</methodName><params><foo/></params></methodCall>`
	if _, err := DecodeCall(strings.NewReader(raw)); err == nil {
		t.Fatal("unexpected element inside <params> must return error")
	}
}

// TestDecodeParamEmptyParam exercises the empty-<param/> → NilValue path.
func TestDecodeParamEmptyParam(t *testing.T) {
	t.Parallel()

	// CCU "void" reply: a <params> containing a bare empty <param/>.
	raw := `<?xml version="1.0"?><methodResponse><params><param/></params></methodResponse>`
	got, err := DecodeResponse(strings.NewReader(raw))
	if err != nil {
		t.Fatalf("DecodeResponse with empty <param/>: %v", err)
	}
	if len(got.Params) != 1 {
		t.Fatalf("params len=%d, want 1", len(got.Params))
	}
	if got.Params[0].Kind() != KindNil {
		t.Fatalf("kind=%v, want KindNil", got.Params[0].Kind())
	}
}
