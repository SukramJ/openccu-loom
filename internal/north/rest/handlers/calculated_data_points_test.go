// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// stubCalculatedDP is a minimal AttachableDataPoint that also exposes
// RawValue / ModifiedAt so toCalculatedDPSummary can extract state.
type stubCalculatedDP struct {
	key        hmtypes.DataPointKey
	category   hmenum.DataPointCategory
	rawValue   float64
	hasValue   bool
	modifiedAt time.Time
}

func (s *stubCalculatedDP) DataPointKey() hmtypes.DataPointKey { return s.key }
func (s *stubCalculatedDP) Category() hmenum.DataPointCategory { return s.category }
func (s *stubCalculatedDP) RawValue() (any, bool) {
	if !s.hasValue {
		return nil, false
	}
	return s.rawValue, true
}
func (s *stubCalculatedDP) ModifiedAt() time.Time { return s.modifiedAt }

// addCalculatedDP attaches a stubCalculatedDP to channel channelNo of device d.
func addCalculatedDP(d *device.Device, addr, param string, no int, cat hmenum.DataPointCategory, value float64) {
	chAddr := addr + ":0"
	if no > 0 {
		chAddr = addr + ":" + string(rune('0'+no)) //nolint:gosec // G115: no is a small channel number (0..9) in test fixtures; '0'+no is 48..57
	}
	// Ensure channel exists.
	ch := d.Channel(chAddr)
	if ch == nil {
		ch = d.AddChannel(chAddr, no, "SENSOR", hmenum.ParamsetKeyValues)
	}
	dp := &stubCalculatedDP{
		key: hmtypes.DataPointKey{
			ChannelAddress: chAddr,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      param,
		},
		category:   cat,
		rawValue:   value,
		hasValue:   true,
		modifiedAt: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
	}
	ch.AttachCalculatedDataPoint(dp)
}

// --- tests: ListCalculatedDataPoints ---

func TestListCalculatedDataPoints_HappyPath(t *testing.T) {
	t.Parallel()
	d := newTestDevice("DEV0010", "HmIP-STE2")
	addCalculatedDP(d, "DEV0010", "DEW_POINT", 1, hmenum.DataPointCategorySensor, 12.5)
	idx := &stubDeviceIndex{devices: map[string]*device.Device{"DEV0010": d}}

	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "DEV0010", "no": "1"}))
	w := httptest.NewRecorder()
	ListCalculatedDataPoints(idx).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var out []CalculatedDPSummary
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("expected 1 calculated DP, got %d", len(out))
	}
	if out[0].Name != "DEW_POINT" {
		t.Fatalf("expected name=DEW_POINT, got %q", out[0].Name)
	}
	if !out[0].Observed {
		t.Fatal("expected observed=true")
	}
}

func TestListCalculatedDataPoints_DeviceNotFound_Returns404(t *testing.T) {
	t.Parallel()
	idx := &stubDeviceIndex{devices: map[string]*device.Device{}}
	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "MISSING", "no": "1"}))
	w := httptest.NewRecorder()
	ListCalculatedDataPoints(idx).ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestListCalculatedDataPoints_ChannelNotFound_Returns404(t *testing.T) {
	t.Parallel()
	d := newTestDevice("DEV0011", "HmIP-STE2")
	idx := &stubDeviceIndex{devices: map[string]*device.Device{"DEV0011": d}}

	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "DEV0011", "no": "99"}))
	w := httptest.NewRecorder()
	ListCalculatedDataPoints(idx).ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestListCalculatedDataPoints_EmptyChannel_ReturnsEmptyList(t *testing.T) {
	t.Parallel()
	d := newTestDevice("DEV0012", "HmIP-STE2")
	d.AddChannel("DEV0012:1", 1, "SENSOR", hmenum.ParamsetKeyValues)
	idx := &stubDeviceIndex{devices: map[string]*device.Device{"DEV0012": d}}

	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "DEV0012", "no": "1"}))
	w := httptest.NewRecorder()
	ListCalculatedDataPoints(idx).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var out []CalculatedDPSummary
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	if len(out) != 0 {
		t.Fatalf("expected empty list, got %d items", len(out))
	}
}

// --- tests: GetCalculatedDataPoint ---

func TestGetCalculatedDataPoint_HappyPath(t *testing.T) {
	t.Parallel()
	d := newTestDevice("DEV0013", "HmIP-STE2")
	addCalculatedDP(d, "DEV0013", "FROST_POINT", 1, hmenum.DataPointCategorySensor, -2.3)
	idx := &stubDeviceIndex{devices: map[string]*device.Device{"DEV0013": d}}

	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "DEV0013", "no": "1", "name": "FROST_POINT"}))
	w := httptest.NewRecorder()
	GetCalculatedDataPoint(idx).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var out CalculatedDPDetail
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Name != "FROST_POINT" {
		t.Fatalf("expected name=FROST_POINT, got %q", out.Name)
	}
	if !out.Observed {
		t.Fatal("expected observed=true")
	}
}

func TestGetCalculatedDataPoint_NotFound_Returns404(t *testing.T) {
	t.Parallel()
	d := newTestDevice("DEV0014", "HmIP-STE2")
	d.AddChannel("DEV0014:1", 1, "SENSOR", hmenum.ParamsetKeyValues)
	idx := &stubDeviceIndex{devices: map[string]*device.Device{"DEV0014": d}}

	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "DEV0014", "no": "1", "name": "MISSING"}))
	w := httptest.NewRecorder()
	GetCalculatedDataPoint(idx).ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestGetCalculatedDataPoint_DeviceNotFound_Returns404(t *testing.T) {
	t.Parallel()
	idx := &stubDeviceIndex{devices: map[string]*device.Device{}}

	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "MISSING", "no": "1", "name": "DEW_POINT"}))
	w := httptest.NewRecorder()
	GetCalculatedDataPoint(idx).ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestGetCalculatedDataPoint_DependsOnPopulated(t *testing.T) {
	t.Parallel()
	d := newTestDevice("DEV0015", "HmIP-STE2")
	addCalculatedDP(d, "DEV0015", "DEW_POINT", 1, hmenum.DataPointCategorySensor, 10.0)
	idx := &stubDeviceIndex{devices: map[string]*device.Device{"DEV0015": d}}

	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "DEV0015", "no": "1", "name": "DEW_POINT"}))
	w := httptest.NewRecorder()
	GetCalculatedDataPoint(idx).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var out CalculatedDPDetail
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	if len(out.DependsOn) == 0 {
		t.Fatal("expected depends_on to be populated for DEW_POINT")
	}
}

func TestGetCalculatedDataPoint_InvalidChannelNo_Returns400(t *testing.T) {
	t.Parallel()
	d := newTestDevice("DEV0016", "HmIP-STE2")
	idx := &stubDeviceIndex{devices: map[string]*device.Device{"DEV0016": d}}

	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "DEV0016", "no": "abc", "name": "DEW_POINT"}))
	w := httptest.NewRecorder()
	GetCalculatedDataPoint(idx).ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

// --- dependsOnForKey branches ---

func TestDependsOnForKey_DefaultCase_ReturnsNil(t *testing.T) {
	t.Parallel()
	key := hmtypes.DataPointKey{Parameter: "UNKNOWN_PARAM"}
	if got := dependsOnForKey(key); got != nil {
		t.Errorf("unknown param: expected nil, got %v", got)
	}
}

func TestDependsOnForKey_DewPoint(t *testing.T) {
	t.Parallel()
	key := hmtypes.DataPointKey{Parameter: "DEW_POINT"}
	got := dependsOnForKey(key)
	if len(got) != 2 {
		t.Errorf("DEW_POINT: expected 2 deps, got %d: %v", len(got), got)
	}
}

func TestDependsOnForKey_OperatingVoltageLevel(t *testing.T) {
	t.Parallel()
	key := hmtypes.DataPointKey{Parameter: "OPERATING_VOLTAGE_LEVEL"}
	got := dependsOnForKey(key)
	if len(got) != 1 {
		t.Errorf("OPERATING_VOLTAGE_LEVEL: expected 1 dep, got %d: %v", len(got), got)
	}
}
