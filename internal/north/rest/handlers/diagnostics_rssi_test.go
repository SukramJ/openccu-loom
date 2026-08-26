// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package handlers_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/north/rest/handlers"
)

type fakeRSSIService struct {
	result map[string]any
	err    error
}

func (f *fakeRSSIService) RSSIInfo(context.Context) (map[string]any, error) {
	return f.result, f.err
}

func TestDiagnosticsRSSI_NilService_Returns503(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/diagnostics/rssi", http.NoBody)
	w := httptest.NewRecorder()
	handlers.DiagnosticsRSSI(nil).ServeHTTP(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("nil service: status = %d, want 503", w.Code)
	}
}

func TestDiagnosticsRSSI_ServiceError_Returns500(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/diagnostics/rssi", http.NoBody)
	w := httptest.NewRecorder()
	handlers.DiagnosticsRSSI(&fakeRSSIService{err: errors.New("boom")}).ServeHTTP(w, req)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("service error: status = %d, want 500", w.Code)
	}
}

func TestDiagnosticsRSSI_OK_ReturnsMatrix(t *testing.T) {
	t.Parallel()
	battLevel := 80
	lowBat := false
	svc := &fakeRSSIService{result: map[string]any{
		"devices": []map[string]any{
			{
				"address":       "DEV001",
				"name":          "Living Room Switch",
				"interface_id":  "BidCos-RF",
				"central":       "ccu-01",
				"rssi_device":   -72,
				"rssi_peer":     nil,
				"battery_level": battLevel,
				"low_battery":   lowBat,
				"reachable":     true,
			},
		},
	}}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/diagnostics/rssi", http.NoBody)
	w := httptest.NewRecorder()
	handlers.DiagnosticsRSSI(svc).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var body struct {
		Devices []struct {
			Address      string `json:"address"`
			InterfaceID  string `json:"interface_id"`
			Central      string `json:"central"`
			RSSIDevice   *int   `json:"rssi_device"`
			RSSIPeer     *int   `json:"rssi_peer"`
			BatteryLevel *int   `json:"battery_level"`
			LowBattery   *bool  `json:"low_battery"`
			Reachable    bool   `json:"reachable"`
		} `json:"devices"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if len(body.Devices) != 1 || body.Devices[0].Address != "DEV001" {
		t.Fatalf("unexpected devices: %+v", body.Devices)
	}
	d := body.Devices[0]
	if d.InterfaceID != "BidCos-RF" || d.Central != "ccu-01" || !d.Reachable {
		t.Errorf("scoping/reachable wrong: %+v", d)
	}
	if d.RSSIDevice == nil || *d.RSSIDevice != -72 {
		t.Errorf("rssi_device = %v, want -72", d.RSSIDevice)
	}
	if d.RSSIPeer != nil {
		t.Errorf("rssi_peer = %v, want null", d.RSSIPeer)
	}
	if d.BatteryLevel == nil || *d.BatteryLevel != 80 {
		t.Errorf("battery_level = %v, want 80", d.BatteryLevel)
	}
	if d.LowBattery == nil || *d.LowBattery != false {
		t.Errorf("low_battery = %v, want false", d.LowBattery)
	}
}
