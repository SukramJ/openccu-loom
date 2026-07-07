// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// Regression tests for the struct/array/param member-count hardening in
// decode.go: a crafted huge element count paired with a truncated payload
// must fail cleanly instead of panicking make() (32-bit makeslice overflow)
// or pre-allocating a slice far larger than the payload could ever fill.

package binrpc

import (
	"bytes"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/client/transport/xmlrpc"
)

// runNoPanic invokes fn and fails the test if it panics, returning the error
// fn produced (if any) so the caller can additionally assert on it.
func runNoPanic(t *testing.T, fn func() error) error {
	t.Helper()
	var err error
	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("decode panicked: %v", r)
			}
		}()
		err = fn()
	}()
	return err
}

// buildFrame assembles a full BIN-RPC frame (8-byte header + payload) for
// the given message type.
func buildFrame(msgType uint8, payload []byte) []byte {
	frame := make([]byte, 0, 8+len(payload))
	size := uint32(len(payload)) //nolint:gosec // len() is always non-negative
	frame = append(frame, 'B', 'i', 'n', msgType, byte(size>>24), byte(size>>16), byte(size>>8), byte(size))
	frame = append(frame, payload...)
	return frame
}

// TestReadResponseHugeStructMemberCountDoesNotPanic feeds a struct value
// declaring 0x7FFFFFFF members against an otherwise-empty payload. The
// member-count guard in readValue (typeStruct branch) must reject it via
// remaining()/minMemberWireBytes before make() ever sees the raw count.
func TestReadResponseHugeStructMemberCountDoesNotPanic(t *testing.T) {
	payload := []byte{
		0x00, 0x00, 0x01, 0x01, // typeStruct
		0x7F, 0xFF, 0xFF, 0xFF, // member count = 0x7FFFFFFF, no member data follows
	}
	frame := buildFrame(msgTypeResponse, payload)
	err := runNoPanic(t, func() error {
		_, err := ReadResponse(bytes.NewReader(frame))
		return err
	})
	if err == nil {
		t.Fatal("expected error for huge struct member count against truncated payload")
	}
}

// TestReadResponseHugeArrayCountDoesNotPanic mirrors the struct case for
// typeArray: readNValues must reject a huge element count via
// remaining()/minValueWireBytes before allocating the values slice.
func TestReadResponseHugeArrayCountDoesNotPanic(t *testing.T) {
	payload := []byte{
		0x00, 0x00, 0x01, 0x00, // typeArray
		0x7F, 0xFF, 0xFF, 0xFF, // element count = 0x7FFFFFFF, no element data follows
	}
	frame := buildFrame(msgTypeResponse, payload)
	err := runNoPanic(t, func() error {
		_, err := ReadResponse(bytes.NewReader(frame))
		return err
	})
	if err == nil {
		t.Fatal("expected error for huge array element count against truncated payload")
	}
}

// TestReadRequestHugeParamCountDoesNotPanic exercises the same guard on the
// request param-count path in ReadRequest, which shares readNValues with the
// typeArray branch.
func TestReadRequestHugeParamCountDoesNotPanic(t *testing.T) {
	payload := []byte{
		0x00, 0x00, 0x00, 0x01, 'm', // method "m"
		0x7F, 0xFF, 0xFF, 0xFF, // param count = 0x7FFFFFFF, no param data follows
	}
	frame := buildFrame(msgTypeRequest, payload)
	err := runNoPanic(t, func() error {
		_, err := ReadRequest(bytes.NewReader(frame))
		return err
	})
	if err == nil {
		t.Fatal("expected error for huge request param count against truncated payload")
	}
}

// TestReadResponseValidSmallStructStillDecodes proves the tighter
// remaining()/minMemberWireBytes bound does not reject a legitimate small
// struct: two real members with real name + value bytes must still survive
// the round trip.
func TestReadResponseValidSmallStructStillDecodes(t *testing.T) {
	in := xmlrpc.StructValue{Members: []xmlrpc.Member{
		{Name: "ADDRESS", Value: xmlrpc.StringValue("ABC:1")},
		{Name: "VALUE", Value: xmlrpc.IntValue(42)},
	}}
	var buf bytes.Buffer
	if err := WriteResponse(&buf, in); err != nil {
		t.Fatalf("WriteResponse: %v", err)
	}
	resp, err := ReadResponse(&buf)
	if err != nil {
		t.Fatalf("ReadResponse: %v", err)
	}
	s, err := xmlrpc.AsStruct(resp.Value)
	if err != nil {
		t.Fatalf("AsStruct: %v", err)
	}
	if len(s.Members) != 2 {
		t.Fatalf("members=%d, want 2", len(s.Members))
	}
	addr, err := xmlrpc.StructField[xmlrpc.StringValue](s, "ADDRESS")
	if err != nil {
		t.Fatalf("ADDRESS field: %v", err)
	}
	if string(addr) != "ABC:1" {
		t.Fatalf("ADDRESS=%q, want %q", addr, "ABC:1")
	}
	val, err := xmlrpc.StructField[xmlrpc.IntValue](s, "VALUE")
	if err != nil {
		t.Fatalf("VALUE field: %v", err)
	}
	if int(val) != 42 {
		t.Fatalf("VALUE=%d, want 42", val)
	}
}

// TestReadResponseValidSmallArrayStillDecodes is the array counterpart:
// a small, fully-populated array must still decode after the hardening fix.
func TestReadResponseValidSmallArrayStillDecodes(t *testing.T) {
	in := xmlrpc.ArrayValue{
		xmlrpc.StringValue("first"),
		xmlrpc.StringValue("second"),
		xmlrpc.IntValue(7),
	}
	var buf bytes.Buffer
	if err := WriteResponse(&buf, in); err != nil {
		t.Fatalf("WriteResponse: %v", err)
	}
	resp, err := ReadResponse(&buf)
	if err != nil {
		t.Fatalf("ReadResponse: %v", err)
	}
	arr, ok := resp.Value.(xmlrpc.ArrayValue)
	if !ok {
		t.Fatalf("value type = %T, want xmlrpc.ArrayValue", resp.Value)
	}
	if len(arr) != 3 {
		t.Fatalf("len=%d, want 3", len(arr))
	}
	s0, err := xmlrpc.AsString(arr[0])
	if err != nil || s0 != "first" {
		t.Fatalf("arr[0]=%q, err=%v, want %q", s0, err, "first")
	}
	s1, err := xmlrpc.AsString(arr[1])
	if err != nil || s1 != "second" {
		t.Fatalf("arr[1]=%q, err=%v, want %q", s1, err, "second")
	}
	n2, err := xmlrpc.AsInt(arr[2])
	if err != nil || n2 != 7 {
		t.Fatalf("arr[2]=%d, err=%v, want 7", n2, err)
	}
}
