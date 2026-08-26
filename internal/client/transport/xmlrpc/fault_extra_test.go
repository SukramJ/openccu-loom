// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package xmlrpc

import (
	"strings"
	"testing"
)

// TestDecodeFaultMissingFaultString exercises the path where faultCode
// is present and is an int, but faultString is absent from the struct.
func TestDecodeFaultMissingFaultString(t *testing.T) {
	t.Parallel()

	raw := `<?xml version="1.0"?><methodResponse><fault>
<value><struct>
  <member><name>faultCode</name><value><i4>-1</i4></value></member>
</struct></value></fault></methodResponse>`
	_, err := DecodeResponse(strings.NewReader(raw))
	if err == nil {
		t.Fatal("fault without faultString must produce error")
	}
}

// TestDecodeFaultUnexpectedElement exercises the "unexpected <X>" path
// inside decodeFault when the element is neither <value> nor </fault>.
func TestDecodeFaultUnexpectedElement(t *testing.T) {
	t.Parallel()

	// <fault> contains <badchild> instead of <value>.
	raw := `<?xml version="1.0"?><methodResponse><fault><badchild/></fault></methodResponse>`
	_, err := DecodeResponse(strings.NewReader(raw))
	if err == nil {
		t.Fatal("unexpected element inside <fault> must produce error")
	}
}

// TestEncodeCallWithAllParamTypes exercises EncodeCall with a variety
// of param types to cover the encodeParams → writeTagged branches.
func TestEncodeCallWithAllParamTypes(t *testing.T) {
	t.Parallel()

	mc := &MethodCall{
		Method: "testAll",
		Params: []Value{
			NilValue{},
			IntValue(42),
			BoolValue(true),
			StringValue("hello"),
			DoubleValue(3.14),
			Base64Value([]byte{0x01, 0x02}),
			ArrayValue{StringValue("a"), StringValue("b")},
			StructValue{Members: []Member{
				{Name: "key", Value: StringValue("value")},
			}},
		},
	}
	raw, err := MarshalBytes(mc)
	if err != nil {
		t.Fatalf("MarshalBytes: %v", err)
	}
	got, err := DecodeCall(strings.NewReader(string(raw)))
	if err != nil {
		t.Fatalf("DecodeCall: %v", err)
	}
	if got.Method != "testAll" {
		t.Fatalf("method=%q, want testAll", got.Method)
	}
	if len(got.Params) != len(mc.Params) {
		t.Fatalf("params count=%d, want %d", len(got.Params), len(mc.Params))
	}
}
