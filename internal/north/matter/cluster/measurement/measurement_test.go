// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package measurement_test

import (
	"context"
	"slices"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/north/matter/cluster/measurement"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/interfaces"
)

// --- fakes ---

type fakeFloat struct {
	class interfaces.MatterMeasurementClass
	val   float64
	obs   bool
}

func (f fakeFloat) MatterMeasurementClass() interfaces.MatterMeasurementClass { return f.class }
func (f fakeFloat) MatterFloatValue() (float64, bool)                         { return f.val, f.obs }

type fakeBool struct {
	class interfaces.MatterMeasurementClass
	val   bool
	obs   bool
}

func (f fakeBool) MatterMeasurementClass() interfaces.MatterMeasurementClass { return f.class }
func (f fakeBool) MatterBoolValue() (value, observed bool)                   { return f.val, f.obs }

// fakeFloatNotifier implements both MatterFloatMeasurementSource and
// MatterChangeNotifier so tests can verify that a cluster server forwards
// OnMatterValueChanged to its wrapped source. Unlike fakeFloat (which
// deliberately does NOT implement MatterChangeNotifier, covering the
// no-notifier fallback path), this fake records the subscribed callback and
// counts unsubscribe calls.
type fakeFloatNotifier struct {
	class      interfaces.MatterMeasurementClass
	val        float64
	obs        bool
	cb         func()
	unsubCalls int
}

func (f *fakeFloatNotifier) MatterMeasurementClass() interfaces.MatterMeasurementClass {
	return f.class
}
func (f *fakeFloatNotifier) MatterFloatValue() (float64, bool) { return f.val, f.obs }

func (f *fakeFloatNotifier) OnMatterValueChanged(cb func()) func() {
	f.cb = cb
	return func() { f.unsubCalls++ }
}

// Compile-time assertions: both Electrical* servers satisfy MatterChangeNotifier.
var (
	_ interfaces.MatterChangeNotifier = (*measurement.ElectricalPowerServer)(nil)
	_ interfaces.MatterChangeNotifier = (*measurement.ElectricalEnergyServer)(nil)
)

// attrClusterRevision is the global cluster-revision attribute ID.
const attrClusterRevision uint32 = 0xFFFD

// --- TemperatureServer ---

// TestTemperatureServerHappyPath verifies ClusterID, MeasuredValue, and ClusterRevision for a normal reading.
func TestTemperatureServerHappyPath(t *testing.T) {
	t.Parallel()
	src := fakeFloat{class: interfaces.MatterMeasurementTemperature, val: 21.5, obs: true}
	s := measurement.NewTemperatureServer(src)

	if got := s.MatterClusterID(); got != measurement.ClusterTemperatureMeasurement {
		t.Errorf("ClusterID = 0x%04X, want 0x%04X", got, measurement.ClusterTemperatureMeasurement)
	}

	v, ok := s.MatterRead(0x0000)
	if !ok {
		t.Fatal("MatterRead(0x0000) ok = false, want true")
	}
	if got, want := v.(int16), int16(2150); got != want {
		t.Errorf("MeasuredValue = %d, want %d", got, want)
	}

	rev, ok := s.MatterRead(attrClusterRevision)
	if !ok {
		t.Fatal("MatterRead(ClusterRevision) ok = false")
	}
	if got, want := rev.(uint16), uint16(6); got != want {
		t.Errorf("ClusterRevision = %d, want %d", got, want)
	}
}

// TestTemperatureServerSaturatesHigh verifies that a very high temperature clamps at 32766
// (the spec ceiling per chip kMaxMeasuredValueRange). 32767 is the NULL sentinel and must
// not be emitted as a real value.
func TestTemperatureServerSaturatesHigh(t *testing.T) {
	t.Parallel()
	s := measurement.NewTemperatureServer(fakeFloat{val: 1000.0, obs: true})
	v, ok := s.MatterRead(0x0000)
	if !ok {
		t.Fatal("ok = false")
	}
	const wantMax int16 = 32766 // spec ceiling; 32767 is TLV-null sentinel
	if got := v.(int16); got != wantMax {
		t.Errorf("want %d (spec ceiling), got %d (32767 is the NULL sentinel)", wantMax, got)
	}
}

// TestTemperatureServerSaturatesLow verifies that a very low temperature clamps at -27315
// (−273.15 °C, physical absolute zero per chip kMinMeasuredValueRange).
func TestTemperatureServerSaturatesLow(t *testing.T) {
	t.Parallel()
	s := measurement.NewTemperatureServer(fakeFloat{val: -1000.0, obs: true})
	v, ok := s.MatterRead(0x0000)
	if !ok {
		t.Fatal("ok = false")
	}
	const wantMin int16 = -27315 // −273.15 °C absolute zero
	if got := v.(int16); got != wantMin {
		t.Errorf("want %d (absolute zero), got %d", wantMin, got)
	}
}

// TestTemperatureServerUnobserved verifies that an unobserved source returns (nil, true) —
// attribute is supported but value is transiently null (Apple Home tolerates null and
// continues building the HAP service; (nil, false) would signal UnsupportedAttribute).
func TestTemperatureServerUnobserved(t *testing.T) {
	t.Parallel()
	s := measurement.NewTemperatureServer(fakeFloat{val: 21.5, obs: false})
	v, ok := s.MatterRead(0x0000)
	if !ok || v != nil {
		t.Errorf("want (nil, true), got (%v, %v)", v, ok)
	}
}

// TestTemperatureServerUnknownAttrReturnsFalse verifies that an unknown attribute returns (nil, false).
func TestTemperatureServerUnknownAttrReturnsFalse(t *testing.T) {
	t.Parallel()
	s := measurement.NewTemperatureServer(fakeFloat{val: 21.5, obs: true})
	v, ok := s.MatterRead(0x9999)
	if ok || v != nil {
		t.Errorf("want (nil, false) for unknown attr, got (%v, %v)", v, ok)
	}
}

// TestTemperatureServerWriteIsReadOnly verifies that MatterWrite returns a non-nil error.
func TestTemperatureServerWriteIsReadOnly(t *testing.T) {
	t.Parallel()
	s := measurement.NewTemperatureServer(fakeFloat{val: 21.5, obs: true})
	err := s.MatterWrite(context.Background(), 0x0000, int16(100), hmenum.CommandPriorityHigh)
	if err == nil {
		t.Error("MatterWrite returned nil, want read-only error")
	}
}

// TestTemperatureServerInvokeRejected verifies that MatterInvoke returns a non-nil error.
func TestTemperatureServerInvokeRejected(t *testing.T) {
	t.Parallel()
	s := measurement.NewTemperatureServer(fakeFloat{val: 21.5, obs: true})
	_, err := s.MatterInvoke(context.Background(), 0x00, nil, hmenum.CommandPriorityHigh)
	if err == nil {
		t.Error("MatterInvoke returned nil error, want rejection")
	}
}

// TestTemperatureServerReportable verifies that MatterReportable contains attr 0x0000.
func TestTemperatureServerReportable(t *testing.T) {
	t.Parallel()
	s := measurement.NewTemperatureServer(fakeFloat{val: 21.5, obs: true})
	attrs := s.MatterReportable()
	if slices.Contains(attrs, 0x0000) {
		return
	}
	t.Errorf("MatterReportable = %v, want to contain 0x0000", attrs)
}

// --- TemperatureServer DataVersion ---

// TestTemperatureServerDataVersion_NonZeroInitial verifies that a freshly-
// constructed TemperatureServer carries a non-zero DataVersion. Apple Home
// treats a constant DataVersion=1 across every cluster as an "uninitialised"
// signal and refuses to cache those clusters to disk.
func TestTemperatureServerDataVersion_NonZeroInitial(t *testing.T) {
	t.Parallel()
	s := measurement.NewTemperatureServer(fakeFloat{val: 21.0, obs: true})
	if v := s.MatterDataVersion(); v == 0 {
		t.Errorf("TemperatureServer.MatterDataVersion() = 0, want non-zero (Matter §10.6.5)")
	}
}

// TestTemperatureServerDataVersion_DistinctAcrossInstances verifies that two
// freshly-constructed TemperatureServers carry different initial DataVersions
// — matching the per-cluster random-init that matter.js applies so Apple
// Home's MTRDevice cache stores each cluster separately.
func TestTemperatureServerDataVersion_DistinctAcrossInstances(t *testing.T) {
	t.Parallel()
	seen := make(map[uint32]struct{}, 50)
	for i := range 50 {
		s := measurement.NewTemperatureServer(fakeFloat{val: float64(i), obs: true})
		seen[s.MatterDataVersion()] = struct{}{}
	}
	if len(seen) < 45 {
		t.Errorf("only %d distinct DataVersions in 50 fresh TemperatureServers — random init regressed?", len(seen))
	}
}

// TestTemperatureServerDataVersion_ImplementsMatterClusterDataVersion verifies
// at compile time that TemperatureServer satisfies the
// interfaces.MatterClusterDataVersion capability the IM dispatcher uses.
func TestTemperatureServerDataVersion_ImplementsMatterClusterDataVersion(t *testing.T) {
	t.Parallel()
	s := measurement.NewTemperatureServer(fakeFloat{val: 0, obs: true})
	// The following line fails to compile if TemperatureServer does not implement
	// the interface — the test verifies the structural contract.
	var _ interface{ MatterDataVersion() uint32 } = s
}

// --- HumidityServer ---

// TestHumidityServerHappyPath verifies ClusterID, MeasuredValue, and ClusterRevision for a normal reading.
func TestHumidityServerHappyPath(t *testing.T) {
	t.Parallel()
	s := measurement.NewHumidityServer(fakeFloat{val: 42.5, obs: true})

	if got := s.MatterClusterID(); got != measurement.ClusterHumidityMeasurement {
		t.Errorf("ClusterID = 0x%04X, want 0x%04X", got, measurement.ClusterHumidityMeasurement)
	}

	v, ok := s.MatterRead(0x0000)
	if !ok {
		t.Fatal("MatterRead(0x0000) ok = false")
	}
	if got, want := v.(uint16), uint16(4250); got != want {
		t.Errorf("MeasuredValue = %d, want %d", got, want)
	}

	rev, ok := s.MatterRead(attrClusterRevision)
	if !ok {
		t.Fatal("MatterRead(ClusterRevision) ok = false")
	}
	if got, want := rev.(uint16), uint16(5); got != want {
		t.Errorf("ClusterRevision = %d, want %d", got, want)
	}
}

// TestHumidityServerClampsHigh verifies that humidity > 100% clamps to 10000 wire units.
func TestHumidityServerClampsHigh(t *testing.T) {
	t.Parallel()
	s := measurement.NewHumidityServer(fakeFloat{val: 150.0, obs: true})
	v, ok := s.MatterRead(0x0000)
	if !ok {
		t.Fatal("ok = false")
	}
	if got, want := v.(uint16), uint16(10000); got != want {
		t.Errorf("want %d, got %d", want, got)
	}
}

// TestHumidityServerClampsLow verifies that negative humidity clamps to 0.
func TestHumidityServerClampsLow(t *testing.T) {
	t.Parallel()
	s := measurement.NewHumidityServer(fakeFloat{val: -5.0, obs: true})
	v, ok := s.MatterRead(0x0000)
	if !ok {
		t.Fatal("ok = false")
	}
	if got, want := v.(uint16), uint16(0); got != want {
		t.Errorf("want %d, got %d", want, got)
	}
}

// --- IlluminanceServer ---

// TestIlluminanceServerHappyPath verifies that 100 lux maps to wire value 20001.
func TestIlluminanceServerHappyPath(t *testing.T) {
	t.Parallel()
	s := measurement.NewIlluminanceServer(fakeFloat{val: 100.0, obs: true})

	if got := s.MatterClusterID(); got != measurement.ClusterIlluminanceMeasurement {
		t.Errorf("ClusterID = 0x%04X, want 0x%04X", got, measurement.ClusterIlluminanceMeasurement)
	}

	v, ok := s.MatterRead(0x0000)
	if !ok {
		t.Fatal("MatterRead(0x0000) ok = false")
	}
	if got, want := v.(uint16), uint16(20001); got != want {
		t.Errorf("MeasuredValue = %d, want %d", got, want)
	}

	rev, ok := s.MatterRead(attrClusterRevision)
	if !ok {
		t.Fatal("MatterRead(ClusterRevision) ok = false")
	}
	if got, want := rev.(uint16), uint16(5); got != want {
		t.Errorf("ClusterRevision = %d, want %d", got, want)
	}
}

// TestIlluminanceServerSubLuxClampsToOne verifies that sub-lux readings (< 1 lux) clamp to wire value 1.
func TestIlluminanceServerSubLuxClampsToOne(t *testing.T) {
	t.Parallel()
	s := measurement.NewIlluminanceServer(fakeFloat{val: 0.5, obs: true})
	v, ok := s.MatterRead(0x0000)
	if !ok {
		t.Fatal("ok = false")
	}
	if got, want := v.(uint16), uint16(1); got != want {
		t.Errorf("want %d, got %d", want, got)
	}
}

// TestIlluminanceServerOneLuxIsOne verifies that exactly 1 lux maps to wire value 1.
func TestIlluminanceServerOneLuxIsOne(t *testing.T) {
	t.Parallel()
	s := measurement.NewIlluminanceServer(fakeFloat{val: 1.0, obs: true})
	v, ok := s.MatterRead(0x0000)
	if !ok {
		t.Fatal("ok = false")
	}
	if got, want := v.(uint16), uint16(1); got != want {
		t.Errorf("want %d, got %d", want, got)
	}
}

// TestIlluminanceServerSaturatesHigh verifies that extremely high lux values saturate at 0xFFFE.
func TestIlluminanceServerSaturatesHigh(t *testing.T) {
	t.Parallel()
	s := measurement.NewIlluminanceServer(fakeFloat{val: 1e10, obs: true})
	v, ok := s.MatterRead(0x0000)
	if !ok {
		t.Fatal("ok = false")
	}
	if got, want := v.(uint16), uint16(0xFFFE); got != want {
		t.Errorf("want 0x%04X, got 0x%04X", want, got)
	}
}

// --- PressureServer ---

// TestPressureServerHappyPath verifies that a typical atmospheric
// reading of 1013 hPa maps to wire value 1013 — MeasuredValue is
// "10 x Pressure [kPa]" per matter.js
// `packages/model/src/standard/resources/pressure-measurement.resource.ts:27`,
// so one wire unit is 0.1 kPa = 100 Pa = exactly 1 hPa.
func TestPressureServerHappyPath(t *testing.T) {
	t.Parallel()
	s := measurement.NewPressureServer(fakeFloat{val: 1013.0, obs: true})

	if got := s.MatterClusterID(); got != measurement.ClusterPressureMeasurement {
		t.Errorf("ClusterID = 0x%04X, want 0x%04X", got, measurement.ClusterPressureMeasurement)
	}

	v, ok := s.MatterRead(0x0000)
	if !ok {
		t.Fatal("MatterRead(0x0000) ok = false")
	}
	if got, want := v.(int16), int16(1013); got != want {
		t.Errorf("MeasuredValue = %d, want %d", got, want)
	}

	rev, ok := s.MatterRead(attrClusterRevision)
	if !ok {
		t.Fatal("MatterRead(ClusterRevision) ok = false")
	}
	if got, want := rev.(uint16), uint16(5); got != want {
		t.Errorf("ClusterRevision = %d, want %d", got, want)
	}
}

// TestPressureServerMeasuredValueIsDeciKiloPascal pins the wire unit
// across the plausible barometric span. A wire unit is one hPa, so the
// reading passes through rounded — a scaling factor here would push
// every reading out of the atmospheric range a controller renders.
func TestPressureServerMeasuredValueIsDeciKiloPascal(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		hpa  float64
		want int16
	}{
		{name: "standard atmosphere", hpa: 1013.25, want: 1013},
		{name: "deep low", hpa: 950.4, want: 950},
		{name: "high pressure", hpa: 1050.6, want: 1051},
		{name: "sub-unit reading rounds", hpa: 1.5, want: 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s := measurement.NewPressureServer(fakeFloat{val: tc.hpa, obs: true})
			v, ok := s.MatterRead(0x0000)
			if !ok {
				t.Fatal("MatterRead(MeasuredValue) ok = false")
			}
			if got := v.(int16); got != tc.want {
				t.Errorf("MeasuredValue for %.2f hPa = %d, want %d", tc.hpa, got, tc.want)
			}
		})
	}
}

// TestPressureServerMinMeasuredValueIsZero verifies the PressureServer returns 0 for
// MinMeasuredValue — the physical domain lower bound for atmospheric pressure.
// int16(-32768) is below the physical domain and misleads strict controllers.
// matter.js pressure-measurement.element.ts:29 + chip PressureMeasurementCluster.cpp:26.
func TestPressureServerMinMeasuredValueIsZero(t *testing.T) {
	t.Parallel()
	s := measurement.NewPressureServer(fakeFloat{val: 1013.0, obs: true})
	v, ok := s.MatterRead(0x0001)
	if !ok {
		t.Fatal("MatterRead(MinMeasuredValue) ok = false")
	}
	if got := v.(int16); got != 0 {
		t.Errorf("MinMeasuredValue = %d, want 0", got)
	}
}

// TestPressureServerMaxMeasuredValueIs32766 verifies the PressureServer returns 32766 for
// MaxMeasuredValue. 32767 is the NULL sentinel (must not be a real value).
func TestPressureServerMaxMeasuredValueIs32766(t *testing.T) {
	t.Parallel()
	s := measurement.NewPressureServer(fakeFloat{val: 1013.0, obs: true})
	v, ok := s.MatterRead(0x0002)
	if !ok {
		t.Fatal("MatterRead(MaxMeasuredValue) ok = false")
	}
	const wantMax int16 = 32766
	if got := v.(int16); got != wantMax {
		t.Errorf("MaxMeasuredValue = %d, want %d (32767 is the NULL sentinel)", got, wantMax)
	}
}

// --- BooleanStateServer ---

// TestBooleanStateServerTrue verifies that StateValue=true is passed through and cluster attrs are correct.
func TestBooleanStateServerTrue(t *testing.T) {
	t.Parallel()
	s := measurement.NewBooleanStateServer(fakeBool{val: true, obs: true})

	if got := s.MatterClusterID(); got != measurement.ClusterBooleanState {
		t.Errorf("ClusterID = 0x%04X, want 0x%04X", got, measurement.ClusterBooleanState)
	}

	v, ok := s.MatterRead(0x0000)
	if !ok {
		t.Fatal("MatterRead(0x0000) ok = false")
	}
	if got := v.(bool); !got {
		t.Error("StateValue = false, want true")
	}

	rev, ok := s.MatterRead(attrClusterRevision)
	if !ok {
		t.Fatal("MatterRead(ClusterRevision) ok = false")
	}
	if got, want := rev.(uint16), uint16(3); got != want {
		t.Errorf("ClusterRevision = %d, want %d", got, want)
	}
}

// TestBooleanStateServerFalse verifies that StateValue=false is passed through correctly.
func TestBooleanStateServerFalse(t *testing.T) {
	t.Parallel()
	s := measurement.NewBooleanStateServer(fakeBool{val: false, obs: true})
	v, ok := s.MatterRead(0x0000)
	if !ok {
		t.Fatal("ok = false")
	}
	if got := v.(bool); got {
		t.Error("StateValue = true, want false")
	}
}

// TestBooleanStateServerUnobserved verifies that an unobserved source returns (false, true).
// BooleanState.StateValue has no quality X (not nullable) — TLV-null for a non-nullable bool
// causes chip CHIP Error 0x26. Defaults to false (safe-state) before the first CCU push.
// matter.js packages/model/src/standard/elements/boolean-state.element.ts:29.
func TestBooleanStateServerUnobserved(t *testing.T) {
	t.Parallel()
	s := measurement.NewBooleanStateServer(fakeBool{val: true, obs: false})
	v, ok := s.MatterRead(0x0000)
	if !ok || v != false {
		t.Errorf("want (false, true) — non-nullable default, got (%v, %v)", v, ok)
	}
}

// --- OccupancySensingServer ---

// TestOccupancyServerOccupied verifies that occupied=true maps to uint8(1) and cluster attrs are correct.
func TestOccupancyServerOccupied(t *testing.T) {
	t.Parallel()
	s := measurement.NewOccupancySensingServer(fakeBool{val: true, obs: true})

	if got := s.MatterClusterID(); got != measurement.ClusterOccupancySensing {
		t.Errorf("ClusterID = 0x%04X, want 0x%04X", got, measurement.ClusterOccupancySensing)
	}

	v, ok := s.MatterRead(0x0000)
	if !ok {
		t.Fatal("MatterRead(0x0000) ok = false")
	}
	if got, want := v.(uint8), uint8(1); got != want {
		t.Errorf("Occupancy = %d, want %d", got, want)
	}

	rev, ok := s.MatterRead(attrClusterRevision)
	if !ok {
		t.Fatal("MatterRead(ClusterRevision) ok = false")
	}
	if got, want := rev.(uint16), uint16(7); got != want {
		t.Errorf("ClusterRevision = %d, want %d", got, want)
	}
}

// TestOccupancyServerUnoccupied verifies that occupied=false maps to uint8(0).
func TestOccupancyServerUnoccupied(t *testing.T) {
	t.Parallel()
	s := measurement.NewOccupancySensingServer(fakeBool{val: false, obs: true})
	v, ok := s.MatterRead(0x0000)
	if !ok {
		t.Fatal("ok = false")
	}
	if got, want := v.(uint8), uint8(0); got != want {
		t.Errorf("Occupancy = %d, want %d", got, want)
	}
}

// TestOccupancyServerSensorType verifies that OccupancySensorType (0x0001) reports PIR (uint8(0)).
func TestOccupancyServerSensorType(t *testing.T) {
	t.Parallel()
	s := measurement.NewOccupancySensingServer(fakeBool{val: true, obs: true})
	v, ok := s.MatterRead(0x0001)
	if !ok {
		t.Fatal("MatterRead(0x0001) ok = false")
	}
	if got, want := v.(uint8), uint8(0); got != want {
		t.Errorf("OccupancySensorType = %d, want %d (PIR)", got, want)
	}
}

// TestOccupancyServerSensorBitmap verifies that OccupancySensorTypeBitmap (0x0002) has PIR bit set.
func TestOccupancyServerSensorBitmap(t *testing.T) {
	t.Parallel()
	s := measurement.NewOccupancySensingServer(fakeBool{val: true, obs: true})
	v, ok := s.MatterRead(0x0002)
	if !ok {
		t.Fatal("MatterRead(0x0002) ok = false")
	}
	if got, want := v.(uint8), uint8(1); got != want {
		t.Errorf("OccupancySensorTypeBitmap = %d, want %d (PIR bit)", got, want)
	}
}

// TestOccupancyServerPIRDelayNotAdvertised verifies the bridge does NOT
// advertise PirOccupiedToUnoccupiedDelay (0x0010): per matter.js
// occupancy-sensing.element.ts it is deprecated (D) and conformance-gated
// on the optional HoldTime (0x3) attribute, which the bridge does not serve.
func TestOccupancyServerPIRDelayNotAdvertised(t *testing.T) {
	t.Parallel()
	s := measurement.NewOccupancySensingServer(fakeBool{val: true, obs: true})
	if _, ok := s.MatterRead(0x0010); ok {
		t.Error("0x0010 must not be served — it is deprecated + HoldTime-gated, and HoldTime is not implemented")
	}
	for _, a := range s.MatterAttributes() {
		if a == 0x0010 {
			t.Error("MatterAttributes() must not include the deprecated 0x0010")
		}
	}
}

// --- FromMeasurementClass materializer ---

// TestFromMeasurementClassTemperature verifies that Temperature class returns a TemperatureMeasurement cluster.
func TestFromMeasurementClassTemperature(t *testing.T) {
	t.Parallel()
	src := fakeFloat{class: interfaces.MatterMeasurementTemperature, val: 20.0, obs: true}
	servers := measurement.FromMeasurementClass(interfaces.MatterMeasurementTemperature, src)
	if len(servers) != 1 {
		t.Fatalf("want 1 server, got %d", len(servers))
	}
	if got := servers[0].MatterClusterID(); got != measurement.ClusterTemperatureMeasurement {
		t.Errorf("ClusterID = 0x%04X, want 0x%04X", got, measurement.ClusterTemperatureMeasurement)
	}
}

// TestFromMeasurementClassHumidity verifies that Humidity class returns a HumidityMeasurement cluster.
func TestFromMeasurementClassHumidity(t *testing.T) {
	t.Parallel()
	src := fakeFloat{class: interfaces.MatterMeasurementHumidity, val: 50.0, obs: true}
	servers := measurement.FromMeasurementClass(interfaces.MatterMeasurementHumidity, src)
	if len(servers) != 1 {
		t.Fatalf("want 1 server, got %d", len(servers))
	}
	if got := servers[0].MatterClusterID(); got != measurement.ClusterHumidityMeasurement {
		t.Errorf("ClusterID = 0x%04X, want 0x%04X", got, measurement.ClusterHumidityMeasurement)
	}
}

// TestFromMeasurementClassIlluminance verifies that Illuminance class returns an IlluminanceMeasurement cluster.
func TestFromMeasurementClassIlluminance(t *testing.T) {
	t.Parallel()
	src := fakeFloat{class: interfaces.MatterMeasurementIlluminance, val: 500.0, obs: true}
	servers := measurement.FromMeasurementClass(interfaces.MatterMeasurementIlluminance, src)
	if len(servers) != 1 {
		t.Fatalf("want 1 server, got %d", len(servers))
	}
	if got := servers[0].MatterClusterID(); got != measurement.ClusterIlluminanceMeasurement {
		t.Errorf("ClusterID = 0x%04X, want 0x%04X", got, measurement.ClusterIlluminanceMeasurement)
	}
}

// TestFromMeasurementClassPressure verifies that Pressure class returns a PressureMeasurement cluster.
func TestFromMeasurementClassPressure(t *testing.T) {
	t.Parallel()
	src := fakeFloat{class: interfaces.MatterMeasurementPressure, val: 1013.0, obs: true}
	servers := measurement.FromMeasurementClass(interfaces.MatterMeasurementPressure, src)
	if len(servers) != 1 {
		t.Fatalf("want 1 server, got %d", len(servers))
	}
	if got := servers[0].MatterClusterID(); got != measurement.ClusterPressureMeasurement {
		t.Errorf("ClusterID = 0x%04X, want 0x%04X", got, measurement.ClusterPressureMeasurement)
	}
}

// TestFromMeasurementClassContact verifies that Contact class returns a BooleanState cluster.
func TestFromMeasurementClassContact(t *testing.T) {
	t.Parallel()
	src := fakeBool{class: interfaces.MatterMeasurementContact, val: true, obs: true}
	servers := measurement.FromMeasurementClass(interfaces.MatterMeasurementContact, src)
	if len(servers) != 1 {
		t.Fatalf("want 1 server, got %d", len(servers))
	}
	if got := servers[0].MatterClusterID(); got != measurement.ClusterBooleanState {
		t.Errorf("ClusterID = 0x%04X, want 0x%04X", got, measurement.ClusterBooleanState)
	}
}

// TestFromMeasurementClassLeak verifies that Leak class also returns a BooleanState cluster.
func TestFromMeasurementClassLeak(t *testing.T) {
	t.Parallel()
	src := fakeBool{class: interfaces.MatterMeasurementLeak, val: false, obs: true}
	servers := measurement.FromMeasurementClass(interfaces.MatterMeasurementLeak, src)
	if len(servers) != 1 {
		t.Fatalf("want 1 server, got %d", len(servers))
	}
	if got := servers[0].MatterClusterID(); got != measurement.ClusterBooleanState {
		t.Errorf("ClusterID = 0x%04X, want 0x%04X", got, measurement.ClusterBooleanState)
	}
}

// TestFromMeasurementClassOccupancy verifies that Occupancy class returns an OccupancySensing cluster.
func TestFromMeasurementClassOccupancy(t *testing.T) {
	t.Parallel()
	src := fakeBool{class: interfaces.MatterMeasurementOccupancy, val: true, obs: true}
	servers := measurement.FromMeasurementClass(interfaces.MatterMeasurementOccupancy, src)
	if len(servers) != 1 {
		t.Fatalf("want 1 server, got %d", len(servers))
	}
	if got := servers[0].MatterClusterID(); got != measurement.ClusterOccupancySensing {
		t.Errorf("ClusterID = 0x%04X, want 0x%04X", got, measurement.ClusterOccupancySensing)
	}
}

// TestFromMeasurementClassBattery_FloatSource verifies that a Battery
// class source implementing MatterFloatMeasurementSource (e.g. a
// derived battery-percentage sensor such as
// calculated.OperatingVoltageLevelSensor) materialises a
// PowerSourceServer that reports BatPercentRemaining — mirroring the
// existing bool-source case (TestFromMeasurementClass_Battery), which
// materialises a PowerSourceServer reporting BatChargeLevel instead.
// Before this fix the switch only checked MatterBoolMeasurementSource,
// so a float-only source silently produced zero cluster servers.
func TestFromMeasurementClassBattery_FloatSource(t *testing.T) {
	t.Parallel()
	src := fakeFloat{class: interfaces.MatterMeasurementBattery, val: 80.0, obs: true}
	servers := measurement.FromMeasurementClass(interfaces.MatterMeasurementBattery, src)
	if len(servers) != 1 {
		t.Fatalf("want 1 server for Battery class with a float source, got %d", len(servers))
	}
	if got := servers[0].MatterClusterID(); got != measurement.ClusterPowerSource {
		t.Errorf("ClusterID = 0x%04X, want 0x%04X (PowerSource)", got, measurement.ClusterPowerSource)
	}
	v, ok := servers[0].MatterRead(0x000C) // BatPercentRemaining
	if !ok {
		t.Fatal("MatterRead(BatPercentRemaining) ok = false")
	}
	if got, want := v.(uint8), uint8(160); got != want { // 80% -> 160 half-percent
		t.Errorf("BatPercentRemaining = %d, want %d", got, want)
	}
}

// TestFromMeasurementClassNoneReturnsNil verifies that None class returns nil.
func TestFromMeasurementClassNoneReturnsNil(t *testing.T) {
	t.Parallel()
	src := fakeFloat{class: interfaces.MatterMeasurementNone, val: 0.0, obs: true}
	servers := measurement.FromMeasurementClass(interfaces.MatterMeasurementNone, src)
	if servers != nil {
		t.Errorf("want nil for None class, got %v", servers)
	}
}

// TestFromMeasurementClassWrongTypedSourceReturnsNil verifies that a mismatched source type returns nil.
func TestFromMeasurementClassWrongTypedSourceReturnsNil(t *testing.T) {
	t.Parallel()

	// fakeFloat passed for Contact class (which expects MatterBoolMeasurementSource) → nil.
	floatSrc := fakeFloat{class: interfaces.MatterMeasurementContact, val: 1.0, obs: true}
	if got := measurement.FromMeasurementClass(interfaces.MatterMeasurementContact, floatSrc); got != nil {
		t.Errorf("float src for Contact: want nil, got %v", got)
	}

	// fakeBool passed for Temperature class (which expects MatterFloatMeasurementSource) → nil.
	boolSrc := fakeBool{class: interfaces.MatterMeasurementTemperature, val: true, obs: true}
	if got := measurement.FromMeasurementClass(interfaces.MatterMeasurementTemperature, boolSrc); got != nil {
		t.Errorf("bool src for Temperature: want nil, got %v", got)
	}
}

// TestFromMeasurementClassAirQualityMountsMandatoryCluster verifies that
// every class the bridge projects onto the AirQualitySensor device type
// (0x002C) materialises the AirQuality cluster next to its concentration
// cluster. matter.js `packages/node/src/devices/air-quality-sensor.ts:169`
// declares `mandatory: { Identify, AirQuality }` and lists every
// concentration cluster as optional, so an endpoint carrying only the
// concentration cluster fails the device type's requirement set.
func TestFromMeasurementClassAirQualityMountsMandatoryCluster(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		class      interfaces.MatterMeasurementClass
		concentrID uint32
	}{
		{name: "co2", class: interfaces.MatterMeasurementCO2, concentrID: measurement.ClusterCO2Concentration},
		{name: "pm25", class: interfaces.MatterMeasurementPM25, concentrID: measurement.ClusterPM25Concentration},
		{name: "pm10", class: interfaces.MatterMeasurementPM10, concentrID: measurement.ClusterPM10Concentration},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			src := fakeFloat{class: tc.class, val: 400.0, obs: true}
			servers := measurement.FromMeasurementClass(tc.class, src)

			got := make(map[uint32]bool, len(servers))
			for _, s := range servers {
				got[s.MatterClusterID()] = true
			}
			if !got[measurement.ClusterAirQuality] {
				t.Errorf("AirQuality (0x%04X) not materialised; got %v", measurement.ClusterAirQuality, got)
			}
			if !got[tc.concentrID] {
				t.Errorf("concentration cluster 0x%04X not materialised; got %v", tc.concentrID, got)
			}
		})
	}
}

// --- AirQualityServer ---

// TestAirQualityServerClassifiesAgainstGuideline verifies the
// concentration -> AirQualityEnum mapping. Matter §2.9.5.1 leaves the
// mapping to the implementer; the bridge grades against the pollutant's
// published guideline value and reports only the levels the base cluster
// mandates (Unknown / Good / Poor), because the finer levels are each
// gated on a FeatureMap bit this server does not advertise.
func TestAirQualityServerClassifiesAgainstGuideline(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		class interfaces.MatterMeasurementClass
		val   float64
		obs   bool
		want  uint8
	}{
		{name: "co2 well ventilated", class: interfaces.MatterMeasurementCO2, val: 425, obs: true, want: 1},
		{name: "co2 at guideline", class: interfaces.MatterMeasurementCO2, val: 1000, obs: true, want: 1},
		{name: "co2 stale air", class: interfaces.MatterMeasurementCO2, val: 1800, obs: true, want: 4},
		{name: "pm25 clean", class: interfaces.MatterMeasurementPM25, val: 8, obs: true, want: 1},
		{name: "pm25 above guideline", class: interfaces.MatterMeasurementPM25, val: 40, obs: true, want: 4},
		{name: "pm10 clean", class: interfaces.MatterMeasurementPM10, val: 20, obs: true, want: 1},
		{name: "pm10 above guideline", class: interfaces.MatterMeasurementPM10, val: 90, obs: true, want: 4},
		{name: "no reading yet", class: interfaces.MatterMeasurementCO2, val: 0, obs: false, want: 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s := measurement.NewAirQualityServer(tc.class, fakeFloat{class: tc.class, val: tc.val, obs: tc.obs})
			v, ok := s.MatterRead(0x0000)
			if !ok {
				t.Fatal("MatterRead(AirQuality) ok = false")
			}
			if got := v.(uint8); got != tc.want {
				t.Errorf("AirQuality = %d, want %d", got, tc.want)
			}
		})
	}
}

// TestAirQualityServerGlobalAttributes pins cluster ID, revision and
// FeatureMap. The revision comes from matter.js
// `packages/model/src/standard/elements/air-quality.element.ts:19`
// (default = 1); FeatureMap stays 0 so only the conformance-mandatory
// enum members (Unknown / Good / Poor) are on the wire.
func TestAirQualityServerGlobalAttributes(t *testing.T) {
	t.Parallel()
	s := measurement.NewAirQualityServer(
		interfaces.MatterMeasurementCO2,
		fakeFloat{class: interfaces.MatterMeasurementCO2, val: 425, obs: true},
	)

	if got, want := s.MatterClusterID(), uint32(0x005B); got != want {
		t.Errorf("ClusterID = 0x%04X, want 0x%04X", got, want)
	}
	rev, ok := s.MatterRead(attrClusterRevision)
	if !ok {
		t.Fatal("MatterRead(ClusterRevision) ok = false")
	}
	if got, want := rev.(uint16), uint16(1); got != want {
		t.Errorf("ClusterRevision = %d, want %d", got, want)
	}
	fm, ok := s.MatterRead(0xFFFC)
	if !ok {
		t.Fatal("MatterRead(FeatureMap) ok = false")
	}
	if got := fm.(uint32); got != 0 {
		t.Errorf("FeatureMap = 0x%08X, want 0 (no optional level features)", got)
	}
}

// TestAirQualityServerIsReadOnly verifies the cluster rejects writes and
// has no commands — AirQuality carries a single "R V" attribute.
func TestAirQualityServerIsReadOnly(t *testing.T) {
	t.Parallel()
	s := measurement.NewAirQualityServer(
		interfaces.MatterMeasurementCO2,
		fakeFloat{class: interfaces.MatterMeasurementCO2, val: 425, obs: true},
	)
	if err := s.MatterWrite(context.Background(), 0x0000, uint8(1), hmenum.CommandPriorityCritical); err == nil {
		t.Error("MatterWrite: want error, got nil")
	}
	if _, err := s.MatterInvoke(context.Background(), 0x00, nil, hmenum.CommandPriorityCritical); err == nil {
		t.Error("MatterInvoke: want error, got nil")
	}
	if got := s.MatterReportable(); !slices.Contains(got, 0x0000) {
		t.Errorf("MatterReportable = %v, want it to contain the AirQuality attribute", got)
	}
}

// --- CO2ConcentrationServer ---

// TestCO2ConcentrationServerHappyPath verifies ClusterID, MeasuredValue, Unit, Medium, FeatureMap, and ClusterRevision for a normal reading.
func TestCO2ConcentrationServerHappyPath(t *testing.T) {
	t.Parallel()
	src := fakeFloat{class: interfaces.MatterMeasurementCO2, val: 425.0, obs: true}
	s := measurement.NewCO2ConcentrationServer(src)

	if got, want := s.MatterClusterID(), uint32(0x040D); got != want {
		t.Errorf("ClusterID = 0x%04X, want 0x%04X", got, want)
	}

	v, ok := s.MatterRead(0x0000)
	if !ok {
		t.Fatal("MatterRead(0x0000) ok = false, want true")
	}
	if got, want := v.(float32), float32(425.0); got != want {
		t.Errorf("MeasuredValue = %v, want %v", got, want)
	}

	unit, ok := s.MatterRead(0x0008)
	if !ok {
		t.Fatal("MatterRead(0x0008) ok = false")
	}
	if got, want := unit.(uint8), uint8(0); got != want {
		t.Errorf("MeasurementUnit = %d, want %d (PPM)", got, want)
	}

	medium, ok := s.MatterRead(0x0009)
	if !ok {
		t.Fatal("MatterRead(0x0009) ok = false")
	}
	if got, want := medium.(uint8), uint8(0); got != want {
		t.Errorf("MeasurementMedium = %d, want %d (Air)", got, want)
	}

	fm, ok := s.MatterRead(0xFFFC)
	if !ok {
		t.Fatal("MatterRead(0xFFFC) ok = false")
	}
	if got, want := fm.(uint32), uint32(1); got != want {
		t.Errorf("FeatureMap = %d, want %d", got, want)
	}

	rev, ok := s.MatterRead(attrClusterRevision)
	if !ok {
		t.Fatal("MatterRead(ClusterRevision) ok = false")
	}
	// matter.js HEAD `concentration-measurement.element.ts:19` declares
	// default=5 for the base ConcentrationMeasurement cluster.
	if got, want := rev.(uint16), uint16(5); got != want {
		t.Errorf("ClusterRevision = %d, want %d", got, want)
	}
}

// TestCO2ConcentrationServerUnobserved verifies that an unobserved source returns (nil, true) for
// MeasuredValue — attribute is supported but value is transiently null (see TestTemperatureServerUnobserved).
func TestCO2ConcentrationServerUnobserved(t *testing.T) {
	t.Parallel()
	s := measurement.NewCO2ConcentrationServer(fakeFloat{val: 425.0, obs: false})
	v, ok := s.MatterRead(0x0000)
	if !ok || v != nil {
		t.Errorf("want (nil, true), got (%v, %v)", v, ok)
	}
}

// TestCO2ConcentrationServerUnknownAttr verifies that an unknown attribute returns (nil, false).
func TestCO2ConcentrationServerUnknownAttr(t *testing.T) {
	t.Parallel()
	s := measurement.NewCO2ConcentrationServer(fakeFloat{val: 425.0, obs: true})
	v, ok := s.MatterRead(0x9999)
	if ok || v != nil {
		t.Errorf("want (nil, false) for unknown attr, got (%v, %v)", v, ok)
	}
}

// TestCO2ConcentrationServerWriteRejected verifies that MatterWrite returns a non-nil error (read-only cluster).
func TestCO2ConcentrationServerWriteRejected(t *testing.T) {
	t.Parallel()
	s := measurement.NewCO2ConcentrationServer(fakeFloat{val: 425.0, obs: true})
	err := s.MatterWrite(context.Background(), 0x0000, float32(500.0), hmenum.CommandPriorityHigh)
	if err == nil {
		t.Error("MatterWrite returned nil, want read-only error")
	}
}

// TestCO2ConcentrationServerInvokeRejected verifies that MatterInvoke returns a non-nil error (no commands).
func TestCO2ConcentrationServerInvokeRejected(t *testing.T) {
	t.Parallel()
	s := measurement.NewCO2ConcentrationServer(fakeFloat{val: 425.0, obs: true})
	_, err := s.MatterInvoke(context.Background(), 0x00, nil, hmenum.CommandPriorityHigh)
	if err == nil {
		t.Error("MatterInvoke returned nil error, want rejection")
	}
}

// TestCO2ConcentrationServerReportable verifies that Reportable contains attr 0x0000 (MeasuredValue).
func TestCO2ConcentrationServerReportable(t *testing.T) {
	t.Parallel()
	s := measurement.NewCO2ConcentrationServer(fakeFloat{val: 425.0, obs: true})
	attrs := s.MatterReportable()
	if slices.Contains(attrs, 0x0000) {
		return
	}
	t.Errorf("MatterReportable = %v, want to contain 0x0000", attrs)
}

// --- PM25ConcentrationServer ---

// TestPM25ConcentrationServerHappyPath verifies ClusterID, MeasuredValue, and Unit (µg/m³) for a normal reading.
func TestPM25ConcentrationServerHappyPath(t *testing.T) {
	t.Parallel()
	src := fakeFloat{class: interfaces.MatterMeasurementPM25, val: 42.5, obs: true}
	s := measurement.NewPM25ConcentrationServer(src)

	if got, want := s.MatterClusterID(), uint32(0x042A); got != want {
		t.Errorf("ClusterID = 0x%04X, want 0x%04X", got, want)
	}

	v, ok := s.MatterRead(0x0000)
	if !ok {
		t.Fatal("MatterRead(0x0000) ok = false, want true")
	}
	if got, want := v.(float32), float32(42.5); got != want {
		t.Errorf("MeasuredValue = %v, want %v", got, want)
	}

	unit, ok := s.MatterRead(0x0008)
	if !ok {
		t.Fatal("MatterRead(0x0008) ok = false")
	}
	if got, want := unit.(uint8), uint8(4); got != want {
		t.Errorf("MeasurementUnit = %d, want %d (µg/m³)", got, want)
	}
}

// TestPM25ConcentrationServerMinMax verifies MinMeasuredValue=float32(0) and MaxMeasuredValue=float32(100000).
func TestPM25ConcentrationServerMinMax(t *testing.T) {
	t.Parallel()
	s := measurement.NewPM25ConcentrationServer(fakeFloat{val: 42.5, obs: true})

	minV, ok := s.MatterRead(0x0001)
	if !ok {
		t.Fatal("MatterRead(0x0001) ok = false")
	}
	if got, want := minV.(float32), float32(0); got != want {
		t.Errorf("MinMeasuredValue = %v, want %v", got, want)
	}

	maxV, ok := s.MatterRead(0x0002)
	if !ok {
		t.Fatal("MatterRead(0x0002) ok = false")
	}
	if got, want := maxV.(float32), float32(100000); got != want {
		t.Errorf("MaxMeasuredValue = %v, want %v", got, want)
	}
}

// --- PM10ConcentrationServer ---

// TestPM10ConcentrationServerHappyPath verifies ClusterID, MeasuredValue, and Unit (µg/m³) for a normal reading.
func TestPM10ConcentrationServerHappyPath(t *testing.T) {
	t.Parallel()
	src := fakeFloat{class: interfaces.MatterMeasurementPM10, val: 65.0, obs: true}
	s := measurement.NewPM10ConcentrationServer(src)

	if got, want := s.MatterClusterID(), uint32(0x042D); got != want {
		t.Errorf("ClusterID = 0x%04X, want 0x%04X", got, want)
	}

	v, ok := s.MatterRead(0x0000)
	if !ok {
		t.Fatal("MatterRead(0x0000) ok = false, want true")
	}
	if got, want := v.(float32), float32(65.0); got != want {
		t.Errorf("MeasuredValue = %v, want %v", got, want)
	}

	unit, ok := s.MatterRead(0x0008)
	if !ok {
		t.Fatal("MatterRead(0x0008) ok = false")
	}
	if got, want := unit.(uint8), uint8(4); got != want {
		t.Errorf("MeasurementUnit = %d, want %d (µg/m³)", got, want)
	}
}

// --- PowerSourceServer ---

// TestPowerSourceServerStatusActive verifies Status=uint8(1), Order=uint8(1), and Description="Battery".
func TestPowerSourceServerStatusActive(t *testing.T) {
	t.Parallel()
	s := measurement.NewPowerSourceServer(fakeBool{class: interfaces.MatterMeasurementBattery, val: false, obs: true})

	if got, want := s.MatterClusterID(), uint32(0x002F); got != want {
		t.Errorf("ClusterID = 0x%04X, want 0x%04X", got, want)
	}

	status, ok := s.MatterRead(0x0000)
	if !ok {
		t.Fatal("MatterRead(0x0000) ok = false")
	}
	if got, want := status.(uint8), uint8(1); got != want {
		t.Errorf("Status = %d, want %d (Active)", got, want)
	}

	order, ok := s.MatterRead(0x0001)
	if !ok {
		t.Fatal("MatterRead(0x0001) ok = false")
	}
	if got, want := order.(uint8), uint8(1); got != want {
		t.Errorf("Order = %d, want %d (primary)", got, want)
	}

	desc, ok := s.MatterRead(0x0002)
	if !ok {
		t.Fatal("MatterRead(0x0002) ok = false")
	}
	if got, want := desc.(string), "Battery"; got != want {
		t.Errorf("Description = %q, want %q", got, want)
	}
}

// TestPowerSourceServerBatChargeOK verifies that an observed false value maps to BatChargeLevel=uint8(0) (OK).
func TestPowerSourceServerBatChargeOK(t *testing.T) {
	t.Parallel()
	s := measurement.NewPowerSourceServer(fakeBool{class: interfaces.MatterMeasurementBattery, val: false, obs: true})
	v, ok := s.MatterRead(0x000E)
	if !ok {
		t.Fatal("MatterRead(0x000E) ok = false")
	}
	if got, want := v.(uint8), uint8(0); got != want {
		t.Errorf("BatChargeLevel = %d, want %d (OK)", got, want)
	}
}

// TestPowerSourceServerBatChargeWarning verifies that an observed true value maps to BatChargeLevel=uint8(1) (Warning).
func TestPowerSourceServerBatChargeWarning(t *testing.T) {
	t.Parallel()
	s := measurement.NewPowerSourceServer(fakeBool{class: interfaces.MatterMeasurementBattery, val: true, obs: true})
	v, ok := s.MatterRead(0x000E)
	if !ok {
		t.Fatal("MatterRead(0x000E) ok = false")
	}
	if got, want := v.(uint8), uint8(1); got != want {
		t.Errorf("BatChargeLevel = %d, want %d (Warning)", got, want)
	}
}

// TestPowerSourceServerBatChargeUnobservedDefaultsToOK verifies that an unobserved source defensively returns BatChargeLevel=uint8(0) with ok=true.
func TestPowerSourceServerBatChargeUnobservedDefaultsToOK(t *testing.T) {
	t.Parallel()
	s := measurement.NewPowerSourceServer(fakeBool{class: interfaces.MatterMeasurementBattery, val: false, obs: false})
	v, ok := s.MatterRead(0x000E)
	if !ok {
		t.Fatal("MatterRead(0x000E) ok = false, want true (defensive default)")
	}
	if got, want := v.(uint8), uint8(0); got != want {
		t.Errorf("BatChargeLevel = %d, want %d (OK, defensive default)", got, want)
	}
}

// TestPowerSourceServerBatReplacementNeededTrue verifies that an observed true value sets BatReplacementNeeded=true.
func TestPowerSourceServerBatReplacementNeededTrue(t *testing.T) {
	t.Parallel()
	s := measurement.NewPowerSourceServer(fakeBool{class: interfaces.MatterMeasurementBattery, val: true, obs: true})
	v, ok := s.MatterRead(0x000F)
	if !ok {
		t.Fatal("MatterRead(0x000F) ok = false")
	}
	if got := v.(bool); !got {
		t.Error("BatReplacementNeeded = false, want true")
	}
}

// TestPowerSourceServerBatReplacementNeededFalse verifies that an observed false value sets BatReplacementNeeded=false.
func TestPowerSourceServerBatReplacementNeededFalse(t *testing.T) {
	t.Parallel()
	s := measurement.NewPowerSourceServer(fakeBool{class: interfaces.MatterMeasurementBattery, val: false, obs: true})
	v, ok := s.MatterRead(0x000F)
	if !ok {
		t.Fatal("MatterRead(0x000F) ok = false")
	}
	if got := v.(bool); got {
		t.Error("BatReplacementNeeded = true, want false")
	}
}

// TestPowerSourceServerBatReplaceability verifies that BatReplaceability (0x0010) = uint8(2) (UserReplaceable).
func TestPowerSourceServerBatReplaceability(t *testing.T) {
	t.Parallel()
	s := measurement.NewPowerSourceServer(fakeBool{class: interfaces.MatterMeasurementBattery, val: false, obs: true})
	v, ok := s.MatterRead(0x0010)
	if !ok {
		t.Fatal("MatterRead(0x0010) ok = false")
	}
	if got, want := v.(uint8), uint8(2); got != want {
		t.Errorf("BatReplaceability = %d, want %d (UserReplaceable)", got, want)
	}
}

// TestPowerSourceServerFeatureMapAndRevision verifies FeatureMap=uint32(0x02)
// (BAT bit only) and ClusterRevision=uint16(3). BAT is bit 1 per matter.js
// power-source-cluster.element.ts.
func TestPowerSourceServerFeatureMapAndRevision(t *testing.T) {
	t.Parallel()
	s := measurement.NewPowerSourceServer(fakeBool{class: interfaces.MatterMeasurementBattery, val: false, obs: true})

	fm, ok := s.MatterRead(0xFFFC)
	if !ok {
		t.Fatal("MatterRead(0xFFFC) ok = false")
	}
	if got, want := fm.(uint32), uint32(0x02); got != want {
		t.Errorf("FeatureMap = 0x%02X, want 0x%02X (BAT bit)", got, want)
	}

	rev, ok := s.MatterRead(attrClusterRevision)
	if !ok {
		t.Fatal("MatterRead(ClusterRevision) ok = false")
	}
	if got, want := rev.(uint16), uint16(3); got != want {
		t.Errorf("ClusterRevision = %d, want %d", got, want)
	}
}

// TestPowerSourceServerUnknownAttr verifies that an unknown attribute returns (nil, false).
func TestPowerSourceServerUnknownAttr(t *testing.T) {
	t.Parallel()
	s := measurement.NewPowerSourceServer(fakeBool{class: interfaces.MatterMeasurementBattery, val: false, obs: true})
	v, ok := s.MatterRead(0x9999)
	if ok || v != nil {
		t.Errorf("want (nil, false) for unknown attr, got (%v, %v)", v, ok)
	}
}

// TestPowerSourceServerWriteRejected verifies that MatterWrite returns a non-nil error (read-only cluster).
func TestPowerSourceServerWriteRejected(t *testing.T) {
	t.Parallel()
	s := measurement.NewPowerSourceServer(fakeBool{class: interfaces.MatterMeasurementBattery, val: false, obs: true})
	err := s.MatterWrite(context.Background(), 0x0000, uint8(0), hmenum.CommandPriorityHigh)
	if err == nil {
		t.Error("MatterWrite returned nil, want read-only error")
	}
}

// TestPowerSourceServerInvokeRejected verifies that MatterInvoke returns a non-nil error (no commands).
func TestPowerSourceServerInvokeRejected(t *testing.T) {
	t.Parallel()
	s := measurement.NewPowerSourceServer(fakeBool{class: interfaces.MatterMeasurementBattery, val: false, obs: true})
	_, err := s.MatterInvoke(context.Background(), 0x00, nil, hmenum.CommandPriorityHigh)
	if err == nil {
		t.Error("MatterInvoke returned nil error, want rejection")
	}
}

// TestPowerSourceServerReportable verifies that Reportable contains attr 0x000E (BatChargeLevel) and 0x000F (BatReplacementNeeded).
func TestPowerSourceServerReportable(t *testing.T) {
	t.Parallel()
	s := measurement.NewPowerSourceServer(fakeBool{class: interfaces.MatterMeasurementBattery, val: false, obs: true})
	attrs := s.MatterReportable()

	var hasCharge, hasReplacement bool
	for _, a := range attrs {
		switch a {
		case 0x000E:
			hasCharge = true
		case 0x000F:
			hasReplacement = true
		}
	}
	if !hasCharge {
		t.Errorf("MatterReportable = %v, want to contain 0x000E (BatChargeLevel)", attrs)
	}
	if !hasReplacement {
		t.Errorf("MatterReportable = %v, want to contain 0x000F (BatReplacementNeeded)", attrs)
	}
}

// TestPowerSourceServerFromBool_BatPercentRemainingNotSupported verifies
// that a bool-constructed PowerSourceServer neither reports
// BatPercentRemaining (0x000C) via MatterRead nor lists it in
// MatterAttributes/MatterReportable — the optional conformance [BAT]
// attribute is genuinely absent, not reported as null.
func TestPowerSourceServerFromBool_BatPercentRemainingNotSupported(t *testing.T) {
	t.Parallel()
	s := measurement.NewPowerSourceServer(fakeBool{class: interfaces.MatterMeasurementBattery, val: false, obs: true})

	if v, ok := s.MatterRead(0x000C); ok || v != nil {
		t.Errorf("MatterRead(BatPercentRemaining) = (%v, %v), want (nil, false)", v, ok)
	}
	for _, a := range s.MatterAttributes() {
		if a == 0x000C {
			t.Error("MatterAttributes lists BatPercentRemaining for a bool-constructed instance")
		}
	}
	for _, a := range s.MatterReportable() {
		if a == 0x000C {
			t.Error("MatterReportable lists BatPercentRemaining for a bool-constructed instance")
		}
	}
}

// TestPowerSourceServerFromFloat_BatPercentRemaining verifies that a
// float-constructed PowerSourceServer converts its source's 0-100
// percentage to Matter's half-percent BatPercentRemaining encoding,
// and that BatChargeLevel still reports OK (no boolean LOWBAT signal
// to derive Warning from) without panicking on the nil bool source.
func TestPowerSourceServerFromFloat_BatPercentRemaining(t *testing.T) {
	t.Parallel()
	s := measurement.NewPowerSourceServerFromFloat(fakeFloat{class: interfaces.MatterMeasurementBattery, val: 42.3, obs: true})

	v, ok := s.MatterRead(0x000C)
	if !ok {
		t.Fatal("MatterRead(BatPercentRemaining) ok = false")
	}
	if got, want := v.(uint8), uint8(85); got != want { // 42.3% rounds to 85 half-percent units
		t.Errorf("BatPercentRemaining = %d, want %d", got, want)
	}

	charge, ok := s.MatterRead(0x000E)
	if !ok {
		t.Fatal("MatterRead(BatChargeLevel) ok = false")
	}
	if got, want := charge.(uint8), uint8(0); got != want {
		t.Errorf("BatChargeLevel = %d, want %d (OK — no bool source to derive Warning)", got, want)
	}
}

// TestPowerSourceServerFromFloat_BatPercentRemaining_Unobserved verifies
// that an unobserved float source reports BatPercentRemaining as TLV
// null (present, quality X) rather than a fabricated value.
func TestPowerSourceServerFromFloat_BatPercentRemaining_Unobserved(t *testing.T) {
	t.Parallel()
	s := measurement.NewPowerSourceServerFromFloat(fakeFloat{class: interfaces.MatterMeasurementBattery, val: 0, obs: false})
	v, ok := s.MatterRead(0x000C)
	if !ok {
		t.Fatal("MatterRead(BatPercentRemaining) ok = false, want true (present, null)")
	}
	if v != nil {
		t.Errorf("BatPercentRemaining = %v, want nil (unobserved)", v)
	}
}

// TestPowerSourceServerFromFloat_BatPercentRemaining_Clamped verifies
// the round-then-clamp bounds: 0% -> 0, 100% -> 200, and an
// out-of-range value clamps to the ceiling instead of overflowing.
func TestPowerSourceServerFromFloat_BatPercentRemaining_Clamped(t *testing.T) {
	t.Parallel()
	tests := []struct {
		pct  float64
		want uint8
	}{
		{pct: 0, want: 0},
		{pct: 100, want: 200},
		{pct: 150, want: 200}, // out of the documented 0-100 range; clamp defensively
		{pct: -5, want: 0},    // ditto, negative
	}
	for _, tc := range tests {
		s := measurement.NewPowerSourceServerFromFloat(fakeFloat{class: interfaces.MatterMeasurementBattery, val: tc.pct, obs: true})
		v, ok := s.MatterRead(0x000C)
		if !ok {
			t.Fatalf("pct=%v: MatterRead(BatPercentRemaining) ok = false", tc.pct)
		}
		if got := v.(uint8); got != tc.want {
			t.Errorf("pct=%v: BatPercentRemaining = %d, want %d", tc.pct, got, tc.want)
		}
	}
}

// TestPowerSourceServerFromFloat_MatterAttributesIncludesBatPercentRemaining
// verifies that a float-constructed instance lists BatPercentRemaining
// in both MatterAttributes (wildcard reads) and MatterReportable
// (subscription surface) — the mirror image of
// TestPowerSourceServerFromBool_BatPercentRemainingNotSupported.
func TestPowerSourceServerFromFloat_MatterAttributesIncludesBatPercentRemaining(t *testing.T) {
	t.Parallel()
	s := measurement.NewPowerSourceServerFromFloat(fakeFloat{class: interfaces.MatterMeasurementBattery, val: 50, obs: true})

	var inAttrs, inReportable bool
	for _, a := range s.MatterAttributes() {
		if a == 0x000C {
			inAttrs = true
		}
	}
	for _, a := range s.MatterReportable() {
		if a == 0x000C {
			inReportable = true
		}
	}
	if !inAttrs {
		t.Error("MatterAttributes does not list BatPercentRemaining for a float-constructed instance")
	}
	if !inReportable {
		t.Error("MatterReportable does not list BatPercentRemaining for a float-constructed instance")
	}
}

// --- Materializer (FromMeasurementClass) — concentration + battery coverage ---

// TestFromMeasurementClassP2Coverage verifies dispatch for CO2, PM2.5, PM10, Battery, and wrong-type sources.
func TestFromMeasurementClassP2Coverage(t *testing.T) {
	t.Parallel()

	type row struct {
		name    string
		class   interfaces.MatterMeasurementClass
		src     any
		wantIDs []uint32
		wantNil bool
	}

	// The air-quality classes materialise two servers: the concentration
	// cluster plus the AirQuality cluster (0x005B) that the
	// AirQualitySensor device type mandates.
	rows := []row{
		{
			name:    "CO2+fakeFloat→0x005B+0x040D",
			class:   interfaces.MatterMeasurementCO2,
			src:     fakeFloat{class: interfaces.MatterMeasurementCO2, val: 800.0, obs: true},
			wantIDs: []uint32{0x005B, 0x040D},
		},
		{
			name:    "PM25+fakeFloat→0x005B+0x042A",
			class:   interfaces.MatterMeasurementPM25,
			src:     fakeFloat{class: interfaces.MatterMeasurementPM25, val: 15.0, obs: true},
			wantIDs: []uint32{0x005B, 0x042A},
		},
		{
			name:    "PM10+fakeFloat→0x005B+0x042D",
			class:   interfaces.MatterMeasurementPM10,
			src:     fakeFloat{class: interfaces.MatterMeasurementPM10, val: 30.0, obs: true},
			wantIDs: []uint32{0x005B, 0x042D},
		},
		{
			name:    "Battery+fakeBool→0x002F",
			class:   interfaces.MatterMeasurementBattery,
			src:     fakeBool{class: interfaces.MatterMeasurementBattery, val: false, obs: true},
			wantIDs: []uint32{0x002F},
		},
		{
			name:    "CO2+fakeBool→nil(wrong type)",
			class:   interfaces.MatterMeasurementCO2,
			src:     fakeBool{class: interfaces.MatterMeasurementCO2, val: false, obs: true},
			wantNil: true,
		},
		{
			// Battery accepts both a bool source (BatChargeLevel) and a
			// float source (BatPercentRemaining, e.g. a derived
			// battery-percentage sensor) — see
			// TestFromMeasurementClassBattery_FloatSource for the
			// dedicated coverage of the wire-level projection.
			name:    "Battery+fakeFloat→0x002F",
			class:   interfaces.MatterMeasurementBattery,
			src:     fakeFloat{class: interfaces.MatterMeasurementBattery, val: 80.0, obs: true},
			wantIDs: []uint32{0x002F},
		},
	}

	for _, r := range rows {
		t.Run(r.name, func(t *testing.T) {
			t.Parallel()
			servers := measurement.FromMeasurementClass(r.class, r.src)
			if r.wantNil {
				if servers != nil {
					t.Errorf("want nil, got %v (len=%d)", servers, len(servers))
				}
				return
			}
			got := make([]uint32, 0, len(servers))
			for _, s := range servers {
				got = append(got, s.MatterClusterID())
			}
			if !slices.Equal(got, r.wantIDs) {
				t.Errorf("cluster IDs = %#04x, want %#04x", got, r.wantIDs)
			}
		})
	}
}

// --- ElectricalPowerServer (0x0090) ---

// TestElectricalPowerServerHappyPath verifies ClusterID, ActivePower
// conversion, fixed metadata attributes, FeatureMap, and ClusterRevision.
func TestElectricalPowerServerHappyPath(t *testing.T) {
	t.Parallel()
	src := fakeFloat{class: interfaces.MatterMeasurementPower, val: 1500.0, obs: true}
	s := measurement.NewElectricalPowerServer(src)

	if got := s.MatterClusterID(); got != measurement.ClusterElectricalPower {
		t.Errorf("ClusterID = 0x%04X, want 0x%04X", got, measurement.ClusterElectricalPower)
	}

	// PowerMode at 0x0000 = uint8(2) (AC).
	pm, ok := s.MatterRead(0x0000)
	if !ok {
		t.Fatal("MatterRead(0x0000 PowerMode) ok = false")
	}
	if got, want := pm.(uint8), uint8(2); got != want {
		t.Errorf("PowerMode = %d, want %d (AC)", got, want)
	}

	// ActivePower at 0x0008 = int64 mW: 1500.0 W * 1000 = 1500000.
	ap, ok := s.MatterRead(0x0008)
	if !ok {
		t.Fatal("MatterRead(0x0008 ActivePower) ok = false")
	}
	if got, want := ap.(int64), int64(1500000); got != want {
		t.Errorf("ActivePower = %d, want %d", got, want)
	}

	// FeatureMap at 0xFFFC = uint32(2) (ALTC bit, 1<<1).
	fm, ok := s.MatterRead(0xFFFC)
	if !ok {
		t.Fatal("MatterRead(0xFFFC FeatureMap) ok = false")
	}
	if got, want := fm.(uint32), uint32(2); got != want {
		t.Errorf("FeatureMap = %d, want %d (ALTC)", got, want)
	}

	// ClusterRevision at 0xFFFD = uint16(3).
	rev, ok := s.MatterRead(attrClusterRevision)
	if !ok {
		t.Fatal("MatterRead(ClusterRevision) ok = false")
	}
	if got, want := rev.(uint16), uint16(3); got != want {
		t.Errorf("ClusterRevision = %d, want %d", got, want)
	}
}

// TestElectricalPowerServerUnobserved verifies that an unobserved source
// returns (nil, true) for the ActivePower attribute — transiently null, not unsupported
// (see TestTemperatureServerUnobserved for the full rationale).
func TestElectricalPowerServerUnobserved(t *testing.T) {
	t.Parallel()
	s := measurement.NewElectricalPowerServer(fakeFloat{val: 1500.0, obs: false})
	v, ok := s.MatterRead(0x0008)
	if !ok || v != nil {
		t.Errorf("ActivePower unobserved: want (nil, true), got (%v, %v)", v, ok)
	}
}

// TestElectricalPowerServerNullAttributes verifies that Voltage (0x0004),
// ActiveCurrent (0x0005), and Frequency (0x000E) return (nil, true) — null
// per spec; multi-source projection is future work. (Attribute IDs
// previously mis-encoded as 0x0005/0x0006/0x000A which collide with spec
// slots; corrected to spec-compliant values.)
func TestElectricalPowerServerNullAttributes(t *testing.T) {
	t.Parallel()
	s := measurement.NewElectricalPowerServer(fakeFloat{val: 1500.0, obs: true})
	for _, attrID := range []uint32{0x0004, 0x0005, 0x000E} {
		v, ok := s.MatterRead(attrID)
		if !ok {
			t.Errorf("MatterRead(0x%04X): ok = false, want true (null present)", attrID)
		}
		if v != nil {
			t.Errorf("MatterRead(0x%04X): value = %v, want nil (null)", attrID, v)
		}
	}
}

// TestElectricalPowerServerWriteRejected verifies that MatterWrite returns
// a non-nil error (read-only cluster).
func TestElectricalPowerServerWriteRejected(t *testing.T) {
	t.Parallel()
	s := measurement.NewElectricalPowerServer(fakeFloat{val: 1500.0, obs: true})
	err := s.MatterWrite(context.Background(), 0x0008, int64(0), hmenum.CommandPriorityHigh)
	if err == nil {
		t.Error("MatterWrite returned nil, want read-only error")
	}
}

// --- ElectricalEnergyServer (0x0091) ---

// TestElectricalEnergyServerHappyPath verifies ClusterID, CumulativeImported
// conversion, FeatureMap, and ClusterRevision.
func TestElectricalEnergyServerHappyPath(t *testing.T) {
	t.Parallel()
	src := fakeFloat{class: interfaces.MatterMeasurementEnergy, val: 12345.0, obs: true}
	s := measurement.NewElectricalEnergyServer(src)

	if got := s.MatterClusterID(); got != measurement.ClusterElectricalEnergy {
		t.Errorf("ClusterID = 0x%04X, want 0x%04X", got, measurement.ClusterElectricalEnergy)
	}

	// CumulativeEnergyImported at 0x0001 = EnergyMeasurementStruct with
	// Energy in int64 mWh: 12345.0 Wh * 1000 = 12345000. A bare int64
	// is wire-invalid — chip-tool's typed decode rejects it.
	ce, ok := s.MatterRead(0x0001)
	if !ok {
		t.Fatal("MatterRead(0x0001 CumulativeImported) ok = false")
	}
	if got, want := ce.(measurement.EnergyMeasurementStruct), (measurement.EnergyMeasurementStruct{Energy: 12345000}); got != want {
		t.Errorf("CumulativeEnergyImported = %+v, want %+v", got, want)
	}

	// FeatureMap at 0xFFFC = uint32(5): IMPE (bit 0) | CUME (bit 2).
	// CumulativeEnergyImported has conformance "IMPE & CUME", so serving
	// it obliges both bits.
	fm, ok := s.MatterRead(0xFFFC)
	if !ok {
		t.Fatal("MatterRead(0xFFFC FeatureMap) ok = false")
	}
	if got, want := fm.(uint32), uint32(0x05); got != want {
		t.Errorf("FeatureMap = 0x%02X, want 0x%02X (IMPE|CUME)", got, want)
	}

	// ClusterRevision at 0xFFFD = uint16(2) per matter.js HEAD (@matter/model 0.16.11).
	rev, ok := s.MatterRead(attrClusterRevision)
	if !ok {
		t.Fatal("MatterRead(ClusterRevision) ok = false")
	}
	if got, want := rev.(uint16), uint16(2); got != want {
		t.Errorf("ClusterRevision = %d, want %d", got, want)
	}
}

// TestElectricalEnergyServerUnobserved verifies that an unobserved source
// returns (nil, true) for CumulativeEnergyImported — transiently null, not unsupported
// (see TestTemperatureServerUnobserved for the full rationale).
func TestElectricalEnergyServerUnobserved(t *testing.T) {
	t.Parallel()
	s := measurement.NewElectricalEnergyServer(fakeFloat{val: 12345.0, obs: false})
	v, ok := s.MatterRead(0x0001)
	if !ok || v != nil {
		t.Errorf("CumulativeImported unobserved: want (nil, true), got (%v, %v)", v, ok)
	}
}

// TestElectricalEnergyServerExportedIsUnsupported verifies that
// CumulativeEnergyExported (0x0002) is not served. Its conformance is
// "EXPE & CUME" and HM metering hardware has no exported-energy path, so
// EXPE stays clear — returning a null for it would publish an attribute
// whose gating feature is absent, which is what makes a
// conformance-checking controller drop the cluster.
func TestElectricalEnergyServerExportedIsUnsupported(t *testing.T) {
	t.Parallel()
	s := measurement.NewElectricalEnergyServer(fakeFloat{val: 12345.0, obs: true})
	v, ok := s.MatterRead(0x0002)
	if ok {
		t.Errorf("MatterRead(0x0002 CumulativeExported) = (%v, true), want ok = false (unsupported attribute)", v)
	}
	for _, id := range s.MatterAttributes() {
		if id == 0x0002 {
			t.Error("MatterAttributes() advertises CumulativeEnergyExported (0x0002) while EXPE is not in the FeatureMap")
		}
	}
}

// TestElectricalPowerServerAccuracyRangesNonEmpty verifies that
// attrElPwrAccuracy (0x0002) returns at least one AccuracyRanges entry
// per Matter §2.13.5.2.
// An empty AccuracyRanges list is schema-invalid and causes chip/Apple
// CHIP Error 0x26 (Wrong TLV type) during Subscribe validation.
func TestElectricalPowerServerAccuracyRangesNonEmpty(t *testing.T) {
	t.Parallel()
	s := measurement.NewElectricalPowerServer(fakeFloat{val: 1500.0, obs: true})
	v, ok := s.MatterRead(0x0002) // attrElPwrAccuracy
	if !ok {
		t.Fatal("MatterRead(0x0002 Accuracy) ok = false, want true")
	}
	list, ok := v.([]measurement.AccuracyStruct)
	if !ok {
		t.Fatalf("Accuracy value is %T, want []AccuracyStruct", v)
	}
	if len(list) == 0 {
		t.Fatal("Accuracy list is empty; Matter §2.13.5.2 requires ≥ 1 AccuracyRanges entry")
	}
	if len(list[0].AccuracyRanges) == 0 {
		t.Fatalf("AccuracyRanges[0] is empty; Matter §2.13.5.2 requires ≥ 1 AccuracyRangeStruct entry")
	}
}

// TestElectricalEnergyServerAccuracyRangesNonEmpty verifies that
// attrElEnAccuracy (0x0000) returns at least one AccuracyRanges entry
// per Matter §2.14.5.2.
func TestElectricalEnergyServerAccuracyRangesNonEmpty(t *testing.T) {
	t.Parallel()
	s := measurement.NewElectricalEnergyServer(fakeFloat{val: 12345.0, obs: true})
	v, ok := s.MatterRead(0x0000) // attrElEnAccuracy
	if !ok {
		t.Fatal("MatterRead(0x0000 Accuracy) ok = false, want true")
	}
	list, ok := v.([]measurement.AccuracyStruct)
	if !ok {
		t.Fatalf("Accuracy value is %T, want []AccuracyStruct", v)
	}
	if len(list) == 0 {
		t.Fatal("Accuracy list is empty; Matter §2.14.5.2 requires ≥ 1 AccuracyRanges entry")
	}
	if len(list[0].AccuracyRanges) == 0 {
		t.Fatalf("AccuracyRanges[0] is empty; Matter §2.14.5.2 requires ≥ 1 AccuracyRangeStruct entry")
	}
}

// TestElectricalPowerServerOnMatterValueChangedForwards verifies that
// ElectricalPowerServer.OnMatterValueChanged forwards subscription and
// unsubscription to the wrapped source's MatterChangeNotifier.
func TestElectricalPowerServerOnMatterValueChangedForwards(t *testing.T) {
	t.Parallel()
	src := &fakeFloatNotifier{class: interfaces.MatterMeasurementPower, val: 1500.0, obs: true}
	s := measurement.NewElectricalPowerServer(src)

	calls := 0
	unsubscribe := s.OnMatterValueChanged(func() { calls++ })
	if unsubscribe == nil {
		t.Fatal("OnMatterValueChanged returned nil unsubscribe, want non-nil")
	}
	if src.cb == nil {
		t.Fatal("wrapped source did not receive a callback")
	}

	src.cb()
	if calls != 1 {
		t.Errorf("cb call count = %d, want 1 after triggering source notifier", calls)
	}

	unsubscribe()
	if src.unsubCalls != 1 {
		t.Errorf("source unsubscribe call count = %d, want 1", src.unsubCalls)
	}
}

// TestElectricalPowerServerOnMatterValueChangedNoNotifierFallback verifies
// that wrapping a source without MatterChangeNotifier yields a safe, non-nil
// no-op unsubscribe rather than a panic.
func TestElectricalPowerServerOnMatterValueChangedNoNotifierFallback(t *testing.T) {
	t.Parallel()
	s := measurement.NewElectricalPowerServer(fakeFloat{val: 1500.0, obs: true})
	unsubscribe := s.OnMatterValueChanged(func() {})
	if unsubscribe == nil {
		t.Fatal("OnMatterValueChanged returned nil unsubscribe, want non-nil no-op")
	}
	unsubscribe() // must not panic
}

// TestElectricalEnergyServerOnMatterValueChangedForwards verifies that
// ElectricalEnergyServer.OnMatterValueChanged forwards subscription and
// unsubscription to the wrapped source's MatterChangeNotifier.
func TestElectricalEnergyServerOnMatterValueChangedForwards(t *testing.T) {
	t.Parallel()
	src := &fakeFloatNotifier{class: interfaces.MatterMeasurementEnergy, val: 12345.0, obs: true}
	s := measurement.NewElectricalEnergyServer(src)

	calls := 0
	unsubscribe := s.OnMatterValueChanged(func() { calls++ })
	if unsubscribe == nil {
		t.Fatal("OnMatterValueChanged returned nil unsubscribe, want non-nil")
	}
	if src.cb == nil {
		t.Fatal("wrapped source did not receive a callback")
	}

	src.cb()
	if calls != 1 {
		t.Errorf("cb call count = %d, want 1 after triggering source notifier", calls)
	}

	unsubscribe()
	if src.unsubCalls != 1 {
		t.Errorf("source unsubscribe call count = %d, want 1", src.unsubCalls)
	}
}

// TestElectricalEnergyServerOnMatterValueChangedNoNotifierFallback verifies
// that wrapping a source without MatterChangeNotifier yields a safe, non-nil
// no-op unsubscribe rather than a panic.
func TestElectricalEnergyServerOnMatterValueChangedNoNotifierFallback(t *testing.T) {
	t.Parallel()
	s := measurement.NewElectricalEnergyServer(fakeFloat{val: 12345.0, obs: true})
	unsubscribe := s.OnMatterValueChanged(func() {})
	if unsubscribe == nil {
		t.Fatal("OnMatterValueChanged returned nil unsubscribe, want non-nil no-op")
	}
	unsubscribe() // must not panic
}

// TestPowerSourceServerFeatureMapHasBATWithoutREPLC verifies that the
// FeatureMap for a battery-backed PowerSource advertises BAT (bit 1 = 0x02)
// and leaves REPLC (bit 3 = 0x08) clear. matter.js
// power-source-cluster.element.ts records BatReplaceability (0x0010) with
// conformance "BAT" — serving it never needed REPLC — while REPLC makes
// BatReplacementDescription (0x0013) and BatQuantity (0x0019) mandatory,
// and the CCU reports neither.
func TestPowerSourceServerFeatureMapHasBATWithoutREPLC(t *testing.T) {
	t.Parallel()
	s := measurement.NewPowerSourceServer(fakeBool{class: interfaces.MatterMeasurementBattery, val: false, obs: true})
	v, ok := s.MatterRead(0xFFFC) // AttrGlobalFeatureMap
	if !ok {
		t.Fatal("MatterRead(0xFFFC FeatureMap) ok = false")
	}
	fm, ok := v.(uint32)
	if !ok {
		t.Fatalf("FeatureMap is %T, want uint32", v)
	}
	const batBit = uint32(1 << 1)
	const replcBit = uint32(1 << 3)
	if fm&batBit == 0 {
		t.Errorf("FeatureMap = 0x%02X: BAT bit (0x02) not set", fm)
	}
	if fm&replcBit != 0 {
		t.Errorf("FeatureMap = 0x%02X: REPLC bit (0x08) set, but neither BatReplacementDescription (0x0013) nor BatQuantity (0x0019) is served", fm)
	}
	// BatReplaceability stays readable — it is gated on BAT, not REPLC.
	if _, served := s.MatterRead(0x0010); !served {
		t.Error("MatterRead(0x0010 BatReplaceability) ok = false; it is mandatory under BAT")
	}
}

// TestFromMeasurementClass_PowerEnergy verifies that the materializer
// dispatches Power → ClusterID 0x0090, Energy → 0x0091, and returns nil
// for a wrong-typed source (fakeBool for a float-only class).
func TestFromMeasurementClass_PowerEnergy(t *testing.T) {
	t.Parallel()

	type row struct {
		name    string
		class   interfaces.MatterMeasurementClass
		src     any
		wantID  uint32
		wantNil bool
	}

	rows := []row{
		{
			name:   "Power+fakeFloat→0x0090",
			class:  interfaces.MatterMeasurementPower,
			src:    fakeFloat{class: interfaces.MatterMeasurementPower, val: 800.0, obs: true},
			wantID: measurement.ClusterElectricalPower,
		},
		{
			name:   "Energy+fakeFloat→0x0091",
			class:  interfaces.MatterMeasurementEnergy,
			src:    fakeFloat{class: interfaces.MatterMeasurementEnergy, val: 1234.5, obs: true},
			wantID: measurement.ClusterElectricalEnergy,
		},
		{
			name:    "Power+fakeBool→nil(wrong type)",
			class:   interfaces.MatterMeasurementPower,
			src:     fakeBool{class: interfaces.MatterMeasurementPower, val: false, obs: true},
			wantNil: true,
		},
		{
			name:    "Energy+fakeBool→nil(wrong type)",
			class:   interfaces.MatterMeasurementEnergy,
			src:     fakeBool{class: interfaces.MatterMeasurementEnergy, val: false, obs: true},
			wantNil: true,
		},
	}

	for _, r := range rows {
		t.Run(r.name, func(t *testing.T) {
			t.Parallel()
			servers := measurement.FromMeasurementClass(r.class, r.src)
			if r.wantNil {
				if servers != nil {
					t.Errorf("want nil, got %v (len=%d)", servers, len(servers))
				}
				return
			}
			if len(servers) != 1 {
				t.Fatalf("want 1 server, got %d", len(servers))
			}
			if got := servers[0].MatterClusterID(); got != r.wantID {
				t.Errorf("ClusterID = 0x%04X, want 0x%04X", got, r.wantID)
			}
		})
	}
}

// --- DataVersion tests for all 9 remaining servers ---
//
// Each test verifies that a freshly-constructed server carries a non-zero
// DataVersion and that MatterDataVersion() satisfies the
// interfaces.MatterClusterDataVersion interface. The random-init rationale
// is documented in cluster.DataVersionTracker; TemperatureServer carries
// the extended commentary; these are the one-line analogs.

func TestHumidityServer_DataVersion_StartsNonZero(t *testing.T) {
	t.Parallel()
	s := measurement.NewHumidityServer(fakeFloat{val: 50.0, obs: true})
	var _ interface{ MatterDataVersion() uint32 } = s
	if v := s.MatterDataVersion(); v == 0 {
		t.Errorf("HumidityServer.MatterDataVersion() = 0, want non-zero (Matter §10.6.5)")
	}
}

func TestIlluminanceServer_DataVersion_StartsNonZero(t *testing.T) {
	t.Parallel()
	s := measurement.NewIlluminanceServer(fakeFloat{val: 100.0, obs: true})
	var _ interface{ MatterDataVersion() uint32 } = s
	if v := s.MatterDataVersion(); v == 0 {
		t.Errorf("IlluminanceServer.MatterDataVersion() = 0, want non-zero (Matter §10.6.5)")
	}
}

func TestPressureServer_DataVersion_StartsNonZero(t *testing.T) {
	t.Parallel()
	s := measurement.NewPressureServer(fakeFloat{val: 1013.25, obs: true})
	var _ interface{ MatterDataVersion() uint32 } = s
	if v := s.MatterDataVersion(); v == 0 {
		t.Errorf("PressureServer.MatterDataVersion() = 0, want non-zero (Matter §10.6.5)")
	}
}

func TestBooleanStateServer_DataVersion_StartsNonZero(t *testing.T) {
	t.Parallel()
	s := measurement.NewBooleanStateServer(fakeBool{val: false, obs: true})
	var _ interface{ MatterDataVersion() uint32 } = s
	if v := s.MatterDataVersion(); v == 0 {
		t.Errorf("BooleanStateServer.MatterDataVersion() = 0, want non-zero (Matter §10.6.5)")
	}
}

func TestOccupancySensingServer_DataVersion_StartsNonZero(t *testing.T) {
	t.Parallel()
	s := measurement.NewOccupancySensingServer(fakeBool{val: false, obs: true})
	var _ interface{ MatterDataVersion() uint32 } = s
	if v := s.MatterDataVersion(); v == 0 {
		t.Errorf("OccupancySensingServer.MatterDataVersion() = 0, want non-zero (Matter §10.6.5)")
	}
}

func TestCO2ConcentrationServer_DataVersion_StartsNonZero(t *testing.T) {
	t.Parallel()
	s := measurement.NewCO2ConcentrationServer(fakeFloat{val: 400.0, obs: true})
	var _ interface{ MatterDataVersion() uint32 } = s
	if v := s.MatterDataVersion(); v == 0 {
		t.Errorf("CO2ConcentrationServer.MatterDataVersion() = 0, want non-zero (Matter §10.6.5)")
	}
}

func TestPM25ConcentrationServer_DataVersion_StartsNonZero(t *testing.T) {
	t.Parallel()
	s := measurement.NewPM25ConcentrationServer(fakeFloat{val: 12.0, obs: true})
	var _ interface{ MatterDataVersion() uint32 } = s
	if v := s.MatterDataVersion(); v == 0 {
		t.Errorf("PM25ConcentrationServer.MatterDataVersion() = 0, want non-zero (Matter §10.6.5)")
	}
}

func TestPM10ConcentrationServer_DataVersion_StartsNonZero(t *testing.T) {
	t.Parallel()
	s := measurement.NewPM10ConcentrationServer(fakeFloat{val: 20.0, obs: true})
	var _ interface{ MatterDataVersion() uint32 } = s
	if v := s.MatterDataVersion(); v == 0 {
		t.Errorf("PM10ConcentrationServer.MatterDataVersion() = 0, want non-zero (Matter §10.6.5)")
	}
}

func TestPowerSourceServer_DataVersion_StartsNonZero(t *testing.T) {
	t.Parallel()
	s := measurement.NewPowerSourceServer(fakeBool{val: false, obs: true})
	var _ interface{ MatterDataVersion() uint32 } = s
	if v := s.MatterDataVersion(); v == 0 {
		t.Errorf("PowerSourceServer.MatterDataVersion() = 0, want non-zero (Matter §10.6.5)")
	}
}

func TestElectricalPowerServer_DataVersion_StartsNonZero(t *testing.T) {
	t.Parallel()
	s := measurement.NewElectricalPowerServer(fakeFloat{val: 1500.0, obs: true})
	var _ interface{ MatterDataVersion() uint32 } = s
	if v := s.MatterDataVersion(); v == 0 {
		t.Errorf("ElectricalPowerServer.MatterDataVersion() = 0, want non-zero (Matter §10.6.5)")
	}
}

func TestElectricalEnergyServer_DataVersion_StartsNonZero(t *testing.T) {
	t.Parallel()
	s := measurement.NewElectricalEnergyServer(fakeFloat{val: 123.4, obs: true})
	var _ interface{ MatterDataVersion() uint32 } = s
	if v := s.MatterDataVersion(); v == 0 {
		t.Errorf("ElectricalEnergyServer.MatterDataVersion() = 0, want non-zero (Matter §10.6.5)")
	}
}

// ─── HumidityServer ────────────────────────────────────────────────────────

func TestHumidityServer_MatterWrite_ReadOnly(t *testing.T) {
	t.Parallel()
	s := measurement.NewHumidityServer(fakeFloat{val: 50, obs: true})
	if err := s.MatterWrite(context.Background(), 0x0000, nil, hmenum.CommandPriorityHigh); err == nil {
		t.Error("MatterWrite: want non-nil error")
	}
}

func TestHumidityServer_MatterInvoke_Rejected(t *testing.T) {
	t.Parallel()
	s := measurement.NewHumidityServer(fakeFloat{val: 50, obs: true})
	_, err := s.MatterInvoke(context.Background(), 0x00, nil, hmenum.CommandPriorityHigh)
	if err == nil {
		t.Error("MatterInvoke: want non-nil error")
	}
}

func TestHumidityServer_MatterReportable_ContainsMeasuredValue(t *testing.T) {
	t.Parallel()
	s := measurement.NewHumidityServer(fakeFloat{val: 50, obs: true})
	if slices.Contains(s.MatterReportable(), 0x0000) {
		return
	}
	t.Error("MatterReportable: missing 0x0000")
}

func TestHumidityServer_MatterAttributes_NonEmpty(t *testing.T) {
	t.Parallel()
	s := measurement.NewHumidityServer(fakeFloat{val: 50, obs: true})
	if len(s.MatterAttributes()) == 0 {
		t.Error("MatterAttributes: want non-empty")
	}
}

func TestHumidityServer_MatterRead_Unobserved_ReturnsNilTrue(t *testing.T) {
	t.Parallel()
	s := measurement.NewHumidityServer(fakeFloat{val: 50, obs: false})
	v, ok := s.MatterRead(0x0000)
	if !ok || v != nil {
		t.Errorf("unobserved: want (nil, true), got (%v, %v)", v, ok)
	}
}

func TestHumidityServer_MatterRead_UnknownAttr(t *testing.T) {
	t.Parallel()
	s := measurement.NewHumidityServer(fakeFloat{val: 50, obs: true})
	_, ok := s.MatterRead(0x9999)
	if ok {
		t.Error("unknown attr: want ok=false")
	}
}

// ─── IlluminanceServer ─────────────────────────────────────────────────────

func TestIlluminanceServer_MatterWrite_ReadOnly(t *testing.T) {
	t.Parallel()
	s := measurement.NewIlluminanceServer(fakeFloat{val: 500, obs: true})
	if err := s.MatterWrite(context.Background(), 0x0000, nil, hmenum.CommandPriorityHigh); err == nil {
		t.Error("MatterWrite: want non-nil error")
	}
}

func TestIlluminanceServer_MatterInvoke_Rejected(t *testing.T) {
	t.Parallel()
	s := measurement.NewIlluminanceServer(fakeFloat{val: 500, obs: true})
	_, err := s.MatterInvoke(context.Background(), 0x00, nil, hmenum.CommandPriorityHigh)
	if err == nil {
		t.Error("MatterInvoke: want non-nil error")
	}
}

func TestIlluminanceServer_MatterReportable_ContainsMeasuredValue(t *testing.T) {
	t.Parallel()
	s := measurement.NewIlluminanceServer(fakeFloat{val: 500, obs: true})
	if slices.Contains(s.MatterReportable(), 0x0000) {
		return
	}
	t.Error("MatterReportable: missing 0x0000")
}

func TestIlluminanceServer_MatterAttributes_NonEmpty(t *testing.T) {
	t.Parallel()
	s := measurement.NewIlluminanceServer(fakeFloat{val: 500, obs: true})
	if len(s.MatterAttributes()) == 0 {
		t.Error("MatterAttributes: want non-empty")
	}
}

func TestIlluminanceServer_MatterRead_Unobserved(t *testing.T) {
	t.Parallel()
	s := measurement.NewIlluminanceServer(fakeFloat{obs: false})
	v, ok := s.MatterRead(0x0000)
	if !ok || v != nil {
		t.Errorf("unobserved: want (nil, true), got (%v, %v)", v, ok)
	}
}

func TestIlluminanceServer_MatterRead_UnknownAttr(t *testing.T) {
	t.Parallel()
	s := measurement.NewIlluminanceServer(fakeFloat{val: 500, obs: true})
	_, ok := s.MatterRead(0x9999)
	if ok {
		t.Error("unknown attr: want ok=false")
	}
}

// ─── PressureServer ────────────────────────────────────────────────────────

func TestPressureServer_MatterWrite_ReadOnly(t *testing.T) {
	t.Parallel()
	s := measurement.NewPressureServer(fakeFloat{val: 1013.25, obs: true})
	if err := s.MatterWrite(context.Background(), 0x0000, nil, hmenum.CommandPriorityHigh); err == nil {
		t.Error("MatterWrite: want non-nil error")
	}
}

func TestPressureServer_MatterInvoke_Rejected(t *testing.T) {
	t.Parallel()
	s := measurement.NewPressureServer(fakeFloat{val: 1013.25, obs: true})
	_, err := s.MatterInvoke(context.Background(), 0x00, nil, hmenum.CommandPriorityHigh)
	if err == nil {
		t.Error("MatterInvoke: want non-nil error")
	}
}

func TestPressureServer_MatterReportable_ContainsMeasuredValue(t *testing.T) {
	t.Parallel()
	s := measurement.NewPressureServer(fakeFloat{val: 1013.25, obs: true})
	if slices.Contains(s.MatterReportable(), 0x0000) {
		return
	}
	t.Error("MatterReportable: missing 0x0000")
}

func TestPressureServer_MatterAttributes_NonEmpty(t *testing.T) {
	t.Parallel()
	s := measurement.NewPressureServer(fakeFloat{val: 1013.25, obs: true})
	if len(s.MatterAttributes()) == 0 {
		t.Error("MatterAttributes: want non-empty")
	}
}

// ─── BooleanStateServer ────────────────────────────────────────────────────

func TestBooleanStateServer_MatterWrite_ReadOnly(t *testing.T) {
	t.Parallel()
	s := measurement.NewBooleanStateServer(fakeBool{val: true, obs: true})
	if err := s.MatterWrite(context.Background(), 0x0000, nil, hmenum.CommandPriorityHigh); err == nil {
		t.Error("MatterWrite: want non-nil error")
	}
}

func TestBooleanStateServer_MatterInvoke_Rejected(t *testing.T) {
	t.Parallel()
	s := measurement.NewBooleanStateServer(fakeBool{val: true, obs: true})
	_, err := s.MatterInvoke(context.Background(), 0x00, nil, hmenum.CommandPriorityHigh)
	if err == nil {
		t.Error("MatterInvoke: want non-nil error")
	}
}

func TestBooleanStateServer_MatterReportable_ContainsStateValue(t *testing.T) {
	t.Parallel()
	s := measurement.NewBooleanStateServer(fakeBool{val: true, obs: true})
	found := false
	for _, id := range s.MatterReportable() {
		if id == 0x0000 {
			found = true
		}
	}
	if !found {
		t.Error("MatterReportable: missing 0x0000 (StateValue)")
	}
}

func TestBooleanStateServer_MatterAttributes_NonEmpty(t *testing.T) {
	t.Parallel()
	s := measurement.NewBooleanStateServer(fakeBool{val: true, obs: true})
	if len(s.MatterAttributes()) == 0 {
		t.Error("MatterAttributes: want non-empty")
	}
}

// ─── ElectricalPowerServer ─────────────────────────────────────────────────

func TestElectricalPowerServer_MatterWrite_ReadOnly(t *testing.T) {
	t.Parallel()
	s := measurement.NewElectricalPowerServer(fakeFloat{val: 1500, obs: true})
	if err := s.MatterWrite(context.Background(), 0x0000, nil, hmenum.CommandPriorityHigh); err == nil {
		t.Error("MatterWrite: want non-nil error")
	}
}

func TestElectricalPowerServer_MatterInvoke_Rejected(t *testing.T) {
	t.Parallel()
	s := measurement.NewElectricalPowerServer(fakeFloat{val: 1500, obs: true})
	_, err := s.MatterInvoke(context.Background(), 0x00, nil, hmenum.CommandPriorityHigh)
	if err == nil {
		t.Error("MatterInvoke: want non-nil error")
	}
}

func TestElectricalPowerServer_MatterReportable_NonEmpty(t *testing.T) {
	t.Parallel()
	s := measurement.NewElectricalPowerServer(fakeFloat{val: 1500, obs: true})
	if len(s.MatterReportable()) == 0 {
		t.Error("MatterReportable: want non-empty")
	}
}

func TestElectricalPowerServer_MatterAttributes_NonEmpty(t *testing.T) {
	t.Parallel()
	s := measurement.NewElectricalPowerServer(fakeFloat{val: 1500, obs: true})
	if len(s.MatterAttributes()) == 0 {
		t.Error("MatterAttributes: want non-empty")
	}
}

// ─── ElectricalEnergyServer ────────────────────────────────────────────────

func TestElectricalEnergyServer_MatterWrite_ReadOnly(t *testing.T) {
	t.Parallel()
	s := measurement.NewElectricalEnergyServer(fakeFloat{val: 100, obs: true})
	if err := s.MatterWrite(context.Background(), 0x0000, nil, hmenum.CommandPriorityHigh); err == nil {
		t.Error("MatterWrite: want non-nil error")
	}
}

func TestElectricalEnergyServer_MatterInvoke_Rejected(t *testing.T) {
	t.Parallel()
	s := measurement.NewElectricalEnergyServer(fakeFloat{val: 100, obs: true})
	_, err := s.MatterInvoke(context.Background(), 0x00, nil, hmenum.CommandPriorityHigh)
	if err == nil {
		t.Error("MatterInvoke: want non-nil error")
	}
}

func TestElectricalEnergyServer_MatterReportable_NonEmpty(t *testing.T) {
	t.Parallel()
	s := measurement.NewElectricalEnergyServer(fakeFloat{val: 100, obs: true})
	if len(s.MatterReportable()) == 0 {
		t.Error("MatterReportable: want non-empty")
	}
}

func TestElectricalEnergyServer_MatterAttributes_NonEmpty(t *testing.T) {
	t.Parallel()
	s := measurement.NewElectricalEnergyServer(fakeFloat{val: 100, obs: true})
	if len(s.MatterAttributes()) == 0 {
		t.Error("MatterAttributes: want non-empty")
	}
}

// ─── TemperatureServer MatterAttributes ────────────────────────────────────

func TestTemperatureServer_MatterAttributes_NonEmpty(t *testing.T) {
	t.Parallel()
	s := measurement.NewTemperatureServer(fakeFloat{val: 21.5, obs: true})
	attrs := s.MatterAttributes()
	if len(attrs) == 0 {
		t.Error("MatterAttributes: want non-empty")
	}
}

// ─── FromMeasurementClass ─────────────────────────────────────────────────

// TestFromMeasurementClass_All verifies that every handled class
// returns at least one server. The function covers branches that are only
// hit via FromMeasurementClass, not via the individual server constructors.
func TestFromMeasurementClass_Temperature(t *testing.T) {
	t.Parallel()
	src := fakeFloat{class: interfaces.MatterMeasurementTemperature, val: 20, obs: true}
	got := measurement.FromMeasurementClass(interfaces.MatterMeasurementTemperature, src)
	if len(got) == 0 {
		t.Error("FromMeasurementClass(Temperature): want non-empty")
	}
}

func TestFromMeasurementClass_Humidity(t *testing.T) {
	t.Parallel()
	src := fakeFloat{class: interfaces.MatterMeasurementHumidity, val: 50, obs: true}
	got := measurement.FromMeasurementClass(interfaces.MatterMeasurementHumidity, src)
	if len(got) == 0 {
		t.Error("FromMeasurementClass(Humidity): want non-empty")
	}
}

func TestFromMeasurementClass_Illuminance(t *testing.T) {
	t.Parallel()
	src := fakeFloat{class: interfaces.MatterMeasurementIlluminance, val: 100, obs: true}
	got := measurement.FromMeasurementClass(interfaces.MatterMeasurementIlluminance, src)
	if len(got) == 0 {
		t.Error("FromMeasurementClass(Illuminance): want non-empty")
	}
}

func TestFromMeasurementClass_Pressure(t *testing.T) {
	t.Parallel()
	src := fakeFloat{class: interfaces.MatterMeasurementPressure, val: 1013, obs: true}
	got := measurement.FromMeasurementClass(interfaces.MatterMeasurementPressure, src)
	if len(got) == 0 {
		t.Error("FromMeasurementClass(Pressure): want non-empty")
	}
}

func TestFromMeasurementClass_Contact(t *testing.T) {
	t.Parallel()
	src := fakeBool{class: interfaces.MatterMeasurementContact, val: true, obs: true}
	got := measurement.FromMeasurementClass(interfaces.MatterMeasurementContact, src)
	if len(got) == 0 {
		t.Error("FromMeasurementClass(Contact): want non-empty")
	}
}

func TestFromMeasurementClass_Leak(t *testing.T) {
	t.Parallel()
	src := fakeBool{class: interfaces.MatterMeasurementLeak, val: false, obs: true}
	got := measurement.FromMeasurementClass(interfaces.MatterMeasurementLeak, src)
	if len(got) == 0 {
		t.Error("FromMeasurementClass(Leak): want non-empty")
	}
}

func TestFromMeasurementClass_Occupancy(t *testing.T) {
	t.Parallel()
	src := fakeBool{class: interfaces.MatterMeasurementOccupancy, val: false, obs: true}
	got := measurement.FromMeasurementClass(interfaces.MatterMeasurementOccupancy, src)
	if len(got) == 0 {
		t.Error("FromMeasurementClass(Occupancy): want non-empty")
	}
}

func TestFromMeasurementClass_Battery(t *testing.T) {
	t.Parallel()
	src := fakeBool{class: interfaces.MatterMeasurementBattery, val: false, obs: true}
	got := measurement.FromMeasurementClass(interfaces.MatterMeasurementBattery, src)
	if len(got) == 0 {
		t.Error("FromMeasurementClass(Battery): want non-empty")
	}
}

func TestFromMeasurementClass_Power(t *testing.T) {
	t.Parallel()
	src := fakeFloat{class: interfaces.MatterMeasurementPower, val: 500, obs: true}
	got := measurement.FromMeasurementClass(interfaces.MatterMeasurementPower, src)
	if len(got) == 0 {
		t.Error("FromMeasurementClass(Power): want non-empty")
	}
}

func TestFromMeasurementClass_Energy(t *testing.T) {
	t.Parallel()
	src := fakeFloat{class: interfaces.MatterMeasurementEnergy, val: 10, obs: true}
	got := measurement.FromMeasurementClass(interfaces.MatterMeasurementEnergy, src)
	if len(got) == 0 {
		t.Error("FromMeasurementClass(Energy): want non-empty")
	}
}

func TestFromMeasurementClass_None_ReturnsNil(t *testing.T) {
	t.Parallel()
	got := measurement.FromMeasurementClass(interfaces.MatterMeasurementNone, nil)
	if len(got) != 0 {
		t.Errorf("FromMeasurementClass(None): want nil, got %v", got)
	}
}

func TestFromMeasurementClass_MomentarySwitch_ReturnsNil(t *testing.T) {
	t.Parallel()
	got := measurement.FromMeasurementClass(interfaces.MatterMeasurementMomentarySwitch, nil)
	if len(got) != 0 {
		t.Errorf("FromMeasurementClass(MomentarySwitch): want nil, got %v", got)
	}
}

// WrongTypeSrc tests: passing a wrong source type should return nil.
func TestFromMeasurementClass_WrongType_ReturnsNil(t *testing.T) {
	t.Parallel()
	// Passing a fakeBool (BoolMeasurementSource) for a float class.
	src := fakeBool{class: interfaces.MatterMeasurementTemperature}
	got := measurement.FromMeasurementClass(interfaces.MatterMeasurementTemperature, src)
	if len(got) != 0 {
		t.Errorf("FromMeasurementClass(Temperature, boolSrc): want nil, got %v", got)
	}
}

// --- PowerSourceServer EndpointList (0x001F) ---

// TestPowerSourceServer_EndpointList verifies the three states of
// EndpointList (0x001F): unset → empty list; after SetEndpoint(n>0) →
// single-element list; after SetEndpoint(0) → empty list again.
// Matter §11.7.6.20 permits an empty list when the endpoint is unspecified.
func TestPowerSourceServer_EndpointList(t *testing.T) {
	t.Parallel()
	src := fakeBool{class: interfaces.MatterMeasurementBattery, val: false, obs: true}

	t.Run("default/unset returns empty list", func(t *testing.T) {
		t.Parallel()
		s := measurement.NewPowerSourceServer(src)
		v, ok := s.MatterRead(0x001F)
		if !ok {
			t.Fatal("MatterRead(0x001F) ok = false, want true")
		}
		list, cast := v.([]uint16)
		if !cast {
			t.Fatalf("EndpointList type = %T, want []uint16", v)
		}
		if len(list) != 0 {
			t.Errorf("EndpointList = %v, want empty (Matter §11.7.6.20 unspecified)", list)
		}
	})

	t.Run("SetEndpoint(3) returns [3]", func(t *testing.T) {
		t.Parallel()
		s := measurement.NewPowerSourceServer(src)
		s.SetEndpoint(3)
		v, ok := s.MatterRead(0x001F)
		if !ok {
			t.Fatal("MatterRead(0x001F) ok = false, want true")
		}
		list, cast := v.([]uint16)
		if !cast {
			t.Fatalf("EndpointList type = %T, want []uint16", v)
		}
		if len(list) != 1 || list[0] != 3 {
			t.Errorf("EndpointList = %v, want [3]", list)
		}
	})

	t.Run("SetEndpoint(0) after SetEndpoint(3) returns empty list", func(t *testing.T) {
		t.Parallel()
		s := measurement.NewPowerSourceServer(src)
		s.SetEndpoint(3)
		s.SetEndpoint(0)
		v, ok := s.MatterRead(0x001F)
		if !ok {
			t.Fatal("MatterRead(0x001F) ok = false, want true")
		}
		list, cast := v.([]uint16)
		if !cast {
			t.Fatalf("EndpointList type = %T, want []uint16", v)
		}
		if len(list) != 0 {
			t.Errorf("EndpointList = %v, want empty after SetEndpoint(0)", list)
		}
	})
}
