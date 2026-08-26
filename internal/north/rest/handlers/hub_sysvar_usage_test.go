// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/hub"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// fakeUsageMutator satisfies the whole hub.Mutator bundle plus
// hub.SysvarUsageReader so hub.SetMutator wires the usage reader.
type fakeUsageMutator struct {
	programs []hub.SysvarUsage
	err      error
}

func (f *fakeUsageMutator) CreateSysvar(context.Context, hub.SysvarCreateSpec) error { return nil }

func (f *fakeUsageMutator) UpdateSysvar(context.Context, hub.SysvarUpdateSpec) error { return nil }

func (f *fakeUsageMutator) DeleteSysvar(context.Context, string) error { return nil }

func (f *fakeUsageMutator) SetDeviceRooms(context.Context, string, []string) error { return nil }

func (f *fakeUsageMutator) SetDeviceFunctions(context.Context, string, []string) error { return nil }

func (f *fakeUsageMutator) TriggerBackup(context.Context) error { return nil }

func (f *fakeUsageMutator) BackupStatus(context.Context) (string, error) { return "", nil }

func (f *fakeUsageMutator) TriggerFirmwareUpdate(context.Context) error { return nil }

func (f *fakeUsageMutator) AcceptDeviceInInbox(context.Context, string) error { return nil }

func (f *fakeUsageMutator) SysvarUsagePrograms(context.Context, string) ([]hub.SysvarUsage, error) {
	return f.programs, f.err
}

func hubWithUsage(t *testing.T, programs []hub.SysvarUsage) *testHubIndex {
	t.Helper()
	h := hub.NewHub("test-ccu")
	h.PutSysvar(&hub.Sysvar{HubDataPoint: hub.HubDataPoint{Name: "Alarm"}, ValueType: hmenum.HubValueTypeLogic})
	// A program known to the hub registry drives the enrichment branch.
	h.PutProgram(&hub.Program{HubDataPoint: hub.HubDataPoint{Name: "Morning Routine"}, ID: "P1"})
	h.SetMutator(&fakeUsageMutator{programs: programs})
	return &testHubIndex{h: h}
}

func TestGetSysvarUsage_EnrichesFromRegistry(t *testing.T) {
	t.Parallel()
	idx := hubWithUsage(t, []hub.SysvarUsage{
		{ID: "P1", Name: "raw-name", Active: false}, // enriched from registry
		{ID: "P2", Name: "Only In ReGa", Active: true},
	})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/sysvars/Alarm/usage", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"name": "Alarm"}))
	w := httptest.NewRecorder()
	GetSysvarUsage(idx).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var body SysvarUsageResponse
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body.Sysvar != "Alarm" || len(body.Programs) != 2 {
		t.Fatalf("body = %+v", body)
	}
	// P1 is known to the registry → localized name + unique_id set.
	if body.Programs[0].Name != "Morning Routine" || body.Programs[0].UniqueID == "" {
		t.Errorf("P1 not enriched: %+v", body.Programs[0])
	}
	// P2 is only in ReGa → its raw name, no unique_id.
	if body.Programs[1].Name != "Only In ReGa" || body.Programs[1].UniqueID != "" {
		t.Errorf("P2 fallback wrong: %+v", body.Programs[1])
	}
}

func TestGetSysvarUsage_UnknownSysvar_Returns404(t *testing.T) {
	t.Parallel()
	idx := hubWithUsage(t, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/sysvars/Nope/usage", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"name": "Nope"}))
	w := httptest.NewRecorder()
	GetSysvarUsage(idx).ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestGetSysvarUsage_NoReader_Returns503(t *testing.T) {
	t.Parallel()
	// A hub with a sysvar but no mutator/reader wired.
	h := hub.NewHub("test-ccu")
	h.PutSysvar(&hub.Sysvar{HubDataPoint: hub.HubDataPoint{Name: "Alarm"}, ValueType: hmenum.HubValueTypeLogic})
	idx := &testHubIndex{h: h}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/sysvars/Alarm/usage", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"name": "Alarm"}))
	w := httptest.NewRecorder()
	GetSysvarUsage(idx).ServeHTTP(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", w.Code)
	}
}

func TestGetSysvarUsage_NilIndex_Returns503(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/sysvars/Alarm/usage", http.NoBody)
	w := httptest.NewRecorder()
	GetSysvarUsage(nil).ServeHTTP(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", w.Code)
	}
}
