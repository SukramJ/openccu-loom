// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/audit"
	"github.com/SukramJ/openccu-loom/internal/reqctx"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// recordingAuditRecorder captures the entries a domain writes so a test
// can assert on the row rather than on the call.
type recordingAuditRecorder struct {
	entries []audit.Entry
}

func (r *recordingAuditRecorder) Record(e audit.Entry) { r.entries = append(r.entries, e) }

func (r *recordingAuditRecorder) List(limit int) []audit.Entry {
	if limit <= 0 || limit > len(r.entries) {
		return r.entries
	}
	return r.entries[:limit]
}

// TestPutParamsetRecordsTheCallingSurface pins that the operation label a
// caller stamps on the context reaches the change-log row.
//
// Every north-bound surface funnels into the same domain method, so
// without the label the log cannot tell a paramset an operator edited in
// the UI from one an assistant wrote over MCP — the first question asked
// of an unexplained configuration change.
func TestPutParamsetRecordsTheCallingSurface(t *testing.T) {
	t.Parallel()

	rec := &recordingAuditRecorder{}
	p := buildParamsetBoost10Fixture(t).SetAuditRecorder(rec)

	ctx := reqctx.WithOperation(t.Context(), "mcp:paramset-write")
	if err := p.PutParamset(ctx, "DEV021", hmenum.ParamsetKeyValues,
		map[string]any{"SET_POINT_TEMPERATURE": 21.0}); err != nil {
		t.Fatalf("PutParamset: %v", err)
	}

	if len(rec.entries) != 1 {
		t.Fatalf("expected exactly one audit entry, got %d", len(rec.entries))
	}
	got := rec.entries[0]
	if got.Action != audit.ActionParamsetWrite {
		t.Errorf("action = %q, want %q", got.Action, audit.ActionParamsetWrite)
	}
	if got.Note != "mcp:paramset-write" {
		t.Errorf("note = %q, want the calling surface %q", got.Note, "mcp:paramset-write")
	}
}

// A context that carries no request scope (a scheduled job, a test) must
// still produce a row — the label is context, not a precondition.
func TestPutParamsetRecordsWithoutARequestScope(t *testing.T) {
	t.Parallel()

	rec := &recordingAuditRecorder{}
	p := buildParamsetBoost10Fixture(t).SetAuditRecorder(rec)

	if err := p.PutParamset(t.Context(), "DEV021", hmenum.ParamsetKeyValues,
		map[string]any{"SET_POINT_TEMPERATURE": 21.0}); err != nil {
		t.Fatalf("PutParamset: %v", err)
	}

	if len(rec.entries) != 1 {
		t.Fatalf("expected exactly one audit entry, got %d", len(rec.entries))
	}
	if got := rec.entries[0].Note; got != "" {
		t.Errorf("note = %q, want empty when the context names no surface", got)
	}
}
