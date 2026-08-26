// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/audit"
	sqlitestore "github.com/SukramJ/openccu-loom/internal/store/sqlite"
	"github.com/SukramJ/openccu-loom/pkg/hmapi"
)

// newAreaFixture returns a real sqlite-backed AreaStore so the tests
// exercise the actual store behaviour (in particular the room-move
// invariant enforced by the (central, room) primary key), not a stub
// that could paper over it.
func newAreaFixture(t *testing.T) *sqlitestore.AreaStore {
	t.Helper()
	db := openMigratedTestDB(t, "areas.db")
	return sqlitestore.NewAreaStore(db)
}

func areaRequestBody(t *testing.T, area hmapi.Area) *bytes.Reader {
	t.Helper()
	b, err := json.Marshal(area)
	if err != nil {
		t.Fatalf("marshal request body: %v", err)
	}
	return bytes.NewReader(b)
}

// --- ListAreas ---

func TestListAreas_Empty(t *testing.T) {
	t.Parallel()
	svc := newAreaFixture(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/areas", http.NoBody)
	w := httptest.NewRecorder()
	ListAreas(svc).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", w.Code, w.Body.String())
	}
	var body []hmapi.Area
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(body) != 0 {
		t.Fatalf("areas = %+v, want empty", body)
	}
}

func TestListAreas_ReturnsSeededAreasWithRooms(t *testing.T) {
	t.Parallel()
	svc := newAreaFixture(t)
	ctx := context.Background()
	if err := svc.Upsert(ctx, sqlitestore.AreaRow{ID: "floor-1", Name: "First Floor", Position: 1, CreatedAtMS: 1, UpdatedAtMS: 1}); err != nil {
		t.Fatalf("seed area: %v", err)
	}
	if err := svc.ReplaceRooms(ctx, "floor-1", []sqlitestore.RoomAreaRow{
		{CentralName: "ccu1", RoomName: "Kitchen", AreaID: "floor-1"},
		{CentralName: "ccu1", RoomName: "Hallway", AreaID: "floor-1"},
	}); err != nil {
		t.Fatalf("seed rooms: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/areas", http.NoBody)
	w := httptest.NewRecorder()
	ListAreas(svc).ServeHTTP(w, req)

	var body []hmapi.Area
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(body) != 1 {
		t.Fatalf("areas = %+v, want 1", body)
	}
	if len(body[0].Rooms) != 2 {
		t.Errorf("rooms = %+v, want 2", body[0].Rooms)
	}
}

// --- CreateArea ---

func TestCreateArea_HappyPath_Returns201AndRecordsAudit(t *testing.T) {
	t.Parallel()
	svc := newAreaFixture(t)
	rec := &captureRecorder{}

	body := areaRequestBody(t, hmapi.Area{Name: "Garden Shed", Position: 3})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/areas", body)
	w := httptest.NewRecorder()
	CreateArea(svc, rec).ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201, body=%s", w.Code, w.Body.String())
	}
	var created hmapi.Area
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if created.ID == "" {
		t.Error("created area has no server-generated id")
	}
	if created.Name != "Garden Shed" || created.Position != 3 {
		t.Errorf("created = %+v, want name=Garden Shed position=3", created)
	}
	if _, ok, err := svc.Get(context.Background(), created.ID); err != nil || !ok {
		t.Fatalf("area not persisted: ok=%v err=%v", ok, err)
	}
	if len(rec.entries) != 1 || rec.entries[0].Action != audit.ActionAreaChange {
		t.Fatalf("audit entries = %+v, want one area_change", rec.entries)
	}
}

func TestCreateArea_EmptyName_Returns422(t *testing.T) {
	t.Parallel()
	svc := newAreaFixture(t)

	body := areaRequestBody(t, hmapi.Area{Name: ""})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/areas", body)
	w := httptest.NewRecorder()
	CreateArea(svc, nil).ServeHTTP(w, req)

	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422, body=%s", w.Code, w.Body.String())
	}
}

func TestCreateArea_MalformedJSON_Returns400(t *testing.T) {
	t.Parallel()
	svc := newAreaFixture(t)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/areas", strings.NewReader("{not-json"))
	w := httptest.NewRecorder()
	CreateArea(svc, nil).ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body=%s", w.Code, w.Body.String())
	}
}

// --- PutArea ---

func TestPutArea_UnknownID_Returns404(t *testing.T) {
	t.Parallel()
	svc := newAreaFixture(t)

	body := areaRequestBody(t, hmapi.Area{Name: "x"})
	req := httptest.NewRequest(http.MethodPut, "/api/v1/areas/missing", body)
	req = withChiParam(req, "id", "missing")
	w := httptest.NewRecorder()
	PutArea(svc, nil).ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404, body=%s", w.Code, w.Body.String())
	}
}

func TestPutArea_HappyPath_Returns204AndPreservesCreatedAt(t *testing.T) {
	t.Parallel()
	svc := newAreaFixture(t)
	ctx := context.Background()
	if err := svc.Upsert(ctx, sqlitestore.AreaRow{ID: "floor-1", Name: "First Floor", Position: 1, CreatedAtMS: 1000, UpdatedAtMS: 1000}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	rec := &captureRecorder{}

	body := areaRequestBody(t, hmapi.Area{Name: "Ground Floor", Position: 2})
	req := httptest.NewRequest(http.MethodPut, "/api/v1/areas/floor-1", body)
	req = withChiParam(req, "id", "floor-1")
	w := httptest.NewRecorder()
	PutArea(svc, rec).ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204, body=%s", w.Code, w.Body.String())
	}
	row, ok, err := svc.Get(ctx, "floor-1")
	if err != nil || !ok {
		t.Fatalf("get after put: ok=%v err=%v", ok, err)
	}
	if row.Name != "Ground Floor" || row.Position != 2 {
		t.Errorf("row = %+v, want name=Ground Floor position=2", row)
	}
	if row.CreatedAtMS != 1000 {
		t.Errorf("CreatedAtMS = %d, want 1000 (preserved)", row.CreatedAtMS)
	}
	if len(rec.entries) != 1 || rec.entries[0].Action != audit.ActionAreaChange {
		t.Fatalf("audit entries = %+v, want one area_change", rec.entries)
	}
}

func TestPutArea_EmptyName_Returns422(t *testing.T) {
	t.Parallel()
	svc := newAreaFixture(t)
	if err := svc.Upsert(context.Background(), sqlitestore.AreaRow{ID: "floor-1", Name: "First Floor", CreatedAtMS: 1, UpdatedAtMS: 1}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	body := areaRequestBody(t, hmapi.Area{Name: ""})
	req := httptest.NewRequest(http.MethodPut, "/api/v1/areas/floor-1", body)
	req = withChiParam(req, "id", "floor-1")
	w := httptest.NewRecorder()
	PutArea(svc, nil).ServeHTTP(w, req)

	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422, body=%s", w.Code, w.Body.String())
	}
}

// --- DeleteArea ---

func TestDeleteArea_UnknownID_Returns404(t *testing.T) {
	t.Parallel()
	svc := newAreaFixture(t)

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/areas/missing", http.NoBody)
	req = withChiParam(req, "id", "missing")
	w := httptest.NewRecorder()
	DeleteArea(svc, nil).ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404, body=%s", w.Code, w.Body.String())
	}
}

func TestDeleteArea_HappyPath_Returns204AndCascadesAssignments(t *testing.T) {
	t.Parallel()
	svc := newAreaFixture(t)
	ctx := context.Background()
	if err := svc.Upsert(ctx, sqlitestore.AreaRow{ID: "floor-1", Name: "First Floor", CreatedAtMS: 1, UpdatedAtMS: 1}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := svc.ReplaceRooms(ctx, "floor-1", []sqlitestore.RoomAreaRow{
		{CentralName: "ccu1", RoomName: "Kitchen", AreaID: "floor-1"},
	}); err != nil {
		t.Fatalf("seed rooms: %v", err)
	}
	rec := &captureRecorder{}

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/areas/floor-1", http.NoBody)
	req = withChiParam(req, "id", "floor-1")
	w := httptest.NewRecorder()
	DeleteArea(svc, rec).ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204, body=%s", w.Code, w.Body.String())
	}
	if _, ok, _ := svc.Get(ctx, "floor-1"); ok {
		t.Error("area row still present after delete")
	}
	assignments, err := svc.ListAssignments(ctx)
	if err != nil {
		t.Fatalf("list assignments: %v", err)
	}
	if len(assignments) != 0 {
		t.Errorf("assignments = %+v, want cascaded delete", assignments)
	}
	if len(rec.entries) != 1 || rec.entries[0].Action != audit.ActionAreaChange {
		t.Fatalf("audit entries = %+v, want one area_change", rec.entries)
	}
}

// --- PutAreaRooms ---

func TestPutAreaRooms_UnknownArea_Returns404(t *testing.T) {
	t.Parallel()
	svc := newAreaFixture(t)

	req := httptest.NewRequest(http.MethodPut, "/api/v1/areas/missing/rooms", strings.NewReader(`[]`))
	req = withChiParam(req, "id", "missing")
	w := httptest.NewRecorder()
	PutAreaRooms(svc, nil).ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404, body=%s", w.Code, w.Body.String())
	}
}

func TestPutAreaRooms_EmptyCentralOrRoom_Returns422(t *testing.T) {
	t.Parallel()
	svc := newAreaFixture(t)
	if err := svc.Upsert(context.Background(), sqlitestore.AreaRow{ID: "floor-1", Name: "First Floor", CreatedAtMS: 1, UpdatedAtMS: 1}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	req := httptest.NewRequest(http.MethodPut, "/api/v1/areas/floor-1/rooms",
		strings.NewReader(`[{"central":"","room":"Kitchen"}]`))
	req = withChiParam(req, "id", "floor-1")
	w := httptest.NewRecorder()
	PutAreaRooms(svc, nil).ServeHTTP(w, req)

	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422, body=%s", w.Code, w.Body.String())
	}
}

func TestPutAreaRooms_HappyPath_Returns204AndReplacesFullSet(t *testing.T) {
	t.Parallel()
	svc := newAreaFixture(t)
	ctx := context.Background()
	if err := svc.Upsert(ctx, sqlitestore.AreaRow{ID: "floor-1", Name: "First Floor", CreatedAtMS: 1, UpdatedAtMS: 1}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := svc.ReplaceRooms(ctx, "floor-1", []sqlitestore.RoomAreaRow{
		{CentralName: "ccu1", RoomName: "Hallway", AreaID: "floor-1"},
	}); err != nil {
		t.Fatalf("seed initial rooms: %v", err)
	}
	rec := &captureRecorder{}

	// Replaces the previous set entirely: Hallway drops out, Kitchen +
	// Garage come in.
	req := httptest.NewRequest(http.MethodPut, "/api/v1/areas/floor-1/rooms",
		strings.NewReader(`[{"central":"ccu1","room":"Kitchen"},{"central":"ccu1","room":"Garage"}]`))
	req = withChiParam(req, "id", "floor-1")
	w := httptest.NewRecorder()
	PutAreaRooms(svc, rec).ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204, body=%s", w.Code, w.Body.String())
	}
	assignments, err := svc.ListAssignments(ctx)
	if err != nil {
		t.Fatalf("list assignments: %v", err)
	}
	if len(assignments) != 2 {
		t.Fatalf("assignments = %+v, want exactly [Kitchen, Garage]", assignments)
	}
	for _, a := range assignments {
		if a.RoomName == "Hallway" {
			t.Errorf("Hallway must have been dropped by the full-set replace, got %+v", assignments)
		}
	}
	if len(rec.entries) != 1 || rec.entries[0].Action != audit.ActionAreaChange {
		t.Fatalf("audit entries = %+v, want one area_change", rec.entries)
	}
}

// TestPutAreaRooms_MovesRoomFromAnotherArea pins the one-area-per-room
// contract at the handler level: assigning a room already owned by
// another area via PUT .../rooms moves it rather than duplicating the
// assignment or erroring.
func TestPutAreaRooms_MovesRoomFromAnotherArea(t *testing.T) {
	t.Parallel()
	svc := newAreaFixture(t)
	ctx := context.Background()
	if err := svc.Upsert(ctx, sqlitestore.AreaRow{ID: "floor-1", Name: "First Floor", CreatedAtMS: 1, UpdatedAtMS: 1}); err != nil {
		t.Fatalf("seed floor-1: %v", err)
	}
	if err := svc.Upsert(ctx, sqlitestore.AreaRow{ID: "floor-2", Name: "Second Floor", CreatedAtMS: 1, UpdatedAtMS: 1}); err != nil {
		t.Fatalf("seed floor-2: %v", err)
	}
	if err := svc.ReplaceRooms(ctx, "floor-1", []sqlitestore.RoomAreaRow{
		{CentralName: "ccu1", RoomName: "Kitchen", AreaID: "floor-1"},
	}); err != nil {
		t.Fatalf("seed floor-1 rooms: %v", err)
	}

	// floor-2 claims the same (central, room) pair.
	req := httptest.NewRequest(http.MethodPut, "/api/v1/areas/floor-2/rooms",
		strings.NewReader(`[{"central":"ccu1","room":"Kitchen"}]`))
	req = withChiParam(req, "id", "floor-2")
	w := httptest.NewRecorder()
	PutAreaRooms(svc, nil).ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204, body=%s", w.Code, w.Body.String())
	}
	assignments, err := svc.ListAssignments(ctx)
	if err != nil {
		t.Fatalf("list assignments: %v", err)
	}
	if len(assignments) != 1 {
		t.Fatalf("assignments = %+v, want exactly 1 row (moved, not duplicated)", assignments)
	}
	if assignments[0].AreaID != "floor-2" {
		t.Errorf("AreaID = %q, want floor-2 (the room must have moved)", assignments[0].AreaID)
	}

	// Reflected through ListAreas too: floor-1 now has zero rooms.
	listReq := httptest.NewRequest(http.MethodGet, "/api/v1/areas", http.NoBody)
	listW := httptest.NewRecorder()
	ListAreas(svc).ServeHTTP(listW, listReq)
	var areas []hmapi.Area
	if err := json.Unmarshal(listW.Body.Bytes(), &areas); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, a := range areas {
		if a.ID == "floor-1" && len(a.Rooms) != 0 {
			t.Errorf("floor-1.Rooms = %+v, want empty after the room moved away", a.Rooms)
		}
		if a.ID == "floor-2" && len(a.Rooms) != 1 {
			t.Errorf("floor-2.Rooms = %+v, want exactly 1", a.Rooms)
		}
	}
}
