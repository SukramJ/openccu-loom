// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/internal/north/filter"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// stubDeviceIndex is an inline stub for DeviceIndex.
type stubDeviceIndex struct {
	devices map[string]*device.Device
}

func (s *stubDeviceIndex) Devices() []*device.Device {
	out := make([]*device.Device, 0, len(s.devices))
	for _, d := range s.devices {
		out = append(out, d)
	}
	return out
}

func (s *stubDeviceIndex) Device(address string) (*device.Device, bool) {
	d, ok := s.devices[address]
	return d, ok
}

func (s *stubDeviceIndex) CentralOf(address string) string {
	if _, ok := s.devices[address]; ok {
		return "ccu-01"
	}
	return ""
}

func newTestDevice(addr, model string) *device.Device {
	return device.New(device.Config{
		Address:     addr,
		Model:       model,
		Interface:   hmenum.InterfaceHmIPRF,
		InterfaceID: "HmIP-RF@CCU",
		Name:        "Test Device",
	})
}

func TestListDevices_HappyPath(t *testing.T) {
	t.Parallel()
	d1 := newTestDevice("0001ABCD", "HmIP-BSM")
	idx := &stubDeviceIndex{
		devices: map[string]*device.Device{"0001ABCD": d1},
	}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/devices", http.NoBody)
	w := httptest.NewRecorder()
	ListDevices(idx).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body["total"].(float64) != 1 {
		t.Fatalf("expected total=1, got %v", body["total"])
	}
}

func TestListDevices_ModelFilter(t *testing.T) {
	t.Parallel()
	d1 := newTestDevice("0001ABCD", "HmIP-BSM")
	d2 := newTestDevice("0002ABCD", "HmIP-STE2")
	idx := &stubDeviceIndex{
		devices: map[string]*device.Device{
			"0001ABCD": d1,
			"0002ABCD": d2,
		},
	}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/devices?model=bsm", http.NoBody)
	w := httptest.NewRecorder()
	ListDevices(idx).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var body map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if body["total"].(float64) != 1 {
		t.Fatalf("expected total=1 after model filter, got %v", body["total"])
	}
}

func TestListDevices_XTotalCountHeader(t *testing.T) {
	t.Parallel()
	d1 := newTestDevice("0001ABCD", "HmIP-BSM")
	idx := &stubDeviceIndex{devices: map[string]*device.Device{"0001ABCD": d1}}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/devices", http.NoBody)
	w := httptest.NewRecorder()
	ListDevices(idx).ServeHTTP(w, req)

	if w.Header().Get("X-Total-Count") != "1" {
		t.Fatalf("expected X-Total-Count=1, got %q", w.Header().Get("X-Total-Count"))
	}
}

func TestGetDevice_HappyPath(t *testing.T) {
	t.Parallel()
	d := newTestDevice("0001ABCD", "HmIP-BSM")
	idx := &stubDeviceIndex{devices: map[string]*device.Device{"0001ABCD": d}}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/devices/0001ABCD", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "0001ABCD"}))
	w := httptest.NewRecorder()
	GetDevice(idx, nil).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var body DeviceDetail
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body.Address != "0001ABCD" {
		t.Fatalf("expected address=0001ABCD, got %q", body.Address)
	}
}

func TestGetDevice_NotFound_Returns404(t *testing.T) {
	t.Parallel()
	idx := &stubDeviceIndex{devices: map[string]*device.Device{}}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/devices/MISSING", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "MISSING"}))
	w := httptest.NewRecorder()
	GetDevice(idx, nil).ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestListChannels_HappyPath(t *testing.T) {
	t.Parallel()
	d := newTestDevice("0001ABCD", "HmIP-BSM")
	d.AddChannel("0001ABCD:0", 0, "MAINTENANCE", hmenum.ParamsetKeyValues)
	d.AddChannel("0001ABCD:1", 1, "SWITCH", hmenum.ParamsetKeyValues)
	idx := &stubDeviceIndex{devices: map[string]*device.Device{"0001ABCD": d}}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/devices/0001ABCD/channels", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "0001ABCD"}))
	w := httptest.NewRecorder()
	ListChannels(idx, nil).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var body []ChannelSummary
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if len(body) != 2 {
		t.Fatalf("expected 2 channels, got %d", len(body))
	}
}

// TestListChannels_CategoryField pins H-036: every ChannelSummary must carry
// a non-empty category field equal to the channel's OCCU Type string.
func TestListChannels_CategoryField(t *testing.T) {
	t.Parallel()
	d := newTestDevice("0001ABCD", "HmIP-BSM")
	d.AddChannel("0001ABCD:0", 0, "MAINTENANCE", hmenum.ParamsetKeyValues)
	d.AddChannel("0001ABCD:1", 1, "SWITCH_TRANSMITTER", hmenum.ParamsetKeyValues)
	idx := &stubDeviceIndex{devices: map[string]*device.Device{"0001ABCD": d}}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/devices/0001ABCD/channels", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "0001ABCD"}))
	w := httptest.NewRecorder()
	ListChannels(idx, nil).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var body []ChannelSummary
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, ch := range body {
		if ch.Category == "" {
			t.Errorf("channel %s: category must not be empty (H-036)", ch.Address)
		}
		if ch.Category != ch.Type {
			t.Errorf("channel %s: category=%q != type=%q (H-036)", ch.Address, ch.Category, ch.Type)
		}
	}
}

func TestListChannels_DeviceNotFound_Returns404(t *testing.T) {
	t.Parallel()
	idx := &stubDeviceIndex{devices: map[string]*device.Device{}}
	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "MISSING"}))
	w := httptest.NewRecorder()
	ListChannels(idx, nil).ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestListRooms_HappyPath(t *testing.T) {
	t.Parallel()
	d1 := newTestDevice("0001ABCD", "HmIP-BSM")
	d1.Rooms = []string{"Living Room", "Office"}
	d2 := newTestDevice("0002ABCD", "HmIP-STE2")
	d2.Rooms = []string{"Living Room"}
	idx := &stubDeviceIndex{
		devices: map[string]*device.Device{
			"0001ABCD": d1,
			"0002ABCD": d2,
		},
	}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/rooms", http.NoBody)
	w := httptest.NewRecorder()
	ListRooms(idx).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var body []RoomEntry
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if len(body) != 2 {
		t.Fatalf("expected 2 rooms, got %d: %+v", len(body), body)
	}
	// Living Room appears on 2 devices.
	for _, r := range body {
		if r.Name == "Living Room" && r.DeviceCount != 2 {
			t.Fatalf("Living Room device count mismatch: %d", r.DeviceCount)
		}
	}
}

func TestListRooms_NilIndex_ReturnsEmptyArray(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/rooms", http.NoBody)
	w := httptest.NewRecorder()
	ListRooms(nil).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var body []RoomEntry
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if len(body) != 0 {
		t.Fatalf("expected empty, got %+v", body)
	}
}

func TestListFunctions_HappyPath(t *testing.T) {
	t.Parallel()
	d := newTestDevice("0001ABCD", "HmIP-BSM")
	d.Functions = []string{"Lighting", "Security"}
	idx := &stubDeviceIndex{devices: map[string]*device.Device{"0001ABCD": d}}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/functions", http.NoBody)
	w := httptest.NewRecorder()
	ListFunctions(idx).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var body []FunctionEntry
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if len(body) != 2 {
		t.Fatalf("expected 2 functions, got %d", len(body))
	}
}

func TestRefreshDevices_ServiceNil_Returns503(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/devices/refresh", http.NoBody)
	w := httptest.NewRecorder()
	RefreshDevices(nil).ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", w.Code)
	}
}

// ---------------------------------------------------------------------------
// Cluster — ListDataPoints outbound visibility filter (ADR 0005)
// ---------------------------------------------------------------------------

// stubVisibilitySet is a test double for filter.VisibilitySet that blocks
// every parameter in its `hidden` set.
type stubVisibilitySet struct {
	hidden map[hmenum.Parameter]bool
}

func newStubVis(hidden ...hmenum.Parameter) filter.VisibilitySet {
	m := make(map[hmenum.Parameter]bool, len(hidden))
	for _, p := range hidden {
		m[p] = true
	}
	return &stubVisibilitySet{hidden: m}
}

func (s *stubVisibilitySet) Visible(_, _ string, _ hmenum.ParamsetKey, p hmenum.Parameter) bool {
	return !s.hidden[p]
}

func (s *stubVisibilitySet) VisibleForChannel(_, _ string, _ int, _ hmenum.ParamsetKey, p hmenum.Parameter) bool {
	return !s.hidden[p]
}

// newTestChannelDevice builds a Device at "0001ABCD" whose channel 1
// carries the given parameter names as VALUES data points.
func newTestChannelDevice(t *testing.T, params ...hmenum.Parameter) *device.Device {
	t.Helper()
	d := device.New(device.Config{
		Address:     "0001ABCD",
		Model:       "HmIP-STH",
		Interface:   hmenum.InterfaceHmIPRF,
		InterfaceID: "HmIP-RF@CCU",
		Name:        "Test",
	})
	ch := d.AddChannel("0001ABCD:1", 1, "TRANSCEIVER", hmenum.ParamsetKeyValues)
	for _, p := range params {
		dp := generic.NewBinarySensor(generic.Spec{
			Key: hmtypes.DataPointKey{
				ChannelAddress: "0001ABCD:1",
				ParamsetKey:    hmenum.ParamsetKeyValues,
				Parameter:      string(p),
			},
			Descriptor: hmproto.ParameterData{
				Type:       hmenum.ParameterTypeBool,
				Operations: hmenum.OperationsRead | hmenum.OperationsEvent,
			},
		})
		dp.OnEvent(true)
		ch.Put(dp)
	}
	return d
}

// TestVisibilityFilterAppliedAtRESTListDPs verifies that ListDataPoints with
// a non-nil VisibilitySet only returns parameters in the visible-set by default,
// and returns all parameters when ?include=all is appended.
//
// This is the ADR-0005 contract test for the REST outbound filter.
func TestVisibilityFilterAppliedAtRESTListDPs(t *testing.T) {
	t.Parallel()

	const (
		visibleParam hmenum.Parameter = "STATE"
		hiddenParam  hmenum.Parameter = "ON_TIME_LIST_1"
	)

	idx := &stubDeviceIndex{
		devices: map[string]*device.Device{
			"0001ABCD": newTestChannelDevice(t, visibleParam, hiddenParam),
		},
	}
	vis := newStubVis(hiddenParam)
	handler := ListDataPoints(idx, nil, vis)

	// --- default: hidden parameter must NOT appear ---
	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "0001ABCD", "no": "1"}))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var body []DataPointSummary
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(body) != 1 {
		t.Fatalf("default filter: expected 1 DP (visible only), got %d: %+v", len(body), body)
	}
	if body[0].Parameter != string(visibleParam) {
		t.Fatalf("default filter: expected visible param %q, got %q", visibleParam, body[0].Parameter)
	}

	// --- ?include=all: both parameters must appear ---
	req2 := httptest.NewRequest(http.MethodGet, "/?include=all", http.NoBody)
	req2 = req2.WithContext(chiContext(req2, map[string]string{"addr": "0001ABCD", "no": "1"}))
	w2 := httptest.NewRecorder()
	handler.ServeHTTP(w2, req2)

	if w2.Code != http.StatusOK {
		t.Fatalf("include=all: expected 200, got %d body=%s", w2.Code, w2.Body.String())
	}
	var body2 []DataPointSummary
	if err := json.Unmarshal(w2.Body.Bytes(), &body2); err != nil {
		t.Fatalf("include=all unmarshal: %v", err)
	}
	if len(body2) != 2 {
		t.Fatalf("include=all: expected 2 DPs, got %d: %+v", len(body2), body2)
	}
}

// TestListDataPoints_NilVisReturnsAll verifies that a nil VisibilitySet
// returns every parameter regardless (backward-compat no-op behavior).
func TestListDataPoints_NilVisReturnsAll(t *testing.T) {
	t.Parallel()

	idx := &stubDeviceIndex{
		devices: map[string]*device.Device{
			"0001ABCD": newTestChannelDevice(t, hmenum.ParameterState, hmenum.ParameterLevel),
		},
	}
	handler := ListDataPoints(idx, nil, nil) // nil vis = no filter

	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "0001ABCD", "no": "1"}))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var body []DataPointSummary
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if len(body) != 2 {
		t.Fatalf("nil vis: expected 2 DPs, got %d", len(body))
	}
}

// --- GetChannel ---

func TestGetChannel_HappyPath(t *testing.T) {
	t.Parallel()
	d := newTestDevice("0001ABCD", "HmIP-BSM")
	d.AddChannel("0001ABCD:0", 0, "MAINTENANCE", hmenum.ParamsetKeyValues)
	d.AddChannel("0001ABCD:1", 1, "SWITCH", hmenum.ParamsetKeyValues)
	idx := &stubDeviceIndex{devices: map[string]*device.Device{"0001ABCD": d}}

	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "0001ABCD", "no": "1"}))
	w := httptest.NewRecorder()
	GetChannel(idx, nil).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var body ChannelSummary
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body.Number != 1 {
		t.Fatalf("expected channel number 1, got %d", body.Number)
	}
}

func TestGetChannel_DeviceNotFound_Returns404(t *testing.T) {
	t.Parallel()
	idx := &stubDeviceIndex{devices: map[string]*device.Device{}}
	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "MISSING", "no": "1"}))
	w := httptest.NewRecorder()
	GetChannel(idx, nil).ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestGetChannel_ChannelNotFound_Returns404(t *testing.T) {
	t.Parallel()
	d := newTestDevice("0001ABCD", "HmIP-BSM")
	d.AddChannel("0001ABCD:0", 0, "MAINTENANCE", hmenum.ParamsetKeyValues)
	idx := &stubDeviceIndex{devices: map[string]*device.Device{"0001ABCD": d}}

	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "0001ABCD", "no": "99"}))
	w := httptest.NewRecorder()
	GetChannel(idx, nil).ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

// --- GetDataPoint ---

func newDeviceWithDP(t *testing.T, addr, model string, channelNo int, param hmenum.Parameter) *device.Device {
	t.Helper()
	d := device.New(device.Config{
		Address:     addr,
		Model:       model,
		Interface:   hmenum.InterfaceHmIPRF,
		InterfaceID: "HmIP-RF@ccu01",
		Name:        "Test",
	})
	chAddr := addr + ":1"
	ch := d.AddChannel(chAddr, channelNo, "SWITCH", hmenum.ParamsetKeyValues)
	dp := generic.NewBinarySensor(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: chAddr,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(param),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeBool,
			Operations: hmenum.OperationsRead | hmenum.OperationsEvent,
		},
	})
	ch.Put(dp)
	return d
}

func TestGetDataPoint_HappyPath(t *testing.T) {
	t.Parallel()
	d := newDeviceWithDP(t, "0001ABCD", "HmIP-BSM", 1, hmenum.ParameterState)
	idx := &stubDeviceIndex{devices: map[string]*device.Device{"0001ABCD": d}}

	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{
		"addr":  "0001ABCD",
		"no":    "1",
		"param": "STATE",
	}))
	w := httptest.NewRecorder()
	GetDataPoint(idx, nil).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var body DataPointSummary
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body.Parameter != "STATE" {
		t.Fatalf("expected parameter=STATE, got %q", body.Parameter)
	}
}

func TestGetDataPoint_ParamNotFound_Returns404(t *testing.T) {
	t.Parallel()
	d := newDeviceWithDP(t, "0001ABCD", "HmIP-BSM", 1, hmenum.ParameterState)
	idx := &stubDeviceIndex{devices: map[string]*device.Device{"0001ABCD": d}}

	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{
		"addr":  "0001ABCD",
		"no":    "1",
		"param": "LEVEL", // not in this device
	}))
	w := httptest.NewRecorder()
	GetDataPoint(idx, nil).ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestGetDataPoint_DeviceNotFound_Returns404(t *testing.T) {
	t.Parallel()
	idx := &stubDeviceIndex{devices: map[string]*device.Device{}}

	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{
		"addr":  "MISSING",
		"no":    "1",
		"param": "STATE",
	}))
	w := httptest.NewRecorder()
	GetDataPoint(idx, nil).ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

// --- PutDataPointValue ---

func TestPutDataPointValue_ParamNotFound_Returns404(t *testing.T) {
	t.Parallel()
	d := newDeviceWithDP(t, "0001ABCD", "HmIP-BSM", 1, hmenum.ParameterState)
	idx := &stubDeviceIndex{devices: map[string]*device.Device{"0001ABCD": d}}

	body := strings.NewReader(`{"value":true}`)
	req := httptest.NewRequest(http.MethodPut, "/", body)
	req = req.WithContext(chiContext(req, map[string]string{
		"addr":  "0001ABCD",
		"no":    "1",
		"param": "LEVEL",
	}))
	w := httptest.NewRecorder()
	PutDataPointValue(idx, nil).ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestPutDataPointValue_InvalidJSON_Returns400(t *testing.T) {
	t.Parallel()
	d := newDeviceWithDP(t, "0001ABCD", "HmIP-BSM", 1, hmenum.ParameterState)
	idx := &stubDeviceIndex{devices: map[string]*device.Device{"0001ABCD": d}}

	req := httptest.NewRequest(http.MethodPut, "/", strings.NewReader("NOT JSON"))
	req = req.WithContext(chiContext(req, map[string]string{
		"addr":  "0001ABCD",
		"no":    "1",
		"param": "STATE",
	}))
	w := httptest.NewRecorder()
	PutDataPointValue(idx, nil).ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestPutDataPointValue_DeviceNotFound_Returns404(t *testing.T) {
	t.Parallel()
	idx := &stubDeviceIndex{devices: map[string]*device.Device{}}

	body := strings.NewReader(`{"value":true}`)
	req := httptest.NewRequest(http.MethodPut, "/", body)
	req = req.WithContext(chiContext(req, map[string]string{
		"addr":  "MISSING",
		"no":    "1",
		"param": "STATE",
	}))
	w := httptest.NewRecorder()
	PutDataPointValue(idx, nil).ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d body=%s", w.Code, w.Body.String())
	}
}

// PutDataPointValue with no channel writer — the binary sensor is read-only
// (Operations has no Write bit), so ch.Set returns a non-writer-related error
// which maps to 502. This test verifies the 502 path.
func TestPutDataPointValue_ReadOnlyParam_Returns502(t *testing.T) {
	t.Parallel()
	d := newDeviceWithDP(t, "0001ABCD", "HmIP-BSM", 1, hmenum.ParameterState)
	idx := &stubDeviceIndex{devices: map[string]*device.Device{"0001ABCD": d}}

	body := strings.NewReader(`{"value":true}`)
	req := httptest.NewRequest(http.MethodPut, "/", body)
	req = req.WithContext(chiContext(req, map[string]string{
		"addr":  "0001ABCD",
		"no":    "1",
		"param": "STATE",
	}))
	w := httptest.NewRecorder()
	PutDataPointValue(idx, nil).ServeHTTP(w, req)

	// The parameter is read-only (no Write bit) so Set fails.
	// This maps to 502 (or 503 when ErrNoChannelWriter fires first).
	if w.Code != http.StatusBadGateway && w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 502 or 503, got %d body=%s", w.Code, w.Body.String())
	}
}

// --- parsePriority ---

func TestParsePriority_AllValues(t *testing.T) {
	t.Parallel()
	cases := []struct {
		input string
		want  hmenum.CommandPriority
	}{
		{"critical", hmenum.CommandPriorityCritical},
		{"high", hmenum.CommandPriorityHigh},
		{"low", hmenum.CommandPriorityLow},
		{"", hmenum.CommandPriorityHigh},        // default fallback
		{"unknown", hmenum.CommandPriorityHigh}, // unknown → fallback
	}
	for _, tc := range cases {
		got := parsePriority(tc.input)
		if got != tc.want {
			t.Errorf("parsePriority(%q) = %v, want %v", tc.input, got, tc.want)
		}
	}
}

// --- parsePagination ---

func TestParsePagination_Defaults(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	page, perPage := parsePagination(req)
	if page != 1 {
		t.Errorf("default page = %d, want 1", page)
	}
	if perPage != 50 {
		t.Errorf("default per_page = %d, want 50", perPage)
	}
}

func TestParsePagination_CustomValues(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest(http.MethodGet, "/?page=3&per_page=100", http.NoBody)
	page, perPage := parsePagination(req)
	if page != 3 {
		t.Errorf("page = %d, want 3", page)
	}
	if perPage != 100 {
		t.Errorf("per_page = %d, want 100", perPage)
	}
}

func TestParsePagination_InvalidValues_FallsBackToDefaults(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest(http.MethodGet, "/?page=notanumber&per_page=-1", http.NoBody)
	page, perPage := parsePagination(req)
	if page != 1 {
		t.Errorf("page = %d, want 1 for invalid input", page)
	}
	if perPage != 50 {
		t.Errorf("per_page = %d, want 50 for invalid input", perPage)
	}
}

func TestParsePagination_PerPageExceedsMax_Clamps(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest(http.MethodGet, "/?per_page=999", http.NoBody)
	_, perPage := parsePagination(req)
	// per_page > 500 is clamped; the function returns the default.
	if perPage != 50 {
		t.Errorf("per_page = %d, want 50 when 999 exceeds max 500", perPage)
	}
}

// --- channelTypeLabel and channelTypedParameterLabel ---

// stubChannelTypedLabeler implements both ParameterLabeler and ChannelTypedLabeler.
type stubChannelTypedLabeler struct {
	ctLabel    string
	ctpLabel   map[string]string
	paramLabel string
}

func (s *stubChannelTypedLabeler) ParameterLabel(_ string) string   { return s.paramLabel }
func (s *stubChannelTypedLabeler) ChannelTypeLabel(_ string) string { return s.ctLabel }
func (s *stubChannelTypedLabeler) ChannelTypedParameterLabel(_, param string) string {
	return s.ctpLabel[param]
}

func TestChannelTypeLabel_NilLabeler_ReturnsEmpty(t *testing.T) {
	t.Parallel()
	if got := channelTypeLabel(nil, "SWITCH"); got != "" {
		t.Errorf("expected empty, got %q", got)
	}
}

func TestChannelTypeLabel_WithLabeler(t *testing.T) {
	t.Parallel()
	lab := &stubChannelTypedLabeler{ctLabel: "Schaltaktor"}
	got := channelTypeLabel(lab, "SWITCH")
	if got != "Schaltaktor" {
		t.Errorf("expected Schaltaktor, got %q", got)
	}
}

func TestChannelTypedParameterLabel_NilLabeler_ReturnsEmpty(t *testing.T) {
	t.Parallel()
	if got := channelTypedParameterLabel(nil, "SWITCH", "STATE"); got != "" {
		t.Errorf("expected empty, got %q", got)
	}
}

func TestChannelTypedParameterLabel_ChannelTypedHit(t *testing.T) {
	t.Parallel()
	lab := &stubChannelTypedLabeler{
		ctpLabel:   map[string]string{"POWER": "Wirkleistung"},
		paramLabel: "Power",
	}
	got := channelTypedParameterLabel(lab, "ENERGIE_METER_TRANSMITTER", "POWER")
	if got != "Wirkleistung" {
		t.Errorf("expected Wirkleistung (channel-typed), got %q", got)
	}
}

func TestChannelTypedParameterLabel_ChannelTypedMiss_FallsBackToParam(t *testing.T) {
	t.Parallel()
	lab := &stubChannelTypedLabeler{
		ctpLabel:   map[string]string{},
		paramLabel: "State",
	}
	got := channelTypedParameterLabel(lab, "SWITCH", "STATE")
	if got != "State" {
		t.Errorf("expected fallback State, got %q", got)
	}
}

// --- RefreshDevices ---

var errRefreshFailed = errors.New("refresh failed")

type stubRefreshDevicesService struct {
	err error
}

func (s *stubRefreshDevicesService) RefreshDevices(_ context.Context) error {
	return s.err
}

func TestRefreshDevices_HappyPath(t *testing.T) {
	t.Parallel()
	svc := &stubRefreshDevicesService{}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/devices/refresh", http.NoBody)
	w := httptest.NewRecorder()
	RefreshDevices(svc).ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestRefreshDevices_Error_Returns502(t *testing.T) {
	t.Parallel()
	svc := &stubRefreshDevicesService{err: errRefreshFailed}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/devices/refresh", http.NoBody)
	w := httptest.NewRecorder()
	RefreshDevices(svc).ServeHTTP(w, req)

	if w.Code != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d body=%s", w.Code, w.Body.String())
	}
}

// --- toDataPointSummary: category + data_point_type ---

// newCategorisedDP builds a generic BinarySensor with Kind explicitly set so
// Category() returns DataPointCategoryBinarySensor. This is the same pattern
// the device-ingest pipeline uses: Kind is injected via Spec rather than
// inferred at constructor time.
func newCategorisedDP(t *testing.T, chAddr string, param hmenum.Parameter, kind generic.ResolvedKind) device.ParameterDataPoint {
	t.Helper()
	return generic.NewBinarySensor(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: chAddr,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(param),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeBool,
			Operations: hmenum.OperationsRead | hmenum.OperationsEvent,
		},
		Kind: kind,
	})
}

// TestToDataPointSummary_CategoryAndType verifies that a DP implementing
// CategorisedDataPoint surfaces non-empty category and data_point_type
// fields, and that data_point_type equals the CategoryToType mapping for
// the given category.
func TestToDataPointSummary_CategoryAndType(t *testing.T) {
	t.Parallel()
	dp := newCategorisedDP(t, "ADDR:1", hmenum.ParameterState, generic.KindBinarySensor)
	s := toDataPointSummary(dp, nil, "SWITCH")

	if s.Category == "" {
		t.Error("category must not be empty for a CategorisedDataPoint")
	}
	if s.DataPointType == "" {
		t.Error("data_point_type must not be empty for a CategorisedDataPoint")
	}
	cat := hmenum.DataPointCategory(s.Category)
	wantType := string(hmenum.CategoryToType[cat])
	if s.DataPointType != wantType {
		t.Errorf("data_point_type = %q, want CategoryToType[%q] = %q", s.DataPointType, s.Category, wantType)
	}
}

// TestToDataPointSummary_NoCategory_FieldsAbsent verifies that a DP that
// does not implement CategorisedDataPoint produces empty category and
// data_point_type, confirming omitempty behaviour at the JSON level.
func TestToDataPointSummary_NoCategory_FieldsAbsent(t *testing.T) {
	t.Parallel()
	// KindUnknown → Category() returns DataPointCategoryUndefined which maps
	// to an empty string (no CategoryToType entry for "undefined"); the DP
	// still implements CategorisedDataPoint, but the empty-string Category
	// means neither field is populated.
	//
	// To get a DP that truly does NOT implement CategorisedDataPoint we use
	// a minimal inline stub — not a generic.DataPoint subtype.
	dp := &minimalDP{param: hmenum.ParameterState}
	s := toDataPointSummary(dp, nil, "SWITCH")

	if s.Category != "" {
		t.Errorf("category must be empty for a non-categorised DP, got %q", s.Category)
	}
	if s.DataPointType != "" {
		t.Errorf("data_point_type must be empty for a non-categorised DP, got %q", s.DataPointType)
	}
}

// TestToDataPointSummary_TypeFieldDistinctFromDataPointType verifies that the
// CCU descriptor's Type (BOOL, INTEGER, …) and the semantic DataPointType
// ("binary_sensor", "switch", …) are independent and both present.
func TestToDataPointSummary_TypeFieldDistinctFromDataPointType(t *testing.T) {
	t.Parallel()
	dp := newCategorisedDP(t, "ADDR:1", hmenum.ParameterState, generic.KindBinarySensor)
	s := toDataPointSummary(dp, nil, "SWITCH")

	// descriptor type is "BOOL" (ParameterTypeBool)
	if s.Type == "" {
		t.Error("type (CCU descriptor) must not be empty")
	}
	if s.DataPointType == "" {
		t.Error("data_point_type (semantic type) must not be empty")
	}
	// They carry different semantics and must not be equal for this DP.
	if s.Type == s.DataPointType {
		t.Errorf("type=%q and data_point_type=%q must differ — they represent distinct concepts", s.Type, s.DataPointType)
	}
}

// minimalDP is a test double that satisfies device.ParameterDataPoint without
// implementing device.CategorisedDataPoint. It carries only the minimum
// surface toDataPointSummary requires.
type minimalDP struct {
	param hmenum.Parameter
}

func (m *minimalDP) Parameter() hmenum.Parameter { return m.param }
func (m *minimalDP) DataPointKey() hmtypes.DataPointKey {
	return hmtypes.DataPointKey{Parameter: string(m.param)}
}
func (m *minimalDP) ParameterData() hmproto.ParameterData     { return hmproto.ParameterData{} }
func (m *minimalDP) RawValue() (any, bool)                    { return nil, false }
func (m *minimalDP) ModifiedAt() time.Time                    { return time.Time{} }
func (m *minimalDP) OnAnyUpdate(_ func(old, next any)) func() { return func() {} }
