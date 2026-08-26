// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

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

// fakeRestartPendingProvider is a configurable test double for
// RestartPendingProvider.
type fakeRestartPendingProvider struct {
	pending bool
	fields  []string
	err     error
}

func (f *fakeRestartPendingProvider) Pending(_ context.Context) (pending bool, fields []string, err error) {
	return f.pending, f.fields, f.err
}

func callRestartPending(t *testing.T, p RestartPendingProvider) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/system/restart-pending", http.NoBody)
	w := httptest.NewRecorder()
	GetRestartPending(p).ServeHTTP(w, req)
	return w
}

// TestGetRestartPending_PendingTrue verifies the handler returns 200 with
// pending=true and the affected field paths when the provider reports a
// pending restart.
func TestGetRestartPending_PendingTrue(t *testing.T) {
	t.Parallel()
	p := &fakeRestartPendingProvider{
		pending: true,
		fields:  []string{"north.matter.enabled"},
	}
	w := callRestartPending(t, p)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var body RestartPendingResponse
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !body.Pending {
		t.Error("expected pending=true")
	}
	if len(body.Fields) != 1 || body.Fields[0] != "north.matter.enabled" {
		t.Errorf("expected fields=[\"north.matter.enabled\"], got %v", body.Fields)
	}
}

// TestGetRestartPending_NotPending verifies that a not-pending response
// has pending=false and an explicit empty JSON array (not null) for fields.
func TestGetRestartPending_NotPending(t *testing.T) {
	t.Parallel()
	p := &fakeRestartPendingProvider{pending: false, fields: nil}
	w := callRestartPending(t, p)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	// Verify the raw JSON contains "fields":[] and not "fields":null.
	raw := w.Body.String()
	if !strings.Contains(raw, `"fields":[]`) {
		t.Errorf("expected fields:[] in JSON body, got: %s", raw)
	}
	var body RestartPendingResponse
	if err := json.Unmarshal([]byte(raw), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body.Pending {
		t.Error("expected pending=false")
	}
	if body.Fields == nil {
		t.Error("expected non-nil fields slice (empty array)")
	}
	if len(body.Fields) != 0 {
		t.Errorf("expected empty fields, got %v", body.Fields)
	}
}

// TestGetRestartPending_ProviderError verifies that a provider error
// results in a 500 response with problem+json content type.
func TestGetRestartPending_ProviderError(t *testing.T) {
	t.Parallel()
	p := &fakeRestartPendingProvider{err: errors.New("db unavailable")}
	w := callRestartPending(t, p)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d body=%s", w.Code, w.Body.String())
	}
	ct := w.Header().Get("Content-Type")
	if !strings.HasPrefix(ct, "application/problem+json") {
		t.Errorf("expected problem+json content type, got %q", ct)
	}
}

// TestGetRestartPending_NilProvider verifies that a nil provider degrades
// gracefully to pending=false without panicking.
func TestGetRestartPending_NilProvider(t *testing.T) {
	t.Parallel()
	w := callRestartPending(t, nil)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var body RestartPendingResponse
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body.Pending {
		t.Error("nil provider: expected pending=false")
	}
}
