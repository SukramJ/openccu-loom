// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

type fakeSystemCCUReader struct{ entries []SystemCCUEntry }

func (f fakeSystemCCUReader) List(_ context.Context) []SystemCCUEntry { return f.entries }

func TestSystemCCU_EmptyRegistry(t *testing.T) {
	t.Parallel()
	w := httptest.NewRecorder()
	SystemCCU(fakeSystemCCUReader{}).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/system/ccu", http.NoBody))
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var got struct {
		Entries []SystemCCUEntry `json:"entries"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Entries == nil {
		t.Fatal("entries must be [] not null")
	}
	if len(got.Entries) != 0 {
		t.Fatalf("len=%d, want 0", len(got.Entries))
	}
}

func TestSystemCCU_NilReader(t *testing.T) {
	t.Parallel()
	w := httptest.NewRecorder()
	SystemCCU(nil).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/system/ccu", http.NoBody))
	if w.Code != http.StatusOK {
		t.Fatalf("nil reader must not 500: status=%d", w.Code)
	}
}

func TestSystemCCU_HappyPath(t *testing.T) {
	t.Parallel()
	reader := fakeSystemCCUReader{entries: []SystemCCUEntry{
		{
			Name: "home", Host: "192.0.2.29", Available: true,
			Model: "OpenCCU", Version: "3.79.6.20240803",
			Hostname: "homematic-raspi", Serial: "OEQ1234567",
			URL: "http://homematic-raspi", IsHaApp: false,
			ConfiguredInterfaces: []string{"HmIP-RF", "BidCos-RF"},
		},
	}}
	w := httptest.NewRecorder()
	SystemCCU(reader).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/system/ccu", http.NoBody))
	var got struct {
		Entries []SystemCCUEntry `json:"entries"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	if len(got.Entries) != 1 {
		t.Fatalf("len=%d, want 1", len(got.Entries))
	}
	e := got.Entries[0]
	if e.Name != "home" || e.Serial != "OEQ1234567" || !e.Available {
		t.Fatalf("entry round-trip failed: %+v", e)
	}
	if len(e.ConfiguredInterfaces) != 2 {
		t.Fatalf("interfaces len=%d", len(e.ConfiguredInterfaces))
	}
}

// TestSystemCCU_CCUReportedFactsWireShape pins the JSON shape of the
// CCU-sourced fields. The two security flags are always present (a bool
// without omitempty, so "false" is distinguishable from a client that
// predates the field), while ccu_interfaces is omitted entirely until the
// CCU has reported one — the SPA reads its absence as "not discovered yet"
// rather than as "no interfaces".
func TestSystemCCU_CCUReportedFactsWireShape(t *testing.T) {
	t.Parallel()
	reader := fakeSystemCCUReader{entries: []SystemCCUEntry{
		{
			Name: "reported", Available: true,
			AuthEnabled:          true,
			HTTPSRedirectEnabled: false,
			ConfiguredInterfaces: []string{"HmIP-RF"},
			CCUInterfaces: []SystemCCUInterface{
				{Type: "HmIP-RF", Address: "HmIP-RF", Port: 2010, URL: "http://ccu:2010"},
				{Type: "CUxD", Address: "CUxD", Port: 8701},
			},
		},
		{Name: "silent", Available: false},
	}}
	w := httptest.NewRecorder()
	SystemCCU(reader).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/system/ccu", http.NoBody))

	var raw struct {
		Entries []map[string]any `json:"entries"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &raw); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(raw.Entries) != 2 {
		t.Fatalf("len=%d, want 2", len(raw.Entries))
	}

	reported := raw.Entries[0]
	if reported["auth_enabled"] != true {
		t.Errorf("auth_enabled = %v, want true", reported["auth_enabled"])
	}
	// Present-but-false, not absent: an operator reading the fleet view must
	// be able to tell "the CCU says no" from "nobody asked".
	redirect, present := reported["https_redirect_enabled"]
	if !present {
		t.Error("https_redirect_enabled missing — a false bool must still serialise")
	}
	if redirect != false {
		t.Errorf("https_redirect_enabled = %v, want false", redirect)
	}
	ifaces, ok := reported["ccu_interfaces"].([]any)
	if !ok {
		t.Fatalf("ccu_interfaces = %T, want array", reported["ccu_interfaces"])
	}
	if len(ifaces) != 2 {
		t.Fatalf("ccu_interfaces len=%d, want 2", len(ifaces))
	}
	first, _ := ifaces[0].(map[string]any)
	if first["address"] != "HmIP-RF" || first["port"] != float64(2010) {
		t.Errorf("ccu_interfaces[0] = %+v", first)
	}
	if first["url"] != "http://ccu:2010" {
		t.Errorf("ccu_interfaces[0].url = %v", first["url"])
	}
	// The CUxD entry carries no URL — omitempty must drop the key rather
	// than emit an empty string.
	second, _ := ifaces[1].(map[string]any)
	if _, hasURL := second["url"]; hasURL {
		t.Errorf("ccu_interfaces[1] carries an empty url key: %+v", second)
	}

	silent := raw.Entries[1]
	if _, hasIfaces := silent["ccu_interfaces"]; hasIfaces {
		t.Errorf("ccu_interfaces present for a central that reported none: %+v", silent)
	}
	if silent["auth_enabled"] != false {
		t.Errorf("auth_enabled = %v, want false", silent["auth_enabled"])
	}
}
