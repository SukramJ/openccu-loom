// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/SukramJ/openccu-loom/internal/alarm"
	"github.com/SukramJ/openccu-loom/internal/alarm/codes"
	"github.com/SukramJ/openccu-loom/internal/audit"
	"github.com/SukramJ/openccu-loom/internal/north/rest/problem"
	sqlitestore "github.com/SukramJ/openccu-loom/internal/store/sqlite"
	"github.com/SukramJ/openccu-loom/pkg/hmapi"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// ErrInvalidAlarmCode is the sentinel every alarm-code write-time
// validation failure wraps (notes/concepts/alarm-concept.md §11, S7
// fail-visible): a binding or PIN that could never authenticate or
// never fire must be rejected at write time rather than stored and
// silently inert. Both the REST handlers and the WS codes_* commands
// share AlarmCodeStoreAdmin, so a check here covers both surfaces.
var ErrInvalidAlarmCode = errors.New("handlers: invalid alarm code request")

// errUnexpectedBindingTrailer reports extra content after the decoded
// binding document (e.g. a second concatenated JSON value).
var errUnexpectedBindingTrailer = errors.New("unexpected trailing data after binding document")

// AlarmCodeAdmin is the narrow facade the /alarm/codes handlers drive: a
// CRUD surface over the alarm-code store that owns the argon2id hashing
// (notes/concepts/alarm-concept.md §11). It is satisfied structurally by the codes
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

// alarmCodeKinds is the accepted set for the AlarmCode.Kind discriminator,
// built from the codes package's own kinds rather than restated: a kind this
// handler accepts but the facade cannot dispatch is stored and never fires.
var alarmCodeKinds = func() map[string]struct{} {
	out := make(map[string]struct{}, len(codes.Kinds()))
	for _, k := range codes.Kinds() {
		out[string(k)] = struct{}{}
	}
	return out
}()

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
			writeAlarmCodeAdminError(w, r, "Create alarm code failed", err)
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
			writeAlarmCodeAdminError(w, r, "Update alarm code failed", err)
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

// writeAlarmCodeAdminError maps a CreateCode/UpdateCode failure onto its
// wire status: a write-time validation failure (ErrInvalidAlarmCode)
// answers 422 with the human-readable detail, mirroring
// decodeAlarmCodeRequest's 422 style; every other failure (store I/O,
// hashing) stays a 500.
func writeAlarmCodeAdminError(w http.ResponseWriter, r *http.Request, title string, err error) {
	if errors.Is(err, ErrInvalidAlarmCode) {
		problem.Write(w, http.StatusUnprocessableEntity,
			problem.New(problem.TypeValidation, r, "Invalid alarm code", err.Error()))
		return
	}
	writeServerError(w, r, http.StatusInternalServerError, problem.TypeInternal, title, err)
}

// AlarmCodeStoreAdmin implements AlarmCodeAdmin (and the identical WS
// facade) over the alarm-code store, mapping the wire DTOs onto stored
// rows and hashing the write-only PIN through the codes domain helper
// (notes/concepts/alarm-concept.md §11). The argon2id hash never leaves this
// adapter — it is read from the store to preserve on a PIN-less update,
// and written on hash, but never surfaced onto an [hmapi.AlarmCode].
type AlarmCodeStoreAdmin struct {
	store    *sqlitestore.AlarmCodeStore
	onChange func()
}

// Compile-time proof the store adapter satisfies the handler port.
var _ AlarmCodeAdmin = (*AlarmCodeStoreAdmin)(nil)

// NewAlarmCodeStoreAdmin builds the adapter over store. The caller passes
// a non-nil store; a disabled alarm subsystem yields a nil AlarmCodeAdmin
// interface at the composition root instead.
func NewAlarmCodeStoreAdmin(store *sqlitestore.AlarmCodeStore) *AlarmCodeStoreAdmin {
	return &AlarmCodeStoreAdmin{store: store}
}

// OnChange installs a hook fired after every successful code mutation.
// The composition root wires it to the alarm service's codes-changed
// notification so dependent surfaces (MQTT discovery's effective
// code-requirement flags) re-derive their projections.
func (a *AlarmCodeStoreAdmin) OnChange(fn func()) *AlarmCodeStoreAdmin {
	a.onChange = fn
	return a
}

// notifyChanged fires the mutation hook, if any.
func (a *AlarmCodeStoreAdmin) notifyChanged() {
	if a.onChange != nil {
		a.onChange()
	}
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
	a.notifyChanged()
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
	a.notifyChanged()
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
	a.notifyChanged()
	return true, nil
}

// alarmCodeRowFromReq maps a create/update request onto a stored row. The
// PIN is hashed via the codes helper for the pin kind; a non-pin kind
// carries no hash, and a pin update with an empty PIN keeps existingHash.
// perms/zones/binding are stored as the whole JSON documents the codes
// facade reads back.
func alarmCodeRowFromReq(id string, req hmapi.AlarmCodeRequest, existingHash string, createdMS, updatedMS int64) (sqlitestore.AlarmCodeRow, error) {
	if err := validateAlarmCodeWrite(req, existingHash); err != nil {
		return sqlitestore.AlarmCodeRow{}, err
	}
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
	zonesJSON := ""
	if len(req.Zones) > 0 {
		b, err := json.Marshal(req.Zones)
		if err != nil {
			return sqlitestore.AlarmCodeRow{}, err
		}
		zonesJSON = string(b)
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
		ZonesJSON:    zonesJSON,
		BindingJSON:  bindingJSON,
		ValidFromMS:  req.ValidFromMS,
		ValidUntilMS: req.ValidUntilMS,
		Enabled:      req.Enabled,
		CreatedAtMS:  createdMS,
		UpdatedAtMS:  updatedMS,
	}, nil
}

// validateAlarmCodeWrite enforces the write-time invariants a stored
// code needs to ever authenticate or fire (notes/concepts/alarm-concept.md §11,
// S7 fail-visible): a pin code must carry a PIN on creation, and a
// hardware binding must be well-formed JSON targeting only the fields
// and edges the intent router (internal/alarm/intents.go) and codes
// facade (internal/alarm/codes) consume. A rejected write never
// reaches the store, so a typo'd or unsupported binding cannot be
// saved and silently never fire.
func validateAlarmCodeWrite(req hmapi.AlarmCodeRequest, existingHash string) error {
	switch req.Kind {
	case string(codes.KindPIN):
		if existingHash == "" && req.PIN == "" {
			return fmt.Errorf("pin is required for a new pin code: %w", ErrInvalidAlarmCode)
		}
	case string(codes.KindKeypadSlot):
		b, err := decodeStrictBinding(req.Binding)
		if err != nil {
			return fmt.Errorf("keypad_slot binding: %w: %w", err, ErrInvalidAlarmCode)
		}
		if b.DeviceAddress == "" {
			return fmt.Errorf("keypad_slot binding requires device_address: %w", ErrInvalidAlarmCode)
		}
		if b.Slot < 1 || b.Slot > 8 {
			return fmt.Errorf("keypad_slot binding slot must be 1..8: %w", ErrInvalidAlarmCode)
		}
		if b.ZoneID == "" {
			return fmt.Errorf("keypad_slot binding requires zone_id: %w", ErrInvalidAlarmCode)
		}
	case string(codes.KindRemoteKey):
		b, err := decodeStrictBinding(req.Binding)
		if err != nil {
			return fmt.Errorf("remote_key binding: %w: %w", err, ErrInvalidAlarmCode)
		}
		if b.ChannelAddress == "" {
			return fmt.Errorf("remote_key binding requires channel_address: %w", ErrInvalidAlarmCode)
		}
		if b.ZoneID == "" {
			return fmt.Errorf("remote_key binding requires zone_id: %w", ErrInvalidAlarmCode)
		}
		// A binding on a parameter the intent router does not route (a
		// typo'd "PRESS", say) is accepted by the wire schema and can then
		// never fire, which is indistinguishable from a broken remote — so
		// it is rejected here rather than stored inert (S7 fail-visible).
		// The set is the router's, not a copy: a parameter added to the
		// routing widens what this write accepts, on its own.
		if !alarm.IsRemotePressParameter(hmenum.Parameter(b.Parameter)) {
			return fmt.Errorf("remote_key binding parameter must be PRESS_SHORT or PRESS_LONG: %w", ErrInvalidAlarmCode)
		}
		if !validRemoteKeyAction(b.Action) {
			return fmt.Errorf("remote_key binding action must be disarm, silence, panic, or arm:<mode>: %w", ErrInvalidAlarmCode)
		}
	}
	return nil
}

// validRemoteKeyAction reports whether action is one of the verbs
// dispatchRemoteAction (internal/alarm/intents.go) actually executes:
// the fixed strings disarm/silence/panic, or an "arm:<mode>" prefix
// (a bare "arm:" is valid too — dispatchArm defaults an empty mode to
// full protection).
func validRemoteKeyAction(action string) bool {
	return alarm.IsDispatchableRemoteAction(action)
}

// decodeStrictBinding decodes raw into a codes.Binding, rejecting a
// malformed document, unknown fields, and any trailing data — the
// three shapes review found being silently accepted and stored inert.
// An empty/missing binding is reported as io.EOF via the decoder so
// its caller's error message stays uniform.
func decodeStrictBinding(raw json.RawMessage) (codes.Binding, error) {
	var b codes.Binding
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&b); err != nil {
		return codes.Binding{}, err
	}
	if dec.More() {
		return codes.Binding{}, errUnexpectedBindingTrailer
	}
	return b, nil
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
	if row.ZonesJSON != "" && row.ZonesJSON != "[]" {
		_ = json.Unmarshal([]byte(row.ZonesJSON), &out.Zones)
	}
	if row.BindingJSON != "" {
		out.Binding = json.RawMessage(row.BindingJSON)
	}
	return out
}
