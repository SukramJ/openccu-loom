// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/internal/north/mqtt"
	"github.com/SukramJ/openccu-loom/internal/north/rest/handlers"
	"github.com/SukramJ/openccu-loom/internal/north/rest/ws"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// crossPlaneDeviceIndex is the minimal [handlers.DeviceIndex] stub needed
// to drive the real REST GetDataPoint handler in
// [TestEventBridgeAndRESTDisplayValueAgree] — deliberately not reusing
// generic.DisplayValue itself, so the test proves the two production
// call sites agree rather than proving a formula agrees with itself.
type crossPlaneDeviceIndex struct {
	d *device.Device
}

func (s crossPlaneDeviceIndex) Devices() []*device.Device { return []*device.Device{s.d} }

func (s crossPlaneDeviceIndex) Device(address string) (*device.Device, bool) {
	if address == s.d.Address {
		return s.d, true
	}
	return nil, false
}

func (s crossPlaneDeviceIndex) CentralOf(string) string { return "ccu-01" }

func (s crossPlaneDeviceIndex) SerialSuffix(string) string { return "" }

// registryWithLevelDevice builds on [registryWithDevice] with one LEVEL
// Float data point on channel :1 (descriptor UNIT "100%", so
// Multiplier() == 100) — the same wire shape the REST-side pin uses
// (newDeviceWithLevelDP in internal/north/rest/handlers/devices_test.go),
// so both planes' pins exercise the identical parameter.
func registryWithLevelDevice(t *testing.T) (*central.Registry, *central.Unit, *device.Device) {
	t.Helper()
	reg, d := registryWithDevice(t)
	chAddr := d.Address + ":1"
	ch := d.AddChannel(chAddr, 1, "BLIND", hmenum.ParamsetKeyValues)
	dp := generic.NewFloat(generic.Spec{
		Key: hmtypes.DataPointKey{
			InterfaceID:    "HmIP-RF",
			ChannelAddress: chAddr,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(hmenum.ParameterLevel),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeFloat,
			Unit:       "100%",
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent,
		},
	})
	ch.Put(dp)
	u, ok := reg.Get("ccu-01")
	if !ok {
		t.Fatal("central ccu-01 not registered")
	}
	return reg, u, d
}

func (s crossPlaneDeviceIndex) Released(string) bool { return true }

// TestEventBridgeValueChanged_LevelDisplayValue is the WS-plane pin for
// the DisplayValue projection plus the MQTT double-scaling tripwire in
// one test, driven through the real EventBridge (not by calling
// generic.DisplayValue directly):
//
//   - the WebSocket data_point.value_changed push must carry
//     display_value 42 for a LEVEL data point observed at the raw wire
//     value 0.42;
//   - the MQTT raw-plane slot state for the same change must still carry
//     the untouched 0.42 — HA's applyMultiplierSensor value_template
//     (internal/north/mqtt/discovery.go) already scales 0.42 -> 42 %, so
//     publishing a pre-scaled value here would double-apply to 4200 %.
func TestEventBridgeValueChanged_LevelDisplayValue(t *testing.T) {
	t.Parallel()
	reg, unit, d := registryWithLevelDevice(t)

	wsHub := ws.NewHub()
	pub := mqtt.NewNoopClient()
	bridge := mqtt.NewBridge(mqtt.BridgeConfig{Base: "openccu-loom", CentralName: "ccu-01", RawEnabled: true}, pub)
	eb := NewEventBridge(reg, wsHub, mqtt.NewWiring(bridge, nil))
	eb.Start(context.Background())
	defer eb.Stop()

	events.Publish(unit.EventBus, hmevent.DataPointValueChangedEvent{
		Base: hmevent.NewBase(),
		Key: hmtypes.DataPointKey{
			InterfaceID:    "HmIP-RF",
			ChannelAddress: d.Address + ":1",
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      "LEVEL",
		},
		NewValue: hmtypes.FloatValue(0.42),
	})
	eb.Flush()

	// --- WS pin ---
	var payload *ws.DataPointValueChangedPayload
	for _, e := range wsHub.Replay(0, nil).Events {
		if e.Type != string(hmevent.EventTypeDataPointValueChanged) {
			continue
		}
		p, ok := e.Payload.(ws.DataPointValueChangedPayload)
		if !ok || p.Parameter != "LEVEL" {
			continue
		}
		payload = &p
	}
	if payload == nil {
		t.Fatal("no WS data_point.value_changed push for LEVEL")
	}
	if fv, ok := payload.Value.(float64); !ok || fv != 0.42 {
		t.Fatalf("WS value = %#v, want the untouched raw wire value 0.42", payload.Value)
	}
	dv, ok := payload.DisplayValue.(float64)
	if !ok || dv != 42.0 {
		t.Fatalf("WS display_value = %#v, want float64(42)", payload.DisplayValue)
	}

	// --- MQTT raw-plane guard ---
	found := false
	for _, p := range pub.Published() {
		// The per-parameter slot-state topic ends in the bare parameter
		// name (…/values/LEVEL); its /config companion carries the
		// static descriptor (unit, multiplier, …) under a disjoint JSON
		// shape with no "value" key, so the decode below naturally skips it.
		if !strings.HasSuffix(p.Topic, "/LEVEL") {
			continue
		}
		var state struct {
			Value        json.Number `json:"value"`
			DisplayValue *float64    `json:"display_value"`
		}
		if err := json.Unmarshal(p.Payload, &state); err != nil {
			continue
		}
		f, convErr := state.Value.Float64()
		if convErr != nil {
			continue
		}
		found = true
		if f != 0.42 {
			t.Errorf("MQTT slot state %q value = %v, want the untouched raw wire value 0.42", p.Topic, f)
		}
		if state.DisplayValue != nil {
			t.Errorf("MQTT slot state %q carries display_value=%v — the raw plane must stay unscaled", p.Topic, *state.DisplayValue)
		}
	}
	if !found {
		t.Fatal("no MQTT slot state publish carried the raw LEVEL value")
	}
}

// TestEventBridgeAndRESTDisplayValueAgree is the cross-plane agreement
// pin: the REST data-point summary and the WebSocket value-changed push
// must report the identical display_value for the same data point,
// because a client seeds its state from REST and updates it from the WS
// stream — a mismatch would make the reading visibly jump on the first
// push.
func TestEventBridgeAndRESTDisplayValueAgree(t *testing.T) {
	t.Parallel()
	reg, unit, d := registryWithLevelDevice(t)
	ch := d.Channel(d.Address + ":1")
	dp := ch.Parameter(hmenum.ParameterLevel).(*generic.Float)
	dp.OnEvent(0.42)

	wsHub := ws.NewHub()
	eb := NewEventBridge(reg, wsHub, nil)
	eb.Start(context.Background())
	defer eb.Stop()

	events.Publish(unit.EventBus, hmevent.DataPointValueChangedEvent{
		Base: hmevent.NewBase(),
		Key: hmtypes.DataPointKey{
			InterfaceID:    "HmIP-RF",
			ChannelAddress: d.Address + ":1",
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      "LEVEL",
		},
		NewValue: hmtypes.FloatValue(0.42),
	})

	var wsDisplay any
	found := false
	for _, e := range wsHub.Replay(0, nil).Events {
		if e.Type != string(hmevent.EventTypeDataPointValueChanged) {
			continue
		}
		p, ok := e.Payload.(ws.DataPointValueChangedPayload)
		if !ok || p.Parameter != "LEVEL" {
			continue
		}
		wsDisplay = p.DisplayValue
		found = true
	}
	if !found {
		t.Fatal("no WS data_point.value_changed push for LEVEL")
	}
	if wsDisplay == nil {
		t.Fatal("WS data_point.value_changed push for LEVEL carries no display_value")
	}

	// REST side: render through the real GetDataPoint handler.
	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("addr", d.Address)
	rctx.URLParams.Add("no", "1")
	rctx.URLParams.Add("param", "LEVEL")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	w := httptest.NewRecorder()
	handlers.GetDataPoint(crossPlaneDeviceIndex{d: d}, nil).ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GetDataPoint: expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var restBody handlers.DataPointSummary
	if err := json.Unmarshal(w.Body.Bytes(), &restBody); err != nil {
		t.Fatalf("unmarshal REST body: %v", err)
	}

	restDisplay, ok := restBody.DisplayValue.(float64)
	if !ok {
		t.Fatalf("REST display_value = %#v, want a float64", restBody.DisplayValue)
	}
	if wsDisplay != restDisplay {
		t.Fatalf("WS display_value=%#v disagrees with REST display_value=%#v for the same LEVEL data point", wsDisplay, restDisplay)
	}
}
