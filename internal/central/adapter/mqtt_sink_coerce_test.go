// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// mqtt_sink_coerce_test.go pins that MQTTCommandSink coerces a
// descriptor-blind topic payload against the resolved parameter's
// descriptor before it reaches the wire — the same first step the REST
// PUT /value path takes. Without it an ENUM select write forwards the
// option label instead of its integer index, a whole-number FLOAT lands
// as an int, and out-of-range / not-in-VALUE_LIST values reach the CCU
// unchecked.

package adapter

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// coerceSinkFixture registers one central carrying one device with a single
// channel that has an ENUM VALUES parameter (BEHAVIOUR, VALUE_LIST
// [ANALOG_OUTPUT, DIGITAL_OUTPUT]) and a FLOAT VALUES parameter
// (TEMPERATURE, MIN 0 / MAX 100). The channel's write path is the recording
// writer it returns.
func coerceSinkFixture(t *testing.T, name string) (*MQTTCommandSink, *sinkChannelWriter) {
	t.Helper()
	c, err := central.New(central.Config{Name: name})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	reg := central.NewRegistry()
	if err := reg.Register(c); err != nil {
		t.Fatalf("reg.Register: %v", err)
	}
	const chAddr = "COERCEDEV:1"
	cw := &sinkChannelWriter{}
	d := device.New(device.Config{
		Address:     "COERCEDEV",
		InterfaceID: "HmIP-RF",
		Interface:   hmenum.InterfaceHmIPRF,
		Model:       "HmIP-MIO",
	})
	ch := d.AddChannel(chAddr, 1, "MULTI_MODE_INPUT_TRANSMITTER", hmenum.ParamsetKeyValues)

	behaviour := generic.NewSelect(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: chAddr,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      "BEHAVIOUR",
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeEnum,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent,
			ValueList:  []string{"ANALOG_OUTPUT", "DIGITAL_OUTPUT"},
		},
		Writer: cw,
	})
	ch.Put(behaviour)

	temperature := generic.NewFloat(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: chAddr,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      "TEMPERATURE",
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeFloat,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent,
			Min:        json.RawMessage("0.0"),
			Max:        json.RawMessage("100.0"),
		},
		Writer: cw,
	})
	ch.Put(temperature)

	ch.SetWriter(cw)
	c.ModelRegistry.Put(d)

	return NewMQTTCommandSink(reg, &fakeWriter{}), cw
}

// TestMQTTCommandSinkSetValueCoercesEnumLabelToIndex is the canonical enum
// defect: Home Assistant publishes the option label ("DIGITAL_OUTPUT"), but
// the CCU expects the 0-based VALUE_LIST index. The sink must map the label
// to its index before the wire.
func TestMQTTCommandSinkSetValueCoercesEnumLabelToIndex(t *testing.T) {
	t.Parallel()
	s, cw := coerceSinkFixture(t, "ccu-enum")

	if err := s.SetValue(context.Background(), "ccu-enum", "HmIP-RF", "COERCEDEV:1",
		hmenum.Parameter("BEHAVIOUR"), "DIGITAL_OUTPUT", hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("SetValue: %v", err)
	}
	param, value, calls := cw.snapshot()
	if calls != 1 {
		t.Fatalf("expected 1 wire write, got %d", calls)
	}
	if param != hmenum.Parameter("BEHAVIOUR") {
		t.Errorf("write carried parameter %q, want BEHAVIOUR", param)
	}
	iv, ok := value.(int)
	if !ok || iv != 1 {
		t.Fatalf("write carried %T(%v); the enum label must be coerced to the integer index 1", value, value)
	}
}

// TestMQTTCommandSinkSetValueCoercesWholeNumberFloat pins that a whole-number
// payload for a FLOAT parameter reaches the wire as a float64, not the int64
// that parseCommandPayload produces for "21".
func TestMQTTCommandSinkSetValueCoercesWholeNumberFloat(t *testing.T) {
	t.Parallel()
	s, cw := coerceSinkFixture(t, "ccu-float")

	// int64(21) is exactly what the MQTT command subscriber's
	// parseCommandPayload hands the sink for the payload "21".
	if err := s.SetValue(context.Background(), "ccu-float", "HmIP-RF", "COERCEDEV:1",
		hmenum.Parameter("TEMPERATURE"), int64(21), hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("SetValue: %v", err)
	}
	_, value, calls := cw.snapshot()
	if calls != 1 {
		t.Fatalf("expected 1 wire write, got %d", calls)
	}
	fv, ok := value.(float64)
	if !ok || fv != 21 {
		t.Fatalf("write carried %T(%v); a whole-number FLOAT must be coerced to float64(21)", value, value)
	}
}

// TestMQTTCommandSinkSetValueRejectsOutOfRange pins that a FLOAT value above
// MAX is rejected client-side and never reaches the wire.
func TestMQTTCommandSinkSetValueRejectsOutOfRange(t *testing.T) {
	t.Parallel()
	s, cw := coerceSinkFixture(t, "ccu-range")

	err := s.SetValue(context.Background(), "ccu-range", "HmIP-RF", "COERCEDEV:1",
		hmenum.Parameter("TEMPERATURE"), int64(150), hmenum.CommandPriorityHigh)
	if err == nil {
		t.Fatal("SetValue with a value above MAX must be rejected")
	}
	if _, _, calls := cw.snapshot(); calls != 0 {
		t.Errorf("out-of-range value still dispatched %d wire writes", calls)
	}
}

// TestMQTTCommandSinkSetValueRejectsUnknownEnumLabel pins that an enum label
// absent from VALUE_LIST is rejected client-side and never reaches the wire.
func TestMQTTCommandSinkSetValueRejectsUnknownEnumLabel(t *testing.T) {
	t.Parallel()
	s, cw := coerceSinkFixture(t, "ccu-badenum")

	err := s.SetValue(context.Background(), "ccu-badenum", "HmIP-RF", "COERCEDEV:1",
		hmenum.Parameter("BEHAVIOUR"), "NONEXISTENT", hmenum.CommandPriorityHigh)
	if err == nil {
		t.Fatal("SetValue with a label not in VALUE_LIST must be rejected")
	}
	if _, _, calls := cw.snapshot(); calls != 0 {
		t.Errorf("unknown enum label still dispatched %d wire writes", calls)
	}
}

// TestMQTTCommandSinkSetMasterValueCoercesAndValidates pins that the MASTER
// write path also coerces against the descriptor and validates the range:
// a whole-number payload reaches the wire as a float, and a value above MAX
// is rejected before dispatch.
func TestMQTTCommandSinkSetMasterValueCoercesAndValidates(t *testing.T) {
	t.Parallel()
	c, err := central.New(central.Config{Name: "ccu-master-coerce"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	reg := central.NewRegistry()
	if err := reg.Register(c); err != nil {
		t.Fatalf("reg.Register: %v", err)
	}
	const chAddr = "MASTERDEV:1"
	const paramName = "TEMPERATURE_OFFSET"
	cw := &sinkChannelWriter{}
	d := device.New(device.Config{
		Address:     "MASTERDEV",
		InterfaceID: "HmIP-RF",
		Interface:   hmenum.InterfaceHmIPRF,
		Model:       "HmIP-eTRV",
	})
	ch := d.AddChannel(chAddr, 1, "HEATING_CLIMATECONTROL_TRANSCEIVER", hmenum.ParamsetKeyValues)
	masterDP := generic.NewFloat(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: chAddr,
			ParamsetKey:    hmenum.ParamsetKeyMaster,
			Parameter:      paramName,
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeFloat,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite,
			Min:        json.RawMessage("0.0"),
			Max:        json.RawMessage("10.0"),
		},
		Writer: cw,
	})
	ch.PutMaster(masterDP)
	ch.SetWriter(cw)
	c.ModelRegistry.Put(d)

	s := NewMQTTCommandSink(reg, nil)

	// Whole-number payload → the MASTER put_paramset must carry a float.
	if err := s.SetMasterValue(context.Background(), "ccu-master-coerce", "HmIP-RF", chAddr,
		hmenum.Parameter(paramName), int64(3), hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("SetMasterValue: %v", err)
	}
	key, vals, calls := cw.putSnapshot()
	if calls != 1 {
		t.Fatalf("expected 1 PutParamset call, got %d", calls)
	}
	if key != hmenum.ParamsetKeyMaster {
		t.Errorf("paramset key: got %q want MASTER", key)
	}
	fv, ok := vals[paramName].(float64)
	if !ok || fv != 3 {
		t.Fatalf("MASTER write carried %T(%v); a whole-number FLOAT must be coerced to float64(3)", vals[paramName], vals[paramName])
	}

	// Out-of-range MASTER value → rejected by Validate, no further dispatch.
	err = s.SetMasterValue(context.Background(), "ccu-master-coerce", "HmIP-RF", chAddr,
		hmenum.Parameter(paramName), int64(50), hmenum.CommandPriorityHigh)
	if err == nil {
		t.Fatal("SetMasterValue with a value above MAX must be rejected")
	}
	if _, _, calls := cw.putSnapshot(); calls != 1 {
		t.Errorf("out-of-range MASTER value triggered an extra dispatch; put calls = %d, want 1", calls)
	}
}
