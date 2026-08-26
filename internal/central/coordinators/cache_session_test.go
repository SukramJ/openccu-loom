// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package coordinators

import (
	"context"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/client/reliability"
	"github.com/SukramJ/openccu-loom/internal/store/session"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// --- session recorder + incident recorder wiring ----------------

// TestCacheSetSessionRecorderPassthrough verifies that RecordSession
// forwards to the wired recorder and that a subsequent Get returns the
// response. When no recorder is wired the call is a no-op.
func TestCacheSetSessionRecorderPassthrough(t *testing.T) {
	t.Parallel()

	rec := session.New(session.Config{Active: true})
	c := NewCacheCoordinator()

	// Without a recorder: no panic, nothing stored.
	c.RecordSession(session.RPCTypeXML, "getDeviceDescription", []string{"addr"}, nil)

	// Wire the recorder; record a request+response pair.
	c.SetSessionRecorder(rec)

	const method = "getParamset"
	params := []string{"A:1", "VALUES"}
	response := map[string]any{"LEVEL": 0.5}
	c.RecordSession(session.RPCTypeXML, method, params, response)

	got, ok := rec.Get(session.RPCTypeXML, method, params)
	if !ok {
		t.Fatal("Get after RecordSession: expected entry to exist")
	}
	m, ok := got.(map[string]any)
	if !ok {
		t.Fatalf("response type = %T, want map[string]any", got)
	}
	if m["LEVEL"] != 0.5 {
		t.Fatalf("LEVEL = %v, want 0.5", m["LEVEL"])
	}
}

// TestCacheSetSessionRecorderChaining verifies that SetSessionRecorder
// returns the coordinator so calls can be chained with other Set*
// methods in a builder style.
func TestCacheSetSessionRecorderChaining(t *testing.T) {
	t.Parallel()

	rec := session.New(session.Config{Active: true})
	c := NewCacheCoordinator().SetSessionRecorder(rec)
	if c == nil {
		t.Fatal("SetSessionRecorder must return non-nil receiver")
	}
	if c.sessionRecorder != rec {
		t.Fatal("sessionRecorder field not set correctly")
	}
}

// TestCacheRecordSessionNilRecorderIsNoop verifies that RecordSession
// does not panic and has no side-effects when no recorder has been
// wired.
func TestCacheRecordSessionNilRecorderIsNoop(t *testing.T) {
	t.Parallel()

	c := NewCacheCoordinator()
	// Must not panic.
	c.RecordSession(session.RPCTypeJSON, "ping", nil, nil)
}

// stubIncidentRecorder is a simple in-memory stub satisfying
// reliability.IncidentRecorder for testing.
type stubIncidentRecorder struct {
	incidents []reliability.IncidentRecord
}

func (s *stubIncidentRecorder) RecordIncident(_ context.Context, inc reliability.IncidentRecord) error {
	s.incidents = append(s.incidents, inc)
	return nil
}

// TestCacheSetIncidentRecorderWireAndRead verifies that
// SetIncidentRecorder stores the recorder and GetIncidentRecorder
// returns the same value.
func TestCacheSetIncidentRecorderWireAndRead(t *testing.T) {
	t.Parallel()

	stub := &stubIncidentRecorder{}
	c := NewCacheCoordinator().SetIncidentRecorder(stub)

	got := c.GetIncidentRecorder()
	if got != stub {
		t.Fatalf("GetIncidentRecorder() = %v, want stub", got)
	}

	// Callers can use the returned recorder directly.
	_ = got.RecordIncident(context.Background(), reliability.IncidentRecord{
		CentralName: "central1",
		InterfaceID: "HmIP-RF",
		Type:        hmenum.IncidentTypePingPongMismatch,
		Severity:    hmenum.IncidentSeverityWarning,
		Message:     "test-incident",
	})
	if len(stub.incidents) != 1 {
		t.Fatalf("incidents count = %d, want 1", len(stub.incidents))
	}
}

// TestCacheGetIncidentRecorderNilWhenUnwired verifies that
// GetIncidentRecorder returns nil when no recorder has been wired —
// the callers must nil-check before using it.
func TestCacheGetIncidentRecorderNilWhenUnwired(t *testing.T) {
	t.Parallel()

	c := NewCacheCoordinator()
	if got := c.GetIncidentRecorder(); got != nil {
		t.Fatalf("GetIncidentRecorder() = %v, want nil for unwired coordinator", got)
	}
}
