// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package xmlrpc

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmerr"
)

// endToEndFixture pairs a handler-backed server with a client pointed at it.
type endToEndFixture struct {
	srv    *httptest.Server
	client *Client
	h      *Handler
}

func newEndToEnd(t *testing.T) *endToEndFixture {
	t.Helper()
	h := NewHandler()
	srv := httptest.NewServer(h)
	c, err := NewClient(Config{URL: srv.URL, Interface: "HmIP-RF"})
	if err != nil {
		srv.Close()
		t.Fatal(err)
	}
	t.Cleanup(srv.Close)
	return &endToEndFixture{srv: srv, client: c, h: h}
}

func TestHandlerRoundTripsMethodCall(t *testing.T) {
	f := newEndToEnd(t)
	var sawAddress atomic.Value
	f.h.Mux.Handle("getValue", func(_ context.Context, params []Value) (Value, error) {
		addr, _ := AsString(params[0])
		sawAddress.Store(addr)
		return IntValue(100), nil
	})

	v, err := f.client.Call(context.Background(), "getValue", []Value{StringValue("ABC:1"), StringValue("LEVEL")})
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	n, err := AsInt(v)
	if err != nil || n != 100 {
		t.Fatalf("result=%v err=%v", v, err)
	}
	if sawAddress.Load() != "ABC:1" {
		t.Fatalf("handler saw address=%v", sawAddress.Load())
	}
}

func TestHandlerFaultRoundTrip(t *testing.T) {
	f := newEndToEnd(t)
	f.h.Mux.Handle("bad", func(context.Context, []Value) (Value, error) {
		return nil, &hmerr.XMLRPCFault{Code: -5, Message: "bad param"}
	})
	_, err := f.client.Call(context.Background(), "bad", nil)
	if err == nil {
		t.Fatal("expected error")
	}
	var fault *hmerr.XMLRPCFault
	if !errors.As(err, &fault) {
		t.Fatalf("want *hmerr.XMLRPCFault, got %T", err)
	}
	if fault.Code != -5 || fault.Message != "bad param" {
		t.Fatalf("fault=%+v", fault)
	}
}

func TestHandlerGenericErrorBecomesFaultMinusOne(t *testing.T) {
	f := newEndToEnd(t)
	f.h.Mux.Handle("boom", func(context.Context, []Value) (Value, error) {
		return nil, errors.New("runtime oops")
	})
	_, err := f.client.Call(context.Background(), "boom", nil)
	var fault *hmerr.XMLRPCFault
	if !errors.As(err, &fault) {
		t.Fatalf("want fault, got %T", err)
	}
	if fault.Code != -1 {
		t.Fatalf("code=%d, want -1", fault.Code)
	}
	if !strings.Contains(fault.Message, "runtime oops") {
		t.Fatalf("message lost: %s", fault.Message)
	}
}

func TestHandlerUnknownMethodReturnsFault32601(t *testing.T) {
	f := newEndToEnd(t)
	_, err := f.client.Call(context.Background(), "what", nil)
	var fault *hmerr.XMLRPCFault
	if !errors.As(err, &fault) || fault.Code != -32601 {
		t.Fatalf("got %v, want fault -32601", err)
	}
}

func TestHandlerRejectsGET(t *testing.T) {
	h := NewHandler()
	srv := httptest.NewServer(h)
	defer srv.Close()

	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("status=%d, want 405", resp.StatusCode)
	}
}

func TestHandlerNilResultEncodedAsNilValue(t *testing.T) {
	f := newEndToEnd(t)
	f.h.Mux.Handle("void", func(context.Context, []Value) (Value, error) {
		return nil, nil
	})
	v, err := f.client.Call(context.Background(), "void", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := v.(NilValue); !ok {
		t.Fatalf("want NilValue, got %T", v)
	}
}

// TestHandlerBadRequest covers the decode-failure branch in
// Handler.ServeHTTP: a POST with an unparseable body must yield HTTP 400
// with a non-empty error string, not a 500 or a partial XML reply.
func TestHandlerBadRequest(t *testing.T) {
	srv := httptest.NewServer(NewHandler())
	defer srv.Close()

	resp, err := http.Post(srv.URL, "text/xml", strings.NewReader("not xml at all"))
	if err != nil {
		t.Fatalf("Post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status=%d, want 400", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "decode request failed") {
		t.Fatalf("body=%q", body)
	}
}

// TestHandlerMulticallOverHTTP exercises the full HTTP -> Decode ->
// Mux.system.multicall -> Encode -> HTTP path, including the per-sub-call
// fault accumulation XML-RPC requires: each sub-call returns either a
// value or a `<fault>` struct, and one failure must not abort the batch.
func TestHandlerMulticallOverHTTP(t *testing.T) {
	f := newEndToEnd(t)
	f.h.Mux.RegisterSystemMethods()
	f.h.Mux.Handle("ok", func(_ context.Context, params []Value) (Value, error) {
		n, _ := AsInt(params[0])
		return IntValue(int32(n * 2)), nil //nolint:gosec // G115: test helper returns small integer values that fit int32
	})
	f.h.Mux.Handle("bad", func(context.Context, []Value) (Value, error) {
		return nil, &hmerr.XMLRPCFault{Code: -7, Message: "boom"}
	})

	calls := ArrayValue{
		StructValue{Members: []Member{
			{Name: "methodName", Value: StringValue("ok")},
			{Name: "params", Value: ArrayValue{IntValue(21)}},
		}},
		StructValue{Members: []Member{
			{Name: "methodName", Value: StringValue("bad")},
			{Name: "params", Value: ArrayValue{}},
		}},
		StructValue{Members: []Member{
			{Name: "methodName", Value: StringValue("ok")},
			{Name: "params", Value: ArrayValue{IntValue(50)}},
		}},
	}
	got, err := f.client.Call(context.Background(), "system.multicall", []Value{calls})
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	results, err := AsArray(got)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 3 {
		t.Fatalf("results=%d, want 3", len(results))
	}

	// Successful sub-calls wrap the return in a single-element array.
	first, _ := AsArray(results[0])
	if n, _ := AsInt(first[0]); n != 42 {
		t.Fatalf("first sub-call=%v", results[0])
	}
	third, _ := AsArray(results[2])
	if n, _ := AsInt(third[0]); n != 100 {
		t.Fatalf("third sub-call=%v", results[2])
	}

	// The faulting sub-call appears as a {faultCode, faultString} struct
	// in line, not as an HTTP-level fault.
	mid, err := AsStruct(results[1])
	if err != nil {
		t.Fatalf("middle result not a struct: %T", results[1])
	}
	code, err := StructField[IntValue](mid, "faultCode")
	if err != nil {
		t.Fatal(err)
	}
	if code != -7 {
		t.Fatalf("faultCode=%d, want -7", code)
	}
	msg, _ := StructField[StringValue](mid, "faultString")
	if msg != "boom" {
		t.Fatalf("faultString=%q", msg)
	}
}
