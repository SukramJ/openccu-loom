// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package xmlrpc

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmerr"
)

// TestEncodeCallRefusesNonLatin1WithTypedError pins the refusal a caller
// can act on: the CCU stores ISO-8859-1 only, so a rune outside it has no
// faithful encoding — but the refusal must be [hmerr.ErrUnencodableString],
// not an opaque charmap error.
func TestEncodeCallRefusesNonLatin1WithTypedError(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	mc := &MethodCall{Method: "setValue", Params: []Value{
		StringValue("VCU0001:1"),
		StringValue("STATE"),
		StringValue("Preis 5 €"),
	}}
	err := EncodeCall(&buf, mc)
	if !errors.Is(err, hmerr.ErrUnencodableString) {
		t.Fatalf("err = %v, want ErrUnencodableString", err)
	}
	if buf.Len() != 0 {
		t.Errorf("wrote %d bytes before refusing; the refusal must precede the wire", buf.Len())
	}
}

// TestEncodeCallRefusesNonLatin1InsideAStruct covers the nested carriers a
// paramset write uses.
func TestEncodeCallRefusesNonLatin1InsideAStruct(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	mc := &MethodCall{Method: "putParamset", Params: []Value{
		StringValue("VCU0001:1"),
		StringValue("VALUES"),
		StructValue{Members: []Member{{Name: "TEXT", Value: ArrayValue{StringValue("€")}}}},
	}}
	if err := EncodeCall(&buf, mc); !errors.Is(err, hmerr.ErrUnencodableString) {
		t.Fatalf("err = %v, want ErrUnencodableString", err)
	}
}

// TestEncodeCallAcceptsLatin1Runes keeps the umlauts the CCU does store.
func TestEncodeCallAcceptsLatin1Runes(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	mc := &MethodCall{Method: "setValue", Params: []Value{StringValue("Küche Vorräte")}}
	if err := EncodeCall(&buf, mc); err != nil {
		t.Fatalf("EncodeCall with Latin-1 runes: %v", err)
	}
	if !strings.Contains(buf.String(), "methodName") {
		t.Error("no method name on the wire")
	}
}

// TestClientCallSurfacesUnencodableStringSentinel proves the wrap survives
// the client's own error decoration, which is where callers match it.
func TestClientCallSurfacesUnencodableStringSentinel(t *testing.T) {
	t.Parallel()

	c, err := NewClient(Config{URL: "http://127.0.0.1:1/", Interface: "HmIP-RF"})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	_, err = c.Call(t.Context(), "setValue", []Value{StringValue("€")})
	if !errors.Is(err, hmerr.ErrUnencodableString) {
		t.Fatalf("err = %v, want ErrUnencodableString at the call boundary", err)
	}
}
