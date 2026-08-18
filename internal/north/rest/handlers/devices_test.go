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

func (s *stubDeviceIndex) SerialSuffix(central string) string {
	if central != "" {
		return "vccu0000000"
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

// TestGetDevice_ConfigRestoreSupportedReflectsInterface verifies
// DeviceSummary.ConfigRestoreSupported (JSON: config_restore_supported)
// mirrors hmenum.Interface.SupportsConfigRestore(): true for HmIP-RF and
// BidCos-RF (rfd / HMIPServer implement restoreConfigToDevice), false for
// BidCos-Wired (hs485d does not).
func TestGetDevice_ConfigRestoreSupportedReflectsInterface(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		iface hmenum.Interface
		want  bool
	}{
		{"HmIP-RF", hmenum.InterfaceHmIPRF, true},
		{"BidCos-RF", hmenum.InterfaceBidCosRF, true},
		{"BidCos-Wired", hmenum.InterfaceBidCosWired, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			d := device.New(device.Config{
				Address:     "0001ABCD",
				Model:       "HmIP-BSM",
				Interface:   tc.iface,
				InterfaceID: string(tc.iface),
				Name:        "Test Device",
			})
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
			if body.ConfigRestoreSupported != tc.want {
				t.Errorf("%s: config_restore_supported=%v, want %v", tc.iface, body.ConfigRestoreSupported, tc.want)
			}
		})
	}
}

// TestGetDevice_CommunicationTestSupportedReflectsInterface verifies
// DeviceSummary.CommunicationTestSupported (JSON:
// communication_test_supported) mirrors
// hmenum.Interface.SupportsCommunicationTest(): true for the radio
// interfaces (HmIP-RF, BidCos-RF, BidCos-Wired), false for VirtualDevices
// and CUxD.
func TestGetDevice_CommunicationTestSupportedReflectsInterface(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		iface hmenum.Interface
		want  bool
	}{
		{"HmIP-RF", hmenum.InterfaceHmIPRF, true},
		{"BidCos-RF", hmenum.InterfaceBidCosRF, true},
		{"BidCos-Wired", hmenum.InterfaceBidCosWired, true},
		{"VirtualDevices", hmenum.InterfaceVirtualDevices, false},
		{"CUxD", hmenum.InterfaceCUxD, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			d := device.New(device.Config{
				Address:     "0001ABCD",
				Model:       "HmIP-BSM",
				Interface:   tc.iface,
				InterfaceID: string(tc.iface),
				Name:        "Test Device",
			})
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
			if body.CommunicationTestSupported != tc.want {
				t.Errorf("%s: communication_test_supported=%v, want %v", tc.iface, body.CommunicationTestSupported, tc.want)
			}
		})
	}
}

func TestGetDevice_TeamSupportedReflectsInterface(t *testing.T) {
	t.Parallel()
	cases := []struct {
		iface hmenum.Interface
		want  bool
	}{
		{hmenum.InterfaceBidCosRF, true},
		{hmenum.InterfaceHmIPRF, true},
		{hmenum.InterfaceBidCosWired, false},
		{hmenum.InterfaceVirtualDevices, false},
		{hmenum.InterfaceCUxD, false},
	}
	for _, tc := range cases {
		t.Run(string(tc.iface), func(t *testing.T) {
			t.Parallel()
			d := device.New(device.Config{
				Address: "0001ABCD", Model: "HM-Sec-SD",
				Interface: tc.iface, InterfaceID: string(tc.iface), Name: "SD",
			})
			idx := &stubDeviceIndex{devices: map[string]*device.Device{"0001ABCD": d}}
			req := httptest.NewRequest(http.MethodGet, "/api/v1/devices/0001ABCD", http.NoBody)
			req = req.WithContext(chiContext(req, map[string]string{"addr": "0001ABCD"}))
			w := httptest.NewRecorder()
			GetDevice(idx, nil).ServeHTTP(w, req)
			var body DeviceDetail
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if body.TeamSupported != tc.want {
				t.Errorf("%s: team_supported=%v, want %v", tc.iface, body.TeamSupported, tc.want)
			}
		})
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
	d1.SetRooms([]string{"Living Room", "Office"})
	d2 := newTestDevice("0002ABCD", "HmIP-STE2")
	d2.SetRooms([]string{"Living Room"})
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
	d.SetFunctions([]string{"Lighting", "Security"})
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

// newDeviceWithLevelDP builds a device carrying one Float data point whose
// descriptor UNIT is "100%" — the wire shape LEVEL and every 0.0-1.0
// percent-family parameter use. CleanupUnit canonicalises this to "%",
// which is why the multiplier (100) has to travel separately: the value
// stays 0.0-1.0 on the wire, only the label says "%".
func newDeviceWithLevelDP(t *testing.T) *device.Device {
	t.Helper()
	d := device.New(device.Config{
		Address:     "0001ABCD",
		Model:       "HmIP-BROLL",
		Interface:   hmenum.InterfaceHmIPRF,
		InterfaceID: "HmIP-RF@ccu01",
		Name:        "Test",
	})
	chAddr := "0001ABCD:1"
	ch := d.AddChannel(chAddr, 1, "BLIND", hmenum.ParamsetKeyValues)
	dp := generic.NewFloat(generic.Spec{
		Key: hmtypes.DataPointKey{
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
	return d
}

// TestGetDataPoint_PercentFamilyUnit_CarriesMultiplier pins the REST
// counterpart of the MQTT raw-plane config payload's multiplier field: a
// data point whose raw wire value needs scaling to match its cleaned-up
// unit (0.0-1.0 "100%" -> "%") must expose the scale factor, or a client
// has no way to render "42 %" instead of "0.42 %" for a LEVEL parameter.
func TestGetDataPoint_PercentFamilyUnit_CarriesMultiplier(t *testing.T) {
	t.Parallel()
	d := newDeviceWithLevelDP(t)
	idx := &stubDeviceIndex{devices: map[string]*device.Device{"0001ABCD": d}}

	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{
		"addr":  "0001ABCD",
		"no":    "1",
		"param": "LEVEL",
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
	if body.Unit != "%" {
		t.Fatalf("unit = %q, want the cleaned-up %%", body.Unit)
	}
	if body.Multiplier != 100 {
		t.Errorf("multiplier = %v, want 100 so a 0.42 wire value renders as 42 %%", body.Multiplier)
	}
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
func TestPutDataPointValue_ReadOnlyParam_Returns400(t *testing.T) {
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

	// The parameter is read-only (no Write bit): the write is rejected by
	// validation before it ever reaches the wire, so it is a client error
	// (400), not a 502 upstream failure.
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", w.Code, w.Body.String())
	}
}

// TestPutDataPointValue_LockedChannel_Returns423 verifies an operator
// channel lock surfaces as 423 Locked: the write is a deliberate local
// policy rejection that never reached the CCU, so it must be
// distinguishable from a 502 upstream failure.
func TestPutDataPointValue_LockedChannel_Returns423(t *testing.T) {
	t.Parallel()
	d := newDeviceWithDP(t, "0001ABCD", "HmIP-BSM", 1, hmenum.ParameterState)
	d.Channel("0001ABCD:1").SetOperatorFlags(false, true)
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

	if w.Code != http.StatusLocked {
		t.Fatalf("expected 423, got %d body=%s", w.Code, w.Body.String())
	}
}

// --- ListDevices central filter ---

// multiCentralDeviceIndex is a DeviceIndex stub that maps each device address
// to its owning central name, enabling tests that span multiple CCUs.
type multiCentralDeviceIndex struct {
	devices  map[string]*device.Device
	centrals map[string]string // address → central name
}

func (m *multiCentralDeviceIndex) Devices() []*device.Device {
	out := make([]*device.Device, 0, len(m.devices))
	for _, d := range m.devices {
		out = append(out, d)
	}
	return out
}

func (m *multiCentralDeviceIndex) Device(address string) (*device.Device, bool) {
	d, ok := m.devices[address]
	return d, ok
}

func (m *multiCentralDeviceIndex) CentralOf(address string) string {
	return m.centrals[address]
}

func (m *multiCentralDeviceIndex) SerialSuffix(central string) string {
	if central != "" {
		return "vccu0000000"
	}
	return ""
}

func TestListDevices_CentralFilter(t *testing.T) {
	t.Parallel()

	homeAddr := "AABB0001"
	officeAddr := "CCDD0002"
	dHome := newTestDevice(homeAddr, "HmIP-BSM")
	dOffice := newTestDevice(officeAddr, "HmIP-STE2")

	idx := &multiCentralDeviceIndex{
		devices: map[string]*device.Device{
			homeAddr:   dHome,
			officeAddr: dOffice,
		},
		centrals: map[string]string{
			homeAddr:   "home",
			officeAddr: "office",
		},
	}

	t.Run("central=home returns only home device", func(t *testing.T) {
		t.Parallel()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/devices?central=home", http.NoBody)
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
			t.Fatalf("expected total=1 for central=home, got %v", body["total"])
		}
		items := body["items"].([]any)
		addr := items[0].(map[string]any)["address"].(string)
		if addr != homeAddr {
			t.Errorf("expected home device %q, got %q", homeAddr, addr)
		}
	})

	t.Run("no central param returns all devices", func(t *testing.T) {
		t.Parallel()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/devices", http.NoBody)
		w := httptest.NewRecorder()
		ListDevices(idx).ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", w.Code)
		}
		var body map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if body["total"].(float64) != 2 {
			t.Fatalf("expected total=2 without central filter, got %v", body["total"])
		}
	})

	t.Run("empty central param returns all devices", func(t *testing.T) {
		t.Parallel()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/devices?central=", http.NoBody)
		w := httptest.NewRecorder()
		ListDevices(idx).ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", w.Code)
		}
		var body map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if body["total"].(float64) != 2 {
			t.Fatalf("expected total=2 for empty central, got %v", body["total"])
		}
	})

	t.Run("unknown central returns no devices", func(t *testing.T) {
		t.Parallel()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/devices?central=nope", http.NoBody)
		w := httptest.NewRecorder()
		ListDevices(idx).ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", w.Code)
		}
		var body map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if body["total"].(float64) != 0 {
			t.Fatalf("expected total=0 for unknown central, got %v", body["total"])
		}
	})

	t.Run("central composes with model filter", func(t *testing.T) {
		t.Parallel()
		// central=home AND model=bsm — matches only the home device.
		req := httptest.NewRequest(http.MethodGet, "/api/v1/devices?central=home&model=bsm", http.NoBody)
		w := httptest.NewRecorder()
		ListDevices(idx).ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", w.Code)
		}
		var body map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if body["total"].(float64) != 1 {
			t.Fatalf("expected total=1 (central+model), got %v", body["total"])
		}
		// central=office AND model=bsm — office device is STE2, not BSM.
		req2 := httptest.NewRequest(http.MethodGet, "/api/v1/devices?central=office&model=bsm", http.NoBody)
		w2 := httptest.NewRecorder()
		ListDevices(idx).ServeHTTP(w2, req2)
		var body2 map[string]any
		if err := json.Unmarshal(w2.Body.Bytes(), &body2); err != nil {
			t.Fatalf("unmarshal body2: %v", err)
		}
		if body2["total"].(float64) != 0 {
			t.Fatalf("expected total=0 (central=office+model=bsm), got %v", body2["total"])
		}
	})
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

// TestParsePagination_HugePageCannotOverflowSliceBounds pins the upper page
// bound. The OpenAPI parameter declares a minimum and no maximum, so a probe
// can send a page whose (page-1)*per_page product wraps negative; the list
// handlers then slice with a negative low bound and panic the request.
func TestParsePagination_HugePageCannotOverflowSliceBounds(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest(http.MethodGet, "/?page=200000000000000000&per_page=50", http.NoBody)
	page, perPage := parsePagination(req)
	if start := (page - 1) * perPage; start < 0 {
		t.Fatalf("start = %d for page=%d per_page=%d, want a non-negative slice bound", start, page, perPage)
	}
	if start := (page - 1) * maxPerPage; start < 0 || start+maxPerPage < 0 {
		t.Fatalf("page=%d overflows at the maximum per_page (start=%d)", page, start)
	}
}

// TestListDevices_HugePageReturnsEmptyPageInsteadOfPanicking drives the
// overflow through the real handler: a page past the end must answer an empty
// item list, never a recovered 500 plus a stack trace in the log ring.
func TestListDevices_HugePageReturnsEmptyPageInsteadOfPanicking(t *testing.T) {
	t.Parallel()
	idx := &stubDeviceIndex{devices: map[string]*device.Device{
		"0001ABCD": newTestDevice("0001ABCD", "HmIP-BSM"),
	}}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/devices?page=200000000000000000", http.NoBody)
	w := httptest.NewRecorder()
	ListDevices(idx).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if items, ok := body["items"].([]any); !ok || len(items) != 0 {
		t.Fatalf("expected an empty page, got %v", body["items"])
	}
	if body["total"].(float64) != 1 {
		t.Fatalf("expected total=1, got %v", body["total"])
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
	s := toDataPointSummary(dp, nil, &device.Channel{Type: "SWITCH"}, "")

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
	s := toDataPointSummary(dp, nil, &device.Channel{Type: "SWITCH"}, "")

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
	s := toDataPointSummary(dp, nil, &device.Channel{Type: "SWITCH"}, "")

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

// ---------------------------------------------------------------------------
// resolvedParameterLabel
// ---------------------------------------------------------------------------

// TestResolvedParameterLabel_ChannelTypedLabelWins verifies that when
// the labeler has a channel-type-specific entry it is returned directly,
// without consulting the title-case fallback.
func TestResolvedParameterLabel_ChannelTypedLabelWins(t *testing.T) {
	t.Parallel()
	lab := &stubChannelTypedLabeler{
		ctpLabel:   map[string]string{"POWER": "Wirkleistung"},
		paramLabel: "Power",
	}
	got := resolvedParameterLabel(lab, "ENERGIE_METER_TRANSMITTER", "POWER")
	if got != "Wirkleistung" {
		t.Errorf("resolvedParameterLabel: got %q, want %q", got, "Wirkleistung")
	}
}

// TestResolvedParameterLabel_BareParamLabelUsed verifies that when there is no
// channel-type-specific entry but the bare-parameter translation is present, it
// is returned instead of the title-case fallback.
func TestResolvedParameterLabel_BareParamLabelUsed(t *testing.T) {
	t.Parallel()
	lab := &stubChannelTypedLabeler{
		ctpLabel:   map[string]string{},
		paramLabel: "State",
	}
	got := resolvedParameterLabel(lab, "SWITCH", "STATE")
	if got != "State" {
		t.Errorf("resolvedParameterLabel: got %q, want %q (bare param fallback)", got, "State")
	}
}

// TestResolvedParameterLabel_NoTranslation_TitleCasedFallback verifies that
// when neither the channel-typed nor the bare-parameter translation is present,
// the function returns the title-cased parameter name so the field is never empty.
func TestResolvedParameterLabel_NoTranslation_TitleCasedFallback(t *testing.T) {
	t.Parallel()
	// Both lookups miss — paramLabel is empty, ctpLabel is empty.
	lab := &stubChannelTypedLabeler{
		ctpLabel:   map[string]string{},
		paramLabel: "",
	}
	got := resolvedParameterLabel(lab, "MAINTENANCE", "RSSI_DEVICE")
	if got != "Rssi Device" {
		t.Errorf("resolvedParameterLabel: got %q, want %q (title-case fallback)", got, "Rssi Device")
	}
}

// TestResolvedParameterLabel_NilLabeler_TitleCasedFallback verifies that a nil
// labeler triggers the title-case fallback so the field is always non-empty.
func TestResolvedParameterLabel_NilLabeler_TitleCasedFallback(t *testing.T) {
	t.Parallel()
	got := resolvedParameterLabel(nil, "MAINTENANCE", "RSSI_DEVICE")
	if got != "Rssi Device" {
		t.Errorf("resolvedParameterLabel(nil): got %q, want %q", got, "Rssi Device")
	}
}

// ---------------------------------------------------------------------------
// toDataPointSummary: ParameterLabel is never empty
// ---------------------------------------------------------------------------

// TestToDataPointSummary_ParameterLabel_TitleCasedWhenUntranslated verifies that
// when the labeler cannot resolve a translation, ParameterLabel falls back to
// the title-cased parameter name rather than the empty string. This pins the
// contract that the field is always ready to render for any parameter.
func TestToDataPointSummary_ParameterLabel_TitleCasedWhenUntranslated(t *testing.T) {
	t.Parallel()
	// Labeler with empty bare-parameter label and no channel-typed entry.
	lab := &stubChannelTypedLabeler{
		ctpLabel:   map[string]string{},
		paramLabel: "",
	}
	dp := newCategorisedDP(t, "ADDR:1", hmenum.ParameterState, generic.KindBinarySensor)
	s := toDataPointSummary(dp, lab, &device.Channel{Type: "SWITCH"}, "")

	// "STATE" → "State"
	if s.ParameterLabel != "State" {
		t.Errorf("ParameterLabel = %q, want %q for untranslated parameter", s.ParameterLabel, "State")
	}
}

// TestToDataPointSummary_ParameterLabel_UsesChannelTypedLabel verifies that when
// the labeler has a channel-type-specific entry, ParameterLabel carries that value.
func TestToDataPointSummary_ParameterLabel_UsesChannelTypedLabel(t *testing.T) {
	t.Parallel()
	lab := &stubChannelTypedLabeler{
		ctpLabel:   map[string]string{"STATE": "Schaltzustand"},
		paramLabel: "State",
	}
	dp := newCategorisedDP(t, "ADDR:1", hmenum.ParameterState, generic.KindBinarySensor)
	s := toDataPointSummary(dp, lab, &device.Channel{Type: "SWITCH"}, "")

	if s.ParameterLabel != "Schaltzustand" {
		t.Errorf("ParameterLabel = %q, want %q (channel-typed label)", s.ParameterLabel, "Schaltzustand")
	}
}

// TestToDataPointSummary_ParameterLabel_NilLabeler_TitleCasedFallback verifies
// that a nil labeler still produces a non-empty ParameterLabel via the
// title-case fallback — so the JSON field is always present for clients.
func TestToDataPointSummary_ParameterLabel_NilLabeler_TitleCasedFallback(t *testing.T) {
	t.Parallel()
	dp := newCategorisedDP(t, "ADDR:1", hmenum.ParameterState, generic.KindBinarySensor)
	s := toDataPointSummary(dp, nil, &device.Channel{Type: "SWITCH"}, "")

	if s.ParameterLabel != "State" {
		t.Errorf("ParameterLabel = %q with nil labeler, want %q (title-case fallback)", s.ParameterLabel, "State")
	}
}

// TestListChannels_GroupAndRoomFields pins the channel-group contract
// external clients consume for the sub-device split: group_no,
// is_group_master, is_in_multi_group, sub_device_name on grouped
// channels plus the group-master-resolved room.
func TestListChannels_GroupAndRoomFields(t *testing.T) {
	t.Parallel()
	d := newTestDevice("0001GRP", "HmIP-BSM")
	d.AddChannel("0001GRP:0", 0, "MAINTENANCE", hmenum.ParamsetKeyValues)
	state := d.AddChannel("0001GRP:3", 3, "SWITCH_TRANSMITTER", hmenum.ParamsetKeyValues)
	master := d.AddChannel("0001GRP:4", 4, "SWITCH_VIRTUAL_RECEIVER", hmenum.ParamsetKeyValues)
	master.SetName("Galerie Aktor")
	master.SetRooms([]string{"Galerie"})
	for _, no := range []int{3, 4, 5} {
		d.AddChannelToGroup(4, no)
	}
	state.AssignGroupNumber(4)
	master.AssignGroupNumber(4)
	vch := d.AddChannel("0001GRP:5", 5, "SWITCH_VIRTUAL_RECEIVER", hmenum.ParamsetKeyValues)
	vch.AssignGroupNumber(4)
	idx := &stubDeviceIndex{devices: map[string]*device.Device{"0001GRP": d}}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/devices/0001GRP/channels", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "0001GRP"}))
	w := httptest.NewRecorder()
	ListChannels(idx, nil).ServeHTTP(w, req)

	var body []ChannelSummary
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	byNo := map[int]ChannelSummary{}
	for _, c := range body {
		byNo[c.Number] = c
	}
	if c := byNo[0]; c.GroupNo != 0 || c.IsInMultiGroup || c.Room != "" {
		t.Errorf("maintenance channel must carry no group fields, got %+v", c)
	}
	m := byNo[4]
	if m.GroupNo != 4 || !m.IsGroupMaster || !m.IsInMultiGroup {
		t.Errorf("master: group_no/is_group_master/is_in_multi_group = %d/%v/%v, want 4/true/true", m.GroupNo, m.IsGroupMaster, m.IsInMultiGroup)
	}
	if m.SubDeviceName != "Galerie Aktor" {
		t.Errorf("master sub_device_name = %q, want Galerie Aktor", m.SubDeviceName)
	}
	if m.Room != "Galerie" {
		t.Errorf("master room = %q, want Galerie", m.Room)
	}
	v := byNo[5]
	if v.GroupNo != 4 || v.IsGroupMaster || !v.IsInMultiGroup {
		t.Errorf("vch: group fields = %+v, want member of group 4", v)
	}
	if v.Room != "Galerie" {
		t.Errorf("vch room = %q, want group-master fallback Galerie", v.Room)
	}
}

// ---------------------------------------------------------------------------
// FEATURE D2 — resolvedValueTranslations
// ---------------------------------------------------------------------------

// fakeValueLabeler implements both ParameterLabeler and ChannelTypedValueLabeler.
type fakeValueLabeler struct{}

func (f *fakeValueLabeler) ParameterLabel(_ string) string { return "" }
func (f *fakeValueLabeler) ChannelTypedValueLabel(_, _, value string) string {
	switch value {
	case "A":
		return "Alpha"
	case "B":
		return "B" // identity — excluded
	default:
		return "" // empty — excluded
	}
}

// fakePlainLabeler implements ParameterLabeler but NOT ChannelTypedValueLabeler.
type fakePlainLabeler struct{}

func (f *fakePlainLabeler) ParameterLabel(_ string) string { return "" }

// TestResolvedValueTranslations_SomeTranslate verifies that only non-empty,
// non-identity translations are included in the returned map.
func TestResolvedValueTranslations_SomeTranslate(t *testing.T) {
	t.Parallel()
	got := resolvedValueTranslations(&fakeValueLabeler{}, "SWITCH", "STATE", []string{"A", "B", "C"})
	if len(got) != 1 {
		t.Fatalf("expected 1 entry, got %d: %v", len(got), got)
	}
	if got["A"] != "Alpha" {
		t.Fatalf(`got["A"] = %q, want "Alpha"`, got["A"])
	}
}

// TestResolvedValueTranslations_NilLabeler verifies that a nil labeler
// returns nil.
func TestResolvedValueTranslations_NilLabeler(t *testing.T) {
	t.Parallel()
	got := resolvedValueTranslations(nil, "SWITCH", "STATE", []string{"A", "B"})
	if got != nil {
		t.Fatalf("expected nil, got %v", got)
	}
}

// TestResolvedValueTranslations_EmptyList verifies that an empty value list
// returns nil even with a valid labeler.
func TestResolvedValueTranslations_EmptyList(t *testing.T) {
	t.Parallel()
	got := resolvedValueTranslations(&fakeValueLabeler{}, "SWITCH", "STATE", nil)
	if got != nil {
		t.Fatalf("expected nil for empty valueList, got %v", got)
	}
}

// TestResolvedValueTranslations_LabelerNotImplementingInterface verifies that a
// labeler that does not implement ChannelTypedValueLabeler returns nil.
func TestResolvedValueTranslations_LabelerNotImplementingInterface(t *testing.T) {
	t.Parallel()
	got := resolvedValueTranslations(&fakePlainLabeler{}, "SWITCH", "STATE", []string{"A", "B"})
	if got != nil {
		t.Fatalf("expected nil for non-implementing labeler, got %v", got)
	}
}

// TestResolvedValueTranslations_NothingTranslates verifies that when every
// label equals the raw value the result is nil.
func TestResolvedValueTranslations_NothingTranslates(t *testing.T) {
	t.Parallel()
	// fakeValueLabeler returns "B" for "B" (identity) and "" for everything
	// else; pass only "B" so nothing survives the filter.
	got := resolvedValueTranslations(&fakeValueLabeler{}, "SWITCH", "STATE", []string{"B"})
	if got != nil {
		t.Fatalf("expected nil when nothing translates, got %v", got)
	}
}

// ---------------------------------------------------------------------------
// FEATURE D3 — toChannelSummary.Functions
// ---------------------------------------------------------------------------

// TestToChannelSummary_FunctionsPopulated verifies that channel Functions are
// copied into ChannelSummary.Functions.
func TestToChannelSummary_FunctionsPopulated(t *testing.T) {
	t.Parallel()
	d := newTestDevice("0001ABCD", "HmIP-BSM")
	ch := d.AddChannel("0001ABCD:1", 1, "SWITCH", hmenum.ParamsetKeyValues)
	ch.SetFunctions([]string{"Licht", "Heizung"})

	s := toChannelSummary(ch, nil)
	if len(s.Functions) != 2 {
		t.Fatalf("Functions len = %d, want 2: %v", len(s.Functions), s.Functions)
	}
	if s.Functions[0] != "Licht" || s.Functions[1] != "Heizung" {
		t.Fatalf("Functions = %v, want [Licht Heizung]", s.Functions)
	}
}

// TestToChannelSummary_FunctionsOmittedWhenEmpty verifies that a channel with
// no Functions yields a nil/empty Functions field on the summary.
func TestToChannelSummary_FunctionsOmittedWhenEmpty(t *testing.T) {
	t.Parallel()
	d := newTestDevice("0001ABCD", "HmIP-BSM")
	ch := d.AddChannel("0001ABCD:1", 1, "SWITCH", hmenum.ParamsetKeyValues)

	s := toChannelSummary(ch, nil)
	if len(s.Functions) != 0 {
		t.Fatalf("expected empty Functions, got %v", s.Functions)
	}
}

// TestToDataPointSummary_UniqueID verifies that a non-empty serialSuffix
// produces a loom_-prefixed unique_id, and that an empty suffix yields no id.
func TestToDataPointSummary_UniqueID(t *testing.T) {
	t.Parallel()
	dp := newCategorisedDP(t, "0001ABCD:1", hmenum.ParameterState, generic.KindBinarySensor)
	ch := &device.Channel{Type: "SWITCH"}

	t.Run("with serialSuffix produces loom_ prefix", func(t *testing.T) {
		t.Parallel()
		s := toDataPointSummary(dp, nil, ch, "vccu0000000")
		if s.UniqueID == "" {
			t.Fatal("UniqueID must not be empty when serialSuffix is set")
		}
		if !strings.HasPrefix(s.UniqueID, "loom_") {
			t.Errorf("UniqueID = %q, want loom_ prefix", s.UniqueID)
		}
	})

	t.Run("empty serialSuffix yields empty UniqueID", func(t *testing.T) {
		t.Parallel()
		s := toDataPointSummary(dp, nil, ch, "")
		if s.UniqueID != "" {
			t.Errorf("UniqueID = %q, want empty string when serialSuffix is empty", s.UniqueID)
		}
	})
}

// ---------------------------------------------------------------------------
// K3 — toDeviceSummary UpdateStatus field
// ---------------------------------------------------------------------------

// newTestDeviceWithFirmware builds a Device whose firmware tracker is seeded
// with the given FirmwareInfo so toDeviceSummary can derive UpdateStatus.
func newTestDeviceWithFirmware(addr string, fw device.FirmwareInfo) *device.Device {
	return device.New(device.Config{
		Address:     addr,
		Model:       "HmIP-BSM",
		Interface:   hmenum.InterfaceHmIPRF,
		InterfaceID: "HmIP-RF@CCU",
		Name:        "Test Device",
		Firmware:    fw,
	})
}

// TestToDeviceSummary_UpdateStatus verifies that toDeviceSummary populates
// UpdateStatus with the correct derived verdict for a range of firmware
// states. The default / zero state must produce "up_to_date".
func TestToDeviceSummary_UpdateStatus(t *testing.T) {
	t.Parallel()

	valid := map[string]struct{}{
		string(hmenum.DeviceUpdateStatusUpToDate):        {},
		string(hmenum.DeviceUpdateStatusUpdateAvailable): {},
		string(hmenum.DeviceUpdateStatusInstalling):      {},
	}

	cases := []struct {
		name string
		fw   device.FirmwareInfo
		want hmenum.DeviceUpdateStatus
	}{
		{
			name: "zero firmware info yields up_to_date",
			fw:   device.FirmwareInfo{},
			want: hmenum.DeviceUpdateStatusUpToDate,
		},
		{
			name: "UpToDate state yields up_to_date",
			fw:   device.FirmwareInfo{UpdateState: hmenum.DeviceFirmwareStateUpToDate},
			want: hmenum.DeviceUpdateStatusUpToDate,
		},
		{
			name: "PerformingUpdate state yields installing",
			fw:   device.FirmwareInfo{UpdateState: hmenum.DeviceFirmwareStatePerformingUpdate},
			want: hmenum.DeviceUpdateStatusInstalling,
		},
		{
			name: "DoUpdatePending state yields installing",
			fw:   device.FirmwareInfo{UpdateState: hmenum.DeviceFirmwareStateDoUpdatePending},
			want: hmenum.DeviceUpdateStatusInstalling,
		},
		{
			// ReadyForUpdate is in IsFirmwareUpdateReady, so HmIP-RF gating
			// surfaces it as update_available even without updateAvailable==true.
			name: "ReadyForUpdate state yields update_available",
			fw:   device.FirmwareInfo{UpdateState: hmenum.DeviceFirmwareStateReadyForUpdate},
			want: hmenum.DeviceUpdateStatusUpdateAvailable,
		},
	}

	for _, tc := range cases {
		d := newTestDeviceWithFirmware("0001ABCD", tc.fw)
		s := toDeviceSummary(d, "")
		if _, ok := valid[s.UpdateStatus]; !ok {
			t.Errorf("%s: UpdateStatus = %q is not a valid DeviceUpdateStatus value", tc.name, s.UpdateStatus)
		}
		if s.UpdateStatus != string(tc.want) {
			t.Errorf("%s: UpdateStatus = %q, want %q", tc.name, s.UpdateStatus, tc.want)
		}
	}
}

// ---------------------------------------------------------------------------
// K1 — toChannelSummary IsCustomDpPrimary field
// ---------------------------------------------------------------------------

// fakeCustomDP is a minimal AttachableDataPoint for use in the handlers
// package test — it satisfies the interface without depending on any
// custom-DP implementation package.
type fakeCustomDP struct{ key hmtypes.DataPointKey }

func (f *fakeCustomDP) DataPointKey() hmtypes.DataPointKey { return f.key }

// TestToChannelSummary_IsCustomDpPrimary verifies that a channel carrying a
// custom DP and sitting outside any group (GroupNo==0, treated as primary)
// yields IsCustomDpPrimary==true, and a plain channel without a custom DP
// yields false.
func TestToChannelSummary_IsCustomDpPrimary(t *testing.T) {
	t.Parallel()

	d := newTestDevice("PRIM0001", "HmIP-BSM")

	// Channel with a custom DP, no group — IsCustomDPPrimaryChannel() → true.
	chPrimary := d.AddChannel("PRIM0001:1", 1, "SWITCH", hmenum.ParamsetKeyValues)
	chPrimary.SetCustomDataPoint(&fakeCustomDP{
		key: hmtypes.DataPointKey{ChannelAddress: "PRIM0001:1", Parameter: "COVER"},
	})

	// Plain channel — no custom DP → IsCustomDPPrimaryChannel() → false.
	chPlain := d.AddChannel("PRIM0001:0", 0, "MAINTENANCE", hmenum.ParamsetKeyValues)

	t.Run("primary channel with custom DP yields is_custom_dp_primary=true", func(t *testing.T) {
		t.Parallel()
		s := toChannelSummary(chPrimary, nil)
		if !s.IsCustomDpPrimary {
			t.Error("expected IsCustomDpPrimary=true for channel with custom DP (no group, treated as primary)")
		}
	})

	t.Run("plain channel without custom DP yields is_custom_dp_primary=false", func(t *testing.T) {
		t.Parallel()
		s := toChannelSummary(chPlain, nil)
		if s.IsCustomDpPrimary {
			t.Error("expected IsCustomDpPrimary=false for channel without custom DP")
		}
	})
}

func TestRxModeInfo_DecodesBitmask(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		mode hmenum.RxMode
		want *RxModeInfo
	}{
		{"undefined yields nil", hmenum.RxModeUndefined, nil},
		{"always only", hmenum.RxModeAlways, &RxModeInfo{Always: true}},
		{"wakeup only", hmenum.RxModeWakeup, &RxModeInfo{Wakeup: true}},
		{"lazy config only", hmenum.RxModeLazyConfig, &RxModeInfo{LazyConfig: true}},
		{
			"burst plus wakeup",
			hmenum.RxModeBurst | hmenum.RxModeWakeup,
			&RxModeInfo{Burst: true, Wakeup: true},
		},
		{
			"config plus lazy config",
			hmenum.RxModeConfig | hmenum.RxModeLazyConfig,
			&RxModeInfo{Config: true, LazyConfig: true},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := rxModeInfo(tc.mode)
			if tc.want == nil {
				if got != nil {
					t.Fatalf("rxModeInfo(%d) = %+v, want nil", tc.mode, got)
				}
				return
			}
			if got == nil {
				t.Fatalf("rxModeInfo(%d) = nil, want %+v", tc.mode, tc.want)
			}
			if *got != *tc.want {
				t.Fatalf("rxModeInfo(%d) = %+v, want %+v", tc.mode, *got, *tc.want)
			}
		})
	}
}

func TestToDeviceSummary_RxModeSurfacesWakeup(t *testing.T) {
	t.Parallel()

	// A mains device (RX_ALWAYS) surfaces rx_mode with wakeup/lazy_config
	// clear, so the SPA shows no pending-wakeup hint.
	mains := device.New(device.Config{
		Address:   "MAINS00001",
		Model:     "HmIP-PSM",
		Interface: hmenum.InterfaceHmIPRF,
		RxMode:    hmenum.RxModeAlways,
	})
	ms := toDeviceSummary(mains, "ccu-01")
	if ms.RxMode == nil {
		t.Fatal("mains device: expected non-nil rx_mode")
	}
	if ms.RxMode.Wakeup || ms.RxMode.LazyConfig {
		t.Errorf("mains device: expected wakeup/lazy_config false, got %+v", *ms.RxMode)
	}
	if !ms.RxMode.Always {
		t.Error("mains device: expected always=true")
	}

	// A battery device (RX_WAKEUP|RX_LAZY_CONFIG) surfaces the wakeup bits.
	battery := device.New(device.Config{
		Address:   "BATT000001",
		Model:     "HmIP-eTRV",
		Interface: hmenum.InterfaceHmIPRF,
		RxMode:    hmenum.RxModeWakeup | hmenum.RxModeLazyConfig,
	})
	bs := toDeviceSummary(battery, "ccu-01")
	if bs.RxMode == nil {
		t.Fatal("battery device: expected non-nil rx_mode")
	}
	if !bs.RxMode.Wakeup || !bs.RxMode.LazyConfig {
		t.Errorf("battery device: expected wakeup and lazy_config true, got %+v", *bs.RxMode)
	}

	// A device with no rx mode (RX_MODE == 0) omits the field entirely.
	none := device.New(device.Config{
		Address:   "NONE000001",
		Model:     "HM-Test",
		Interface: hmenum.InterfaceVirtualDevices,
	})
	ns := toDeviceSummary(none, "ccu-01")
	if ns.RxMode != nil {
		t.Errorf("no-rx-mode device: expected nil rx_mode, got %+v", *ns.RxMode)
	}
}
