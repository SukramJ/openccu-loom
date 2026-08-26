// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// Tests for the resolution of the operator-config parameters that live on the
// device-root MASTER paramset of classic RF thermostats — TEMPERATURE_OFFSET
// (HM-CC-RT-DN, HM-TC-IT-WM-W-EU) and WEEK_PROGRAM_POINTER (HM-TC-IT-WM-W-EU,
// HM-CC-VG-1). Both the read/subscribe side and the write side must target the
// owning channel's MASTER paramset, not the climate channel's VALUES paramset.
package climate

import (
	"context"
	"encoding/json"
	"maps"
	"sync"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/internal/payload"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// recordingParamsetWriter captures both SetValue and PutParamset calls with
// their full target (address + paramset) so a test can assert exactly which
// paramset and channel a write landed on.
type recordingParamsetWriter struct {
	mu   sync.Mutex
	sets []recordedSet
	puts []recordedPut
}

type recordedSet struct {
	address string
	param   hmenum.Parameter
	value   any
}

type recordedPut struct {
	address  string
	paramset hmenum.ParamsetKey
	values   map[string]any
}

func (w *recordingParamsetWriter) SetValue(_ context.Context, address string, p hmenum.Parameter, v any, _ hmenum.CommandPriority) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.sets = append(w.sets, recordedSet{address: address, param: p, value: v})
	return nil
}

func (w *recordingParamsetWriter) PutParamset(_ context.Context, address string, paramset hmenum.ParamsetKey, values map[string]any, _ hmenum.CommandPriority) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	cp := make(map[string]any, len(values))
	maps.Copy(cp, values)
	w.puts = append(w.puts, recordedPut{address: address, paramset: paramset, values: cp})
	return nil
}

func (w *recordingParamsetWriter) putsSnapshot() []recordedPut {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := make([]recordedPut, len(w.puts))
	copy(out, w.puts)
	return out
}

// putFloatMaster attaches a FLOAT data point to the channel's MASTER paramset.
func putFloatMaster(ch *device.Channel, param hmenum.Parameter) *generic.Float {
	dp := generic.NewFloat(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: ch.Address,
			ParamsetKey:    hmenum.ParamsetKeyMaster,
			Parameter:      string(param),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeFloat,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite,
		},
	})
	ch.PutMaster(dp)
	return dp
}

// putSelectMaster attaches an ENUM data point to the channel's MASTER
// paramset, carrying the real 15-entry TEMPERATURE_OFFSET VALUE_LIST the
// classic RF family (HM-CC-RT-DN, HM-TC-IT-WM-W-EU) advertises on the
// device-root MASTER paramset.
func putSelectMaster(ch *device.Channel, param hmenum.Parameter) *generic.Select {
	dp := generic.NewSelect(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: ch.Address,
			ParamsetKey:    hmenum.ParamsetKeyMaster,
			Parameter:      string(param),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeEnum,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite,
			Min:        json.RawMessage("0"),
			Max:        json.RawMessage("14"),
			ValueList: []string{
				"-3.5K", "-3.0K", "-2.5K", "-2.0K", "-1.5K", "-1.0K", "-0.5K",
				"0.0K", "0.5K", "1.0K", "1.5K", "2.0K", "2.5K", "3.0K", "3.5K",
			},
		},
	})
	ch.PutMaster(dp)
	return dp
}

// putPointerMaster attaches an INTEGER WEEK_PROGRAM_POINTER data point to the
// channel's MASTER paramset with a 0..2 descriptor (three week programs, the
// HM-TC-IT-WM-W-EU / HM-CC-VG-1 shape).
func putPointerMaster(ch *device.Channel, param hmenum.Parameter) *generic.Integer {
	dp := generic.NewInteger(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: ch.Address,
			ParamsetKey:    hmenum.ParamsetKeyMaster,
			Parameter:      string(param),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeInteger,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite,
			Min:        json.RawMessage("0"),
			Max:        json.RawMessage("2"),
		},
	})
	ch.PutMaster(dp)
	return dp
}

// newClassicRFDevice builds an HM-CC-RT-DN / HM-TC-IT-WM-W-EU-shaped device:
// a climate channel carrying the VALUES-paramset runtime parameters and a
// device-root channel carrying the MASTER-paramset operator-config parameters
// (TEMPERATURE_OFFSET / WEEK_PROGRAM_POINTER).
func newClassicRFDevice(t *testing.T, deviceAddr, climateAddr string, w Writer) (climateCh, root *device.Channel) {
	t.Helper()
	d := device.New(device.Config{InterfaceID: "HmRF", Address: deviceAddr})
	climateCh = d.AddChannel(climateAddr, 2, "CLIMATE", hmenum.ParamsetKeyValues)

	// Setpoint on the climate channel so New can attach.
	setpoint := generic.NewFloat(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: climateAddr,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(hmenum.ParameterSetTemperature),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeFloat,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent,
		},
		Writer: w,
	})
	climateCh.Put(setpoint)

	root = d.EnsureRootChannel()
	return climateCh, root
}

// TestSetTemperatureOffsetWritesRootMaster pins that SetTemperatureOffset on a
// classic RF thermostat writes TEMPERATURE_OFFSET to the MASTER paramset of the
// device-root channel that carries it — not a VALUES setValue on the climate
// channel, which never reaches the thermostat.
func TestSetTemperatureOffsetWritesRootMaster(t *testing.T) {
	w := &recordingParamsetWriter{}
	climateCh, root := newClassicRFDevice(t, "VCU0000050", "VCU0000050:4", w)
	putFloatMaster(root, hmenum.ParameterTemperatureOffset)

	c := New(Config{Channel: climateCh, Writer: w, Kind: KindRF})
	if err := c.SetTemperatureOffset(context.Background(), "1.5", hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("SetTemperatureOffset: %v", err)
	}

	puts := w.putsSnapshot()
	if len(puts) != 1 {
		t.Fatalf("expected 1 put_paramset, got %d (sets=%d)", len(puts), len(w.sets))
	}
	got := puts[0]
	if got.paramset != hmenum.ParamsetKeyMaster {
		t.Errorf("offset write paramset=%v, want MASTER", got.paramset)
	}
	if got.address != root.Address {
		t.Errorf("offset write address=%q, want device-root %q", got.address, root.Address)
	}
	if _, ok := got.values[string(hmenum.ParameterTemperatureOffset)]; !ok {
		t.Errorf("offset write values=%v, missing TEMPERATURE_OFFSET", got.values)
	}
}

// TestSubscribeTemperatureOffsetRootMaster pins that Subscribe observes
// TEMPERATURE_OFFSET when it lives on the device-root MASTER paramset (classic
// RF), so TemperatureOffset() and the state payload surface the value.
func TestSubscribeTemperatureOffsetRootMaster(t *testing.T) {
	w := &recordingParamsetWriter{}
	climateCh, root := newClassicRFDevice(t, "VCU0000341", "VCU0000341:2", w)
	offset := putFloatMaster(root, hmenum.ParameterTemperatureOffset)

	c := New(Config{Channel: climateCh, Writer: w, Kind: KindRF})
	cancel := c.Subscribe(climateCh)
	defer cancel()

	offset.OnEvent(2.0)

	v, ok := c.TemperatureOffset()
	if !ok || v != "2.0" {
		t.Fatalf("TemperatureOffset() = (%q, %v), want (\"2.0\", true)", v, ok)
	}
	sp, _ := c.State().(*payload.ClimateState)
	if sp == nil || sp.TemperatureOffset == nil {
		t.Fatalf("state payload should carry temperature_offset once observed on the root MASTER paramset")
	}
}

// TestSubscribeTemperatureOffsetRootMasterEnum pins that Subscribe resolves
// TEMPERATURE_OFFSET through its VALUE_LIST label rather than reporting the
// raw ENUM index. On the classic RF family (HM-CC-RT-DN, HM-TC-IT-WM-W-EU)
// the device-root TEMPERATURE_OFFSET is an ENUM, not a FLOAT — its wire
// value is a 0-based VALUE_LIST index, exactly like HEATING_COOLING — so
// a bare type-assertion / stringification reports the index ("7") instead
// of the label ("0.0K").
func TestSubscribeTemperatureOffsetRootMasterEnum(t *testing.T) {
	w := &recordingParamsetWriter{}
	climateCh, root := newClassicRFDevice(t, "VCU0000342", "VCU0000342:2", w)
	offset := putSelectMaster(root, hmenum.ParameterTemperatureOffset)

	c := New(Config{Channel: climateCh, Writer: w, Kind: KindRF})
	cancel := c.Subscribe(climateCh)
	defer cancel()

	// Index 7 is "0.0K" in the real 15-entry VALUE_LIST.
	offset.OnEvent(int32(7))

	v, ok := c.TemperatureOffset()
	if !ok || v != "0.0K" {
		t.Fatalf("TemperatureOffset() = (%q, %v), want (\"0.0K\", true)", v, ok)
	}
	sp, _ := c.State().(*payload.ClimateState)
	if sp == nil || sp.TemperatureOffset == nil || *sp.TemperatureOffset != "0.0K" {
		t.Fatalf("state payload temperature_offset should report the ENUM label, got %+v", sp)
	}
}

// TestSetProfileRFWritesPointerToRootMaster pins that SetProfile on a classic
// RF thermostat splits its writes: AUTO_MODE + BOOST_MODE go to the climate
// channel's VALUES paramset, while WEEK_PROGRAM_POINTER goes to the device-root
// MASTER paramset that carries it. No single put_paramset mixes the two
// paramsets.
func TestSetProfileRFWritesPointerToRootMaster(t *testing.T) {
	w := &recordingParamsetWriter{}
	climateCh, root := newClassicRFDevice(t, "VCU0000341", "VCU0000341:2", w)
	putPointerMaster(root, hmenum.ParameterWeekProgramPointer)

	c := New(Config{Channel: climateCh, Writer: w, Kind: KindRF, Capabilities: custom.ClimateCapabilities{SupportsProfile: true}})
	if err := c.SetProfile(context.Background(), ProfileWeekProgram2, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("SetProfile: %v", err)
	}

	var valuesPut, masterPut *recordedPut
	for i := range w.putsSnapshot() {
		p := w.puts[i]
		switch p.paramset {
		case hmenum.ParamsetKeyValues:
			valuesPut = &w.puts[i]
		case hmenum.ParamsetKeyMaster:
			masterPut = &w.puts[i]
		default:
			// Only VALUES and MASTER are expected here.
		}
		// No put may carry parameters from both paramsets.
		_, hasPointer := p.values[string(hmenum.ParameterWeekProgramPointer)]
		_, hasAuto := p.values[string(hmenum.ParameterAutoMode)]
		if hasPointer && hasAuto {
			t.Fatalf("a single put mixed MASTER pointer with VALUES mode flags: %v", p.values)
		}
	}
	if valuesPut == nil {
		t.Fatal("expected a VALUES put_paramset carrying AUTO_MODE/BOOST_MODE")
	}
	if valuesPut.address != climateCh.Address {
		t.Errorf("VALUES put address=%q, want climate channel %q", valuesPut.address, climateCh.Address)
	}
	if _, ok := valuesPut.values[string(hmenum.ParameterAutoMode)]; !ok {
		t.Errorf("VALUES put missing AUTO_MODE: %v", valuesPut.values)
	}
	if _, ok := valuesPut.values[string(hmenum.ParameterWeekProgramPointer)]; ok {
		t.Errorf("VALUES put must NOT carry WEEK_PROGRAM_POINTER: %v", valuesPut.values)
	}
	if masterPut == nil {
		t.Fatal("expected a MASTER put_paramset carrying WEEK_PROGRAM_POINTER")
	}
	if masterPut.address != root.Address {
		t.Errorf("MASTER put address=%q, want device-root %q", masterPut.address, root.Address)
	}
	if v, ok := masterPut.values[string(hmenum.ParameterWeekProgramPointer)]; !ok || v != "WEEK PROGRAM 2" {
		t.Errorf("MASTER put WEEK_PROGRAM_POINTER=%v, want \"WEEK PROGRAM 2\"", masterPut.values)
	}
}

// TestSubscribeWeekProgramPointerRootMaster pins that Subscribe observes
// WEEK_PROGRAM_POINTER when it lives on the device-root MASTER paramset, so the
// active week program is recovered and the profile projects to it in AUTO
// mode. With the pre-fix VALUES-only resolution the pointer is never observed
// and the profile stays none.
func TestSubscribeWeekProgramPointerRootMaster(t *testing.T) {
	w := &recordingParamsetWriter{}
	climateCh, root := newClassicRFDevice(t, "VCU0000341", "VCU0000341:2", w)
	pointer := putPointerMaster(root, hmenum.ParameterWeekProgramPointer)
	controlMode := putIntegerDP(climateCh, hmenum.ParameterControlMode)

	c := New(Config{Channel: climateCh, Writer: w, Kind: KindRF})
	cancel := c.Subscribe(climateCh)
	defer cancel()

	// WEEK_PROGRAM_POINTER=1 (0-based) -> week_program_2; then CONTROL_MODE=0
	// (AUTO-MODE) recovers the profile from the cached pointer index.
	pointer.OnEvent(int32(1))
	controlMode.OnEvent(int32(0))

	m, mOK := c.Mode()
	if !mOK || m != ModeAuto {
		t.Fatalf("Mode() = (%v, %v), want (auto, true)", m, mOK)
	}
	p, pOK := c.Profile()
	if !pOK || p != ProfileWeekProgram2 {
		t.Fatalf("Profile() = (%v, %v), want (week_program_2, true) — pointer on root MASTER was not observed", p, pOK)
	}
}
