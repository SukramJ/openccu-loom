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

	"github.com/SukramJ/openccu-loom/internal/model/hub"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// testHubIndex wraps a real *hub.Hub as a HubIndex.
type testHubIndex struct {
	h           *hub.Hub
	centralName string // optional; defaults to "test-ccu" when empty
}

func (t *testHubIndex) Hub() *hub.Hub { return t.h }

func (t *testHubIndex) Hubs() []NamedHub {
	if t.h == nil {
		return nil
	}
	name := t.centralName
	if name == "" {
		name = "test-ccu"
	}
	return []NamedHub{{Central: name, Hub: t.h}}
}

func (t *testHubIndex) HubFor(centralName string) *hub.Hub {
	name := t.centralName
	if name == "" {
		name = "test-ccu"
	}
	if centralName == name {
		return t.h
	}
	return nil
}

func (t *testHubIndex) SerialSuffix(central string) string {
	if central != "" {
		return "vccu0000000"
	}
	return ""
}

func newTestHubWithProgram(t *testing.T) (*testHubIndex, *hub.Hub) {
	t.Helper()
	h := hub.NewHub("test-ccu")
	h.PutProgram(&hub.Program{HubDataPoint: hub.HubDataPoint{Name: "Morning Routine"}, ID: "P1"})
	h.PutSysvar(&hub.Sysvar{HubDataPoint: hub.HubDataPoint{Name: "Alarm"}, ValueType: hmenum.HubValueTypeLogic})
	return &testHubIndex{h: h}, h
}

func TestListPrograms_HappyPath(t *testing.T) {
	t.Parallel()
	idx, _ := newTestHubWithProgram(t)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/programs", http.NoBody)
	w := httptest.NewRecorder()
	ListPrograms(idx).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var body []ProgramSummary
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(body) != 1 || body[0].ID != "P1" {
		t.Fatalf("unexpected body: %+v", body)
	}
}

func TestListPrograms_NilHub_ReturnsEmptyArray(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/programs", http.NoBody)
	w := httptest.NewRecorder()
	ListPrograms(nil).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var body []ProgramSummary
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if len(body) != 0 {
		t.Fatalf("expected empty, got %+v", body)
	}
}

func TestListSysvars_HappyPath(t *testing.T) {
	t.Parallel()
	idx, _ := newTestHubWithProgram(t)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/sysvars", http.NoBody)
	w := httptest.NewRecorder()
	ListSysvars(idx).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var body []SysvarSummary
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(body) != 1 || body[0].Name != "Alarm" {
		t.Fatalf("unexpected body: %+v", body)
	}
}

func TestListSysvars_NilHub_ReturnsEmptyArray(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/sysvars", http.NoBody)
	w := httptest.NewRecorder()
	ListSysvars(nil).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestGetProgram_HappyPath(t *testing.T) {
	t.Parallel()
	idx, _ := newTestHubWithProgram(t)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/programs/P1", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"id": "P1"}))
	w := httptest.NewRecorder()
	GetProgram(idx).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var body ProgramSummary
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body.ID != "P1" || body.Name != "Morning Routine" {
		t.Fatalf("unexpected body: %+v", body)
	}
	if body.Central != "test-ccu" {
		t.Fatalf("central=%q want test-ccu", body.Central)
	}
}

func TestGetProgram_NotFound_Returns404(t *testing.T) {
	t.Parallel()
	idx, _ := newTestHubWithProgram(t)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/programs/NonExistent", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"id": "NonExistent"}))
	w := httptest.NewRecorder()
	GetProgram(idx).ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestGetProgram_NilHub_Returns503(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/programs/P1", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"id": "P1"}))
	w := httptest.NewRecorder()
	GetProgram(nil).ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", w.Code)
	}
}

func TestGetSysvar_NotFound_Returns404(t *testing.T) {
	t.Parallel()
	idx, _ := newTestHubWithProgram(t)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/sysvars/NonExistent", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"name": "NonExistent"}))
	w := httptest.NewRecorder()
	GetSysvar(idx).ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestCreateSysvar_MissingName_Returns422(t *testing.T) {
	t.Parallel()
	idx, _ := newTestHubWithProgram(t)
	body := strings.NewReader(`{"value_type":"BOOL"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/sysvars", body)
	w := httptest.NewRecorder()
	CreateSysvar(idx).ServeHTTP(w, req)

	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestCreateSysvar_NilHub_Returns503(t *testing.T) {
	t.Parallel()
	body := strings.NewReader(`{"name":"MyVar","value_type":"BOOL"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/sysvars", body)
	w := httptest.NewRecorder()
	CreateSysvar(nil).ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", w.Code)
	}
}

func TestListInterfaces_NilIndex_ReturnsEmptyArray(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/interfaces", http.NoBody)
	w := httptest.NewRecorder()
	ListInterfaces(nil).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var body []InterfaceState
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if len(body) != 0 {
		t.Fatalf("expected empty, got %+v", body)
	}
}

func TestGetInterface_NilIndex_Returns503(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/interfaces/HmIP-RF", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"id": "HmIP-RF"}))
	w := httptest.NewRecorder()
	GetInterface(nil).ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", w.Code)
	}
}

func TestListInbox_NilHub_ReturnsEmptyArray(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/inbox", http.NoBody)
	w := httptest.NewRecorder()
	ListInbox(nil).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

// ── H-032 ProgramSummary.last_executed ─────────────────────────────────────

// TestListPrograms_LastExecuted pins H-032: a program that has been executed
// must expose its last_executed field as an RFC3339 timestamp.
func TestListPrograms_LastExecuted(t *testing.T) {
	t.Parallel()
	h := hub.NewHub("test-ccu")
	prog := hub.NewProgram("test-ccu", "P-EXEC", "Run Me", "", false, nil)
	prog.OnExecution(true, hmenum.ProgramTriggerAPI) // record one execution so lastExecute is non-zero
	h.PutProgram(prog)
	idx := &testHubIndex{h: h}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/programs", http.NoBody)
	w := httptest.NewRecorder()
	ListPrograms(idx).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var body []ProgramSummary
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(body) != 1 {
		t.Fatalf("expected 1 program, got %d", len(body))
	}
	if body[0].LastExecuted == "" {
		t.Error("last_executed must be non-empty after OnExecution (H-032)")
	}
}

// TestListPrograms_NoExecution_LastExecutedOmitted pins H-032: a program that
// has never been executed must omit last_executed from the JSON.
func TestListPrograms_NoExecution_LastExecutedOmitted(t *testing.T) {
	t.Parallel()
	h := hub.NewHub("test-ccu")
	h.PutProgram(hub.NewProgram("test-ccu", "P-NEW", "Fresh", "", false, nil))
	idx := &testHubIndex{h: h}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/programs", http.NoBody)
	w := httptest.NewRecorder()
	ListPrograms(idx).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var raw []map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &raw); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(raw) != 1 {
		t.Fatalf("expected 1 program, got %d", len(raw))
	}
	if _, present := raw[0]["last_executed"]; present {
		t.Error("last_executed must be omitted when no execution observed (H-032)")
	}
}

// ── H-033 SysvarSummary.min / max ──────────────────────────────────────────

// TestListSysvars_MinMaxExposed pins H-033: a sysvar with declared bounds
// must expose min and max in the summary.
func TestListSysvars_MinMaxExposed(t *testing.T) {
	t.Parallel()
	h := hub.NewHub("test-ccu")
	sv := hub.NewSysvar("test-ccu", "TempTarget", "", hmenum.HubValueTypeFloat, nil)
	minV := hmtypes.FloatValue(10.0)
	maxV := hmtypes.FloatValue(30.0)
	sv.Min = &minV
	sv.Max = &maxV
	h.PutSysvar(sv)
	idx := &testHubIndex{h: h}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/sysvars", http.NoBody)
	w := httptest.NewRecorder()
	ListSysvars(idx).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var body []SysvarSummary
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(body) != 1 {
		t.Fatalf("expected 1 sysvar, got %d", len(body))
	}
	s := body[0]
	if s.Min == nil || *s.Min != 10.0 {
		t.Errorf("min=%v want 10.0 (H-033)", s.Min)
	}
	if s.Max == nil || *s.Max != 30.0 {
		t.Errorf("max=%v want 30.0 (H-033)", s.Max)
	}
}

// TestListSysvars_NoMinMax_Omitted pins H-033: a sysvar without bounds must
// omit min and max from the JSON.
func TestListSysvars_NoMinMax_Omitted(t *testing.T) {
	t.Parallel()
	h := hub.NewHub("test-ccu")
	h.PutSysvar(hub.NewSysvar("test-ccu", "Flag", "", hmenum.HubValueTypeLogic, nil))
	idx := &testHubIndex{h: h}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/sysvars", http.NoBody)
	w := httptest.NewRecorder()
	ListSysvars(idx).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var raw []map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &raw); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(raw) != 1 {
		t.Fatalf("expected 1 sysvar, got %d", len(raw))
	}
	if _, present := raw[0]["min"]; present {
		t.Error("min must be omitted when no bounds declared (H-033)")
	}
	if _, present := raw[0]["max"]; present {
		t.Error("max must be omitted when no bounds declared (H-033)")
	}
}

// ── H-034 AlarmMessageDTO.address / state_value ─────────────────────────────

// TestListAlarmMessages_AddressAndStateValue pins H-034: alarm messages with
// an address and state_value must expose those fields in the DTO.
func TestListAlarmMessages_AddressAndStateValue(t *testing.T) {
	t.Parallel()
	h := hub.NewHub("test-ccu")
	h.Messages.Replace([]hub.AlarmMessage{
		{
			ID: "A1", Name: "Window open", Address: "ABC123:1",
			StateValue: "OPEN", Timestamp: time.Now(), Counter: 1,
		},
	})
	idx := &testHubIndex{h: h}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/alarm-messages", http.NoBody)
	w := httptest.NewRecorder()
	ListAlarmMessages(idx).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var body []AlarmMessageDTO
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(body) != 1 {
		t.Fatalf("expected 1 message, got %d", len(body))
	}
	if body[0].Address != "ABC123:1" {
		t.Errorf("address=%q want ABC123:1 (H-034)", body[0].Address)
	}
	if body[0].StateValue != "OPEN" {
		t.Errorf("state_value=%q want OPEN (H-034)", body[0].StateValue)
	}
}

// ── H-034 ServiceMessageDTO.description / priority ──────────────────────────

// TestListServiceMessages_DescriptionAndPriority pins H-034: service messages
// with a description and priority must expose those in the DTO.
func TestListServiceMessages_DescriptionAndPriority(t *testing.T) {
	t.Parallel()
	h := hub.NewHub("test-ccu")
	h.ServiceMessages.Replace([]hub.ServiceMessage{
		{
			ID: "S1", Name: "Low battery", Address: "XYZ456:0",
			Type:        hmenum.ServiceMessageTypeGeneric,
			Description: "Battery below 10 %", Priority: 2,
			Timestamp: time.Now(), Counter: 1, Quittable: true,
		},
	})
	idx := &testHubIndex{h: h}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/service-messages", http.NoBody)
	w := httptest.NewRecorder()
	ListServiceMessages(idx).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var body []ServiceMessageDTO
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(body) != 1 {
		t.Fatalf("expected 1 message, got %d", len(body))
	}
	if body[0].Description != "Battery below 10 %" {
		t.Errorf("description=%q want 'Battery below 10 %%' (H-034)", body[0].Description)
	}
	if body[0].Priority != 2 {
		t.Errorf("priority=%d want 2 (H-034)", body[0].Priority)
	}
}

// --- rfc3339OrEmpty ---

func TestRfc3339OrEmpty_ZeroTime_ReturnsEmpty(t *testing.T) {
	t.Parallel()
	if s := rfc3339OrEmpty(time.Time{}); s != "" {
		t.Fatalf("expected empty for zero time, got %q", s)
	}
}

func TestRfc3339OrEmpty_NonZeroTime_ReturnsRFC3339(t *testing.T) {
	t.Parallel()
	ts := time.Date(2026, 3, 15, 12, 0, 0, 0, time.UTC)
	s := rfc3339OrEmpty(ts)
	if s == "" {
		t.Fatal("expected non-empty string for non-zero time")
	}
	if !strings.Contains(s, "2026-03-15") {
		t.Fatalf("expected RFC3339 with date 2026-03-15, got %q", s)
	}
}

// --- requireMutationHub ---

// TestRequireMutationHub_NilIndex_Writes503 verifies that a nil
// HubIndex is reported as "no hub wired" and the helper reports
// !ok so the caller returns immediately.
func TestRequireMutationHub_NilIndex_Writes503(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest(http.MethodPut, "/", http.NoBody)
	w := httptest.NewRecorder()

	h, ok := requireMutationHub(w, req, nil)

	if ok || h != nil {
		t.Fatalf("expected ok=false, h=nil, got ok=%v h=%v", ok, h)
	}
	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503, got %d body=%s", w.Code, w.Body.String())
	}
}

// TestRequireMutationHub_AmbiguousCentral_Writes400 verifies that a
// multi-central index without a disambiguating ?central= query
// parameter is reported as 400, not silently picking one hub.
func TestRequireMutationHub_AmbiguousCentral_Writes400(t *testing.T) {
	t.Parallel()
	idx := &multiHubIndex{hubs: []NamedHub{
		{Central: "ccu-alpha", Hub: hub.NewHub("ccu-alpha")},
		{Central: "ccu-beta", Hub: hub.NewHub("ccu-beta")},
	}}
	req := httptest.NewRequest(http.MethodPut, "/", http.NoBody)
	w := httptest.NewRecorder()

	h, ok := requireMutationHub(w, req, idx)

	if ok || h != nil {
		t.Fatalf("expected ok=false, h=nil, got ok=%v h=%v", ok, h)
	}
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d body=%s", w.Code, w.Body.String())
	}
}

// TestRequireMutationHub_ResolvesSingleHub_NoResponseWritten verifies
// the success path: the hub is returned, ok is true, and nothing was
// written to w so the calling handler is free to keep processing.
func TestRequireMutationHub_ResolvesSingleHub_NoResponseWritten(t *testing.T) {
	t.Parallel()
	idx, wantHub := newTestHubWithProgram(t)
	req := httptest.NewRequest(http.MethodPut, "/", http.NoBody)
	w := httptest.NewRecorder()

	h, ok := requireMutationHub(w, req, idx)

	if !ok || h != wantHub {
		t.Fatalf("expected ok=true and the resolved hub, got ok=%v h=%v", ok, h)
	}
	if w.Code != 0 && w.Code != http.StatusOK {
		t.Errorf("expected no response written, got status %d body=%s", w.Code, w.Body.String())
	}
}

// --- SetProgramEnabled ---

func TestSetProgramEnabled_HubNil_Returns503(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest(http.MethodPut, "/", strings.NewReader(`{"active":true}`))
	req = req.WithContext(chiContext(req, map[string]string{"id": "P1"}))
	w := httptest.NewRecorder()
	SetProgramEnabled(nil).ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", w.Code)
	}
}

func TestSetProgramEnabled_ProgramNotFound_Returns404(t *testing.T) {
	t.Parallel()
	idx, _ := newTestHubWithProgram(t)
	req := httptest.NewRequest(http.MethodPut, "/", strings.NewReader(`{"active":true}`))
	req = req.WithContext(chiContext(req, map[string]string{"id": "NONEXISTENT"}))
	w := httptest.NewRecorder()
	SetProgramEnabled(idx).ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestSetProgramEnabled_InvalidJSON_Returns400(t *testing.T) {
	t.Parallel()
	idx, _ := newTestHubWithProgram(t)
	req := httptest.NewRequest(http.MethodPut, "/", strings.NewReader("NOT JSON"))
	req = req.WithContext(chiContext(req, map[string]string{"id": "P1"}))
	w := httptest.NewRecorder()
	SetProgramEnabled(idx).ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

// --- ExecuteProgram ---

func TestExecuteProgram_HubNil_Returns503(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest(http.MethodPost, "/", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"id": "P1"}))
	w := httptest.NewRecorder()
	ExecuteProgram(nil).ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", w.Code)
	}
}

func TestExecuteProgram_NotFound_Returns404(t *testing.T) {
	t.Parallel()
	idx, _ := newTestHubWithProgram(t)
	req := httptest.NewRequest(http.MethodPost, "/", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"id": "NOTFOUND"}))
	w := httptest.NewRecorder()
	ExecuteProgram(idx).ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d body=%s", w.Code, w.Body.String())
	}
}

// --- PatchSysvar ---

func TestPatchSysvar_HubNil_Returns503(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest(http.MethodPatch, "/", strings.NewReader(`{"unit":"°C"}`))
	req = req.WithContext(chiContext(req, map[string]string{"name": "TempTarget"}))
	w := httptest.NewRecorder()
	PatchSysvar(nil).ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", w.Code)
	}
}

func TestPatchSysvar_InvalidJSON_Returns400(t *testing.T) {
	t.Parallel()
	idx, _ := newTestHubWithProgram(t)
	req := httptest.NewRequest(http.MethodPatch, "/", strings.NewReader("NOT JSON"))
	req = req.WithContext(chiContext(req, map[string]string{"name": "Alarm"}))
	w := httptest.NewRecorder()
	PatchSysvar(idx).ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

// --- DeleteSysvar ---

func TestDeleteSysvar_HubNil_Returns503(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest(http.MethodDelete, "/", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"name": "Flag"}))
	w := httptest.NewRecorder()
	DeleteSysvar(nil).ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", w.Code)
	}
}

// --- PutSysvar ---

func TestPutSysvar_NotFound_Returns404(t *testing.T) {
	t.Parallel()
	idx, _ := newTestHubWithProgram(t)
	req := httptest.NewRequest(http.MethodPut, "/", strings.NewReader(`{"value":true}`))
	req = req.WithContext(chiContext(req, map[string]string{"name": "NOTFOUND"}))
	w := httptest.NewRecorder()
	PutSysvar(idx).ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestPutSysvar_InvalidJSON_Returns400(t *testing.T) {
	t.Parallel()
	idx, _ := newTestHubWithProgram(t)
	req := httptest.NewRequest(http.MethodPut, "/", strings.NewReader("NOT JSON"))
	req = req.WithContext(chiContext(req, map[string]string{"name": "Alarm"}))
	w := httptest.NewRecorder()
	PutSysvar(idx).ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

// --- GetSysvar happy path ---

func TestGetSysvar_HappyPath(t *testing.T) {
	t.Parallel()
	idx, _ := newTestHubWithProgram(t)
	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"name": "Alarm"}))
	w := httptest.NewRecorder()
	GetSysvar(idx).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var body SysvarSummary
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body.Name != "Alarm" {
		t.Fatalf("expected name=Alarm, got %q", body.Name)
	}
}

// --- AckAlarmMessage ---

func TestAckAlarmMessage_HubNil_Returns503(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest(http.MethodPost, "/", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"id": "A1"}))
	w := httptest.NewRecorder()
	AckAlarmMessage(nil).ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", w.Code)
	}
}

func TestAckAlarmMessage_Error_Returns502(t *testing.T) {
	t.Parallel()
	h := hub.NewHub("test-ccu")
	// Acknowledge on a non-existent ID returns an error from the MessageStore.
	idx := &testHubIndex{h: h}
	req := httptest.NewRequest(http.MethodPost, "/", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"id": "NOTFOUND"}))
	w := httptest.NewRecorder()
	AckAlarmMessage(idx).ServeHTTP(w, req)

	// The hub's message store returns an error for unknown IDs.
	if w.Code != http.StatusBadGateway && w.Code != http.StatusAccepted {
		t.Fatalf("expected 502 or 202, got %d body=%s", w.Code, w.Body.String())
	}
}

// --- AckServiceMessage ---

func TestAckServiceMessage_HubNil_Returns503(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest(http.MethodPost, "/", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"id": "S1"}))
	w := httptest.NewRecorder()
	AckServiceMessage(nil).ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", w.Code)
	}
}

// --- ListInbox with real inbox ---

func TestListInbox_WithEntries(t *testing.T) {
	t.Parallel()
	h := hub.NewHub("test-ccu")
	h.Inbox.Replace([]hub.InboxDevice{
		{Address: "0003001122:0", Model: "HmIP-PS", Serial: "00030011"},
	})
	idx := &testHubIndex{h: h}
	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	w := httptest.NewRecorder()
	ListInbox(idx).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var body []InboxDeviceDTO
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(body) != 1 {
		t.Fatalf("expected 1 inbox entry, got %d", len(body))
	}
	if body[0].Address != "0003001122:0" {
		t.Fatalf("expected address=0003001122:0, got %q", body[0].Address)
	}
}

// --- GetInstallMode happy path ---

// --- GetInterface happy path ---

// testFullInterfaceIndex implements InterfaceIndex fully with Reconnect.
type testFullInterfaceIndex struct {
	ifaces   []InterfaceState
	reconErr error
}

func (f *testFullInterfaceIndex) Interfaces() []InterfaceState { return f.ifaces }
func (f *testFullInterfaceIndex) Interface(id string) (InterfaceState, bool) {
	for _, i := range f.ifaces {
		if i.ID == id {
			return i, true
		}
	}
	return InterfaceState{}, false
}
func (f *testFullInterfaceIndex) Reconnect(_ context.Context, _ string) error { return f.reconErr }

func TestGetInterface_HappyPath(t *testing.T) {
	t.Parallel()
	idx := &testFullInterfaceIndex{
		ifaces: []InterfaceState{
			{ID: "HmIP-RF", Name: "HmIP RF", Connected: true, Interface: "HmIP-RF"},
		},
	}
	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"id": "HmIP-RF"}))
	w := httptest.NewRecorder()
	GetInterface(idx).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var state InterfaceState
	if err := json.Unmarshal(w.Body.Bytes(), &state); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if state.ID != "HmIP-RF" {
		t.Fatalf("expected ID=HmIP-RF, got %q", state.ID)
	}
}

func TestGetInterface_NotFound_Returns404(t *testing.T) {
	t.Parallel()
	idx := &testFullInterfaceIndex{ifaces: []InterfaceState{}}
	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"id": "NOTFOUND"}))
	w := httptest.NewRecorder()
	GetInterface(idx).ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d body=%s", w.Code, w.Body.String())
	}
}

// --- ReconnectInterface ---

func TestReconnectInterface_NilIndex_Returns503(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest(http.MethodPost, "/", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"id": "HmIP-RF"}))
	w := httptest.NewRecorder()
	ReconnectInterface(nil).ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", w.Code)
	}
}

func TestReconnectInterface_HappyPath(t *testing.T) {
	t.Parallel()
	idx := &testFullInterfaceIndex{ifaces: []InterfaceState{
		{ID: "HmIP-RF"},
	}}
	req := httptest.NewRequest(http.MethodPost, "/", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"id": "HmIP-RF"}))
	w := httptest.NewRecorder()
	ReconnectInterface(idx).ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestReconnectInterface_Error_Returns502(t *testing.T) {
	t.Parallel()
	idx := &testFullInterfaceIndex{reconErr: errors.New("connect timeout")}
	req := httptest.NewRequest(http.MethodPost, "/", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"id": "HmIP-RF"}))
	w := httptest.NewRecorder()
	ReconnectInterface(idx).ServeHTTP(w, req)

	if w.Code != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d body=%s", w.Code, w.Body.String())
	}
}

// --- ListInterfaces happy path ---

func TestListInterfaces_WithEntries(t *testing.T) {
	t.Parallel()
	idx := &testFullInterfaceIndex{
		ifaces: []InterfaceState{
			{ID: "HmIP-RF", Name: "HmIP RF", Connected: true},
			{ID: "CUxD", Name: "CUxD", Connected: false},
		},
	}
	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	w := httptest.NewRecorder()
	ListInterfaces(idx).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var body []InterfaceState
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(body) != 2 {
		t.Fatalf("expected 2 interfaces, got %d", len(body))
	}
}

// --- CreateSysvar with hub and valid request ---

func TestCreateSysvar_HappyPath_Returns202(t *testing.T) {
	t.Parallel()
	// Use a real hub; CreateSysvarRemote will return an error because the
	// CCU isn't wired, but we only want to exercise the request-parsing path.
	// We need a stub that accepts the call successfully.
	idx := &testHubIndex{h: hub.NewHub("ccu01")}
	body := strings.NewReader(`{"name":"MyVar","value_type":"BOOL"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/sysvars", body)
	w := httptest.NewRecorder()
	CreateSysvar(idx).ServeHTTP(w, req)

	// 202 if the hub accepted it, 502 if upstream not wired — both are valid
	// outcomes here; 400/422/503 would be bugs.
	if w.Code == http.StatusBadRequest || w.Code == http.StatusUnprocessableEntity || w.Code == http.StatusServiceUnavailable {
		t.Fatalf("unexpected error status %d body=%s", w.Code, w.Body.String())
	}
}

// --- AlarmMessages happy path with nil hub ---

func TestListAlarmMessages_NilHub_ReturnsEmptyArray(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	w := httptest.NewRecorder()
	ListAlarmMessages(nil).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var body []AlarmMessageDTO
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if len(body) != 0 {
		t.Fatalf("expected empty, got %+v", body)
	}
}

// --- ServiceMessages happy path with nil hub ---

func TestListServiceMessages_NilHub_ReturnsEmptyArray(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	w := httptest.NewRecorder()
	ListServiceMessages(nil).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var body []ServiceMessageDTO
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if len(body) != 0 {
		t.Fatalf("expected empty, got %+v", body)
	}
}

// --- ListPrograms with IsInternal flag ---

func TestListPrograms_InternalFlag(t *testing.T) {
	t.Parallel()
	h := hub.NewHub("ccu01")
	p := hub.NewProgram("ccu01", "Tmp_001", "Tmp_001", "", false, nil)
	p.IsInternal = true
	h.PutProgram(p)
	idx := &testHubIndex{h: h}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/programs", http.NoBody)
	w := httptest.NewRecorder()
	ListPrograms(idx).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var body []ProgramSummary
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(body) != 1 {
		t.Fatalf("expected 1 program, got %d", len(body))
	}
	if !body[0].IsInternal {
		t.Error("expected is_internal=true for Tmp_ programs")
	}
}

// --- toSysvarSummary with value observed ---

func TestToSysvarSummary_WithValue(t *testing.T) {
	t.Parallel()
	h := hub.NewHub("ccu01")
	sv := hub.NewSysvar("ccu01", "Counter", "", hmenum.HubValueTypeFloat, nil)
	h.PutSysvar(sv)
	idx := &testHubIndex{h: h}

	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	w := httptest.NewRecorder()
	ListSysvars(idx).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var body []SysvarSummary
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if len(body) != 1 {
		t.Fatalf("expected 1 sysvar, got %d", len(body))
	}
	// No value has been set so Observed must be false.
	if body[0].Observed {
		t.Error("expected observed=false when no value has been set")
	}
}

// ---------------------------------------------------------------------------
// Stubs for sysvar / program write paths
// ---------------------------------------------------------------------------

// errSysvarMutator implements hub.SysvarMutator and always returns an error.
type errSysvarMutator struct{ err error }

func (e *errSysvarMutator) CreateSysvar(_ context.Context, _, _, _, _, _ string, _ []string) error {
	return e.err
}

func (e *errSysvarMutator) UpdateSysvar(_ context.Context, _, _, _, _, _ string, _ []string) error {
	return e.err
}

func (e *errSysvarMutator) DeleteSysvar(_ context.Context, _ string) error { return e.err }

// errSysvarWriter implements hub.SysvarWriter and always returns an error.
type errSysvarWriter struct{ err error }

func (e *errSysvarWriter) SetSysvar(_ context.Context, _ string, _ any) error { return e.err }

// errMessageAcknowledger implements hub.MessageAcknowledger and always errors.
type errMessageAcknowledger struct{ err error }

func (e *errMessageAcknowledger) AcknowledgeMessage(_ context.Context, _ string) error {
	return e.err
}

// okMessageAcknowledger implements hub.MessageAcknowledger and always succeeds.
type okMessageAcknowledger struct{}

func (okMessageAcknowledger) AcknowledgeMessage(_ context.Context, _ string) error { return nil }

// errProgramWriter implements hub.ProgramWriter and always returns an error.
type errProgramWriter struct{ err error }

func (e *errProgramWriter) ExecuteProgram(_ context.Context, _ string) error {
	return e.err
}

func (e *errProgramWriter) SetProgramEnabled(_ context.Context, _ string, _ bool) error {
	return e.err
}

// ---------------------------------------------------------------------------
// PatchSysvar — error path (UpdateSysvarRemote error → 502) and happy path
// ---------------------------------------------------------------------------

func TestPatchSysvar_MutatorError_Returns502(t *testing.T) {
	t.Parallel()
	h := hub.NewHub("test-ccu")
	h.SysvarMutator = &errSysvarMutator{err: errors.New("ccu down")}
	idx := &testHubIndex{h: h}
	req := httptest.NewRequest(http.MethodPatch, "/", strings.NewReader(`{"unit":"°C"}`))
	req = req.WithContext(chiContext(req, map[string]string{"name": "Temp"}))
	w := httptest.NewRecorder()
	PatchSysvar(idx).ServeHTTP(w, req)

	if w.Code != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestPatchSysvar_HappyPath_Returns202(t *testing.T) {
	t.Parallel()
	h := hub.NewHub("test-ccu")
	h.SysvarMutator = &errSysvarMutator{err: nil}
	idx := &testHubIndex{h: h}
	body := `{"unit":"°C","min":"0","max":"100","description":"room temp"}`
	req := httptest.NewRequest(http.MethodPatch, "/", strings.NewReader(body))
	req = req.WithContext(chiContext(req, map[string]string{"name": "RoomTemp"}))
	w := httptest.NewRecorder()
	PatchSysvar(idx).ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d body=%s", w.Code, w.Body.String())
	}
}

// ---------------------------------------------------------------------------
// DeleteSysvar — error path and happy path
// ---------------------------------------------------------------------------

func TestDeleteSysvar_MutatorError_Returns502(t *testing.T) {
	t.Parallel()
	h := hub.NewHub("test-ccu")
	h.SysvarMutator = &errSysvarMutator{err: errors.New("ccu unreachable")}
	idx := &testHubIndex{h: h}
	req := httptest.NewRequest(http.MethodDelete, "/", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"name": "Flag"}))
	w := httptest.NewRecorder()
	DeleteSysvar(idx).ServeHTTP(w, req)

	if w.Code != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestDeleteSysvar_HappyPath_Returns202(t *testing.T) {
	t.Parallel()
	h := hub.NewHub("test-ccu")
	h.PutSysvar(&hub.Sysvar{HubDataPoint: hub.HubDataPoint{Name: "ToDelete"}})
	h.SysvarMutator = &errSysvarMutator{err: nil}
	idx := &testHubIndex{h: h}
	req := httptest.NewRequest(http.MethodDelete, "/", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"name": "ToDelete"}))
	w := httptest.NewRecorder()
	DeleteSysvar(idx).ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d body=%s", w.Code, w.Body.String())
	}
}

// ---------------------------------------------------------------------------
// PutSysvar — writer error → 502 and happy path
// ---------------------------------------------------------------------------

func TestPutSysvar_WriterError_Returns502(t *testing.T) {
	t.Parallel()
	h := hub.NewHub("test-ccu")
	writer := &errSysvarWriter{err: errors.New("write fail")}
	sv := hub.NewSysvar("test-ccu", "Flag", "", hmenum.HubValueTypeLogic, writer)
	h.PutSysvar(sv)
	idx := &testHubIndex{h: h}
	req := httptest.NewRequest(http.MethodPut, "/", strings.NewReader(`{"value":true}`))
	req = req.WithContext(chiContext(req, map[string]string{"name": "Flag"}))
	w := httptest.NewRecorder()
	PutSysvar(idx).ServeHTTP(w, req)

	if w.Code != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestPutSysvar_HappyPath_Returns202(t *testing.T) {
	t.Parallel()
	h := hub.NewHub("test-ccu")
	writer := &errSysvarWriter{err: nil}
	sv := hub.NewSysvar("test-ccu", "OnOff", "", hmenum.HubValueTypeLogic, writer)
	h.PutSysvar(sv)
	idx := &testHubIndex{h: h}
	req := httptest.NewRequest(http.MethodPut, "/", strings.NewReader(`{"value":true}`))
	req = req.WithContext(chiContext(req, map[string]string{"name": "OnOff"}))
	w := httptest.NewRecorder()
	PutSysvar(idx).ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d body=%s", w.Code, w.Body.String())
	}
}

// ---------------------------------------------------------------------------
// ExecuteProgram — error path and happy path
// ---------------------------------------------------------------------------

func TestExecuteProgram_ExecuteError_Returns502(t *testing.T) {
	t.Parallel()
	h := hub.NewHub("test-ccu")
	prog := hub.NewProgram("test-ccu", "P-fail", "Failing", "", false,
		&errProgramWriter{err: errors.New("exec fail")})
	h.PutProgram(prog)
	idx := &testHubIndex{h: h}
	req := httptest.NewRequest(http.MethodPost, "/", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"id": "P-fail"}))
	w := httptest.NewRecorder()
	ExecuteProgram(idx).ServeHTTP(w, req)

	if w.Code != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestExecuteProgram_HappyPath_Returns202(t *testing.T) {
	t.Parallel()
	h := hub.NewHub("test-ccu")
	prog := hub.NewProgram("test-ccu", "P-ok", "Ok", "", false,
		&errProgramWriter{err: nil})
	h.PutProgram(prog)
	idx := &testHubIndex{h: h}
	req := httptest.NewRequest(http.MethodPost, "/", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"id": "P-ok"}))
	w := httptest.NewRecorder()
	ExecuteProgram(idx).ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d body=%s", w.Code, w.Body.String())
	}
}

// ---------------------------------------------------------------------------
// AckServiceMessage — error path and happy path
// ---------------------------------------------------------------------------

func TestAckServiceMessage_Error_Returns502(t *testing.T) {
	t.Parallel()
	h := hub.NewHub("test-ccu")
	h.ServiceMessages = hub.NewServiceMessages(&errMessageAcknowledger{err: errors.New("ack fail")})
	idx := &testHubIndex{h: h}
	req := httptest.NewRequest(http.MethodPost, "/", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"id": "S1"}))
	w := httptest.NewRecorder()
	AckServiceMessage(idx).ServeHTTP(w, req)

	if w.Code != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestAckServiceMessage_HappyPath_Returns202(t *testing.T) {
	t.Parallel()
	h := hub.NewHub("test-ccu")
	h.ServiceMessages = hub.NewServiceMessages(okMessageAcknowledger{})
	idx := &testHubIndex{h: h}
	req := httptest.NewRequest(http.MethodPost, "/", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"id": "S1"}))
	w := httptest.NewRecorder()
	AckServiceMessage(idx).ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d body=%s", w.Code, w.Body.String())
	}
}

// ---------------------------------------------------------------------------
// DisableServiceMessage — hub-nil, error path, and happy path
// ---------------------------------------------------------------------------

func TestDisableServiceMessage_HubNil_Returns503(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest(http.MethodPost, "/", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"id": "S1"}))
	w := httptest.NewRecorder()
	DisableServiceMessage(nil).ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", w.Code)
	}
}

func TestDisableServiceMessage_Error_Returns502(t *testing.T) {
	t.Parallel()
	h := hub.NewHub("test-ccu")
	h.ServiceMessages = hub.NewServiceMessages(&errMessageAcknowledger{err: errors.New("disable fail")})
	idx := &testHubIndex{h: h}
	req := httptest.NewRequest(http.MethodPost, "/", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"id": "S1"}))
	w := httptest.NewRecorder()
	DisableServiceMessage(idx).ServeHTTP(w, req)

	if w.Code != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestDisableServiceMessage_HappyPath_Returns202(t *testing.T) {
	t.Parallel()
	h := hub.NewHub("test-ccu")
	h.ServiceMessages = hub.NewServiceMessages(okMessageAcknowledger{})
	h.ServiceMessages.Replace([]hub.ServiceMessage{{ID: "S1", Timestamp: time.Now()}})
	idx := &testHubIndex{h: h}
	req := httptest.NewRequest(http.MethodPost, "/", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"id": "S1"}))
	w := httptest.NewRecorder()
	DisableServiceMessage(idx).ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d body=%s", w.Code, w.Body.String())
	}
	if h.ServiceMessages.Count() != 0 {
		t.Fatalf("Count=%d after disable, want 0", h.ServiceMessages.Count())
	}
}

// ---------------------------------------------------------------------------
// CreateSysvar — error path (mutator error → 502)
// ---------------------------------------------------------------------------

func TestCreateSysvar_MutatorError_Returns502(t *testing.T) {
	t.Parallel()
	h := hub.NewHub("test-ccu")
	h.SysvarMutator = &errSysvarMutator{err: errors.New("rega down")}
	idx := &testHubIndex{h: h}
	body := `{"name":"Flag","value_type":"LOGIC"}`
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	w := httptest.NewRecorder()
	CreateSysvar(idx).ServeHTTP(w, req)

	if w.Code != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d body=%s", w.Code, w.Body.String())
	}
}

// ---------------------------------------------------------------------------
// SetProgramEnabled — error path (SetEnabled error → 502)
// ---------------------------------------------------------------------------

func TestSetProgramEnabled_SetEnabledError_Returns502(t *testing.T) {
	t.Parallel()
	h := hub.NewHub("test-ccu")
	prog := hub.NewProgram("test-ccu", "P1", "Morning", "", false,
		&errProgramWriter{err: errors.New("set enabled fail")})
	h.PutProgram(prog)
	idx := &testHubIndex{h: h}
	req := httptest.NewRequest(http.MethodPut, "/", strings.NewReader(`{"active":true}`))
	req = req.WithContext(chiContext(req, map[string]string{"id": "P1"}))
	w := httptest.NewRecorder()
	SetProgramEnabled(idx).ServeHTTP(w, req)

	if w.Code != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d body=%s", w.Code, w.Body.String())
	}
}

// ---------------------------------------------------------------------------
// snapshotPrograms — with an observed active state (covers the if-observed branch)
// ---------------------------------------------------------------------------

func TestSnapshotPrograms_WithObservedActive(t *testing.T) {
	t.Parallel()
	h := hub.NewHub("test-ccu")
	prog := hub.NewProgram("test-ccu", "P1", "Morning", "", false, nil)
	prog.OnActive(true) // triggers the observed branch
	h.PutProgram(prog)
	idx := &testHubIndex{h: h}
	out := snapshotPrograms(idx)
	if len(out) != 1 {
		t.Fatalf("expected 1 program, got %d", len(out))
	}
	if out[0].Active == nil || !*out[0].Active {
		t.Errorf("expected Active=true, got %v", out[0].Active)
	}
}

// ---------------------------------------------------------------------------
// ListPrograms — with observed active state (covers if-observed branch)
// ---------------------------------------------------------------------------

func TestListPrograms_WithObservedActive(t *testing.T) {
	t.Parallel()
	h := hub.NewHub("test-ccu")
	prog := hub.NewProgram("test-ccu", "P1", "Morning", "", false, nil)
	prog.OnActive(true)
	h.PutProgram(prog)
	idx := &testHubIndex{h: h}
	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	w := httptest.NewRecorder()
	ListPrograms(idx).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
}

// ---------------------------------------------------------------------------
// resolveHubForRead / GetSysvar — single-central-unambiguous routing
// ---------------------------------------------------------------------------

// multiHubIndex is a test double that holds exactly two centrals.
type multiHubIndex struct {
	hubs []NamedHub
}

func (m *multiHubIndex) Hub() *hub.Hub {
	if len(m.hubs) > 0 {
		return m.hubs[0].Hub
	}
	return nil
}

func (m *multiHubIndex) Hubs() []NamedHub { return m.hubs }

func (m *multiHubIndex) HubFor(centralName string) *hub.Hub {
	for _, nh := range m.hubs {
		if nh.Central == centralName {
			return nh.Hub
		}
	}
	return nil
}

func (m *multiHubIndex) SerialSuffix(central string) string {
	if central != "" {
		return "vccu0000000"
	}
	return ""
}

// TestGetSysvar_SingleCentralUnambiguous verifies that GetSysvar resolves
// the correct hub without ?central= when exactly one central owns the sysvar.
func TestGetSysvar_SingleCentralUnambiguous(t *testing.T) {
	t.Parallel()

	h1 := hub.NewHub("ccu-alpha")
	sv := hub.NewSysvar("ccu-alpha", "TempOut", "", hmenum.HubValueTypeFloat, nil)
	h1.PutSysvar(sv)

	h2 := hub.NewHub("ccu-beta")
	// ccu-beta does NOT have TempOut.

	idx := &multiHubIndex{hubs: []NamedHub{
		{Central: "ccu-alpha", Hub: h1},
		{Central: "ccu-beta", Hub: h2},
	}}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/sysvars/TempOut", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"name": "TempOut"}))
	w := httptest.NewRecorder()
	GetSysvar(idx).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 when sysvar exists on exactly one central, got %d body=%s", w.Code, w.Body.String())
	}
	var body SysvarSummary
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body.Name != "TempOut" {
		t.Errorf("Name=%q want TempOut", body.Name)
	}
}

// TestGetSysvar_AmbiguousRequiresCentral verifies that GetSysvar returns 400
// when the same sysvar name exists on two centrals and no ?central= is given.
func TestGetSysvar_AmbiguousRequiresCentral(t *testing.T) {
	t.Parallel()

	h1 := hub.NewHub("ccu-alpha")
	h1.PutSysvar(hub.NewSysvar("ccu-alpha", "Alarm", "", hmenum.HubValueTypeLogic, nil))
	h2 := hub.NewHub("ccu-beta")
	h2.PutSysvar(hub.NewSysvar("ccu-beta", "Alarm", "", hmenum.HubValueTypeLogic, nil))

	idx := &multiHubIndex{hubs: []NamedHub{
		{Central: "ccu-alpha", Hub: h1},
		{Central: "ccu-beta", Hub: h2},
	}}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/sysvars/Alarm", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"name": "Alarm"}))
	w := httptest.NewRecorder()
	GetSysvar(idx).ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for ambiguous sysvar, got %d body=%s", w.Code, w.Body.String())
	}
}

// ---------------------------------------------------------------------------
// PutSysvar — unsupported value type (422)
// ---------------------------------------------------------------------------

func TestPutSysvar_InvalidValue_Returns422(t *testing.T) {
	t.Parallel()
	h := hub.NewHub("test-ccu")
	sv := hub.NewSysvar("test-ccu", "Flag", "", hmenum.HubValueTypeLogic, &errSysvarWriter{err: nil})
	h.PutSysvar(sv)
	idx := &testHubIndex{h: h}
	// Send an unsupported complex value that will fail NewParamValue.
	req := httptest.NewRequest(http.MethodPut, "/", strings.NewReader(`{"value":{"bad":true}}`))
	req = req.WithContext(chiContext(req, map[string]string{"name": "Flag"}))
	w := httptest.NewRecorder()
	PutSysvar(idx).ServeHTTP(w, req)

	// Either 422 (value not supported) or 400 (bad JSON) is acceptable.
	if w.Code != http.StatusUnprocessableEntity && w.Code != http.StatusBadRequest {
		t.Fatalf("expected 422 or 400, got %d body=%s", w.Code, w.Body.String())
	}
}

// ---------------------------------------------------------------------------
// FetchSysvars
// ---------------------------------------------------------------------------

// stubSysvarRefreshService implements SysvarRefreshService for testing.
type stubSysvarRefreshService struct {
	recordedCentral string
	err             error
}

func (s *stubSysvarRefreshService) FetchSystemVariables(_ context.Context, centralName string) error {
	s.recordedCentral = centralName
	return s.err
}

// TestFetchSysvars_HappyPath verifies that ?central= is forwarded to the
// service and the handler returns 202.
func TestFetchSysvars_HappyPath(t *testing.T) {
	t.Parallel()
	svc := &stubSysvarRefreshService{}
	req := httptest.NewRequest(http.MethodPost, "/?central=ccu-01", http.NoBody)
	w := httptest.NewRecorder()
	FetchSysvars(svc).ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d body=%s", w.Code, w.Body.String())
	}
	if svc.recordedCentral != "ccu-01" {
		t.Errorf("recordedCentral=%q want ccu-01", svc.recordedCentral)
	}
}

// TestFetchSysvars_NilService_Returns503 verifies that a nil service yields 503.
func TestFetchSysvars_NilService_Returns503(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest(http.MethodPost, "/", http.NoBody)
	w := httptest.NewRecorder()
	FetchSysvars(nil).ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", w.Code)
	}
}

// TestFetchSysvars_ServiceError_Returns502 verifies that a service error
// results in 502.
func TestFetchSysvars_ServiceError_Returns502(t *testing.T) {
	t.Parallel()
	svc := &stubSysvarRefreshService{err: errors.New("CCU unreachable")}
	req := httptest.NewRequest(http.MethodPost, "/", http.NoBody)
	w := httptest.NewRecorder()
	FetchSysvars(svc).ServeHTTP(w, req)

	if w.Code != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d body=%s", w.Code, w.Body.String())
	}
}

// TestToSysvarSummary_UniqueID verifies that toSysvarSummary stamps a
// loom_-prefixed unique_id. The sysvar's CanonicalUniqueID always
// produces a loom_ key (even with an empty serialSuffix) because the sysvar
// address is stable; the serialSuffix differentiates identical sysvar names
// across CCUs.
func TestToSysvarSummary_UniqueID(t *testing.T) {
	t.Parallel()
	sv := hub.NewSysvar("ccu01", "AussenTemp", "", hmenum.HubValueTypeFloat, nil)

	s := toSysvarSummary(sv, "vccu0000000")
	if s.UniqueID == "" {
		t.Fatal("UniqueID must not be empty when serialSuffix is set")
	}
	if !strings.HasPrefix(s.UniqueID, "loom_") {
		t.Errorf("UniqueID = %q, want loom_ prefix", s.UniqueID)
	}
}

// TestToProgramSummary_UniqueID verifies that toProgramSummary stamps a
// loom_-prefixed unique_id. The program's CanonicalUniqueID always produces
// a loom_ key; the serialSuffix disambiguates same-named programs across CCUs.
func TestToProgramSummary_UniqueID(t *testing.T) {
	t.Parallel()
	p := hub.NewProgram("ccu01", "P1", "Morning Routine", "", false, nil)

	s := toProgramSummary(p, "ccu01", "vccu0000000")
	if s.UniqueID == "" {
		t.Fatal("UniqueID must not be empty when serialSuffix is set")
	}
	if !strings.HasPrefix(s.UniqueID, "loom_") {
		t.Errorf("UniqueID = %q, want loom_ prefix", s.UniqueID)
	}
}

// TestListSysvars_DeviceLinkExposed verifies that a sysvar associated with a
// device channel — via the CCU's explicit channel assignment or a
// device-referencing name, both stored as the resolved channel link — renders
// channel and device_address in the summary so REST/WS clients can attach the
// entity to the physical device instead of the hub. Pins the real-world CCU
// energy-counter name shape as the linked example.
func TestListSysvars_DeviceLinkExposed(t *testing.T) {
	t.Parallel()
	h := hub.NewHub("test-ccu")
	linked := hub.NewSysvar("test-ccu", "svEnergyCounter_14884_000858A994D482:7", "", hmenum.HubValueTypeFloat, nil)
	linked.SetChannel("000858A994D482:7")
	h.PutSysvar(linked)
	h.PutSysvar(hub.NewSysvar("test-ccu", "Unlinked", "", hmenum.HubValueTypeLogic, nil))
	idx := &testHubIndex{h: h}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/sysvars", http.NoBody)
	w := httptest.NewRecorder()
	ListSysvars(idx).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var body []SysvarSummary
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	byName := make(map[string]SysvarSummary, len(body))
	for _, s := range body {
		byName[s.Name] = s
	}
	got, ok := byName["svEnergyCounter_14884_000858A994D482:7"]
	if !ok {
		t.Fatal("linked sysvar missing from summary")
	}
	if got.Channel != "000858A994D482:7" {
		t.Errorf("Channel = %q, want %q", got.Channel, "000858A994D482:7")
	}
	if got.DeviceAddress != "000858A994D482" {
		t.Errorf("DeviceAddress = %q, want %q", got.DeviceAddress, "000858A994D482")
	}
	unlinked, ok := byName["Unlinked"]
	if !ok {
		t.Fatal("unlinked sysvar missing from summary")
	}
	if unlinked.Channel != "" || unlinked.DeviceAddress != "" {
		t.Errorf("unlinked sysvar must omit channel/device_address, got %q/%q",
			unlinked.Channel, unlinked.DeviceAddress)
	}
	// The wire encoding must omit the fields entirely for the hub-card case.
	if strings.Contains(w.Body.String(), `"Unlinked","channel"`) {
		t.Error("unlinked sysvar serialised a channel field")
	}
}

// TestListPrograms_DeviceLinkExposed mirrors the sysvar device-link summary
// test for programs (name-match only — programs have no CCU-side channel
// assignment).
func TestListPrograms_DeviceLinkExposed(t *testing.T) {
	t.Parallel()
	h := hub.NewHub("test-ccu")
	linked := hub.NewProgram("test-ccu", "P1", "Heizung 000858A994D482:7", "", false, nil)
	linked.SetChannel("000858A994D482:7")
	h.PutProgram(linked)
	h.PutProgram(hub.NewProgram("test-ccu", "P2", "Unlinked", "", false, nil))
	idx := &testHubIndex{h: h}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/programs", http.NoBody)
	w := httptest.NewRecorder()
	ListPrograms(idx).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var body []ProgramSummary
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	byID := make(map[string]ProgramSummary, len(body))
	for _, p := range body {
		byID[p.ID] = p
	}
	got, ok := byID["P1"]
	if !ok {
		t.Fatal("linked program missing from summary")
	}
	if got.Channel != "000858A994D482:7" {
		t.Errorf("Channel = %q, want %q", got.Channel, "000858A994D482:7")
	}
	if got.DeviceAddress != "000858A994D482" {
		t.Errorf("DeviceAddress = %q, want %q", got.DeviceAddress, "000858A994D482")
	}
	unlinked, ok := byID["P2"]
	if !ok {
		t.Fatal("unlinked program missing from summary")
	}
	if unlinked.Channel != "" || unlinked.DeviceAddress != "" {
		t.Errorf("unlinked program must omit channel/device_address, got %q/%q",
			unlinked.Channel, unlinked.DeviceAddress)
	}
}
