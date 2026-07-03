// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package climate

import (
	"context"
	"errors"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/internal/north/matter/im"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/interfaces"
)

// findCluster returns the cluster server with the given ID.
func findCluster(t *testing.T, c *Climate, id uint32) interfaces.MatterClusterServer {
	t.Helper()
	for _, s := range c.MatterClusterServers() {
		if s.MatterClusterID() == id {
			return s
		}
	}
	t.Fatalf("cluster 0x%04X not present in projection", id)
	return nil
}

// TestMatterDeviceTypeIsThermostat locks the Thermostat (0x0301)
// device-type advertisement.
func TestMatterDeviceTypeIsThermostat(t *testing.T) {
	r := newRig(t, "HmIP-BWTH:1", KindIP, &stubWriter{}, custom.ClimateCapabilities{})
	if got := r.climate.MatterDeviceType(); got != 0x0301 {
		t.Fatalf("MatterDeviceType = 0x%04X, want 0x0301", got)
	}
}

// TestClimateClusterCompositionWithHumidity confirms the four-cluster
// projection on a Climate that has a HUMIDITY DP: Thermostat (0x0201),
// ThermostatUIConfiguration (0x0204), TemperatureMeasurement (0x0402),
// and RelativeHumidityMeasurement (0x0405). The Schedules cluster
// (0x0024) is intentionally absent — matter.js MatterDefinition does
// not include it, and Apple Home pair-aborts when it is advertised.
func TestClimateClusterCompositionWithHumidity(t *testing.T) {
	r := newRig(t, "HmIP-BWTH:1", KindIP, &stubWriter{}, custom.ClimateCapabilities{})
	servers := r.climate.MatterClusterServers()
	got := map[uint32]bool{}
	for _, s := range servers {
		got[s.MatterClusterID()] = true
	}
	want := []uint32{0x0201, 0x0204, 0x0402, 0x0405}
	for _, id := range want {
		if !got[id] {
			t.Errorf("cluster 0x%04X missing from %v", id, got)
		}
	}
	if got[0x0024] {
		t.Errorf("Schedules cluster (0x0024) must not be emitted")
	}
	if len(servers) != 4 {
		t.Errorf("expected 4 cluster servers, got %d", len(servers))
	}
}

// TestClimateClusterCompositionWithoutHumidity asserts the conditional
// emission of RelativeHumidityMeasurement: when the Climate has no
// humidity DP, the cluster is absent from the projection. The Schedules
// cluster (0x0024) is intentionally absent in all cases.
// This is the rich-model way to keep Matter advertising honest.
func TestClimateClusterCompositionWithoutHumidity(t *testing.T) {
	r := newRig(t, "HmIP-eTRV:1", KindIP, &stubWriter{}, custom.ClimateCapabilities{})
	// Detach humidity to simulate a TRV without humidity sensor.
	r.climate.humidity = nil
	servers := r.climate.MatterClusterServers()
	for _, s := range servers {
		if s.MatterClusterID() == 0x0405 {
			t.Fatalf("RelativeHumidityMeasurement must not be emitted when humidity DP is absent")
		}
	}
	if len(servers) != 3 {
		t.Errorf("expected 3 cluster servers (no humidity, no Schedules), got %d", len(servers))
	}
}

// TestThermostatLocalTemperatureEncoding round-trips ACTUAL_TEMPERATURE
// through the int16 0.01°C encoding.
func TestThermostatLocalTemperatureEncoding(t *testing.T) {
	r := newRig(t, "HmIP-BWTH:1", KindIP, &stubWriter{}, custom.ClimateCapabilities{})
	r.actualTemperature.OnEvent(21.5)
	srv := findCluster(t, r.climate, 0x0201)
	v, ok := srv.MatterRead(0x0000)
	if !ok || v.(int16) != 2150 {
		t.Fatalf("LocalTemperature = (%v, %v), want (2150, true)", v, ok)
	}
}

// TestThermostatLocalTemperatureUnobserved ensures stale reads
// surface (nil, true) — attribute is supported but value is transiently null.
// (nil, false) would signal UnsupportedAttribute to the HAP mapper and abort
// the HAP service build with HAPErrorDomain Code=24.
func TestThermostatLocalTemperatureUnobserved(t *testing.T) {
	r := newRig(t, "HmIP-BWTH:1", KindIP, &stubWriter{}, custom.ClimateCapabilities{})
	srv := findCluster(t, r.climate, 0x0201)
	v, ok := srv.MatterRead(0x0000)
	if !ok || v != nil {
		t.Fatalf("LocalTemperature on unobserved = (%v, %v), want (nil, true)", v, ok)
	}
}

// TestThermostatHeatingSetpointReadAndWrite covers the round-trip on
// OccupiedHeatingSetpoint: read returns the current setpoint *100,
// write decodes back through SetTemperature.
func TestThermostatHeatingSetpointReadAndWrite(t *testing.T) {
	w := &stubWriter{}
	r := newRig(t, "HmIP-BWTH:1", KindIP, w, custom.ClimateCapabilities{
		MinTemperature: 4.5,
		MaxTemperature: 30.5,
	})
	r.setpoint.OnEvent(20.0)
	srv := findCluster(t, r.climate, 0x0201)
	v, ok := srv.MatterRead(0x0012)
	if !ok || v.(int16) != 2000 {
		t.Fatalf("OccupiedHeatingSetpoint = (%v, %v), want (2000, true)", v, ok)
	}

	if err := srv.MatterWrite(context.Background(), 0x0012, int16(2150), hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("MatterWrite(setpoint=21.5) err: %v", err)
	}
	if got := w.last(); got.value.(float64) != 21.5 {
		t.Fatalf("setpoint write reached wire as %v, want 21.5", got.value)
	}
}

// TestThermostatMinMaxHeatSetpointFromCapabilities reads the
// configurator-supplied limits. Note: Climate.MinTemp() has the
// off-temperature special case (4.5 °C → 5.0 °C bump) that the
// projection inherits — using 5.5 °C here keeps the test focused
// on the encoding rather than the off-shift behaviour.
func TestThermostatMinMaxHeatSetpointFromCapabilities(t *testing.T) {
	r := newRig(t, "HmIP-BWTH:1", KindIP, &stubWriter{}, custom.ClimateCapabilities{
		MinTemperature: 5.5,
		MaxTemperature: 30.5,
	})
	srv := findCluster(t, r.climate, 0x0201)
	mn, _ := srv.MatterRead(0x0015) // MinHeatSetpointLimit
	if mn.(int16) != 550 {
		t.Errorf("MinHeatSetpointLimit = %d, want 550", mn.(int16))
	}
	mx, _ := srv.MatterRead(0x0016) // MaxHeatSetpointLimit
	if mx.(int16) != 3050 {
		t.Errorf("MaxHeatSetpointLimit = %d, want 3050", mx.(int16))
	}
}

// TestThermostatControlSequenceHeatingOnly locks the heating-only
// projection — the worked example treats every HM Climate as a heating
// device. M6 widens this for cooling-capable profiles.
func TestThermostatControlSequenceHeatingOnly(t *testing.T) {
	r := newRig(t, "HmIP-BWTH:1", KindIP, &stubWriter{}, custom.ClimateCapabilities{})
	srv := findCluster(t, r.climate, 0x0201)
	v, _ := srv.MatterRead(0x001B)
	if v.(uint8) != matterCtrlSeqHeatingOnly {
		t.Fatalf("ControlSequenceOfOperation = %d, want 2 (HeatingOnly)", v.(uint8))
	}
}

// TestThermostatFeatureMapHeatOnly confirms the FeatureMap advertises
// only HEAT for heating-only HmIP devices. AUTO requires both HEAT and
// COOL (spec conformance "AUTO, O.a+"), so it must not be set here.
func TestThermostatFeatureMapHeatOnly(t *testing.T) {
	r := newRig(t, "HmIP-BWTH:1", KindIP, &stubWriter{}, custom.ClimateCapabilities{})
	srv := findCluster(t, r.climate, 0x0201)
	v, _ := srv.MatterRead(0xFFFC)
	got := v.(uint32)
	if got&matterThermFeatureHeat == 0 {
		t.Errorf("FeatureMap = 0x%08X, missing HEAT bit", got)
	}
	if got&matterThermFeatureCool != 0 {
		t.Errorf("FeatureMap = 0x%08X, must not advertise COOL on heating-only", got)
	}
	if got&matterThermFeatureAuto != 0 {
		t.Errorf("FeatureMap = 0x%08X, must not advertise AUTO without COOL", got)
	}
}

// TestThermostatHeatOnlyExcludesCoolAndAutoAttributes locks the
// conformance-gated MatterAttributes() surface for a heating-only
// device: OccupiedCoolingSetpoint (0x11), MinCoolSetpointLimit (0x17),
// MaxCoolSetpointLimit (0x18) require the COOL feature
// (thermostat-cluster.element.ts:66-68, 92-98) and ThermostatRunningMode
// (0x1e) requires AUTO (thermostat-cluster.element.ts:117-120) — none of
// which a heat-only FeatureMap advertises.
func TestThermostatHeatOnlyExcludesCoolAndAutoAttributes(t *testing.T) {
	r := newRig(t, "HmIP-BWTH:1", KindIP, &stubWriter{}, custom.ClimateCapabilities{})
	srv := findCluster(t, r.climate, 0x0201)
	lister, ok := srv.(interfaces.MatterClusterAttributeLister)
	if !ok {
		t.Fatal("Thermostat server does not implement MatterClusterAttributeLister")
	}
	attrs := lister.MatterAttributes()
	forbidden := map[uint32]string{
		matterAttrThermOccupiedCoolSp: "OccupiedCoolingSetpoint",
		matterAttrThermMinCoolSp:      "MinCoolSetpointLimit",
		matterAttrThermMaxCoolSp:      "MaxCoolSetpointLimit",
		matterAttrThermRunningMode:    "ThermostatRunningMode",
	}
	for _, id := range attrs {
		if name, bad := forbidden[id]; bad {
			t.Errorf("MatterAttributes() lists %s (0x%04X) on a heating-only device", name, id)
		}
	}
}

// TestThermostatHeatOnlyCoolSetpointNotPresent asserts that reading
// OccupiedCoolingSetpoint (0x11) on a heating-only device reports
// not-present (ok=false) rather than leaking the shared HM setpoint —
// the attribute does not exist on the cluster without the COOL feature.
func TestThermostatHeatOnlyCoolSetpointNotPresent(t *testing.T) {
	r := newRig(t, "HmIP-BWTH:1", KindIP, &stubWriter{}, custom.ClimateCapabilities{})
	r.setpoint.OnEvent(20.0)
	srv := findCluster(t, r.climate, 0x0201)
	if _, ok := srv.MatterRead(matterAttrThermOccupiedCoolSp); ok {
		t.Error("MatterRead(OccupiedCoolingSetpoint) on a heating-only device should report not-present")
	}
	if _, ok := srv.MatterRead(matterAttrThermMinCoolSp); ok {
		t.Error("MatterRead(MinCoolSetpointLimit) on a heating-only device should report not-present")
	}
	if _, ok := srv.MatterRead(matterAttrThermMaxCoolSp); ok {
		t.Error("MatterRead(MaxCoolSetpointLimit) on a heating-only device should report not-present")
	}
}

// TestThermostatSystemModeWriteCoolRejectedHeatOnly asserts that writing
// SystemMode=Cool on a heating-only device is rejected with a
// ConstraintError rather than silently retargeting the single HM
// heating setpoint. Mirrors matter.js
// ThermostatServer.ts:#assertSystemModeChanging (lines 615-632).
func TestThermostatSystemModeWriteCoolRejectedHeatOnly(t *testing.T) {
	r := newRig(t, "HmIP-BWTH:1", KindIP, &stubWriter{}, custom.ClimateCapabilities{})
	srv := findCluster(t, r.climate, 0x0201)
	err := srv.MatterWrite(context.Background(), matterAttrThermSystemMode, matterSysModeCool, hmenum.CommandPriorityHigh)
	if err == nil {
		t.Fatal("MatterWrite SystemMode=Cool on a heating-only device should be rejected")
	}
	sc, ok := err.(interface{ MatterStatusCode() im.StatusCode })
	if !ok {
		t.Fatalf("error %v does not implement MatterStatusCode()", err)
	}
	if sc.MatterStatusCode() != im.StatusConstraintError {
		t.Errorf("MatterStatusCode() = 0x%02X, want StatusConstraintError (0x87)", sc.MatterStatusCode())
	}
}

// TestThermostatCoolCapableDeviceAdvertisesCoolAndAutoAttributes proves
// the gating keys off the FeatureMap rather than a hardcoded exclusion:
// a Climate whose Capabilities advertise SupportsCool (and therefore, by
// [climateThermostatServer.featureMap], HEAT+COOL+AUTO) must list and
// serve the Cool-setpoint and RunningMode attributes, and must accept a
// SystemMode=Auto write.
func TestThermostatCoolCapableDeviceAdvertisesCoolAndAutoAttributes(t *testing.T) {
	r := newRig(t, "HmIP-BWTH:1", KindIP, &stubWriter{}, custom.ClimateCapabilities{SupportsCool: true})
	srv := findCluster(t, r.climate, 0x0201)

	fm, ok := srv.MatterRead(matterAttrFeatureMap)
	if !ok || fm.(uint32)&(matterThermFeatureHeat|matterThermFeatureCool|matterThermFeatureAuto) !=
		matterThermFeatureHeat|matterThermFeatureCool|matterThermFeatureAuto {
		t.Fatalf("FeatureMap = (%v, %v), want HEAT|COOL|AUTO", fm, ok)
	}

	lister, ok := srv.(interfaces.MatterClusterAttributeLister)
	if !ok {
		t.Fatal("Thermostat server does not implement MatterClusterAttributeLister")
	}
	attrs := lister.MatterAttributes()
	want := map[uint32]string{
		matterAttrThermOccupiedCoolSp: "OccupiedCoolingSetpoint",
		matterAttrThermMinCoolSp:      "MinCoolSetpointLimit",
		matterAttrThermMaxCoolSp:      "MaxCoolSetpointLimit",
		matterAttrThermRunningMode:    "ThermostatRunningMode",
	}
	got := make(map[uint32]bool, len(attrs))
	for _, id := range attrs {
		got[id] = true
	}
	for id, name := range want {
		if !got[id] {
			t.Errorf("MatterAttributes() missing %s (0x%04X) on a heat+cool device", name, id)
		}
		if _, ok := srv.MatterRead(id); !ok {
			t.Errorf("MatterRead(%s / 0x%04X) reports not-present on a heat+cool device", name, id)
		}
	}

	if err := srv.MatterWrite(context.Background(), matterAttrThermSystemMode, matterSysModeAuto, hmenum.CommandPriorityHigh); err != nil {
		t.Errorf("SystemMode=Auto write on a heat+cool device should be accepted, got: %v", err)
	}
}

// TestThermostatSystemModeReadFromHmModeAuto routes the Climate's Auto
// mode through to Matter SystemMode=1.
func TestThermostatSystemModeReadFromHmModeAuto(t *testing.T) {
	r := newRig(t, "HmIP-BWTH:1", KindIP, &stubWriter{}, custom.ClimateCapabilities{})
	r.climate.mu.Lock()
	r.climate.mode = ModeAuto
	r.climate.hasMode = true
	r.climate.mu.Unlock()
	srv := findCluster(t, r.climate, 0x0201)
	v, ok := srv.MatterRead(0x001C)
	if !ok || v.(uint8) != matterSysModeAuto {
		t.Fatalf("SystemMode = (%v, %v), want (1, true)", v, ok)
	}
}

// TestThermostatSystemModeWriteRoundTrip writes Heat (4) and asserts
// Climate.SetMode is invoked with ModeHeat.
func TestThermostatSystemModeWriteRoundTrip(t *testing.T) {
	w := &stubWriter{}
	r := newRig(t, "HmIP-BWTH:1", KindIP, w, custom.ClimateCapabilities{})
	srv := findCluster(t, r.climate, 0x0201)
	if err := srv.MatterWrite(context.Background(), 0x001C, matterSysModeHeat, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("SystemMode write err: %v", err)
	}
	// SetMode on KindIP writes CONTROL_MODE=1 (MANUAL) for ModeHeat.
	// We assert the chain reached the writer at all rather than the
	// HM-specific parameter shape — that's what the existing climate
	// tests cover.
	if len(w.calls) == 0 {
		t.Fatal("SystemMode=Heat write did not reach the wire")
	}
}

// TestThermostatSystemModeUnsupportedRejected rejects e.g. SystemMode=7
// (FanOnly) which has no HM equivalent.
func TestThermostatSystemModeUnsupportedRejected(t *testing.T) {
	r := newRig(t, "HmIP-BWTH:1", KindIP, &stubWriter{}, custom.ClimateCapabilities{})
	srv := findCluster(t, r.climate, 0x0201)
	err := srv.MatterWrite(context.Background(), 0x001C, uint8(7), hmenum.CommandPriorityHigh)
	if !errors.Is(err, errMatterUnsupportedMode) {
		t.Fatalf("err = %v, want errMatterUnsupportedMode", err)
	}
}

// TestThermostatSetpointRaiseLowerCommand exercises the delta-style
// command shape: amount is in 0.1°C; positive raises.
func TestThermostatSetpointRaiseLowerCommand(t *testing.T) {
	w := &stubWriter{}
	r := newRig(t, "HmIP-BWTH:1", KindIP, w, custom.ClimateCapabilities{
		MinTemperature: 4.5,
		MaxTemperature: 30.5,
	})
	r.setpoint.OnEvent(20.0)
	srv := findCluster(t, r.climate, 0x0201)
	fields := map[string]any{"mode": uint8(0), "amount": int8(15)} // +1.5 °C
	_, err := srv.MatterInvoke(context.Background(), 0x00, fields, hmenum.CommandPriorityHigh)
	if err != nil {
		t.Fatalf("SetpointRaiseLower err: %v", err)
	}
	if got := w.last(); got.value.(float64) != 21.5 {
		t.Fatalf("raise reached wire as %v, want 21.5", got.value)
	}
}

// TestThermostatSetpointRaiseLowerWithoutBaselineFails refuses the
// command when the setpoint has not been observed — the bridge will
// translate the resulting error to FAILURE.
func TestThermostatSetpointRaiseLowerWithoutBaselineFails(t *testing.T) {
	r := newRig(t, "HmIP-BWTH:1", KindIP, &stubWriter{}, custom.ClimateCapabilities{
		MinTemperature: 4.5,
		MaxTemperature: 30.5,
	})
	srv := findCluster(t, r.climate, 0x0201)
	fields := map[string]any{"mode": uint8(0), "amount": int8(15)}
	_, err := srv.MatterInvoke(context.Background(), 0x00, fields, hmenum.CommandPriorityHigh)
	if err == nil {
		t.Fatalf("SetpointRaiseLower without baseline should fail")
	}
}

// TestThermostatUITempDisplayModeIsCelsius locks the static-Celsius
// projection.
func TestThermostatUITempDisplayModeIsCelsius(t *testing.T) {
	r := newRig(t, "HmIP-BWTH:1", KindIP, &stubWriter{}, custom.ClimateCapabilities{})
	srv := findCluster(t, r.climate, 0x0204)
	v, ok := srv.MatterRead(0x0000)
	if !ok || v.(uint8) != matterTempDisplayCelsius {
		t.Fatalf("TempDisplayMode = (%v, %v), want (0=Celsius, true)", v, ok)
	}
}

// TestTempMeasurementMirrorsLocalTemperature confirms the
// TemperatureMeasurement cluster surfaces the same value as Thermostat
// LocalTemperature.
func TestTempMeasurementMirrorsLocalTemperature(t *testing.T) {
	r := newRig(t, "HmIP-BWTH:1", KindIP, &stubWriter{}, custom.ClimateCapabilities{})
	r.actualTemperature.OnEvent(19.25)
	srv := findCluster(t, r.climate, 0x0402)
	v, ok := srv.MatterRead(0x0000)
	if !ok || v.(int16) != 1925 {
		t.Fatalf("TempMeas MeasuredValue = (%v, %v), want (1925, true)", v, ok)
	}
}

// TestRelativeHumidityEncoding round-trips HUMIDITY through the
// uint16 0.01% encoding.
func TestRelativeHumidityEncoding(t *testing.T) {
	r := newRig(t, "HmIP-BWTH:1", KindIP, &stubWriter{}, custom.ClimateCapabilities{})
	r.humidity.OnEvent(45.5)
	srv := findCluster(t, r.climate, 0x0405)
	v, ok := srv.MatterRead(0x0000)
	if !ok || v.(uint16) != 4550 {
		t.Fatalf("Humidity MeasuredValue = (%v, %v), want (4550, true)", v, ok)
	}
}

// TestRelativeHumiditySaturation locks the clamp at 100 % → 10000.
func TestRelativeHumiditySaturation(t *testing.T) {
	r := newRig(t, "HmIP-BWTH:1", KindIP, &stubWriter{}, custom.ClimateCapabilities{})
	r.humidity.OnEvent(105) // out-of-range CCU value
	srv := findCluster(t, r.climate, 0x0405)
	v, _ := srv.MatterRead(0x0000)
	if v.(uint16) != 10000 {
		t.Fatalf("Humidity 105 %% → Matter %d, want 10000", v.(uint16))
	}
}

// TestSetpointWriteWrongTypeRejected — defence-in-depth against bridge
// regressions.
func TestSetpointWriteWrongTypeRejected(t *testing.T) {
	r := newRig(t, "HmIP-BWTH:1", KindIP, &stubWriter{}, custom.ClimateCapabilities{
		MinTemperature: 4.5,
		MaxTemperature: 30.5,
	})
	srv := findCluster(t, r.climate, 0x0201)
	err := srv.MatterWrite(context.Background(), 0x0012, "21.5", hmenum.CommandPriorityHigh)
	if !errors.Is(err, errMatterValueType) {
		t.Fatalf("err = %v, want errMatterValueType", err)
	}
}

// TestThermostatClusterRevisionIs7 locks the ClusterRevision bump to 7
// introduced with Matter 1.4 mandatory attributes.
func TestThermostatClusterRevisionIs7(t *testing.T) {
	r := newRig(t, "HmIP-BWTH:1", KindIP, &stubWriter{}, custom.ClimateCapabilities{})
	srv := findCluster(t, r.climate, 0x0201)
	v, ok := srv.MatterRead(0xFFFD)
	if !ok || v.(uint16) != matterThermClusterRevision {
		t.Fatalf("ClusterRevision = (%v, %v), want (%d, true)", v, ok, matterThermClusterRevision)
	}
}

// TestThermostatNoFabricatedAttrAt0x30 confirms the bridge does not
// advertise a fabricated attribute at 0x0030 — that ID is
// SetpointChangeSource in matter.js; LocalTemperatureNotExposed is
// FeatureMap bit 6, not an attribute.
func TestThermostatNoFabricatedAttrAt0x30(t *testing.T) {
	r := newRig(t, "HmIP-BWTH:1", KindIP, &stubWriter{}, custom.ClimateCapabilities{})
	srv := findCluster(t, r.climate, 0x0201)
	if _, ok := srv.MatterRead(0x0030); ok {
		t.Fatalf("0x0030 must not be readable (no fabricated LocalTemperatureNotExposed attribute)")
	}
	for _, id := range srv.MatterReportable() {
		if id == 0x0030 {
			t.Fatalf("0x0030 must not appear in MatterReportable")
		}
	}
}

// TestUnknownCommandsRejected — uniform UNSUPPORTED_COMMAND surface
// across every cluster server in the projection.
func TestUnknownCommandsRejected(t *testing.T) {
	r := newRig(t, "HmIP-BWTH:1", KindIP, &stubWriter{}, custom.ClimateCapabilities{})
	for _, id := range []uint32{0x0201, 0x0204, 0x0402, 0x0405} {
		srv := findCluster(t, r.climate, id)
		_, err := srv.MatterInvoke(context.Background(), 0x99, nil, hmenum.CommandPriorityHigh)
		if !errors.Is(err, errMatterUnknownCommand) {
			t.Errorf("cluster 0x%04X: err=%v, want errMatterUnknownCommand", id, err)
		}
	}
}
