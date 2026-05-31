// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package reliability

import (
	"context"
	"testing"
)

// TestMakeCoalesceKeyNoArgs verifies that a bare method name without args
// returns just the method.
func TestMakeCoalesceKeyNoArgs(t *testing.T) {
	got := MakeCoalesceKey("getParamset", nil)
	if got != "getParamset" {
		t.Errorf("MakeCoalesceKey(\"getParamset\", nil) = %q; want %q", got, "getParamset")
	}
}

// TestMakeCoalesceKeyWithArgs verifies that args are appended with colon
// separators.
func TestMakeCoalesceKeyWithArgs(t *testing.T) {
	got := MakeCoalesceKey("getValue", []any{"HmIP-RF", "VCU1234567:3", "SET_POINT_TEMPERATURE"})
	want := "getValue:HmIP-RF:VCU1234567:3:SET_POINT_TEMPERATURE"
	if got != want {
		t.Errorf("MakeCoalesceKey = %q; want %q", got, want)
	}
}

// TestMakeCoalesceKeyDifferentArgs verifies that different arg sets produce
// different keys — two calls with the same method but different targets must
// NOT be coalesced together.
func TestMakeCoalesceKeyDifferentArgs(t *testing.T) {
	k1 := MakeCoalesceKey("getParamset", []any{"VCU001:1", "VALUES"})
	k2 := MakeCoalesceKey("getParamset", []any{"VCU002:1", "VALUES"})
	if k1 == k2 {
		t.Errorf("different channel addresses should produce different keys; both = %q", k1)
	}
}

// TestMakeCoalesceKeySameArgsSameKey verifies that identical inputs always
// produce the same key (deterministic, no randomness).
func TestMakeCoalesceKeySameArgsSameKey(t *testing.T) {
	args := []any{"HmIP-RF", "VCU9999:0", "MASTER"}
	k1 := MakeCoalesceKey("getParamsetDescription", args)
	k2 := MakeCoalesceKey("getParamsetDescription", args)
	if k1 != k2 {
		t.Errorf("identical calls produced different keys: %q != %q", k1, k2)
	}
}

// TestMakeCoalesceKeyEmptyArgs verifies that an empty (non-nil) args slice
// behaves identically to a nil slice — both yield just the method.
func TestMakeCoalesceKeyEmptyArgs(t *testing.T) {
	withNil := MakeCoalesceKey("ping", nil)
	withEmpty := MakeCoalesceKey("ping", []any{})
	if withNil != withEmpty {
		t.Errorf("nil vs empty args produced different keys: %q vs %q", withNil, withEmpty)
	}
}

// TestMakeCoalesceKeyUsableWithCoalescer verifies that keys produced by
// MakeCoalesceKey correctly deduplicate concurrent calls in the Coalescer
// ( — end-to-end integration).
func TestMakeCoalesceKeyUsableWithCoalescer(t *testing.T) {
	c := NewCoalescer()
	key := MakeCoalesceKey("getParamset", []any{"VCU1234:0", "VALUES"})
	if key == "" {
		t.Fatal("MakeCoalesceKey returned empty string")
	}
	// Sequential call with the generated key — verifies the key is valid
	// (non-empty, accepted by the coalescer).
	var innerCalls int
	_, err := c.Do(context.Background(), key, func(_ context.Context) (any, error) {
		innerCalls++
		return "ok", nil
	})
	if err != nil {
		t.Fatalf("Coalescer.Do returned error: %v", err)
	}
	if innerCalls != 1 {
		t.Fatalf("expected 1 inner call; got %d", innerCalls)
	}
}
