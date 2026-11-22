// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package xmlrpc

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

// TestMulticallAnswersAVoidSubCallWithTrue pins the wire shape of a
// system.multicall response whose sub-calls return nothing — the CCU's
// batched event delivery. The HmIP server's XML-RPC parser
// (HMIPServer de.eq3.cbcs.legacy.communication.rpc.internal.format.xml.XmlRpcParser)
// advances one tag too far after a <nil> value, so a <nil> inside the
// result array makes it read the next element's <data> as a type tag and
// fail with "Unknown type: data". Since HMIPServer 1.5.2 three such
// failures in a row evict the callback client, which silences every HmIP
// event. A void sub-call therefore answers true, never nil.
func TestMulticallAnswersAVoidSubCallWithTrue(t *testing.T) {
	m := NewMux()
	m.Handle("event", func(context.Context, []Value) (Value, error) {
		return NilValue{}, nil
	})
	m.Handle("untyped", func(context.Context, []Value) (Value, error) {
		return nil, nil
	})
	m.Handle("answer", func(context.Context, []Value) (Value, error) {
		return IntValue(42), nil
	})
	m.RegisterSystemMethods()

	call := func(name string) Value {
		return StructValue{Members: []Member{
			{Name: "methodName", Value: StringValue(name)},
			{Name: "params", Value: ArrayValue{}},
		}}
	}
	v, err := m.Dispatch(context.Background(), "system.multicall",
		[]Value{ArrayValue{call("event"), call("event"), call("untyped"), call("answer")}})
	if err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	if err := EncodeResponse(&buf, &MethodResponse{Params: []Value{v}}); err != nil {
		t.Fatal(err)
	}
	wire := buf.String()
	if strings.Contains(wire, "<nil") {
		t.Fatalf("multicall response carries <nil>, which the HmIP server cannot parse inside an array:\n%s", wire)
	}

	results, err := AsArray(v)
	if err != nil {
		t.Fatal(err)
	}
	want := []Value{BoolValue(true), BoolValue(true), BoolValue(true), IntValue(42)}
	if len(results) != len(want) {
		t.Fatalf("results=%d, want %d", len(results), len(want))
	}
	for i, r := range results {
		wrapped, err := AsArray(r)
		if err != nil || len(wrapped) != 1 {
			t.Fatalf("result %d: want a single-element array, got %#v", i, r)
		}
		if wrapped[0] != want[i] {
			t.Errorf("result %d: got %#v, want %#v", i, wrapped[0], want[i])
		}
	}
}
