// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// Additional codec tests targeting the encode/decode branches not
// reached by the existing codec_test.go suite.

package binrpc

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/client/transport/xmlrpc"
	"github.com/SukramJ/openccu-loom/pkg/hmerr"
)

// --- WriteFault ---

func TestWriteFaultNilReturnError(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteFault(&buf, nil); err == nil {
		t.Error("WriteFault(nil) should return error")
	}
}

func TestWriteFaultRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteFault(&buf, &hmerr.XMLRPCFault{Code: 42, Message: "boom"}); err != nil {
		t.Fatalf("WriteFault: %v", err)
	}
	resp, err := ReadResponse(&buf)
	if err != nil {
		t.Fatalf("ReadResponse: %v", err)
	}
	if resp.Fault == nil {
		t.Fatal("expected fault in response")
	}
	if resp.Fault.Code != 42 || resp.Fault.Message != "boom" {
		t.Errorf("fault = %+v, want Code=42 Message=boom", resp.Fault)
	}
}

// --- WriteResponse nil value ---

func TestWriteResponseNilEncodeAsEmptyString(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteResponse(&buf, nil); err != nil {
		t.Fatalf("WriteResponse(nil): %v", err)
	}
	resp, err := ReadResponse(&buf)
	if err != nil {
		t.Fatalf("ReadResponse: %v", err)
	}
	s, err := xmlrpc.AsString(resp.Value)
	if err != nil {
		t.Fatalf("AsString: %v", err)
	}
	if s != "" {
		t.Errorf("nil value round-tripped as %q, want empty string", s)
	}
}

// --- writeStruct: nil member value ---

func TestWriteRequestStructWithNilMemberFails(t *testing.T) {
	var buf bytes.Buffer
	err := WriteRequest(&buf, "method", []xmlrpc.Value{
		xmlrpc.StructValue{Members: []xmlrpc.Member{
			{Name: "field", Value: nil},
		}},
	})
	if err == nil {
		t.Error("nil struct member value should return error")
	}
}

// --- writeArray: nil element ---

func TestWriteRequestArrayWithNilElementFails(t *testing.T) {
	var buf bytes.Buffer
	err := WriteRequest(&buf, "method", []xmlrpc.Value{
		xmlrpc.ArrayValue{nil},
	})
	if err == nil {
		t.Error("nil array element should return error")
	}
}

// --- encodeDouble: NaN and Inf rejection ---

func TestEncodedoubleRejectsNaN(t *testing.T) {
	// NaN is produced by 0/0.
	nan := func() float64 { var x float64; return x / x }()
	_, _, err := encodeDouble(nan)
	if err == nil {
		t.Error("expected error for NaN")
	}
}

func TestEncodedoubleRejectsInf(t *testing.T) {
	// math.Inf(1) = positive infinity.
	var inf float64
	inf = 1.7976931348623157e+308
	inf *= 2 // overflow to +Inf at runtime
	_, _, err := encodeDouble(inf)
	if err == nil {
		t.Error("expected error for +Inf")
	}
}

func TestEncodedoubleAcceptsZero(t *testing.T) {
	m, e, err := encodeDouble(0)
	if err != nil {
		t.Fatalf("encodeDouble(0): %v", err)
	}
	if m != 0 || e != 0 {
		t.Errorf("encodeDouble(0): got (%d,%d), want (0,0)", m, e)
	}
}

// --- ReadRequest: non-request message type ---

func TestReadRequestRejectsResponseMessage(t *testing.T) {
	// Build a valid response frame (type 0x01), then try to ReadRequest it.
	var buf bytes.Buffer
	if err := WriteResponse(&buf, xmlrpc.IntValue(1)); err != nil {
		t.Fatalf("WriteResponse: %v", err)
	}
	_, err := ReadRequest(&buf)
	if err == nil {
		t.Error("ReadRequest should fail on a response-type message")
	}
}

// --- ReadRequest: trailing bytes after params ---

func TestReadRequestTrailingBytesAfterParams(t *testing.T) {
	// Build a request, then inject an extra byte in the payload.
	var buf bytes.Buffer
	if err := WriteRequest(&buf, "m", []xmlrpc.Value{xmlrpc.IntValue(1)}); err != nil {
		t.Fatalf("WriteRequest: %v", err)
	}
	raw := buf.Bytes()
	// Increase payload size by 1 and append a garbage byte.
	raw[7]++
	raw = append(raw, 0xFF)
	_, err := ReadRequest(bytes.NewReader(raw))
	if err == nil {
		t.Error("expected error due to trailing bytes in request payload")
	}
}

// --- readValue: unknown type tag ---

func TestReadResponseUnknownValueType(t *testing.T) {
	// Construct a response frame whose single-value payload has type tag 0xDEAD.
	//
	// Frame: marker(3) + msgType(1) + size(4) + typeTag(4).
	// type tag 0x0000DEAD → 4 bytes.
	payload := []byte{0x00, 0x00, 0xDE, 0xAD}
	frame := make([]byte, 0, 8+len(payload))
	frame = append(frame, 'B', 'i', 'n', msgTypeResponse, 0x00, 0x00, 0x00, byte(len(payload)))
	frame = append(frame, payload...)
	_, err := ReadResponse(bytes.NewReader(frame))
	if err == nil {
		t.Error("expected error for unknown value type tag in response")
	}
}

// --- readValue: double with truncated mantissa ---

func TestReadResponseDoubleTruncatedMantissa(t *testing.T) {
	// type_tag=typeDouble(4) + only 2 bytes instead of 4 for mantissa.
	payload := []byte{
		0x00, 0x00, 0x00, 0x04, // typeDouble
		0x00, 0x01, // only 2 bytes of mantissa (truncated)
	}
	frame := make([]byte, 0, 8+len(payload))
	frame = append(frame, 'B', 'i', 'n', msgTypeResponse, 0x00, 0x00, 0x00, byte(len(payload)))
	frame = append(frame, payload...)
	_, err := ReadResponse(bytes.NewReader(frame))
	if err == nil {
		t.Error("expected error for truncated double mantissa")
	}
}

// --- readValue: array with truncated count field ---

func TestReadResponseArrayTruncatedCount(t *testing.T) {
	payload := []byte{
		0x00, 0x00, 0x01, 0x00, // typeArray
		0x00, 0x01, // only 2 bytes of count (truncated)
	}
	frame := make([]byte, 0, 8+len(payload))
	frame = append(frame, 'B', 'i', 'n', msgTypeResponse, 0x00, 0x00, 0x00, byte(len(payload)))
	frame = append(frame, payload...)
	_, err := ReadResponse(bytes.NewReader(frame))
	if err == nil {
		t.Error("expected error for truncated array count")
	}
}

// --- readValue: struct member value error ---

func TestReadResponseStructMemberValueError(t *testing.T) {
	// typeStruct + count=1 + member name "X" + member value with bad type tag.
	//
	// typeStruct (4) + count (4) + name_len (4) + 'X' (1) + bad_type_tag (4)
	payload := []byte{
		0x00, 0x00, 0x01, 0x01, // typeStruct
		0x00, 0x00, 0x00, 0x01, // count=1
		0x00, 0x00, 0x00, 0x01, // name len=1
		'X',                    // name
		0x00, 0x00, 0xFF, 0xFF, // bad type tag for member value
	}
	frame := make([]byte, 0, 8+len(payload))
	frame = append(frame, 'B', 'i', 'n', msgTypeResponse, 0x00, 0x00, 0x00, byte(len(payload)))
	frame = append(frame, payload...)
	_, err := ReadResponse(bytes.NewReader(frame))
	if err == nil {
		t.Error("expected error for bad struct member value type tag")
	}
}

// --- readValue: bool truncated ---

func TestReadResponseBoolTruncated(t *testing.T) {
	payload := []byte{
		0x00, 0x00, 0x00, 0x02, // typeBool — no value byte follows
	}
	frame := make([]byte, 0, 8+len(payload))
	frame = append(frame, 'B', 'i', 'n', msgTypeResponse, 0x00, 0x00, 0x00, byte(len(payload)))
	frame = append(frame, payload...)
	_, err := ReadResponse(bytes.NewReader(frame))
	if err == nil {
		t.Error("expected error for truncated bool value")
	}
}

// --- readValue: int truncated ---

func TestReadResponseIntTruncated(t *testing.T) {
	payload := []byte{
		0x00, 0x00, 0x00, 0x01, // typeInt — no int32 value follows
	}
	frame := make([]byte, 0, 8+len(payload))
	frame = append(frame, 'B', 'i', 'n', msgTypeResponse, 0x00, 0x00, 0x00, byte(len(payload)))
	frame = append(frame, payload...)
	_, err := ReadResponse(bytes.NewReader(frame))
	if err == nil {
		t.Error("expected error for truncated int value")
	}
}

// --- readNValues: count exceeds remaining (negative count path) ---

func TestReadResponseArrayNegativeCount(t *testing.T) {
	// readNValues checks n < 0, but since count is uint32 it can't be negative
	// from the wire directly. We test the "count > remaining" path instead.
	// With count=0x10000000 and payload of only a few bytes, remaining is tiny.
	payload := []byte{
		0x00, 0x00, 0x01, 0x00, // typeArray
		0x10, 0x00, 0x00, 0x00, // count = 0x10000000 (too large for remaining)
	}
	frame := make([]byte, 0, 8+len(payload))
	frame = append(frame, 'B', 'i', 'n', msgTypeResponse, 0x00, 0x00, 0x00, byte(len(payload)))
	frame = append(frame, payload...)
	_, err := ReadResponse(bytes.NewReader(frame))
	if err == nil {
		t.Error("expected error: array count exceeds remaining bytes")
	}
}

// --- readN: negative length ---

func TestReadNNegativeLengthFails(t *testing.T) {
	// readRawString reads a u32 length then calls readN. If the length
	// field is 0xFFFFFFFF the cast to int may be negative on 32-bit, but on
	// 64-bit it's a huge positive. We test the "truncated" path instead.
	payload := []byte{
		0x00, 0x00, 0x00, 0x03, // typeString
		0xFF, 0xFF, 0xFF, 0xFF, // length = 4294967295 (huge)
	}
	// Payload size is only 8 bytes but the string claims 4 GB.
	// readN will fail with truncated error.
	frame := make([]byte, 0, 8+len(payload))
	frame = append(frame, 'B', 'i', 'n', msgTypeResponse, 0x00, 0x00, 0x00, byte(len(payload)))
	frame = append(frame, payload...)
	_, err := ReadResponse(bytes.NewReader(frame))
	if err == nil {
		t.Error("expected error for oversize string length")
	}
}

// --- ReadResponse: fault with bad faultCode member ---

func TestReadResponseFaultMissingFaultCode(t *testing.T) {
	// Build a fault frame whose struct payload is missing the faultCode member.
	// We encode a fault-typed frame (0xFF) that contains a struct with only
	// faultString — no faultCode — so StructField fails.
	var buf bytes.Buffer
	// Hand-craft a fault frame with struct that only has faultString.
	badFault := xmlrpc.StructValue{Members: []xmlrpc.Member{
		{Name: "faultString", Value: xmlrpc.StringValue("oops")},
		// faultCode is missing
	}}
	var payload bytes.Buffer
	if err := writeValue(&payload, badFault); err != nil {
		t.Fatalf("writeValue: %v", err)
	}
	frame := make([]byte, 0, 8+payload.Len())
	frame = append(frame, 'B', 'i', 'n', msgTypeFault)
	pSize := uint32(payload.Len()) //nolint:gosec // payload.Len() is always non-negative so the conversion is safe
	frame = append(frame, byte(pSize>>24), byte(pSize>>16), byte(pSize>>8), byte(pSize))
	frame = append(frame, payload.Bytes()...)
	_, err := ReadResponse(bytes.NewReader(frame))
	// StructField should fail because faultCode is absent.
	if err == nil {
		buf.Reset()
		t.Error("expected error for fault missing faultCode member")
	}
}

// --- readNValues: count exceeds remaining ---

func TestReadRequestNegativeParamCount(t *testing.T) {
	// Frame: marker + request type + payload that encodes
	// method "m" + param-count wrapping to a huge positive number.
	// We craft a payload where after the method, the count field encodes
	// a value that exceeds remaining bytes.
	//
	// Method "m" = length(1=00000001) + 'm' = 5 bytes.
	// Then param count = 0x7FFFFFFF (huge positive) = 4 bytes.
	// Total payload = 9 bytes.
	payload := []byte{
		0x00, 0x00, 0x00, 0x01, 'm', // method "m"
		0x7F, 0xFF, 0xFF, 0xFF, // param count way too large
	}
	frame := make([]byte, 0, 8+len(payload))
	frame = append(frame, 'B', 'i', 'n', msgTypeRequest, 0x00, 0x00, 0x00, byte(len(payload)))
	frame = append(frame, payload...)
	_, err := ReadRequest(bytes.NewReader(frame))
	if err == nil {
		t.Error("expected error: param count exceeds remaining bytes")
	}
}

// --- readRawString: truncated ---

func TestReadResponseTruncatedString(t *testing.T) {
	// A response that claims string length 100 but only has 4 bytes.
	// typeTag(string=3) + length(100) + 4 bytes of actual data.
	payload := []byte{
		0x00, 0x00, 0x00, 0x03, // typeString
		0x00, 0x00, 0x00, 0x64, // length=100
		'h', 'e', 'l', 'l', // only 4 bytes
	}
	frame := make([]byte, 0, 8+len(payload))
	frame = append(frame, 'B', 'i', 'n', msgTypeResponse, 0x00, 0x00, 0x00, byte(len(payload)))
	frame = append(frame, payload...)
	_, err := ReadResponse(bytes.NewReader(frame))
	if err == nil {
		t.Error("expected error: string payload truncated")
	}
}

// --- readFrame: payload size exceeds MaxMessageSize ---

func TestReadResponseOversizedPayload(t *testing.T) {
	// Declare a payload of MaxMessageSize+1 bytes.
	bigSize := uint32(MaxMessageSize + 1)
	frame := []byte{
		'B', 'i', 'n', msgTypeResponse,
		byte(bigSize >> 24), byte(bigSize >> 16), byte(bigSize >> 8), byte(bigSize),
	}
	_, err := ReadResponse(bytes.NewReader(frame))
	if err == nil {
		t.Error("expected error for oversized payload")
	}
}

// --- writeFrame: payload too large ---

func TestWriteFrameOversizedPayloadReturnsError(t *testing.T) {
	// writeFrame is unexported, but we can trigger it via WriteRequest with
	// a big enough param count to exceed MaxMessageSize — actually simpler is
	// to construct a huge string.
	//
	// 10 MiB + 1 byte string → exceeds MaxMessageSize (10 MiB).
	bigStr := make([]byte, int(MaxMessageSize)+1)
	var buf bytes.Buffer
	if err := WriteResponse(&buf, xmlrpc.StringValue(string(bigStr))); err == nil {
		t.Error("expected error: payload exceeds MaxMessageSize")
	}
}

// --- asFault: wraps unknown error ---

func TestAsFaultConvertsGenericError(t *testing.T) {
	err := errors.New("some error")
	f := asFault(err)
	if f.Code != -1 {
		t.Errorf("asFault(generic): code=%d, want -1", f.Code)
	}
	if f.Message != "some error" {
		t.Errorf("asFault(generic): message=%q, want \"some error\"", f.Message)
	}
}

func TestAsFaultPassesThroughXMLRPCFault(t *testing.T) {
	expected := &hmerr.XMLRPCFault{Code: -7, Message: "already a fault"}
	f := asFault(expected)
	if f != expected {
		t.Errorf("asFault(XMLRPCFault): returned different pointer")
	}
}

// --- Server handleConn: handler returns nil result ---

func TestServerHandlerReturnsNilResultUseNilValue(t *testing.T) {
	// When a handler returns (nil, nil), the server must encode NilValue as
	// an empty string. This path exercises the nil-result branch in handleConn.
	s := testServer(t)
	s.Mux().Handle("nilResult", func(_ context.Context, _ []xmlrpc.Value) (xmlrpc.Value, error) {
		return nil, nil
	})
	c := testClient(t, s)
	v, err := c.Call(context.Background(), "nilResult", nil)
	if err != nil {
		t.Fatalf("nilResult call: %v", err)
	}
	str, err := xmlrpc.AsString(v)
	if err != nil {
		t.Fatalf("AsString: %v", err)
	}
	if str != "" {
		t.Errorf("nil result should be empty string, got %q", str)
	}
}
