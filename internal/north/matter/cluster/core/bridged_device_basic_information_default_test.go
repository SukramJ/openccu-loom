// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package core_test

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/north/matter/cluster/core"
)

// TestBridgedBasicInfo_SerialEqualsUniqueIDIsValid verifies that a
// BridgedConfig where SerialNumber equals UniqueID still constructs
// successfully (the validation only warns, it never blocks). This also
// confirms that the backfill path (SerialNumber="") is silent.
func TestBridgedBasicInfo_SerialEqualsUniqueIDIsValid(t *testing.T) {
	t.Parallel()
	// Case 1: explicit SerialNumber == UniqueID — constructor must succeed.
	cfg := core.BridgedConfig{
		UniqueID:     "HM-01:1",
		NodeLabel:    "Test",
		SerialNumber: "HM-01:1",
	}
	b, err := core.NewBridgedDeviceBasicInformation(cfg)
	if err != nil {
		t.Fatalf("NewBridgedDeviceBasicInformation with Serial==UniqueID: %v", err)
	}
	_ = b

	// Case 2: backfill path (SerialNumber="") — no warning, serial is UniqueID.
	cfg2 := core.BridgedConfig{
		UniqueID:  "HM-02:1",
		NodeLabel: "Test2",
	}
	b2, err := core.NewBridgedDeviceBasicInformation(cfg2)
	if err != nil {
		t.Fatalf("NewBridgedDeviceBasicInformation with empty serial: %v", err)
	}
	v, ok := b2.MatterRead(0x000F) // SerialNumber attr
	if !ok {
		t.Fatal("MatterRead(SerialNumber): ok=false after backfill")
	}
	if v.(string) != "HM-02:1" {
		t.Errorf("SerialNumber = %q after backfill, want UniqueID %q", v.(string), "HM-02:1")
	}
}

// TestBridgedBasicInfo_HonorsConfigReachable verifies that the
// constructor now reflects cfg.Reachable verbatim in both directions.
// The bridge passes the underlying CCU device's live availability
// (dev.Available()) through BridgedConfig.Reachable, so a dead device
// must surface Reachable=false at construction time rather than relying
// on a deferred SetReachable(false) that — because cluster servers are
// reconstructed per dispatch — would never persist.
func TestBridgedBasicInfo_HonorsConfigReachable(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		want bool
	}{
		{"reachable", true},
		{"unreachable", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cfg := core.BridgedConfig{
				UniqueID:  "HM-00:1",
				NodeLabel: "Test",
				Reachable: tc.want,
			}
			b, err := core.NewBridgedDeviceBasicInformation(cfg)
			if err != nil {
				t.Fatalf("NewBridgedDeviceBasicInformation: %v", err)
			}
			v, ok := b.MatterRead(0x0011) // Reachable attr
			if !ok {
				t.Fatal("MatterRead(Reachable): ok=false")
			}
			reachable, isBool := v.(bool)
			if !isBool {
				t.Fatalf("Reachable = %T, want bool", v)
			}
			if reachable != tc.want {
				t.Errorf("Reachable = %v, want %v (cfg.Reachable honored)", reachable, tc.want)
			}
		})
	}
}
