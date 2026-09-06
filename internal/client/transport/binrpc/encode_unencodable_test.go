// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package binrpc

import (
	"bytes"
	"errors"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/client/transport/xmlrpc"
	"github.com/SukramJ/openccu-loom/pkg/hmerr"
)

// TestWriteRequestRefusesNonLatin1WithTypedError pins the same typed
// refusal on BIN-RPC as on XML-RPC: CUxD stores ISO-8859-1 only, and a
// caller must be able to tell "the CCU cannot store this text" from a
// transport failure.
func TestWriteRequestRefusesNonLatin1WithTypedError(t *testing.T) {
	t.Parallel()

	var frame bytes.Buffer
	err := WriteRequest(&frame, "setValue", []xmlrpc.Value{
		xmlrpc.StringValue("CUX0001:1"),
		xmlrpc.StringValue("STATE"),
		xmlrpc.StringValue("Preis 5 €"),
	})
	if !errors.Is(err, hmerr.ErrUnencodableString) {
		t.Fatalf("err = %v, want ErrUnencodableString", err)
	}
}

// TestWriteRequestAcceptsLatin1Runes keeps the umlauts CUxD does store.
func TestWriteRequestAcceptsLatin1Runes(t *testing.T) {
	t.Parallel()

	var frame bytes.Buffer
	if err := WriteRequest(&frame, "setValue", []xmlrpc.Value{xmlrpc.StringValue("Küche Vorräte")}); err != nil {
		t.Fatalf("WriteRequest with Latin-1 runes: %v", err)
	}
	if frame.Len() == 0 {
		t.Error("nothing encoded")
	}
}

// TestClientCallSurfacesUnencodableStringSentinel proves the wrap survives
// the client's error decoration, where callers match it.
func TestClientCallSurfacesUnencodableStringSentinel(t *testing.T) {
	t.Parallel()

	c, err := NewClient(Config{Addr: "127.0.0.1:1", Interface: "CUxD"})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	_, err = c.Call(t.Context(), "setValue", []xmlrpc.Value{xmlrpc.StringValue("€")})
	if !errors.Is(err, hmerr.ErrUnencodableString) {
		t.Fatalf("err = %v, want ErrUnencodableString at the call boundary", err)
	}
}
