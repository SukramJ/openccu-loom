// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package climate

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"sync"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

type stubWriter struct {
	mu    sync.Mutex
	calls []call
}

type call struct {
	param hmenum.Parameter
	value any
}

func (s *stubWriter) SetValue(_ context.Context, _ string, p hmenum.Parameter, v any, _ hmenum.CommandPriority) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, call{p, v})
	return nil
}

func (s *stubWriter) last() call {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls[len(s.calls)-1]
}

// putWriter additionally implements generic.ParamsetWriter so the
// atomic-batching code path is exercised. It records the entire
// values map as one logical "put" call.
type putWriter struct {
	stubWriter
	puts []map[string]any
}

func (p *putWriter) PutParamset(_ context.Context, _ string, _ hmenum.ParamsetKey, values map[string]any, _ hmenum.CommandPriority) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	cp := make(map[string]any, len(values))
	maps.Copy(cp, values)
	p.puts = append(p.puts, cp)
	return nil
}

// rig holds a Climate together with the channel-side generic data
// points that back it, so tests can drive wire updates by calling
// OnEvent on the same instance the production wire path would.
type rig struct {
	climate           *Climate
	channel           *device.Channel
	setpoint          *generic.Float
	actualTemperature *generic.Sensor[float64]
	humidity          *generic.Sensor[float64]
}

func newRig(t *testing.T, address string, kind Kind, w Writer, caps custom.ClimateCapabilities) *rig {
	t.Helper()
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "ABC0001"})
	ch := d.AddChannel(address, 1, "CLIMATE", hmenum.ParamsetKeyValues)

	setpointParam := hmenum.ParameterSetPointTemperature
	if kind != KindIP {
		setpointParam = hmenum.ParameterSetTemperature
	}
	setpoint := generic.NewFloat(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: address,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(setpointParam),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeFloat,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent,
		},
		Writer: w,
	})
	ch.Put(setpoint)

	actual := generic.NewFloatSensor(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: address,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(hmenum.ParameterActualTemperature),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeFloat,
			Operations: hmenum.OperationsRead | hmenum.OperationsEvent,
		},
	})
	ch.Put(actual)

	humidity := generic.NewFloatSensor(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: address,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(hmenum.ParameterHumidity),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeFloat,
			Operations: hmenum.OperationsRead | hmenum.OperationsEvent,
		},
	})
	ch.Put(humidity)

	// Attach a synthetic week-program-pointer DP with descriptor
	// MIN=0/MAX=5 so [Profiles] returns six week-program slots in
	// AUTO mode — matches the IP-thermostat shape rigs assert
	// against. KindIP uses ACTIVE_PROFILE; KindRF/SimpleRF use
	// WEEK_PROGRAM_POINTER.
	if kind == KindIP || kind == KindRF {
		pointerParam := hmenum.ParameterActiveProfile
		if kind == KindRF {
			pointerParam = hmenum.ParameterWeekProgramPointer
		}
		pointer := generic.NewInteger(generic.Spec{
			Key: hmtypes.DataPointKey{
				ChannelAddress: address,
				ParamsetKey:    hmenum.ParamsetKeyMaster,
				Parameter:      string(pointerParam),
			},
			Descriptor: hmproto.ParameterData{
				Type:       hmenum.ParameterTypeInteger,
				Operations: hmenum.OperationsRead | hmenum.OperationsWrite,
				Min:        json.RawMessage("0"),
				Max:        json.RawMessage("5"),
			},
		})
		ch.PutMaster(pointer)
	}

	// Activity source: IP rigs model a LEVEL-carrying thermostat
	// (HmIP-eTRV shape), RF rigs a VALVE_STATE-carrying one. Without
	// any source the action surface is omitted entirely (HmIP-STHD
	// display-only shape) — covered by dedicated tests.
	if kind == KindIP || kind == KindRF {
		activityParam := hmenum.ParameterLevel
		if kind == KindRF {
			activityParam = hmenum.ParameterValveState
		}
		activity := generic.NewFloatSensor(generic.Spec{
			Key: hmtypes.DataPointKey{
				ChannelAddress: address,
				ParamsetKey:    hmenum.ParamsetKeyValues,
				Parameter:      string(activityParam),
			},
			Descriptor: hmproto.ParameterData{
				Type:       hmenum.ParameterTypeFloat,
				Operations: hmenum.OperationsRead | hmenum.OperationsEvent,
			},
		})
		ch.Put(activity)
	}

	c := New(Config{Channel: ch, Writer: w, Capabilities: caps, Kind: kind})
	return &rig{
		climate:           c,
		channel:           ch,
		setpoint:          setpoint,
		actualTemperature: actual,
		humidity:          humidity,
	}
}

func TestClimateSetTemperatureClamps(t *testing.T) {
	w := &stubWriter{}
	r := newRig(t, "HmIP-BWTH:1", KindIP, w, custom.ClimateCapabilities{
		MinTemperature: 4.5,
		MaxTemperature: 30.5,
	})
	// SetTemperature rejects out-of-range values with ErrTemperatureOutOfRange.
	err := r.climate.SetTemperature(context.Background(), 40, hmenum.CommandPriorityHigh)
	if !errors.Is(err, ErrTemperatureOutOfRange) {
		t.Fatalf("SetTemperature(40) got %v, want ErrTemperatureOutOfRange", err)
	}
}

// TestClimateSetModeIP pins the contract that HmIP thermostats
// (HmIP-BWTH/eTRV/STH/WTH/…) write CONTROL_MODE (write-only ACTION) to change
// mode, NOT the read-only SET_POINT_MODE.
//
// - AUTO → CONTROL_MODE=0 - OFF → CONTROL_MODE=1 + SET_POINT_TEMPERATURE=4.5
// (atomic put)
//
// Sending SET_POINT_MODE has no effect on most CCU firmwares because it is a
// read-only echo of the active mode.
func TestClimateSetModeIP(t *testing.T) {
	w := &stubWriter{}
	r := newRig(t, "x", KindIP, w, custom.ClimateCapabilities{MinTemperature: 4.5, MaxTemperature: 30.5})

	if err := r.climate.SetMode(context.Background(), ModeAuto, hmenum.CommandPriorityHigh); err != nil {
		t.Fatal(err)
	}
	if got := w.last(); got.param != hmenum.ParameterControlMode || got.value.(int32) != 0 {
		t.Fatalf("AUTO wrote %+v want CONTROL_MODE=0", got)
	}
}

func TestClimateSetModeIPOffAtomicPutParamset(t *testing.T) {
	w := &putWriter{}
	r := newRig(t, "VCU0000050:4", KindIP, w, custom.ClimateCapabilities{MinTemperature: 4.5, MaxTemperature: 30.5})
	if err := r.climate.SetMode(context.Background(), ModeOff, hmenum.CommandPriorityHigh); err != nil {
		t.Fatal(err)
	}
	if len(w.puts) != 1 {
		t.Fatalf("expected 1 put_paramset, got %d (sets=%d)", len(w.puts), len(w.calls))
	}
	got := w.puts[0]
	if got[string(hmenum.ParameterControlMode)].(int32) != 1 {
		t.Errorf("CONTROL_MODE=%v want 1 (MANU)", got[string(hmenum.ParameterControlMode)])
	}
	if got[string(hmenum.ParameterSetPointTemperature)].(float64) != 4.5 {
		t.Errorf("SET_POINT_TEMPERATURE=%v want 4.5", got[string(hmenum.ParameterSetPointTemperature)])
	}
}

func TestClimateSetModeRFOffAtomicPutParamset(t *testing.T) {
	w := &putWriter{}
	r := newRig(t, "VCU0000341:2", KindRF, w, custom.ClimateCapabilities{MinTemperature: 4.5, MaxTemperature: 30.5})
	if err := r.climate.SetMode(context.Background(), ModeOff, hmenum.CommandPriorityHigh); err != nil {
		t.Fatal(err)
	}
	if len(w.puts) != 1 {
		t.Fatalf("expected 1 put_paramset, got %d", len(w.puts))
	}
	got := w.puts[0]
	if got[string(hmenum.ParameterManuMode)].(float64) != 4.5 {
		t.Errorf("MANU_MODE=%v", got[string(hmenum.ParameterManuMode)])
	}
	if got[string(hmenum.ParameterSetTemperature)].(float64) != 4.5 {
		t.Errorf("SET_TEMPERATURE=%v", got[string(hmenum.ParameterSetTemperature)])
	}
}

func TestClimateSetAwayIPAtomicPutParamset(t *testing.T) {
	w := &putWriter{}
	r := newRig(t, "VCU0000050:4", KindIP, w, custom.ClimateCapabilities{
		MinTemperature: 4.5, MaxTemperature: 30.5, SupportsAway: true,
	})
	end := time.Now().Add(2 * time.Hour)
	if err := r.climate.SetAway(context.Background(), end, 17.0, hmenum.CommandPriorityHigh); err != nil {
		t.Fatal(err)
	}
	if len(w.puts) != 1 {
		t.Fatalf("expected 1 put_paramset, got %d (sets=%d)", len(w.puts), len(w.calls))
	}
	got := w.puts[0]
	for _, p := range []hmenum.Parameter{
		hmenum.ParameterPartyTimeStart,
		hmenum.ParameterPartyTimeEnd,
		hmenum.ParameterSetPointMode,
		hmenum.ParameterSetPointTemperature,
	} {
		if _, ok := got[string(p)]; !ok {
			t.Errorf("missing %s in atomic batch", p)
		}
	}
}

func TestClimateSetProfileRejectsNonWeek(t *testing.T) {
	r := newRig(t, "x", KindIP, &stubWriter{}, custom.ClimateCapabilities{SupportsProfile: true})
	if err := r.climate.SetProfile(context.Background(), ProfileAway, hmenum.CommandPriorityHigh); err == nil {
		t.Fatal("non-week profile should be rejected")
	}
}

// TestClimateSetProfileIPMapsToActiveProfile pins the contract that
// `SetProfile` on KindIP writes 1-based ACTIVE_PROFILE (INTEGER 1..6) — NOT
// WEEK_PROGRAM_POINTER. HmIP-BWTH / -eTRV / -STH / -WTH / -WGT expose
// `ACTIVE_PROFILE` on the climate channel; sending WEEK_PROGRAM_POINTER
// instead triggers XML-RPC fault `-5 "Invalid parameter or value"`.
//
// Starts from AUTO so only ACTIVE_PROFILE goes out — this isolates the
// value shape from the AUTO/BOOST_MODE batching that
// TestSetProfileWeekProgramIPSwitchesToAutoFirst covers.
func TestClimateSetProfileIPMapsToActiveProfile(t *testing.T) {
	w := &stubWriter{}
	r := newRig(t, "x", KindIP, w, custom.ClimateCapabilities{SupportsProfile: true})
	if err := r.climate.SetMode(context.Background(), ModeAuto, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("SetMode(Auto): %v", err)
	}
	if err := r.climate.SetProfile(context.Background(), ProfileWeekProgram3, hmenum.CommandPriorityHigh); err != nil {
		t.Fatal(err)
	}
	got := w.last()
	if got.param != hmenum.ParameterActiveProfile {
		t.Fatalf("last param=%v want ACTIVE_PROFILE", got.param)
	}
	if v, ok := got.value.(int32); !ok || v != 3 {
		t.Fatalf("last value=%#v want int32(3)", got.value)
	}
}

// TestClimateSetProfileRFMapsToEnumLabel pins the contract that `SetProfile`
// on KindRF writes the CCU's ENUM-string label (`"WEEK PROGRAM N"`, 1-based)
// to `WEEK_PROGRAM_POINTER`. HM-CC-RT-DN and the rest of the RF thermostat
// family use this ENUM shape.
func TestClimateSetProfileRFMapsToEnumLabel(t *testing.T) {
	w := &stubWriter{}
	r := newRig(t, "x", KindRF, w, custom.ClimateCapabilities{SupportsProfile: true})
	if err := r.climate.SetProfile(context.Background(), ProfileWeekProgram3, hmenum.CommandPriorityHigh); err != nil {
		t.Fatal(err)
	}
	got := w.last()
	if got.param != hmenum.ParameterWeekProgramPointer {
		t.Fatalf("last param=%v want WEEK_PROGRAM_POINTER", got.param)
	}
	if s, ok := got.value.(string); !ok || s != "WEEK PROGRAM 3" {
		t.Fatalf("last value=%#v want \"WEEK PROGRAM 3\"", got.value)
	}
}

func TestClimateBoostGatedOnCapability(t *testing.T) {
	w := &stubWriter{}
	r := newRig(t, "x", KindIP, w, custom.ClimateCapabilities{})
	if err := r.climate.EnableBoost(context.Background(), hmenum.CommandPriorityHigh); !errors.Is(err, ErrModeNotSupported) {
		t.Fatalf("got %v, want ErrModeNotSupported", err)
	}
	r.climate.Capabilities.SupportsBoost = true
	if err := r.climate.EnableBoost(context.Background(), hmenum.CommandPriorityHigh); err != nil {
		t.Fatal(err)
	}
	if got := w.last(); got.param != hmenum.ParameterBoostMode || got.value != true {
		t.Fatalf("boost call=%+v", got)
	}
}

func TestClimateSetBoostConvenience(t *testing.T) {
	w := &stubWriter{}
	r := newRig(t, "x", KindIP, w, custom.ClimateCapabilities{SupportsBoost: true})

	// Activating sets profile to boost optimistically.
	if err := r.climate.SetBoost(context.Background(), true, hmenum.CommandPriorityHigh); err != nil {
		t.Fatal(err)
	}
	if got := w.last(); got.param != hmenum.ParameterBoostMode || got.value != true {
		t.Fatalf("on call=%+v", got)
	}
	if p, ok := r.climate.Profile(); !ok || p != ProfileBoost {
		t.Fatalf("profile after activation = (%v, %v) want (boost, true)", p, ok)
	}

	// Deactivating writes false but does NOT clear the profile (CCU
	// echoes the new profile back via OnProfile).
	if err := r.climate.SetBoost(context.Background(), false, hmenum.CommandPriorityHigh); err != nil {
		t.Fatal(err)
	}
	if got := w.last(); got.param != hmenum.ParameterBoostMode || got.value != false {
		t.Fatalf("off call=%+v", got)
	}
}

func TestClimateIngestion(t *testing.T) {
	r := newRig(t, "x", KindIP, &stubWriter{}, custom.ClimateCapabilities{})

	// Wire-backed values: drive the channel-side DPs (production
	// path) and verify Climate sees them through the shared pointer.
	r.actualTemperature.OnEvent(22.3)
	r.setpoint.OnEvent(21)
	r.humidity.OnEvent(45)
	r.climate.OnMode(ModeAuto)
	r.climate.OnProfile(ProfileWeekProgram2)

	if v, ok := r.climate.CurrentTemperature(); !ok || v != 22.3 {
		t.Errorf("current=%v ok=%v", v, ok)
	}
	if v, ok := r.climate.Setpoint(); !ok || v != 21 {
		t.Errorf("setpoint=%v ok=%v", v, ok)
	}
	if v, ok := r.climate.Humidity(); !ok || v != 45 {
		t.Errorf("humidity=%v ok=%v", v, ok)
	}
	if m, ok := r.climate.Mode(); !ok || m != ModeAuto {
		t.Errorf("mode=%v ok=%v", m, ok)
	}
	if p, ok := r.climate.Profile(); !ok || p != ProfileWeekProgram2 {
		t.Errorf("profile=%v ok=%v", p, ok)
	}
}

func TestClimateSharesSetpointInstanceWithChannel(t *testing.T) {
	// Verifies the core invariant: the Climate's setpoint pointer IS
	// the channel's setpoint pointer — no duplicate instance.
	r := newRig(t, "x", KindIP, &stubWriter{}, custom.ClimateCapabilities{})
	chDP := r.channel.Parameter(hmenum.ParameterSetPointTemperature)
	if chDP == nil {
		t.Fatal("channel must expose SET_POINT_TEMPERATURE")
	}
	if any(r.climate.setpoint) != any(chDP) {
		t.Fatalf("Climate.setpoint must be the same instance as channel parameter")
	}
}

func TestClimateRFHeatMode(t *testing.T) {
	w := &stubWriter{}
	r := newRig(t, "x", KindRF, w, custom.ClimateCapabilities{MinTemperature: 4.5, MaxTemperature: 30.5})
	if err := r.climate.SetMode(context.Background(), ModeHeat, hmenum.CommandPriorityHigh); err != nil {
		t.Fatal(err)
	}
	// No setpoint observed; min_temp equals the off-sentinel (4.5), so
	// temperatureForHeatMode returns off-sentinel + step = 5.0.
	got := w.last()
	if got.param != hmenum.ParameterManuMode {
		t.Fatalf("RF HEAT: param=%v, want MANU_MODE", got.param)
	}
	if got.value.(float64) != 5.0 {
		t.Fatalf("RF HEAT: value=%v, want 5.0 (off-sentinel+step, no prior setpoint)", got.value)
	}
}
