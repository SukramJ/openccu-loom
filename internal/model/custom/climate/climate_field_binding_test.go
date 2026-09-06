// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package climate_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/internal/model/custom/climate"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// recordingWriter captures the (channelAddress, parameter) a write was
// dispatched to, which is the whole point of the HM-CC-TC assertions
// below: the setpoint lives on a different channel than the thermostat.
type recordingWriter struct {
	mu      sync.Mutex
	address string
	param   hmenum.Parameter
	value   any
}

func (w *recordingWriter) SetValue(
	_ context.Context,
	channelAddress string,
	parameter hmenum.Parameter,
	value any,
	_ hmenum.CommandPriority,
) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.address, w.param, w.value = channelAddress, parameter, value
	return nil
}

func (w *recordingWriter) target() (address string, parameter hmenum.Parameter, value any) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.address, w.param, w.value
}

// bindingDiscoveryCtx renders topics that carry the channel address so a
// test can tell "declared under the custom DP's own channel" apart from
// "declared under the channel the parameter is published on".
type bindingDiscoveryCtx struct{ channelAddress string }

func (bindingDiscoveryCtx) CustomDPStateTopic() string { return "test/custom/state" }
func (bindingDiscoveryCtx) ServiceMethodCommandTopic(method string) string {
	return "test/svc/" + method + "/set"
}

func (c bindingDiscoveryCtx) WireParameterCommandTopic(parameter string) string {
	return "test/" + c.channelAddress + "/" + parameter + "/set"
}

func (c bindingDiscoveryCtx) WireParameterStateTopic(parameter string) string {
	return "test/" + c.channelAddress + "/" + parameter
}

func (bindingDiscoveryCtx) WireParameterStateTopicOn(channelAddress, parameter string) string {
	return "test/" + channelAddress + "/" + parameter
}

// putWireDP registers one wire data point on ch, choosing the generic
// shape the device pipeline would resolve for the descriptor.
func putWireDP(
	t *testing.T,
	ch *device.Channel,
	parameter string,
	typ hmenum.ParameterType,
	ops hmenum.Operations,
	writer generic.Writer,
) {
	t.Helper()
	spec := generic.Spec{
		Key: hmtypes.DataPointKey{
			InterfaceID:    "BidCos-RF",
			ChannelAddress: ch.Address,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      parameter,
		},
		Descriptor: hmproto.ParameterData{
			Type:       typ,
			Operations: ops,
			Min:        json.RawMessage("0"),
			Max:        json.RawMessage("100"),
		},
		Writer: writer,
	}
	switch kind := generic.ResolveDataPointKind(generic.ResolveInput{Parameter: parameter, Descriptor: spec.Descriptor}); kind {
	case generic.KindSensor:
		if typ == hmenum.ParameterTypeFloat {
			ch.Put(generic.NewFloatSensor(spec))
			return
		}
		ch.Put(generic.NewIntegerSensor(spec))
	case generic.KindNumberFloat:
		ch.Put(generic.NewFloat(spec))
	case generic.KindNumberInteger:
		ch.Put(generic.NewInteger(spec))
	default:
		t.Fatalf("unexpected resolved kind %q for %s", kind, parameter)
	}
}

// newSimpleRfThermostatDevice builds an HM-CC-TC the way the CCU reports
// it: the WEATHER channel 1 carries TEMPERATURE + HUMIDITY, the
// CLIMATECONTROL_REGULATOR channel 2 carries SETPOINT. Neither parameter
// name nor channel matches what the HmIP and classic-RF thermostats use,
// which is exactly why the binding has to come from the profile schema.
func newSimpleRfThermostatDevice(t *testing.T, writer generic.Writer) (dev *device.Device, channels map[int]*device.Channel) {
	t.Helper()
	dev = device.New(device.Config{
		InterfaceID:  "BidCos-RF",
		Interface:    hmenum.InterfaceBidCosRF,
		Address:      "LEQ0123456",
		Model:        "HM-CC-TC",
		ProductGroup: hmenum.ProductGroupHM,
	})
	types := map[int]string{
		0: "MAINTENANCE",
		1: "WEATHER",
		2: "CLIMATECONTROL_REGULATOR",
		3: "WINDOW_SWITCH_RECEIVER",
	}
	chs := make(map[int]*device.Channel, len(types))
	for no, typ := range types {
		chs[no] = dev.AddChannel(fmt.Sprintf("LEQ0123456:%d", no), no, typ, hmenum.ParamsetKeyValues)
	}
	ro := hmenum.OperationsRead | hmenum.OperationsEvent
	rw := hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent
	putWireDP(t, chs[1], "TEMPERATURE", hmenum.ParameterTypeFloat, ro, nil)
	putWireDP(t, chs[1], "HUMIDITY", hmenum.ParameterTypeInteger, ro, nil)
	putWireDP(t, chs[2], "SETPOINT", hmenum.ParameterTypeFloat, rw, writer)

	if err := custom.CreateCustomDataPoints(dev, custom.DefaultRegistry()); err != nil {
		t.Fatalf("materialize custom data points: %v", err)
	}
	return dev, chs
}

func climateOn(t *testing.T, ch *device.Channel) *climate.Climate {
	t.Helper()
	cdp := ch.CustomDataPoint()
	if cdp == nil {
		t.Fatalf("no custom data point on %s", ch.Address)
	}
	c, ok := cdp.(*climate.Climate)
	if !ok {
		t.Fatalf("custom data point on %s is %T, want *climate.Climate", ch.Address, cdp)
	}
	return c
}

// TestSimpleRfThermostatBindsSetpointFromSiblingChannel pins the HM-CC-TC
// binding end-to-end through the real profile registry: the thermostat is
// materialised on the WEATHER channel, and both its wire values resolve
// through the profile schema — SETPOINT from the *regulator* channel, the
// current temperature from TEMPERATURE rather than ACTUAL_TEMPERATURE.
//
// Resolving parameter names on the custom DP's own channel bound neither,
// so the thermostat reported no temperature at all and every write went
// to a parameter the device does not have.
func TestSimpleRfThermostatBindsSetpointFromSiblingChannel(t *testing.T) {
	t.Parallel()
	writer := &recordingWriter{}
	_, chs := newSimpleRfThermostatDevice(t, writer)

	if cdp := chs[2].CustomDataPoint(); cdp != nil {
		t.Fatalf("regulator channel must not host a custom DP, got %T", cdp)
	}
	c := climateOn(t, chs[1])

	// Feed both wire values and read them back through the aggregate.
	chs[1].Parameter(hmenum.ParameterTemperature).(*generic.Sensor[float64]).OnEvent(19.5)
	chs[1].Parameter(hmenum.ParameterHumidity).(*generic.Sensor[int32]).OnEvent(52)
	chs[2].Parameter(hmenum.ParameterSetpoint).(*generic.Float).OnEvent(21.0)

	if got, ok := c.CurrentTemperature(); !ok || got != 19.5 {
		t.Errorf("CurrentTemperature() = (%v, %v), want (19.5, true) — TEMPERATURE on the weather channel", got, ok)
	}
	if got, ok := c.Setpoint(); !ok || got != 21.0 {
		t.Errorf("Setpoint() = (%v, %v), want (21, true) — SETPOINT on the regulator channel", got, ok)
	}
	if got, ok := c.Humidity(); !ok || got != 52 {
		t.Errorf("Humidity() = (%v, %v), want (52, true) — HUMIDITY on the weather channel", got, ok)
	}

	// The custom DP's identity stays on the channel it is attached to —
	// the REST/WS `cdps` surface and the MQTT unique_id derive from it.
	if got := c.DataPointKey().ChannelAddress; got != chs[1].Address {
		t.Errorf("DataPointKey().ChannelAddress = %q, want the attaching channel %q", got, chs[1].Address)
	}
	if got := c.DataPointKey().Parameter; got != string(hmenum.ParameterSetpoint) {
		t.Errorf("DataPointKey().Parameter = %q, want %q", got, hmenum.ParameterSetpoint)
	}
}

// TestSimpleRfThermostatWritesSetpointToRegulatorChannel pins the write
// half: set_temperature has to reach SETPOINT on the regulator channel.
func TestSimpleRfThermostatWritesSetpointToRegulatorChannel(t *testing.T) {
	t.Parallel()
	writer := &recordingWriter{}
	_, chs := newSimpleRfThermostatDevice(t, writer)
	c := climateOn(t, chs[1])

	if err := c.SetTemperature(context.Background(), 22.5, hmenum.CommandPriorityCritical); err != nil {
		t.Fatalf("SetTemperature: %v", err)
	}
	addr, param, value := writer.target()
	if addr != chs[2].Address {
		t.Errorf("write went to %q, want the regulator channel %q", addr, chs[2].Address)
	}
	if param != hmenum.ParameterSetpoint {
		t.Errorf("write parameter = %q, want %q", param, hmenum.ParameterSetpoint)
	}
	if v, ok := value.(float64); !ok || v != 22.5 {
		t.Errorf("written value = %v (%T), want 22.5", value, value)
	}
}

// TestSimpleRfThermostatDiscoveryNamesPublishedTopics is the
// declared-equals-published half: HA reads the current temperature and
// the setpoint from per-parameter slot topics, and those are published
// under the channel the parameter lives on. A payload that declares them
// under the thermostat's own channel names topics nothing ever writes,
// and both fields stay empty in HA forever.
func TestSimpleRfThermostatDiscoveryNamesPublishedTopics(t *testing.T) {
	t.Parallel()
	_, chs := newSimpleRfThermostatDevice(t, nil)
	c := climateOn(t, chs[1])

	comp, body := c.HADiscoveryPayload(bindingDiscoveryCtx{channelAddress: chs[1].Address})
	if comp != "climate" {
		t.Fatalf("component = %q, want climate", comp)
	}
	want := map[string]string{
		"current_temperature_topic": "test/" + chs[1].Address + "/TEMPERATURE",
		"temperature_state_topic":   "test/" + chs[2].Address + "/SETPOINT",
		"current_humidity_topic":    "test/" + chs[1].Address + "/HUMIDITY",
	}
	for key, expected := range want {
		got, ok := body[key].(string)
		if !ok {
			t.Errorf("%s missing from discovery payload", key)
			continue
		}
		if got != expected {
			t.Errorf("%s = %q, want %q", key, got, expected)
		}
	}
}

// TestRfThermostatBindsActualHumidity covers the second device family the
// schema and the code disagreed on: the classic RF wall thermostats
// publish ACTUAL_HUMIDITY on the thermostat channel, not HUMIDITY, so a
// fixed parameter name left HM-TC-IT-WM-W-EU and HM-CC-VG-1 without a
// humidity reading while every other field bound correctly.
func TestRfThermostatBindsActualHumidity(t *testing.T) {
	t.Parallel()
	dev := device.New(device.Config{
		InterfaceID:  "BidCos-RF",
		Interface:    hmenum.InterfaceBidCosRF,
		Address:      "MEQ0987654",
		Model:        "HM-TC-IT-WM-W-EU",
		ProductGroup: hmenum.ProductGroupHM,
	})
	chs := make(map[int]*device.Channel, 3)
	for no := range 3 {
		chs[no] = dev.AddChannel(fmt.Sprintf("MEQ0987654:%d", no), no, "T", hmenum.ParamsetKeyValues)
	}
	ro := hmenum.OperationsRead | hmenum.OperationsEvent
	rw := hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent
	putWireDP(t, chs[1], "TEMPERATURE", hmenum.ParameterTypeFloat, ro, nil)
	putWireDP(t, chs[1], "HUMIDITY", hmenum.ParameterTypeInteger, ro, nil)
	putWireDP(t, chs[2], "ACTUAL_TEMPERATURE", hmenum.ParameterTypeFloat, ro, nil)
	putWireDP(t, chs[2], "ACTUAL_HUMIDITY", hmenum.ParameterTypeInteger, ro, nil)
	putWireDP(t, chs[2], "SET_TEMPERATURE", hmenum.ParameterTypeFloat, rw, nil)

	if err := custom.CreateCustomDataPoints(dev, custom.DefaultRegistry()); err != nil {
		t.Fatalf("materialize custom data points: %v", err)
	}
	c := climateOn(t, chs[2])

	chs[2].Parameter(hmenum.ParameterActualHumidity).(*generic.Sensor[int32]).OnEvent(47)
	if got, ok := c.Humidity(); !ok || got != 47 {
		t.Errorf("Humidity() = (%v, %v), want (47, true) — ACTUAL_HUMIDITY on the thermostat channel", got, ok)
	}
	// The thermostat channel's own weather sibling must not be picked up
	// instead: the schema maps humidity to the thermostat channel only.
	keys := c.SubDataPointKeys()
	for _, k := range keys {
		if strings.HasSuffix(k.ChannelAddress, ":1") {
			t.Errorf("slot %v resolved to the weather channel; schema maps every field to the thermostat channel", k)
		}
	}
}
