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

	"github.com/SukramJ/openccu-loom/internal/audit"
)

// stubMQTTReloadService is a minimal in-memory stub for MQTTReloadService.
type stubMQTTReloadService struct {
	took time.Duration
	err  error
}

func (s *stubMQTTReloadService) Reload(_ context.Context) (time.Duration, error) {
	return s.took, s.err
}

// captureRecorder is an audit.Recorder that collects every entry for inspection.
type captureRecorder struct {
	entries []audit.Entry
}

func (c *captureRecorder) Record(e audit.Entry) {
	c.entries = append(c.entries, e)
}

// List satisfies the audit.Recorder interface if it has a List method;
// for this stub we only need Record, so we provide a no-op List as well.
func (c *captureRecorder) List(_ int) []audit.Entry { return c.entries }

// TestMQTTReload_HappyPath_Returns200 verifies that a successful Reload call
// yields HTTP 200 with Reloaded=true and TookMS equal to the service duration.
func TestMQTTReload_HappyPath_Returns200(t *testing.T) {
	t.Parallel()
	svc := &stubMQTTReloadService{took: 750 * time.Millisecond}
	req := httptest.NewRequest(http.MethodPost, "/admin/mqtt/reload", http.NoBody)
	w := httptest.NewRecorder()

	MQTTReload(svc, audit.NoopRecorder()).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var resp MQTTReloadResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if !resp.Reloaded {
		t.Error("expected Reloaded=true")
	}
	if resp.TookMS != 750 {
		t.Errorf("expected TookMS=750, got %d", resp.TookMS)
	}
}

// TestMQTTReload_RecordsAudit verifies that a successful Reload call records
// exactly one audit entry with ActionConfigSectionUpdate and note "mqtt.reload".
func TestMQTTReload_RecordsAudit(t *testing.T) {
	t.Parallel()
	svc := &stubMQTTReloadService{took: 750 * time.Millisecond}
	rec := &captureRecorder{}
	req := httptest.NewRequest(http.MethodPost, "/admin/mqtt/reload", http.NoBody)
	w := httptest.NewRecorder()

	MQTTReload(svc, rec).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	if len(rec.entries) != 1 {
		t.Fatalf("expected 1 audit entry, got %d", len(rec.entries))
	}
	e := rec.entries[0]
	if e.Action != audit.ActionConfigSectionUpdate {
		t.Errorf("expected action=%q, got %q", audit.ActionConfigSectionUpdate, e.Action)
	}
	if e.Note != "mqtt.reload" {
		t.Errorf("expected note=%q, got %q", "mqtt.reload", e.Note)
	}
}

// TestMQTTReload_ServiceError_Returns503 verifies that when Reload returns an
// error the handler responds with HTTP 503 and a problem+json body that
// includes the error message.
func TestMQTTReload_ServiceError_Returns503(t *testing.T) {
	t.Parallel()
	svc := &stubMQTTReloadService{err: errors.New("broker unreachable")}
	req := httptest.NewRequest(http.MethodPost, "/admin/mqtt/reload", http.NoBody)
	w := httptest.NewRecorder()

	MQTTReload(svc, audit.NoopRecorder()).ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d body=%s", w.Code, w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/problem+json" {
		t.Errorf("expected Content-Type application/problem+json, got %q", ct)
	}
	if !strings.Contains(w.Body.String(), "broker unreachable") {
		t.Errorf("expected error message in body, got %s", w.Body.String())
	}
}

// TestMQTTReload_NilAuditor_NoPanic verifies that passing a nil Recorder does
// not cause a panic and still returns HTTP 200 on success.
func TestMQTTReload_NilAuditor_NoPanic(t *testing.T) {
	t.Parallel()
	svc := &stubMQTTReloadService{took: 10 * time.Millisecond}
	req := httptest.NewRequest(http.MethodPost, "/admin/mqtt/reload", http.NoBody)
	w := httptest.NewRecorder()

	// A nil recorder must not panic; the handler guards with rec != nil.
	MQTTReload(svc, nil).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 with nil auditor, got %d body=%s", w.Code, w.Body.String())
	}
}
