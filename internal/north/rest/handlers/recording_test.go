// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/audit"
)

type stubRecordingService struct {
	effRecord bool
	effSource string
	effErr    error

	setCalls   int
	clearCalls int
	lastRecord bool
}

func (s *stubRecordingService) Effective(_ context.Context, _, _, _, _ string) (record bool, source string, err error) {
	return s.effRecord, s.effSource, s.effErr
}

func (s *stubRecordingService) Set(_ context.Context, _, _, _, _ string, record bool, _ string) error {
	s.setCalls++
	s.lastRecord = record
	return nil
}

func (s *stubRecordingService) Clear(_ context.Context, _, _, _, _, _ string) error {
	s.clearCalls++
	return nil
}

func TestGetRecordingOverride_HappyPath(t *testing.T) {
	t.Parallel()
	svc := &stubRecordingService{effRecord: true, effSource: "override"}
	req := httptest.NewRequest(http.MethodGet,
		"/api/v1/history/recording?central=ccu1&interface_id=if&channel=DEV:1&parameter=TEMPERATURE", http.NoBody)
	w := httptest.NewRecorder()
	GetRecordingOverride(svc).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var body recordingResponse
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !body.Record || body.Source != "override" {
		t.Errorf("body = %+v, want {true override}", body)
	}
}

func TestGetRecordingOverride_MissingParam_Returns400(t *testing.T) {
	t.Parallel()
	svc := &stubRecordingService{}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/history/recording?central=ccu1", http.NoBody)
	w := httptest.NewRecorder()
	GetRecordingOverride(svc).ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestGetRecordingOverride_NilService_Returns503(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/history/recording", http.NoBody)
	w := httptest.NewRecorder()
	GetRecordingOverride(nil).ServeHTTP(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", w.Code)
	}
}

func TestPutRecordingOverride_Set_RecordsAudit(t *testing.T) {
	t.Parallel()
	svc := &stubRecordingService{effRecord: true, effSource: "override"}
	rec := &captureRecorder{}
	body := `{"central":"ccu1","interface_id":"if","channel":"DEV:1","parameter":"TEMPERATURE","record":true}`
	req := httptest.NewRequest(http.MethodPut, "/api/v1/history/recording", strings.NewReader(body))
	w := httptest.NewRecorder()
	PutRecordingOverride(svc, rec).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	if svc.setCalls != 1 || !svc.lastRecord {
		t.Errorf("expected one Set(record=true), got calls=%d record=%v", svc.setCalls, svc.lastRecord)
	}
	if len(rec.entries) != 1 || rec.entries[0].Action != audit.ActionRecordingToggle {
		t.Errorf("expected one recording_toggle audit entry, got %+v", rec.entries)
	}
}

func TestPutRecordingOverride_NullClears(t *testing.T) {
	t.Parallel()
	svc := &stubRecordingService{effRecord: true, effSource: "policy"}
	rec := &captureRecorder{}
	body := `{"central":"ccu1","interface_id":"if","channel":"DEV:1","parameter":"TEMPERATURE","record":null}`
	req := httptest.NewRequest(http.MethodPut, "/api/v1/history/recording", strings.NewReader(body))
	w := httptest.NewRecorder()
	PutRecordingOverride(svc, rec).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if svc.clearCalls != 1 || svc.setCalls != 0 {
		t.Errorf("null record must Clear, not Set: set=%d clear=%d", svc.setCalls, svc.clearCalls)
	}
}

func TestPutRecordingOverride_MissingParam_Returns400(t *testing.T) {
	t.Parallel()
	svc := &stubRecordingService{}
	body := `{"central":"ccu1","record":true}`
	req := httptest.NewRequest(http.MethodPut, "/api/v1/history/recording", strings.NewReader(body))
	w := httptest.NewRecorder()
	PutRecordingOverride(svc, nil).ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestPutRecordingOverride_NilService_Returns503(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest(http.MethodPut, "/api/v1/history/recording", strings.NewReader(`{}`))
	w := httptest.NewRecorder()
	PutRecordingOverride(nil, nil).ServeHTTP(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", w.Code)
	}
}
