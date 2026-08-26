// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package calculated

import (
	"testing"
)

// stubSourceDP is a lightweight SourceDP implementation for tests that
// need to control `RawValue` and `StateUncertain` independently without
// requiring a real *generic.DataPoint.
type stubSourceDP struct {
	value             any
	observed          bool
	uncertain         bool
	publishedRecently bool
	// unusable marks a source that was read but carries nothing to
	// calculate from — the observed-but-invalid case a real DP reaches via
	// a bad paired STATUS or an out-of-range reading.
	unusable bool
}

func (s *stubSourceDP) RawValue() (any, bool)        { return s.value, s.observed }
func (s *stubSourceDP) StateUncertain() bool         { return s.uncertain }
func (s *stubSourceDP) PublishedEventRecently() bool { return s.publishedRecently }
func (s *stubSourceDP) IsValid() bool                { return s.observed && !s.unusable }
func (s *stubSourceDP) setObserved(v any)            { s.value = v; s.observed = true }
func (s *stubSourceDP) setUncertain(u bool)          { s.uncertain = u }
func (s *stubSourceDP) clearObserved()               { s.value = nil; s.observed = false }
func (s *stubSourceDP) setPublishedRecently(p bool)  { s.publishedRecently = p }

// --- sourceSink unit tests ---

func TestSourceSinkNoSourcesIsUncertain(t *testing.T) {
	t.Parallel()
	var ss sourceSink
	if !ss.StateUncertain() {
		t.Fatal("sourceSink with no registered sources must report uncertain")
	}
}

func TestSourceSinkAllSourcesUnobservedIsUncertain(t *testing.T) {
	t.Parallel()
	var ss sourceSink
	a := &stubSourceDP{}
	b := &stubSourceDP{}
	ss.RegisterSource(a)
	ss.RegisterSource(b)
	if !ss.StateUncertain() {
		t.Fatal("unobserved sources must report uncertain")
	}
}

func TestSourceSinkOneSourceUnobservedIsUncertain(t *testing.T) {
	t.Parallel()
	var ss sourceSink
	a := &stubSourceDP{}
	b := &stubSourceDP{}
	ss.RegisterSource(a)
	ss.RegisterSource(b)
	a.setObserved(20.0) // only temperature observed
	if !ss.StateUncertain() {
		t.Fatal("one unobserved source must still report uncertain")
	}
}

func TestSourceSinkAllSourcesObservedNotUncertain(t *testing.T) {
	t.Parallel()
	var ss sourceSink
	a := &stubSourceDP{}
	b := &stubSourceDP{}
	ss.RegisterSource(a)
	ss.RegisterSource(b)
	a.setObserved(20.0)
	b.setObserved(60.0)
	if ss.StateUncertain() {
		t.Fatal("all observed, no optimistic write → must report NOT uncertain")
	}
}

func TestSourceSinkOptimisticWriteIsUncertain(t *testing.T) {
	t.Parallel()
	var ss sourceSink
	a := &stubSourceDP{}
	b := &stubSourceDP{}
	ss.RegisterSource(a)
	ss.RegisterSource(b)
	a.setObserved(20.0)
	b.setObserved(60.0)
	// Simulate an optimistic write on one of the sources.
	a.setUncertain(true)
	if !ss.StateUncertain() {
		t.Fatal("optimistic write on any source must report uncertain")
	}
}

func TestSourceSinkOptimisticWriteClearedNotUncertain(t *testing.T) {
	t.Parallel()
	var ss sourceSink
	a := &stubSourceDP{}
	b := &stubSourceDP{}
	ss.RegisterSource(a)
	ss.RegisterSource(b)
	a.setObserved(20.0)
	b.setObserved(60.0)
	a.setUncertain(true)  // optimistic write in flight
	a.setUncertain(false) // CCU confirms — cleared
	if ss.StateUncertain() {
		t.Fatal("after confirmation, optimistic state cleared → must report NOT uncertain")
	}
}

func TestSourceSinkNilRegistrationIsIgnored(t *testing.T) {
	t.Parallel()
	var ss sourceSink
	ss.RegisterSource(nil) // must not panic
	// No non-nil sources registered → still uncertain.
	if !ss.StateUncertain() {
		t.Fatal("nil registration must be ignored; no real sources → still uncertain")
	}
}

func TestSourceSinkIsRefreshedFromSourcesEmpty(t *testing.T) {
	t.Parallel()
	var ss sourceSink
	if ss.IsRefreshedFromSources() {
		t.Fatal("no sources → IsRefreshedFromSources must return false")
	}
}

func TestSourceSinkIsRefreshedFromSourcesPartial(t *testing.T) {
	t.Parallel()
	var ss sourceSink
	a := &stubSourceDP{}
	b := &stubSourceDP{}
	ss.RegisterSource(a)
	ss.RegisterSource(b)
	a.setObserved(1.0)
	if ss.IsRefreshedFromSources() {
		t.Fatal("only one of two sources observed → IsRefreshedFromSources must be false")
	}
}

func TestSourceSinkIsRefreshedFromSourcesAll(t *testing.T) {
	t.Parallel()
	var ss sourceSink
	a := &stubSourceDP{}
	b := &stubSourceDP{}
	ss.RegisterSource(a)
	ss.RegisterSource(b)
	a.setObserved(1.0)
	b.setObserved(2.0)
	if !ss.IsRefreshedFromSources() {
		t.Fatal("all sources observed → IsRefreshedFromSources must be true")
	}
}

// --- DewPointSensor StateUncertain integration ---

// TestDewPointStateUncertainBeforeAnyValue: both sources unregistered
// (no Subscribe called) → no sources → uncertain.
func TestDewPointStateUncertainBeforeAnyValue(t *testing.T) {
	t.Parallel()
	s := NewDewPointSensor()
	if !s.StateUncertain() {
		t.Fatal("DewPointSensor with no registered sources must report uncertain")
	}
}

// TestDewPointStateUncertainAfterManualSourceRegistration tests that
// manually registering sources and feeding values transitions the
// sensor to not-uncertain.
func TestDewPointStateUncertainAfterManualSourceRegistration(t *testing.T) {
	t.Parallel()
	s := NewDewPointSensor()

	tempDP := &stubSourceDP{}
	humDP := &stubSourceDP{}
	s.RegisterSource(tempDP)
	s.RegisterSource(humDP)

	// Neither source observed → uncertain.
	if !s.StateUncertain() {
		t.Fatal("before any value: both unobserved → must be uncertain")
	}

	// One source observed → still uncertain.
	tempDP.setObserved(22.0)
	s.OnTemperature(22.0)
	if !s.StateUncertain() {
		t.Fatal("after first value (humidity still missing): must be uncertain")
	}

	// Both sources observed → not uncertain.
	humDP.setObserved(60.0)
	s.OnHumidity(60.0)
	if s.StateUncertain() {
		t.Fatal("after both values observed: must NOT be uncertain")
	}
}

// TestDewPointStateUncertainOptimisticWrite: after both sources are
// observed, an optimistic write on one makes the sensor uncertain again.
func TestDewPointStateUncertainOptimisticWrite(t *testing.T) {
	t.Parallel()
	s := NewDewPointSensor()

	tempDP := &stubSourceDP{}
	humDP := &stubSourceDP{}
	s.RegisterSource(tempDP)
	s.RegisterSource(humDP)

	tempDP.setObserved(22.0)
	s.OnTemperature(22.0)
	humDP.setObserved(60.0)
	s.OnHumidity(60.0)

	// Simulate CCU-pending write on temperature.
	tempDP.setUncertain(true)
	if !s.StateUncertain() {
		t.Fatal("optimistic write on temperature source → DewPoint must be uncertain")
	}

	// Write confirmed.
	tempDP.setUncertain(false)
	if s.StateUncertain() {
		t.Fatal("after CCU confirmation → DewPoint must NOT be uncertain")
	}
}

// TestDewPointStateUncertainAfterSourceCleared: a source DP that was
// observed is cleared (optimistically rolled back) → uncertain again.
func TestDewPointStateUncertainAfterSourceCleared(t *testing.T) {
	t.Parallel()
	s := NewDewPointSensor()

	tempDP := &stubSourceDP{}
	humDP := &stubSourceDP{}
	s.RegisterSource(tempDP)
	s.RegisterSource(humDP)

	tempDP.setObserved(22.0)
	humDP.setObserved(60.0)
	s.OnTemperature(22.0)
	s.OnHumidity(60.0)

	if s.StateUncertain() {
		t.Fatal("precondition: both observed → must NOT be uncertain")
	}

	// Simulate rollback: source DP clears its observed flag.
	tempDP.clearObserved()
	if !s.StateUncertain() {
		t.Fatal("source cleared (optimistic rollback) → DewPoint must be uncertain")
	}
}

// --- Contract: all Sensor implementations expose StateUncertain ---

// TestAllSensorsImplementStateUncertain verifies that the compile-time
// interface assertions in contract.go cover StateUncertain. Since
// contract.go already asserts all sensor types satisfy Sensor, and
// Sensor now includes StateUncertain(), a successful build implies
// coverage. This test is a redundant runtime check.
func TestAllSensorsImplementStateUncertain(t *testing.T) {
	t.Parallel()
	sensors := []Sensor{
		NewDewPointSensor(),
		NewDewPointSpreadSensor(),
		NewFrostPointSensor(),
		NewVaporConcentrationSensor(),
		NewEnthalpySensor(),
		NewApparentTemperatureSensor(),
		NewOperatingVoltageLevelSensor(),
		NewWindowOpenSensor(),
	}
	for _, s := range sensors {
		// Before any value → no sources registered → uncertain.
		if !s.StateUncertain() {
			t.Errorf("%T.StateUncertain() = false before any source; want true", s)
		}
	}
}

// --- shouldPublishCalcUpdate / PublishedEventRecently guard ---

// TestShouldPublishCalcUpdateOneSrcAlwaysTrue verifies the ≤1 source
// fast path: with at most one source the guard always allows publishing.
func TestShouldPublishCalcUpdateOneSrcAlwaysTrue(t *testing.T) {
	t.Parallel()
	src := &stubSourceDP{}
	src.setPublishedRecently(true)
	if !shouldPublishCalcUpdate([]SourceDP{src}) {
		t.Fatal("with ≤1 source, shouldPublishCalcUpdate must always return true")
	}
	if !shouldPublishCalcUpdate(nil) {
		t.Fatal("with no sources, shouldPublishCalcUpdate must return true")
	}
}

// TestShouldPublishCalcUpdateAllowsWhenNoSourceStampsAPublishTime pins the
// production shape the guard's direction depends on: no wire data point
// installs a publisher, so PublishedEventRecently is permanently false for
// every source. Suppressing on that answer would silence every
// multi-source calculated sensor, which is why the comparison in
// shouldPublishCalcUpdate cannot be aligned with the reference contract
// until the publish stamping exists.
func TestShouldPublishCalcUpdateAllowsWhenNoSourceStampsAPublishTime(t *testing.T) {
	t.Parallel()
	a := &stubSourceDP{}
	b := &stubSourceDP{}
	if !shouldPublishCalcUpdate([]SourceDP{a, b}) {
		t.Fatal("sources that never stamp a publish time must not suppress the publish")
	}
}

// TestCalculatedSuppressesPublishWhenSourcesRecentlyPublished asserts that
// when all ≥2 sources published recently, shouldPublishCalcUpdate returns false.
func TestCalculatedSuppressesPublishWhenSourcesRecentlyPublished(t *testing.T) {
	t.Parallel()

	// Both sources published recently → suppress.
	a := &stubSourceDP{}
	b := &stubSourceDP{}
	a.setPublishedRecently(true)
	b.setPublishedRecently(true)
	if shouldPublishCalcUpdate([]SourceDP{a, b}) {
		t.Fatal("all sources published recently → shouldPublishCalcUpdate must return false")
	}

	// One source NOT published recently → allow.
	b.setPublishedRecently(false)
	if !shouldPublishCalcUpdate([]SourceDP{a, b}) {
		t.Fatal("one source not published recently → shouldPublishCalcUpdate must return true")
	}

	// Neither published recently → allow.
	a.setPublishedRecently(false)
	if !shouldPublishCalcUpdate([]SourceDP{a, b}) {
		t.Fatal("no sources published recently → shouldPublishCalcUpdate must return true")
	}
}

// TestShouldPublishCalcUpdateThreeSources verifies that with 3 sources all
// must have published recently to trigger suppression.
func TestShouldPublishCalcUpdateThreeSources(t *testing.T) {
	t.Parallel()
	a := &stubSourceDP{}
	b := &stubSourceDP{}
	c := &stubSourceDP{}
	a.setPublishedRecently(true)
	b.setPublishedRecently(true)
	// c starts with publishedRecently = false (zero value)
	if !shouldPublishCalcUpdate([]SourceDP{a, b, c}) {
		t.Fatal("one source not published → shouldPublishCalcUpdate must allow")
	}
	c.setPublishedRecently(true)
	if shouldPublishCalcUpdate([]SourceDP{a, b, c}) {
		t.Fatal("all 3 published recently → shouldPublishCalcUpdate must suppress")
	}
}

// --- SourcesValid ---

func TestSourceSinkSourcesValid(t *testing.T) {
	t.Parallel()

	t.Run("no sources is not valid", func(t *testing.T) {
		t.Parallel()
		var ss sourceSink
		if ss.SourcesValid() {
			t.Fatal("without a state carrier there is nothing to derive a value from")
		}
	})

	t.Run("all sources usable", func(t *testing.T) {
		t.Parallel()
		var ss sourceSink
		ss.RegisterSource(&stubSourceDP{value: 20.0, observed: true})
		ss.RegisterSource(&stubSourceDP{value: 50.0, observed: true})
		if !ss.SourcesValid() {
			t.Fatal("every source carries a usable reading")
		}
	})

	t.Run("one source read but unusable", func(t *testing.T) {
		t.Parallel()
		var ss sourceSink
		ss.RegisterSource(&stubSourceDP{value: 20.0, observed: true, unusable: true})
		ss.RegisterSource(&stubSourceDP{value: 50.0, observed: true})
		if ss.SourcesValid() {
			t.Fatal("an observed-but-unusable source must invalidate the aggregate")
		}
	})

	t.Run("unobserved source", func(t *testing.T) {
		t.Parallel()
		var ss sourceSink
		ss.RegisterSource(&stubSourceDP{})
		if ss.SourcesValid() {
			t.Fatal("a source that never delivered a value is not valid")
		}
	})
}
