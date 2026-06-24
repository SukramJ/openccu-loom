// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package handlers

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/audit"
	"github.com/SukramJ/openccu-loom/internal/model/device"
)

// stubAuditService is an inline stub for AuditService.
type stubAuditService struct {
	entries []audit.Entry
}

func (s *stubAuditService) List(limit int) []audit.Entry {
	if limit >= len(s.entries) {
		return s.entries
	}
	return s.entries[:limit]
}

func TestListAudit_HappyPath(t *testing.T) {
	t.Parallel()
	svc := &stubAuditService{
		entries: []audit.Entry{
			{Action: audit.ActionParamsetWrite, DeviceAddress: "DEV001"},
			{Action: audit.ActionLinkAdd, DeviceAddress: "DEV002"},
		},
	}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/audit", http.NoBody)
	w := httptest.NewRecorder()
	ListAudit(svc, nil).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var body []audit.Entry
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(body) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(body))
	}
}

func TestListAudit_ServiceNil_Returns503(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/audit", http.NoBody)
	w := httptest.NewRecorder()
	ListAudit(nil, nil).ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", w.Code)
	}
}

func TestListAudit_LimitQueryParam(t *testing.T) {
	t.Parallel()
	entries := make([]audit.Entry, 10)
	for i := range entries {
		entries[i] = audit.Entry{Action: audit.ActionDataPointWrite}
	}
	svc := &stubAuditService{entries: entries}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/audit?limit=3", http.NoBody)
	w := httptest.NewRecorder()
	ListAudit(svc, nil).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var body []audit.Entry
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if len(body) != 3 {
		t.Fatalf("expected 3 entries with limit=3, got %d", len(body))
	}
}

func TestListAudit_InvalidLimitFallsBackToDefault(t *testing.T) {
	t.Parallel()
	entries := make([]audit.Entry, 5)
	svc := &stubAuditService{entries: entries}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/audit?limit=notanumber", http.NoBody)
	w := httptest.NewRecorder()
	ListAudit(svc, nil).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var body []audit.Entry
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	// Default limit is 1000, all 5 entries returned.
	if len(body) != 5 {
		t.Fatalf("expected 5 entries, got %d", len(body))
	}
}

// --- Filter tests ---

func TestAuditFilterByDevice(t *testing.T) {
	t.Parallel()
	svc := &stubAuditService{
		entries: []audit.Entry{
			{Action: audit.ActionParamsetWrite, DeviceAddress: "0001ABCD"},
			{Action: audit.ActionParamsetWrite, DeviceAddress: "0002EFGH"},
			{Action: audit.ActionDataPointWrite, DeviceAddress: "0001XY"},
		},
	}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/audit?device=0001", http.NoBody)
	w := httptest.NewRecorder()
	ListAudit(svc, nil).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var body []audit.Entry
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(body) != 2 {
		t.Fatalf("expected 2 entries matching prefix 0001, got %d", len(body))
	}
	for _, e := range body {
		if e.DeviceAddress[:4] != "0001" {
			t.Fatalf("unexpected device address %q in filtered result", e.DeviceAddress)
		}
	}
}

func TestAuditFilterByOp(t *testing.T) {
	t.Parallel()
	svc := &stubAuditService{
		entries: []audit.Entry{
			{Action: audit.ActionParamsetWrite, DeviceAddress: "D1"},
			{Action: audit.ActionLinkAdd, DeviceAddress: "D2"},
			{Action: audit.ActionDataPointWrite, DeviceAddress: "D3"},
		},
	}
	// "paramset" matches ActionParamsetWrite.
	req := httptest.NewRequest(http.MethodGet, "/api/v1/audit?op=paramset", http.NoBody)
	w := httptest.NewRecorder()
	ListAudit(svc, nil).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var body []audit.Entry
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(body) != 1 {
		t.Fatalf("expected 1 entry matching op=paramset, got %d", len(body))
	}
	if body[0].Action != audit.ActionParamsetWrite {
		t.Fatalf("unexpected action %q", body[0].Action)
	}
}

func TestAuditFilterBySince(t *testing.T) {
	t.Parallel()
	base := time.Date(2026, 4, 28, 12, 0, 0, 0, time.UTC)
	svc := &stubAuditService{
		entries: []audit.Entry{
			{Action: audit.ActionDataPointWrite, Timestamp: base.Add(2 * time.Hour)},  // after
			{Action: audit.ActionDataPointWrite, Timestamp: base},                     // at (inclusive)
			{Action: audit.ActionDataPointWrite, Timestamp: base.Add(-1 * time.Hour)}, // before
		},
	}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/audit?since=2026-04-28T12:00:00Z", http.NoBody)
	w := httptest.NewRecorder()
	ListAudit(svc, nil).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var body []audit.Entry
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(body) != 2 {
		t.Fatalf("expected 2 entries at-or-after since, got %d", len(body))
	}
}

func TestAuditFilterByUntil(t *testing.T) {
	t.Parallel()
	base := time.Date(2026, 4, 28, 12, 0, 0, 0, time.UTC)
	svc := &stubAuditService{
		entries: []audit.Entry{
			{Action: audit.ActionDataPointWrite, Timestamp: base.Add(1 * time.Hour)},  // at or after → excluded
			{Action: audit.ActionDataPointWrite, Timestamp: base},                     // at until → excluded (strict)
			{Action: audit.ActionDataPointWrite, Timestamp: base.Add(-1 * time.Hour)}, // before → included
		},
	}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/audit?until=2026-04-28T12:00:00Z", http.NoBody)
	w := httptest.NewRecorder()
	ListAudit(svc, nil).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var body []audit.Entry
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(body) != 1 {
		t.Fatalf("expected 1 entry strictly before until, got %d", len(body))
	}
}

func TestAuditFilterByLimit(t *testing.T) {
	t.Parallel()
	entries := make([]audit.Entry, 20)
	for i := range entries {
		entries[i] = audit.Entry{Action: audit.ActionDataPointWrite}
	}
	svc := &stubAuditService{entries: entries}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/audit?limit=5", http.NoBody)
	w := httptest.NewRecorder()
	ListAudit(svc, nil).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var body []audit.Entry
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(body) != 5 {
		t.Fatalf("expected 5 entries with limit=5, got %d", len(body))
	}
}

func TestAuditFilterCombination(t *testing.T) {
	t.Parallel()
	base := time.Date(2026, 4, 28, 12, 0, 0, 0, time.UTC)
	svc := &stubAuditService{
		entries: []audit.Entry{
			// Matches device + op + since.
			{Action: audit.ActionParamsetWrite, DeviceAddress: "0001AB", Timestamp: base.Add(1 * time.Hour)},
			// Wrong device.
			{Action: audit.ActionParamsetWrite, DeviceAddress: "0002CD", Timestamp: base.Add(1 * time.Hour)},
			// Wrong op.
			{Action: audit.ActionLinkAdd, DeviceAddress: "0001AB", Timestamp: base.Add(1 * time.Hour)},
			// Too old (before since).
			{Action: audit.ActionParamsetWrite, DeviceAddress: "0001AB", Timestamp: base.Add(-1 * time.Hour)},
		},
	}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/audit?device=0001&op=paramset&since=2026-04-28T12:00:00Z", http.NoBody)
	w := httptest.NewRecorder()
	ListAudit(svc, nil).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var body []audit.Entry
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(body) != 1 {
		t.Fatalf("expected 1 combined-filter match, got %d", len(body))
	}
}

func TestAuditFilterInvalidSinceReturnsBadRequest(t *testing.T) {
	t.Parallel()
	svc := &stubAuditService{entries: []audit.Entry{{Action: audit.ActionDataPointWrite}}}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/audit?since=not-a-date", http.NoBody)
	w := httptest.NewRecorder()
	ListAudit(svc, nil).ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid since, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestAuditFilterInvalidUntilReturnsBadRequest(t *testing.T) {
	t.Parallel()
	svc := &stubAuditService{entries: []audit.Entry{{Action: audit.ActionDataPointWrite}}}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/audit?until=not-a-date", http.NoBody)
	w := httptest.NewRecorder()
	ListAudit(svc, nil).ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid until, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestAuditFilterDeviceCaseInsensitive(t *testing.T) {
	t.Parallel()
	svc := &stubAuditService{
		entries: []audit.Entry{
			{Action: audit.ActionDataPointWrite, DeviceAddress: "0001ABCD"},
		},
	}
	// Lowercase prefix should still match.
	req := httptest.NewRequest(http.MethodGet, "/api/v1/audit?device=0001abcd", http.NoBody)
	w := httptest.NewRecorder()
	ListAudit(svc, nil).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var body []audit.Entry
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(body) != 1 {
		t.Fatalf("expected 1 case-insensitive match, got %d", len(body))
	}
}

func TestAuditFilterZeroLimitUsesDefault(t *testing.T) {
	t.Parallel()
	entries := make([]audit.Entry, 3)
	for i := range entries {
		entries[i] = audit.Entry{Action: audit.ActionDataPointWrite}
	}
	svc := &stubAuditService{entries: entries}

	// limit=0 means "use default" (1000), not "no results".
	req := httptest.NewRequest(http.MethodGet, "/api/v1/audit?limit=0", http.NoBody)
	w := httptest.NewRecorder()
	ListAudit(svc, nil).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var body []audit.Entry
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(body) != 3 {
		t.Fatalf("expected 3 entries with limit=0 (default), got %d", len(body))
	}
}

// --- Durable-path (AuditQuerier) tests ---

// stubAuditQuerier implements both AuditService and AuditQuerier so the
// handler takes the durable code path.
type stubAuditQuerier struct {
	entries     []audit.Entry
	queryCalled bool
	lastQuery   audit.Query
	err         error
}

func (s *stubAuditQuerier) List(limit int) []audit.Entry {
	if limit >= len(s.entries) {
		return s.entries
	}
	return s.entries[:limit]
}

func (s *stubAuditQuerier) Query(_ context.Context, q audit.Query) ([]audit.Entry, error) {
	s.queryCalled = true
	s.lastQuery = q
	if s.err != nil {
		return nil, s.err
	}
	return s.entries, nil
}

// stubCentralIndex is a minimal DeviceIndex that only wires CentralOf.
// Unused methods satisfy the interface and return zero values.
type stubCentralIndex struct {
	centralOf map[string]string
}

func (s *stubCentralIndex) Devices() []*device.Device            { return nil }
func (s *stubCentralIndex) Device(string) (*device.Device, bool) { return nil, false }
func (s *stubCentralIndex) SerialSuffix(string) string           { return "" }
func (s *stubCentralIndex) CentralOf(addr string) string         { return s.centralOf[addr] }

func TestListAudit_DurablePath_QueryCalledNotList(t *testing.T) {
	t.Parallel()
	svc := &stubAuditQuerier{
		entries: []audit.Entry{
			{Action: audit.ActionParamsetWrite, DeviceAddress: "D1"},
			{Action: audit.ActionLinkAdd, DeviceAddress: "D2"},
		},
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/audit", http.NoBody)
	w := httptest.NewRecorder()
	ListAudit(svc, nil).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	if !svc.queryCalled {
		t.Fatal("Query was not called; handler should use durable path when svc implements AuditQuerier")
	}
	var body []AuditEntryDTO
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(body) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(body))
	}
}

func TestListAudit_DurablePath_OpPostFilterApplied(t *testing.T) {
	t.Parallel()
	svc := &stubAuditQuerier{
		entries: []audit.Entry{
			{Action: audit.ActionParamsetWrite, DeviceAddress: "D1"},
			{Action: audit.ActionLinkAdd, DeviceAddress: "D2"},
		},
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/audit?op=paramset", http.NoBody)
	w := httptest.NewRecorder()
	ListAudit(svc, nil).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var body []AuditEntryDTO
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(body) != 1 {
		t.Fatalf("op post-filter: expected 1 entry, got %d", len(body))
	}
	if body[0].Action != audit.ActionParamsetWrite {
		t.Fatalf("wrong action after op filter: %q", body[0].Action)
	}
}

func TestListAudit_DurablePath_CentralPostFilterApplied(t *testing.T) {
	t.Parallel()
	svc := &stubAuditQuerier{
		entries: []audit.Entry{
			{Action: audit.ActionDataPointWrite, DeviceAddress: "DEV001"},
			{Action: audit.ActionDataPointWrite, DeviceAddress: "OTHER001"},
		},
	}
	idx := &stubCentralIndex{centralOf: map[string]string{
		"DEV001":   "ccu1",
		"OTHER001": "ccu2",
	}}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/audit?central=ccu1", http.NoBody)
	w := httptest.NewRecorder()
	ListAudit(svc, idx).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var body []AuditEntryDTO
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(body) != 1 {
		t.Fatalf("central post-filter: expected 1 entry for ccu1, got %d", len(body))
	}
	if body[0].Central != "ccu1" {
		t.Fatalf("wrong central: %q", body[0].Central)
	}
}

func TestListAudit_DurablePath_OffsetPassedToQuery(t *testing.T) {
	t.Parallel()
	svc := &stubAuditQuerier{}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/audit?offset=42", http.NoBody)
	w := httptest.NewRecorder()
	ListAudit(svc, nil).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if svc.lastQuery.Offset != 42 {
		t.Fatalf("offset not forwarded to Query: got %d, want 42", svc.lastQuery.Offset)
	}
}

func TestListAudit_DurablePath_QueryError_Returns500(t *testing.T) {
	t.Parallel()
	svc := &stubAuditQuerier{err: context.DeadlineExceeded}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/audit", http.NoBody)
	w := httptest.NewRecorder()
	ListAudit(svc, nil).ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestListAudit_CSV_ContentTypeAndBody(t *testing.T) {
	t.Parallel()
	svc := &stubAuditService{
		entries: []audit.Entry{
			{
				Action:        audit.ActionParamsetWrite,
				DeviceAddress: "DEV001",
				User:          "alice",
				Timestamp:     time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
			},
		},
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/audit?format=csv", http.NoBody)
	w := httptest.NewRecorder()
	ListAudit(svc, nil).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	ct := w.Header().Get("Content-Type")
	if !strings.Contains(ct, "text/csv") {
		t.Fatalf("Content-Type: want text/csv, got %q", ct)
	}
	cd := w.Header().Get("Content-Disposition")
	if !strings.Contains(cd, "attachment") {
		t.Fatalf("Content-Disposition: want attachment, got %q", cd)
	}

	records, err := csv.NewReader(w.Body).ReadAll()
	if err != nil {
		t.Fatalf("csv parse: %v", err)
	}
	if len(records) < 2 {
		t.Fatalf("expected header + 1 data row, got %d rows", len(records))
	}
	header := records[0]
	if header[0] != "timestamp" || header[2] != "action" {
		t.Fatalf("unexpected CSV header: %v", header)
	}
	dataRow := records[1]
	found := false
	for _, col := range dataRow {
		if strings.Contains(col, "paramset_write") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("data row missing paramset_write: %v", dataRow)
	}
}

func TestListAudit_InMemoryStubStillWorksWithoutQuerier(t *testing.T) {
	t.Parallel()
	svc := &stubAuditService{
		entries: []audit.Entry{
			{Action: audit.ActionDataPointWrite, DeviceAddress: "D1"},
		},
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/audit", http.NoBody)
	w := httptest.NewRecorder()
	ListAudit(svc, nil).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var body []AuditEntryDTO
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(body) != 1 {
		t.Fatalf("in-memory path: expected 1 entry, got %d", len(body))
	}
}
