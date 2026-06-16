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

	"github.com/SukramJ/openccu-loom/pkg/hmerr"
)

// stubLinksService is an inline stub for LinksService.
type stubLinksService struct {
	listResult     []Link
	listErr        error
	addErr         error
	removeErr      error
	linkableResult []LinkableChannel
	linkableErr    error
}

func (s *stubLinksService) ListLinks(_ context.Context, _, _ string) ([]Link, error) {
	return s.listResult, s.listErr
}

func (s *stubLinksService) AddLink(_ context.Context, _, _, _, _ string) error {
	return s.addErr
}

func (s *stubLinksService) RemoveLink(_ context.Context, _, _ string) error {
	return s.removeErr
}

func (s *stubLinksService) LinkableChannels(_ context.Context, _, _, _, _ string) ([]LinkableChannel, error) {
	return s.linkableResult, s.linkableErr
}

func TestListLinks_HappyPath(t *testing.T) {
	t.Parallel()
	svc := &stubLinksService{
		listResult: []Link{
			{Sender: "DEV001:1", Receiver: "DEV002:1", Direction: "SENDER"},
		},
	}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/devices/DEV001/links", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "DEV001"}))
	w := httptest.NewRecorder()
	ListLinks(svc).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var body []Link
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(body) != 1 {
		t.Fatalf("expected 1 link, got %d", len(body))
	}
}

func TestListLinks_ServiceNil_Returns503(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "DEV001"}))
	w := httptest.NewRecorder()
	ListLinks(nil).ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", w.Code)
	}
}

func TestListLinks_ServiceError(t *testing.T) {
	t.Parallel()
	svc := &stubLinksService{listErr: errors.New("backend error")}
	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "DEV001"}))
	w := httptest.NewRecorder()
	ListLinks(svc).ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", w.Code)
	}
}

func TestAddLink_HappyPath(t *testing.T) {
	t.Parallel()
	svc := &stubLinksService{}
	body := strings.NewReader(`{"sender_address":"DEV001:1","receiver_address":"DEV002:1"}`)
	req := httptest.NewRequest(http.MethodPost, "/", body)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "DEV001"}))
	w := httptest.NewRecorder()
	AddLink(svc).ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestAddLink_MissingAddresses_Returns400(t *testing.T) {
	t.Parallel()
	svc := &stubLinksService{}
	body := strings.NewReader(`{"sender_address":"DEV001:1"}`)
	req := httptest.NewRequest(http.MethodPost, "/", body)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "DEV001"}))
	w := httptest.NewRecorder()
	AddLink(svc).ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestRemoveLink_HappyPath(t *testing.T) {
	t.Parallel()
	svc := &stubLinksService{}
	req := httptest.NewRequest(http.MethodDelete, "/?sender=DEV001:1&receiver=DEV002:1", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "DEV001"}))
	w := httptest.NewRecorder()
	RemoveLink(svc).ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestRemoveLink_MissingQueryParams_Returns400(t *testing.T) {
	t.Parallel()
	svc := &stubLinksService{}
	req := httptest.NewRequest(http.MethodDelete, "/?sender=DEV001:1", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "DEV001"}))
	w := httptest.NewRecorder()
	RemoveLink(svc).ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestLinkableChannels_HappyPath(t *testing.T) {
	t.Parallel()
	svc := &stubLinksService{
		linkableResult: []LinkableChannel{
			{Address: "DEV002:1", DeviceAddress: "DEV002"},
		},
	}
	req := httptest.NewRequest(http.MethodGet, "/?role=sender&interface=HmIP-RF", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "DEV001", "no": "1"}))
	w := httptest.NewRecorder()
	LinkableChannels(svc).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestLinkableChannels_BadRole_Returns400(t *testing.T) {
	t.Parallel()
	svc := &stubLinksService{}
	req := httptest.NewRequest(http.MethodGet, "/?role=invalid&interface=HmIP-RF", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "DEV001", "no": "1"}))
	w := httptest.NewRecorder()
	LinkableChannels(svc).ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestLinkableChannels_MissingInterface_Returns400(t *testing.T) {
	t.Parallel()
	svc := &stubLinksService{}
	req := httptest.NewRequest(http.MethodGet, "/?role=receiver", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "DEV001", "no": "1"}))
	w := httptest.NewRecorder()
	LinkableChannels(svc).ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

// --- AddLink error paths ---

func TestAddLink_InvalidJSON_Returns400(t *testing.T) {
	t.Parallel()
	svc := &stubLinksService{}
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("NOT JSON"))
	req = req.WithContext(chiContext(req, map[string]string{"addr": "DEV001"}))
	w := httptest.NewRecorder()
	AddLink(svc).ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestAddLink_ServiceError_Returns502(t *testing.T) {
	t.Parallel()
	svc := &stubLinksService{addErr: errors.New("CCU error")}
	body := strings.NewReader(`{"sender_address":"DEV001:1","receiver_address":"DEV002:1"}`)
	req := httptest.NewRequest(http.MethodPost, "/", body)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "DEV001"}))
	w := httptest.NewRecorder()
	AddLink(svc).ServeHTTP(w, req)

	if w.Code != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestAddLink_NilService_Returns503(t *testing.T) {
	t.Parallel()
	body := strings.NewReader(`{"sender_address":"DEV001:1","receiver_address":"DEV002:1"}`)
	req := httptest.NewRequest(http.MethodPost, "/", body)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "DEV001"}))
	w := httptest.NewRecorder()
	AddLink(nil).ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", w.Code)
	}
}

// --- RemoveLink error paths ---

func TestRemoveLink_NilService_Returns503(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest(http.MethodDelete, "/?sender=DEV001:1&receiver=DEV002:1", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "DEV001"}))
	w := httptest.NewRecorder()
	RemoveLink(nil).ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", w.Code)
	}
}

func TestRemoveLink_ServiceError_Returns502(t *testing.T) {
	t.Parallel()
	svc := &stubLinksService{removeErr: errors.New("CCU error")}
	req := httptest.NewRequest(http.MethodDelete, "/?sender=DEV001:1&receiver=DEV002:1", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "DEV001"}))
	w := httptest.NewRecorder()
	RemoveLink(svc).ServeHTTP(w, req)

	if w.Code != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d body=%s", w.Code, w.Body.String())
	}
}

// --- ListLinks device-not-found ---

func TestListLinks_DeviceNotFound_Returns404(t *testing.T) {
	t.Parallel()
	svc := &stubLinksService{listErr: hmerr.ErrDescriptionNotFound}
	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "DEV001"}))
	w := httptest.NewRecorder()
	ListLinks(svc).ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d body=%s", w.Code, w.Body.String())
	}
}

// --- LinkableChannels error path ---

func TestLinkableChannels_ServiceError_Returns500(t *testing.T) {
	t.Parallel()
	svc := &stubLinksService{linkableErr: errors.New("backend error")}
	req := httptest.NewRequest(http.MethodGet, "/?role=sender&interface=HmIP-RF", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "DEV001", "no": "1"}))
	w := httptest.NewRecorder()
	LinkableChannels(svc).ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d body=%s", w.Code, w.Body.String())
	}
}

// --- ErrNoConnection → 502 upstream mapping ----------------------------------

func TestListLinks_NoConnection_Returns502(t *testing.T) {
	t.Parallel()
	svc := &stubLinksService{listErr: hmerr.ErrNoConnection}
	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "DEV001"}))
	w := httptest.NewRecorder()
	ListLinks(svc).ServeHTTP(w, req)

	if w.Code != http.StatusBadGateway {
		t.Fatalf("ErrNoConnection must produce 502, got %d", w.Code)
	}
}

func TestLinkableChannels_NoConnection_Returns502(t *testing.T) {
	t.Parallel()
	svc := &stubLinksService{linkableErr: hmerr.ErrNoConnection}
	req := httptest.NewRequest(http.MethodGet, "/?role=sender&interface=HmIP-RF", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "DEV001", "no": "1"}))
	w := httptest.NewRecorder()
	LinkableChannels(svc).ServeHTTP(w, req)

	if w.Code != http.StatusBadGateway {
		t.Fatalf("ErrNoConnection must produce 502, got %d", w.Code)
	}
}
