// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/client/transport/jsonrpc"
	"github.com/SukramJ/openccu-loom/internal/model/hub"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
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

func TestLoadProgramsLoadsInternalUnfiltered(t *testing.T) {
	t.Parallel()
	// The fetch is always complete: internal programs are loaded into the
	// hub regardless of include_internal_programs. The flag only records
	// the per-central delivery default, surfaced via
	// IncludeInternalProgramsDefault for the northbound list filter.
	for _, tc := range []struct {
		name            string
		includeInternal bool
	}{
		{"default_hide", false},
		{"opt_in_show", true},
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
			if got := len(h.Programs()); got != 2 {
				t.Fatalf("includeInternal=%v: expected both programs loaded, got %d", tc.includeInternal, got)
			}
			if got := h.IncludeInternalProgramsDefault(); got != tc.includeInternal {
				t.Fatalf("IncludeInternalProgramsDefault=%v, want %v", got, tc.includeInternal)
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

// TestLoadSysvarsMarkersDriveEnabledNotImport pins the documented
// contract: markers decide whether a sysvar arrives ENABLED, never
// whether it is imported at all.
//
// The reference stack states it explicitly - "all variables are imported
// as disabled entities. With markers configured ..., only marked
// variables are imported as enabled entities". Treating the marker as an
// import filter (which this code did) hid most of a CCU's catalogue and
// made it unreachable: an entity that is never created cannot be enabled
// by the operator afterwards. On a real install that left 23 of 83
// sysvars and 2 of 40 programs visible.
func TestLoadSysvarsMarkersDriveEnabledNotImport(t *testing.T) {
	t.Parallel()
	srv := hubScanServer(t)
	defer srv.Close()

	// Baseline: how many sysvars exist without any marker configured.
	all := hub.NewHub("c")
	if err := loadSysvars(context.Background(), newScanClient(t, srv), nil, all, nil,
		hubScanOptions{enableSysvarScan: true, includeInternalSysvars: true}); err != nil {
		t.Fatalf("loadSysvars (no markers): %v", err)
	}
	want := len(all.Sysvars())
	if want < 2 {
		t.Fatalf("fixture needs at least two sysvars to be meaningful, got %d", want)
	}

	h := hub.NewHub("c")
	err := loadSysvars(context.Background(), newScanClient(t, srv), nil, h, nil,
		hubScanOptions{
			enableSysvarScan:       true,
			includeInternalSysvars: true,
			sysvarMarkers:          []hmenum.DescriptionMarker{hmenum.DescriptionMarkerHAHM},
		})
	if err != nil {
		t.Fatalf("loadSysvars: %v", err)
	}
	// Configuring a marker must not shrink the catalogue.
	if got := len(h.Sysvars()); got != want {
		t.Fatalf("markers changed the imported count: got %d, want %d (markers gate enabled-by-default, not import)", got, want)
	}
	sv, ok := h.Sysvar("Normal")
	if !ok {
		t.Fatal("HAHM-marked sysvar missing from the model")
	}
	if !sv.EnabledByDefault() {
		t.Error("marker-matched sysvar should be enabled by default")
	}
	// An unmarked sysvar is still imported - just disabled.
	var sawUnmarkedDisabled bool
	for _, other := range h.Sysvars() {
		if other.Name != "Normal" && !other.EnabledByDefault() {
			sawUnmarkedDisabled = true
		}
	}
	if !sawUnmarkedDisabled {
		t.Error("expected at least one unmarked sysvar to be imported but disabled")
	}
}

func TestLoadSysvarsNoMarkersDisabledByDefault(t *testing.T) {
	t.Parallel()
	// Without markers every sysvar is included but disabled by default.
	srv := hubScanServer(t)
	defer srv.Close()
	h := hub.NewHub("c")
	if err := loadSysvars(context.Background(), newScanClient(t, srv), nil, h, nil,
		hubScanOptions{enableSysvarScan: true, includeInternalSysvars: true}); err != nil {
		t.Fatalf("loadSysvars: %v", err)
	}
	if len(h.Sysvars()) == 0 {
		t.Fatal("expected sysvars without a marker filter")
	}
	for _, sv := range h.Sysvars() {
		if sv.EnabledByDefault() {
			t.Errorf("sysvar %q should be disabled by default without markers", sv.Name)
		}
	}
}

func TestHubEnabledDefault(t *testing.T) {
	t.Parallel()
	hahm := []hmenum.DescriptionMarker{hmenum.DescriptionMarkerHAHM}
	internal := []hmenum.DescriptionMarker{hmenum.DescriptionMarkerInternal}
	for _, tc := range []struct {
		name       string
		isInternal bool
		desc       string
		markers    []hmenum.DescriptionMarker
		want       bool
	}{
		{"no markers", false, "HAHM x", nil, false},
		{"non-internal match", false, "HAHM x", hahm, true},
		{"non-internal no match", false, "plain", hahm, false},
		{"internal with INTERNAL marker", true, "x", internal, true},
		{"internal without INTERNAL marker", true, "HAHM x", hahm, false},
	} {
		if got := hubEnabledDefault(tc.isInternal, tc.desc, tc.markers); got != tc.want {
			t.Errorf("%s: hubEnabledDefault = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestMarkerMatch(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		desc    string
		markers []hmenum.DescriptionMarker
		want    bool
	}{
		{"anything", nil, true}, // empty markers → match all
		{"HAHM kitchen", []hmenum.DescriptionMarker{hmenum.DescriptionMarkerHAHM}, true},                         // marker at the start
		{"  HAHM kitchen", []hmenum.DescriptionMarker{hmenum.DescriptionMarkerHAHM}, true},                       // leading space trimmed
		{"kitchen HAHM", []hmenum.DescriptionMarker{hmenum.DescriptionMarkerHAHM}, true},                         // marker anywhere: substring, matching parseSysvarDescription
		{"plain", []hmenum.DescriptionMarker{hmenum.DescriptionMarkerHAHM, hmenum.DescriptionMarkerMQTT}, false}, // no marker
		{"MQTT light", []hmenum.DescriptionMarker{hmenum.DescriptionMarkerHAHM, hmenum.DescriptionMarkerMQTT}, true},
	} {
		if got := markerMatch(tc.desc, tc.markers); got != tc.want {
			t.Errorf("markerMatch(%q, %v) = %v, want %v", tc.desc, tc.markers, got, tc.want)
		}
	}
}

// TestInternalMarkerOpensInternalDelivery pins the meaning the reference
// gives the INTERNAL marker - "includes CCU-internal variables/programs" -
// against the include_internal_* booleans.
//
// The two are independent requests, and either alone must suffice. This
// matters because the CCU classifies most ordinary user programs as
// internal: on a real install 38 of 40 carried the flag, so honouring
// only the boolean hid them from an operator whose marker list contained
// INTERNAL.
func TestInternalMarkerOpensInternalDelivery(t *testing.T) {
	t.Parallel()
	internal := []hmenum.DescriptionMarker{hmenum.DescriptionMarkerInternal}
	other := []hmenum.DescriptionMarker{hmenum.DescriptionMarkerHX}
	for _, tc := range []struct {
		name    string
		markers []hmenum.DescriptionMarker
		want    bool
	}{
		{"INTERNAL configured", internal, true},
		{"INTERNAL among others", []hmenum.DescriptionMarker{hmenum.DescriptionMarkerMQTT, hmenum.DescriptionMarkerInternal}, true},
		{"other marker only", other, false},
		{"no markers", nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := hasInternalMarker(tc.markers); got != tc.want {
				t.Errorf("hasInternalMarker(%v) = %v, want %v", tc.markers, got, tc.want)
			}
		})
	}
}

// TestLoadSysvarsInternalMarkerIncludesInternal covers the sysvar path
// end to end: with the boolean off but INTERNAL configured, internal
// sysvars still reach the model.
func TestLoadSysvarsInternalMarkerIncludesInternal(t *testing.T) {
	t.Parallel()
	srv := hubScanServer(t)
	defer srv.Close()

	withBool := hub.NewHub("c")
	if err := loadSysvars(context.Background(), newScanClient(t, srv), nil, withBool, nil,
		hubScanOptions{enableSysvarScan: true, includeInternalSysvars: true}); err != nil {
		t.Fatalf("loadSysvars (boolean): %v", err)
	}
	viaMarker := hub.NewHub("c")
	if err := loadSysvars(context.Background(), newScanClient(t, srv), nil, viaMarker, nil,
		hubScanOptions{
			enableSysvarScan:       true,
			includeInternalSysvars: false,
			sysvarMarkers:          []hmenum.DescriptionMarker{hmenum.DescriptionMarkerInternal},
		}); err != nil {
		t.Fatalf("loadSysvars (marker): %v", err)
	}
	if got, want := len(viaMarker.Sysvars()), len(withBool.Sysvars()); got != want {
		t.Errorf("INTERNAL marker admitted %d sysvars, the boolean admits %d — either alone must suffice", got, want)
	}
}
