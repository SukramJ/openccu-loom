// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package xmlrpc

import (
	"math"
	"strings"
	"testing"
)

// TestHmCliMarshalDoubleRejectsNonFinite pins the write-side finiteness guard
// on the XML-RPC encoder. Without it, FormatFloat renders NaN / ±Inf as the
// bare words "NaN", "+Inf", "-Inf" — none of which contains a '.', so the
// "force one fractional digit" branch appends one and the daemon puts
// <double>NaN.0</double> on the wire, which is not a valid XML-RPC double.
//
// The three sibling sites already reject the same value (binrpc encodeDouble,
// binrpc decodeDouble, xmlrpc DecodeValue), so an identical
// [DoubleValue] is refused locally over BIN-RPC and transmitted over XML-RPC
// unless this guard holds.
func TestHmCliMarshalDoubleRejectsNonFinite(t *testing.T) {
	cases := []struct {
		name string
		val  float64
	}{
		{"NaN", math.NaN()},
		{"+Inf", math.Inf(1)},
		{"-Inf", math.Inf(-1)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mc := &MethodCall{
				Method: "setValue",
				Params: []Value{
					StringValue("VCU0000123:1"),
					StringValue("LEVEL"),
					DoubleValue(tc.val),
				},
			}
			var sb strings.Builder
			err := EncodeCall(&sb, mc)
			if err == nil {
				t.Fatalf("EncodeCall(%v) = nil error, body %q; want a non-finite rejection", tc.val, sb.String())
			}
			if !strings.Contains(err.Error(), "non-finite") {
				t.Fatalf("EncodeCall(%v) error = %q, want it to name the non-finite double", tc.val, err)
			}
		})
	}
}

// TestHmCliMarshalDoubleKeepsFiniteValues is the negative control for the
// guard above: the same encoder path must still serialise ordinary doubles,
// including the forced fractional digit on a whole number.
func TestHmCliMarshalDoubleKeepsFiniteValues(t *testing.T) {
	cases := []struct {
		val  float64
		want string
	}{
		{1, "<double>1.0</double>"},
		{20.5, "<double>20.5</double>"},
		{-0.115, "<double>-0.115</double>"},
	}
	for _, tc := range cases {
		var sb strings.Builder
		mc := &MethodCall{Method: "setValue", Params: []Value{DoubleValue(tc.val)}}
		if err := EncodeCall(&sb, mc); err != nil {
			t.Fatalf("EncodeCall(%v) = %v, want success", tc.val, err)
		}
		if !strings.Contains(sb.String(), tc.want) {
			t.Fatalf("EncodeCall(%v) body = %q, want it to contain %q", tc.val, sb.String(), tc.want)
		}
	}
}
