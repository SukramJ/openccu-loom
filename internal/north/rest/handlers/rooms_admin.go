// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package handlers

import (
	"context"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/SukramJ/openccu-loom/internal/audit"
	"github.com/SukramJ/openccu-loom/internal/model/hub"
	"github.com/SukramJ/openccu-loom/internal/north/rest/problem"
)

// RoomFunctionAdmin is the entity-CRUD surface for rooms and functions
// (Gewerke). *adapter.RoomFunctionAdminDomain satisfies it. Every call
// names a central (empty = the sole configured CCU) because rooms and
// functions are per-CCU objects.
type RoomFunctionAdmin interface {
	CreateRoom(ctx context.Context, central, name string) (int, error)
	RenameRoom(ctx context.Context, central, oldName, newName string) error
	DeleteRoom(ctx context.Context, central, name string) error
	CreateFunction(ctx context.Context, central, name string) (int, error)
	RenameFunction(ctx context.Context, central, oldName, newName string) error
	DeleteFunction(ctx context.Context, central, name string) error
}

type createGroupRequest struct {
	Name    string `json:"name"`
	Central string `json:"central,omitempty"`
}

type renameGroupRequest struct {
	NewName string `json:"new_name"`
	Central string `json:"central,omitempty"`
}

// writeGroupError maps the domain/hub sentinels to HTTP problem codes.
func writeGroupError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, hub.ErrRoomExists), errors.Is(err, hub.ErrFunctionExists):
		problem.Write(w, http.StatusConflict,
			problem.New(problem.TypeConflict, r, "Already exists", err.Error()))
	case errors.Is(err, hub.ErrRoomNotFound), errors.Is(err, hub.ErrFunctionNotFound),
		errors.Is(err, hub.ErrCentralNotFound):
		problem.Write(w, http.StatusNotFound,
			problem.New(problem.TypeNotFound, r, "Not found", err.Error()))
	case errors.Is(err, hub.ErrNoRoomMutator), errors.Is(err, hub.ErrNoFunctionMutator):
		problem.Write(w, http.StatusServiceUnavailable,
			problem.New(problem.TypeServiceUnready, r, "Room/function management unavailable", err.Error()))
	case errors.Is(err, hub.ErrCentralAmbiguous):
		problem.Write(w, http.StatusBadRequest,
			problem.New(problem.TypeValidation, r, "Central name required", err.Error()))
	default:
		writeServerError(w, r, http.StatusBadGateway, problem.TypeUpstreamUnavailable, "CCU write failed", err)
	}
}

func groupAudit(rec audit.Recorder, r *http.Request, note string) {
	if rec == nil {
		return
	}
	rec.Record(audit.Entry{
		User:   identityFromCtx(r.Context()),
		Action: audit.ActionRoomFunction,
		Note:   note,
	})
}

// CreateRoom handles POST /rooms.
func CreateRoom(svc RoomFunctionAdmin, rec audit.Recorder) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			problem.Write(w, http.StatusServiceUnavailable,
				problem.New(problem.TypeServiceUnready, r, "room admin unwired", ""))
			return
		}
		var body createGroupRequest
		if err := DecodeJSON(r, &body); err != nil || body.Name == "" {
			problem.Write(w, DecodeJSONStatus(err),
				problem.New(problem.TypeValidation, r, "name is required", ""))
			return
		}
		id, err := svc.CreateRoom(r.Context(), body.Central, body.Name)
		if err != nil {
			writeGroupError(w, r, err)
			return
		}
		groupAudit(rec, r, "create room "+body.Name)
		JSON(w, http.StatusCreated, map[string]any{"id": id, "name": body.Name})
	}
}

// RenameRoom handles PATCH /rooms/{name}.
func RenameRoom(svc RoomFunctionAdmin, rec audit.Recorder) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			problem.Write(w, http.StatusServiceUnavailable,
				problem.New(problem.TypeServiceUnready, r, "room admin unwired", ""))
			return
		}
		name := chi.URLParam(r, "name")
		var body renameGroupRequest
		if err := DecodeJSON(r, &body); err != nil || body.NewName == "" {
			problem.Write(w, DecodeJSONStatus(err),
				problem.New(problem.TypeValidation, r, "new_name is required", ""))
			return
		}
		if err := svc.RenameRoom(r.Context(), body.Central, name, body.NewName); err != nil {
			writeGroupError(w, r, err)
			return
		}
		groupAudit(rec, r, "rename room "+name+" to "+body.NewName)
		w.WriteHeader(http.StatusNoContent)
	}
}

// DeleteRoom handles DELETE /rooms/{name}.
func DeleteRoom(svc RoomFunctionAdmin, rec audit.Recorder) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			problem.Write(w, http.StatusServiceUnavailable,
				problem.New(problem.TypeServiceUnready, r, "room admin unwired", ""))
			return
		}
		name := chi.URLParam(r, "name")
		if err := svc.DeleteRoom(r.Context(), r.URL.Query().Get("central"), name); err != nil {
			writeGroupError(w, r, err)
			return
		}
		groupAudit(rec, r, "delete room "+name)
		w.WriteHeader(http.StatusNoContent)
	}
}

// CreateFunction handles POST /functions.
func CreateFunction(svc RoomFunctionAdmin, rec audit.Recorder) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			problem.Write(w, http.StatusServiceUnavailable,
				problem.New(problem.TypeServiceUnready, r, "function admin unwired", ""))
			return
		}
		var body createGroupRequest
		if err := DecodeJSON(r, &body); err != nil || body.Name == "" {
			problem.Write(w, DecodeJSONStatus(err),
				problem.New(problem.TypeValidation, r, "name is required", ""))
			return
		}
		id, err := svc.CreateFunction(r.Context(), body.Central, body.Name)
		if err != nil {
			writeGroupError(w, r, err)
			return
		}
		groupAudit(rec, r, "create function "+body.Name)
		JSON(w, http.StatusCreated, map[string]any{"id": id, "name": body.Name})
	}
}

// RenameFunction handles PATCH /functions/{name}.
func RenameFunction(svc RoomFunctionAdmin, rec audit.Recorder) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			problem.Write(w, http.StatusServiceUnavailable,
				problem.New(problem.TypeServiceUnready, r, "function admin unwired", ""))
			return
		}
		name := chi.URLParam(r, "name")
		var body renameGroupRequest
		if err := DecodeJSON(r, &body); err != nil || body.NewName == "" {
			problem.Write(w, DecodeJSONStatus(err),
				problem.New(problem.TypeValidation, r, "new_name is required", ""))
			return
		}
		if err := svc.RenameFunction(r.Context(), body.Central, name, body.NewName); err != nil {
			writeGroupError(w, r, err)
			return
		}
		groupAudit(rec, r, "rename function "+name+" to "+body.NewName)
		w.WriteHeader(http.StatusNoContent)
	}
}

// DeleteFunction handles DELETE /functions/{name}.
func DeleteFunction(svc RoomFunctionAdmin, rec audit.Recorder) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			problem.Write(w, http.StatusServiceUnavailable,
				problem.New(problem.TypeServiceUnready, r, "function admin unwired", ""))
			return
		}
		name := chi.URLParam(r, "name")
		if err := svc.DeleteFunction(r.Context(), r.URL.Query().Get("central"), name); err != nil {
			writeGroupError(w, r, err)
			return
		}
		groupAudit(rec, r, "delete function "+name)
		w.WriteHeader(http.StatusNoContent)
	}
}
