// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package climate

import (
	"context"
	"errors"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/internal/north/matter/im"
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

	if err := srv.MatterWrite(context.Background(), 0x0012, int16(2150)); err != nil {
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
	err := srv.MatterWrite(context.Background(), matterAttrThermSystemMode, matterSysModeCool)
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

// TestThermostatCoolCapableDeviceAdvertisesCoolWithoutAuto proves the
// COOL gating keys off the FeatureMap while AUTO stays off even on a
// cooling-capable profile: a Climate whose Capabilities advertise
// SupportsCool lists and serves the Cool-setpoint attributes, but the
// FeatureMap must not carry AUTO (HM climates are single-setpoint and
// the projection implements no MinSetpointDeadBand, which AUTO
// mandates), so ThermostatRunningMode stays absent and a
// SystemMode=Auto write is rejected with ConstraintError.
func TestThermostatCoolCapableDeviceAdvertisesCoolWithoutAuto(t *testing.T) {
	r := newRig(t, "HmIP-BWTH:1", KindIP, &stubWriter{}, custom.ClimateCapabilities{SupportsCool: true})
	srv := findCluster(t, r.climate, 0x0201)

	fm, ok := srv.MatterRead(matterAttrFeatureMap)
	if !ok || fm.(uint32)&(matterThermFeatureHeat|matterThermFeatureCool) !=
		matterThermFeatureHeat|matterThermFeatureCool {
		t.Fatalf("FeatureMap = (%v, %v), want HEAT|COOL", fm, ok)
	}
	if fm.(uint32)&matterThermFeatureAuto != 0 {
		t.Fatalf("FeatureMap = 0x%08X advertises AUTO on a single-setpoint device without MinSetpointDeadBand", fm)
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
	if got[matterAttrThermRunningMode] {
		t.Error("MatterAttributes() lists ThermostatRunningMode (0x001E) without the AUTO feature")
	}
	if _, ok := srv.MatterRead(matterAttrThermRunningMode); ok {
		t.Error("MatterRead(ThermostatRunningMode) should report not-present without the AUTO feature")
	}

	err := srv.MatterWrite(context.Background(), matterAttrThermSystemMode, matterSysModeAuto)
	var sc im.StatusCodeError
	if !errors.As(err, &sc) || sc.MatterStatusCode() != im.StatusConstraintError {
		t.Errorf("SystemMode=Auto write without the AUTO feature = %v, want ConstraintError", err)
	}
}

// TestThermostatSystemModeReadClampsHmAutoToFeatureMap asserts the
// SystemMode read never surfaces Auto(1) when the FeatureMap does not
// advertise the AUTO feature — Auto has conformance "AUTO" per matter.js
// packages/model/src/standard/elements/thermostat-cluster.element.ts:558.
// HM's factory-default AUTO (week-program) mode is a single-setpoint
// schedule and projects onto Heat, or Cool when a hybrid device is
// currently in its COOLING direction. The clamp keeps reads and the
// write gate consistent: a controller echoing the read value back on
// state sync must not receive ConstraintError.
func TestThermostatSystemModeReadClampsHmAutoToFeatureMap(t *testing.T) {
	setAutoMode := func(c *Climate) {
		c.mu.Lock()
		c.mode = ModeAuto
		c.hasMode = true
		c.mu.Unlock()
	}

	t.Run("heat-only device surfaces Heat", func(t *testing.T) {
		w := &stubWriter{}
		r := newRig(t, "HmIP-BWTH:1", KindIP, w, custom.ClimateCapabilities{})
		setAutoMode(r.climate)
		srv := findCluster(t, r.climate, 0x0201)
		v, ok := srv.MatterRead(matterAttrThermSystemMode)
		if !ok || v.(uint8) != matterSysModeHeat {
			t.Fatalf("SystemMode = (%v, %v), want (4 Heat, true)", v, ok)
		}
		// The read value must survive the write gate when echoed back.
		if err := srv.MatterWrite(context.Background(), matterAttrThermSystemMode, v); err != nil {
			t.Fatalf("echoing the read SystemMode back must not error, got: %v", err)
		}
	})

	t.Run("cool-capable device in cooling direction surfaces Cool", func(t *testing.T) {
		r := newRig(t, "HmIP-BWTH:1", KindIP, &stubWriter{}, custom.ClimateCapabilities{SupportsCool: true})
		setAutoMode(r.climate)
		r.climate.OnHeatingCooling("COOLING")
		srv := findCluster(t, r.climate, 0x0201)
		v, ok := srv.MatterRead(matterAttrThermSystemMode)
		if !ok || v.(uint8) != matterSysModeCool {
			t.Fatalf("SystemMode = (%v, %v), want (3 Cool, true)", v, ok)
		}
	})

	t.Run("cool-capable device in heating direction surfaces Heat", func(t *testing.T) {
		r := newRig(t, "HmIP-BWTH:1", KindIP, &stubWriter{}, custom.ClimateCapabilities{SupportsCool: true})
		setAutoMode(r.climate)
		srv := findCluster(t, r.climate, 0x0201)
		v, ok := srv.MatterRead(matterAttrThermSystemMode)
		if !ok || v.(uint8) != matterSysModeHeat {
			t.Fatalf("SystemMode = (%v, %v), want (4 Heat, true)", v, ok)
		}
	})
}

// TestThermostatRunningModeValueNeverAuto pins the value space of the
// RunningMode projection to ThermostatRunningModeEnum, which has no
// Auto member — Off(0)/Cool(3)/Heat(4) only, matter.js
// packages/model/src/standard/elements/thermostat-cluster.element.ts:568-573.
// HM's AUTO mode maps onto the active HEATING_COOLING direction.
func TestThermostatRunningModeValueNeverAuto(t *testing.T) {
	r := newRig(t, "HmIP-BWTH:1", KindIP, &stubWriter{}, custom.ClimateCapabilities{SupportsCool: true})
	srv := climateThermostatServer{c: r.climate}

	if got := srv.runningModeFromHmMode(ModeAuto); got != matterRunningModeHeat {
		t.Errorf("runningModeFromHmMode(ModeAuto) heating direction = %d, want 4 (Heat)", got)
	}
	r.climate.OnHeatingCooling("COOLING")
	if got := srv.runningModeFromHmMode(ModeAuto); got != matterRunningModeCool {
		t.Errorf("runningModeFromHmMode(ModeAuto) cooling direction = %d, want 3 (Cool)", got)
	}
	for _, c := range []struct {
		mode Mode
		want uint8
	}{
		{ModeOff, matterRunningModeOff},
		{ModeHeat, matterRunningModeHeat},
		{ModeCool, matterRunningModeCool},
	} {
		if got := srv.runningModeFromHmMode(c.mode); got != c.want {
			t.Errorf("runningModeFromHmMode(%s) = %d, want %d", c.mode, got, c.want)
		}
	}
}

// TestThermostatSystemModeWriteRoundTrip writes Heat (4) and asserts
// Climate.SetMode is invoked with ModeHeat.
func TestThermostatSystemModeWriteRoundTrip(t *testing.T) {
	w := &stubWriter{}
	r := newRig(t, "HmIP-BWTH:1", KindIP, w, custom.ClimateCapabilities{})
	srv := findCluster(t, r.climate, 0x0201)
	if err := srv.MatterWrite(context.Background(), 0x001C, matterSysModeHeat); err != nil {
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
	err := srv.MatterWrite(context.Background(), 0x001C, uint8(7))
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
	_, err := srv.MatterInvoke(context.Background(), 0x00, fields)
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
	_, err := srv.MatterInvoke(context.Background(), 0x00, fields)
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

// TestSetpointWriteWrongTypeRejected — defence-in-depth against bridge
// regressions.
func TestSetpointWriteWrongTypeRejected(t *testing.T) {
	r := newRig(t, "HmIP-BWTH:1", KindIP, &stubWriter{}, custom.ClimateCapabilities{
		MinTemperature: 4.5,
		MaxTemperature: 30.5,
	})
	srv := findCluster(t, r.climate, 0x0201)
	err := srv.MatterWrite(context.Background(), 0x0012, "21.5")
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
	for _, id := range []uint32{0x0201, 0x0204} {
		srv := findCluster(t, r.climate, id)
		_, err := srv.MatterInvoke(context.Background(), 0x99, nil)
		if !errors.Is(err, errMatterUnknownCommand) {
			t.Errorf("cluster 0x%04X: err=%v, want errMatterUnknownCommand", id, err)
		}
	}
}

// --- OnMatterValueChanged (MatterChangeNotifier) ---

// TestClimateOnMatterValueChangedFiresOnConfirmedSetpointChange verifies
// that a CCU-confirmed setpoint change (e.g. adjusted at the wall dial)
// reaches a registered OnMatterValueChanged callback.
func TestClimateOnMatterValueChangedFiresOnConfirmedSetpointChange(t *testing.T) {
	r := newRig(t, "HmIP-BWTH:1", KindIP, &stubWriter{}, custom.ClimateCapabilities{})
	var count int
	_ = r.climate.OnMatterValueChanged(func() { count++ })
	r.setpoint.OnEvent(20.0)
	r.setpoint.OnEvent(21.5)
	if count != 2 {
		t.Fatalf("expected 2 callback invocations, got %d", count)
	}
}

// TestClimateOnMatterValueChangedUnsubscribeStopsCallback verifies that
// the returned closure detaches the setpoint subscription so a further
// confirmed change does not fire the callback again.
func TestClimateOnMatterValueChangedUnsubscribeStopsCallback(t *testing.T) {
	r := newRig(t, "HmIP-BWTH:1", KindIP, &stubWriter{}, custom.ClimateCapabilities{})
	var count int
	unsub := r.climate.OnMatterValueChanged(func() { count++ })
	r.setpoint.OnEvent(20.0)
	unsub()
	r.setpoint.OnEvent(21.5)
	if count != 1 {
		t.Fatalf("expected 1 callback invocation after unsub, got %d", count)
	}
}

// TestClimateOnMatterValueChangedFansActualTemperature confirms that
// CombineUnsubs fans a second wired DP (ACTUAL_TEMPERATURE) into the
// same callback, not just the setpoint.
func TestClimateOnMatterValueChangedFansActualTemperature(t *testing.T) {
	r := newRig(t, "HmIP-BWTH:1", KindIP, &stubWriter{}, custom.ClimateCapabilities{})
	var count int
	_ = r.climate.OnMatterValueChanged(func() { count++ })
	r.actualTemperature.OnEvent(21.5)
	if count != 1 {
		t.Fatalf("expected 1 callback invocation from actualTemperature change, got %d", count)
	}
}

// TestClimateOnMatterValueChangedNilSafe verifies nil-receiver and
// nil-callback safety.
func TestClimateOnMatterValueChangedNilSafe(t *testing.T) {
	var c *Climate
	unsub := c.OnMatterValueChanged(func() {})
	if unsub == nil {
		t.Fatal("nil Climate: OnMatterValueChanged must return non-nil unsub")
	}
	unsub() // must not panic

	r := newRig(t, "HmIP-BWTH:1", KindIP, &stubWriter{}, custom.ClimateCapabilities{})
	unsub2 := r.climate.OnMatterValueChanged(nil)
	if unsub2 == nil {
		t.Fatal("nil callback: OnMatterValueChanged must return non-nil unsub")
	}
	r.setpoint.OnEvent(20.0) // must not panic with no subscriber
}

// TestClimateClusterCompositionIsThermostatOnly pins the projection to the
// two clusters the Matter Device Library specifies for device type 0x0301 as
// server clusters: Thermostat (0x0201, conformance M) and
// ThermostatUserInterfaceConfiguration (0x0204, conformance O).
//
// Three clusters must NOT appear, each for its own reason:
//
//   - TemperatureMeasurement (0x0402) and RelativeHumidityMeasurement
//     (0x0405) are named for 0x0301 as element=clientCluster — a thermostat
//     consumes those readings from another endpoint rather than serving them
//     (matter.js packages/model/src/standard/elements/thermostat-device.element.ts).
//     They were served here until the device-type conformance guard was
//     built; the readings now reach controllers as their own
//     TemperatureSensor / HumiditySensor endpoints, and Apple reads the
//     temperature from the Thermostat cluster's LocalTemperature either way.
//   - Schedules (0x0024) is not in matter.js's MatterDefinition at all, and
//     Apple Home pair-aborts on an unknown cluster ID.
//
// The count assertion is what makes this a composition test rather than a
// presence test: an extra cluster nobody noticed is exactly the defect the
// conformance guard exists to catch, and catching it here is cheaper.
func TestClimateClusterCompositionIsThermostatOnly(t *testing.T) {
	for _, tc := range []struct {
		name     string
		model    string
		humidity bool
	}{
		{name: "WithHumiditySensor", model: "HmIP-BWTH:1", humidity: true},
		{name: "WithoutHumiditySensor", model: "HmIP-eTRV:1", humidity: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newRig(t, tc.model, KindIP, &stubWriter{}, custom.ClimateCapabilities{})
			if !tc.humidity {
				r.climate.humidity = nil
			}
			// The humidity slot state must not change the cluster set — that
			// it once did is why 0x0405 was conditional here.
			if tc.humidity && r.climate.humidity == nil {
				t.Fatal("rig has no humidity data point; the case tests nothing")
			}

			got := map[uint32]bool{}
			servers := r.climate.MatterClusterServers()
			for _, s := range servers {
				got[s.MatterClusterID()] = true
			}
			for _, id := range []uint32{0x0201, 0x0204} {
				if !got[id] {
					t.Errorf("cluster 0x%04X missing from %v", id, got)
				}
			}
			for _, id := range []uint32{0x0402, 0x0405, 0x0024} {
				if got[id] {
					t.Errorf("cluster 0x%04X must not be served on a Thermostat endpoint", id)
				}
			}
			if len(servers) != 2 {
				t.Errorf("expected exactly 2 cluster servers, got %d (%v)", len(servers), got)
			}
		})
	}
}
