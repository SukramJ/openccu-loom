// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmerr"
)

// stubParamsetService is an inline stub for ParamsetService.
type stubParamsetService struct {
	getResult     map[string]any
	getErr        error
	putErr        error
	getLinkResult map[string]any
	getLinkErr    error
	putLinkErr    error

	// putCalls / putLinkCalls count invocations so lock-gating tests can
	// assert the service was never reached when a write is rejected.
	putCalls     int
	putLinkCalls int
}

func (s *stubParamsetService) GetParamset(_ context.Context, _ string, _ hmenum.ParamsetKey) (map[string]any, error) {
	return s.getResult, s.getErr
}

func (s *stubParamsetService) PutParamset(_ context.Context, _ string, _ hmenum.ParamsetKey, _ map[string]any) error {
	s.putCalls++
	return s.putErr
}

func (s *stubParamsetService) GetLinkParamset(_ context.Context, _, _ string) (map[string]any, error) {
	return s.getLinkResult, s.getLinkErr
}

func (s *stubParamsetService) PutLinkParamset(_ context.Context, _, _ string, _ map[string]any) error {
	s.putLinkCalls++
	return s.putLinkErr
}

func TestGetParamset_HappyPath(t *testing.T) {
	t.Parallel()
	svc := &stubParamsetService{
		getResult: map[string]any{"LEVEL": 0.5, "STATE": true},
	}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/devices/DEV001:1/paramsets/VALUES", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "DEV001:1", "key": "VALUES"}))
	w := httptest.NewRecorder()
	GetParamset(svc).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, ok := body["LEVEL"]; !ok {
		t.Fatal("expected LEVEL in response")
	}
}

func TestGetParamset_ServiceNil_Returns503(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "DEV001:1", "key": "VALUES"}))
	w := httptest.NewRecorder()
	GetParamset(nil).ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", w.Code)
	}
}

func TestGetParamset_InvalidKey_Returns400(t *testing.T) {
	t.Parallel()
	svc := &stubParamsetService{}
	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "DEV001:1", "key": "INVALID"}))
	w := httptest.NewRecorder()
	GetParamset(svc).ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestGetParamset_ServiceError_Returns502(t *testing.T) {
	t.Parallel()
	svc := &stubParamsetService{getErr: errors.New("CCU error")}
	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "DEV001:1", "key": "MASTER"}))
	w := httptest.NewRecorder()
	GetParamset(svc).ServeHTTP(w, req)

	if w.Code != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d", w.Code)
	}
}

func TestPutParamset_HappyPath(t *testing.T) {
	t.Parallel()
	svc := &stubParamsetService{}
	body := strings.NewReader(`{"LEVEL": 0.75}`)
	req := httptest.NewRequest(http.MethodPut, "/", body)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "DEV001:1", "key": "VALUES"}))
	w := httptest.NewRecorder()
	PutParamset(svc, nil).ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestPutParamset_InvalidJSON_Returns400(t *testing.T) {
	t.Parallel()
	svc := &stubParamsetService{}
	body := strings.NewReader(`NOT JSON`)
	req := httptest.NewRequest(http.MethodPut, "/", body)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "DEV001:1", "key": "VALUES"}))
	w := httptest.NewRecorder()
	PutParamset(svc, nil).ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestGetLinkParamset_HappyPath(t *testing.T) {
	t.Parallel()
	svc := &stubParamsetService{
		getLinkResult: map[string]any{"SHORT_ON_TIME": 0.5},
	}
	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "DEV001:1", "peer": "DEV002:1"}))
	w := httptest.NewRecorder()
	GetLinkParamset(svc).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestGetLinkParamset_NoPeer_Returns400(t *testing.T) {
	t.Parallel()
	svc := &stubParamsetService{}
	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "DEV001:1"}))
	w := httptest.NewRecorder()
	GetLinkParamset(svc).ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestPutLinkParamset_HappyPath(t *testing.T) {
	t.Parallel()
	svc := &stubParamsetService{}
	body := strings.NewReader(`{"SHORT_ON_TIME": 1.0}`)
	req := httptest.NewRequest(http.MethodPut, "/", body)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "DEV001:1", "peer": "DEV002:1"}))
	w := httptest.NewRecorder()
	PutLinkParamset(svc, nil).ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d body=%s", w.Code, w.Body.String())
	}
}

// TestPutParamset_HiddenParam_Returns403 asserts that when the service
// returns ErrParameterHidden the handler responds with HTTP 403
// (Forbidden) and a problem+json body.
func TestPutParamset_HiddenParam_Returns403(t *testing.T) {
	t.Parallel()
	svc := &stubParamsetService{
		putErr: fmt.Errorf("parameter %q: %w", "PARTY_MODE_SUBMIT", hmerr.ErrParameterHidden),
	}
	body := strings.NewReader(`{"PARTY_MODE_SUBMIT": "submit"}`)
	req := httptest.NewRequest(http.MethodPut, "/", body)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "DEV001:1", "key": "VALUES"}))
	w := httptest.NewRecorder()
	PutParamset(svc, nil).ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for hidden parameter, got %d body=%s", w.Code, w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/problem+json" {
		t.Errorf("expected problem+json content type, got %q", ct)
	}
}

// TestPutParamset_LockedChannel_Returns423 asserts that an operator
// channel lock surfaces as 423 Locked: the VALUES write never reached the
// CCU, so it must not look like an upstream failure.
func TestPutParamset_LockedChannel_Returns423(t *testing.T) {
	t.Parallel()
	svc := &stubParamsetService{
		putErr: fmt.Errorf("put paramset: %w", device.ErrChannelOperationLocked),
	}
	body := strings.NewReader(`{"LEVEL": 0.5}`)
	req := httptest.NewRequest(http.MethodPut, "/", body)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "DEV001:1", "key": "VALUES"}))
	w := httptest.NewRecorder()
	PutParamset(svc, nil).ServeHTTP(w, req)

	if w.Code != http.StatusLocked {
		t.Fatalf("expected 423 for a locked channel, got %d body=%s", w.Code, w.Body.String())
	}
}

// TestPutLinkParamset_HiddenParam_Returns403 asserts that when a link
// paramset write is rejected because a parameter is hidden, the
// handler returns HTTP 403.
func TestPutLinkParamset_HiddenParam_Returns403(t *testing.T) {
	t.Parallel()
	svc := &stubParamsetService{
		putLinkErr: fmt.Errorf("parameter %q: %w", "INTERNAL_FLAG", hmerr.ErrParameterHidden),
	}
	body := strings.NewReader(`{"INTERNAL_FLAG": 1}`)
	req := httptest.NewRequest(http.MethodPut, "/", body)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "DEV001:1", "peer": "DEV002:1"}))
	w := httptest.NewRecorder()
	PutLinkParamset(svc, nil).ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for hidden link parameter, got %d body=%s", w.Code, w.Body.String())
	}
}

// TestPutParamset_ServiceError_Returns502 guards the existing 502
// behaviour: non-hidden errors must still produce BadGateway.
func TestPutParamset_BackendError_Returns502(t *testing.T) {
	t.Parallel()
	svc := &stubParamsetService{putErr: errors.New("CCU unreachable")}
	body := strings.NewReader(`{"LEVEL": 0.5}`)
	req := httptest.NewRequest(http.MethodPut, "/", body)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "DEV001:1", "key": "VALUES"}))
	w := httptest.NewRecorder()
	PutParamset(svc, nil).ServeHTTP(w, req)

	if w.Code != http.StatusBadGateway {
		t.Fatalf("expected 502 for backend error, got %d", w.Code)
	}
}

// --- PutLinkParamset additional paths ---

func TestPutLinkParamset_NilService_Returns503(t *testing.T) {
	t.Parallel()
	r := chi.NewRouter()
	r.Put("/devices/{addr}/link-paramsets/{peer}", PutLinkParamset(nil, nil))
	req := httptest.NewRequest(http.MethodPut, "/devices/DEV001:1/link-paramsets/DEV002:1", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestPutLinkParamset_EmptyPeer_Returns400(t *testing.T) {
	t.Parallel()
	svc := &stubParamsetService{}
	req := httptest.NewRequest(http.MethodPut, "/link-paramsets/", strings.NewReader(`{}`))
	req = req.WithContext(chiContext(req, map[string]string{"addr": "DEV001:1", "peer": ""}))
	w := httptest.NewRecorder()
	PutLinkParamset(svc, nil).ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for empty peer, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestPutLinkParamset_BadBody_Returns400(t *testing.T) {
	t.Parallel()
	svc := &stubParamsetService{}
	req := httptest.NewRequest(http.MethodPut, "/link-paramsets/peer", strings.NewReader("not-json"))
	req = req.WithContext(chiContext(req, map[string]string{"addr": "DEV001:1", "peer": "DEV002:1"}))
	w := httptest.NewRecorder()
	PutLinkParamset(svc, nil).ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestPutLinkParamset_HiddenParamDirect_Returns403(t *testing.T) {
	t.Parallel()
	svc := &stubParamsetService{putLinkErr: hmerr.ErrParameterHidden}
	req := httptest.NewRequest(http.MethodPut, "/link-paramsets/peer", strings.NewReader(`{"KEY":"value"}`))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(chiContext(req, map[string]string{"addr": "DEV001:1", "peer": "DEV002:1"}))
	w := httptest.NewRecorder()
	PutLinkParamset(svc, nil).ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestPutLinkParamset_ServiceError_Returns502(t *testing.T) {
	t.Parallel()
	svc := &stubParamsetService{putLinkErr: errors.New("backend error")}
	req := httptest.NewRequest(http.MethodPut, "/link-paramsets/peer", strings.NewReader(`{"KEY":"value"}`))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(chiContext(req, map[string]string{"addr": "DEV001:1", "peer": "DEV002:1"}))
	w := httptest.NewRecorder()
	PutLinkParamset(svc, nil).ServeHTTP(w, req)

	if w.Code != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestPutLinkParamset_HappyPath_Returns202(t *testing.T) {
	t.Parallel()
	svc := &stubParamsetService{}
	req := httptest.NewRequest(http.MethodPut, "/link-paramsets/peer", strings.NewReader(`{"KEY":"value"}`))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(chiContext(req, map[string]string{"addr": "DEV001:1", "peer": "DEV002:1"}))
	w := httptest.NewRecorder()
	PutLinkParamset(svc, nil).ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestGetLinkParamset_ServiceError_Returns502(t *testing.T) {
	t.Parallel()
	svc := &stubParamsetService{getLinkErr: errors.New("get failed")}
	req := httptest.NewRequest(http.MethodGet, "/link-paramsets/peer", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "DEV001:1", "peer": "DEV002:1"}))
	w := httptest.NewRecorder()
	GetLinkParamset(svc).ServeHTTP(w, req)

	if w.Code != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d body=%s", w.Code, w.Body.String())
	}
}

// --- Strict edit-lock enforcement (MASTER/LINK gated, VALUES not) ---

func TestPutParamset_MasterNoToken_Returns423(t *testing.T) {
	t.Parallel()
	locks := NewEditSessions()
	svc := &stubParamsetService{}
	body := strings.NewReader(`{"CTRL_MODE": 1}`)
	req := httptest.NewRequest(http.MethodPut, "/", body)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "DEV001:1", "key": "MASTER"}))
	w := httptest.NewRecorder()
	PutParamset(svc, locks).ServeHTTP(w, req)

	if w.Code != http.StatusLocked {
		t.Fatalf("expected 423, got %d body=%s", w.Code, w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/problem+json" {
		t.Errorf("expected problem+json content type, got %q", ct)
	}
	if svc.putCalls != 0 {
		t.Fatalf("expected service NOT to be called, got %d calls", svc.putCalls)
	}
}

func TestPutParamset_MasterWrongToken_Returns423(t *testing.T) {
	t.Parallel()
	locks := NewEditSessions()
	if _, ok := locks.Open("channel:DEV001:1:MASTER", "test"); !ok {
		t.Fatal("open failed")
	}
	svc := &stubParamsetService{}
	body := strings.NewReader(`{"CTRL_MODE": 1}`)
	req := httptest.NewRequest(http.MethodPut, "/", body)
	req.Header.Set(EditTokenHeader, "wrong-token")
	req = req.WithContext(chiContext(req, map[string]string{"addr": "DEV001:1", "key": "MASTER"}))
	w := httptest.NewRecorder()
	PutParamset(svc, locks).ServeHTTP(w, req)

	if w.Code != http.StatusLocked {
		t.Fatalf("expected 423, got %d body=%s", w.Code, w.Body.String())
	}
	if svc.putCalls != 0 {
		t.Fatalf("expected service NOT to be called, got %d calls", svc.putCalls)
	}
}

func TestPutParamset_MasterHeldToken_Returns202(t *testing.T) {
	t.Parallel()
	locks := NewEditSessions()
	lock, ok := locks.Open("channel:DEV001:1:MASTER", "test")
	if !ok {
		t.Fatal("open failed")
	}
	svc := &stubParamsetService{}
	body := strings.NewReader(`{"CTRL_MODE": 1}`)
	req := httptest.NewRequest(http.MethodPut, "/", body)
	req.Header.Set(EditTokenHeader, lock.Token)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "DEV001:1", "key": "MASTER"}))
	w := httptest.NewRecorder()
	PutParamset(svc, locks).ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d body=%s", w.Code, w.Body.String())
	}
	if svc.putCalls != 1 {
		t.Fatalf("expected service to be called once, got %d calls", svc.putCalls)
	}
}

// TestPutParamset_ValuesNoToken_Returns202 proves VALUES writes are
// never gated even when a real edit-lock verifier is wired in.
func TestPutParamset_ValuesNoToken_Returns202(t *testing.T) {
	t.Parallel()
	locks := NewEditSessions()
	svc := &stubParamsetService{}
	body := strings.NewReader(`{"LEVEL": 0.5}`)
	req := httptest.NewRequest(http.MethodPut, "/", body)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "DEV001:1", "key": "VALUES"}))
	w := httptest.NewRecorder()
	PutParamset(svc, locks).ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d body=%s", w.Code, w.Body.String())
	}
	if svc.putCalls != 1 {
		t.Fatalf("expected service to be called once, got %d calls", svc.putCalls)
	}
}

func TestPutLinkParamset_NoToken_Returns423(t *testing.T) {
	t.Parallel()
	locks := NewEditSessions()
	svc := &stubParamsetService{}
	body := strings.NewReader(`{"SHORT_ON_TIME": 1.0}`)
	req := httptest.NewRequest(http.MethodPut, "/", body)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "DEV001:1", "peer": "DEV002:1"}))
	w := httptest.NewRecorder()
	PutLinkParamset(svc, locks).ServeHTTP(w, req)

	if w.Code != http.StatusLocked {
		t.Fatalf("expected 423, got %d body=%s", w.Code, w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/problem+json" {
		t.Errorf("expected problem+json content type, got %q", ct)
	}
	if svc.putLinkCalls != 0 {
		t.Fatalf("expected service NOT to be called, got %d calls", svc.putLinkCalls)
	}
}

func TestPutLinkParamset_HeldToken_Returns202(t *testing.T) {
	t.Parallel()
	locks := NewEditSessions()
	lock, ok := locks.Open("channel:DEV001:1:LINK:DEV002:1", "test")
	if !ok {
		t.Fatal("open failed")
	}
	svc := &stubParamsetService{}
	body := strings.NewReader(`{"SHORT_ON_TIME": 1.0}`)
	req := httptest.NewRequest(http.MethodPut, "/", body)
	req.Header.Set(EditTokenHeader, lock.Token)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "DEV001:1", "peer": "DEV002:1"}))
	w := httptest.NewRecorder()
	PutLinkParamset(svc, locks).ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d body=%s", w.Code, w.Body.String())
	}
	if svc.putLinkCalls != 1 {
		t.Fatalf("expected service to be called once, got %d calls", svc.putLinkCalls)
	}
}
