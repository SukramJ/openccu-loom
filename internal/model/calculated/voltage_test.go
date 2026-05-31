// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// Package calculated tests battery voltage configuration and
// OperatingVoltageLevelSensor behaviour.
package calculated

import "testing"

// ---------------------------------------------------------------------------
// CellVoltage / BatteryConfig.VoltageMax
// ---------------------------------------------------------------------------

func TestCellVoltageKnownTypes(t *testing.T) {
	cases := []struct {
		bt   BatteryType
		want float64
	}{
		{BatteryTypeCR2032, 3.0},
		{BatteryTypeLR44, 1.5},
		{BatteryTypeAAA, 1.5},
		{BatteryTypeBaby, 1.5},
		{BatteryTypeAA, 1.5},
		{BatteryTypeUnknown, 0},
	}
	for _, c := range cases {
		got := CellVoltage(c.bt)
		if got != c.want {
			t.Errorf("CellVoltage(%v): got %v want %v", c.bt, got, c.want)
		}
	}
}

func TestBatteryConfigVoltageMax(t *testing.T) {
	bc := BatteryConfig{Battery: BatteryTypeAA, Quantity: 2}
	if bc.VoltageMax() != 3.0 {
		t.Fatalf("expected 3.0, got %v", bc.VoltageMax())
	}
	bc2 := BatteryConfig{Battery: BatteryTypeUnknown, Quantity: 2}
	if bc2.VoltageMax() != 0 {
		t.Fatalf("expected 0 for unknown battery, got %v", bc2.VoltageMax())
	}
	bc3 := BatteryConfig{Battery: BatteryTypeAA, Quantity: 0}
	if bc3.VoltageMax() != 1.5 {
		t.Fatalf("zero quantity: expected 1.5, got %v", bc3.VoltageMax())
	}
}

func TestBatteryConfigVoltageMaxNegativeQuantity(t *testing.T) {
	bc := BatteryConfig{Battery: BatteryTypeAA, Quantity: -3}
	if bc.VoltageMax() != 1.5 {
		t.Fatalf("negative quantity: expected 1.5, got %v", bc.VoltageMax())
	}
}

// ---------------------------------------------------------------------------
// LookupBatteryConfig
// ---------------------------------------------------------------------------

func TestLookupBatteryConfigKnownModel(t *testing.T) {
	cfg, ok := LookupBatteryConfig("HM-CC-RT-DN")
	if !ok {
		t.Fatal("expected ok for HM-CC-RT-DN")
	}
	if cfg.Battery != BatteryTypeAA {
		t.Fatalf("expected AA, got %v", cfg.Battery)
	}
	if cfg.Quantity != 2 {
		t.Fatalf("expected 2, got %d", cfg.Quantity)
	}
}

func TestLookupBatteryConfigPrefixMatch(t *testing.T) {
	cfg, ok := LookupBatteryConfig("HM-CC-RT-DN-NG")
	if !ok {
		t.Fatal("expected ok for HM-CC-RT-DN-NG (prefix match)")
	}
	if cfg.Battery != BatteryTypeAA {
		t.Fatalf("expected AA, got %v", cfg.Battery)
	}

	_, ok = LookupBatteryConfig("HM-Sec-SD-2")
	if ok {
		t.Fatal("HM-Sec-SD-2 has unknown battery type: expected ok=false")
	}
}

func TestLookupBatteryConfigUnknownModel(t *testing.T) {
	_, ok := LookupBatteryConfig("TOTALLY-UNKNOWN")
	if ok {
		t.Fatal("expected not ok for unknown model")
	}
}

func TestLookupBatteryConfigEmptyModel(t *testing.T) {
	_, ok := LookupBatteryConfig("")
	if ok {
		t.Fatal("expected not ok for empty model")
	}
}

func TestLookupBatteryConfigUnknownBatteryType(t *testing.T) {
	_, ok := LookupBatteryConfig("HmIP-SWSD")
	if ok {
		t.Fatal("expected ok=false for unknown battery type model")
	}
}

// ---------------------------------------------------------------------------
// OperatingVoltageLevelSensor — reference cycle and dedup
// ---------------------------------------------------------------------------

func TestOperatingVoltageLevelSensorRefsCycle(t *testing.T) {
	s := NewOperatingVoltageLevelSensor()
	s.SetReferences(3.0, 2.0)
	s.OnOperatingVoltage(2.5)
	if _, ok := s.Value(); ok {
		t.Fatal("should not compute with invalid refs")
	}
	s.SetReferences(2.0, 3.0)
	v, ok := s.Value()
	if !ok || v != 50 {
		t.Fatalf("expected 50%%, got %v ok=%v", v, ok)
	}
}

func TestOperatingVoltageLevelSensorDedup(t *testing.T) {
	s := NewOperatingVoltageLevelSensor()
	var fired int
	s.OnUpdate(func(_, _ float64) { fired++ })
	s.SetReferences(2.0, 3.0)
	s.OnOperatingVoltage(2.5)
	if fired != 1 {
		t.Fatalf("expected 1 fire, got %d", fired)
	}
	s.OnOperatingVoltage(2.5)
	if fired != 1 {
		t.Fatalf("dedup: expected 1, got %d", fired)
	}
	s.OnOperatingVoltage(2.8)
	if fired != 2 {
		t.Fatalf("expected 2, got %d", fired)
	}
}
