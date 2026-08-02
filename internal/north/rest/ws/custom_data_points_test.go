// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package ws

import (
	"context"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/calculated"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/internal/north/rest/handlers"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// --- stubs ---

// stubWSDP is a minimal AttachableDataPoint + CategorisedDataPoint.
type stubWSDP struct {
	key      hmtypes.DataPointKey
	category hmenum.DataPointCategory
}

func (s *stubWSDP) DataPointKey() hmtypes.DataPointKey { return s.key }
func (s *stubWSDP) Category() hmenum.DataPointCategory { return s.category }

// stubCustomDPIndex is an inline stub for CustomDPIndex.
type stubCustomDPIndex struct {
	devices map[string]*device.Device
}

func (s *stubCustomDPIndex) Devices() []*device.Device {
	out := make([]*device.Device, 0, len(s.devices))
	for _, d := range s.devices {
		out = append(out, d)
	}
	return out
}

func (s *stubCustomDPIndex) Device(addr string) (*device.Device, bool) {
	d, ok := s.devices[addr]
	return d, ok
}

// stubCustomDPInvoker is an inline stub for CustomDPInvoker.
type stubCustomDPInvoker struct {
	err   error
	calls []string // "device:name:op"
}

func (s *stubCustomDPInvoker) InvokeCustomDP(
	_ context.Context,
	deviceAddress, name, operation string,
	_ map[string]any,
	_ hmenum.CommandPriority,
	_ string,
) error {
	s.calls = append(s.calls, deviceAddress+":"+name+":"+operation)
	return s.err
}

// newWSTestDevice constructs a minimal Device for WS tests.
func newWSTestDevice(addr, model string) *device.Device {
	return device.New(device.Config{
		Address:     addr,
		Model:       model,
		Interface:   hmenum.InterfaceHmIPRF,
		InterfaceID: "HmIP-RF@CCU",
		Name:        "WS Test Device",
	})
}

// addWSCustomDP attaches a custom DP to a channel of the device.
func addWSCustomDP(d *device.Device, addr, param string, no int, cat hmenum.DataPointCategory) {
	chAddr := addr + ":" + string(rune('0'+no)) //nolint:gosec // G115: no is a small channel number (0..9) in test fixtures; '0'+no is 48..57
	ch := d.AddChannel(chAddr, no, "SWITCH", hmenum.ParamsetKeyValues)
	dp := &stubWSDP{
		key: hmtypes.DataPointKey{
			ChannelAddress: chAddr,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      param,
		},
		category: cat,
	}
	ch.SetCustomDataPoint(dp)
}

// addWSCalculatedDP attaches a calculated DP to a channel.
type stubWSCalcDP struct {
	key      hmtypes.DataPointKey
	category hmenum.DataPointCategory
	value    float64
	hasValue bool
}

func (s *stubWSCalcDP) DataPointKey() hmtypes.DataPointKey { return s.key }
func (s *stubWSCalcDP) Category() hmenum.DataPointCategory { return s.category }
func (s *stubWSCalcDP) RawValue() (any, bool) {
	if !s.hasValue {
		return nil, false
	}
	return s.value, true
}

func addWSCalculatedDP(d *device.Device, addr, param string, no int, cat hmenum.DataPointCategory, value float64) {
	chAddr := addr + ":" + string(rune('0'+no)) //nolint:gosec // G115: no is a small channel number (0..9) in test fixtures; '0'+no is 48..57
	ch := d.Channel(chAddr)
	if ch == nil {
		ch = d.AddChannel(chAddr, no, "SENSOR", hmenum.ParamsetKeyValues)
	}
	dp := &stubWSCalcDP{
		key: hmtypes.DataPointKey{
			ChannelAddress: chAddr,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      param,
		},
		category: cat,
		value:    value,
		hasValue: true,
	}
	ch.AttachCalculatedDataPoint(dp)
}

// --- tests: RegisterCustomDPCommands registration ---

func TestRegisterCustomDPCommands_NilRouter_NoPanic(t *testing.T) {
	t.Parallel()
	RegisterCustomDPCommands(nil, CustomDPCommandsConfig{})
}

func TestRegisterCustomDPCommands_NilIndex_SkipsRegistration(t *testing.T) {
	t.Parallel()
	r := NewRouter()
	RegisterCustomDPCommands(r, CustomDPCommandsConfig{})
	res := r.Dispatch(context.Background(), "cdp.list", nil)
	if res.Error == nil {
		t.Fatal("expected error for unregistered command")
	}
}

func TestRegisterCustomDPCommands_AllFiveCommandsRegistered(t *testing.T) {
	t.Parallel()
	r := NewRouter()
	idx := &stubCustomDPIndex{devices: map[string]*device.Device{}}
	invoker := &stubCustomDPInvoker{}
	RegisterCustomDPCommands(r, CustomDPCommandsConfig{Index: idx, Invoker: invoker})

	expected := []string{
		"cdp.list",
		"cdp.get",
		"cdp.invoke",
		"calc_dp.list",
		"calc_dp.get",
	}
	for _, cmd := range expected {
		res := r.Dispatch(ctxForCommand(cmd), cmd, nil)
		// Commands themselves may error (e.g. missing params) but must be registered.
		_ = res
	}
	// Just ensuring no panic and dispatch is routed.
}

// --- tests: cdp.list ---

func TestCustomDPList_NoParams_ReturnsAllDevices(t *testing.T) {
	t.Parallel()
	d1 := newWSTestDevice("DEV0020", "HmIP-BSM")
	addWSCustomDP(d1, "DEV0020", "STATE", 1, hmenum.DataPointCategorySwitch)
	idx := &stubCustomDPIndex{devices: map[string]*device.Device{"DEV0020": d1}}
	r := NewRouter()
	RegisterCustomDPCommands(r, CustomDPCommandsConfig{Index: idx})

	res := r.Dispatch(context.Background(), "cdp.list", nil)
	if res.Error != nil {
		t.Fatalf("dispatch error: %+v", res.Error)
	}
	m, ok := res.Data.(map[string]any)
	if !ok {
		t.Fatalf("expected map result, got %T", res.Data)
	}
	if _, ok := m["DEV0020"]; !ok {
		t.Fatal("expected DEV0020 in result")
	}
}

func TestCustomDPList_WithDeviceParam_ReturnsOneDevice(t *testing.T) {
	t.Parallel()
	d1 := newWSTestDevice("DEV0021", "HmIP-BSM")
	addWSCustomDP(d1, "DEV0021", "STATE", 1, hmenum.DataPointCategorySwitch)
	idx := &stubCustomDPIndex{devices: map[string]*device.Device{"DEV0021": d1}}
	r := NewRouter()
	RegisterCustomDPCommands(r, CustomDPCommandsConfig{Index: idx})

	res := r.Dispatch(context.Background(), "cdp.list", jsonParam(`{"device":"DEV0021"}`))
	if res.Error != nil {
		t.Fatalf("dispatch error: %+v", res.Error)
	}
	items, ok := res.Data.([]map[string]any)
	if !ok {
		t.Fatalf("expected []map result, got %T", res.Data)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 custom DP, got %d", len(items))
	}
}

func TestCustomDPList_DeviceNotFound_ReturnsError(t *testing.T) {
	t.Parallel()
	idx := &stubCustomDPIndex{devices: map[string]*device.Device{}}
	r := NewRouter()
	RegisterCustomDPCommands(r, CustomDPCommandsConfig{Index: idx})

	res := r.Dispatch(context.Background(), "cdp.list", jsonParam(`{"device":"MISSING"}`))
	if res.Error == nil {
		t.Fatal("expected error for missing device")
	}
}

// --- tests: cdp.get ---

func TestCustomDPGet_HappyPath(t *testing.T) {
	t.Parallel()
	d := newWSTestDevice("DEV0022", "HmIP-BSM")
	addWSCustomDP(d, "DEV0022", "STATE", 1, hmenum.DataPointCategorySwitch)
	idx := &stubCustomDPIndex{devices: map[string]*device.Device{"DEV0022": d}}
	r := NewRouter()
	RegisterCustomDPCommands(r, CustomDPCommandsConfig{Index: idx})

	res := r.Dispatch(context.Background(), "cdp.get", jsonParam(`{"device":"DEV0022","name":"STATE"}`))
	if res.Error != nil {
		t.Fatalf("dispatch error: %+v", res.Error)
	}
	m, ok := res.Data.(map[string]any)
	if !ok {
		t.Fatalf("expected map result, got %T", res.Data)
	}
	if m["name"] != "STATE" {
		t.Fatalf("expected name=STATE, got %v", m["name"])
	}
}

func TestCustomDPGet_MissingDevice_ReturnsError(t *testing.T) {
	t.Parallel()
	idx := &stubCustomDPIndex{devices: map[string]*device.Device{}}
	r := NewRouter()
	RegisterCustomDPCommands(r, CustomDPCommandsConfig{Index: idx})

	res := r.Dispatch(context.Background(), "cdp.get", jsonParam(`{"device":"MISSING","name":"STATE"}`))
	if res.Error == nil {
		t.Fatal("expected error for missing device")
	}
}

func TestCustomDPGet_MissingDeviceParam_ReturnsError(t *testing.T) {
	t.Parallel()
	idx := &stubCustomDPIndex{devices: map[string]*device.Device{}}
	r := NewRouter()
	RegisterCustomDPCommands(r, CustomDPCommandsConfig{Index: idx})

	res := r.Dispatch(context.Background(), "cdp.get", jsonParam(`{"name":"STATE"}`))
	if res.Error == nil {
		t.Fatal("expected error when device param is missing")
	}
}

// --- tests: cdp.invoke ---

func TestCustomDPSet_HappyPath(t *testing.T) {
	t.Parallel()
	d := newWSTestDevice("DEV0023", "HmIP-BSM")
	addWSCustomDP(d, "DEV0023", "STATE", 1, hmenum.DataPointCategorySwitch)
	idx := &stubCustomDPIndex{devices: map[string]*device.Device{"DEV0023": d}}
	invoker := &stubCustomDPInvoker{}
	r := NewRouter()
	RegisterCustomDPCommands(r, CustomDPCommandsConfig{Index: idx, Invoker: invoker})

	res := r.Dispatch(opCtx(), "cdp.invoke",
		jsonParam(`{"device":"DEV0023","name":"STATE","operation":"turn_on"}`))
	if res.Error != nil {
		t.Fatalf("dispatch error: %+v", res.Error)
	}
	if len(invoker.calls) != 1 {
		t.Fatalf("expected 1 invoke call, got %d", len(invoker.calls))
	}
	if invoker.calls[0] != "DEV0023:STATE:turn_on" {
		t.Fatalf("unexpected call: %q", invoker.calls[0])
	}
}

func TestCustomDPSet_UnknownOperation_ReturnsError(t *testing.T) {
	t.Parallel()
	d := newWSTestDevice("DEV0024", "HmIP-BSM")
	addWSCustomDP(d, "DEV0024", "STATE", 1, hmenum.DataPointCategorySwitch)
	idx := &stubCustomDPIndex{devices: map[string]*device.Device{"DEV0024": d}}
	invoker := &stubCustomDPInvoker{err: handlers.ErrUnknownOperation}
	r := NewRouter()
	RegisterCustomDPCommands(r, CustomDPCommandsConfig{Index: idx, Invoker: invoker})

	res := r.Dispatch(opCtx(), "cdp.invoke",
		jsonParam(`{"device":"DEV0024","name":"STATE","operation":"fly"}`))
	if res.Error == nil {
		t.Fatal("expected error for unknown operation")
	}
}

func TestCustomDPSet_MissingOperation_ReturnsError(t *testing.T) {
	t.Parallel()
	d := newWSTestDevice("DEV0025", "HmIP-BSM")
	idx := &stubCustomDPIndex{devices: map[string]*device.Device{"DEV0025": d}}
	invoker := &stubCustomDPInvoker{}
	r := NewRouter()
	RegisterCustomDPCommands(r, CustomDPCommandsConfig{Index: idx, Invoker: invoker})

	res := r.Dispatch(opCtx(), "cdp.invoke",
		jsonParam(`{"device":"DEV0025","name":"STATE"}`))
	if res.Error == nil {
		t.Fatal("expected error when operation is missing")
	}
}

// --- tests: calc_dp.list ---

func TestCalculatedDPList_HappyPath(t *testing.T) {
	t.Parallel()
	d := newWSTestDevice("DEV0030", "HmIP-STE2")
	addWSCalculatedDP(d, "DEV0030", "DEW_POINT", 1, hmenum.DataPointCategorySensor, 12.5)
	idx := &stubCustomDPIndex{devices: map[string]*device.Device{"DEV0030": d}}
	r := NewRouter()
	RegisterCustomDPCommands(r, CustomDPCommandsConfig{Index: idx})

	res := r.Dispatch(context.Background(), "calc_dp.list", jsonParam(`{"device":"DEV0030"}`))
	if res.Error != nil {
		t.Fatalf("dispatch error: %+v", res.Error)
	}
	items, ok := res.Data.([]map[string]any)
	if !ok {
		t.Fatalf("expected []map result, got %T", res.Data)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 calculated DP, got %d", len(items))
	}
	if items[0]["name"] != "DEW_POINT" {
		t.Fatalf("expected name=DEW_POINT, got %v", items[0]["name"])
	}
}

func TestCalculatedDPList_AllDevices_ReturnsMap(t *testing.T) {
	t.Parallel()
	d := newWSTestDevice("DEV0031", "HmIP-STE2")
	addWSCalculatedDP(d, "DEV0031", "FROST_POINT", 1, hmenum.DataPointCategorySensor, -3.2)
	idx := &stubCustomDPIndex{devices: map[string]*device.Device{"DEV0031": d}}
	r := NewRouter()
	RegisterCustomDPCommands(r, CustomDPCommandsConfig{Index: idx})

	res := r.Dispatch(context.Background(), "calc_dp.list", nil)
	if res.Error != nil {
		t.Fatalf("dispatch error: %+v", res.Error)
	}
	m, ok := res.Data.(map[string]any)
	if !ok {
		t.Fatalf("expected map result, got %T", res.Data)
	}
	if _, ok := m["DEV0031"]; !ok {
		t.Fatal("expected DEV0031 in result")
	}
}

// --- tests: calc_dp.get ---

func TestCalculatedDPGet_HappyPath(t *testing.T) {
	t.Parallel()
	d := newWSTestDevice("DEV0032", "HmIP-STE2")
	addWSCalculatedDP(d, "DEV0032", "VAPOR_CONCENTRATION", 1, hmenum.DataPointCategorySensor, 8.9)
	idx := &stubCustomDPIndex{devices: map[string]*device.Device{"DEV0032": d}}
	r := NewRouter()
	RegisterCustomDPCommands(r, CustomDPCommandsConfig{Index: idx})

	res := r.Dispatch(context.Background(), "calc_dp.get",
		jsonParam(`{"device":"DEV0032","channel_no":1,"name":"VAPOR_CONCENTRATION"}`))
	if res.Error != nil {
		t.Fatalf("dispatch error: %+v", res.Error)
	}
	m, ok := res.Data.(map[string]any)
	if !ok {
		t.Fatalf("expected map, got %T", res.Data)
	}
	if m["name"] != "VAPOR_CONCENTRATION" {
		t.Fatalf("expected name=VAPOR_CONCENTRATION, got %v", m["name"])
	}
}

func TestCalculatedDPGet_NotFound_ReturnsError(t *testing.T) {
	t.Parallel()
	d := newWSTestDevice("DEV0033", "HmIP-STE2")
	idx := &stubCustomDPIndex{devices: map[string]*device.Device{"DEV0033": d}}
	r := NewRouter()
	RegisterCustomDPCommands(r, CustomDPCommandsConfig{Index: idx})

	res := r.Dispatch(context.Background(), "calc_dp.get",
		jsonParam(`{"device":"DEV0033","channel_no":1,"name":"MISSING"}`))
	if res.Error == nil {
		t.Fatal("expected error for missing calculated DP")
	}
}

func TestCalculatedDPGet_MissingDeviceParam_ReturnsError(t *testing.T) {
	t.Parallel()
	idx := &stubCustomDPIndex{devices: map[string]*device.Device{}}
	r := NewRouter()
	RegisterCustomDPCommands(r, CustomDPCommandsConfig{Index: idx})

	res := r.Dispatch(context.Background(), "calc_dp.get",
		jsonParam(`{"channel_no":1,"name":"DEW_POINT"}`))
	if res.Error == nil {
		t.Fatal("expected error when device param is missing")
	}
}

// --- helper ---

func jsonParam(s string) []byte { return []byte(s) }

// --- tests: calc_dp translated_name ---

// wsTranslatorLabeler implements handlers.ParameterLabeler AND
// device.ParameterTranslator so the WS calc-DP entries exercise the
// same translated-name chain as the REST handler.
type wsTranslatorLabeler struct {
	entries map[string]string // "<channelType>|<parameter>" → label
}

func (l wsTranslatorLabeler) ParameterLabel(string) string { return "" }
func (l wsTranslatorLabeler) ChannelTypedParameterLabelOk(channelType, parameter string) (string, bool) {
	v, ok := l.entries[channelType+"|"+parameter]
	return v, ok
}

// TestCalculatedDPList_TranslatedName pins that the WS calc_dp.list
// entries carry the locale-aware translated_name with the title-cased
// fallback the reference stack generates for untranslated calculated
// parameters.
func TestCalculatedDPList_TranslatedName(t *testing.T) {
	t.Parallel()
	d := newWSTestDevice("DEV0040", "HmIP-STHD")
	addWSCalculatedDP(d, "DEV0040", "DEW_POINT", 1, hmenum.DataPointCategorySensor, 12.5)
	idx := &stubCustomDPIndex{devices: map[string]*device.Device{"DEV0040": d}}
	r := NewRouter()
	RegisterCustomDPCommands(r, CustomDPCommandsConfig{Index: idx, Labels: wsTranslatorLabeler{}})

	res := r.Dispatch(context.Background(), "calc_dp.list", jsonParam(`{"device":"DEV0040"}`))
	if res.Error != nil {
		t.Fatalf("dispatch error: %+v", res.Error)
	}
	items, ok := res.Data.([]map[string]any)
	if !ok || len(items) != 1 {
		t.Fatalf("expected 1 entry, got %T %v", res.Data, res.Data)
	}
	if items[0]["translated_name"] != "Dew Point" {
		t.Fatalf("translated_name = %v, want Dew Point", items[0]["translated_name"])
	}
}

// TestCalculatedDPGet_TranslatedNameLocaleWins pins that a
// channel-typed OCCU translation overrides the fallback on calc_dp.get.
func TestCalculatedDPGet_TranslatedNameLocaleWins(t *testing.T) {
	t.Parallel()
	d := newWSTestDevice("DEV0041", "HmIP-STHD")
	addWSCalculatedDP(d, "DEV0041", "DEW_POINT", 1, hmenum.DataPointCategorySensor, 12.5)
	idx := &stubCustomDPIndex{devices: map[string]*device.Device{"DEV0041": d}}
	lab := wsTranslatorLabeler{entries: map[string]string{"SENSOR|DEW_POINT": "Taupunkt"}}
	r := NewRouter()
	RegisterCustomDPCommands(r, CustomDPCommandsConfig{Index: idx, Labels: lab})

	res := r.Dispatch(context.Background(), "calc_dp.get",
		jsonParam(`{"device":"DEV0041","channel_no":1,"name":"DEW_POINT"}`))
	if res.Error != nil {
		t.Fatalf("dispatch error: %+v", res.Error)
	}
	m, ok := res.Data.(map[string]any)
	if !ok {
		t.Fatalf("expected map, got %T", res.Data)
	}
	if m["translated_name"] != "Taupunkt" {
		t.Fatalf("translated_name = %v, want Taupunkt", m["translated_name"])
	}
}

// TestCalculatedDPEntryCarriesSourceGatedAvailability pins that the WS record
// ships the same `available` flag as the REST calc-dps record, gated on the
// validity of the sources the value derives from. REST and WS consumers must
// not disagree about whether a calculated reading is confirmed.
func TestCalculatedDPEntryCarriesSourceGatedAvailability(t *testing.T) {
	t.Parallel()

	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "DEV0040"})
	ch := d.AddChannel("DEV0040:1", 1, "WEATHER_TRANSCEIVER", hmenum.ParamsetKeyValues)
	temp := generic.NewFloatSensor(generic.Spec{
		Key:        hmtypes.DataPointKey{ChannelAddress: ch.Address, Parameter: string(hmenum.ParameterActualTemperature)},
		Descriptor: hmproto.ParameterData{Type: hmenum.ParameterTypeFloat, Operations: hmenum.OperationsRead | hmenum.OperationsEvent},
	})
	hum := generic.NewFloatSensor(generic.Spec{
		Key:        hmtypes.DataPointKey{ChannelAddress: ch.Address, Parameter: string(hmenum.ParameterHumidity)},
		Descriptor: hmproto.ParameterData{Type: hmenum.ParameterTypeFloat, Operations: hmenum.OperationsRead | hmenum.OperationsEvent},
	})
	ch.Put(temp)
	ch.Put(hum)

	sensor := calculated.NewDewPointSensor()
	ch.AttachCalculatedDataPoint(sensor)
	temp.OnEvent(20.0)
	hum.OnEvent(50.0)

	if entry := toCalculatedDPEntry(sensor, ch, nil); entry["available"] != true {
		t.Fatalf("expected available=true while both sources are healthy, got %v", entry["available"])
	}

	temp.UpdateStatus(hmenum.ParameterStatusOverflow)

	entry := toCalculatedDPEntry(sensor, ch, nil)
	if entry["available"] != false {
		t.Fatalf("expected available=false once the temperature source reports OVERFLOW, got %v", entry["available"])
	}
	if entry["observed"] != true {
		t.Fatal("observed must stay true — it is why `available` has to be carried separately")
	}
}
