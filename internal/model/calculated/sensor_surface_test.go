// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// Package calculated tests the DataPoint surface methods (Default, Max, Min,
// Multiplier, Service, Values, DataPointNamePostfix, HasDataPoints, IsStatusValid,
// TranslationKey, ModifiedAt, RefreshedAt, IsStateChange, ParamsetKey,
// LoadDataPointValue) across all calculated sensor types.
package calculated

import (
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// ---------------------------------------------------------------------------
// Helpers shared by this file
// ---------------------------------------------------------------------------

// stubTimestampSourceDP implements both SourceDP and sourceTimestampProvider
// so we can drive aggregateModifiedAt / aggregateRefreshedAt.
type stubTimestampSourceDP struct {
	stubSourceDP
	modifiedAt  time.Time
	refreshedAt time.Time
}

func (s *stubTimestampSourceDP) ModifiedAt() time.Time  { return s.modifiedAt }
func (s *stubTimestampSourceDP) RefreshedAt() time.Time { return s.refreshedAt }

// stubDataPointKeyDP satisfies SourceDP + dataPointKeyProvider for
// LoadDataPointValue testing.
type stubDataPointKeyDP struct {
	stubSourceDP
	key hmtypes.DataPointKey
}

func (s *stubDataPointKeyDP) DataPointKey() hmtypes.DataPointKey { return s.key }

// ---------------------------------------------------------------------------
// DewPoint — P2 surface
// ---------------------------------------------------------------------------

// TestDewPointSensorP2Methods verifies P2 surface on DewPointSensor.
func TestDewPointSensorP2Methods(t *testing.T) {
	s := NewDewPointSensor()

	if s.Default() != nil {
		t.Error("Default() must be nil")
	}
	if _, ok := s.Max(); ok {
		t.Error("Max() must return (0, false)")
	}
	if _, ok := s.Min(); ok {
		t.Error("Min() must return (0, false)")
	}
	if s.Multiplier() != 1.0 {
		t.Errorf("Multiplier()=%v, want 1.0", s.Multiplier())
	}
	if s.Service() {
		t.Error("Service() must be false")
	}
	if s.Values() != nil {
		t.Error("Values() must be nil")
	}
	if s.DataPointNamePostfix() != "" {
		t.Error("DataPointNamePostfix() must be empty")
	}
	if s.HasDataPoints() {
		t.Error("HasDataPoints() must be false when no sources registered")
	}
	if s.IsStatusValid() {
		t.Error("IsStatusValid() must be false when no sources")
	}
	want := calcTranslationKey(hmenum.CalculatedParameterDewPoint)
	if got := s.TranslationKey(); got != want {
		t.Errorf("TranslationKey()=%q, want %q", got, want)
	}
	if !s.ModifiedAt().IsZero() {
		t.Error("ModifiedAt() must be zero with no sources")
	}
	if !s.RefreshedAt().IsZero() {
		t.Error("RefreshedAt() must be zero with no sources")
	}
	if s.IsStateChange() {
		t.Error("IsStateChange() must be false before any emission")
	}
}

// TestCalculatedTranslationKeyLowercases verifies the translation key
// helper lowercases the parameter name.
func TestCalculatedTranslationKeyLowercases(t *testing.T) {
	got := calcTranslationKey(hmenum.CalculatedParameterDewPoint)
	if got != "dew_point" {
		t.Errorf("calcTranslationKey(DEW_POINT)=%q, want dew_point", got)
	}
	got2 := calcTranslationKey(hmenum.CalculatedParameterApparentTemperature)
	if got2 != "apparent_temperature" {
		t.Errorf("calcTranslationKey(APPARENT_TEMPERATURE)=%q, want apparent_temperature", got2)
	}
}

// TestApparentTemperatureSensorP2Methods spot-checks a second sensor type.
func TestApparentTemperatureSensorP2Methods(t *testing.T) {
	s := NewApparentTemperatureSensor()

	if s.Default() != nil {
		t.Error("Default() must be nil")
	}
	if s.Multiplier() != 1.0 {
		t.Errorf("Multiplier()=%v, want 1.0", s.Multiplier())
	}
	if s.Service() {
		t.Error("Service() must be false")
	}
	want := calcTranslationKey(hmenum.CalculatedParameterApparentTemperature)
	if got := s.TranslationKey(); got != want {
		t.Errorf("TranslationKey()=%q, want %q", got, want)
	}
}

// TestOperatingVoltageLevelSensorP2Methods spot-checks OVL sensor.
func TestOperatingVoltageLevelSensorP2Methods(t *testing.T) {
	s := NewOperatingVoltageLevelSensor()
	if s.Multiplier() != 1.0 {
		t.Errorf("Multiplier()=%v, want 1.0", s.Multiplier())
	}
	if s.Values() != nil {
		t.Error("Values() must be nil")
	}
	want := calcTranslationKey(hmenum.CalculatedParameterOperatingVoltageLevel)
	if got := s.TranslationKey(); got != want {
		t.Errorf("TranslationKey()=%q, want %q", got, want)
	}
}

// TestDerivedBinarySensorP2Methods spot-checks DerivedBinarySensor.
func TestDerivedBinarySensorP2Methods(t *testing.T) {
	s := NewDerivedBinarySensor(
		hmenum.CalculatedParameterWindowOpen,
		[]string{"OPEN"},
		nil,
	)
	if s.Multiplier() != 1.0 {
		t.Errorf("Multiplier()=%v, want 1.0", s.Multiplier())
	}
	if s.Service() {
		t.Error("Service() must be false")
	}
	want := calcTranslationKey(hmenum.CalculatedParameterWindowOpen)
	if got := s.TranslationKey(); got != want {
		t.Errorf("TranslationKey()=%q, want %q", got, want)
	}
}

// TestCalculatedSensorsParamsetKey verifies that ParamsetKey() returns
// "CALCULATED" on all sensor types.
func TestCalculatedSensorsParamsetKey(t *testing.T) {
	cases := []struct {
		name string
		got  string
	}{
		{"DewPointSensor", NewDewPointSensor().ParamsetKey()},
		{"DewPointSpreadSensor", NewDewPointSpreadSensor().ParamsetKey()},
		{"FrostPointSensor", NewFrostPointSensor().ParamsetKey()},
		{"VaporConcentrationSensor", NewVaporConcentrationSensor().ParamsetKey()},
		{"EnthalpySensor", NewEnthalpySensor().ParamsetKey()},
		{"ApparentTemperatureSensor", NewApparentTemperatureSensor().ParamsetKey()},
		{"OperatingVoltageLevelSensor", NewOperatingVoltageLevelSensor().ParamsetKey()},
		{
			"DerivedBinarySensor",
			NewDerivedBinarySensor(hmenum.CalculatedParameterWindowOpen, []string{"OPEN"}, nil).ParamsetKey(),
		},
	}
	for _, tc := range cases {
		if tc.got != "CALCULATED" {
			t.Errorf("%s: ParamsetKey()=%q, want CALCULATED", tc.name, tc.got)
		}
	}
}

// ---------------------------------------------------------------------------
// DewPointSpread — full P2 surface
// ---------------------------------------------------------------------------

func TestDewPointSpreadSensorP2Full(t *testing.T) {
	s := NewDewPointSpreadSensor()

	if s.Default() != nil {
		t.Error("DewPointSpread.Default() must be nil")
	}
	if _, ok := s.Max(); ok {
		t.Error("DewPointSpread.Max() must return (0, false)")
	}
	if _, ok := s.Min(); ok {
		t.Error("DewPointSpread.Min() must return (0, false)")
	}
	if s.Multiplier() != 1.0 {
		t.Errorf("DewPointSpread.Multiplier()=%v, want 1.0", s.Multiplier())
	}
	if s.Service() {
		t.Error("DewPointSpread.Service() must be false")
	}
	if s.Values() != nil {
		t.Error("DewPointSpread.Values() must be nil")
	}
	if s.DataPointNamePostfix() != "" {
		t.Error("DewPointSpread.DataPointNamePostfix() must be empty")
	}
	if s.HasDataPoints() {
		t.Error("DewPointSpread.HasDataPoints() must be false when no sources")
	}
	if s.IsStatusValid() {
		t.Error("DewPointSpread.IsStatusValid() must be false when no sources")
	}
	want := calcTranslationKey(hmenum.CalculatedParameterDewPointSpread)
	if got := s.TranslationKey(); got != want {
		t.Errorf("DewPointSpread.TranslationKey()=%q, want %q", got, want)
	}
	if !s.ModifiedAt().IsZero() {
		t.Error("DewPointSpread.ModifiedAt() must be zero with no sources")
	}
	if !s.RefreshedAt().IsZero() {
		t.Error("DewPointSpread.RefreshedAt() must be zero with no sources")
	}
	if s.IsStateChange() {
		t.Error("DewPointSpread.IsStateChange() must be false before emission")
	}
}

// ---------------------------------------------------------------------------
// FrostPoint — full P2 surface
// ---------------------------------------------------------------------------

func TestFrostPointSensorP2Full(t *testing.T) {
	s := NewFrostPointSensor()

	if s.Default() != nil {
		t.Error("FrostPoint.Default() must be nil")
	}
	if _, ok := s.Max(); ok {
		t.Error("FrostPoint.Max() must return (0, false)")
	}
	if _, ok := s.Min(); ok {
		t.Error("FrostPoint.Min() must return (0, false)")
	}
	if s.Multiplier() != 1.0 {
		t.Errorf("FrostPoint.Multiplier()=%v, want 1.0", s.Multiplier())
	}
	if s.Service() {
		t.Error("FrostPoint.Service() must be false")
	}
	if s.Values() != nil {
		t.Error("FrostPoint.Values() must be nil")
	}
	if s.DataPointNamePostfix() != "" {
		t.Error("FrostPoint.DataPointNamePostfix() must be empty")
	}
	if s.HasDataPoints() {
		t.Error("FrostPoint.HasDataPoints() must be false when no sources")
	}
	if s.IsStatusValid() {
		t.Error("FrostPoint.IsStatusValid() must be false when no sources")
	}
	want := calcTranslationKey(hmenum.CalculatedParameterFrostPoint)
	if got := s.TranslationKey(); got != want {
		t.Errorf("FrostPoint.TranslationKey()=%q, want %q", got, want)
	}
	if !s.ModifiedAt().IsZero() {
		t.Error("FrostPoint.ModifiedAt() must be zero with no sources")
	}
	if !s.RefreshedAt().IsZero() {
		t.Error("FrostPoint.RefreshedAt() must be zero with no sources")
	}
	if s.IsStateChange() {
		t.Error("FrostPoint.IsStateChange() must be false before emission")
	}
}

// ---------------------------------------------------------------------------
// VaporConcentration — full P2 surface
// ---------------------------------------------------------------------------

func TestVaporConcentrationSensorP2Full(t *testing.T) {
	s := NewVaporConcentrationSensor()

	if s.Default() != nil {
		t.Error("VaporConcentration.Default() must be nil")
	}
	if _, ok := s.Max(); ok {
		t.Error("VaporConcentration.Max() must return (0, false)")
	}
	if _, ok := s.Min(); ok {
		t.Error("VaporConcentration.Min() must return (0, false)")
	}
	if s.Multiplier() != 1.0 {
		t.Errorf("VaporConcentration.Multiplier()=%v, want 1.0", s.Multiplier())
	}
	if s.Service() {
		t.Error("VaporConcentration.Service() must be false")
	}
	if s.Values() != nil {
		t.Error("VaporConcentration.Values() must be nil")
	}
	if s.DataPointNamePostfix() != "" {
		t.Error("VaporConcentration.DataPointNamePostfix() must be empty")
	}
	if s.HasDataPoints() {
		t.Error("VaporConcentration.HasDataPoints() must be false when no sources")
	}
	if s.IsStatusValid() {
		t.Error("VaporConcentration.IsStatusValid() must be false when no sources")
	}
	want := calcTranslationKey(hmenum.CalculatedParameterVaporConcentration)
	if got := s.TranslationKey(); got != want {
		t.Errorf("VaporConcentration.TranslationKey()=%q, want %q", got, want)
	}
	if !s.ModifiedAt().IsZero() {
		t.Error("VaporConcentration.ModifiedAt() must be zero with no sources")
	}
	if !s.RefreshedAt().IsZero() {
		t.Error("VaporConcentration.RefreshedAt() must be zero with no sources")
	}
	if s.IsStateChange() {
		t.Error("VaporConcentration.IsStateChange() must be false before emission")
	}
}

// ---------------------------------------------------------------------------
// Enthalpy — full P2 surface
// ---------------------------------------------------------------------------

func TestEnthalpySensorP2Full(t *testing.T) {
	s := NewEnthalpySensor()

	if s.Default() != nil {
		t.Error("Enthalpy.Default() must be nil")
	}
	if _, ok := s.Max(); ok {
		t.Error("Enthalpy.Max() must return (0, false)")
	}
	if _, ok := s.Min(); ok {
		t.Error("Enthalpy.Min() must return (0, false)")
	}
	if s.Multiplier() != 1.0 {
		t.Errorf("Enthalpy.Multiplier()=%v, want 1.0", s.Multiplier())
	}
	if s.Service() {
		t.Error("Enthalpy.Service() must be false")
	}
	if s.Values() != nil {
		t.Error("Enthalpy.Values() must be nil")
	}
	if s.DataPointNamePostfix() != "" {
		t.Error("Enthalpy.DataPointNamePostfix() must be empty")
	}
	if s.HasDataPoints() {
		t.Error("Enthalpy.HasDataPoints() must be false when no sources")
	}
	if s.IsStatusValid() {
		t.Error("Enthalpy.IsStatusValid() must be false when no sources")
	}
	want := calcTranslationKey(hmenum.CalculatedParameterEnthalpy)
	if got := s.TranslationKey(); got != want {
		t.Errorf("Enthalpy.TranslationKey()=%q, want %q", got, want)
	}
	if !s.ModifiedAt().IsZero() {
		t.Error("Enthalpy.ModifiedAt() must be zero with no sources")
	}
	if !s.RefreshedAt().IsZero() {
		t.Error("Enthalpy.RefreshedAt() must be zero with no sources")
	}
	if s.IsStateChange() {
		t.Error("Enthalpy.IsStateChange() must be false before emission")
	}
}

// ---------------------------------------------------------------------------
// ApparentTemperature — remaining P2 surface
// ---------------------------------------------------------------------------

func TestApparentTemperatureSensorP2Remaining(t *testing.T) {
	s := NewApparentTemperatureSensor()

	if s.Default() != nil {
		t.Error("ApparentTemperature.Default() must be nil")
	}
	if _, ok := s.Max(); ok {
		t.Error("ApparentTemperature.Max() must return (0, false)")
	}
	if _, ok := s.Min(); ok {
		t.Error("ApparentTemperature.Min() must return (0, false)")
	}
	if s.Values() != nil {
		t.Error("ApparentTemperature.Values() must be nil")
	}
	if s.DataPointNamePostfix() != "" {
		t.Error("ApparentTemperature.DataPointNamePostfix() must be empty")
	}
	if s.HasDataPoints() {
		t.Error("ApparentTemperature.HasDataPoints() must be false when no sources")
	}
	if s.IsStatusValid() {
		t.Error("ApparentTemperature.IsStatusValid() must be false when no sources")
	}
	if !s.ModifiedAt().IsZero() {
		t.Error("ApparentTemperature.ModifiedAt() must be zero with no sources")
	}
	if !s.RefreshedAt().IsZero() {
		t.Error("ApparentTemperature.RefreshedAt() must be zero with no sources")
	}
	if s.IsStateChange() {
		t.Error("ApparentTemperature.IsStateChange() must be false before emission")
	}
}

// ---------------------------------------------------------------------------
// OperatingVoltageLevel — remaining P2 surface
// ---------------------------------------------------------------------------

func TestOperatingVoltageLevelSensorP2Remaining(t *testing.T) {
	s := NewOperatingVoltageLevelSensor()

	if s.Default() != nil {
		t.Error("OVL.Default() must be nil")
	}
	if _, ok := s.Max(); ok {
		t.Error("OVL.Max() must return (0, false)")
	}
	if _, ok := s.Min(); ok {
		t.Error("OVL.Min() must return (0, false)")
	}
	if s.Service() {
		t.Error("OVL.Service() must be false")
	}
	if s.DataPointNamePostfix() != "" {
		t.Error("OVL.DataPointNamePostfix() must be empty")
	}
	if s.HasDataPoints() {
		t.Error("OVL.HasDataPoints() must be false when no sources")
	}
	if s.IsStatusValid() {
		t.Error("OVL.IsStatusValid() must be false when no sources")
	}
	if !s.ModifiedAt().IsZero() {
		t.Error("OVL.ModifiedAt() must be zero with no sources")
	}
	if !s.RefreshedAt().IsZero() {
		t.Error("OVL.RefreshedAt() must be zero with no sources")
	}
	if s.IsStateChange() {
		t.Error("OVL.IsStateChange() must be false before emission")
	}
}

// ---------------------------------------------------------------------------
// DerivedBinarySensor — remaining P2 surface
// ---------------------------------------------------------------------------

func TestDerivedBinarySensorP2Remaining(t *testing.T) {
	s := NewDerivedBinarySensor(hmenum.CalculatedParameterWindowOpen, []string{"OPEN"}, nil)

	if s.Default() != nil {
		t.Error("DerivedBinary.Default() must be nil")
	}
	if _, ok := s.Max(); ok {
		t.Error("DerivedBinary.Max() must return (0, false)")
	}
	if _, ok := s.Min(); ok {
		t.Error("DerivedBinary.Min() must return (0, false)")
	}
	if s.Values() != nil {
		t.Error("DerivedBinary.Values() must be nil")
	}
	if s.DataPointNamePostfix() != "" {
		t.Error("DerivedBinary.DataPointNamePostfix() must be empty")
	}
	if s.HasDataPoints() {
		t.Error("DerivedBinary.HasDataPoints() must be false when no sources")
	}
	if s.IsStatusValid() {
		t.Error("DerivedBinary.IsStatusValid() must be false when no sources")
	}
	if !s.ModifiedAt().IsZero() {
		t.Error("DerivedBinary.ModifiedAt() must be zero with no sources")
	}
	if !s.RefreshedAt().IsZero() {
		t.Error("DerivedBinary.RefreshedAt() must be zero with no sources")
	}
	if s.IsStateChange() {
		t.Error("DerivedBinary.IsStateChange() must be false before any label")
	}
}

// ---------------------------------------------------------------------------
// DerivedBinarySensor.IsRefreshed transitions
// ---------------------------------------------------------------------------

func TestDerivedBinarySensorIsRefreshedTransitions(t *testing.T) {
	s := NewWindowOpenSensor()

	if s.IsRefreshed() {
		t.Fatal("IsRefreshed() must be false before any label is fed")
	}
	s.OnLabel("OPEN")
	if !s.IsRefreshed() {
		t.Fatal("IsRefreshed() must be true after a known label is classified")
	}
	s.OnLabel("CLOSED")
	if !s.IsRefreshed() {
		t.Fatal("IsRefreshed() must remain true after subsequent labels")
	}
}

// TestDerivedBinarySensorIsStateChange verifies IsStateChange transitions.
func TestDerivedBinarySensorIsStateChange(t *testing.T) {
	s := NewWindowOpenSensor()

	src := &stubSourceDP{}
	s.RegisterSource(src)

	if s.IsStateChange() {
		t.Error("IsStateChange() must be false when not refreshed")
	}

	s.OnLabel("OPEN")
	if s.IsStateChange() {
		t.Error("IsStateChange() must be false when uncertain (source unobserved)")
	}

	src.setObserved("OPEN")
	if !s.IsStateChange() {
		t.Error("IsStateChange() must be true when refreshed and not uncertain")
	}
}

// ---------------------------------------------------------------------------
// LowBatLimitDefault
// ---------------------------------------------------------------------------

func TestLowBatLimitDefaultBeforeSubscribe(t *testing.T) {
	s := NewOperatingVoltageLevelSensor()

	v, ok := s.LowBatLimitDefault()
	if ok {
		t.Fatalf("LowBatLimitDefault() must return ok=false before subscribe; got v=%v", v)
	}
	if v != 0 {
		t.Fatalf("LowBatLimitDefault() value must be 0 before subscribe; got %v", v)
	}
}

func TestLowBatLimitDefaultAfterDirectSet(t *testing.T) {
	s := NewOperatingVoltageLevelSensor()

	s.lowBatLimitDefault = 2.1
	s.hasLowBatDefault = true

	v, ok := s.LowBatLimitDefault()
	if !ok {
		t.Fatal("LowBatLimitDefault() must return ok=true after default is set")
	}
	if v != 2.1 {
		t.Fatalf("LowBatLimitDefault()=%v, want 2.1", v)
	}
}

// ---------------------------------------------------------------------------
// sourceSink.LoadDataPointValue
// ---------------------------------------------------------------------------

func TestSourceSinkLoadDataPointValueNilLoader(t *testing.T) {
	var ss sourceSink
	dp := &stubDataPointKeyDP{
		key: hmtypes.DataPointKey{ChannelAddress: "VCU:1", Parameter: "HUMIDITY"},
	}
	dp.setObserved(50.0)
	ss.RegisterSource(dp)
	ss.LoadDataPointValue(nil)
}

func TestSourceSinkLoadDataPointValueCalls(t *testing.T) {
	var ss sourceSink

	dp1 := &stubDataPointKeyDP{
		key: hmtypes.DataPointKey{ChannelAddress: "VCU:1", Parameter: "ACTUAL_TEMPERATURE"},
	}
	dp1.setObserved(20.0)

	dp2 := &stubSourceDP{}
	dp2.setObserved(50.0)

	ss.RegisterSource(dp1)
	ss.RegisterSource(dp2)

	var calls []struct{ addr, param string }
	ss.LoadDataPointValue(func(addr, param string) {
		calls = append(calls, struct{ addr, param string }{addr, param})
	})

	if len(calls) != 1 {
		t.Fatalf("expected 1 loader call (only dp1 has a key), got %d", len(calls))
	}
	if calls[0].addr != "VCU:1" || calls[0].param != "ACTUAL_TEMPERATURE" {
		t.Fatalf("unexpected key: %+v", calls[0])
	}
}

// TestAllSensorsLoadDataPointValue verifies that every sensor type's
// LoadDataPointValue does not panic with no registered sources.
func TestAllSensorsLoadDataPointValue(t *testing.T) {
	sensors := []struct {
		name   string
		loader func(func(string, string))
	}{
		{
			"DewPoint",
			func(f func(string, string)) { NewDewPointSensor().LoadDataPointValue(f) },
		},
		{
			"DewPointSpread",
			func(f func(string, string)) { NewDewPointSpreadSensor().LoadDataPointValue(f) },
		},
		{
			"FrostPoint",
			func(f func(string, string)) { NewFrostPointSensor().LoadDataPointValue(f) },
		},
		{
			"VaporConcentration",
			func(f func(string, string)) { NewVaporConcentrationSensor().LoadDataPointValue(f) },
		},
		{
			"Enthalpy",
			func(f func(string, string)) { NewEnthalpySensor().LoadDataPointValue(f) },
		},
		{
			"ApparentTemperature",
			func(f func(string, string)) { NewApparentTemperatureSensor().LoadDataPointValue(f) },
		},
		{
			"OperatingVoltageLevel",
			func(f func(string, string)) { NewOperatingVoltageLevelSensor().LoadDataPointValue(f) },
		},
		{
			"DerivedBinary",
			func(f func(string, string)) {
				NewDerivedBinarySensor(
					hmenum.CalculatedParameterWindowOpen,
					[]string{"OPEN"}, nil,
				).LoadDataPointValue(f)
			},
		},
	}

	for _, s := range sensors {
		t.Run(s.name, func(t *testing.T) {
			var called bool
			s.loader(func(_, _ string) { called = true })
			if called {
				t.Fatalf("%s: loader called with no registered sources", s.name)
			}
		})
	}
}

// TestDewPointLoadDataPointValueWithSource verifies that a registered source
// with a key causes the loader to be called.
func TestDewPointLoadDataPointValueWithSource(t *testing.T) {
	s := NewDewPointSensor()
	dp := &stubDataPointKeyDP{
		key: hmtypes.DataPointKey{ChannelAddress: "ADDR:1", Parameter: "ACTUAL_TEMPERATURE"},
	}
	dp.setObserved(22.0)
	s.RegisterSource(dp)

	var calls []string
	s.LoadDataPointValue(func(addr, param string) {
		calls = append(calls, addr+"/"+param)
	})
	if len(calls) != 1 || calls[0] != "ADDR:1/ACTUAL_TEMPERATURE" {
		t.Fatalf("unexpected loader calls: %v", calls)
	}
}

// ---------------------------------------------------------------------------
// aggregateModifiedAt / aggregateRefreshedAt
// ---------------------------------------------------------------------------

func TestAggregateTimestampsWithSources(t *testing.T) {
	var ss sourceSink

	now := time.Now().Truncate(time.Millisecond)
	earlier := now.Add(-time.Hour)

	dp1 := &stubTimestampSourceDP{modifiedAt: earlier, refreshedAt: earlier}
	dp1.setObserved(1.0)
	dp2 := &stubTimestampSourceDP{modifiedAt: now, refreshedAt: now}
	dp2.setObserved(2.0)

	ss.RegisterSource(dp1)
	ss.RegisterSource(dp2)

	if got := ss.aggregateModifiedAt(); !got.Equal(now) {
		t.Errorf("aggregateModifiedAt()=%v, want %v (max of %v and %v)", got, now, earlier, now)
	}
	if got := ss.aggregateRefreshedAt(); !got.Equal(now) {
		t.Errorf("aggregateRefreshedAt()=%v, want %v", got, now)
	}
}

func TestAggregateTimestampsSkipsNonProvider(t *testing.T) {
	var ss sourceSink

	now := time.Now().Truncate(time.Millisecond)
	dp1 := &stubTimestampSourceDP{modifiedAt: now, refreshedAt: now}
	dp1.setObserved(1.0)
	dp2 := &stubSourceDP{}
	dp2.setObserved(2.0)

	ss.RegisterSource(dp1)
	ss.RegisterSource(dp2)

	if got := ss.aggregateModifiedAt(); !got.Equal(now) {
		t.Errorf("aggregateModifiedAt()=%v, want %v", got, now)
	}
}

func TestDewPointSensorTimestampsWithSources(t *testing.T) {
	s := NewDewPointSensor()

	now := time.Now().Truncate(time.Millisecond)
	dp1 := &stubTimestampSourceDP{modifiedAt: now, refreshedAt: now}
	dp1.setObserved(20.0)
	dp2 := &stubTimestampSourceDP{modifiedAt: now.Add(-time.Minute), refreshedAt: now.Add(-time.Minute)}
	dp2.setObserved(50.0)

	s.RegisterSource(dp1)
	s.RegisterSource(dp2)

	if got := s.ModifiedAt(); !got.Equal(now) {
		t.Errorf("DewPoint.ModifiedAt()=%v, want %v", got, now)
	}
	if got := s.RefreshedAt(); !got.Equal(now) {
		t.Errorf("DewPoint.RefreshedAt()=%v, want %v", got, now)
	}
	if !s.HasDataPoints() {
		t.Error("DewPoint.HasDataPoints() must be true when all sources observed")
	}
	if !s.IsStatusValid() {
		t.Error("DewPoint.IsStatusValid() must be true when all sources certain")
	}
}

func TestDewPointIsStateChangeAfterEmission(t *testing.T) {
	s := NewDewPointSensor()

	src1 := &stubTimestampSourceDP{}
	src2 := &stubTimestampSourceDP{}
	src1.setObserved(22.0)
	src2.setObserved(60.0)
	s.RegisterSource(src1)
	s.RegisterSource(src2)

	s.OnTemperature(22)
	s.OnHumidity(60)

	if !s.IsStateChange() {
		t.Error("DewPoint.IsStateChange() must be true after emission with certain sources")
	}
}

// ---------------------------------------------------------------------------
// DerivedBinarySensor.MatterMeasurementClass default branch
// ---------------------------------------------------------------------------

func TestDerivedBinaryMatterClassDefaultBranch(t *testing.T) {
	unknown := hmenum.CalculatedParameter("UNKNOWN_FUTURE_PARAM")
	s := NewDerivedBinarySensor(unknown, []string{"ON"}, []string{"OFF"})
	if got := s.MatterMeasurementClass(); got != 0 {
		t.Errorf("default branch: got %v, want MatterMeasurementNone (0)", got)
	}
}

// ---------------------------------------------------------------------------
// masterSubscribe — nil channel branch
// ---------------------------------------------------------------------------

func TestMasterSubscribeNilDP(t *testing.T) {
	unsub := masterSubscribe(nil, hmenum.ParameterLowBatLimit, func(_ float64) {})
	if unsub != nil {
		t.Fatal("masterSubscribe(nil, ...) must return nil")
	}
}

// ---------------------------------------------------------------------------
// DerivedBinarySensor.Subscribe — nil channel and missing DP paths
// ---------------------------------------------------------------------------

func TestDerivedBinarySensorSubscribeNilChannel(t *testing.T) {
	s := NewWindowOpenSensor()
	s.SourceParameter = hmenum.ParameterState
	unsub := s.Subscribe(nil)
	if unsub != nil {
		t.Fatal("Subscribe(nil) must return nil")
	}
}

func TestDerivedBinarySensorSubscribeEmptySourceParameter(t *testing.T) {
	s := NewDerivedBinarySensor(hmenum.CalculatedParameterWindowOpen, []string{"OPEN"}, nil)
	unsub := s.Subscribe(nil)
	if unsub != nil {
		t.Fatal("Subscribe with empty SourceParameter must return nil")
	}
}

func TestDerivedBinarySensorSubscribeMissingDP(t *testing.T) {
	s := NewDerivedBinarySensor(hmenum.CalculatedParameterWindowOpen, []string{"OPEN"}, nil)
	s.SourceParameter = hmenum.ParameterState
	unsub := s.Subscribe(nil)
	if unsub != nil {
		t.Fatal("Subscribe(nil) must return nil even when SourceParameter is set")
	}
}
