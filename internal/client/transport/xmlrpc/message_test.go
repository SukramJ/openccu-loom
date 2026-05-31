// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package xmlrpc

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmerr"
)

func TestEncodeCallRoundTrip(t *testing.T) {
	in := &MethodCall{
		Method: "setValue",
		Params: []Value{
			StringValue("ABC:1"),
			StringValue("LEVEL"),
			DoubleValue(0.25),
		},
	}
	var buf bytes.Buffer
	if err := EncodeCall(&buf, in); err != nil {
		t.Fatalf("EncodeCall: %v", err)
	}
	out, err := DecodeCall(&buf)
	if err != nil {
		t.Fatalf("DecodeCall: %v", err)
	}
	if out.Method != in.Method {
		t.Fatalf("method=%q, want %q", out.Method, in.Method)
	}
	if len(out.Params) != len(in.Params) {
		t.Fatalf("params=%d, want %d", len(out.Params), len(in.Params))
	}
	for i, p := range out.Params {
		if p.Kind() != in.Params[i].Kind() {
			t.Errorf("param %d kind=%s, want %s", i, p.Kind(), in.Params[i].Kind())
		}
	}
}

func TestEncodeCallEmitsISO8859Preamble(t *testing.T) {
	var buf bytes.Buffer
	if err := EncodeCall(&buf, &MethodCall{Method: "noop"}); err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(buf.Bytes(), []byte(`<?xml version="1.0" encoding="ISO-8859-1"?>`)) {
		t.Fatalf("preamble missing: %q", buf.String())
	}
}

func TestEncodeCallUmlautsSurviveISO8859(t *testing.T) {
	in := &MethodCall{Method: "echo", Params: []Value{StringValue("Türöffner")}}
	var buf bytes.Buffer
	if err := EncodeCall(&buf, in); err != nil {
		t.Fatal(err)
	}
	out, err := DecodeCall(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	s, err := AsString(out.Params[0])
	if err != nil {
		t.Fatal(err)
	}
	if s != "Türöffner" {
		t.Fatalf("umlaut round-trip failed: got %q", s)
	}
}

func TestEncodeCallRejectsEmptyMethod(t *testing.T) {
	var buf bytes.Buffer
	if err := EncodeCall(&buf, &MethodCall{}); err == nil {
		t.Fatal("expected error for empty Method")
	}
}

func TestDecodeResponseParams(t *testing.T) {
	resp := &MethodResponse{Params: []Value{IntValue(42)}}
	var buf bytes.Buffer
	if err := EncodeResponse(&buf, resp); err != nil {
		t.Fatal(err)
	}
	out, err := DecodeResponse(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if out.Fault != nil {
		t.Fatalf("unexpected fault: %v", out.Fault)
	}
	if len(out.Params) != 1 {
		t.Fatalf("params=%d", len(out.Params))
	}
	n, err := AsInt(out.Params[0])
	if err != nil {
		t.Fatal(err)
	}
	if n != 42 {
		t.Fatalf("n=%d", n)
	}
}

func TestDecodeResponseFault(t *testing.T) {
	resp := &MethodResponse{Fault: &hmerr.XMLRPCFault{Code: -3, Message: "not found"}}
	var buf bytes.Buffer
	if err := EncodeResponse(&buf, resp); err != nil {
		t.Fatal(err)
	}
	out, err := DecodeResponse(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if out.Fault == nil {
		t.Fatal("fault missing")
	}
	if out.Fault.Code != -3 || out.Fault.Message != "not found" {
		t.Fatalf("fault=%+v", out.Fault)
	}
	if !errors.Is(out.Fault, hmerr.ErrClientException) {
		t.Fatal("fault should classify as ErrClientException")
	}
}

func TestDecodeResponseEmptyParamsForVoid(t *testing.T) {
	raw := `<?xml version="1.0"?><methodResponse><params/></methodResponse>`
	out, err := DecodeResponse(strings.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	if out.Fault != nil {
		t.Fatalf("unexpected fault: %v", out.Fault)
	}
	if len(out.Params) != 0 {
		t.Fatalf("params=%d, want 0", len(out.Params))
	}
}

func TestMarshalBytes(t *testing.T) {
	b, err := MarshalBytes(&MethodCall{Method: "ping"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "<methodName>ping</methodName>") {
		t.Fatalf("method name missing: %s", b)
	}
}

// TestDecodeParamEmptyAsNil locks in the CCU void-reply tolerance: a
// <param/> with no <value> child must decode as NilValue rather than
// erroring out. Loss of this fallback would silently break setValue
// replies on certain interfaces.
func TestDecodeParamEmptyAsNil(t *testing.T) {
	raw := `<?xml version="1.0"?><methodCall><methodName>x</methodName><params><param/></params></methodCall>`
	call, err := DecodeCall(strings.NewReader(raw))
	if err != nil {
		t.Fatalf("DecodeCall: %v", err)
	}
	if len(call.Params) != 1 {
		t.Fatalf("params=%d, want 1", len(call.Params))
	}
	if _, ok := call.Params[0].(NilValue); !ok {
		t.Fatalf("want NilValue, got %T", call.Params[0])
	}
}

func TestEncodeCallNilRejected(t *testing.T) {
	var buf bytes.Buffer
	if err := EncodeCall(&buf, nil); err == nil {
		t.Fatal("expected error for nil MethodCall")
	}
}

func TestEncodeResponseNilRejected(t *testing.T) {
	var buf bytes.Buffer
	if err := EncodeResponse(&buf, nil); err == nil {
		t.Fatal("expected error for nil MethodResponse")
	}
}
