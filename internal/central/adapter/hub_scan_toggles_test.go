// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/client/transport/jsonrpc"
	"github.com/SukramJ/openccu-loom/internal/model/hub"
)

// hubScanServer serves one internal and one normal program + sysvar.
func hubScanServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		var result any
		switch req["method"] {
		case "Program.getAll":
			result = []map[string]any{
				{"id": "p1", "name": "Normal", "isActive": false, "isInternal": false},
				{"id": "p2", "name": "Internal", "isActive": false, "isInternal": true},
			}
		case "SysVar.getAll":
			result = []map[string]any{
				{"id": "100", "name": "Normal", "type": "BOOL", "value": "false", "isInternal": false, "description": "HAHM kitchen"},
				{"id": "101", "name": "Internal", "type": "BOOL", "value": "false", "isInternal": true, "description": ""},
			}
		default:
			result = nil
		}
		resp, _ := json.Marshal(map[string]any{"result": result, "error": nil})
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(resp)
	}))
}

func newScanClient(t *testing.T, srv *httptest.Server) *jsonrpc.Client {
	t.Helper()
	jc, err := jsonrpc.New(jsonrpc.Config{Endpoint: srv.URL})
	if err != nil {
		t.Fatalf("jsonrpc.New: %v", err)
	}
	return jc
}

func TestLoadProgramsScanDisabledLoadsNothing(t *testing.T) {
	t.Parallel()
	srv := hubScanServer(t)
	defer srv.Close()
	h := hub.NewHub("c")
	if err := loadPrograms(context.Background(), newScanClient(t, srv), nil, h, &noopProgramWriter{},
		hubScanOptions{enableProgramScan: false}); err != nil {
		t.Fatalf("loadPrograms: %v", err)
	}
	if got := len(h.Programs()); got != 0 {
		t.Fatalf("scan disabled should load 0 programs, got %d", got)
	}
}

func TestLoadProgramsInternalFilter(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name            string
		includeInternal bool
		want            int
	}{
		{"exclude_internal", false, 1},
		{"include_internal", true, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			srv := hubScanServer(t)
			defer srv.Close()
			h := hub.NewHub("c")
			err := loadPrograms(context.Background(), newScanClient(t, srv), nil, h, &noopProgramWriter{},
				hubScanOptions{enableProgramScan: true, includeInternalPrograms: tc.includeInternal})
			if err != nil {
				t.Fatalf("loadPrograms: %v", err)
			}
			if got := len(h.Programs()); got != tc.want {
				t.Fatalf("includeInternal=%v: got %d programs, want %d", tc.includeInternal, got, tc.want)
			}
		})
	}
}

func TestLoadSysvarsScanDisabledLoadsNothing(t *testing.T) {
	t.Parallel()
	srv := hubScanServer(t)
	defer srv.Close()
	h := hub.NewHub("c")
	if err := loadSysvars(context.Background(), newScanClient(t, srv), nil, h, nil,
		hubScanOptions{enableSysvarScan: false}); err != nil {
		t.Fatalf("loadSysvars: %v", err)
	}
	if got := len(h.Sysvars()); got != 0 {
		t.Fatalf("scan disabled should load 0 sysvars, got %d", got)
	}
}

func TestLoadSysvarsInternalAndMarkerFilter(t *testing.T) {
	t.Parallel()
	// Internal excluded by default; markers restrict to the HAHM-described one.
	srv := hubScanServer(t)
	defer srv.Close()
	h := hub.NewHub("c")
	err := loadSysvars(context.Background(), newScanClient(t, srv), nil, h, nil,
		hubScanOptions{
			enableSysvarScan:       true,
			includeInternalSysvars: true,
			sysvarMarkers:          []string{"HAHM"},
		})
	if err != nil {
		t.Fatalf("loadSysvars: %v", err)
	}
	// Only the HAHM-described "Normal" sysvar passes the marker filter,
	// even though internal inclusion is on.
	if got := len(h.Sysvars()); got != 1 {
		t.Fatalf("marker filter should keep 1 sysvar, got %d", got)
	}
	if _, ok := h.Sysvar("Normal"); !ok {
		t.Fatal("HAHM-marked sysvar should survive the marker filter")
	}
}

func TestMarkerMatch(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		desc    string
		markers []string
		want    bool
	}{
		{"anything", nil, true},                    // empty markers → match all
		{"HAHM kitchen", []string{"HAHM"}, true},   // prefix match
		{"  HAHM kitchen", []string{"HAHM"}, true}, // leading space trimmed
		{"kitchen HAHM", []string{"HAHM"}, false},  // not a prefix
		{"plain", []string{"HAHM", "MQTT"}, false}, // no marker
		{"MQTT light", []string{"HAHM", "MQTT"}, true},
	} {
		if got := markerMatch(tc.desc, tc.markers); got != tc.want {
			t.Errorf("markerMatch(%q, %v) = %v, want %v", tc.desc, tc.markers, got, tc.want)
		}
	}
}
