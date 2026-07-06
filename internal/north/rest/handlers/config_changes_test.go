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
)

// stubChangesProvider is a minimal in-memory stub for ConfigChangesProvider.
type stubChangesProvider struct {
	fields []string
	err    error
}

func (s *stubChangesProvider) Changes(_ context.Context) ([]string, error) {
	return s.fields, s.err
}

// TestGetConfigChanges_TwoFields_Returns200 verifies that when the provider
// returns two changed paths the handler responds with HTTP 200 and a body
// containing exactly those two paths.
func TestGetConfigChanges_TwoFields_Returns200(t *testing.T) {
	t.Parallel()
	p := &stubChangesProvider{fields: []string{"north.mqtt.broker_url", "locale"}}
	req := httptest.NewRequest(http.MethodGet, "/system/config-changes", http.NoBody)
	w := httptest.NewRecorder()

	GetConfigChanges(p).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var resp ConfigChangesResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if len(resp.Fields) != 2 {
		t.Fatalf("expected 2 fields, got %d: %v", len(resp.Fields), resp.Fields)
	}
	wantPaths := []string{"north.mqtt.broker_url", "locale"}
	for _, want := range wantPaths {
		found := false
		for _, got := range resp.Fields {
			if got == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected path %q in response fields, got %v", want, resp.Fields)
		}
	}
}

// TestGetConfigChanges_NilFromProvider_Returns200WithEmptyArray verifies that
// when the provider returns nil the handler serialises an empty JSON array,
// not a JSON null.
func TestGetConfigChanges_NilFromProvider_Returns200WithEmptyArray(t *testing.T) {
	t.Parallel()
	p := &stubChangesProvider{fields: nil}
	req := httptest.NewRequest(http.MethodGet, "/system/config-changes", http.NoBody)
	w := httptest.NewRecorder()

	GetConfigChanges(p).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"fields":[]`) {
		t.Errorf("expected body to contain %q, got %s", `"fields":[]`, w.Body.String())
	}
}

// TestGetConfigChanges_ProviderError_Returns500 verifies that when the
// provider returns an error the handler responds with HTTP 500 and a
// generic problem body — the real error text is logged (see
// [writeServerError]), never echoed to the caller, so a driver-specific
// message like "db failure" cannot leak through this 5xx response.
func TestGetConfigChanges_ProviderError_Returns500(t *testing.T) {
	t.Parallel()
	p := &stubChangesProvider{err: errors.New("db failure")}
	req := httptest.NewRequest(http.MethodGet, "/system/config-changes", http.NoBody)
	w := httptest.NewRecorder()

	GetConfigChanges(p).ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d body=%s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "db failure") {
		t.Errorf("5xx body must not echo the underlying error text, got %s", w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "Config-changes check failed") {
		t.Errorf("expected the generic title in body, got %s", w.Body.String())
	}
}

// TestGetConfigChanges_NilProvider_Returns200WithEmptyArray verifies that a
// nil provider degrades gracefully to HTTP 200 with an empty fields array.
func TestGetConfigChanges_NilProvider_Returns200WithEmptyArray(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest(http.MethodGet, "/system/config-changes", http.NoBody)
	w := httptest.NewRecorder()

	GetConfigChanges(nil).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 with nil provider, got %d body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"fields":[]`) {
		t.Errorf("expected body to contain %q, got %s", `"fields":[]`, w.Body.String())
	}
}
