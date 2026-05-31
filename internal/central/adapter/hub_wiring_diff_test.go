// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/client/transport/jsonrpc"
	"github.com/SukramJ/openccu-loom/internal/model/hub"
)

// staleProgServer returns a server that serves two programs on the first
// Program.getAll call and only one on subsequent calls, simulating a
// CCU-side deletion.
func staleProgServer(t *testing.T) *httptest.Server {
	t.Helper()
	calls := 0
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var req map[string]any
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		method, _ := req["method"].(string)
		var result any
		switch method {
		case "Program.getAll":
			if calls == 1 {
				result = []map[string]any{
					{"id": "p1", "name": "Alpha", "isActive": false},
					{"id": "p2", "name": "Beta", "isActive": false},
				}
			} else {
				// p2 deleted on CCU side.
				result = []map[string]any{
					{"id": "p1", "name": "Alpha", "isActive": false},
				}
			}
		default:
			result = nil
		}
		resp, _ := json.Marshal(map[string]any{"result": result, "error": nil})
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(resp)
	}))
}

// TestLoadProgramsDiffPassRemovesStalePrograms verifies that calling
// loadPrograms twice removes a program that disappeared from the CCU list.
func TestLoadProgramsDiffPassRemovesStalePrograms(t *testing.T) {
	t.Parallel()

	srv := staleProgServer(t)
	defer srv.Close()

	jc, err := jsonrpc.New(jsonrpc.Config{Endpoint: srv.URL})
	if err != nil {
		t.Fatalf("jsonrpc.New: %v", err)
	}

	h := hub.NewHub("test-central")
	writer := &noopProgramWriter{}

	// First load: both p1 and p2 appear.
	if err := loadPrograms(context.Background(), jc, h, writer); err != nil {
		t.Fatalf("first loadPrograms: %v", err)
	}
	if got := len(h.Programs()); got != 2 {
		t.Fatalf("want 2 programs after first load, got %d", got)
	}

	// Second load: only p1 survives; p2 should be removed by the diff pass.
	if err := loadPrograms(context.Background(), jc, h, writer); err != nil {
		t.Fatalf("second loadPrograms: %v", err)
	}
	progs := h.Programs()
	if len(progs) != 1 {
		t.Fatalf("want 1 program after diff pass, got %d: %v", len(progs), programIDs(progs))
	}
	if progs[0].ID != "p1" {
		t.Fatalf("want p1 to survive, got %q", progs[0].ID)
	}
}

func programIDs(progs []*hub.Program) []string {
	out := make([]string, len(progs))
	for i, p := range progs {
		out[i] = p.ID
	}
	return out
}

// noopProgramWriter satisfies hub.ProgramWriter without side effects.
type noopProgramWriter struct{}

func (n *noopProgramWriter) ExecuteProgram(_ context.Context, _ string) error { return nil }
func (n *noopProgramWriter) SetProgramEnabled(_ context.Context, _ string, _ bool) error {
	return nil
}
