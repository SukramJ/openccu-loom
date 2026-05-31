// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package xmlrpc

import (
	"bytes"
	"encoding/xml"
	"strings"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/pkg/hmerr"
)

// seedValueBytes returns the encoded <value>…</value> bytes for v.
// Failures are fatal — seeds must encode cleanly.
func seedValueBytes(f *testing.F, v Value) []byte {
	f.Helper()
	var buf bytes.Buffer
	enc := xml.NewEncoder(&buf)
	if err := v.MarshalXML(enc, valueEnvelope); err != nil {
		f.Fatalf("MarshalXML: %v", err)
	}
	if err := enc.Flush(); err != nil {
		f.Fatalf("Flush: %v", err)
	}
	return buf.Bytes()
}

// seedResponseBytes returns the encoded <methodResponse>…</methodResponse>
// bytes for mr. Failures are fatal.
func seedResponseBytes(f *testing.F, mr *MethodResponse) []byte {
	f.Helper()
	var buf bytes.Buffer
	if err := EncodeResponse(&buf, mr); err != nil {
		f.Fatalf("EncodeResponse: %v", err)
	}
	return buf.Bytes()
}

// FuzzDecodeValue fuzzes the XML-RPC value parser. Seeds cover every
// Kind (nil, int, bool, string, double, dateTime, base64, struct, array)
// plus several malformed inputs. The fuzz body asserts no panic occurs;
// on a successful decode it also calls Format() to confirm the
// stringifier doesn't panic.
func FuzzDecodeValue(f *testing.F) {
	// Positive corpus — one entry per Kind.
	f.Add(seedValueBytes(f, NilValue{}))
	f.Add(seedValueBytes(f, IntValue(0)))
	f.Add(seedValueBytes(f, IntValue(42)))
	f.Add(seedValueBytes(f, IntValue(-1_000_000)))
	f.Add(seedValueBytes(f, BoolValue(true)))
	f.Add(seedValueBytes(f, BoolValue(false)))
	f.Add(seedValueBytes(f, StringValue("")))
	f.Add(seedValueBytes(f, StringValue("hello")))
	f.Add(seedValueBytes(f, StringValue("Türöffner")))
	f.Add(seedValueBytes(f, DoubleValue(0.0)))
	f.Add(seedValueBytes(f, DoubleValue(-9999.0)))
	f.Add(seedValueBytes(f, DoubleValue(3.14159265358979)))
	f.Add(seedValueBytes(f, DateTimeValue(time.Date(2024, 1, 2, 15, 4, 5, 0, time.UTC))))
	f.Add(seedValueBytes(f, Base64Value([]byte("openccu-loom"))))
	f.Add(seedValueBytes(f, StructValue{Members: []Member{
		{Name: "ADDRESS", Value: StringValue("ABC:1")},
		{Name: "LEVEL", Value: DoubleValue(0.75)},
	}}))
	f.Add(seedValueBytes(f, ArrayValue{IntValue(1), IntValue(2), StringValue("x")}))

	// Negative corpus — malformed / edge-case inputs.
	f.Add([]byte(`<value></value>`))
	f.Add([]byte(`<value><unknown>x</unknown></value>`))
	f.Add([]byte(`<value><int>not-a-number</int></value>`))
	f.Add([]byte(`<value><boolean>2</boolean></value>`))
	f.Add([]byte(`<value><double>NaN</double></value>`))
	f.Add([]byte(`<value><base64>!!!not base64!!!</base64></value>`))
	f.Add([]byte(`<value><struct></struct></value>`))
	f.Add([]byte(`<value><array><data></data></array></value>`))
	f.Add([]byte(`<value>`))            // truncated
	f.Add([]byte(`<value><int>`))       // truncated mid-element
	f.Add([]byte(``))                   // empty
	f.Add([]byte(`garbage input here`)) // not XML

	f.Fuzz(func(t *testing.T, data []byte) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("panic in DecodeValue with input %q: %v", data, r)
			}
		}()

		dec := xml.NewDecoder(bytes.NewReader(data))
		tok, err := dec.Token()
		if err != nil {
			return // malformed XML before any start element — acceptable
		}
		start, ok := tok.(xml.StartElement)
		if !ok {
			return // first token is not a start element — acceptable
		}

		v, err := DecodeValue(dec, start)
		if err != nil {
			return // parse errors are acceptable; panics are not
		}

		// On success: confirm the stringifier doesn't panic.
		_ = Format(v)
	})
}

// FuzzDecodeMethodResponse fuzzes the <methodResponse> envelope parser.
// Seeds include a normal response, a fault, an empty params block, and
// several deliberately malformed envelopes. Errors are acceptable;
// panics are not.
func FuzzDecodeMethodResponse(f *testing.F) {
	// Positive corpus.
	f.Add(seedResponseBytes(f, &MethodResponse{Params: []Value{IntValue(42)}}))
	f.Add(seedResponseBytes(f, &MethodResponse{
		Params: []Value{StringValue("ok"), BoolValue(true), DoubleValue(1.5)},
	}))
	f.Add(seedResponseBytes(f, &MethodResponse{Params: nil})) // void response
	f.Add(seedResponseBytes(f, &MethodResponse{
		Fault: &hmerr.XMLRPCFault{Code: -3, Message: "not found"},
	}))
	f.Add(seedResponseBytes(f, &MethodResponse{
		Fault: &hmerr.XMLRPCFault{Code: -8, Message: "duty cycle limit"},
	}))
	f.Add([]byte(`<?xml version="1.0"?><methodResponse><params/></methodResponse>`))
	f.Add([]byte(`<?xml version="1.0" encoding="ISO-8859-1"?><methodResponse><params><param><value><string>hello</string></value></param></params></methodResponse>`))

	// Negative corpus.
	f.Add([]byte(`<methodResponse>`))                                                  // truncated
	f.Add([]byte(`<methodResponse><params><param></param></params></methodResponse>`)) // empty param
	f.Add([]byte(`<methodResponse><fault></fault></methodResponse>`))                  // fault without value
	f.Add([]byte(`<methodResponse><unknown/></methodResponse>`))                       // unknown child
	f.Add([]byte(`<methodResponse></methodResponse>`))                                 // no params or fault
	f.Add([]byte(`not xml at all`))
	f.Add([]byte(``))

	f.Fuzz(func(t *testing.T, data []byte) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("panic in DecodeResponse with input %q: %v", data, r)
			}
		}()

		_, _ = DecodeResponse(bytes.NewReader(data))
		// errors are acceptable; panics are not
	})
}

// FuzzDecodeStruct fuzzes the struct value decoder specifically, with
// deeply nested structs, empty structs, duplicate keys, and non-ASCII
// member names. Errors are acceptable; panics are not.
func FuzzDecodeStruct(f *testing.F) {
	// Deep nesting: {a: {b: {c: 1}}}.
	f.Add(seedValueBytes(f, StructValue{Members: []Member{
		{Name: "a", Value: StructValue{Members: []Member{
			{Name: "b", Value: StructValue{Members: []Member{
				{Name: "c", Value: IntValue(1)},
			}}},
		}}},
	}}))
	// Empty struct.
	f.Add(seedValueBytes(f, StructValue{}))
	// Struct with all primitive kinds as values.
	f.Add(seedValueBytes(f, StructValue{Members: []Member{
		{Name: "ADDRESS", Value: StringValue("ABC:1")},
		{Name: "LEVEL", Value: DoubleValue(0.75)},
		{Name: "STATE", Value: BoolValue(false)},
		{Name: "COUNT", Value: IntValue(99)},
	}}))
	// Non-ASCII member name (latin-1 extended).
	f.Add(seedValueBytes(f, StructValue{Members: []Member{
		{Name: "Schaltkanal", Value: StringValue("ein")},
	}}))
	// Struct whose value is an array.
	f.Add(seedValueBytes(f, StructValue{Members: []Member{
		{Name: "tags", Value: ArrayValue{StringValue("a"), StringValue("b")}},
	}}))

	// Negative corpus — raw XML quirks.
	f.Add([]byte(`<value><struct><member><name>x</name></member></struct></value>`))                                                                                          // member missing value
	f.Add([]byte(`<value><struct><member><value><int>1</int></value></member></struct></value>`))                                                                             // member missing name
	f.Add([]byte(`<value><struct><member><name>dup</name><value><int>1</int></value></member><member><name>dup</name><value><int>2</int></value></member></struct></value>`)) // duplicate key
	f.Add([]byte(`<value><struct>`))                                                                                                                                          // truncated
	f.Add([]byte(`<value><struct><member><name>` + strings.Repeat("x", 1000) + `</name><value><int>1</int></value></member></struct></value>`))                               // very long name

	f.Fuzz(func(t *testing.T, data []byte) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("panic in struct decode with input %q: %v", data, r)
			}
		}()

		dec := xml.NewDecoder(bytes.NewReader(data))
		tok, err := dec.Token()
		if err != nil {
			return
		}
		start, ok := tok.(xml.StartElement)
		if !ok {
			return
		}

		v, err := DecodeValue(dec, start)
		if err != nil {
			return
		}
		_ = Format(v)
	})
}
