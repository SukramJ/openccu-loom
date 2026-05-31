// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package rest

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/internal/north/rest/handlers"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

type fakeDeviceIndex struct{ devices map[string]*device.Device }

func (f *fakeDeviceIndex) Devices() []*device.Device {
	out := make([]*device.Device, 0, len(f.devices))
	for _, d := range f.devices {
		out = append(out, d)
	}
	return out
}

func (f *fakeDeviceIndex) Device(addr string) (*device.Device, bool) {
	d, ok := f.devices[addr]
	return d, ok
}

func (f *fakeDeviceIndex) CentralOf(addr string) string {
	if _, ok := f.devices[addr]; ok {
		return "ccu-test"
	}
	return ""
}

// fakeChannelWriter satisfies [device.ChannelWriter] and records every
// SetValue / PutParamset call made through the channel's installed writer.
type fakeChannelWriter struct {
	calls atomic.Int32
	last  struct {
		address   string
		parameter hmenum.Parameter
		value     any
		priority  hmenum.CommandPriority
	}
	err error
}

func (f *fakeChannelWriter) SetValue(_ context.Context, addr string, p hmenum.Parameter, v any, prio hmenum.CommandPriority) error {
	f.calls.Add(1)
	f.last.address = addr
	f.last.parameter = p
	f.last.value = v
	f.last.priority = prio
	return f.err
}

func (f *fakeChannelWriter) PutParamset(_ context.Context, _ string, _ hmenum.ParamsetKey, _ map[string]any, _ hmenum.CommandPriority) error {
	return f.err
}

// newDeviceWithDP builds a test device with one channel and one writable
// boolean parameter. When chWriter is non-nil it is installed on the
// channel as the ChannelWriter so Channel.Set dispatches through it.
func newDeviceWithDP(t *testing.T, chWriter device.ChannelWriter) *device.Device {
	t.Helper()
	d := device.New(device.Config{
		InterfaceID: "HmIP-RF",
		Interface:   hmenum.InterfaceHmIPRF,
		Address:     "0001ABCD",
		Model:       "HmIP-STH",
		Name:        "Wohnzimmer",
		Updatable:   true,
	})
	ch := d.AddChannel("0001ABCD:1", 1, "", hmenum.ParamsetKeyValues)
	if chWriter != nil {
		ch.SetWriter(chWriter)
	}
	dp := generic.NewBinarySensor(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: "0001ABCD:1",
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(hmenum.ParameterState),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeBool,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent,
		},
	})
	dp.OnEvent(true)
	ch.Put(dp)
	return d
}

// newDeviceRouter builds a test HTTP router. writer is forwarded to
// Deps.DPWriter for legacy compat; chWriter is installed on the test
// channel so Channel.Set has a dispatching backend.
func newDeviceRouter(t *testing.T, writer handlers.DataPointWriter, chWriter device.ChannelWriter) http.Handler {
	t.Helper()
	idx := &fakeDeviceIndex{devices: map[string]*device.Device{"0001ABCD": newDeviceWithDP(t, chWriter)}}
	return NewRouter(Deps{Devices: idx, DPWriter: writer})
}

func TestListDevices(t *testing.T) {
	r := newDeviceRouter(t, nil, nil)
	req := httptest.NewRequest("GET", "/api/v1/devices", http.NoBody)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != 200 {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	var body struct {
		Items []handlers.DeviceSummary `json:"items"`
		Total int                      `json:"total"`
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &body)
	if body.Total != 1 || len(body.Items) != 1 || body.Items[0].Address != "0001ABCD" {
		t.Fatalf("body=%+v", body)
	}
	if rr.Header().Get("X-Total-Count") != "1" {
		t.Fatalf("total header=%s", rr.Header().Get("X-Total-Count"))
	}
}

func TestListDevicesFilters(t *testing.T) {
	cases := []struct {
		name  string
		query string
		want  int
	}{
		{"interface match", "?interface=HmIP-RF", 1},
		{"interface miss", "?interface=BidCos-RF", 0},
		{"interface lower", "?interface=hmip-rf", 1},
		{"model match", "?model=HmIP-STH", 1},
		{"model miss", "?model=ZZZ", 0},
		{"name match", "?name=wohnzimmer", 1},
		{"name miss", "?name=garage", 0},
		{"address match", "?address=0001abcd", 1},
		{"address miss", "?address=ffffffff", 0},
		{"combined", "?interface=HmIP-RF&model=STH&name=wohn", 1},
		{"combined miss", "?interface=HmIP-RF&model=ZZZ", 0},
	}
	r := newDeviceRouter(t, nil, nil)
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/api/v1/devices"+c.query, http.NoBody)
			rr := httptest.NewRecorder()
			r.ServeHTTP(rr, req)
			if rr.Code != 200 {
				t.Fatalf("status=%d", rr.Code)
			}
			var body struct {
				Items []handlers.DeviceSummary `json:"items"`
				Total int                      `json:"total"`
			}
			_ = json.Unmarshal(rr.Body.Bytes(), &body)
			if body.Total != c.want {
				t.Fatalf("query %q: got total=%d, want %d", c.query, body.Total, c.want)
			}
			if len(body.Items) != c.want {
				t.Fatalf("query %q: got items=%d, want %d", c.query, len(body.Items), c.want)
			}
		})
	}
}

func TestGetDeviceDetail(t *testing.T) {
	r := newDeviceRouter(t, nil, nil)
	req := httptest.NewRequest("GET", "/api/v1/devices/0001ABCD", http.NoBody)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != 200 {
		t.Fatalf("status=%d", rr.Code)
	}
	var body handlers.DeviceDetail
	_ = json.Unmarshal(rr.Body.Bytes(), &body)
	if body.Address != "0001ABCD" || len(body.Channels) != 1 {
		t.Fatalf("body=%+v", body)
	}
}

func TestGetDeviceNotFound(t *testing.T) {
	r := newDeviceRouter(t, nil, nil)
	req := httptest.NewRequest("GET", "/api/v1/devices/MISSING", http.NoBody)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != 404 {
		t.Fatalf("status=%d", rr.Code)
	}
}

func TestListDataPoints(t *testing.T) {
	r := newDeviceRouter(t, nil, nil)
	req := httptest.NewRequest("GET", "/api/v1/devices/0001ABCD/channels/1/data-points", http.NoBody)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != 200 {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	var body []handlers.DataPointSummary
	_ = json.Unmarshal(rr.Body.Bytes(), &body)
	if len(body) != 1 || body[0].Parameter != "STATE" || body[0].Observed != true {
		t.Fatalf("body=%+v", body)
	}
}

func TestGetDataPoint(t *testing.T) {
	r := newDeviceRouter(t, nil, nil)
	req := httptest.NewRequest("GET", "/api/v1/devices/0001ABCD/channels/1/data-points/STATE", http.NoBody)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != 200 {
		t.Fatalf("status=%d", rr.Code)
	}
}

// TestPutDataPointValue verifies that PutDataPointValue routes through
// the channel's installed ChannelWriter (via Channel.Set) rather than
// calling the legacy DataPointWriter directly.
func TestPutDataPointValue(t *testing.T) {
	chw := &fakeChannelWriter{}
	r := newDeviceRouter(t, nil, chw) // chw is installed on the channel
	req := httptest.NewRequest(http.MethodPut,
		"/api/v1/devices/0001ABCD/channels/1/data-points/STATE/value",
		strings.NewReader(`{"value": false, "priority":"high"}`))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	// The route must have dispatched through the channel writer, not the
	// legacy DataPointWriter.
	if chw.calls.Load() != 1 {
		t.Fatalf("channel writer calls=%d, want 1", chw.calls.Load())
	}
	if chw.last.address != "0001ABCD:1" {
		t.Fatalf("channel writer address=%q, want 0001ABCD:1", chw.last.address)
	}
	if chw.last.parameter != hmenum.ParameterState {
		t.Fatalf("channel writer parameter=%q", chw.last.parameter)
	}
	if chw.last.priority != hmenum.CommandPriorityHigh {
		t.Fatalf("channel writer priority=%d, want high", chw.last.priority)
	}
}

// TestPutDataPointValueFloatAcceptsIntegerJSON locks the coercion bug
// fix: a Float-typed LEVEL parameter receiving the integer-valued JSON
// number `1` must land on the wire as a float64, not collapse to int
// (which the descriptor validator would reject with "want float, got
// int" → 502). The fix routes the body through parameter.Coerce, which
// respects the descriptor's declared type.
func TestPutDataPointValueFloatAcceptsIntegerJSON(t *testing.T) {
	chw := &fakeChannelWriter{}
	d := device.New(device.Config{
		InterfaceID: "HmIP-RF",
		Interface:   hmenum.InterfaceHmIPRF,
		Address:     "0001ABCD",
		Model:       "HmIP-BDT",
	})
	ch := d.AddChannel("0001ABCD:4", 4, "", hmenum.ParamsetKeyValues)
	ch.SetWriter(chw)
	dp := generic.NewFloat(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: "0001ABCD:4",
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(hmenum.ParameterLevel),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeFloat,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent,
			Min:        json.RawMessage(`0.0`),
			Max:        json.RawMessage(`1.0`),
		},
	})
	ch.Put(dp)
	idx := &fakeDeviceIndex{devices: map[string]*device.Device{"0001ABCD": d}}
	r := NewRouter(Deps{Devices: idx})

	req := httptest.NewRequest(http.MethodPut,
		"/api/v1/devices/0001ABCD/channels/4/data-points/LEVEL/value",
		strings.NewReader(`{"value": 1}`))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("status=%d body=%s — integer JSON on a Float DP must be coerced, not 502", rr.Code, rr.Body.String())
	}
	if chw.calls.Load() != 1 {
		t.Fatalf("writer calls=%d, want 1", chw.calls.Load())
	}
	// Wire-side value must be float, not int, so the XML-RPC encoder
	// emits <double> not <int> — CCU rejects type-mismatched LEVELs.
	if _, ok := chw.last.value.(float64); !ok {
		t.Fatalf("wire value type=%T (%v), want float64", chw.last.value, chw.last.value)
	}
}

func TestPutDataPointValueBadJSON(t *testing.T) {
	// No channel writer needed — bad JSON is rejected before dispatching.
	r := newDeviceRouter(t, nil, &fakeChannelWriter{})
	req := httptest.NewRequest(http.MethodPut,
		"/api/v1/devices/0001ABCD/channels/1/data-points/STATE/value",
		strings.NewReader(`{not json}`))
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status=%d", rr.Code)
	}
}

// TestPutDataPointValueMissingWriter verifies that 503 is returned when
// no ChannelWriter has been installed on the channel (i.e. the channel
// was not hydrated yet). Previously the test checked for a nil
// DataPointWriter — now the 503 originates from ErrNoChannelWriter.
func TestPutDataPointValueMissingWriter(t *testing.T) {
	// Pass nil chWriter so the channel has no installed ChannelWriter.
	r := newDeviceRouter(t, nil, nil)
	req := httptest.NewRequest(http.MethodPut,
		"/api/v1/devices/0001ABCD/channels/1/data-points/STATE/value",
		strings.NewReader(`{"value":true}`))
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d, want 503 (ErrNoChannelWriter)", rr.Code)
	}
}

func TestGetChannelNotFound(t *testing.T) {
	r := newDeviceRouter(t, nil, nil)
	req := httptest.NewRequest("GET", "/api/v1/devices/0001ABCD/channels/99", http.NoBody)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != 404 {
		t.Fatalf("status=%d", rr.Code)
	}
}
