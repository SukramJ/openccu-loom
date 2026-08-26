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
	"testing"
)

// stubUISchemaService is an inline stub for UISchemaService.
type stubUISchemaService struct {
	schema *UISchema
	err    error
}

func (s *stubUISchemaService) UISchema(_ context.Context, _ UISchemaRequest) (*UISchema, error) {
	return s.schema, s.err
}

func TestUISchemaHandler_HappyPath(t *testing.T) {
	t.Parallel()
	svc := &stubUISchemaService{
		schema: &UISchema{
			Channel: UISchemaChannel{
				Address: "DEV001:1",
				Number:  1,
				Device:  "DEV001",
			},
		},
	}
	req := httptest.NewRequest(http.MethodGet, "/?locale=en&paramset=VALUES", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "DEV001", "no": "1"}))
	w := httptest.NewRecorder()
	UISchemaHandler(svc).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var schema UISchema
	if err := json.Unmarshal(w.Body.Bytes(), &schema); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if schema.Channel.Device != "DEV001" {
		t.Fatalf("unexpected channel device: %q", schema.Channel.Device)
	}
}

func TestUISchemaHandler_ServiceNil_Returns503(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "DEV001", "no": "1"}))
	w := httptest.NewRecorder()
	UISchemaHandler(nil).ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", w.Code)
	}
}

func TestUISchemaHandler_InvalidChannelNo_Returns400(t *testing.T) {
	t.Parallel()
	svc := &stubUISchemaService{}
	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "DEV001", "no": "abc"}))
	w := httptest.NewRecorder()
	UISchemaHandler(svc).ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestUISchemaHandler_NotFound_Returns404(t *testing.T) {
	t.Parallel()
	svc := &stubUISchemaService{err: fmt.Errorf("wrap: %w", ErrUISchemaNotFound)}
	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "DEV001", "no": "99"}))
	w := httptest.NewRecorder()
	UISchemaHandler(svc).ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestUISchemaHandler_ServiceError_Returns500(t *testing.T) {
	t.Parallel()
	svc := &stubUISchemaService{err: errors.New("internal adapter error")}
	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "DEV001", "no": "1"}))
	w := httptest.NewRecorder()
	UISchemaHandler(svc).ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", w.Code)
	}
}
