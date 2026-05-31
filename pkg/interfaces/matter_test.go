// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package interfaces

import "testing"

func TestMatterDeviceTypeName_KnownIDs(t *testing.T) {
	t.Parallel()
	cases := map[uint16]string{
		0x000A: "Door Lock",
		0x000F: "Generic Switch",
		0x0015: "Contact Sensor",
		0x002C: "Air Quality Sensor",
		0x0043: "Water Leak Detector",
		0x0076: "Smoke / CO Alarm",
		0x0100: "On/Off Light",
		0x0101: "Dimmable Light",
		0x0106: "Light Sensor",
		0x0107: "Occupancy Sensor",
		0x010A: "On/Off Plug-in Unit",
		0x010C: "Color Temperature Light",
		0x010D: "Extended Color Light",
		0x0202: "Window Covering",
		0x0301: "Thermostat",
		0x0302: "Temperature Sensor",
		0x0305: "Pressure Sensor",
		0x0307: "Humidity Sensor",
	}
	for id, want := range cases {
		if got := MatterDeviceTypeName(id); got != want {
			t.Errorf("MatterDeviceTypeName(0x%04X): got %q, want %q", id, got, want)
		}
	}
}

func TestMatterDeviceTypeName_ZeroIsEmpty(t *testing.T) {
	t.Parallel()
	if got := MatterDeviceTypeName(0); got != "" {
		t.Errorf("MatterDeviceTypeName(0): got %q, want \"\"", got)
	}
}

func TestMatterDeviceTypeName_UnknownReturnsHexFallback(t *testing.T) {
	t.Parallel()
	if got := MatterDeviceTypeName(0xABCD); got != "0xABCD" {
		t.Errorf("MatterDeviceTypeName(0xABCD): got %q, want %q", got, "0xABCD")
	}
}

// TestMatterChangeNotifier_InterfaceShape verifies the
// [MatterChangeNotifier] contract surface: the method must accept a
// nullable callback and return a non-nil unsubscribe closure. The
// concrete implementations (Sensor[float64], BinarySensor) are tested
// via compile-time assertions in the generic package; this test
// validates the interface contract against an in-package stub so the
// interfaces package has at least one exercise of its own definition.
func TestMatterChangeNotifier_InterfaceShape(t *testing.T) {
	t.Parallel()
	// stubNotifier is a minimal MatterChangeNotifier that records
	// whether OnMatterValueChanged was called and stores the callback.
	var called int
	var stored func()
	stub := stubChangeNotifier{
		onSubscribe: func(cb func()) func() {
			called++
			stored = cb
			return func() { stored = nil }
		},
	}
	var n MatterChangeNotifier = &stub
	unsub := n.OnMatterValueChanged(func() {})
	if unsub == nil {
		t.Fatal("OnMatterValueChanged must return a non-nil unsubscribe closure")
	}
	if called != 1 {
		t.Fatalf("expected 1 call to onSubscribe, got %d", called)
	}
	// Unsubscribe must detach the callback.
	unsub()
	if stored != nil {
		t.Fatal("unsubscribe must clear the stored callback")
	}
}

type stubChangeNotifier struct {
	onSubscribe func(func()) func()
}

func (s *stubChangeNotifier) OnMatterValueChanged(cb func()) func() {
	return s.onSubscribe(cb)
}
