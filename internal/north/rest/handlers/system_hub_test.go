// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/hub"
)

func TestGetSystemUpdate(t *testing.T) {
	t.Parallel()
	h := hub.NewHub("test-ccu")
	h.Update.OnInfo(hub.UpdateInfo{
		CurrentFirmware:   "3.75.6",
		AvailableFirmware: "3.77.10",
		UpdateAvailable:   true,
	})
	idx := &testHubIndex{h: h}

	rr := httptest.NewRecorder()
	GetSystemUpdate(idx)(rr, httptest.NewRequest(http.MethodGet, "/system/update", http.NoBody))
	if rr.Code != 200 {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	var out []SystemUpdateEntry
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 || out[0].CurrentFirmware != "3.75.6" || !out[0].UpdateAvailable || !out[0].Observed {
		t.Fatalf("unexpected entry: %+v", out)
	}
}

func TestGetHubMetrics(t *testing.T) {
	t.Parallel()
	h := hub.NewHub("test-ccu")
	h.Metrics.Observe(hub.MetricSystemHealth, 98.5)
	h.Metrics.Observe(hub.MetricConnectionLatMs, 12.3)
	idx := &testHubIndex{h: h}

	rr := httptest.NewRecorder()
	GetHubMetrics(idx)(rr, httptest.NewRequest(http.MethodGet, "/system/metrics", http.NoBody))
	if rr.Code != 200 {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	var out []HubMetricsEntry
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 || out[0].SystemHealth == nil || *out[0].SystemHealth != 98.5 {
		t.Fatalf("system_health: %+v", out)
	}
	// last_event_age was never observed — must be omitted, not zero.
	if out[0].LastEventAgeSeconds != nil {
		t.Fatalf("last_event_age must be nil until observed, got %v", *out[0].LastEventAgeSeconds)
	}
}

func TestInstallModeInterfaces(t *testing.T) {
	t.Parallel()
	h := hub.NewHub("test-ccu")
	dp := hub.NewInstallMode("HmIP-RF", nil)
	dp.OnState(false, 0)
	h.PutInstallMode(dp)
	idx := &testHubIndex{h: h}

	rr := httptest.NewRecorder()
	GetInstallModeInterfaces(idx)(rr, httptest.NewRequest(http.MethodGet, "/install-mode/interfaces", http.NoBody))
	if rr.Code != 200 {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	var out []InstallModeInterfaceEntry
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 || out[0].Interface != "HmIP-RF" || out[0].Active || !out[0].Observed {
		t.Fatalf("unexpected entry: %+v", out)
	}

	// POST without interface → 422.
	rr = httptest.NewRecorder()
	PostInstallModeInterface(idx)(rr, httptest.NewRequest(http.MethodPost, "/install-mode/interfaces",
		strings.NewReader(`{"active":true}`)))
	if rr.Code != 422 {
		t.Fatalf("missing interface: status = %d, want 422", rr.Code)
	}

	// POST for an unknown interface → 404.
	rr = httptest.NewRecorder()
	PostInstallModeInterface(idx)(rr, httptest.NewRequest(http.MethodPost, "/install-mode/interfaces",
		strings.NewReader(`{"interface":"BidCos-RF","active":true}`)))
	if rr.Code != 404 {
		t.Fatalf("unknown interface: status = %d, want 404", rr.Code)
	}
}
