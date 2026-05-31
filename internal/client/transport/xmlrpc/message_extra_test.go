// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package xmlrpc

import (
	"bytes"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmerr"
)

// TestEncodeCallNilMethodCall verifies that nil is rejected with an error.
func TestEncodeCallNilMethodCall(t *testing.T) {
	t.Parallel()

	if err := EncodeCall(&bytes.Buffer{}, nil); err == nil {
		t.Fatal("EncodeCall(nil) must return error")
	}
}

// TestEncodeCallEmptyMethod verifies that an empty method name is rejected.
func TestEncodeCallEmptyMethod(t *testing.T) {
	t.Parallel()

	if err := EncodeCall(&bytes.Buffer{}, &MethodCall{Method: ""}); err == nil {
		t.Fatal("EncodeCall with empty Method must return error")
	}
}

// TestEncodeResponseNilResponse verifies that nil is rejected.
func TestEncodeResponseNilResponse(t *testing.T) {
	t.Parallel()

	if err := EncodeResponse(&bytes.Buffer{}, nil); err == nil {
		t.Fatal("EncodeResponse(nil) must return error")
	}
}

// TestEncodeDecodeResponseWithFault verifies the fault encode/decode round-trip.
func TestEncodeDecodeResponseWithFault(t *testing.T) {
	t.Parallel()

	fault := &hmerr.XMLRPCFault{Code: 7, Message: "method not found"}
	mr := &MethodResponse{Fault: fault}
	var buf bytes.Buffer
	if err := EncodeResponse(&buf, mr); err != nil {
		t.Fatalf("EncodeResponse(fault): %v", err)
	}
	got, err := DecodeResponse(&buf)
	if err != nil {
		t.Fatalf("DecodeResponse: %v", err)
	}
	if got.Fault == nil {
		t.Fatal("expected Fault, got nil")
	}
	if got.Fault.Code != 7 {
		t.Errorf("Code=%d, want 7", got.Fault.Code)
	}
	if got.Fault.Message != "method not found" {
		t.Errorf("Message=%q, want 'method not found'", got.Fault.Message)
	}
}

// TestEncodeDecodeResponseEmptyParams verifies that a MethodResponse with
// no params and no fault round-trips as an empty params set.
func TestEncodeDecodeResponseEmptyParams(t *testing.T) {
	t.Parallel()

	mr := &MethodResponse{} // no params, no fault
	var buf bytes.Buffer
	if err := EncodeResponse(&buf, mr); err != nil {
		t.Fatalf("EncodeResponse(empty): %v", err)
	}
	got, err := DecodeResponse(&buf)
	if err != nil {
		t.Fatalf("DecodeResponse: %v", err)
	}
	if got.Fault != nil {
		t.Fatalf("Fault=%v, want nil", got.Fault)
	}
	if len(got.Params) != 0 {
		t.Fatalf("Params=%v, want empty", got.Params)
	}
}

// TestDecodeCallMissingMethodName verifies that a <methodCall> without a
// <methodName> element is rejected.
func TestDecodeCallMissingMethodName(t *testing.T) {
	t.Parallel()

	xml := `<?xml version="1.0" encoding="UTF-8"?><methodCall><params/></methodCall>`
	_, err := DecodeCall(strings.NewReader(xml))
	if err == nil {
		t.Fatal("DecodeCall without methodName must return error")
	}
}

// TestDecodeCallUnexpectedElement verifies that an unknown child element
// inside <methodCall> produces an error.
func TestDecodeCallUnexpectedElement(t *testing.T) {
	t.Parallel()

	xml := `<?xml version="1.0"?><methodCall><unknown/></methodCall>`
	_, err := DecodeCall(strings.NewReader(xml))
	if err == nil {
		t.Fatal("unexpected element inside methodCall should produce error")
	}
}

// TestFindStartWrongRootElement verifies that findStart rejects a root
// element that does not match the expected name.
func TestFindStartWrongRootElement(t *testing.T) {
	t.Parallel()

	xml := `<?xml version="1.0"?><methodResponse/>`
	dec := newDecoder(strings.NewReader(xml))
	if _, err := findStart(dec, "methodCall"); err == nil {
		t.Fatal("findStart should return error when root element name differs")
	}
}

// TestDecodeResponseUnexpectedElement verifies that an unknown child
// inside <methodResponse> returns an error.
func TestDecodeResponseUnexpectedElement(t *testing.T) {
	t.Parallel()

	xml := `<?xml version="1.0"?><methodResponse><unknown/></methodResponse>`
	_, err := DecodeResponse(strings.NewReader(xml))
	if err == nil {
		t.Fatal("unexpected element inside methodResponse should produce error")
	}
}

// TestEncodeParamsNilParam verifies that encodeParams rejects a nil
// param element (index-preserving error message).
func TestEncodeParamsNilParam(t *testing.T) {
	t.Parallel()

	mr := &MethodResponse{Params: []Value{StringValue("ok"), nil, IntValue(1)}}
	if err := EncodeResponse(&bytes.Buffer{}, mr); err == nil {
		t.Fatal("EncodeResponse with nil param must return error")
	}
}

// TestMarshalBytesRoundTrip exercises the MarshalBytes convenience wrapper.
func TestMarshalBytesRoundTrip(t *testing.T) {
	t.Parallel()

	mc := &MethodCall{Method: "listDevices"}
	raw, err := MarshalBytes(mc)
	if err != nil {
		t.Fatalf("MarshalBytes: %v", err)
	}
	got, err := DecodeCall(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("DecodeCall: %v", err)
	}
	if got.Method != "listDevices" {
		t.Fatalf("method=%q, want listDevices", got.Method)
	}
}
