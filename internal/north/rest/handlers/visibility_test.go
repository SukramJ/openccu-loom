// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package handlers_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/audit"
	"github.com/SukramJ/openccu-loom/internal/north/rest/handlers"
	"github.com/SukramJ/openccu-loom/internal/store/sqlite"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// fakeUnIgnoreStore is an in-memory drop-in for
// [handlers.VisibilityUnIgnoreStore].
type fakeUnIgnoreStore struct {
	mu       sync.Mutex
	rows     map[string][]sqlite.UnIgnoreEntry
	listErr  error
	replaced int
}

func newFakeUnIgnoreStore() *fakeUnIgnoreStore {
	return &fakeUnIgnoreStore{rows: make(map[string][]sqlite.UnIgnoreEntry)}
}

func (f *fakeUnIgnoreStore) List(_ context.Context, centralName string) ([]sqlite.UnIgnoreEntry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.listErr != nil {
		return nil, f.listErr
	}
	out := make([]sqlite.UnIgnoreEntry, len(f.rows[centralName]))
	copy(out, f.rows[centralName])
	return out, nil
}

func (f *fakeUnIgnoreStore) Patterns(_ context.Context, centralName string) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, 0, len(f.rows[centralName]))
	for _, e := range f.rows[centralName] {
		out = append(out, e.Pattern)
	}
	return out, nil
}

func (f *fakeUnIgnoreStore) Replace(_ context.Context, centralName string, patterns []string, updatedBy string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	entries := make([]sqlite.UnIgnoreEntry, 0, len(patterns))
	now := time.Now().UTC()
	for _, p := range patterns {
		entries = append(entries, sqlite.UnIgnoreEntry{
			Pattern:   p,
			UpdatedAt: now,
			UpdatedBy: updatedBy,
		})
	}
	f.rows[centralName] = entries
	f.replaced++
	return nil
}

type fakeCentralLister struct{ names []string }

func (f *fakeCentralLister) Names() []string {
	out := make([]string, len(f.names))
	copy(out, f.names)
	return out
}

type fakeCandidateProvider struct {
	values map[string][]string
	master map[string][]string
}

func (f *fakeCandidateProvider) UnIgnoreCandidates(central string, paramset hmenum.ParamsetKey) []string {
	if paramset == hmenum.ParamsetKeyMaster {
		return append([]string(nil), f.master[central]...)
	}
	return append([]string(nil), f.values[central]...)
}

type fakeLoader struct {
	mu       sync.Mutex
	last     []string
	affected int
}

func (f *fakeLoader) LoadUnIgnore(_ string, patterns []string) (affected int, parseErrors []string, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.last = append([]string(nil), patterns...)
	return f.affected, nil, nil
}

// TestVisibilityUnIgnoreListReturnsPerCentralPatterns asserts that
// GET /api/v1/visibility/unignore aggregates every registered central
// and reflects the per-central pattern set.
func TestVisibilityUnIgnoreListReturnsPerCentralPatterns(t *testing.T) {
	t.Parallel()
	store := newFakeUnIgnoreStore()
	_ = store.Replace(context.Background(), "ccu-01", []string{"*:*:RSSI_PEER"}, "alice")
	_ = store.Replace(context.Background(), "ccu-02", []string{"LOW_BAT"}, "bob")
	lister := &fakeCentralLister{names: []string{"ccu-01", "ccu-02"}}

	h := handlers.ListVisibilityUnIgnore(lister, store)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/visibility/unignore", http.NoBody)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var body handlers.UnIgnoreListResponseDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Centrals) != 2 {
		t.Fatalf("centrals = %d, want 2", len(body.Centrals))
	}
	if body.Centrals[0].CentralName != "ccu-01" {
		t.Errorf("centrals[0] = %q, want ccu-01", body.Centrals[0].CentralName)
	}
	if len(body.Centrals[0].Patterns) != 1 || body.Centrals[0].Patterns[0].Pattern != "*:*:RSSI_PEER" {
		t.Errorf("ccu-01 patterns = %v, want [*:*:RSSI_PEER]", body.Centrals[0].Patterns)
	}
	if body.Centrals[1].CentralName != "ccu-02" || body.Centrals[1].Patterns[0].UpdatedBy != "bob" {
		t.Errorf("ccu-02 entry mismatch: %+v", body.Centrals[1])
	}
}

// TestVisibilityUnIgnoreUpdateApplies asserts that PUT validates, dedupes,
// persists, calls the loader, and emits an audit entry for the diff.
func TestVisibilityUnIgnoreUpdateApplies(t *testing.T) {
	t.Parallel()
	store := newFakeUnIgnoreStore()
	_ = store.Replace(context.Background(), "ccu-01", []string{"OLD_PATTERN"}, "alice")
	loader := &fakeLoader{affected: 42}
	auditBuf := audit.NewBuffer(100)

	h := handlers.UpdateVisibilityUnIgnore(store, loader, auditBuf)

	body := `{
        "central_name": "ccu-01",
        "patterns": [
            "LOW_BAT",
            "  LOW_BAT  ",
            "RSSI_PEER",
            "",
            ":bogus"
        ]
    }`
	req := httptest.NewRequest(http.MethodPut, "/api/v1/visibility/unignore", strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var resp handlers.UnIgnoreUpdateResponseDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.AppliedCount != 2 {
		t.Errorf("applied_count = %d, want 2 (LOW_BAT, RSSI_PEER)", resp.AppliedCount)
	}
	if resp.AffectedDevices != 42 {
		t.Errorf("affected_devices = %d, want 42", resp.AffectedDevices)
	}
	if len(resp.ParseErrors) == 0 {
		t.Errorf("expected at least one parse_error for ':bogus', got none")
	}
	// Loader received the well-formed subset, dedup'd + sorted.
	if len(loader.last) != 2 || loader.last[0] != "LOW_BAT" || loader.last[1] != "RSSI_PEER" {
		t.Errorf("loader received %v, want [LOW_BAT RSSI_PEER]", loader.last)
	}
	// Store has the new set.
	got, _ := store.Patterns(context.Background(), "ccu-01")
	if len(got) != 2 || got[0] != "LOW_BAT" || got[1] != "RSSI_PEER" {
		t.Errorf("store after PUT = %v, want [LOW_BAT RSSI_PEER]", got)
	}
	// Audit emitted with diff: added [LOW_BAT, RSSI_PEER], removed [OLD_PATTERN].
	entries := auditBuf.List(10)
	if len(entries) != 1 || entries[0].Action != audit.ActionUnIgnoreUpdate {
		t.Fatalf("audit entries = %+v, want 1 ActionUnIgnoreUpdate", entries)
	}
	gotChanges := map[string]any{}
	for _, ch := range entries[0].Changes {
		gotChanges[ch.Parameter] = ch.After
	}
	if gotChanges["LOW_BAT"] != "active" || gotChanges["RSSI_PEER"] != "active" {
		t.Errorf("audit added missing LOW_BAT/RSSI_PEER: %+v", entries[0].Changes)
	}
	if _, ok := gotChanges["OLD_PATTERN"]; !ok {
		t.Errorf("audit missing OLD_PATTERN removed entry: %+v", entries[0].Changes)
	}
}

// TestVisibilityUnIgnoreUpdateRejectsEmptyCentral verifies that PUT
// returns 400 when the request body lacks `central_name`.
func TestVisibilityUnIgnoreUpdateRejectsEmptyCentral(t *testing.T) {
	t.Parallel()
	store := newFakeUnIgnoreStore()
	loader := &fakeLoader{}
	h := handlers.UpdateVisibilityUnIgnore(store, loader, nil)

	body := `{"patterns": ["LOW_BAT"]}`
	req := httptest.NewRequest(http.MethodPut, "/api/v1/visibility/unignore", strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

// TestVisibilityUnIgnoreCandidatesAggregates verifies that the candidate
// endpoint unions every central's candidate set and respects include_master.
func TestVisibilityUnIgnoreCandidatesAggregates(t *testing.T) {
	t.Parallel()
	lister := &fakeCentralLister{names: []string{"ccu-01", "ccu-02"}}
	provider := &fakeCandidateProvider{
		values: map[string][]string{
			"ccu-01": {"LOW_BAT", "RSSI_PEER"},
			"ccu-02": {"RSSI_PEER", "ERROR"},
		},
		master: map[string][]string{
			"ccu-01": {"TEMPERATURE_OFFSET"},
		},
	}

	t.Run("values_only", func(t *testing.T) {
		t.Parallel()
		h := handlers.ListVisibilityUnIgnoreCandidates(lister, provider)
		req := httptest.NewRequest(http.MethodGet, "/api/v1/visibility/unignore/candidates", http.NoBody)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d", rec.Code)
		}
		var body handlers.UnIgnoreCandidateListDTO
		_ = json.Unmarshal(rec.Body.Bytes(), &body)
		if len(body.Candidates) != 3 {
			t.Errorf("candidates = %v, want 3 unique", body.Candidates)
		}
		if body.IncludeMaster {
			t.Errorf("include_master = true, want false (default)")
		}
	})

	t.Run("with_master", func(t *testing.T) {
		t.Parallel()
		h := handlers.ListVisibilityUnIgnoreCandidates(lister, provider)
		req := httptest.NewRequest(http.MethodGet, "/api/v1/visibility/unignore/candidates?include_master=true", http.NoBody)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)

		var body handlers.UnIgnoreCandidateListDTO
		_ = json.Unmarshal(rec.Body.Bytes(), &body)
		if len(body.Candidates) != 4 {
			t.Errorf("candidates = %v, want 4 (3 values + TEMPERATURE_OFFSET)", body.Candidates)
		}
		if !body.IncludeMaster {
			t.Errorf("include_master = false, want true")
		}
	})
}
