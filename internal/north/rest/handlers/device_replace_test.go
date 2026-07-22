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

	"github.com/SukramJ/openccu-loom/internal/audit"
	"github.com/SukramJ/openccu-loom/internal/client/backends"
	"github.com/SukramJ/openccu-loom/pkg/hmapi"
	"github.com/SukramJ/openccu-loom/pkg/hmerr"
)

// stubDeviceReplacer is an inline stub for DeviceReplacePort.
type stubDeviceReplacer struct {
	candidates    []hmapi.ReplaceCandidate
	candidatesErr error
	replaceErr    error

	lastCandidatesCentral string
	lastCandidatesAddress string

	lastReplaceCentral    string
	lastReplaceOldAddress string
	lastReplaceNewAddress string
}

func (s *stubDeviceReplacer) ReplaceCandidates(_ context.Context, central, newAddress string) ([]hmapi.ReplaceCandidate, error) {
	s.lastCandidatesCentral = central
	s.lastCandidatesAddress = newAddress
	if s.candidatesErr != nil {
		return nil, s.candidatesErr
	}
	return s.candidates, nil
}

func (s *stubDeviceReplacer) ReplaceDevice(_ context.Context, central, oldAddress, newAddress string) error {
	s.lastReplaceCentral = central
	s.lastReplaceOldAddress = oldAddress
	s.lastReplaceNewAddress = newAddress
	return s.replaceErr
}

// --- GetDeviceReplaceCandidates ---

func TestGetDeviceReplaceCandidates_HappyPath_Returns200(t *testing.T) {
	t.Parallel()
	svc := &stubDeviceReplacer{candidates: []hmapi.ReplaceCandidate{
		{Address: "OLD001", Model: "HM-Sec-SC", Interface: "BidCos-RF", Central: "ccu-01", ModelMatches: true},
	}}
	req := httptest.NewRequest(http.MethodGet, "/?central=ccu-01", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "NEW001"}))
	w := httptest.NewRecorder()
	GetDeviceReplaceCandidates(svc).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	if svc.lastCandidatesAddress != "NEW001" || svc.lastCandidatesCentral != "ccu-01" {
		t.Fatalf("forwarded address/central mismatch: address=%q central=%q", svc.lastCandidatesAddress, svc.lastCandidatesCentral)
	}
	var body struct {
		Candidates []hmapi.ReplaceCandidate `json:"candidates"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(body.Candidates) != 1 || body.Candidates[0].Address != "OLD001" {
		t.Fatalf("candidates=%+v", body.Candidates)
	}
}

func TestGetDeviceReplaceCandidates_NilResult_ReturnsEmptyArray(t *testing.T) {
	t.Parallel()
	svc := &stubDeviceReplacer{candidates: nil}
	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "NEW001"}))
	w := httptest.NewRecorder()
	GetDeviceReplaceCandidates(svc).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var body struct {
		Candidates []hmapi.ReplaceCandidate `json:"candidates"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body.Candidates == nil || len(body.Candidates) != 0 {
		t.Fatalf("expected a non-nil empty candidates array, got %v", body.Candidates)
	}
}

func TestGetDeviceReplaceCandidates_NilService_Returns503(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "NEW001"}))
	w := httptest.NewRecorder()
	GetDeviceReplaceCandidates(nil).ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestGetDeviceReplaceCandidates_MissingAddr_Returns400(t *testing.T) {
	t.Parallel()
	svc := &stubDeviceReplacer{}
	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"addr": ""}))
	w := httptest.NewRecorder()
	GetDeviceReplaceCandidates(svc).ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", w.Code, w.Body.String())
	}
	if svc.lastCandidatesAddress != "" {
		t.Errorf("domain layer must not be called on a missing address, got %q", svc.lastCandidatesAddress)
	}
}

func TestGetDeviceReplaceCandidates_UnknownCentral_Returns404(t *testing.T) {
	t.Parallel()
	svc := &stubDeviceReplacer{candidatesErr: hmerr.ErrUnknownCentral}
	req := httptest.NewRequest(http.MethodGet, "/?central=nope", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "NEW001"}))
	w := httptest.NewRecorder()
	GetDeviceReplaceCandidates(svc).ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestGetDeviceReplaceCandidates_Unsupported_Returns422(t *testing.T) {
	t.Parallel()
	svc := &stubDeviceReplacer{candidatesErr: backends.ErrUnsupported}
	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "NEW001"}))
	w := httptest.NewRecorder()
	GetDeviceReplaceCandidates(svc).ServeHTTP(w, req)

	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestGetDeviceReplaceCandidates_ServiceError_Returns502(t *testing.T) {
	t.Parallel()
	svc := &stubDeviceReplacer{candidatesErr: errors.New("CCU unreachable")}
	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "NEW001"}))
	w := httptest.NewRecorder()
	GetDeviceReplaceCandidates(svc).ServeHTTP(w, req)

	if w.Code != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d body=%s", w.Code, w.Body.String())
	}
}

// --- PostDeviceReplace ---

func TestPostDeviceReplace_HappyPath_Returns202AndRecordsAudit(t *testing.T) {
	t.Parallel()
	svc := &stubDeviceReplacer{}
	rec := &captureRecorder{}
	body := strings.NewReader(`{"old_address":"OLD001","central":"ccu-01"}`)
	req := httptest.NewRequest(http.MethodPost, "/", body)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "NEW001"}))
	w := httptest.NewRecorder()
	PostDeviceReplace(svc, rec).ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d body=%s", w.Code, w.Body.String())
	}
	if svc.lastReplaceOldAddress != "OLD001" || svc.lastReplaceNewAddress != "NEW001" || svc.lastReplaceCentral != "ccu-01" {
		t.Fatalf("forwarded call mismatch: old=%q new=%q central=%q",
			svc.lastReplaceOldAddress, svc.lastReplaceNewAddress, svc.lastReplaceCentral)
	}
	var respBody struct {
		Status     string `json:"status"`
		OldAddress string `json:"old_address"`
		NewAddress string `json:"new_address"`
		Central    string `json:"central"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &respBody); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if respBody.Status != "replacing" || respBody.OldAddress != "OLD001" || respBody.NewAddress != "NEW001" || respBody.Central != "ccu-01" {
		t.Fatalf("response body=%+v", respBody)
	}
	if len(rec.entries) != 1 {
		t.Fatalf("expected exactly 1 audit entry, got %d: %+v", len(rec.entries), rec.entries)
	}
	e := rec.entries[0]
	if e.Action != audit.ActionDeviceReplace {
		t.Errorf("expected action=%q, got %q", audit.ActionDeviceReplace, e.Action)
	}
	if e.DeviceAddress != "OLD001" {
		t.Errorf("expected device_address=%q, got %q", "OLD001", e.DeviceAddress)
	}
	if !strings.Contains(e.Note, "NEW001") {
		t.Errorf("expected note to mention the new address NEW001, got %q", e.Note)
	}
}

func TestPostDeviceReplace_MissingOldAddress_Returns422(t *testing.T) {
	t.Parallel()
	svc := &stubDeviceReplacer{}
	body := strings.NewReader(`{}`)
	req := httptest.NewRequest(http.MethodPost, "/", body)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "NEW001"}))
	w := httptest.NewRecorder()
	PostDeviceReplace(svc, nil).ServeHTTP(w, req)

	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422, got %d body=%s", w.Code, w.Body.String())
	}
	if svc.lastReplaceOldAddress != "" {
		t.Errorf("domain layer must not be called without old_address, got %q", svc.lastReplaceOldAddress)
	}
}

func TestPostDeviceReplace_BadJSON_Returns400(t *testing.T) {
	t.Parallel()
	svc := &stubDeviceReplacer{}
	body := strings.NewReader(`{"old_address":`)
	req := httptest.NewRequest(http.MethodPost, "/", body)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "NEW001"}))
	w := httptest.NewRecorder()
	PostDeviceReplace(svc, nil).ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestPostDeviceReplace_NilService_Returns503(t *testing.T) {
	t.Parallel()
	body := strings.NewReader(`{"old_address":"OLD001"}`)
	req := httptest.NewRequest(http.MethodPost, "/", body)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "NEW001"}))
	w := httptest.NewRecorder()
	PostDeviceReplace(nil, nil).ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestPostDeviceReplace_Unsupported_Returns422(t *testing.T) {
	t.Parallel()
	svc := &stubDeviceReplacer{replaceErr: backends.ErrUnsupported}
	body := strings.NewReader(`{"old_address":"OLD001"}`)
	req := httptest.NewRequest(http.MethodPost, "/", body)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "NEW001"}))
	w := httptest.NewRecorder()
	PostDeviceReplace(svc, nil).ServeHTTP(w, req)

	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestPostDeviceReplace_UnknownCentral_Returns404(t *testing.T) {
	t.Parallel()
	svc := &stubDeviceReplacer{replaceErr: hmerr.ErrUnknownCentral}
	body := strings.NewReader(`{"old_address":"OLD001","central":"nope"}`)
	req := httptest.NewRequest(http.MethodPost, "/", body)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "NEW001"}))
	w := httptest.NewRecorder()
	PostDeviceReplace(svc, nil).ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestPostDeviceReplace_MissingAddr_Returns400(t *testing.T) {
	t.Parallel()
	svc := &stubDeviceReplacer{}
	body := strings.NewReader(`{"old_address":"OLD001"}`)
	req := httptest.NewRequest(http.MethodPost, "/", body)
	req = req.WithContext(chiContext(req, map[string]string{"addr": ""}))
	w := httptest.NewRecorder()
	PostDeviceReplace(svc, nil).ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", w.Code, w.Body.String())
	}
	if svc.lastReplaceOldAddress != "" {
		t.Errorf("domain layer must not be called on a missing address, got %q", svc.lastReplaceOldAddress)
	}
}
