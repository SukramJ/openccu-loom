// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package main

import (
	"testing"
)

// ---------------------------------------------------------------------------
// rssiOrNull
// ---------------------------------------------------------------------------

func TestRSSIOrNull_SentinelBecomesNil(t *testing.T) {
	t.Parallel()
	if got := rssiOrNull(rssiNoInfo); got != nil {
		t.Fatalf("want nil for sentinel %d, got %v", rssiNoInfo, got)
	}
}

func TestRSSIOrNull_RealReadingPassedThrough(t *testing.T) {
	t.Parallel()
	cases := []int{-82, -90, 0, -1, rssiNoInfo - 1}
	for _, v := range cases {
		got := rssiOrNull(v)
		if got != v {
			t.Errorf("rssiOrNull(%d): want %d, got %v", v, v, got)
		}
	}
}

// ---------------------------------------------------------------------------
// wsRSSIInfo.RSSIInfo — nil registry / nil writer guard
// ---------------------------------------------------------------------------

func TestWSRSSIInfo_NilRegistryReturnsEmptyDevices(t *testing.T) {
	t.Parallel()
	w := &wsRSSIInfo{registry: nil, writer: nil}
	out, err := w.RSSIInfo(t.Context())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	devs, ok := out["devices"].([]map[string]any)
	if !ok {
		t.Fatalf("expected []map[string]any devices, got %T", out["devices"])
	}
	if len(devs) != 0 {
		t.Fatalf("expected empty devices slice, got %d entries", len(devs))
	}
}

func TestWSRSSIInfo_EmptyRegistryReturnsEmptyDevices(t *testing.T) {
	t.Parallel()
	reg := buildTestRegistry(t) // no centrals
	w := &wsRSSIInfo{registry: reg, writer: nil}
	out, err := w.RSSIInfo(t.Context())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	devs := out["devices"].([]map[string]any)
	if len(devs) != 0 {
		t.Fatalf("expected 0 devices from empty registry, got %d", len(devs))
	}
}
