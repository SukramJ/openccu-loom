// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// --- SystemStatusBuffer tests ---

func TestNewSystemStatusBuffer_ClampsZeroToOne(t *testing.T) {
	t.Parallel()
	b := NewSystemStatusBuffer(0)
	// Should not panic; ring has capacity 1.
	b.append(SystemStatusEntry{Central: "test", Component: "c1", Healthy: true})
	entries := b.SystemStatusEntries()
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
}

func TestSystemStatusBuffer_AppendAndRead(t *testing.T) {
	t.Parallel()
	b := NewSystemStatusBuffer(5)
	now := time.Now()
	b.append(SystemStatusEntry{Central: "ccu1", Component: "conn", Healthy: true, EventAt: now})
	b.append(SystemStatusEntry{Central: "ccu1", Component: "rpc", Healthy: false, EventAt: now.Add(time.Second)})

	entries := b.SystemStatusEntries()
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
	// Oldest first.
	if entries[0].Component != "conn" {
		t.Errorf("expected first entry component=conn, got %q", entries[0].Component)
	}
	if entries[1].Component != "rpc" {
		t.Errorf("expected second entry component=rpc, got %q", entries[1].Component)
	}
}

func TestSystemStatusBuffer_Ring_EvictsOldest(t *testing.T) {
	t.Parallel()
	b := NewSystemStatusBuffer(3)
	for i := 0; i < 5; i++ {
		b.append(SystemStatusEntry{Component: string(rune('a' + i))})
	}
	entries := b.SystemStatusEntries()
	if len(entries) != 3 {
		t.Fatalf("expected 3 entries (ring capacity), got %d", len(entries))
	}
	// Should be last 3: c, d, e
	if entries[0].Component != "c" {
		t.Errorf("expected oldest-retained component=c, got %q", entries[0].Component)
	}
	if entries[2].Component != "e" {
		t.Errorf("expected newest component=e, got %q", entries[2].Component)
	}
}

func TestSystemStatusBuffer_Empty_ReturnsNil(t *testing.T) {
	t.Parallel()
	b := NewSystemStatusBuffer(10)
	entries := b.SystemStatusEntries()
	if entries != nil {
		t.Fatalf("expected nil for empty buffer, got %+v", entries)
	}
}

// --- ListSystemStatus handler tests ---

func TestListSystemStatus_NilReader_ReturnsEmptyEvents(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/system/status", http.NoBody)
	w := httptest.NewRecorder()
	ListSystemStatus(nil).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var resp SystemStatusResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(resp.Events) != 0 {
		t.Fatalf("expected 0 events for nil reader, got %d", len(resp.Events))
	}
}

func TestListSystemStatus_WithBuffer_ReturnsEvents(t *testing.T) {
	t.Parallel()
	b := NewSystemStatusBuffer(10)
	b.append(SystemStatusEntry{Central: "ccu1", Component: "xmlrpc", Healthy: true})
	b.append(SystemStatusEntry{Central: "ccu1", Component: "binrpc", Healthy: false, Reason: "timeout"})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/system/status", http.NoBody)
	w := httptest.NewRecorder()
	ListSystemStatus(b).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var resp SystemStatusResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(resp.Events) != 2 {
		t.Fatalf("expected 2 events, got %d: %+v", len(resp.Events), resp.Events)
	}
	if resp.Events[0].Component != "xmlrpc" {
		t.Errorf("expected first event component=xmlrpc, got %q", resp.Events[0].Component)
	}
}

func TestListSystemStatus_EmptyBuffer_ReturnsEmptyNotNull(t *testing.T) {
	t.Parallel()
	b := NewSystemStatusBuffer(10)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/system/status", http.NoBody)
	w := httptest.NewRecorder()
	ListSystemStatus(b).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	// events must be [] not null.
	body := w.Body.String()
	if body == "" {
		t.Fatal("empty body")
	}
	var resp SystemStatusResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Events == nil {
		t.Error("events must not be null (must be []) for empty buffer")
	}
}

// --- SystemStatusBuffer.Subscribe with nil registry ---

func TestSystemStatusBuffer_Subscribe_NilRegistry(t *testing.T) {
	t.Parallel()
	b := NewSystemStatusBuffer(5)
	stop := b.Subscribe(nil)
	// Must not panic; stop is a no-op.
	stop()
}
