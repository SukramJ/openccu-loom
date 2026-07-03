// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package handlers

import (
	"context"
	"errors"
	"net/http"

	"github.com/SukramJ/openccu-loom/internal/audit"
	"github.com/SukramJ/openccu-loom/internal/auth"
	"github.com/SukramJ/openccu-loom/internal/north/rest/problem"
	"github.com/SukramJ/openccu-loom/internal/store/sqlite"
)

// SelfPasswordService is the narrow surface the self-service password
// endpoint needs: verify the caller's current credentials and write a
// new password while preserving the existing role. *sqlite.UserStore
// satisfies it directly.
type SelfPasswordService interface {
	// AuthenticateBasic resolves a username/password pair to an
	// identity (carrying the stored role) or an error.
	AuthenticateBasic(ctx context.Context, username, password string) (auth.Identity, error)
	// Put upserts the user with a new password and role.
	Put(ctx context.Context, subject, password string, role auth.Role) error
}

// changeOwnPasswordRequest is the body of PATCH /auth/me/password.
type changeOwnPasswordRequest struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
}

// ChangeOwnPassword lets a logged-in local user rotate their own
// password. Unlike PATCH /users/{subject} (admin-only, role-changing)
// this requires no admin role but does require the caller to prove
// knowledge of the current password, and it never changes the role.
//
// Accounts without a local password (OIDC / bearer-token identities)
// have no row to verify against and receive 409.
//
// On a successful change every *other* session for the caller is revoked
// (the caller's own session is preserved) so a change made in response to
// a suspected compromise immediately invalidates any parallel stolen
// session instead of letting it live out the session TTL.
func ChangeOwnPassword(svc SelfPasswordService, rec audit.Recorder, revoker SessionRevoker) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			problem.Write(w, http.StatusServiceUnavailable,
				problem.New(problem.TypeServiceUnready, r, "user store unwired", ""))
			return
		}
		ident, ok := auth.IdentityFrom(r.Context())
		if !ok || ident.Subject == "" {
			problem.Write(w, http.StatusUnauthorized,
				problem.New(problem.TypeUnauthorized, r, "Not authenticated", ""))
			return
		}

		var body changeOwnPasswordRequest
		if err := DecodeJSON(r, &body); err != nil {
			problem.Write(w, http.StatusBadRequest,
				problem.New(problem.TypeValidation, r, "Invalid request body", err.Error()))
			return
		}
		if body.CurrentPassword == "" || body.NewPassword == "" {
			problem.Write(w, http.StatusBadRequest,
				problem.New(problem.TypeValidation, r, "Missing password",
					"current_password and new_password are required"))
			return
		}

		// Verify the current password. This also yields the stored role,
		// which we preserve on the write so a self-service change can
		// never escalate or drop the caller's privileges.
		verified, err := svc.AuthenticateBasic(r.Context(), ident.Subject, body.CurrentPassword)
		if errors.Is(err, sqlite.ErrUserNotFound) {
			problem.Write(w, http.StatusConflict,
				problem.New(problem.TypeConflict, r, "No local password",
					"this account has no local password to change"))
			return
		}
		if err != nil {
			problem.Write(w, http.StatusForbidden,
				problem.New(problem.TypeForbidden, r, "Current password incorrect", ""))
			return
		}

		if err := svc.Put(r.Context(), ident.Subject, body.NewPassword, verified.Role); err != nil {
			problem.Write(w, http.StatusInternalServerError,
				problem.New(problem.TypeInternal, r, "Password change failed", err.Error()))
			return
		}
		if revoker != nil {
			keepSID := ""
			if c, err := r.Cookie(auth.SessionCookieName); err == nil {
				keepSID = c.Value
			}
			revoker.RevokeBySubjectExcept(ident.Subject, keepSID)
		}
		if rec != nil {
			rec.Record(audit.Entry{
				User:   ident.Subject,
				Action: audit.ActionUserUpdate,
				Note:   "self password change",
			})
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
