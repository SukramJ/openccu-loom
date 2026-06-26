// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package backends

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// rssiToInt
// ---------------------------------------------------------------------------

func TestRssiToInt_Int(t *testing.T) {
	t.Parallel()
	if got := rssiToInt(int(-82)); got != -82 {
		t.Fatalf("want -82, got %d", got)
	}
}

func TestRssiToInt_Int64(t *testing.T) {
	t.Parallel()
	if got := rssiToInt(int64(-90)); got != -90 {
		t.Fatalf("want -90, got %d", got)
	}
}

func TestRssiToInt_Int32(t *testing.T) {
	t.Parallel()
	if got := rssiToInt(int32(-75)); got != -75 {
		t.Fatalf("want -75, got %d", got)
	}
}

func TestRssiToInt_Float64(t *testing.T) {
	t.Parallel()
	if got := rssiToInt(float64(-88)); got != -88 {
		t.Fatalf("want -88, got %d", got)
	}
}

func TestRssiToInt_UnknownTypeReturnsNoInfo(t *testing.T) {
	t.Parallel()
	if got := rssiToInt("bad"); got != rssiNoInfo {
		t.Fatalf("want rssiNoInfo (%d), got %d", rssiNoInfo, got)
	}
}

// ---------------------------------------------------------------------------
// RSSIInfo — nil xml caller
// ---------------------------------------------------------------------------

func TestCcuRSSIInfo_NoXMLCallerReturnsErrNotWired(t *testing.T) {
	t.Parallel()
	b := NewCcuBackend(nil, &fakeCaller{}, nil)
	_, err := b.RSSIInfo(context.Background())
	if !errors.Is(err, ErrNotWired) {
		t.Fatalf("want ErrNotWired, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// RSSIInfo — reply is not a map
// ---------------------------------------------------------------------------

func TestCcuRSSIInfo_NonMapReplyReturnsError(t *testing.T) {
	t.Parallel()
	x := &fakeCaller{reply: []any{"not", "a", "map"}}
	b := NewCcuBackend(x, nil, nil)
	_, err := b.RSSIInfo(context.Background())
	if err == nil {
		t.Fatal("want error for non-map reply, got nil")
	}
	if !strings.Contains(err.Error(), "unexpected type") {
		t.Fatalf("want 'unexpected type' in error, got %q", err.Error())
	}
}

// ---------------------------------------------------------------------------
// RSSIInfo — valid nested matrix
// ---------------------------------------------------------------------------

func TestCcuRSSIInfo_ValidMatrixParsed(t *testing.T) {
	t.Parallel()
	x := &fakeCaller{
		reply: map[string]any{
			"ABC0001": map[string]any{
				"ABC0002": []any{int(-82), int(-90)},
				"ABC0003": []any{int64(-75), float64(-88)},
			},
			"DEF0001": map[string]any{
				"DEF0002": []any{int(rssiNoInfo), int(-65)},
			},
		},
	}
	b := NewCcuBackend(x, nil, nil)
	out, err := b.RSSIInfo(context.Background())
	if err != nil {
		t.Fatalf("RSSIInfo: %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("want 2 device entries, got %d", len(out))
	}
	abc := out["ABC0001"]
	if abc == nil {
		t.Fatal("want ABC0001 in result")
	}
	if abc["ABC0002"] != [2]int{-82, -90} {
		t.Errorf("ABC0001→ABC0002: got %v", abc["ABC0002"])
	}
	if abc["ABC0003"] != [2]int{-75, -88} {
		t.Errorf("ABC0001→ABC0003: got %v", abc["ABC0003"])
	}
	def := out["DEF0001"]
	if def == nil {
		t.Fatal("want DEF0001 in result")
	}
	// 65536 sentinel preserved verbatim.
	if def["DEF0002"] != [2]int{rssiNoInfo, -65} {
		t.Errorf("DEF0001→DEF0002: got %v", def["DEF0002"])
	}

	// Verify the XML-RPC method name dispatched.
	method, _, ok := loadArgs(x)
	if !ok || method != "rssiInfo" {
		t.Fatalf("expected XML-RPC method 'rssiInfo', got %q", method)
	}
}

// ---------------------------------------------------------------------------
// RSSIInfo — malformed partner entries are skipped
// ---------------------------------------------------------------------------

func TestCcuRSSIInfo_MalformedPartnerEntriesSkipped(t *testing.T) {
	t.Parallel()
	x := &fakeCaller{
		reply: map[string]any{
			"ABC0001": map[string]any{
				"GOOD":          []any{int(-80), int(-70)}, // valid
				"NOT_ARRAY":     "bad_value",               // not []any — skipped
				"TOO_SHORT":     []any{int(-80)},           // len < 2 — skipped
				"PARTNER_NOMAP": "partner-not-map",         // skipped
			},
			"BAD_DEVICE": "not-a-map", // device entry not map[string]any — skipped
		},
	}
	b := NewCcuBackend(x, nil, nil)
	out, err := b.RSSIInfo(context.Background())
	if err != nil {
		t.Fatalf("RSSIInfo: %v", err)
	}
	// BAD_DEVICE is skipped; ABC0001 is present.
	if _, hasBad := out["BAD_DEVICE"]; hasBad {
		t.Error("BAD_DEVICE must be skipped (not a map)")
	}
	abc := out["ABC0001"]
	if abc == nil {
		t.Fatal("ABC0001 must be present")
	}
	// Only the GOOD partner survives.
	if len(abc) != 1 {
		t.Fatalf("want 1 partner for ABC0001, got %d: %v", len(abc), abc)
	}
	if abc["GOOD"] != [2]int{-80, -70} {
		t.Errorf("GOOD partner: got %v", abc["GOOD"])
	}
}

// ---------------------------------------------------------------------------
// RSSIInfo — caller error propagates
// ---------------------------------------------------------------------------

func TestCcuRSSIInfo_CallerErrorPropagates(t *testing.T) {
	t.Parallel()
	sentinel := errors.New("xml-rpc: connection refused")
	x := &fakeCaller{err: sentinel}
	b := NewCcuBackend(x, nil, nil)
	_, err := b.RSSIInfo(context.Background())
	if !errors.Is(err, sentinel) {
		t.Fatalf("want caller error, got %v", err)
	}
}
