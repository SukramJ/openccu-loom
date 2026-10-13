// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package climate

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/SukramJ/go-fabric/cluster"
	thermostatspec "github.com/SukramJ/go-fabric/cluster/spec/thermostat"
	"github.com/SukramJ/go-fabric/cluster/thermo"
	"github.com/SukramJ/go-fabric/im"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
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

// thermostatServer returns the Thermostat server a Climate serves.
func thermostatServer(t *testing.T, c *Climate) *thermo.ThermostatServer {
	t.Helper()
	srv, ok := findCluster(t, c, thermo.ThermostatClusterID).(*thermo.ThermostatServer)
	if !ok {
		t.Fatal("the Thermostat server is not go-fabric's *thermo.ThermostatServer")
	}
	return srv
}

// readInt16 / readU8 read an attribute that must be served with a value.
func readInt16(t *testing.T, srv interfaces.MatterClusterServer, attr uint32) int16 {
	t.Helper()
	v, ok := srv.MatterRead(attr)
	if !ok {
		t.Fatalf("attribute 0x%04X not served", attr)
	}
	n, isI16 := v.(int16)
	if !isI16 {
		t.Fatalf("attribute 0x%04X read %T (%v), want int16", attr, v, v)
	}
	return n
}

func readU8(t *testing.T, srv interfaces.MatterClusterServer, attr uint32) uint8 {
	t.Helper()
	v, ok := srv.MatterRead(attr)
	if !ok {
		t.Fatalf("attribute 0x%04X not served", attr)
	}
	n, isU8 := v.(uint8)
	if !isU8 {
		t.Fatalf("attribute 0x%04X read %T (%v), want uint8", attr, v, v)
	}
	return n
}

func featureMapOf(t *testing.T, srv interfaces.MatterClusterServer) uint32 {
	t.Helper()
	v, ok := srv.MatterRead(cluster.AttrGlobalFeatureMap)
	if !ok {
		t.Fatal("FeatureMap not served")
	}
	return v.(uint32)
}

func attributeSet(t *testing.T, srv interfaces.MatterClusterServer) map[uint32]bool {
	t.Helper()
	lister, ok := srv.(interfaces.MatterClusterAttributeLister)
	if !ok {
		t.Fatal("server does not implement MatterClusterAttributeLister")
	}
	out := map[uint32]bool{}
	for _, id := range lister.MatterAttributes() {
		out[id] = true
	}
	return out
}

func containsAttr(ids []uint32, want uint32) bool {
	for _, id := range ids {
		if id == want {
			return true
		}
	}
	return false
}

// TestMatterDeviceTypeIsThermostat locks the Thermostat (0x0301)
// device-type advertisement.
func TestMatterDeviceTypeIsThermostat(t *testing.T) {
	r := newRig(t, "HmIP-BWTH:1", KindIP, &stubWriter{}, custom.ClimateCapabilities{})
	if got := r.climate.MatterDeviceType(); got != 0x0301 {
		t.Fatalf("MatterDeviceType = 0x%04X, want 0x0301", got)
	}
}

// TestThermostatServerIsOneInstanceAcrossReassemblies pins the holder:
// the assembler re-reads the servers on every reassembly and the bridge
// keeps the one it already serves.
func TestThermostatServerIsOneInstanceAcrossReassemblies(t *testing.T) {
	r := newRig(t, "HmIP-BWTH:1", KindIP, &stubWriter{}, custom.ClimateCapabilities{})
	first, second := thermostatServer(t, r.climate), thermostatServer(t, r.climate)
	if first != second {
		t.Fatal("MatterClusterServers built a second Thermostat server")
	}
	ui1, ui2 := findCluster(t, r.climate, 0x0204), findCluster(t, r.climate, 0x0204)
	if ui1 != ui2 {
		t.Fatal("MatterClusterServers built a second ThermostatUserInterfaceConfiguration server")
	}
}

// TestThermostatLocalTemperatureFollowsTheConfirmedReading pins the
// inbound temperature path: ACTUAL_TEMPERATURE observed before the server
// is built is its start value, and a later confirmed reading replaces it
// through SetLocalTemperature (the int16 0.01 °C encoding).
func TestThermostatLocalTemperatureFollowsTheConfirmedReading(t *testing.T) {
	r := newRig(t, "HmIP-BWTH:1", KindIP, &stubWriter{}, custom.ClimateCapabilities{})
	r.actualTemperature.OnEvent(21.5)
	srv := thermostatServer(t, r.climate)
	if got := readInt16(t, srv, thermostatspec.AttrLocalTemperature); got != 2150 {
		t.Fatalf("LocalTemperature at build = %d, want 2150", got)
	}
	r.actualTemperature.OnEvent(22.25)
	if got := readInt16(t, srv, thermostatspec.AttrLocalTemperature); got != 2225 {
		t.Fatalf("LocalTemperature after the confirmed reading = %d, want 2225", got)
	}
}

// TestThermostatLocalTemperatureUnobserved ensures an unobserved reading
// surfaces (nil, true) — attribute supported, value transiently null.
// (nil, false) would signal UnsupportedAttribute to the HAP mapper and
// abort the HAP service build with HAPErrorDomain Code=24.
func TestThermostatLocalTemperatureUnobserved(t *testing.T) {
	r := newRig(t, "HmIP-BWTH:1", KindIP, &stubWriter{}, custom.ClimateCapabilities{})
	srv := thermostatServer(t, r.climate)
	v, ok := srv.MatterRead(thermostatspec.AttrLocalTemperature)
	if !ok || v != nil {
		t.Fatalf("LocalTemperature on unobserved = (%v, %v), want (nil, true)", v, ok)
	}
}

// TestThermostatHeatingSetpointWriteReachesTheCCU covers the outbound
// setpoint path: a controller's OccupiedHeatingSetpoint write (the wire
// delivers the int16 as int64) reaches the CCU through the Sink as
// SetTemperature, and the attribute moves once the Sink consented.
func TestThermostatHeatingSetpointWriteReachesTheCCU(t *testing.T) {
	w := &stubWriter{}
	r := newRig(t, "HmIP-BWTH:1", KindIP, w, custom.ClimateCapabilities{
		MinTemperature: 4.5,
		MaxTemperature: 30.5,
	})
	r.setpoint.OnEvent(20.0)
	srv := thermostatServer(t, r.climate)
	if got := readInt16(t, srv, thermostatspec.AttrOccupiedHeatingSetpoint); got != 2000 {
		t.Fatalf("OccupiedHeatingSetpoint = %d, want 2000", got)
	}
	if err := srv.MatterWrite(context.Background(), thermostatspec.AttrOccupiedHeatingSetpoint, int64(2150)); err != nil {
		t.Fatalf("MatterWrite(setpoint=21.5): %v", err)
	}
	if got := w.last(); got.value.(float64) != 21.5 {
		t.Fatalf("setpoint write reached the wire as %v, want 21.5", got.value)
	}
	if got := readInt16(t, srv, thermostatspec.AttrOccupiedHeatingSetpoint); got != 2150 {
		t.Fatalf("OccupiedHeatingSetpoint after the write = %d, want 2150", got)
	}
}

// TestThermostatConfirmedSetpointReachesTheServer pins the inbound
// setpoint path: a SET_POINT_TEMPERATURE the CCU confirms (wall dial, CCU
// program) lands on the server through SetOccupiedHeatingSetpoint —
// readable at once and reported through the change notifier — without a
// write.
func TestThermostatConfirmedSetpointReachesTheServer(t *testing.T) {
	w := &stubWriter{}
	r := newRig(t, "HmIP-BWTH:1", KindIP, w, custom.ClimateCapabilities{
		MinTemperature: 4.5,
		MaxTemperature: 30.5,
	})
	srv := thermostatServer(t, r.climate)
	reported := make(chan []uint32, 4)
	unsub := srv.OnMatterAttributesChanged(func(ids []uint32) { reported <- ids })
	defer unsub()

	r.setpoint.OnEvent(19.5)

	if got := readInt16(t, srv, thermostatspec.AttrOccupiedHeatingSetpoint); got != 1950 {
		t.Fatalf("OccupiedHeatingSetpoint after the confirmed 19.5 °C = %d, want 1950", got)
	}
	select {
	case ids := <-reported:
		if !containsAttr(ids, thermostatspec.AttrOccupiedHeatingSetpoint) {
			t.Fatalf("reported %v, want OccupiedHeatingSetpoint", ids)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the confirmed setpoint was not reported through the change notifier")
	}
	if len(w.calls) != 0 {
		t.Fatalf("a confirmed setpoint must not echo into a write; writes: %d", len(w.calls))
	}
}

// TestThermostatOffSentinelReportsSystemModeOff: a setpoint at or below
// the OFF temperature means the thermostat is off ([Climate.Mode]), and
// the server's SystemMode follows the setpoint hook.
func TestThermostatOffSentinelReportsSystemModeOff(t *testing.T) {
	r := newRig(t, "HmIP-BWTH:1", KindIP, &stubWriter{}, custom.ClimateCapabilities{
		MinTemperature: 4.5,
		MaxTemperature: 30.5,
	})
	r.climate.OnMode(ModeHeat)
	r.setpoint.OnEvent(20.0)
	srv := thermostatServer(t, r.climate)
	if got := readU8(t, srv, thermostatspec.AttrSystemMode); got != matterSysModeHeat {
		t.Fatalf("SystemMode at 20 °C = %d, want 4 (Heat)", got)
	}
	r.setpoint.OnEvent(offTemperature)
	if got := readU8(t, srv, thermostatspec.AttrSystemMode); got != matterSysModeOff {
		t.Fatalf("SystemMode at the OFF temperature = %d, want 0 (Off)", got)
	}
}

// TestThermostatModeRecordersReachTheServer pins the mode hook: a mode
// the device took on itself reaches the server through SetSystemMode
// from every recorder the wire feeds — SET_POINT_MODE (IP), CONTROL_MODE
// (RF), the generic OnMode and HEATING_COOLING — and is reported through
// the change notifier.
func TestThermostatModeRecordersReachTheServer(t *testing.T) {
	t.Run("set-point-mode MANU on an IP thermostat", func(t *testing.T) {
		r := newRig(t, "HmIP-BWTH:1", KindIP, &stubWriter{}, custom.ClimateCapabilities{})
		r.setpoint.OnEvent(20.0)
		srv := thermostatServer(t, r.climate)
		reported := make(chan []uint32, 4)
		unsub := srv.OnMatterAttributesChanged(func(ids []uint32) { reported <- ids })
		defer unsub()
		r.climate.OnSetPointMode(1) // MANU → ModeHeat
		if got := readU8(t, srv, thermostatspec.AttrSystemMode); got != matterSysModeHeat {
			t.Fatalf("SystemMode after MANU = %d, want 4 (Heat)", got)
		}
		// The server starts in Heat, so the first mode report is the one
		// the OFF transition below produces; the setpoint itself is
		// reported first.
		r.setpoint.OnEvent(offTemperature)
		deadline := time.After(2 * time.Second)
		for {
			select {
			case ids := <-reported:
				if containsAttr(ids, thermostatspec.AttrSystemMode) {
					return
				}
			case <-deadline:
				t.Fatal("the mode change was not reported through the change notifier")
			}
		}
	})
	t.Run("control-mode on an RF thermostat", func(t *testing.T) {
		r := newRig(t, "HM-CC-RT-DN:4", KindRF, &stubWriter{}, custom.ClimateCapabilities{})
		r.setpoint.OnEvent(20.0)
		srv := thermostatServer(t, r.climate)
		r.climate.OnControlMode("MANU-MODE")
		if got := readU8(t, srv, thermostatspec.AttrSystemMode); got != matterSysModeHeat {
			t.Fatalf("SystemMode after MANU-MODE = %d, want 4 (Heat)", got)
		}
	})
	t.Run("heating-cooling direction on a hybrid thermostat", func(t *testing.T) {
		r := newRig(t, "HmIP-BWTH:1", KindIP, &stubWriter{}, custom.ClimateCapabilities{SupportsCool: true})
		r.setpoint.OnEvent(20.0)
		r.climate.OnMode(ModeAuto)
		srv := thermostatServer(t, r.climate)
		if got := readU8(t, srv, thermostatspec.AttrSystemMode); got != matterSysModeHeat {
			t.Fatalf("SystemMode in AUTO heating = %d, want 4 (Heat)", got)
		}
		r.climate.OnHeatingCooling("COOLING")
		if got := readU8(t, srv, thermostatspec.AttrSystemMode); got != matterSysModeCool {
			t.Fatalf("SystemMode in AUTO cooling = %d, want 3 (Cool)", got)
		}
	})
}

// TestThermostatMinMaxHeatSetpointFromCapabilities reads the
// configurator-supplied limits as the absolute and user limits of the
// server. Note: Climate.MinTemp() has the off-temperature special case
// (4.5 °C → 5.0 °C bump) that the projection inherits — using 5.5 °C
// here keeps the test focused on the encoding rather than the
// off-shift behaviour.
func TestThermostatMinMaxHeatSetpointFromCapabilities(t *testing.T) {
	r := newRig(t, "HmIP-BWTH:1", KindIP, &stubWriter{}, custom.ClimateCapabilities{
		MinTemperature: 5.5,
		MaxTemperature: 30.5,
	})
	srv := thermostatServer(t, r.climate)
	for attr, want := range map[uint32]int16{
		thermostatspec.AttrMinHeatSetpointLimit:    550,
		thermostatspec.AttrMaxHeatSetpointLimit:    3050,
		thermostatspec.AttrAbsMinHeatSetpointLimit: 550,
		thermostatspec.AttrAbsMaxHeatSetpointLimit: 3050,
	} {
		if got := readInt16(t, srv, attr); got != want {
			t.Errorf("attribute 0x%04X = %d, want %d", attr, got, want)
		}
	}
}

// TestThermostatControlSequenceHeatingOnly locks the heating-only
// projection's ControlSequenceOfOperation.
func TestThermostatControlSequenceHeatingOnly(t *testing.T) {
	r := newRig(t, "HmIP-BWTH:1", KindIP, &stubWriter{}, custom.ClimateCapabilities{})
	srv := thermostatServer(t, r.climate)
	if got := readU8(t, srv, thermostatspec.AttrControlSequenceOfOperation); got != uint8(thermostatspec.ControlSequenceOfOperationHeatingOnly) {
		t.Fatalf("ControlSequenceOfOperation = %d, want 2 (HeatingOnly)", got)
	}
}

// TestThermostatFeatureMapHeatOnly confirms the FeatureMap advertises
// only HEAT for heating-only HmIP devices. AUTO requires both HEAT and
// COOL (spec conformance "AUTO, O.a+"), so it must not be set here.
func TestThermostatFeatureMapHeatOnly(t *testing.T) {
	r := newRig(t, "HmIP-BWTH:1", KindIP, &stubWriter{}, custom.ClimateCapabilities{})
	srv := thermostatServer(t, r.climate)
	if got := featureMapOf(t, srv); got != thermo.ThermostatFeatureHEAT {
		t.Fatalf("FeatureMap = 0x%08X, want HEAT only", got)
	}
}

// TestThermostatHeatOnlyExcludesCoolAndAutoAttributes locks the
// conformance-gated attribute surface for a heating-only device:
// OccupiedCoolingSetpoint (0x11), MinCoolSetpointLimit (0x17),
// MaxCoolSetpointLimit (0x18) require the COOL feature
// (thermostat-cluster.element.ts:66-68, 92-98) and ThermostatRunningMode
// (0x1e) requires AUTO (thermostat-cluster.element.ts:117-120) — none of
// which a heat-only FeatureMap advertises; a read reports not-present.
func TestThermostatHeatOnlyExcludesCoolAndAutoAttributes(t *testing.T) {
	r := newRig(t, "HmIP-BWTH:1", KindIP, &stubWriter{}, custom.ClimateCapabilities{})
	r.setpoint.OnEvent(20.0)
	srv := thermostatServer(t, r.climate)
	attrs := attributeSet(t, srv)
	for id, name := range map[uint32]string{
		thermostatspec.AttrOccupiedCoolingSetpoint: "OccupiedCoolingSetpoint",
		thermostatspec.AttrMinCoolSetpointLimit:    "MinCoolSetpointLimit",
		thermostatspec.AttrMaxCoolSetpointLimit:    "MaxCoolSetpointLimit",
		thermostatspec.AttrThermostatRunningMode:   "ThermostatRunningMode",
	} {
		if attrs[id] {
			t.Errorf("MatterAttributes() lists %s (0x%04X) on a heating-only device", name, id)
		}
		if _, ok := srv.MatterRead(id); ok {
			t.Errorf("MatterRead(%s) on a heating-only device should report not-present", name)
		}
	}
}

// TestThermostatSystemModeWriteCoolRejectedHeatOnly asserts that writing
// SystemMode=Cool on a heating-only device is rejected with a
// ConstraintError by the server's definition rather than silently
// retargeting the single HM heating setpoint. Mirrors matter.js
// ThermostatServer.ts:#assertSystemModeChanging.
func TestThermostatSystemModeWriteCoolRejectedHeatOnly(t *testing.T) {
	w := &stubWriter{}
	r := newRig(t, "HmIP-BWTH:1", KindIP, w, custom.ClimateCapabilities{})
	srv := thermostatServer(t, r.climate)
	err := srv.MatterWrite(context.Background(), thermostatspec.AttrSystemMode, uint64(matterSysModeCool))
	var sc im.StatusCodeError
	if !errors.As(err, &sc) || sc.MatterStatusCode() != im.StatusConstraintError {
		t.Fatalf("SystemMode=Cool on a heating-only device = %v, want ConstraintError", err)
	}
	if len(w.calls) != 0 {
		t.Fatal("a refused SystemMode reached the wire")
	}
}

// TestThermostatCoolCapableDeviceAdvertisesCoolWithoutAuto proves the
// COOL gating keys off the capability while AUTO stays off even on a
// cooling-capable profile: the Cool-setpoint attributes are listed and
// served (aliasing the one HM setpoint), the FeatureMap carries no AUTO,
// ThermostatRunningMode stays absent and a SystemMode=Auto write is
// rejected with ConstraintError.
func TestThermostatCoolCapableDeviceAdvertisesCoolWithoutAuto(t *testing.T) {
	w := &stubWriter{}
	r := newRig(t, "HmIP-BWTH:1", KindIP, w, custom.ClimateCapabilities{
		SupportsCool:   true,
		MinTemperature: 4.5,
		MaxTemperature: 30.5,
	})
	r.setpoint.OnEvent(21.0)
	srv := thermostatServer(t, r.climate)

	if fm := featureMapOf(t, srv); fm != thermo.ThermostatFeatureHEAT|thermo.ThermostatFeatureCOOL {
		t.Fatalf("FeatureMap = 0x%08X, want HEAT|COOL without AUTO", fm)
	}
	attrs := attributeSet(t, srv)
	for id, name := range map[uint32]string{
		thermostatspec.AttrOccupiedCoolingSetpoint: "OccupiedCoolingSetpoint",
		thermostatspec.AttrMinCoolSetpointLimit:    "MinCoolSetpointLimit",
		thermostatspec.AttrMaxCoolSetpointLimit:    "MaxCoolSetpointLimit",
	} {
		if !attrs[id] {
			t.Errorf("MatterAttributes() missing %s (0x%04X) on a heat+cool device", name, id)
		}
		if _, ok := srv.MatterRead(id); !ok {
			t.Errorf("MatterRead(%s) reports not-present on a heat+cool device", name)
		}
	}
	if got := readInt16(t, srv, thermostatspec.AttrOccupiedCoolingSetpoint); got != 2100 {
		t.Fatalf("OccupiedCoolingSetpoint = %d, want the one HM setpoint 2100", got)
	}
	if attrs[thermostatspec.AttrThermostatRunningMode] {
		t.Error("MatterAttributes() lists ThermostatRunningMode (0x001E) without the AUTO feature")
	}

	err := srv.MatterWrite(context.Background(), thermostatspec.AttrSystemMode, uint64(matterSysModeAuto))
	var sc im.StatusCodeError
	if !errors.As(err, &sc) || sc.MatterStatusCode() != im.StatusConstraintError {
		t.Errorf("SystemMode=Auto write without the AUTO feature = %v, want ConstraintError", err)
	}

	// A cooling setpoint write is the same single HM setpoint.
	if err := srv.MatterWrite(context.Background(), thermostatspec.AttrOccupiedCoolingSetpoint, int64(2300)); err != nil {
		t.Fatalf("OccupiedCoolingSetpoint write: %v", err)
	}
	if got := w.last(); got.value.(float64) != 23.0 {
		t.Fatalf("cooling setpoint write reached the wire as %v, want 23.0", got.value)
	}
}

// TestThermostatSystemModeReadClampsHmAutoToFeatureMap asserts the
// reported SystemMode never surfaces Auto(1) when the FeatureMap does
// not advertise the AUTO feature — Auto has conformance "AUTO" per
// matter.js thermostat-cluster.element.ts:558. HM's factory-default AUTO
// (week-program) mode is a single-setpoint schedule and projects onto
// Heat, or Cool when a hybrid device is currently in its COOLING
// direction. The clamp keeps reports and the write gate consistent: a
// controller echoing the read value back on state sync must not receive
// ConstraintError.
func TestThermostatSystemModeReadClampsHmAutoToFeatureMap(t *testing.T) {
	t.Run("heat-only device surfaces Heat", func(t *testing.T) {
		r := newRig(t, "HmIP-BWTH:1", KindIP, &stubWriter{}, custom.ClimateCapabilities{})
		r.setpoint.OnEvent(20.0)
		r.climate.OnMode(ModeAuto)
		srv := thermostatServer(t, r.climate)
		v := readU8(t, srv, thermostatspec.AttrSystemMode)
		if v != matterSysModeHeat {
			t.Fatalf("SystemMode = %d, want 4 (Heat)", v)
		}
		if err := srv.MatterWrite(context.Background(), thermostatspec.AttrSystemMode, uint64(v)); err != nil {
			t.Fatalf("echoing the read SystemMode back must not error, got: %v", err)
		}
	})
	t.Run("cool-capable device in cooling direction surfaces Cool", func(t *testing.T) {
		r := newRig(t, "HmIP-BWTH:1", KindIP, &stubWriter{}, custom.ClimateCapabilities{SupportsCool: true})
		r.setpoint.OnEvent(20.0)
		r.climate.OnMode(ModeAuto)
		r.climate.OnHeatingCooling("COOLING")
		srv := thermostatServer(t, r.climate)
		if v := readU8(t, srv, thermostatspec.AttrSystemMode); v != matterSysModeCool {
			t.Fatalf("SystemMode = %d, want 3 (Cool)", v)
		}
	})
}

// TestThermostatSystemModeWriteRoundTrip writes Heat (4) on a thermostat
// reporting Off and asserts Climate.SetMode reached the wire through the
// Sink.
func TestThermostatSystemModeWriteRoundTrip(t *testing.T) {
	w := &stubWriter{}
	r := newRig(t, "HmIP-BWTH:1", KindIP, w, custom.ClimateCapabilities{
		MinTemperature: 4.5,
		MaxTemperature: 30.5,
	})
	r.setpoint.OnEvent(offTemperature)
	srv := thermostatServer(t, r.climate)
	if got := readU8(t, srv, thermostatspec.AttrSystemMode); got != matterSysModeOff {
		t.Fatalf("SystemMode before the write = %d, want 0 (Off)", got)
	}
	if err := srv.MatterWrite(context.Background(), thermostatspec.AttrSystemMode, uint64(matterSysModeHeat)); err != nil {
		t.Fatalf("SystemMode write: %v", err)
	}
	if len(w.calls) == 0 {
		t.Fatal("SystemMode=Heat write did not reach the wire")
	}
}

// TestThermostatSystemModeUnsupportedRejected: SystemMode=7 (FanOnly) is
// conformant for the definition but has no HM mode; the Sink refuses it
// and the server answers FAILURE without a write.
func TestThermostatSystemModeUnsupportedRejected(t *testing.T) {
	w := &stubWriter{}
	r := newRig(t, "HmIP-BWTH:1", KindIP, w, custom.ClimateCapabilities{})
	srv := thermostatServer(t, r.climate)
	err := srv.MatterWrite(context.Background(), thermostatspec.AttrSystemMode, uint64(thermostatspec.SystemModeFanOnly))
	var sc im.StatusCodeError
	if !errors.As(err, &sc) || sc.MatterStatusCode() != im.StatusFailure {
		t.Fatalf("SystemMode=FanOnly = %v, want FAILURE from the Sink's refusal", err)
	}
	if len(w.calls) != 0 {
		t.Fatal("a refused SystemMode reached the wire")
	}
}

// TestThermostatSetpointRaiseLowerCommand exercises the delta command
// through the server: amount is in 0.1 °C; +15 on 20.0 °C reaches the CCU
// as 21.5.
func TestThermostatSetpointRaiseLowerCommand(t *testing.T) {
	w := &stubWriter{}
	r := newRig(t, "HmIP-BWTH:1", KindIP, w, custom.ClimateCapabilities{
		MinTemperature: 4.5,
		MaxTemperature: 30.5,
	})
	r.setpoint.OnEvent(20.0)
	srv := thermostatServer(t, r.climate)
	req := thermostatspec.SetpointRaiseLowerRequest{Mode: thermostatspec.SetpointRaiseLowerModeHeat, Amount: 15}
	if _, err := srv.MatterInvoke(context.Background(), 0x00, req); err != nil {
		t.Fatalf("SetpointRaiseLower: %v", err)
	}
	if got := w.last(); got.value.(float64) != 21.5 {
		t.Fatalf("raise reached the wire as %v, want 21.5", got.value)
	}
	if got := readInt16(t, srv, thermostatspec.AttrOccupiedHeatingSetpoint); got != 2150 {
		t.Fatalf("OccupiedHeatingSetpoint after the command = %d, want 2150", got)
	}
}

// TestThermostatUITempDisplayModeIsCelsius locks the display-mode start
// value of the ThermostatUserInterfaceConfiguration server.
func TestThermostatUITempDisplayModeIsCelsius(t *testing.T) {
	r := newRig(t, "HmIP-BWTH:1", KindIP, &stubWriter{}, custom.ClimateCapabilities{})
	srv := findCluster(t, r.climate, thermo.ClusterIDThermostatUserInterfaceConfiguration)
	if got := readU8(t, srv, 0x0000); got != uint8(thermo.TemperatureDisplayCelsius) {
		t.Fatalf("TemperatureDisplayMode = %d, want 0 (Celsius)", got)
	}
	if got := readU8(t, srv, 0x0001); got != 0 {
		t.Fatalf("KeypadLockout = %d, want 0 (NoLockout)", got)
	}
}

// TestSetpointWriteWrongTypeRejected — defence-in-depth against bridge
// regressions: the definition refuses a value that is not a temperature.
func TestSetpointWriteWrongTypeRejected(t *testing.T) {
	w := &stubWriter{}
	r := newRig(t, "HmIP-BWTH:1", KindIP, w, custom.ClimateCapabilities{
		MinTemperature: 4.5,
		MaxTemperature: 30.5,
	})
	srv := thermostatServer(t, r.climate)
	if err := srv.MatterWrite(context.Background(), thermostatspec.AttrOccupiedHeatingSetpoint, "21.5"); err == nil {
		t.Fatal("a string setpoint write must be refused")
	}
	if len(w.calls) != 0 {
		t.Fatal("a refused setpoint reached the wire")
	}
}

// TestThermostatNoFabricatedAttrAt0x30 confirms the server does not
// serve SetpointChangeSource (0x0030): it is optional and unbacked, and
// LocalTemperatureNotExposed is FeatureMap bit 6, not an attribute.
func TestThermostatNoFabricatedAttrAt0x30(t *testing.T) {
	r := newRig(t, "HmIP-BWTH:1", KindIP, &stubWriter{}, custom.ClimateCapabilities{})
	srv := thermostatServer(t, r.climate)
	if _, ok := srv.MatterRead(thermostatspec.AttrSetpointChangeSource); ok {
		t.Fatal("0x0030 must not be readable")
	}
	if attributeSet(t, srv)[thermostatspec.AttrSetpointChangeSource] {
		t.Fatal("0x0030 must not be listed")
	}
}

// TestUnknownCommandsRejected — uniform UNSUPPORTED_COMMAND surface
// across both cluster servers in the projection.
func TestUnknownCommandsRejected(t *testing.T) {
	r := newRig(t, "HmIP-BWTH:1", KindIP, &stubWriter{}, custom.ClimateCapabilities{})
	for _, id := range []uint32{thermo.ThermostatClusterID, thermo.ClusterIDThermostatUserInterfaceConfiguration} {
		srv := findCluster(t, r.climate, id)
		_, err := srv.MatterInvoke(context.Background(), 0x99, nil)
		var sc im.StatusCodeError
		if !errors.As(err, &sc) || sc.MatterStatusCode() != im.StatusUnsupportedCommand {
			t.Errorf("cluster 0x%04X: err=%v, want UNSUPPORTED_COMMAND", id, err)
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
