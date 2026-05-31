// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package jsonrpc

import (
	"context"
	"testing"
)

// TestSuppressServiceMessage_WireName verifies that the correct CCU method
// name (Interface.suppressServiceMessages) is sent on the wire.
func TestSuppressServiceMessage_WireName(t *testing.T) {
	t.Parallel()
	var capturedMethod string
	srv := newTestServer(t, map[string]func(envelope) any{
		"Interface.suppressServiceMessages": func(env envelope) any {
			capturedMethod = env.Method
			return okResult(true)
		},
	})
	defer srv.Close()

	c, _ := New(Config{Endpoint: srv.URL})
	if err := c.SuppressServiceMessage(context.Background(), "HmIP-RF", "AABBCC001122:1", "ERROR", true); err != nil {
		t.Fatalf("SuppressServiceMessage: %v", err)
	}
	if capturedMethod != "Interface.suppressServiceMessages" {
		t.Errorf("wire method = %q, want %q", capturedMethod, "Interface.suppressServiceMessages")
	}
}

// TestHasProgramIDs_WireName verifies that the correct CCU method name
// (Channel.hasProgramIds) is sent on the wire.
func TestHasProgramIDs_WireName(t *testing.T) {
	t.Parallel()
	var capturedMethod string
	srv := newTestServer(t, map[string]func(envelope) any{
		"Channel.hasProgramIds": func(env envelope) any {
			capturedMethod = env.Method
			return okResult(true)
		},
	})
	defer srv.Close()

	c, _ := New(Config{Endpoint: srv.URL})
	ok, err := c.HasProgramIDs(context.Background(), "42")
	if err != nil {
		t.Fatalf("HasProgramIDs: %v", err)
	}
	if !ok {
		t.Error("HasProgramIDs: want true, got false")
	}
	if capturedMethod != "Channel.hasProgramIds" {
		t.Errorf("wire method = %q, want %q", capturedMethod, "Channel.hasProgramIds")
	}
}
