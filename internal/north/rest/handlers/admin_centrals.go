// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/SukramJ/openccu-loom/internal/audit"
	"github.com/SukramJ/openccu-loom/internal/auth"
	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/internal/north/rest/problem"
	"github.com/SukramJ/openccu-loom/internal/store/sqlite"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// CentralAdminService is the DI surface for the /api/v1/admin/centrals
// endpoints. The router wires a [*sqlite.CentralsStore] underneath.
type CentralAdminService interface {
	// Put creates or replaces a central row.
	Put(ctx context.Context, row sqlite.CentralRow) error
	// Get returns one central by name. Returns [sqlite.ErrCentralNotFound]
	// when the name is unknown.
	Get(ctx context.Context, name string) (sqlite.CentralRow, error)
	// Delete removes a central by name. Returns [sqlite.ErrCentralNotFound]
	// when the name is unknown.
	Delete(ctx context.Context, name string) error
	// List returns every central sorted by name.
	List(ctx context.Context) ([]sqlite.CentralRow, error)
}

// maskCentralRow returns row with its cleartext CCU password replaced by
// the mask sentinel, and — for a caller below [auth.RoleAdmin] — with the
// CCU's network coordinates and account name removed as well.
//
// The password mask is unconditional: the store decrypts password_plain on
// read, so without it GET /admin/centrals would hand out the live CCU
// credential in the clear.
//
// The rest of the row is role-scoped because the two read routes are
// deliberately NOT admin-gated — the energy, backup and rooms/functions
// views need the central list, and all three read only Name, Enabled and
// Interfaces. Everything else (host, ports, username, the TLS posture)
// tells an authenticated viewer exactly where the CCU lives and how it is
// reached, which is reconnaissance rather than anything those views use.
// Gating the routes instead would break them, so the row is narrowed and
// the routes stay open.
//
// An absent identity means authentication is switched off entirely; there
// is no viewer to distinguish from an admin then, so the full row is
// returned.
//
// password_env holds only the env-variable NAME (not a secret) and stays
// visible for an admin so the operator can see which variable is
// referenced. [restoreCentralSecret] swaps the sentinel back on write.
func maskCentralRow(ctx context.Context, row sqlite.CentralRow) sqlite.CentralRow {
	if row.PasswordPlain != "" {
		row.PasswordPlain = maskSentinel
	}
	if row.APITokenPlain != "" {
		row.APITokenPlain = maskSentinel
	}
	if id, ok := auth.IdentityFrom(ctx); ok && !id.HasRole(auth.RoleAdmin) {
		row.Host = ""
		row.Serial = ""
		row.Port = 0
		row.JSONRPCPort = 0
		row.Ports = nil
		row.Username = ""
		row.PasswordEnv = ""
		row.PasswordPlain = ""
		row.TLS = false
		row.TLSInsecureSkipVerify = false
		row.APITokenEnv = ""
		row.APITokenPlain = ""
		row.TLSFingerprint = ""
	}
	return row
}

// decodeCentralRow decodes a central request body and additionally reports,
// per JSON field name (lower-cased), whether the payload actually carried
// that key — the same presence-probe technique for every field, not just
// password_plain.
//
// The distinction is load-bearing on the update path: [CentralAdminService.Put]
// is an unconditional full-row upsert, and every field in [sqlite.CentralRow]
// besides name/host/interfaces/enabled is optional per the published schema.
// A client that only means to change one field — flip `enabled`, rotate
// `username` — omits the rest, and those keys must decode to the value
// already on disk, not the Go zero value. [UpdateCentral] uses the presence
// set to overlay only the fields the payload actually carried onto the
// stored row.
//
// password_plain carries one extra rule: GET masks the stored credential to
// [maskSentinel] and never returns it in the clear, so a client following
// the published schema has no way to echo the real password back. An
// explicit `null` therefore means "unchanged" exactly like an absent key —
// present["password_plain"] is false for both — matching the contract the
// config section editor implements in [restoreMaskedSecrets]; only an
// explicit empty string clears the password.
func decodeCentralRow(r *http.Request) (row sqlite.CentralRow, pairingID string, present map[string]bool, err error) {
	body, err := io.ReadAll(http.MaxBytesReader(nil, r.Body, maxRequestBodyBytes))
	if err != nil {
		return row, "", nil, err
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	var req centralWriteRequest
	if err := dec.Decode(&req); err != nil {
		return row, "", nil, err
	}
	row, pairingID = req.CentralRow, req.PairingID
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(body, &keys); err != nil {
		return row, "", nil, err
	}
	present = make(map[string]bool, len(keys))
	for k, v := range keys {
		// encoding/json matches object keys case-insensitively, so the
		// presence probe has to as well.
		lk := strings.ToLower(k)
		if (lk == "password_plain" || lk == "api_token_plain") && string(v) == "null" {
			continue
		}
		present[lk] = true
	}
	return row, pairingID, present, nil
}

// centralWriteRequest is a central row as a client writes it, plus the
// write-only id of an approved pairing whose token the row takes.
type centralWriteRequest struct {
	sqlite.CentralRow
	PairingID string `json:"pairing_id,omitempty"`
}

// takePairing fills the row's API token (and the pinned fingerprint) from
// an approved pairing, reporting whether the request may go on. The token
// is read here, on the server, and never travels through the client.
func takePairing(w http.ResponseWriter, r *http.Request, o CentralOnboarding, id string, row *sqlite.CentralRow) bool {
	if id == "" {
		return true
	}
	if o == nil {
		problem.Write(w, http.StatusServiceUnavailable, problem.New(problem.TypeServiceUnready, r, "Onboarding unavailable", ""))
		return false
	}
	token, fp, err := o.PairingToken(id)
	if err != nil {
		writeOnboardingError(w, r, "Pairing not usable", err)
		return false
	}
	row.APITokenPlain = token
	if fp != "" {
		row.TLSFingerprint = fp
	}
	return true
}

// writeCentralSecretRefusal answers a store refusal to persist a CCU
// password that cannot be encrypted at rest, reporting whether it handled
// err. The condition is the operator's configuration — no master key plus
// `security.allow_plaintext_secrets: false` — so it is a 400 naming the knob
// to change, not a 500 that invites a retry of a write that will never
// succeed.
func writeCentralSecretRefusal(w http.ResponseWriter, r *http.Request, err error) bool {
	if !errors.Is(err, sqlite.ErrPlaintextSecretNotAllowed) {
		return false
	}
	problem.Write(w, http.StatusBadRequest,
		problem.New(problem.TypeValidation, r, "Password cannot be stored", err.Error()))
	return true
}

// writeCentralSystemRefusal validates the fields whose meaning depends on
// the row's system type and answers 400 naming the offending field. It
// reports whether the row may be persisted. It runs after the masked
// secrets were restored, so the rules see the real token.
func writeCentralSystemRefusal(w http.ResponseWriter, r *http.Request, row sqlite.CentralRow) bool {
	cc := config.CentralConfig{
		Name:                  row.Name,
		SystemType:            hmenum.SystemType(row.SystemType),
		Host:                  row.Host,
		Port:                  row.Port,
		Ports:                 row.Ports,
		Username:              row.Username,
		Password:              row.PasswordPlain,
		Interfaces:            row.Interfaces,
		TLS:                   row.TLS,
		TLSInsecureSkipVerify: row.TLSInsecureSkipVerify,
		APIToken:              row.APITokenPlain,
		TLSFingerprint:        row.TLSFingerprint,
	}
	// A password named by env var counts as a credential for the lite
	// rule that forbids username/password.
	if row.PasswordEnv != "" && cc.Password == "" {
		cc.Password = row.PasswordEnv
	}
	if err := config.ValidateCentralSystemToken(0, &cc, row.APITokenEnv != ""); err != nil {
		problem.Write(w, http.StatusBadRequest,
			problem.New(problem.TypeValidation, r, "Invalid central", err.Error()))
		return false
	}
	return true
}

// ListCentrals handles GET /admin/centrals. Returns every central row
// sorted by name.
func ListCentrals(svc CentralAdminService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rows, err := svc.List(r.Context())
		if err != nil {
			writeServerError(w, r, http.StatusInternalServerError, problem.TypeInternal, "Central list failed", err)
			return
		}
		masked := make([]sqlite.CentralRow, 0, len(rows))
		for i := range rows {
			masked = append(masked, maskCentralRow(r.Context(), rows[i]))
		}
		JSON(w, http.StatusOK, masked)
	}
}

// GetCentral handles GET /admin/centrals/{name}. Returns 404 when the
// central is unknown.
func GetCentral(svc CentralAdminService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := chi.URLParam(r, "name")
		if name == "" {
			problem.Write(w, http.StatusBadRequest,
				problem.New(problem.TypeValidation, r, "Missing name", "name path parameter is required"))
			return
		}
		row, err := svc.Get(r.Context(), name)
		if errors.Is(err, sqlite.ErrCentralNotFound) {
			problem.Write(w, http.StatusNotFound,
				problem.New(problem.TypeNotFound, r, "Central not found", name))
			return
		}
		if err != nil {
			writeServerError(w, r, http.StatusInternalServerError, problem.TypeInternal, "Central lookup failed", err)
			return
		}
		JSON(w, http.StatusOK, maskCentralRow(r.Context(), row))
	}
}

// CreateCentral handles POST /admin/centrals. The request body is a
// [sqlite.CentralRow] JSON object. Returns 201 on success.
func CreateCentral(svc CentralAdminService, rec audit.Recorder, onboarding CentralOnboarding) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req centralWriteRequest
		if err := DecodeJSON(r, &req); err != nil {
			problem.Write(w, DecodeJSONStatus(err),
				problem.New(problem.TypeValidation, r, "Invalid request body", err.Error()))
			return
		}
		row := req.CentralRow
		if row.Name == "" {
			problem.Write(w, http.StatusBadRequest,
				problem.New(problem.TypeValidation, r, "Missing name", "central name is required"))
			return
		}
		// The name becomes a path segment of the callback URL announced to
		// the CCU. Refusing it here is the only place an operator gets an
		// explanation — at callback time the symptom is a CCU that pushes
		// nothing, with nothing pointing at the name.
		if err := hmtypes.ValidateCentralName(row.Name); err != nil {
			problem.Write(w, http.StatusBadRequest,
				problem.New(problem.TypeValidation, r, "Invalid name", err.Error()))
			return
		}
		if row.Host == "" {
			problem.Write(w, http.StatusBadRequest,
				problem.New(problem.TypeValidation, r, "Missing host", "central host is required"))
			return
		}
		// The host goes into every south-bound URL of the central; the
		// config loader applies the same rule to centrals[].host.
		if err := config.ValidateCentralHost(row.Host); err != nil {
			problem.Write(w, http.StatusBadRequest,
				problem.New(problem.TypeValidation, r, "Invalid host", err.Error()))
			return
		}
		// A fresh central has no stored credential to restore; the sentinel
		// is not a real password or token, so drop it rather than persist
		// "***".
		if row.PasswordPlain == maskSentinel {
			row.PasswordPlain = ""
		}
		if row.APITokenPlain == maskSentinel {
			row.APITokenPlain = ""
		}
		if !takePairing(w, r, onboarding, req.PairingID, &row) {
			return
		}
		if !writeCentralSystemRefusal(w, r, row) {
			return
		}
		if err := svc.Put(r.Context(), row); err != nil {
			if writeCentralSecretRefusal(w, r, err) {
				return
			}
			writeServerError(w, r, http.StatusInternalServerError, problem.TypeInternal, "Central creation failed", err)
			return
		}
		if req.PairingID != "" {
			onboarding.ForgetPairing(req.PairingID)
		}
		actor := identityFromCtx(r.Context())
		if rec != nil {
			rec.Record(audit.Entry{
				User:   actor,
				Action: audit.ActionCentralCreate,
				Note:   "name=" + row.Name,
			})
		}
		JSON(w, http.StatusCreated, maskCentralRow(r.Context(), row))
	}
}

// UpdateCentral handles PUT /admin/centrals/{name}. Merges on omit: a
// field the body never mentions keeps its stored value, except
// `enabled` and `interfaces`, which the body must always supply.
// Returns 204 on success.
func UpdateCentral(svc CentralAdminService, rec audit.Recorder, onboarding CentralOnboarding) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := chi.URLParam(r, "name")
		if name == "" {
			problem.Write(w, http.StatusBadRequest,
				problem.New(problem.TypeValidation, r, "Missing name", "name path parameter is required"))
			return
		}
		row, pairingID, present, err := decodeCentralRow(r)
		if err != nil {
			problem.Write(w, DecodeJSONStatus(err),
				problem.New(problem.TypeValidation, r, "Invalid request body", err.Error()))
			return
		}
		// URL path name takes precedence so the body name field cannot
		// accidentally create a different central.
		row.Name = name
		if err := hmtypes.ValidateCentralName(row.Name); err != nil {
			problem.Write(w, http.StatusBadRequest,
				problem.New(problem.TypeValidation, r, "Invalid name", err.Error()))
			return
		}
		if row.Host == "" {
			problem.Write(w, http.StatusBadRequest,
				problem.New(problem.TypeValidation, r, "Missing host", "central host is required"))
			return
		}
		// The host goes into every south-bound URL of the central; the
		// config loader applies the same rule to centrals[].host.
		if err := config.ValidateCentralHost(row.Host); err != nil {
			problem.Write(w, http.StatusBadRequest,
				problem.New(problem.TypeValidation, r, "Invalid host", err.Error()))
			return
		}
		// Unlike the optional fields below, `enabled` and `interfaces` have no
		// "unchanged" fallback to restore, so a body that omits either is
		// rejected rather than silently decoding to the Go zero value —
		// false / nil — which would disable the central and drop every
		// configured interface. `host` above is mandatory for the same reason.
		if !present["enabled"] {
			problem.Write(w, http.StatusBadRequest,
				problem.New(problem.TypeValidation, r, "Missing enabled", "enabled must be supplied: it has no stored value to fall back to"))
			return
		}
		if !present["interfaces"] {
			problem.Write(w, http.StatusBadRequest,
				problem.New(problem.TypeValidation, r, "Missing interfaces", "interfaces must be supplied: it has no stored value to fall back to"))
			return
		}
		// Every other field in sqlite.CentralRow is optional per the
		// published schema, so a key the payload never mentioned must keep
		// its stored value rather than decode to the Go zero value and
		// disappear on this unconditional upsert — the same reasoning
		// [decodeCentralRow] already documents for password_plain, applied
		// to the whole row.
		existing, err := svc.Get(r.Context(), name)
		if err != nil && !errors.Is(err, sqlite.ErrCentralNotFound) {
			writeServerError(w, r, http.StatusInternalServerError, problem.TypeInternal, "Central lookup failed", err)
			return
		}
		overlayOmittedCentralFields(&row, existing, present)
		if !takePairing(w, r, onboarding, pairingID, &row) {
			return
		}
		if !writeCentralSystemRefusal(w, r, row) {
			return
		}
		if err := svc.Put(r.Context(), row); err != nil {
			if writeCentralSecretRefusal(w, r, err) {
				return
			}
			writeServerError(w, r, http.StatusInternalServerError, problem.TypeInternal, "Central update failed", err)
			return
		}
		if pairingID != "" {
			onboarding.ForgetPairing(pairingID)
		}
		actor := identityFromCtx(r.Context())
		if rec != nil {
			rec.Record(audit.Entry{
				User:   actor,
				Action: audit.ActionCentralUpdate,
				Note:   "name=" + name,
			})
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// overlayOmittedCentralFields keeps the stored value of every optional field
// the update payload did not carry — [CentralAdminService.Put] is an
// unconditional upsert, so an omitted key would otherwise decode to the Go
// zero value and disappear. The masked secrets follow their own rule: the
// mask, an absent key and null all mean "unchanged" (see [decodeCentralRow]).
func overlayOmittedCentralFields(row *sqlite.CentralRow, existing sqlite.CentralRow, present map[string]bool) {
	if !present["serial"] {
		row.Serial = existing.Serial
	}
	if !present["port"] {
		row.Port = existing.Port
	}
	if !present["json_rpc_port"] {
		row.JSONRPCPort = existing.JSONRPCPort
	}
	if !present["username"] {
		row.Username = existing.Username
	}
	if !present["password_env"] {
		row.PasswordEnv = existing.PasswordEnv
	}
	if !present["tls"] {
		row.TLS = existing.TLS
	}
	if !present["tls_insecure_skip_verify"] {
		row.TLSInsecureSkipVerify = existing.TLSInsecureSkipVerify
	}
	if !present["primary_interface"] {
		row.PrimaryInterface = existing.PrimaryInterface
	}
	if !present["ports"] {
		row.Ports = existing.Ports
	}
	if !present["visibility"] {
		row.Visibility = existing.Visibility
	}
	if !present["behavior"] {
		row.Behavior = existing.Behavior
	}
	if !present["system_type"] {
		row.SystemType = existing.SystemType
	}
	if !present["api_token_env"] {
		row.APITokenEnv = existing.APITokenEnv
	}
	if !present["tls_fingerprint"] {
		row.TLSFingerprint = existing.TLSFingerprint
	}
	// The GET path masks password_plain to the sentinel and omits it
	// entirely when unset; a save that echoes the sentinel back — or
	// leaves the optional key out — means "unchanged" and must restore
	// the stored credential rather than overwrite it. See
	// [decodeCentralRow] for why the absent key cannot be read as "clear".
	if !present["password_plain"] || row.PasswordPlain == maskSentinel {
		row.PasswordPlain = existing.PasswordPlain
	}
	// The API token follows the password's rule exactly.
	if !present["api_token_plain"] || row.APITokenPlain == maskSentinel {
		row.APITokenPlain = existing.APITokenPlain
	}
}

// DeleteCentral handles DELETE /admin/centrals/{name}. Returns 404
// when the central is unknown.
func DeleteCentral(svc CentralAdminService, rec audit.Recorder) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := chi.URLParam(r, "name")
		if name == "" {
			problem.Write(w, http.StatusBadRequest,
				problem.New(problem.TypeValidation, r, "Missing name", "name path parameter is required"))
			return
		}
		err := svc.Delete(r.Context(), name)
		if errors.Is(err, sqlite.ErrCentralNotFound) {
			problem.Write(w, http.StatusNotFound,
				problem.New(problem.TypeNotFound, r, "Central not found", name))
			return
		}
		if err != nil {
			writeServerError(w, r, http.StatusInternalServerError, problem.TypeInternal, "Central deletion failed", err)
			return
		}
		actor := identityFromCtx(r.Context())
		if rec != nil {
			rec.Record(audit.Entry{
				User:   actor,
				Action: audit.ActionCentralDelete,
				Note:   "name=" + name,
			})
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
