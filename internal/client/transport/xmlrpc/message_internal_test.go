// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package xmlrpc

import (
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmerr"
)

// TestDecodeFaultMissingValue exercises the "fault missing <value>"
// error path inside decodeFault — an empty <fault></fault>.
func TestDecodeFaultMissingValue(t *testing.T) {
	t.Parallel()

	raw := `<?xml version="1.0"?><methodResponse><fault></fault></methodResponse>`
	_, err := DecodeResponse(strings.NewReader(raw))
	if err == nil {
		t.Fatal("empty <fault> element must produce error (no <value>)")
	}
}

// TestDecodeFaultFaultCodeNotInt exercises the path where faultCode is
// not an IntValue — StructField[IntValue] rejects it.
func TestDecodeFaultFaultCodeNotInt(t *testing.T) {
	t.Parallel()

	// Embed a struct where faultCode is a string, not an int.
	raw := `<?xml version="1.0"?><methodResponse><fault>
<value><struct>
  <member><name>faultCode</name><value><string>bad</string></value></member>
  <member><name>faultString</name><value><string>msg</string></value></member>
</struct></value></fault></methodResponse>`
	_, err := DecodeResponse(strings.NewReader(raw))
	if err == nil {
		t.Fatal("fault with non-int faultCode must produce error")
	}
}

// TestMarshalBytesError exercises the error path of MarshalBytes by
// passing a nil MethodCall.
func TestMarshalBytesError(t *testing.T) {
	t.Parallel()

	if _, err := MarshalBytes(nil); err == nil {
		t.Fatal("MarshalBytes(nil) must return error")
	}
}

// TestDecodeCallUnexpectedEndElement verifies that an unexpected EndElement
// while parsing a methodCall value returns an error.
func TestDecodeCallUnexpectedEndElement(t *testing.T) {
	t.Parallel()

	// A methodCall with a stray </extra> before </methodCall>.
	raw := `<?xml version="1.0"?><methodCall><methodName>noop</methodName></extra></methodCall>`
	_, err := DecodeCall(strings.NewReader(raw))
	if err == nil {
		t.Fatal("stray </extra> should produce error")
	}
}

// TestFindStartUnexpectedEndElement exercises the EndElement case inside
// findStart — a document that starts immediately with a closing tag.
func TestFindStartUnexpectedEndElement(t *testing.T) {
	t.Parallel()

	raw := `<?xml version="1.0"?></methodCall>`
	dec := newDecoder(strings.NewReader(raw))
	if _, err := findStart(dec, "methodCall"); err == nil {
		t.Fatal("EndElement before StartElement must produce error in findStart")
	}
}

// TestEncodeCallWithNoParams exercises the zero-param path in encodeParams
// (just <params/> with no <param> children).
func TestEncodeCallWithNoParams(t *testing.T) {
	t.Parallel()

	mc := &MethodCall{Method: "noop", Params: nil}
	raw, err := MarshalBytes(mc)
	if err != nil {
		t.Fatalf("MarshalBytes: %v", err)
	}
	got, err := DecodeCall(strings.NewReader(string(raw)))
	if err != nil {
		t.Fatalf("DecodeCall: %v", err)
	}
	if got.Method != "noop" {
		t.Fatalf("method=%q, want noop", got.Method)
	}
	if len(got.Params) != 0 {
		t.Fatalf("params=%v, want empty", got.Params)
	}
}

// TestEncodeDecodeFaultWithProtocolCode exercises the full
// encodeFault → decodeFault path with a non-trivial fault code.
func TestEncodeDecodeFaultWithProtocolCode(t *testing.T) {
	t.Parallel()

	want := &hmerr.XMLRPCFault{Code: -32600, Message: "invalid request"}
	mr := &MethodResponse{Fault: want}
	got, decErr := DecodeResponse(strings.NewReader(encodeResponseToString(t, mr)))
	if decErr != nil {
		t.Fatalf("DecodeResponse: %v", decErr)
	}
	if got.Fault == nil {
		t.Fatal("expected Fault, got nil")
	}
	if got.Fault.Code != -32600 {
		t.Fatalf("Code=%d, want -32600", got.Fault.Code)
	}
}

// encodeResponseToString is a test helper that serialises a MethodResponse
// to a string using EncodeResponse.
func encodeResponseToString(t *testing.T, mr *MethodResponse) string {
	t.Helper()
	var buf strings.Builder
	if err := EncodeResponse(&buf, mr); err != nil {
		t.Fatalf("EncodeResponse: %v", err)
	}
	return buf.String()
}
