// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package calculated

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/datapoint"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// Compile-time guards: every calculated DP family satisfies the
// [datapoint.BaseDataPoint] contract via the embedded
// [datapoint.BaseDataPointFields]. The runtime tests below exercise
// the methods so the contract is not purely static.
var (
	_ datapoint.BaseDataPoint = (*DewPointSensor)(nil)
	_ datapoint.BaseDataPoint = (*DewPointSpreadSensor)(nil)
	_ datapoint.BaseDataPoint = (*FrostPointSensor)(nil)
	_ datapoint.BaseDataPoint = (*VaporConcentrationSensor)(nil)
	_ datapoint.BaseDataPoint = (*EnthalpySensor)(nil)
	_ datapoint.BaseDataPoint = (*ApparentTemperatureSensor)(nil)
	_ datapoint.BaseDataPoint = (*OperatingVoltageLevelSensor)(nil)
)

// TestCalculatedDewPointUniqueID is the per-family stichprobe pinning the
// canonical "<central>:<channelAddress>:CALCULATED/<param>" UniqueID format.
func TestCalculatedDewPointUniqueID(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		central string
		address string
		want    string
	}{
		{
			name:    "production multi-CCU dp",
			central: "ccu-prod",
			address: "VCU0123:1",
			want:    "ccu-prod:VCU0123:1:CALCULATED/DEW_POINT",
		},
		{
			name:    "second central, same address",
			central: "ccu-secondary",
			address: "VCU0123:1",
			want:    "ccu-secondary:VCU0123:1:CALCULATED/DEW_POINT",
		},
		{
			name:    "legacy fixture (no central)",
			central: "",
			address: "",
			want:    "::CALCULATED/DEW_POINT",
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s := NewDewPointSensorWithIdentity(tc.central, tc.address)
			if got := s.UniqueID(); got != tc.want {
				t.Fatalf("UniqueID() = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestCalculatedSatisfiesBaseDataPoint exercises one DP per family so
// the promoted UniqueID / Visible / EnabledByDefault behave the same
// across the calculated package.
func TestCalculatedSatisfiesBaseDataPoint(t *testing.T) {
	t.Parallel()
	type tc struct {
		name string
		dp   datapoint.BaseDataPoint
		want string
	}
	cases := []tc{
		{
			name: "DewPoint",
			dp:   NewDewPointSensorWithIdentity("ccu", "VCU:1"),
			want: "ccu:VCU:1:CALCULATED/DEW_POINT",
		},
		{
			name: "DewPointSpread",
			dp:   NewDewPointSpreadSensorWithIdentity("ccu", "VCU:1"),
			want: "ccu:VCU:1:CALCULATED/DEW_POINT_SPREAD",
		},
		{
			name: "FrostPoint",
			dp:   NewFrostPointSensorWithIdentity("ccu", "VCU:1"),
			want: "ccu:VCU:1:CALCULATED/FROST_POINT",
		},
		{
			name: "VaporConcentration",
			dp:   NewVaporConcentrationSensorWithIdentity("ccu", "VCU:1"),
			want: "ccu:VCU:1:CALCULATED/VAPOR_CONCENTRATION",
		},
		{
			name: "Enthalpy",
			dp:   NewEnthalpySensorWithIdentity("ccu", "VCU:1"),
			want: "ccu:VCU:1:CALCULATED/ENTHALPY",
		},
		{
			name: "ApparentTemperature",
			dp:   NewApparentTemperatureSensorWithIdentity("ccu", "VCU:1"),
			want: "ccu:VCU:1:CALCULATED/APPARENT_TEMPERATURE",
		},
		{
			name: "OperatingVoltageLevel",
			dp:   NewOperatingVoltageLevelSensorWithIdentity("ccu", "VCU:1"),
			want: "ccu:VCU:1:CALCULATED/OPERATING_VOLTAGE_LEVEL",
		},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if got := c.dp.UniqueID(); got != c.want {
				t.Fatalf("UniqueID() = %q, want %q", got, c.want)
			}
			if !c.dp.Visible() {
				t.Fatal("Visible() must default to true")
			}
			if !c.dp.EnabledByDefault() {
				t.Fatal("EnabledByDefault() must default to true")
			}
		})
	}
}

// TestCalculatedLegacyConstructorsKeepFamilySuffix verifies that the
// backwards-compatible no-arg constructors still produce a usable
// UniqueID with the CALCULATED/<param> suffix preserved — even though
// the central / address segments are empty.
func TestCalculatedLegacyConstructorsKeepFamilySuffix(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		dp   datapoint.BaseDataPoint
		want string
	}{
		{name: "DewPoint", dp: NewDewPointSensor(), want: ":CALCULATED/DEW_POINT"},
		{name: "DewPointSpread", dp: NewDewPointSpreadSensor(), want: ":CALCULATED/DEW_POINT_SPREAD"},
		{name: "FrostPoint", dp: NewFrostPointSensor(), want: ":CALCULATED/FROST_POINT"},
		{name: "VaporConcentration", dp: NewVaporConcentrationSensor(), want: ":CALCULATED/VAPOR_CONCENTRATION"},
		{name: "Enthalpy", dp: NewEnthalpySensor(), want: ":CALCULATED/ENTHALPY"},
		{name: "ApparentTemperature", dp: NewApparentTemperatureSensor(), want: ":CALCULATED/APPARENT_TEMPERATURE"},
		{name: "OperatingVoltageLevel", dp: NewOperatingVoltageLevelSensor(), want: ":CALCULATED/OPERATING_VOLTAGE_LEVEL"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := tc.dp.UniqueID(); !strings.HasSuffix(got, tc.want) {
				t.Fatalf("UniqueID() = %q, want suffix %q", got, tc.want)
			}
		})
	}
}

// TestCalculatedSetForcedUsageNoCreate verifies that the promoted
// [datapoint.BaseDataPointFields.SetForcedUsage] flips Visible() to
// false on a calculated DP. After PR-32 the calculated sensor only
// has the inner [generic.DataPoint]'s BaseDataPointFields (the outer
// embed was removed to fix V2 — dual-embed where MarkForcedSensor on
// the outer had no effect on the inner Usage / Category). Both
// Visible and the SetForcedUsage state therefore route through the
// same single source of truth.
func TestCalculatedSetForcedUsageNoCreate(t *testing.T) {
	t.Parallel()
	s := NewDewPointSensorWithIdentity("ccu", "VCU:1")
	if !s.Visible() {
		t.Fatal("Visible() must default to true")
	}
	s.SetForcedUsage(hmenum.DataPointUsageNoCreate)
	if s.Visible() {
		t.Fatal("after SetForcedUsage(NoCreate), Visible() must be false")
	}
	if s.EnabledByDefault() {
		t.Fatal("after SetForcedUsage(NoCreate), EnabledByDefault() must be false")
	}
}

// TestCalculatedConcurrent race-tests the migrated calculated DP at
// the embedded [datapoint.BaseDataPointFields] surface: the
// foundation's lock is independent of the inner [generic.Sensor]'s
// `DataPoint.mu`, so concurrent forced-usage churn, publisher updates,
// and identity reads must not race or deadlock.
//
// The existing per-formula recompute path (`OnTemperature` /
// `OnHumidity` on `s.in`, `s.last`) is intentionally NOT driven
// concurrently here — those state slots predate this migration and
// Have never been concurrent-safe on the producer side
// also relies on a single-writer event loop for the inputs. The race
// detector therefore only exercises the surface introduced in Phase
// 5C-3.
func TestCalculatedConcurrent(t *testing.T) {
	t.Parallel()
	s := NewDewPointSensorWithIdentity("ccu-prod", "VCU0123:1")
	pub := &calcCountingPublisher{}
	s.SetPublisher(pub)

	const (
		writers    = 4
		readers    = 4
		iterations = 200
	)

	usages := []hmenum.DataPointUsage{
		hmenum.DataPointUsageCDPPrimary,
		hmenum.DataPointUsageDataPoint,
		hmenum.DataPointUsageNoCreate,
	}

	var wg sync.WaitGroup

	for w := 0; w < writers; w++ {
		w := w
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx := context.Background()
			for i := 0; i < iterations; i++ {
				s.SetForcedUsage(usages[(w+i)%len(usages)])
				s.PublishUpdate(ctx, i)
			}
		}()
	}

	for r := 0; r < readers; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				_ = s.UniqueID()
				_ = s.Visible()
				_ = s.EnabledByDefault()
				_, _ = s.ForcedUsage()
				_, _ = s.Value()
			}
		}()
	}

	wg.Wait()

	if got, want := pub.count(), int64(writers*iterations); got != want {
		t.Fatalf("publisher saw %d calls, want %d", got, want)
	}
}

// TestCalculatedMarkForcedSensorReachesUsage pins V2 (PR-32):
// [datapoint.BaseDataPointFields.MarkForcedSensor] called on a
// calculated sensor must propagate to the inner generic
// [DataPoint.Usage] / [DataPoint.Category] surfaces. Before PR-32
// the calculated sensor type carried two BaseDataPointFields embeds
// (an outer one for the CALCULATED/<param> UniqueID + the inner one
// inside [generic.Sensor]); MarkForcedSensor on the outer never
// reached the inner Usage() and Category() readers, so the flag was
// silently inert.
//
// The fix removes the outer embed and threads the CALCULATED/<param>
// keyName through [generic.Spec.KeyNameOverride] instead — there
// is now a single BaseDataPointFields, and MarkForcedSensor flows
// through to Usage / Category as expected.
func TestCalculatedMarkForcedSensorReachesUsage(t *testing.T) {
	t.Parallel()
	s := NewDewPointSensorWithIdentity("ccu", "VCU:1")
	if got := s.Usage(); got == hmenum.DataPointUsageDataPoint && s.IsForcedSensor() {
		t.Fatal("precondition violated: sensor reports forced before MarkForcedSensor")
	}
	s.MarkForcedSensor()
	if !s.IsForcedSensor() {
		t.Fatal("MarkForcedSensor must flip IsForcedSensor() to true")
	}
	if got, want := s.Usage(), hmenum.DataPointUsageDataPoint; got != want {
		t.Fatalf("after MarkForcedSensor, Usage()=%q want %q (DataPoint head from generic)", got, want)
	}
	if got, want := s.Category(), hmenum.DataPointCategorySensor; got != want {
		t.Fatalf("after MarkForcedSensor, Category()=%q want %q (Sensor override from generic)", got, want)
	}
}

// TestCalculatedPublishUpdateUsesCalculatedKey verifies that
// PublishUpdate (promoted from the embedded BaseDataPointFields)
// forwards the *calculated* UniqueID rather than the bare wire
// parameter name. North-bound subscribers must dispatch by the
// calculated identity.
func TestCalculatedPublishUpdateUsesCalculatedKey(t *testing.T) {
	t.Parallel()
	s := NewDewPointSensorWithIdentity("ccu-prod", "VCU0123:1")
	pub := &calcCapturingPublisher{}
	s.SetPublisher(pub)
	s.PublishUpdate(context.Background(), 12.5)

	calls := pub.snapshot()
	if len(calls) != 1 {
		t.Fatalf("expected 1 publisher call, got %d", len(calls))
	}
	if got, want := calls[0].key, "ccu-prod:VCU0123:1:CALCULATED/DEW_POINT"; got != want {
		t.Fatalf("publisher key=%q want %q", got, want)
	}
}

// calcCapturingPublisher / calcCountingPublisher mirror the lightweight
// publishers used in sibling tests. The "calc" prefix avoids colliding
// with other packages' helpers should they ever land in the same test
// binary.
type calcCapturingPublisher struct {
	mu    sync.Mutex
	calls []calcCapturedCall
}

type calcCapturedCall struct {
	key   string
	value any
}

func (c *calcCapturingPublisher) PublishUpdate(_ context.Context, key string, value any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls = append(c.calls, calcCapturedCall{key: key, value: value})
}

func (c *calcCapturingPublisher) snapshot() []calcCapturedCall {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]calcCapturedCall, len(c.calls))
	copy(out, c.calls)
	return out
}

type calcCountingPublisher struct {
	n atomic.Int64
}

func (c *calcCountingPublisher) PublishUpdate(_ context.Context, _ string, _ any) {
	c.n.Add(1)
}

func (c *calcCountingPublisher) count() int64 { return c.n.Load() }
