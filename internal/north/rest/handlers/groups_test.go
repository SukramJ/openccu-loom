// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/client/backends"
	"github.com/SukramJ/openccu-loom/pkg/hmerr"
)

// fakeGroupsReader implements GroupsReader for tests: it records the
// requested central and returns a configurable entries slice / error.
type fakeGroupsReader struct {
	lastCentral string
	entries     []GroupCentralEntry
	err         error
}

func (f *fakeGroupsReader) List(_ context.Context, central string) ([]GroupCentralEntry, error) {
	f.lastCentral = central
	if f.err != nil {
		return nil, f.err
	}
	return f.entries, nil
}

func TestListGroups_NilReader_Returns503(t *testing.T) {
	t.Parallel()
	w := httptest.NewRecorder()
	ListGroups(nil).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/groups", http.NoBody))
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestListGroups_HappyPath_Returns200(t *testing.T) {
	t.Parallel()
	reader := &fakeGroupsReader{entries: []GroupCentralEntry{
		{
			Central: "ccu-01",
			Groups: []GroupEntry{
				{
					ID:                    3,
					Name:                  "Kitchen",
					GroupDeviceName:       "Kitchen Group Device",
					ForbidSingleOperation: true,
					TypeID:                "HEATING",
					TypeLabel:             "group.type.heating",
					Members: []GroupMemberEntry{
						{Address: "000AAA:1", TypeID: "THERMOSTAT"},
					},
				},
			},
		},
	}}
	w := httptest.NewRecorder()
	ListGroups(reader).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/groups", http.NoBody))
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}

	var got struct {
		Entries []GroupCentralEntry `json:"entries"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got.Entries) != 1 {
		t.Fatalf("entries len=%d, want 1", len(got.Entries))
	}
	entry := got.Entries[0]
	if entry.Central != "ccu-01" {
		t.Errorf("Central = %q, want ccu-01", entry.Central)
	}
	if len(entry.Groups) != 1 {
		t.Fatalf("Groups len=%d, want 1", len(entry.Groups))
	}
	g := entry.Groups[0]
	if g.ID != 3 || g.Name != "Kitchen" || g.TypeID != "HEATING" {
		t.Errorf("group round-trip failed: %+v", g)
	}
	if !g.ForbidSingleOperation {
		t.Error("ForbidSingleOperation = false, want true")
	}
	if len(g.Members) != 1 || g.Members[0].Address != "000AAA:1" {
		t.Errorf("members round-trip failed: %+v", g.Members)
	}
}

func TestListGroups_EmptyEntries_Returns200WithEmptyArray(t *testing.T) {
	t.Parallel()
	reader := &fakeGroupsReader{entries: nil}
	w := httptest.NewRecorder()
	ListGroups(reader).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/groups", http.NoBody))
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var got struct {
		Entries []GroupCentralEntry `json:"entries"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Entries == nil {
		t.Fatal("entries must be [] not null")
	}
	if len(got.Entries) != 0 {
		t.Fatalf("entries len=%d, want 0", len(got.Entries))
	}
}

func TestListGroups_ForwardsCentralQueryParam(t *testing.T) {
	t.Parallel()
	reader := &fakeGroupsReader{}
	w := httptest.NewRecorder()
	ListGroups(reader).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/groups?central=ccu-02", http.NoBody))
	if reader.lastCentral != "ccu-02" {
		t.Fatalf("central query param not forwarded, got %q", reader.lastCentral)
	}
}

func TestListGroups_UnknownCentral_Returns404(t *testing.T) {
	t.Parallel()
	reader := &fakeGroupsReader{err: hmerr.ErrUnknownCentral}
	w := httptest.NewRecorder()
	ListGroups(reader).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/groups?central=nope", http.NoBody))
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestListGroups_Unsupported_Returns404(t *testing.T) {
	t.Parallel()
	reader := &fakeGroupsReader{err: backends.ErrUnsupported}
	w := httptest.NewRecorder()
	ListGroups(reader).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/groups?central=ccu-01", http.NoBody))
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestListGroups_GenericError_Returns502(t *testing.T) {
	t.Parallel()
	reader := &fakeGroupsReader{err: hmerr.ErrNoConnection}
	w := httptest.NewRecorder()
	ListGroups(reader).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/groups", http.NoBody))
	if w.Code != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d body=%s", w.Code, w.Body.String())
	}
}
