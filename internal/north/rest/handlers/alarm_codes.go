// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/SukramJ/openccu-loom/internal/alarm/codes"
	"github.com/SukramJ/openccu-loom/internal/audit"
	"github.com/SukramJ/openccu-loom/internal/north/rest/problem"
	sqlitestore "github.com/SukramJ/openccu-loom/internal/store/sqlite"
	"github.com/SukramJ/openccu-loom/pkg/hmapi"
)

// AlarmCodeAdmin is the narrow facade the /alarm/codes handlers drive: a
// CRUD surface over the alarm-code store that owns the argon2id hashing
// (docs/alarm-concept.md §11). It is satisfied structurally by the codes
// facade — the composition root wires the concrete value, or leaves the
// router dependency nil to serve the routes as 503 (the alarm-code
// subsystem is not available). The facade NEVER returns a cleartext PIN
// or hash on the [hmapi.AlarmCode] projection; the write-only PIN travels
// one way through [hmapi.AlarmCodeRequest].
type AlarmCodeAdmin interface {
	// ListCodes returns every configured code as a hash-free projection.
	ListCodes(ctx context.Context) ([]hmapi.AlarmCode, error)
	// GetCode returns one code by id; ok is false when the id is unknown.
	GetCode(ctx context.Context, id string) (code hmapi.AlarmCode, ok bool, err error)
	// CreateCode persists a new code (hashing the PIN when present) and
	// returns its hash-free projection.
	CreateCode(ctx context.Context, req hmapi.AlarmCodeRequest) (hmapi.AlarmCode, error)
	// UpdateCode replaces an existing code; ok is false when the id is
	// unknown. An empty PIN in req keeps the stored hash.
	UpdateCode(ctx context.Context, id string, req hmapi.AlarmCodeRequest) (code hmapi.AlarmCode, ok bool, err error)
	// DeleteCode removes a code; ok is false when the id is unknown.
	DeleteCode(ctx context.Context, id string) (ok bool, err error)
}

// alarmCodeKinds is the accepted set for the AlarmCode.Kind discriminator.
var alarmCodeKinds = map[string]struct{}{
	"pin":         {},
	"keypad_slot": {},
	"remote_key":  {},
}

// ListAlarmCodes renders every configured alarm code (hash-free).
func ListAlarmCodes(admin AlarmCodeAdmin) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if admin == nil {
			writeAlarmCodesUnavailable(w, r)
			return
		}
		rows, err := admin.ListCodes(r.Context())
		if err != nil {
			writeServerError(w, r, http.StatusInternalServerError, problem.TypeInternal, "List alarm codes failed", err)
			return
		}
		if rows == nil {
			rows = []hmapi.AlarmCode{}
		}
		JSON(w, http.StatusOK, rows)
	}
}

// GetAlarmCode renders a single alarm code by id (hash-free).
func GetAlarmCode(admin AlarmCodeAdmin) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if admin == nil {
			writeAlarmCodesUnavailable(w, r)
			return
		}
		code, ok, err := admin.GetCode(r.Context(), chi.URLParam(r, "id"))
		if err != nil {
			writeServerError(w, r, http.StatusInternalServerError, problem.TypeInternal, "Get alarm code failed", err)
			return
		}
		if !ok {
			writeAlarmNotFound(w, r)
			return
		}
		JSON(w, http.StatusOK, code)
	}
}

// CreateAlarmCode persists a new alarm code with a server-generated id.
func CreateAlarmCode(admin AlarmCodeAdmin, rec audit.Recorder) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if admin == nil {
			writeAlarmCodesUnavailable(w, r)
			return
		}
		req, ok := decodeAlarmCodeRequest(w, r)
		if !ok {
			return
		}
		created, err := admin.CreateCode(r.Context(), req)
		if err != nil {
			writeServerError(w, r, http.StatusInternalServerError, problem.TypeInternal, "Create alarm code failed", err)
			return
		}
		recordAlarm(rec, r, audit.ActionAlarmCodeChange, "code_create="+created.ID)
		JSON(w, http.StatusCreated, created)
	}
}

// PutAlarmCode replaces an existing alarm code. An empty PIN keeps the
// stored hash so an operator can edit metadata without re-entering the
// code.
func PutAlarmCode(admin AlarmCodeAdmin, rec audit.Recorder) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if admin == nil {
			writeAlarmCodesUnavailable(w, r)
			return
		}
		id := chi.URLParam(r, "id")
		req, ok := decodeAlarmCodeRequest(w, r)
		if !ok {
			return
		}
		_, found, err := admin.UpdateCode(r.Context(), id, req)
		if err != nil {
			writeServerError(w, r, http.StatusInternalServerError, problem.TypeInternal, "Update alarm code failed", err)
			return
		}
		if !found {
			writeAlarmNotFound(w, r)
			return
		}
		recordAlarm(rec, r, audit.ActionAlarmCodeChange, "code_update="+id)
		w.WriteHeader(http.StatusNoContent)
	}
}

// DeleteAlarmCode removes an alarm code.
func DeleteAlarmCode(admin AlarmCodeAdmin, rec audit.Recorder) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if admin == nil {
			writeAlarmCodesUnavailable(w, r)
			return
		}
		id := chi.URLParam(r, "id")
		found, err := admin.DeleteCode(r.Context(), id)
		if err != nil {
			writeServerError(w, r, http.StatusInternalServerError, problem.TypeInternal, "Delete alarm code failed", err)
			return
		}
		if !found {
			writeAlarmNotFound(w, r)
			return
		}
		recordAlarm(rec, r, audit.ActionAlarmCodeChange, "code_delete="+id)
		w.WriteHeader(http.StatusNoContent)
	}
}

// decodeAlarmCodeRequest decodes and validates the shared create/update
// body. It answers 400 on a malformed body and 422 on an invalid kind or
// missing name, returning ok=false so the caller returns without acting.
func decodeAlarmCodeRequest(w http.ResponseWriter, r *http.Request) (hmapi.AlarmCodeRequest, bool) {
	var req hmapi.AlarmCodeRequest
	if err := DecodeJSON(r, &req); err != nil && !errors.Is(err, io.EOF) {
		problem.Write(w, DecodeJSONStatus(err),
			problem.New(problem.TypeBadRequest, r, "Invalid request body", err.Error()))
		return req, false
	}
	if req.Name == "" {
		problem.Write(w, http.StatusUnprocessableEntity,
			problem.New(problem.TypeValidation, r, "Invalid alarm code", "name is required"))
		return req, false
	}
	if _, ok := alarmCodeKinds[req.Kind]; !ok {
		problem.Write(w, http.StatusUnprocessableEntity,
			problem.New(problem.TypeValidation, r, "Invalid alarm code",
				"kind must be one of pin, keypad_slot, remote_key"))
		return req, false
	}
	return req, true
}

// writeAlarmCodesUnavailable answers 503 when the alarm-code subsystem is
// not wired (no codes facade). The route still exists in the contract; it
// is the backing store that is absent.
func writeAlarmCodesUnavailable(w http.ResponseWriter, r *http.Request) {
	problem.Write(w, http.StatusServiceUnavailable,
		problem.New(problem.TypeServiceUnready, r, "Alarm code subsystem unavailable", ""))
}

// AlarmCodeStoreAdmin implements AlarmCodeAdmin (and the identical WS
// facade) over the alarm-code store, mapping the wire DTOs onto stored
// rows and hashing the write-only PIN through the codes domain helper
// (docs/alarm-concept.md §11). The argon2id hash never leaves this
// adapter — it is read from the store to preserve on a PIN-less update,
// and written on hash, but never surfaced onto an [hmapi.AlarmCode].
type AlarmCodeStoreAdmin struct {
	store *sqlitestore.AlarmCodeStore
}

// Compile-time proof the store adapter satisfies the handler port.
var _ AlarmCodeAdmin = (*AlarmCodeStoreAdmin)(nil)

// NewAlarmCodeStoreAdmin builds the adapter over store. The caller passes
// a non-nil store; a disabled alarm subsystem yields a nil AlarmCodeAdmin
// interface at the composition root instead.
func NewAlarmCodeStoreAdmin(store *sqlitestore.AlarmCodeStore) *AlarmCodeStoreAdmin {
	return &AlarmCodeStoreAdmin{store: store}
}

// ListCodes returns every code as a hash-free projection.
func (a *AlarmCodeStoreAdmin) ListCodes(ctx context.Context) ([]hmapi.AlarmCode, error) {
	rows, err := a.store.GetAll(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]hmapi.AlarmCode, 0, len(rows))
	for i := range rows {
		out = append(out, alarmCodeFromRow(rows[i]))
	}
	return out, nil
}

// GetCode returns one code by id.
func (a *AlarmCodeStoreAdmin) GetCode(ctx context.Context, id string) (hmapi.AlarmCode, bool, error) {
	row, ok, err := a.store.Get(ctx, id)
	if err != nil || !ok {
		return hmapi.AlarmCode{}, ok, err
	}
	return alarmCodeFromRow(row), true, nil
}

// CreateCode persists a new code with a server-generated id, hashing the
// PIN when the kind is pin and a PIN is supplied.
func (a *AlarmCodeStoreAdmin) CreateCode(ctx context.Context, req hmapi.AlarmCodeRequest) (hmapi.AlarmCode, error) {
	now := time.Now().UnixMilli()
	row, err := alarmCodeRowFromReq(uuid.NewString(), req, "", now, now)
	if err != nil {
		return hmapi.AlarmCode{}, err
	}
	if err := a.store.Upsert(ctx, row); err != nil {
		return hmapi.AlarmCode{}, err
	}
	return alarmCodeFromRow(row), nil
}

// UpdateCode replaces an existing code, preserving its created-at stamp
// and — when req carries no PIN — its stored hash.
func (a *AlarmCodeStoreAdmin) UpdateCode(ctx context.Context, id string, req hmapi.AlarmCodeRequest) (hmapi.AlarmCode, bool, error) {
	existing, ok, err := a.store.Get(ctx, id)
	if err != nil || !ok {
		return hmapi.AlarmCode{}, ok, err
	}
	row, err := alarmCodeRowFromReq(id, req, existing.Hash, existing.CreatedAtMS, time.Now().UnixMilli())
	if err != nil {
		return hmapi.AlarmCode{}, false, err
	}
	if err := a.store.Upsert(ctx, row); err != nil {
		return hmapi.AlarmCode{}, false, err
	}
	return alarmCodeFromRow(row), true, nil
}

// DeleteCode removes a code, reporting whether it existed.
func (a *AlarmCodeStoreAdmin) DeleteCode(ctx context.Context, id string) (bool, error) {
	_, ok, err := a.store.Get(ctx, id)
	if err != nil || !ok {
		return ok, err
	}
	if err := a.store.Delete(ctx, id); err != nil {
		return false, err
	}
	return true, nil
}

// alarmCodeRowFromReq maps a create/update request onto a stored row. The
// PIN is hashed via the codes helper for the pin kind; a non-pin kind
// carries no hash, and a pin update with an empty PIN keeps existingHash.
// perms/areas/binding are stored as the whole JSON documents the codes
// facade reads back.
func alarmCodeRowFromReq(id string, req hmapi.AlarmCodeRequest, existingHash string, createdMS, updatedMS int64) (sqlitestore.AlarmCodeRow, error) {
	hash := existingHash
	switch {
	case req.Kind != string(codes.KindPIN):
		hash = "" // hardware kinds carry no secret
	case req.PIN != "":
		h, err := codes.HashPIN(req.PIN)
		if err != nil {
			return sqlitestore.AlarmCodeRow{}, err
		}
		hash = h
	}
	permsJSON, err := json.Marshal(req.Perms)
	if err != nil {
		return sqlitestore.AlarmCodeRow{}, err
	}
	areasJSON := ""
	if len(req.Areas) > 0 {
		b, err := json.Marshal(req.Areas)
		if err != nil {
			return sqlitestore.AlarmCodeRow{}, err
		}
		areasJSON = string(b)
	}
	bindingJSON := ""
	if len(req.Binding) > 0 {
		bindingJSON = string(req.Binding)
	}
	return sqlitestore.AlarmCodeRow{
		ID:           id,
		Name:         req.Name,
		Kind:         req.Kind,
		Hash:         hash,
		Duress:       req.Duress,
		PermsJSON:    string(permsJSON),
		AreasJSON:    areasJSON,
		BindingJSON:  bindingJSON,
		ValidFromMS:  req.ValidFromMS,
		ValidUntilMS: req.ValidUntilMS,
		Enabled:      req.Enabled,
		CreatedAtMS:  createdMS,
		UpdatedAtMS:  updatedMS,
	}, nil
}

// alarmCodeFromRow maps a stored row onto the hash-free wire projection.
func alarmCodeFromRow(row sqlitestore.AlarmCodeRow) hmapi.AlarmCode {
	out := hmapi.AlarmCode{
		ID:           row.ID,
		Name:         row.Name,
		Kind:         row.Kind,
		Duress:       row.Duress,
		ValidFromMS:  row.ValidFromMS,
		ValidUntilMS: row.ValidUntilMS,
		Enabled:      row.Enabled,
		CreatedMS:    row.CreatedAtMS,
		UpdatedMS:    row.UpdatedAtMS,
	}
	if row.PermsJSON != "" {
		_ = json.Unmarshal([]byte(row.PermsJSON), &out.Perms)
	}
	if row.AreasJSON != "" && row.AreasJSON != "[]" {
		_ = json.Unmarshal([]byte(row.AreasJSON), &out.Areas)
	}
	if row.BindingJSON != "" {
		out.Binding = json.RawMessage(row.BindingJSON)
	}
	return out
}
