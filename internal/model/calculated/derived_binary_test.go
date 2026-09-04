// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// Package calculated tests DerivedBinarySensor: lookup and relevance functions
// (LookupDerivedBinaryMappingByParam, IsRelevantForMapping, IsRelevantForModel),
// LookupDerivedBinaryMappings, MakeDerivedBinarySensor, and sensor behaviour.
package calculated

import (
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// ─── LookupDerivedBinaryMappingByParam ───────────────────────────────────────

// TestLookupDerivedBinaryMappingByParamFound verifies that looking up
// a registered CalculatedParameter returns the correct mapping.
func TestLookupDerivedBinaryMappingByParamFound(t *testing.T) {
	m, ok := LookupDerivedBinaryMappingByParam(hmenum.CalculatedParameterWindowOpen)
	if !ok {
		t.Fatal("LookupDerivedBinaryMappingByParam(WINDOW_OPEN) must find a mapping")
	}
	if m.CalculatedParameter != hmenum.CalculatedParameterWindowOpen {
		t.Errorf("mapping.CalculatedParameter=%v, want WINDOW_OPEN", m.CalculatedParameter)
	}
}

// TestLookupDerivedBinaryMappingByParamSmokeAlarm verifies lookup for
// SMOKE_ALARM.
func TestLookupDerivedBinaryMappingByParamSmokeAlarm(t *testing.T) {
	m, ok := LookupDerivedBinaryMappingByParam(hmenum.CalculatedParameterSmokeAlarm)
	if !ok {
		t.Fatal("LookupDerivedBinaryMappingByParam(SMOKE_ALARM) must find a mapping")
	}
	if m.CalculatedParameter != hmenum.CalculatedParameterSmokeAlarm {
		t.Errorf("mapping.CalculatedParameter=%v, want SMOKE_ALARM", m.CalculatedParameter)
	}
}

// TestLookupDerivedBinaryMappingByParamNotFound verifies that an
// unregistered CalculatedParameter returns (zero, false).
func TestLookupDerivedBinaryMappingByParamNotFound(t *testing.T) {
	_, ok := LookupDerivedBinaryMappingByParam(hmenum.CalculatedParameter("NO_SUCH_PARAM"))
	if ok {
		t.Fatal("LookupDerivedBinaryMappingByParam(unknown) must return ok=false")
	}
}

// ─── IsRelevantForMapping ────────────────────────────────────────────────────

// TestIsRelevantForMappingMatchesModelAndChannel verifies that
// IsRelevantForMapping returns true when both the model string and
// channel number satisfy the mapping.
func TestIsRelevantForMappingMatchesModelAndChannel(t *testing.T) {
	m, ok := LookupDerivedBinaryMappingByParam(hmenum.CalculatedParameterWindowOpen)
	if !ok {
		t.Skip("no WINDOW_OPEN mapping registered")
	}
	model := m.Models[0]
	chNo := m.SourceChannelNo
	if !IsRelevantForMapping(m, model, chNo) {
		t.Errorf("IsRelevantForMapping must return true for model=%q chNo=%d", model, chNo)
	}
}

// TestIsRelevantForMappingWrongModel verifies that IsRelevantForMapping
// returns false when the model does not match.
func TestIsRelevantForMappingWrongModel(t *testing.T) {
	m, ok := LookupDerivedBinaryMappingByParam(hmenum.CalculatedParameterWindowOpen)
	if !ok {
		t.Skip("no WINDOW_OPEN mapping registered")
	}
	if IsRelevantForMapping(m, "HmIP-SomeOtherDevice", m.SourceChannelNo) {
		t.Error("IsRelevantForMapping must return false for non-matching model")
	}
}

// TestIsRelevantForMappingWrongChannel verifies that IsRelevantForMapping
// returns false when the channel number does not match.
func TestIsRelevantForMappingWrongChannel(t *testing.T) {
	m, ok := LookupDerivedBinaryMappingByParam(hmenum.CalculatedParameterWindowOpen)
	if !ok {
		t.Skip("no WINDOW_OPEN mapping registered")
	}
	model := m.Models[0]
	// Use a channel number that is definitely not the one in the mapping
	wrongCh := m.SourceChannelNo + 99
	if IsRelevantForMapping(m, model, wrongCh) {
		t.Errorf("IsRelevantForMapping must return false for channel %d (mapping expects %d)", wrongCh, m.SourceChannelNo)
	}
}

// TestIsRelevantForMappingOpenChannel verifies that a mapping with
// SourceChannelNoOpen (-1) matches any channel number.
func TestIsRelevantForMappingOpenChannel(t *testing.T) {
	m := DerivedBinaryMapping{
		Models:              []string{"HmIP-TEST"},
		SourceChannelNo:     SourceChannelNoOpen,
		CalculatedParameter: hmenum.CalculatedParameterWindowOpen,
	}
	for _, chNo := range []int{0, 1, 2, 10, 99} {
		if !IsRelevantForMapping(m, "HmIP-TEST", chNo) {
			t.Errorf("IsRelevantForMapping with SourceChannelNoOpen must accept channel %d", chNo)
		}
	}
}

// ─── IsRelevantForModel ──────────────────────────────────────────────────────

// TestIsRelevantForModelKnownModel verifies that IsRelevantForModel
// returns true for a model + channel combination that appears in the
// registry.
func TestIsRelevantForModelKnownModel(t *testing.T) {
	// HmIP-SRH is registered for WINDOW_OPEN on channel 1
	if !IsRelevantForModel("HmIP-SRH", 1) {
		t.Error("IsRelevantForModel(HmIP-SRH, ch1) must be true")
	}
}

// TestIsRelevantForModelUnknownModel verifies that IsRelevantForModel
// returns false for a model that is not in the registry at all.
func TestIsRelevantForModelUnknownModel(t *testing.T) {
	if IsRelevantForModel("HmIP-NONEXISTENT", 1) {
		t.Error("IsRelevantForModel for unknown model must be false")
	}
}

// TestIsRelevantForModelWrongChannel verifies that IsRelevantForModel
// returns false when the model is registered but not for the given
// channel number.
func TestIsRelevantForModelWrongChannel(t *testing.T) {
	// HmIP-SRH registers on channel 1, not channel 42
	if IsRelevantForModel("HmIP-SRH", 42) {
		t.Error("IsRelevantForModel(HmIP-SRH, ch42) must be false")
	}
}

// TestIsRelevantForModelSmokeDetector verifies that HmIP-SWSD channel 1
// is relevant (both SMOKE_ALARM and INTRUSION_ALARM are registered there).
func TestIsRelevantForModelSmokeDetector(t *testing.T) {
	if !IsRelevantForModel("HmIP-SWSD", 1) {
		t.Error("IsRelevantForModel(HmIP-SWSD, ch1) must be true")
	}
}

// ─── LookupDerivedBinaryMappings / MakeDerivedBinarySensor ───────────────────

func TestLookupDerivedBinaryMappingsKnownModel(t *testing.T) {
	mappings := LookupDerivedBinaryMappings("HmIP-SRH")
	if len(mappings) == 0 {
		t.Fatal("expected at least one mapping for HmIP-SRH")
	}
	for _, m := range mappings {
		if m.CalculatedParameter == hmenum.CalculatedParameterWindowOpen {
			return
		}
	}
	t.Fatal("expected WINDOW_OPEN mapping for HmIP-SRH")
}

func TestLookupDerivedBinaryMappingsUnknownModel(t *testing.T) {
	mappings := LookupDerivedBinaryMappings("UNKNOWN-DEVICE")
	if len(mappings) != 0 {
		t.Fatalf("expected no mappings for unknown device, got %d", len(mappings))
	}
}

func TestLookupDerivedBinaryMappingsSmokeDevice(t *testing.T) {
	mappings := LookupDerivedBinaryMappings("HmIP-SWSD")
	if len(mappings) < 2 {
		t.Fatalf("expected 2 mappings for HmIP-SWSD, got %d", len(mappings))
	}
}

func TestMakeDerivedBinarySensor(t *testing.T) {
	mappings := LookupDerivedBinaryMappings("HmIP-SWSD")
	if len(mappings) == 0 {
		t.Fatal("no mappings found")
	}
	s := MakeDerivedBinarySensor(mappings[0])
	if s == nil {
		t.Fatal("MakeDerivedBinarySensor returned nil")
	}
	s.OnLabel("PRIMARY_ALARM")
	if v, ok := s.Value(); !ok || !v {
		t.Fatalf("MakeDerivedBinarySensor: PRIMARY_ALARM should yield true, got v=%v ok=%v", v, ok)
	}
}

// ─── DerivedBinarySensor behaviour ───────────────────────────────────────────

func TestDerivedBinarySensorNilOffValues(t *testing.T) {
	s := NewDerivedBinarySensor(hmenum.CalculatedParameterWindowOpen, []string{"OPEN"}, nil)
	s.OnLabel("UNKNOWN")
	v, ok := s.Value()
	if !ok || v {
		t.Fatalf("nil OffValues: expected (false, ok=true) for unknown label, got v=%v ok=%v", v, ok)
	}
}

func TestDerivedBinarySensorUnknownWithExplicitOffValues(t *testing.T) {
	s := newWindowOpenSensorForTest(t)
	s.OnLabel("WEIRD_LABEL")
	_, ok := s.Value()
	if ok {
		t.Fatal("before any known label, Value() should not be ok")
	}
}

func TestDerivedBinarySensorNoRepeatFire(t *testing.T) {
	s := newWindowOpenSensorForTest(t)
	var fired int
	s.OnUpdate(func(_, _ bool) { fired++ })
	s.OnLabel("OPEN")
	if fired != 1 {
		t.Fatalf("first fire: expected 1, got %d", fired)
	}
	s.OnLabel("OPEN")
	if fired != 1 {
		t.Fatalf("dedup: expected 1, got %d", fired)
	}
	s.OnLabel("TILTED")
	if fired != 1 {
		t.Fatalf("same bool value: expected 1, got %d", fired)
	}
	s.OnLabel("CLOSED")
	if fired != 2 {
		t.Fatalf("closed: expected 2, got %d", fired)
	}
}
