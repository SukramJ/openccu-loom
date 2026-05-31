// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

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
			Name: "home", Host: "172.18.4.29", Available: true,
			Model: "RaspberryMatic", Version: "3.79.6.20240803",
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
