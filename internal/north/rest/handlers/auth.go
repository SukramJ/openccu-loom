// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package handlers

import (
	"crypto/rand"
	"encoding/base64"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/SukramJ/openccu-loom/internal/audit"
	"github.com/SukramJ/openccu-loom/internal/auth"
	"github.com/SukramJ/openccu-loom/internal/north/rest/problem"
)

// AuthDeps bundles the stores needed by the /api/v1/auth endpoints.
// Nil means "auth is not configured" — the handlers return 503 so
// SPA flows can fail clearly rather than appearing broken.
type AuthDeps struct {
	Users    *auth.MemoryUserStore
	Sessions *auth.SessionStore
	Tokens   *auth.MemoryTokenStore
	Secure   bool
	// AuditRecorder receives an audit entry whenever a token is
	// created or revoked. Nil disables auditing for those flows;
	// the daemon's composition root wires the same Recorder as
	// every other admin-grade mutation surface.
	AuditRecorder audit.Recorder
	// LoginUsers, when non-nil, overrides the concrete Users
	// store for the [Login] handler. Wired by the daemon to the
	// chained SQLite+Memory store so wizard-created
	// admins can sign in. When nil, [Login] falls back to the
	// concrete [auth.MemoryUserStore] field above.
	LoginUsers auth.UserStore
}

// loginRequest is the JSON payload the SPA POSTs to /api/v1/auth/login.
type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// UserListEntry is one entry of `GET /api/v1/auth/users`.
type UserListEntry struct {
	Username string `json:"username"`
	Role     string `json:"role"`
}

// ListUsers renders the registered Basic-auth users as a read-only
// fallback. Admin-only — the SPA gates the route accordingly.
// The live-edit path (add / update / delete) is the UserAdmin-backed
// /users CRUD mounted when a UserAdmin store is wired; this handler
// serves the legacy in-memory store when no UserAdmin is present.
func ListUsers(d *AuthDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d == nil || d.Users == nil {
			problem.Write(w, http.StatusServiceUnavailable,
				problem.New(problem.TypeServiceUnready, r, "Auth not configured", "no user store"))
			return
		}
		entries := d.Users.List()
		out := make([]UserListEntry, 0, len(entries))
		for _, u := range entries {
			out = append(out, UserListEntry{Username: u.Username, Role: string(u.Role)})
		}
		JSON(w, http.StatusOK, out)
	}
}

// TokenListEntry is one entry of `GET /api/v1/auth/tokens`.
type TokenListEntry struct {
	ID          string `json:"id"`
	Fingerprint string `json:"fingerprint"`
	Subject     string `json:"subject"`
	Role        string `json:"role"`
}

// ListTokens renders every registered API-token. Admin-only;
// fingerprints elide the actual secret so the list is safe to
// surface in the SPA.
func ListTokens(d *AuthDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d == nil || d.Tokens == nil {
			JSON(w, http.StatusOK, []TokenListEntry{})
			return
		}
		entries := d.Tokens.List()
		out := make([]TokenListEntry, 0, len(entries))
		for _, t := range entries {
			out = append(out, TokenListEntry{
				ID:          t.ID,
				Fingerprint: t.Fingerprint,
				Subject:     t.Subject,
				Role:        string(t.Role),
			})
		}
		JSON(w, http.StatusOK, out)
	}
}

// CreateTokenRequest is the body of `POST /api/v1/auth/tokens`.
type CreateTokenRequest struct {
	Subject string `json:"subject"`
	Role    string `json:"role"`
}

// CreateTokenResponse carries the freshly-minted token. The raw
// `token` value is returned **only on creation** — subsequent List /
// audit operations elide it. Clients must store the token immediately;
// the daemon cannot reissue it.
type CreateTokenResponse struct {
	ID          string `json:"id"`
	Token       string `json:"token"`
	Fingerprint string `json:"fingerprint"`
	Subject     string `json:"subject"`
	Role        string `json:"role"`
}

// CreateToken issues a new bearer token bound to the requested
// subject + role. Admin-only. The generated token is 32 random
// bytes encoded as URL-safe base64 (~43 characters) — high entropy,
// shell-friendly.
func CreateToken(d *AuthDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d == nil || d.Tokens == nil {
			problem.Write(w, http.StatusServiceUnavailable,
				problem.New(problem.TypeServiceUnready, r, "Auth not configured", "no token store"))
			return
		}
		var req CreateTokenRequest
		if err := DecodeJSON(r, &req); err != nil {
			problem.Write(w, DecodeJSONStatus(err),
				problem.New(problem.TypeBadRequest, r, "Invalid body", err.Error()))
			return
		}
		req.Subject = strings.TrimSpace(req.Subject)
		req.Role = strings.TrimSpace(req.Role)
		if req.Subject == "" {
			problem.Write(w, http.StatusUnprocessableEntity,
				problem.New(problem.TypeValidation, r, "subject required", "subject must not be empty"))
			return
		}
		role := auth.Role(req.Role)
		if role != auth.RoleViewer && role != auth.RoleOperator && role != auth.RoleAdmin {
			problem.Write(w, http.StatusUnprocessableEntity,
				problem.New(problem.TypeValidation, r, "invalid role", "role must be one of: viewer, operator, admin"))
			return
		}
		token, err := generateBearerToken()
		if err != nil {
			writeServerError(w, r, http.StatusInternalServerError, problem.TypeInternal, "Token generation failed", err)
			return
		}
		id := d.Tokens.Put(token, auth.Identity{Subject: req.Subject, Role: role, Scheme: auth.SchemeBearer})
		fp := "…" + token[len(token)-6:]
		recordTokenAudit(d.AuditRecorder, r, audit.ActionTokenCreate, req.Subject, string(role), id)
		JSON(w, http.StatusCreated, CreateTokenResponse{
			ID:          id,
			Token:       token,
			Fingerprint: fp,
			Subject:     req.Subject,
			Role:        string(role),
		})
	}
}

// DeleteToken revokes the token identified by the URL path segment.
// Admin-only. Returns 204 on success, 404 problem+json when no token
// matches the supplied ID.
func DeleteToken(d *AuthDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d == nil || d.Tokens == nil {
			problem.Write(w, http.StatusServiceUnavailable,
				problem.New(problem.TypeServiceUnready, r, "Auth not configured", "no token store"))
			return
		}
		id := strings.TrimSpace(chi.URLParam(r, "id"))
		if id == "" {
			problem.Write(w, http.StatusBadRequest,
				problem.New(problem.TypeBadRequest, r, "Missing id", "path parameter id is required"))
			return
		}
		if !d.Tokens.DeleteByID(id) {
			problem.Write(w, http.StatusNotFound,
				problem.New(problem.TypeNotFound, r, "Token not found", "no token registered with that id"))
			return
		}
		recordTokenAudit(d.AuditRecorder, r, audit.ActionTokenRevoke, "", "", id)
		w.WriteHeader(http.StatusNoContent)
	}
}

// recordTokenAudit emits an audit entry for token CRUD events. The
// actor comes from the request's [auth.Identity]; the target token
// is identified by its stable id (never the raw secret). Subject and
// role land in Note for the create path; revoke carries only the id.
// Nil rec is a no-op so tests and bootstrap paths can pass nil
// without ceremony.
func recordTokenAudit(rec audit.Recorder, r *http.Request, action audit.Action, subject, role, id string) {
	if rec == nil {
		return
	}
	user := ""
	if r != nil {
		if ident, ok := auth.IdentityFrom(r.Context()); ok {
			user = ident.Subject
		}
	}
	var note string
	switch {
	case subject != "" && role != "":
		note = "subject=" + subject + " role=" + role + " id=" + id
	default:
		note = "id=" + id
	}
	rec.Record(audit.Entry{
		Timestamp: time.Now().UTC(),
		User:      user,
		Action:    action,
		Note:      note,
	})
}

// generateBearerToken returns a freshly-generated, URL-safe bearer
// token: 32 random bytes (256 bits of entropy) encoded as base64-URL
// without padding (~43 chars).
func generateBearerToken() (string, error) {
	var buf [32]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf[:]), nil
}

// meResponse describes the currently-authenticated identity.
type meResponse struct {
	Subject string `json:"subject"`
	Role    string `json:"role"`
	Scheme  string `json:"scheme,omitempty"`
}

// Login authenticates the supplied credentials against the user store
// and, on success, issues a session cookie the browser uses for every
// subsequent REST + SPA request.
func Login(d *AuthDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d == nil || d.Sessions == nil {
			problem.Write(w, http.StatusServiceUnavailable,
				problem.New(problem.TypeServiceUnready, r, "Auth not configured", "no session store"))
			return
		}
		resolver := d.LoginUsers
		if resolver == nil {
			if d.Users == nil {
				problem.Write(w, http.StatusServiceUnavailable,
					problem.New(problem.TypeServiceUnready, r, "Auth not configured", "no user store"))
				return
			}
			resolver = d.Users
		}
		var req loginRequest
		if err := DecodeJSON(r, &req); err != nil {
			problem.Write(w, DecodeJSONStatus(err),
				problem.New(problem.TypeBadRequest, r, "Invalid JSON", err.Error()))
			return
		}
		id, err := resolver.AuthenticateBasic(r.Context(), req.Username, req.Password)
		if err != nil {
			problem.Write(w, http.StatusUnauthorized,
				problem.New(problem.TypeUnauthorized, r, "Invalid credentials", "login refused"))
			return
		}
		sess, err := d.Sessions.Issue(id) //nolint:contextcheck // session persist detaches from the request ctx by design (best-effort durability); see ADR 0041
		if err != nil {
			writeServerError(w, r, http.StatusInternalServerError, problem.TypeInternal, "Session issue failed", err)
			return
		}
		auth.WriteSessionCookie(w, sess, d.Secure)
		JSON(w, http.StatusOK, meResponse{
			Subject: id.Subject,
			Role:    string(id.Role),
			Scheme:  string(id.Scheme),
		})
	}
}

// Logout revokes the current session and clears the cookie.
func Logout(d *AuthDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if c, err := r.Cookie(auth.SessionCookieName); err == nil && d != nil && d.Sessions != nil {
			d.Sessions.Revoke(c.Value) //nolint:contextcheck // session persist detaches from the request ctx by design (best-effort durability); see ADR 0041
		}
		auth.ClearSessionCookie(w)
		w.WriteHeader(http.StatusNoContent)
	}
}

// Me reports the authenticated identity. 401 when the request carries
// no resolved identity — useful for the SPA to probe "am I logged in?"
// without tripping a redirect loop.
func Me() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := auth.IdentityFrom(r.Context())
		if !ok {
			problem.Write(w, http.StatusUnauthorized,
				problem.New(problem.TypeUnauthorized, r, "Not authenticated", "no active session"))
			return
		}
		JSON(w, http.StatusOK, meResponse{
			Subject: id.Subject,
			Role:    string(id.Role),
			Scheme:  string(id.Scheme),
		})
	}
}
