// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package handlers

import (
	"context"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/SukramJ/openccu-loom/internal/audit"
	"github.com/SukramJ/openccu-loom/internal/north/rest/problem"
	sqlitestore "github.com/SukramJ/openccu-loom/internal/store/sqlite"
	"github.com/SukramJ/openccu-loom/pkg/hmapi"
)

// AreaAdmin is the DI surface the /api/v1/areas endpoints drive: CRUD
// on the area entity plus its room assignments. The sqlite-backed
// store (*sqlitestore.AreaStore) satisfies this interface directly —
// no adapter layer needed, mirroring how the per-user preferences
// store is wired.
type AreaAdmin interface {
	GetAll(ctx context.Context) ([]sqlitestore.AreaRow, error)
	Get(ctx context.Context, id string) (sqlitestore.AreaRow, bool, error)
	Upsert(ctx context.Context, row sqlitestore.AreaRow) error
	Delete(ctx context.Context, id string) error
	ListAssignments(ctx context.Context) ([]sqlitestore.RoomAreaRow, error)
	ReplaceRooms(ctx context.Context, areaID string, refs []sqlitestore.RoomAreaRow) error
}

// Compile-time proof the sqlite store satisfies the handler facade.
var _ AreaAdmin = (*sqlitestore.AreaStore)(nil)

func writeAreaUnavailable(w http.ResponseWriter, r *http.Request) {
	problem.Write(w, http.StatusServiceUnavailable,
		problem.New(problem.TypeServiceUnready, r, "Areas unavailable", ""))
}

func writeAreaNotFound(w http.ResponseWriter, r *http.Request) {
	problem.Write(w, http.StatusNotFound,
		problem.New(problem.TypeNotFound, r, "Unknown area", ""))
}

func areaAudit(rec audit.Recorder, r *http.Request, note string) {
	if rec == nil {
		return
	}
	rec.Record(audit.Entry{
		User:   identityFromCtx(r.Context()),
		Action: audit.ActionAreaChange,
		Note:   note,
	})
}

// apiArea maps a stored area row plus its resolved room set onto the
// wire DTO.
func apiArea(row sqlitestore.AreaRow, rooms []hmapi.AreaRoomRef) hmapi.Area {
	return hmapi.Area{ID: row.ID, Name: row.Name, Position: row.Position, Rooms: rooms}
}

// ListAreas handles GET /areas — every area with its assigned rooms,
// ordered by position then name.
func ListAreas(svc AreaAdmin) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			writeAreaUnavailable(w, r)
			return
		}
		rows, err := svc.GetAll(r.Context())
		if err != nil {
			writeServerError(w, r, http.StatusInternalServerError, problem.TypeInternal, "List areas failed", err)
			return
		}
		assignments, err := svc.ListAssignments(r.Context())
		if err != nil {
			writeServerError(w, r, http.StatusInternalServerError, problem.TypeInternal, "List area rooms failed", err)
			return
		}
		roomsByArea := make(map[string][]hmapi.AreaRoomRef, len(rows))
		for i := range assignments {
			a := &assignments[i]
			roomsByArea[a.AreaID] = append(roomsByArea[a.AreaID], hmapi.AreaRoomRef{Central: a.CentralName, Room: a.RoomName})
		}
		out := make([]hmapi.Area, 0, len(rows))
		for i := range rows {
			out = append(out, apiArea(rows[i], roomsByArea[rows[i].ID]))
		}
		JSON(w, http.StatusOK, out)
	}
}

// CreateArea handles POST /areas — persists a new area with a
// server-generated id.
func CreateArea(svc AreaAdmin, rec audit.Recorder) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			writeAreaUnavailable(w, r)
			return
		}
		var in hmapi.Area
		if err := DecodeJSON(r, &in); err != nil {
			problem.Write(w, DecodeJSONStatus(err),
				problem.New(problem.TypeBadRequest, r, "Invalid request body", err.Error()))
			return
		}
		if in.Name == "" {
			problem.Write(w, http.StatusUnprocessableEntity,
				problem.New(problem.TypeValidation, r, "name is required", ""))
			return
		}
		now := time.Now().UnixMilli()
		row := sqlitestore.AreaRow{
			ID:          uuid.NewString(),
			Name:        in.Name,
			Position:    in.Position,
			CreatedAtMS: now,
			UpdatedAtMS: now,
		}
		if err := svc.Upsert(r.Context(), row); err != nil {
			writeServerError(w, r, http.StatusInternalServerError, problem.TypeInternal, "Create area failed", err)
			return
		}
		areaAudit(rec, r, "create="+row.ID)
		JSON(w, http.StatusCreated, apiArea(row, nil))
	}
}

// PutArea handles PUT /areas/{id} — renames/reorders an existing area.
// created_at is preserved from the existing row.
func PutArea(svc AreaAdmin, rec audit.Recorder) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			writeAreaUnavailable(w, r)
			return
		}
		id := chi.URLParam(r, "id")
		existing, ok, err := svc.Get(r.Context(), id)
		if err != nil {
			writeServerError(w, r, http.StatusInternalServerError, problem.TypeInternal, "Get area failed", err)
			return
		}
		if !ok {
			writeAreaNotFound(w, r)
			return
		}
		var in hmapi.Area
		if err := DecodeJSON(r, &in); err != nil {
			problem.Write(w, DecodeJSONStatus(err),
				problem.New(problem.TypeBadRequest, r, "Invalid request body", err.Error()))
			return
		}
		if in.Name == "" {
			problem.Write(w, http.StatusUnprocessableEntity,
				problem.New(problem.TypeValidation, r, "name is required", ""))
			return
		}
		row := sqlitestore.AreaRow{
			ID:          id,
			Name:        in.Name,
			Position:    in.Position,
			CreatedAtMS: existing.CreatedAtMS,
			UpdatedAtMS: time.Now().UnixMilli(),
		}
		if err := svc.Upsert(r.Context(), row); err != nil {
			writeServerError(w, r, http.StatusInternalServerError, problem.TypeInternal, "Update area failed", err)
			return
		}
		areaAudit(rec, r, "update="+id)
		w.WriteHeader(http.StatusNoContent)
	}
}

// DeleteArea handles DELETE /areas/{id} — removes the area and clears
// its room assignments (cascaded by the store in one transaction).
func DeleteArea(svc AreaAdmin, rec audit.Recorder) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			writeAreaUnavailable(w, r)
			return
		}
		id := chi.URLParam(r, "id")
		_, ok, err := svc.Get(r.Context(), id)
		if err != nil {
			writeServerError(w, r, http.StatusInternalServerError, problem.TypeInternal, "Get area failed", err)
			return
		}
		if !ok {
			writeAreaNotFound(w, r)
			return
		}
		if err := svc.Delete(r.Context(), id); err != nil {
			writeServerError(w, r, http.StatusInternalServerError, problem.TypeInternal, "Delete area failed", err)
			return
		}
		areaAudit(rec, r, "delete="+id)
		w.WriteHeader(http.StatusNoContent)
	}
}

// PutAreaRooms handles PUT /areas/{id}/rooms — full-set replace of the
// area's room assignments. A room already assigned to another area
// moves to this one (one area per room, enforced by the store's
// primary key).
func PutAreaRooms(svc AreaAdmin, rec audit.Recorder) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			writeAreaUnavailable(w, r)
			return
		}
		id := chi.URLParam(r, "id")
		if _, ok, err := svc.Get(r.Context(), id); err != nil {
			writeServerError(w, r, http.StatusInternalServerError, problem.TypeInternal, "Get area failed", err)
			return
		} else if !ok {
			writeAreaNotFound(w, r)
			return
		}
		var in []hmapi.AreaRoomRef
		if err := DecodeJSON(r, &in); err != nil {
			problem.Write(w, DecodeJSONStatus(err),
				problem.New(problem.TypeBadRequest, r, "Invalid request body", err.Error()))
			return
		}
		// Validate every row before touching the store so a bad entry
		// never leaves the assignment set half-replaced.
		refs := make([]sqlitestore.RoomAreaRow, 0, len(in))
		for i := range in {
			if in[i].Central == "" || in[i].Room == "" {
				problem.Write(w, http.StatusUnprocessableEntity,
					problem.New(problem.TypeValidation, r, "central and room are required", ""))
				return
			}
			refs = append(refs, sqlitestore.RoomAreaRow{
				CentralName: in[i].Central,
				RoomName:    in[i].Room,
				AreaID:      id,
			})
		}
		if err := svc.ReplaceRooms(r.Context(), id, refs); err != nil {
			writeServerError(w, r, http.StatusInternalServerError, problem.TypeInternal, "Replace area rooms failed", err)
			return
		}
		areaAudit(rec, r, "rooms_replace="+id)
		w.WriteHeader(http.StatusNoContent)
	}
}
