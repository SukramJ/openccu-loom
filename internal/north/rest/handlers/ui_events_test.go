// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package handlers

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPostUIEvent_HappyPath_Returns204(t *testing.T) {
	t.Parallel()
	body := `{"event":"page_view","properties":{"page":"/devices"}}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/ui/event", strings.NewReader(body))
	w := httptest.NewRecorder()
	PostUIEvent().ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestPostUIEvent_HappyPath_NoProperties_Returns204(t *testing.T) {
	t.Parallel()
	body := `{"event":"button_click"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/ui/event", strings.NewReader(body))
	w := httptest.NewRecorder()
	PostUIEvent().ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestPostUIEvent_MissingEventField_Returns400(t *testing.T) {
	t.Parallel()
	body := `{"properties":{"page":"/devices"}}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/ui/event", strings.NewReader(body))
	w := httptest.NewRecorder()
	PostUIEvent().ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestPostUIEvent_EmptyEventField_Returns400(t *testing.T) {
	t.Parallel()
	body := `{"event":""}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/ui/event", strings.NewReader(body))
	w := httptest.NewRecorder()
	PostUIEvent().ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestPostUIEvent_InvalidJSON_Returns400(t *testing.T) {
	t.Parallel()
	body := `{"event": not-valid-json}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/ui/event", bytes.NewBufferString(body))
	w := httptest.NewRecorder()
	PostUIEvent().ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestPostUIEvent_EmptyBody_Returns400(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/ui/event", http.NoBody)
	w := httptest.NewRecorder()
	PostUIEvent().ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", w.Code, w.Body.String())
	}
}
