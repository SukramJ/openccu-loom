// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package binrpc

import (
	"bytes"
	"encoding/binary"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/client/transport/xmlrpc"
	"github.com/SukramJ/openccu-loom/pkg/hmerr"
)

// seedRequest encodes a BIN-RPC request frame and returns the raw bytes.
// Fatal on encode error — seeds must be well-formed.
func seedRequest(f *testing.F, method string, params []xmlrpc.Value) []byte {
	f.Helper()
	var buf bytes.Buffer
	if err := WriteRequest(&buf, method, params); err != nil {
		f.Fatalf("WriteRequest(%q): %v", method, err)
	}
	return buf.Bytes()
}

// seedResponse encodes a BIN-RPC response frame and returns the raw bytes.
func seedResponse(f *testing.F, v xmlrpc.Value) []byte {
	f.Helper()
	var buf bytes.Buffer
	if err := WriteResponse(&buf, v); err != nil {
		f.Fatalf("WriteResponse: %v", err)
	}
	return buf.Bytes()
}

// seedFault encodes a BIN-RPC fault frame and returns the raw bytes.
func seedFault(f *testing.F, code int, msg string) []byte {
	f.Helper()
	var buf bytes.Buffer
	if err := WriteFault(&buf, &hmerr.XMLRPCFault{Code: code, Message: msg}); err != nil {
		f.Fatalf("WriteFault: %v", err)
	}
	return buf.Bytes()
}

// FuzzReadRequest fuzzes the BIN-RPC request parser. Seeds cover all
// supported Value kinds, a multi-param request, and a no-param request.
// Truncated frames are included so the fuzzer learns to mutate length
// prefixes. Errors are acceptable; panics are not.
func FuzzReadRequest(f *testing.F) {
	// Positive corpus — one entry per Value kind sent as a single param.
	f.Add(seedRequest(f, "system.listMethods", nil))
	f.Add(seedRequest(f, "echo", []xmlrpc.Value{xmlrpc.IntValue(0)}))
	f.Add(seedRequest(f, "echo", []xmlrpc.Value{xmlrpc.IntValue(42)}))
	f.Add(seedRequest(f, "echo", []xmlrpc.Value{xmlrpc.IntValue(-1_000_000)}))
	f.Add(seedRequest(f, "echo", []xmlrpc.Value{xmlrpc.BoolValue(true)}))
	f.Add(seedRequest(f, "echo", []xmlrpc.Value{xmlrpc.BoolValue(false)}))
	f.Add(seedRequest(f, "echo", []xmlrpc.Value{xmlrpc.StringValue("")}))
	f.Add(seedRequest(f, "echo", []xmlrpc.Value{xmlrpc.StringValue("hello")}))
	f.Add(seedRequest(f, "echo", []xmlrpc.Value{xmlrpc.DoubleValue(0.0)}))
	f.Add(seedRequest(f, "echo", []xmlrpc.Value{xmlrpc.DoubleValue(3.14)}))
	f.Add(seedRequest(f, "echo", []xmlrpc.Value{xmlrpc.DoubleValue(-9999.9999)}))
	f.Add(seedRequest(f, "setValue", []xmlrpc.Value{
		xmlrpc.StringValue("ABC:1"),
		xmlrpc.StringValue("LEVEL"),
		xmlrpc.DoubleValue(0.5),
	}))
	f.Add(seedRequest(f, "echo", []xmlrpc.Value{
		xmlrpc.StructValue{Members: []xmlrpc.Member{
			{Name: "ADDRESS", Value: xmlrpc.StringValue("ABC:1")},
			{Name: "LEVEL", Value: xmlrpc.DoubleValue(0.75)},
		}},
	}))
	f.Add(seedRequest(f, "echo", []xmlrpc.Value{
		xmlrpc.ArrayValue{xmlrpc.IntValue(1), xmlrpc.StringValue("two"), xmlrpc.BoolValue(false)},
	}))
	// CUxD-style init frame (TEST-NET-1, RFC 5737).
	f.Add(seedRequest(f, "init", []xmlrpc.Value{
		xmlrpc.StringValue("xmlrpc_bin://192.0.2.1:8129"),
		xmlrpc.StringValue("openccu-loom-test"),
	}))

	// Negative corpus — truncated / malformed frames so the fuzzer
	// learns about length-prefix mutations.
	f.Add([]byte{})                                                        // empty
	f.Add([]byte{'B', 'i', 'n'})                                           // header only, no type byte
	f.Add([]byte{'B', 'i', 'n', 0x00, 0x00, 0x00, 0x00, 0x00})             // zero-length payload request
	f.Add([]byte{'X', 'i', 'n', 0x00, 0x00, 0x00, 0x00, 0x00})             // bad marker
	f.Add([]byte{'B', 'i', 'n', 0x01, 0x00, 0x00, 0x00, 0x00})             // response type in request parser
	f.Add([]byte{'B', 'i', 'n', 0x00, 0x00, 0x00, 0x00, 0x08, 0x01, 0x02}) // declared size > actual data

	f.Fuzz(func(t *testing.T, data []byte) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("panic in ReadRequest with input %x: %v", data, r)
			}
		}()

		_, _ = ReadRequest(bytes.NewReader(data))
		// errors are acceptable; panics are not
	})
}

// FuzzReadResponse fuzzes the BIN-RPC response/fault parser. Seeds cover
// all supported Value kinds in a response frame plus fault frames.
// Truncated and type-mutated frames are included as negative seeds.
// Errors are acceptable; panics are not.
func FuzzReadResponse(f *testing.F) {
	// Positive corpus — one entry per Value kind in a response frame.
	f.Add(seedResponse(f, xmlrpc.IntValue(0)))
	f.Add(seedResponse(f, xmlrpc.IntValue(42)))
	f.Add(seedResponse(f, xmlrpc.IntValue(-1_000_000)))
	f.Add(seedResponse(f, xmlrpc.BoolValue(true)))
	f.Add(seedResponse(f, xmlrpc.BoolValue(false)))
	f.Add(seedResponse(f, xmlrpc.StringValue("")))
	f.Add(seedResponse(f, xmlrpc.StringValue("hello")))
	f.Add(seedResponse(f, xmlrpc.StringValue("Türöffner")))
	f.Add(seedResponse(f, xmlrpc.DoubleValue(0.0)))
	f.Add(seedResponse(f, xmlrpc.DoubleValue(1234567.89)))
	f.Add(seedResponse(f, xmlrpc.DoubleValue(-9999.9999)))
	f.Add(seedResponse(f, xmlrpc.StructValue{Members: []xmlrpc.Member{
		{Name: "ADDRESS", Value: xmlrpc.StringValue("ABC:1")},
		{Name: "LEVEL", Value: xmlrpc.DoubleValue(0.75)},
		{Name: "STATE", Value: xmlrpc.BoolValue(false)},
	}}))
	f.Add(seedResponse(f, xmlrpc.ArrayValue{
		xmlrpc.StringValue("a"), xmlrpc.StringValue("b"), xmlrpc.IntValue(99),
	}))
	// NilValue encodes as empty string in BIN-RPC.
	f.Add(seedResponse(f, xmlrpc.NilValue{}))

	// Fault corpus.
	f.Add(seedFault(f, -3, "not found"))
	f.Add(seedFault(f, -8, "duty cycle limit"))
	f.Add(seedFault(f, 0, ""))

	// Negative corpus — truncated and malformed frames.
	f.Add([]byte{})                                                  // empty
	f.Add([]byte{'B', 'i', 'n'})                                     // header only
	f.Add([]byte{'B', 'i', 'n', 0x01, 0x00, 0x00, 0x00, 0x00})       // zero-payload response
	f.Add([]byte{'B', 'i', 'n', 0xFF, 0x00, 0x00, 0x00, 0x00})       // zero-payload fault
	f.Add([]byte{'B', 'i', 'n', 0x77, 0x00, 0x00, 0x00, 0x00})       // unknown message type
	f.Add([]byte{'B', 'i', 'n', 0x01, 0xFF, 0xFF, 0xFF, 0xFF})       // declared size over limit
	f.Add([]byte{'B', 'i', 'n', 0x01, 0x00, 0x00, 0x00, 0x04, 0xDE}) // declared 4 bytes, only 1 present

	// Deeply-nested arrays. Past maxDecodeDepth this must error, not
	// recurse into a stack-overflow crash that kills the worker.
	f.Add(nestedArrayResponse(maxDecodeDepth + 50))

	f.Fuzz(func(t *testing.T, data []byte) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("panic in ReadResponse with input %x: %v", data, r)
			}
		}()

		_, _ = ReadResponse(bytes.NewReader(data))
		// errors are acceptable; panics are not
	})
}

// nestedArrayResponse builds a response frame whose payload is `levels`
// nested single-element arrays wrapping one int. Used to exercise the
// recursion-depth guard in readValue.
func nestedArrayResponse(levels int) []byte {
	var payload bytes.Buffer
	for range levels {
		_ = binary.Write(&payload, binary.BigEndian, typeArray)
		_ = binary.Write(&payload, binary.BigEndian, uint32(1)) // count = 1
	}
	_ = binary.Write(&payload, binary.BigEndian, typeInt)
	_ = binary.Write(&payload, binary.BigEndian, int32(42))

	var frame bytes.Buffer
	frame.Write([]byte{'B', 'i', 'n', msgTypeResponse})
	_ = binary.Write(&frame, binary.BigEndian, uint32(payload.Len()))
	frame.Write(payload.Bytes())
	return frame.Bytes()
}

// TestReadResponseRejectsDeepNesting locks the fix for the stack-overflow
// crash: a deeply-nested array message must return an error rather than
// recurse until the goroutine stack is exhausted (a non-recoverable fatal
// error that takes the whole process down).
func TestReadResponseRejectsDeepNesting(t *testing.T) {
	frame := nestedArrayResponse(maxDecodeDepth + 50)
	_, err := ReadResponse(bytes.NewReader(frame))
	if err == nil {
		t.Fatal("expected error for over-deep nesting, got nil")
	}
	if !strings.Contains(err.Error(), "max depth") {
		t.Fatalf("expected max-depth error, got: %v", err)
	}
}

// TestReadResponseAcceptsLegitimateNesting guards against the depth limit
// being set so low it rejects real CCU paramsets, which nest only a few
// levels (here: array → struct → value).
func TestReadResponseAcceptsLegitimateNesting(t *testing.T) {
	frame := nestedArrayResponse(8)
	if _, err := ReadResponse(bytes.NewReader(frame)); err != nil {
		t.Fatalf("legitimate 8-level nesting rejected: %v", err)
	}
}
