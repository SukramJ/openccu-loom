// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/SukramJ/openccu-loom/internal/audit"
	"github.com/SukramJ/openccu-loom/internal/client/backends"
	"github.com/SukramJ/openccu-loom/internal/north/rest/problem"
	"github.com/SukramJ/openccu-loom/pkg/hmerr"
)

// GroupsWriter is the write facade for heating-group administration
// (GR02). It is backed by the CCU jpages proxy — create runs the two-step
// GET create → POST save flow and confirms completion by re-reading the
// roster; edit/delete map onto save/delete. See
// docs/adr/0055-groups-jpages-proxy.md.
type GroupsWriter interface {
	CreateGroup(ctx context.Context, central string, req CreateGroupRequest) (GroupEntry, error)
	UpdateGroup(ctx context.Context, central string, id int, req UpdateGroupRequest) error
	DeleteGroup(ctx context.Context, central string, id int) error
	SuitableMembers(ctx context.Context, central, typeID string) (SuitableMembersResponse, error)
	GroupTypes(ctx context.Context, central string) ([]GroupTypeEntry, error)
}

// CreateGroupRequest is the body of POST /api/v1/groups.
type CreateGroupRequest struct {
	// TypeID is the group-type key (e.g. the HmIP heating-group type).
	TypeID string `json:"type_id"`
	// Name is the operator-facing group name.
	Name string `json:"name"`
	// ForbidSingleOperation sets the "operate only via group" flag.
	ForbidSingleOperation bool `json:"forbid_single_operation"`
	// Members are the member channel/device addresses to assign.
	Members []string `json:"members"`
}

// UpdateGroupRequest is the body of PUT /api/v1/groups/{id}.
type UpdateGroupRequest struct {
	Name                  string   `json:"name"`
	ForbidSingleOperation bool     `json:"forbid_single_operation"`
	Members               []string `json:"members"`
}

// GroupTypeEntry is one assignable group type for the create form.
type GroupTypeEntry struct {
	ID       string `json:"id"`
	LabelKey string `json:"label_key,omitempty"`
}

// SuitableMemberEntry is one device/channel assignable to a group type. The
// fields below Type are best-effort enrichment from the live device model that
// let the SPA identify, group and filter candidates; they are omitted when the
// member is not (yet) in the model.
type SuitableMemberEntry struct {
	Address       string   `json:"address"`
	Serial        string   `json:"serial,omitempty"`
	Type          string   `json:"type,omitempty"`
	DeviceAddress string   `json:"device_address,omitempty"`
	DeviceName    string   `json:"device_name,omitempty"`
	DeviceModel   string   `json:"device_model,omitempty"`
	ChannelName   string   `json:"channel_name,omitempty"`
	ChannelNo     int      `json:"channel_no,omitempty"`
	Rooms         []string `json:"rooms,omitempty"`
	Functions     []string `json:"functions,omitempty"`
}

// SuitableMembersResponse splits the candidate members into assignable and
// leftover buckets.
type SuitableMembersResponse struct {
	Assignable []SuitableMemberEntry `json:"assignable"`
	Leftover   []SuitableMemberEntry `json:"leftover"`
}

// CreateGroup serves POST /api/v1/groups (admin-gated). `?central=` selects
// the target CCU (optional when only one is configured). Returns 201 with the
// created group.
func CreateGroup(svc GroupsWriter, rec audit.Recorder) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			problem.Write(w, http.StatusServiceUnavailable,
				problem.New(problem.TypeServiceUnready, r, "Groups service unwired", ""))
			return
		}
		var req CreateGroupRequest
		if !decodeGroupBody(w, r, &req) {
			return
		}
		if strings.TrimSpace(req.Name) == "" || strings.TrimSpace(req.TypeID) == "" {
			problem.Write(w, http.StatusBadRequest,
				problem.New(problem.TypeBadRequest, r, "name and type_id are required", ""))
			return
		}
		central := r.URL.Query().Get("central")
		g, err := svc.CreateGroup(r.Context(), central, req)
		if err != nil {
			writeGroupWriteError(w, r, err)
			return
		}
		heatingGroupAudit(rec, r, "create", central, g.Name)
		JSON(w, http.StatusCreated, g)
	}
}

// UpdateGroup serves PUT /api/v1/groups/{id} (admin-gated). Returns 204.
func UpdateGroup(svc GroupsWriter, rec audit.Recorder) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			problem.Write(w, http.StatusServiceUnavailable,
				problem.New(problem.TypeServiceUnready, r, "Groups service unwired", ""))
			return
		}
		id, ok := groupID(w, r)
		if !ok {
			return
		}
		var req UpdateGroupRequest
		if !decodeGroupBody(w, r, &req) {
			return
		}
		if strings.TrimSpace(req.Name) == "" {
			problem.Write(w, http.StatusBadRequest,
				problem.New(problem.TypeBadRequest, r, "name is required", ""))
			return
		}
		central := r.URL.Query().Get("central")
		if err := svc.UpdateGroup(r.Context(), central, id, req); err != nil {
			writeGroupWriteError(w, r, err)
			return
		}
		heatingGroupAudit(rec, r, "update", central, fmt.Sprintf("%s (id %d)", req.Name, id))
		w.WriteHeader(http.StatusNoContent)
	}
}

// DeleteGroup serves DELETE /api/v1/groups/{id} (admin-gated). Returns 204.
func DeleteGroup(svc GroupsWriter, rec audit.Recorder) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			problem.Write(w, http.StatusServiceUnavailable,
				problem.New(problem.TypeServiceUnready, r, "Groups service unwired", ""))
			return
		}
		id, ok := groupID(w, r)
		if !ok {
			return
		}
		central := r.URL.Query().Get("central")
		if err := svc.DeleteGroup(r.Context(), central, id); err != nil {
			writeGroupWriteError(w, r, err)
			return
		}
		heatingGroupAudit(rec, r, "delete", central, fmt.Sprintf("id %d", id))
		w.WriteHeader(http.StatusNoContent)
	}
}

// ListSuitableMembers serves GET /api/v1/groups/suitable-members?type_id=…
// It returns the devices assignable to a group of the given type.
func ListSuitableMembers(svc GroupsWriter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			problem.Write(w, http.StatusServiceUnavailable,
				problem.New(problem.TypeServiceUnready, r, "Groups service unwired", ""))
			return
		}
		typeID := r.URL.Query().Get("type_id")
		if strings.TrimSpace(typeID) == "" {
			problem.Write(w, http.StatusBadRequest,
				problem.New(problem.TypeBadRequest, r, "type_id is required", ""))
			return
		}
		res, err := svc.SuitableMembers(r.Context(), r.URL.Query().Get("central"), typeID)
		if err != nil {
			writeGroupWriteError(w, r, err)
			return
		}
		JSON(w, http.StatusOK, res)
	}
}

// ListGroupTypes serves GET /api/v1/groups/types — the group types a new
// group can be created as.
func ListGroupTypes(svc GroupsWriter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			problem.Write(w, http.StatusServiceUnavailable,
				problem.New(problem.TypeServiceUnready, r, "Groups service unwired", ""))
			return
		}
		types, err := svc.GroupTypes(r.Context(), r.URL.Query().Get("central"))
		if err != nil {
			writeGroupWriteError(w, r, err)
			return
		}
		if types == nil {
			types = []GroupTypeEntry{}
		}
		JSON(w, http.StatusOK, map[string]any{"types": types})
	}
}

// --- helpers ----------------------------------------------------------------

func decodeGroupBody(w http.ResponseWriter, r *http.Request, dst any) bool {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		problem.Write(w, http.StatusBadRequest,
			problem.New(problem.TypeBadRequest, r, "Invalid request body", err.Error()))
		return false
	}
	return true
}

func groupID(w http.ResponseWriter, r *http.Request) (int, bool) {
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil || id < 0 {
		problem.Write(w, http.StatusBadRequest,
			problem.New(problem.TypeBadRequest, r, "Invalid group id", chi.URLParam(r, "id")))
		return 0, false
	}
	return id, true
}

func writeGroupWriteError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, hmerr.ErrUnknownCentral):
		problem.Write(w, http.StatusNotFound,
			problem.New(problem.TypeNotFound, r, "Unknown central", r.URL.Query().Get("central")))
	case errors.Is(err, hmerr.ErrGroupNotFound):
		problem.Write(w, http.StatusNotFound,
			problem.New(problem.TypeNotFound, r, "Group not found", chi.URLParam(r, "id")))
	case errors.Is(err, backends.ErrUnsupported):
		problem.Write(w, http.StatusNotFound,
			problem.New(problem.TypeNotFound, r, "Group administration not available on this central", ""))
	default:
		writeServerError(w, r, http.StatusBadGateway, problem.TypeUpstreamUnavailable,
			"Group operation failed", err)
	}
}

func heatingGroupAudit(rec audit.Recorder, r *http.Request, op, central, target string) {
	if rec == nil {
		return
	}
	note := op + " " + target
	if central != "" {
		note = central + ": " + note
	}
	rec.Record(audit.Entry{
		User:   identityFromCtx(r.Context()),
		Action: audit.ActionGroupAdmin,
		Note:   note,
	})
}
