// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package handlers

import (
	"context"
	"errors"
	"net/http"
	"time"
	"unicode"

	"github.com/go-chi/chi/v5"

	"github.com/SukramJ/openccu-loom/internal/audit"
	"github.com/SukramJ/openccu-loom/internal/auth"
	"github.com/SukramJ/openccu-loom/internal/north/rest/problem"
	"github.com/SukramJ/openccu-loom/internal/store/sqlite"
)

// UserAdminService is the DI surface for the /api/v1/admin/users
// endpoints. The router wires a [*sqlite.UserStore] underneath.
type UserAdminService interface {
	// Put creates or updates a user with the given credentials and role.
	// Returns [sqlite.ErrLastAdmin] when the write would demote the only
	// remaining admin.
	Put(ctx context.Context, subject, password string, role auth.Role) error
	// SetRole changes a user's role and keeps the stored password hash.
	// Returns [sqlite.ErrUserNotFound] when the subject is unknown and
	// [sqlite.ErrLastAdmin] when the change would demote the only
	// remaining admin.
	SetRole(ctx context.Context, subject string, role auth.Role) error
	// Delete removes a user. Returns [sqlite.ErrUserNotFound] when the
	// subject is unknown and [sqlite.ErrLastAdmin] when removing the
	// subject would leave zero admins.
	Delete(ctx context.Context, subject string) error
	// List returns every user sorted by subject.
	List(ctx context.Context) ([]sqlite.UserRow, error)
	// Count returns the number of users in the store.
	Count(ctx context.Context) (int, error)
}

// SessionRevoker invalidates server-side sessions for a subject so a
// credential change (password reset, role change, deletion) takes effect
// immediately instead of lingering for the full session TTL.
// *auth.SessionStore satisfies it. A nil revoker disables the hook.
type SessionRevoker interface {
	RevokeBySubject(subject string) int
	RevokeBySubjectExcept(subject, keepSID string) int
}

// TokenPurger deletes every bearer token issued to a subject, called when
// the user account itself is removed. *sqlite.TokenStore satisfies it.
type TokenPurger interface {
	DeleteBySubject(ctx context.Context, subject string) (int, error)
}

// createUserRequest is the body of POST /admin/users.
type createUserRequest struct {
	Username string    `json:"username"`
	Password string    `json:"password"`
	Role     auth.Role `json:"role"`
}

// updateUserRequest is the body of PATCH /admin/users/{subject}.
type updateUserRequest struct {
	Password string    `json:"password"`
	Role     auth.Role `json:"role"`
}

// userSummaryResponse is the response for a successful user creation.
type userSummaryResponse struct {
	Subject   string    `json:"subject"`
	Role      auth.Role `json:"role"`
	CreatedAt time.Time `json:"created_at"`
}

// userListEntry is one element of the GET /admin/users response.
type userListEntry struct {
	Subject    string     `json:"subject"`
	Role       auth.Role  `json:"role"`
	CreatedAt  time.Time  `json:"created_at"`
	LastSeenAt *time.Time `json:"last_seen_at,omitempty"`
}

// validRole reports whether r is one of the three canonical roles.
func validRole(r auth.Role) bool {
	switch r {
	case auth.RoleAdmin, auth.RoleOperator, auth.RoleViewer:
		return true
	}
	return false
}

// validUsername rejects names that could tamper with the key=value shape
// of a free-form audit note or inject log lines. '=' and whitespace are
// the note's field separators; control characters can smuggle newlines
// into the log. Refusing such a username at creation keeps every
// downstream `subject=<name> …` note unambiguous and unspoofable.
func validUsername(name string) bool {
	for _, r := range name {
		if r == '=' || unicode.IsSpace(r) || unicode.IsControl(r) {
			return false
		}
	}
	return true
}

// CreateUser handles POST /admin/users. It creates a user with the
// supplied credentials and returns 201 with a summary.
//
// Create-only: a subject that already exists is refused with 409 rather
// than overwritten. Rewriting an existing account's password and role
// here would skip the session revocation PATCH performs, so a stolen
// cookie carrying the old role would survive the very reset that was
// meant to kill it. Every change to an existing account goes through
// PATCH /admin/users/{subject}.
func CreateUser(svc UserAdminService, rec audit.Recorder) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body createUserRequest
		if err := DecodeJSON(r, &body); err != nil {
			problem.Write(w, DecodeJSONStatus(err),
				problem.New(problem.TypeValidation, r, "Invalid request body", err.Error()))
			return
		}
		if body.Username == "" {
			problem.Write(w, http.StatusBadRequest,
				problem.New(problem.TypeValidation, r, "Missing username", "username is required"))
			return
		}
		if !validUsername(body.Username) {
			problem.Write(w, http.StatusBadRequest,
				problem.New(problem.TypeValidation, r, "Invalid username",
					"username must not contain '=', whitespace, or control characters"))
			return
		}
		if body.Password == "" {
			problem.Write(w, http.StatusBadRequest,
				problem.New(problem.TypeValidation, r, "Missing password", "password is required"))
			return
		}
		if !validRole(body.Role) {
			problem.Write(w, http.StatusBadRequest,
				problem.New(problem.TypeValidation, r, "Invalid role",
					"role must be one of: admin, operator, viewer"))
			return
		}
		// Compare canonically: the store folds the subject before the
		// upsert, so a raw comparison would let "Bob" slip past an
		// existing "bob" and overwrite it.
		subject := auth.CanonicalSubject(body.Username)
		users, err := svc.List(r.Context())
		if err != nil {
			writeServerError(w, r, http.StatusInternalServerError, problem.TypeInternal, "User list failed", err)
			return
		}
		for _, u := range users {
			if auth.CanonicalSubject(u.Subject) == subject {
				problem.Write(w, http.StatusConflict,
					problem.New(problem.TypeConflict, r, "User already exists",
						"a user with this name already exists — change it via PATCH"))
				return
			}
		}
		if err := svc.Put(r.Context(), subject, body.Password, body.Role); err != nil {
			writeServerError(w, r, http.StatusInternalServerError, problem.TypeInternal, "User creation failed", err)
			return
		}
		actor := identityFromCtx(r.Context())
		if rec != nil {
			rec.Record(audit.Entry{
				User:   actor,
				Action: audit.ActionUserCreate,
				Note:   "subject=" + subject + " role=" + string(body.Role),
			})
		}
		JSON(w, http.StatusCreated, userSummaryResponse{
			Subject: subject,
			Role:    body.Role,
		})
	}
}

// UpdateUser handles PATCH /admin/users/{subject}. Both body fields are
// optional and an omitted field leaves that half of the account
// unchanged, as the published request schema states: a body carrying
// only a role changes the role and keeps the stored password hash, a
// body carrying only a password keeps the current role. A body naming
// neither is refused rather than answered with a success that changed
// nothing.
//
// Returns 404 when the subject does not exist and 409 when the change
// would demote the last admin. On success every existing session for the
// subject is revoked so a password reset or role change cannot be
// outrun by a still-cached session. A role that actually changed also
// purges the subject's bearer tokens: a token carries the role it was
// minted with, so leaving it alive would keep the pre-demotion
// privileges usable long after the demotion.
func UpdateUser(svc UserAdminService, rec audit.Recorder, revoker SessionRevoker, tokens TokenPurger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Fold the path parameter to the spelling the store and the
		// session map are keyed on, so the revocation below cannot miss.
		subject := auth.CanonicalSubject(chi.URLParam(r, "subject"))
		if subject == "" {
			problem.Write(w, http.StatusBadRequest,
				problem.New(problem.TypeValidation, r, "Missing subject", "subject path parameter is required"))
			return
		}

		// Verify the user exists before attempting an update.
		users, err := svc.List(r.Context())
		if err != nil {
			writeServerError(w, r, http.StatusInternalServerError, problem.TypeInternal, "User list failed", err)
			return
		}
		currentRole := auth.Role("")
		found := false
		for _, u := range users {
			if auth.CanonicalSubject(u.Subject) == subject {
				currentRole = u.Role
				found = true
				break
			}
		}
		if !found {
			problem.Write(w, http.StatusNotFound,
				problem.New(problem.TypeNotFound, r, "User not found", subject))
			return
		}

		var body updateUserRequest
		if err := DecodeJSON(r, &body); err != nil {
			problem.Write(w, DecodeJSONStatus(err),
				problem.New(problem.TypeValidation, r, "Invalid request body", err.Error()))
			return
		}
		if body.Password == "" && body.Role == "" {
			problem.Write(w, http.StatusBadRequest,
				problem.New(problem.TypeValidation, r, "Nothing to update",
					"at least one of password or role is required"))
			return
		}
		if body.Role != "" && !validRole(body.Role) {
			problem.Write(w, http.StatusBadRequest,
				problem.New(problem.TypeValidation, r, "Invalid role",
					"role must be one of: admin, operator, viewer"))
			return
		}
		// An omitted role keeps the stored one, so a password reset never
		// moves the account to a role the caller did not ask for.
		newRole := body.Role
		if newRole == "" {
			newRole = currentRole
		}

		if body.Password == "" {
			// Role-only: the account keeps the hash it already has. Going
			// through Put would require a password the caller never sent.
			err = svc.SetRole(r.Context(), subject, newRole)
		} else {
			err = svc.Put(r.Context(), subject, body.Password, newRole)
		}
		if err != nil {
			switch {
			case errors.Is(err, sqlite.ErrLastAdmin):
				problem.Write(w, http.StatusConflict,
					problem.New(problem.TypeConflict, r, "Cannot demote last admin",
						"at least one admin user must remain"))
			case errors.Is(err, sqlite.ErrUserNotFound):
				// The row vanished between the lookup above and the write.
				problem.Write(w, http.StatusNotFound,
					problem.New(problem.TypeNotFound, r, "User not found", subject))
			default:
				writeServerError(w, r, http.StatusInternalServerError, problem.TypeInternal, "User update failed", err)
			}
			return
		}
		if revoker != nil {
			revoker.RevokeBySubject(subject)
		}
		if tokens != nil && newRole != currentRole {
			if _, perr := tokens.DeleteBySubject(r.Context(), subject); perr != nil {
				// The role change itself succeeded; a purge miss is logged by
				// the store layer and must not turn it into a 500.
				_ = perr
			}
		}
		actor := identityFromCtx(r.Context())
		if rec != nil {
			rec.Record(audit.Entry{
				User:   actor,
				Action: audit.ActionUserUpdate,
				Note:   "subject=" + subject + " role=" + string(newRole),
			})
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// DeleteUser handles DELETE /admin/users/{subject}. Returns 404 when
// the user does not exist and 409 when removing the subject would
// leave no admins. On success the subject's sessions are revoked and its
// bearer tokens purged so a deleted account retains no live credentials.
func DeleteUser(svc UserAdminService, rec audit.Recorder, revoker SessionRevoker, tokens TokenPurger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Fold the path parameter to the spelling the store, the session
		// map and the token table are keyed on, so neither the revocation
		// nor the token purge below can miss.
		subject := auth.CanonicalSubject(chi.URLParam(r, "subject"))
		if subject == "" {
			problem.Write(w, http.StatusBadRequest,
				problem.New(problem.TypeValidation, r, "Missing subject", "subject path parameter is required"))
			return
		}
		err := svc.Delete(r.Context(), subject)
		if errors.Is(err, sqlite.ErrUserNotFound) {
			problem.Write(w, http.StatusNotFound,
				problem.New(problem.TypeNotFound, r, "User not found", subject))
			return
		}
		if errors.Is(err, sqlite.ErrLastAdmin) {
			problem.Write(w, http.StatusConflict,
				problem.New(problem.TypeConflict, r, "Cannot remove last admin",
					"at least one admin user must remain"))
			return
		}
		if err != nil {
			writeServerError(w, r, http.StatusInternalServerError, problem.TypeInternal, "User deletion failed", err)
			return
		}
		if revoker != nil {
			revoker.RevokeBySubject(subject)
		}
		if tokens != nil {
			if _, err := tokens.DeleteBySubject(r.Context(), subject); err != nil {
				// The account is already gone; a token-purge miss is logged by
				// the store layer and must not turn a successful delete into a
				// 500. Surface nothing to the caller.
				_ = err
			}
		}
		actor := identityFromCtx(r.Context())
		if rec != nil {
			rec.Record(audit.Entry{
				User:   actor,
				Action: audit.ActionUserDelete,
				Note:   "subject=" + subject,
			})
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// ListUsersV2 handles GET /admin/users. Returns every user sorted by
// subject. Password hashes are never included in the response.
func ListUsersV2(svc UserAdminService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rows, err := svc.List(r.Context())
		if err != nil {
			writeServerError(w, r, http.StatusInternalServerError, problem.TypeInternal, "User list failed", err)
			return
		}
		out := make([]userListEntry, 0, len(rows))
		for _, row := range rows {
			out = append(out, userListEntry{
				Subject:    row.Subject,
				Role:       row.Role,
				CreatedAt:  row.CreatedAt,
				LastSeenAt: row.LastSeenAt,
			})
		}
		JSON(w, http.StatusOK, out)
	}
}
