// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package handlers

import (
	"context"
	"errors"
	"math"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/SukramJ/openccu-loom/internal/audit"
	"github.com/SukramJ/openccu-loom/internal/auth"
	"github.com/SukramJ/openccu-loom/internal/north/rest/problem"
	"github.com/SukramJ/openccu-loom/internal/store/sqlite"
)

// TokenAdminService is the DI surface for the /api/v1/admin/auth/tokens
// endpoints. The router wires a [*sqlite.TokenStore] underneath.
type TokenAdminService interface {
	// Create generates a new bearer token. The plaintext token is
	// returned exactly once in [sqlite.CreateResult.Token].
	Create(ctx context.Context, in sqlite.CreateInput) (sqlite.CreateResult, error)
	// Delete removes a token by fingerprint. Returns
	// [sqlite.ErrTokenNotFound] when the fingerprint is unknown.
	Delete(ctx context.Context, fingerprint string) error
	// List returns every token sorted by subject. Plaintext secrets are
	// never surfaced.
	List(ctx context.Context) ([]sqlite.TokenRow, error)
}

// TokenSocketRevoker closes the WebSocket connections a bearer token
// authenticated. Revoking a token stops REST calls immediately because
// every request re-resolves the credential, while a socket resolves it
// once at the upgrade and gates every later command on that snapshot —
// so the revocation only lands on both planes when the socket is torn
// down as well. *ws.Hub satisfies it; a nil revoker disables the hook.
type TokenSocketRevoker interface {
	// CloseByToken closes every connection that authenticated with the
	// token identified by fingerprint and reports how many it closed.
	CloseByToken(fingerprint string) int
}

// createTokenRequest is the body of POST /admin/auth/tokens.
// ExpiresInDays, when set and positive, bounds the token's lifetime; a
// nil or non-positive value creates a token that never expires.
type createTokenRequest struct {
	Subject       string    `json:"subject"`
	Role          auth.Role `json:"role"`
	ExpiresInDays *int      `json:"expires_in_days,omitempty"`
}

// maxTokenExpiryDays is the largest expires_in_days a token can carry.
// The value is a day count multiplied into nanoseconds, and beyond this
// the int64 product wraps negative: time.Add then moves the expiry into
// the past and the endpoint answers 201 with a plaintext credential the
// bearer resolver rejects on first use. Rejecting the input instead keeps
// the failure visible at the point the operator can still fix it. The
// bound is the representable range, not a policy — an operator who wants a
// non-expiring token omits the field.
const maxTokenExpiryDays = int(math.MaxInt64 / int64(24*time.Hour))

// createTokenResponse carries the plaintext token shown exactly once.
type createTokenResponse struct {
	Token       string     `json:"token"`
	Fingerprint string     `json:"fingerprint"`
	Subject     string     `json:"subject"`
	Role        auth.Role  `json:"role"`
	ExpiresAt   *time.Time `json:"expires_at,omitempty"`
}

// tokenListEntry is one element of the GET /admin/auth/tokens response.
// Plaintext secrets are never included.
type tokenListEntry struct {
	Fingerprint string     `json:"fingerprint"`
	Subject     string     `json:"subject"`
	Role        auth.Role  `json:"role"`
	CreatedAt   time.Time  `json:"created_at"`
	LastSeenAt  *time.Time `json:"last_seen_at,omitempty"`
	ExpiresAt   *time.Time `json:"expires_at,omitempty"`
}

// CreateTokenAdmin handles POST /admin/auth/tokens. It generates a fresh
// bearer token and returns the plaintext value exactly once. The
// operator must copy the token immediately — the daemon cannot recover
// it from disk.
func CreateTokenAdmin(svc TokenAdminService, rec audit.Recorder) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body createTokenRequest
		if err := DecodeJSON(r, &body); err != nil {
			problem.Write(w, DecodeJSONStatus(err),
				problem.New(problem.TypeValidation, r, "Invalid request body", err.Error()))
			return
		}
		// Report the spelling the store keys the token on, not the one the
		// operator typed: the response feeds the token list and the audit
		// note, and the identity this token later produces carries the
		// canonical form.
		subject := auth.CanonicalSubject(body.Subject)
		if subject == "" {
			problem.Write(w, http.StatusBadRequest,
				problem.New(problem.TypeValidation, r, "Missing subject", "subject is required"))
			return
		}
		if !validRole(body.Role) {
			problem.Write(w, http.StatusBadRequest,
				problem.New(problem.TypeValidation, r, "Invalid role",
					"role must be one of: admin, operator, viewer"))
			return
		}
		if body.ExpiresInDays != nil && *body.ExpiresInDays > maxTokenExpiryDays {
			problem.Write(w, http.StatusBadRequest,
				problem.New(problem.TypeValidation, r, "Invalid expiry",
					"expires_in_days must not exceed "+itoa(maxTokenExpiryDays)+
						"; omit the field for a token that never expires"))
			return
		}
		var expiresAt *time.Time
		if body.ExpiresInDays != nil && *body.ExpiresInDays > 0 {
			exp := time.Now().UTC().Add(time.Duration(*body.ExpiresInDays) * 24 * time.Hour)
			expiresAt = &exp
		}
		res, err := svc.Create(r.Context(), sqlite.CreateInput{
			Subject:   subject,
			Role:      body.Role,
			ExpiresAt: expiresAt,
		})
		if err != nil {
			writeServerError(w, r, http.StatusInternalServerError, problem.TypeInternal, "Token creation failed", err)
			return
		}
		actor := identityFromCtx(r.Context())
		if rec != nil {
			rec.Record(audit.Entry{
				User:   actor,
				Action: audit.ActionTokenCreate,
				Note:   "subject=" + subject + " role=" + string(body.Role) + " fingerprint=" + res.Fingerprint,
			})
		}
		JSON(w, http.StatusCreated, createTokenResponse{
			Token:       res.Token,
			Fingerprint: res.Fingerprint,
			Subject:     subject,
			Role:        body.Role,
			ExpiresAt:   expiresAt,
		})
	}
}

// DeleteTokenAdmin handles DELETE /admin/auth/tokens/{fingerprint}.
// Returns 404 when the fingerprint is unknown. On success the sockets the
// token opened are closed too, so the revocation reaches the command
// plane and not just REST (see [TokenSocketRevoker]).
func DeleteTokenAdmin(svc TokenAdminService, rec audit.Recorder, sockets TokenSocketRevoker) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		fp := chi.URLParam(r, "fingerprint")
		if fp == "" {
			problem.Write(w, http.StatusBadRequest,
				problem.New(problem.TypeValidation, r, "Missing fingerprint", "fingerprint path parameter is required"))
			return
		}
		err := svc.Delete(r.Context(), fp)
		if errors.Is(err, sqlite.ErrTokenNotFound) {
			problem.Write(w, http.StatusNotFound,
				problem.New(problem.TypeNotFound, r, "Token not found", fp))
			return
		}
		if err != nil {
			writeServerError(w, r, http.StatusInternalServerError, problem.TypeInternal, "Token deletion failed", err)
			return
		}
		if sockets != nil {
			sockets.CloseByToken(fp)
		}
		actor := identityFromCtx(r.Context())
		if rec != nil {
			rec.Record(audit.Entry{
				User:   actor,
				Action: audit.ActionTokenRevoke,
				Note:   "fingerprint=" + fp,
			})
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// ListTokensV2 handles GET /admin/auth/tokens. Returns every token
// sorted by subject. Plaintext secrets are never included.
func ListTokensV2(svc TokenAdminService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rows, err := svc.List(r.Context())
		if err != nil {
			writeServerError(w, r, http.StatusInternalServerError, problem.TypeInternal, "Token list failed", err)
			return
		}
		out := make([]tokenListEntry, 0, len(rows))
		for _, row := range rows {
			out = append(out, tokenListEntry{
				Fingerprint: row.Fingerprint,
				Subject:     row.Subject,
				Role:        row.Role,
				CreatedAt:   row.CreatedAt,
				LastSeenAt:  row.LastSeenAt,
				ExpiresAt:   row.ExpiresAt,
			})
		}
		JSON(w, http.StatusOK, out)
	}
}
