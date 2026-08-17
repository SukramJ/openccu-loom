// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/model/hub"
)

// TestGetHubDataPoints_NilIndex returns 200 with an empty JSON array.
func TestGetHubDataPoints_NilIndex(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	GetHubDataPoints(nil)(rr, httptest.NewRequest(http.MethodGet, "/api/v1/hub/data-points", http.NoBody))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	var out []HubDataPoints
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(out) != 0 {
		t.Fatalf("expected empty array, got %d entries", len(out))
	}
}

// TestGetHubDataPoints_EmptyHubs returns 200 with an empty JSON array when Hubs() is empty.
func TestGetHubDataPoints_EmptyHubs(t *testing.T) {
	t.Parallel()
	idx := &testHubIndex{h: nil}
	rr := httptest.NewRecorder()
	GetHubDataPoints(idx)(rr, httptest.NewRequest(http.MethodGet, "/api/v1/hub/data-points", http.NoBody))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	var out []HubDataPoints
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(out) != 0 {
		t.Fatalf("expected empty array, got %d entries", len(out))
	}
}

// TestGetHubDataPoints_OneCentral_PopulatedSingletons verifies counts,
// metrics (with units), connectivity, install-mode, update flags and
// all legacy_name strings for a single populated central.
func TestGetHubDataPoints_OneCentral_PopulatedSingletons(t *testing.T) {
	t.Parallel()

	h := hub.NewHub("ccu-a")

	// alarm messages: 2 entries
	h.Messages.Replace([]hub.AlarmMessage{
		{ID: "A1", Name: "Window", Timestamp: time.Now(), Counter: 1},
		{ID: "A2", Name: "Door", Timestamp: time.Now(), Counter: 1},
	})

	// service messages: 1 entry
	h.ServiceMessages.Replace([]hub.ServiceMessage{
		{ID: "S1", Name: "Low battery", Timestamp: time.Now(), Counter: 1},
	})

	// inbox: 3 entries
	h.Inbox.Replace([]hub.InboxDevice{
		{Address: "addr1:0"}, {Address: "addr2:0"}, {Address: "addr3:0"},
	})

	// update: available, not in progress
	h.Update.OnInfo(hub.UpdateInfo{
		CurrentFirmware:   "3.75.6",
		AvailableFirmware: "3.77.10",
		UpdateAvailable:   true,
	})

	// metrics: all three kinds
	h.Metrics.Observe(hub.MetricSystemHealth, 95.0)
	h.Metrics.Observe(hub.MetricConnectionLatMs, 42.5)
	h.Metrics.Observe(hub.MetricLastEventAgeSecs, 7.0)

	// connectivity: two interfaces
	conn := hub.NewConnectivity()
	conn.OnState("HmIP-RF", true)
	conn.OnState("BidCos-RF", false)
	h.SetConnectivity(conn)

	// install mode: one interface, observed, disabled
	im := hub.NewInstallMode("HmIP-RF", nil)
	im.OnState(false, 0)
	h.PutInstallMode(im)

	idx := &testHubIndex{h: h, centralName: "ccu-a"}

	rr := httptest.NewRecorder()
	GetHubDataPoints(idx)(rr, httptest.NewRequest(http.MethodGet, "/api/v1/hub/data-points", http.NoBody))

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	var out []HubDataPoints
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(out))
	}

	dp := out[0]

	if dp.Central != "ccu-a" {
		t.Errorf("central=%q want ccu-a", dp.Central)
	}

	// legacy_name strings
	if dp.AlarmMessages.LegacyName != "alarm_messages" {
		t.Errorf("alarm legacy_name=%q want alarm_messages", dp.AlarmMessages.LegacyName)
	}
	if dp.ServiceMessages.LegacyName != "service_messages" {
		t.Errorf("service legacy_name=%q want service_messages", dp.ServiceMessages.LegacyName)
	}
	if dp.Inbox.LegacyName != "inbox" {
		t.Errorf("inbox legacy_name=%q want inbox", dp.Inbox.LegacyName)
	}
	if dp.Update.LegacyName != "system_update" {
		t.Errorf("update legacy_name=%q want system_update", dp.Update.LegacyName)
	}

	// counts
	if dp.AlarmMessages.Value != 2 {
		t.Errorf("alarm count=%d want 2", dp.AlarmMessages.Value)
	}
	if dp.ServiceMessages.Value != 1 {
		t.Errorf("service count=%d want 1", dp.ServiceMessages.Value)
	}
	if dp.Inbox.Value != 3 {
		t.Errorf("inbox count=%d want 3", dp.Inbox.Value)
	}

	// update flags
	if !dp.Update.UpdateAvailable {
		t.Error("update_available must be true")
	}
	if dp.Update.InProgress {
		t.Error("in_progress must be false")
	}

	// metrics: fixed order system_health, connection_latency_ms, last_event_age_seconds
	if len(dp.Metrics) != 3 {
		t.Fatalf("metrics count=%d want 3", len(dp.Metrics))
	}
	checkMetric(t, dp.Metrics[0], string(hub.MetricSystemHealth), 95.0, "%")
	checkMetric(t, dp.Metrics[1], string(hub.MetricConnectionLatMs), 42.5, "ms")
	checkMetric(t, dp.Metrics[2], string(hub.MetricLastEventAgeSecs), 7.0, "s")

	// connectivity: sorted by interface ID → BidCos-RF first, HmIP-RF second
	if len(dp.Connectivity) != 2 {
		t.Fatalf("connectivity count=%d want 2", len(dp.Connectivity))
	}
	if dp.Connectivity[0].InterfaceID != "BidCos-RF" || dp.Connectivity[0].Reachable {
		t.Errorf("connectivity[0]=%+v want BidCos-RF reachable=false", dp.Connectivity[0])
	}
	if dp.Connectivity[1].InterfaceID != "HmIP-RF" || !dp.Connectivity[1].Reachable {
		t.Errorf("connectivity[1]=%+v want HmIP-RF reachable=true", dp.Connectivity[1])
	}

	// install mode: the DP carries the bare interface name, but the aggregate
	// promotes it to the wire id `<central>-<interface>` so it lines up with the
	// connectivity sibling and GET /interfaces (a client keys install-mode
	// entries onto the interface list by exactly this id).
	if len(dp.InstallMode) != 1 {
		t.Fatalf("install_mode count=%d want 1", len(dp.InstallMode))
	}
	if dp.InstallMode[0].InterfaceID != "ccu-a-HmIP-RF" {
		t.Errorf("install_mode interface=%q want ccu-a-HmIP-RF", dp.InstallMode[0].InterfaceID)
	}
	if dp.InstallMode[0].Enabled {
		t.Error("install_mode enabled must be false")
	}
	if !dp.InstallMode[0].Observed {
		t.Error("install_mode observed must be true after OnState")
	}
}

// TestGetHubDataPoints_MultipleCentrals ensures one array entry per central.
func TestGetHubDataPoints_MultipleCentrals(t *testing.T) {
	t.Parallel()

	h1 := hub.NewHub("ccu-alpha")
	h1.Messages.Replace([]hub.AlarmMessage{{ID: "A1", Timestamp: time.Now(), Counter: 1}})

	h2 := hub.NewHub("ccu-beta")

	idx := &multiHubIndex{hubs: []NamedHub{
		{Central: "ccu-alpha", Hub: h1},
		{Central: "ccu-beta", Hub: h2},
	}}

	rr := httptest.NewRecorder()
	GetHubDataPoints(idx)(rr, httptest.NewRequest(http.MethodGet, "/api/v1/hub/data-points", http.NoBody))

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	var out []HubDataPoints
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(out))
	}

	centrals := map[string]bool{}
	for _, dp := range out {
		centrals[dp.Central] = true
	}
	if !centrals["ccu-alpha"] || !centrals["ccu-beta"] {
		t.Errorf("missing central in output: %v", centrals)
	}

	// ccu-alpha has 1 alarm message
	for _, dp := range out {
		if dp.Central == "ccu-alpha" && dp.AlarmMessages.Value != 1 {
			t.Errorf("ccu-alpha alarm count=%d want 1", dp.AlarmMessages.Value)
		}
		if dp.Central == "ccu-beta" && dp.AlarmMessages.Value != 0 {
			t.Errorf("ccu-beta alarm count=%d want 0", dp.AlarmMessages.Value)
		}
	}
}

// TestGetHubDataPoints_EmptyHub_ZeroCountsAndOmittedSlices verifies that a
// hub with no observations produces zero counts and omits metrics, connectivity
// and install_mode (omitempty).
func TestGetHubDataPoints_EmptyHub_ZeroCountsAndOmittedSlices(t *testing.T) {
	t.Parallel()

	h := hub.NewHub("ccu-empty")
	idx := &testHubIndex{h: h, centralName: "ccu-empty"}

	rr := httptest.NewRecorder()
	GetHubDataPoints(idx)(rr, httptest.NewRequest(http.MethodGet, "/api/v1/hub/data-points", http.NoBody))

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	var out []HubDataPoints
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(out))
	}

	dp := out[0]
	if dp.AlarmMessages.Value != 0 {
		t.Errorf("alarm count=%d want 0", dp.AlarmMessages.Value)
	}
	if dp.ServiceMessages.Value != 0 {
		t.Errorf("service count=%d want 0", dp.ServiceMessages.Value)
	}
	if dp.Inbox.Value != 0 {
		t.Errorf("inbox count=%d want 0", dp.Inbox.Value)
	}
	if dp.Update.UpdateAvailable {
		t.Error("update_available must be false for unobserved hub")
	}

	// omitempty fields must be absent in raw JSON
	var raw []map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &raw); err != nil {
		t.Fatalf("unmarshal raw: %v", err)
	}
	entry := raw[0]
	if _, ok := entry["metrics"]; ok {
		t.Error("metrics must be omitted when empty")
	}
	if _, ok := entry["connectivity"]; ok {
		t.Error("connectivity must be omitted when empty")
	}
	if _, ok := entry["install_mode"]; ok {
		t.Error("install_mode must be omitted when empty")
	}
}

// checkMetric is a helper that asserts a HubMetricDataPoint's fields.
func checkMetric(t *testing.T, m HubMetricDataPoint, wantName string, wantValue float64, wantUnit string) {
	t.Helper()
	if m.LegacyName != wantName {
		t.Errorf("metric legacy_name=%q want %q", m.LegacyName, wantName)
	}
	if m.Value != wantValue {
		t.Errorf("metric[%s] value=%v want %v", wantName, m.Value, wantValue)
	}
	if m.Unit != wantUnit {
		t.Errorf("metric[%s] unit=%q want %q", m.LegacyName, m.Unit, wantUnit)
	}
}
