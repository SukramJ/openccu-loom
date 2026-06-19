// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package ui

import (
	"log/slog"
	"net/http"

	"github.com/SukramJ/openccu-loom/internal/auth"
)

// AuthDeps bundles the session-auth backing stores.
type AuthDeps struct {
	Users    *auth.MemoryUserStore
	Sessions *auth.SessionStore
	Secure   bool // Secure-cookie flag
}

// handleLoginPost authenticates a POST login form. On success it
// issues a session cookie and redirects to `/`; on failure the
// response is a redirect back to `/login?error=1`.
func handleLoginPost(_ Deps, ad *AuthDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if ad == nil || ad.Users == nil || ad.Sessions == nil {
			http.Error(w, "auth not configured", http.StatusServiceUnavailable)
			return
		}
		// Cap the form body before parsing — a malicious POST with
		// a multi-GB body would otherwise pin one goroutine in
		// r.ParseForm. 64 KiB is far above any legitimate login
		// payload.
		r.Body = http.MaxBytesReader(w, r.Body, 64*1024)
		if err := r.ParseForm(); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		user := r.FormValue("username")
		pass := r.FormValue("password")
		id, err := ad.Users.AuthenticateBasic(r.Context(), user, pass)
		if err != nil {
			slog.WarnContext(r.Context(), "auth.login.fail",
				slog.String("subject", user),
				slog.String("remote", r.RemoteAddr),
				slog.String("err", err.Error()))
			http.Redirect(w, r, "/login?error=1", http.StatusSeeOther)
			return
		}
		sess, err := ad.Sessions.Issue(id) //nolint:contextcheck // session persist detaches from the request ctx by design (best-effort durability); see ADR 0041
		if err != nil {
			slog.ErrorContext(r.Context(), "auth.session.issue_fail",
				slog.String("subject", id.Subject),
				slog.String("err", err.Error()))
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		slog.InfoContext(r.Context(), "auth.login.ok",
			slog.String("subject", id.Subject),
			slog.String("role", string(id.Role)),
			slog.String("remote", r.RemoteAddr))
		auth.WriteSessionCookie(w, sess, ad.Secure)
		http.Redirect(w, r, "/", http.StatusSeeOther)
	}
}

// handleLogoutPost clears the session cookie and returns the user
// to `/login`.
func handleLogoutPost(_ Deps, ad *AuthDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if c, err := r.Cookie(auth.SessionCookieName); err == nil && ad != nil && ad.Sessions != nil {
			ad.Sessions.Revoke(c.Value) //nolint:contextcheck // session persist detaches from the request ctx by design (best-effort durability); see ADR 0041
			slog.InfoContext(r.Context(), "auth.logout",
				slog.String("remote", r.RemoteAddr))
		}
		auth.ClearSessionCookie(w)
		http.Redirect(w, r, "/login", http.StatusSeeOther)
	}
}

// handleSetupPost handles the first-run admin bootstrap. Refuses to
// run once *any* user exists; that single-shot check matches spec
// §17.2's setup wizard semantics.
func handleSetupPost(_ Deps, ad *AuthDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if ad == nil || ad.Users == nil {
			http.Error(w, "auth not configured", http.StatusServiceUnavailable)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 64*1024)
		if err := r.ParseForm(); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		user := r.FormValue("username")
		pass := r.FormValue("password")
		confirm := r.FormValue("confirm")
		if user == "" || pass == "" || pass != confirm {
			http.Redirect(w, r, "/setup?error=1", http.StatusSeeOther)
			return
		}
		hashed, err := auth.HashPassword(pass)
		if err != nil {
			http.Redirect(w, r, "/setup?error=1", http.StatusSeeOther)
			return
		}
		ad.Users.Put(user, hashed, auth.RoleAdmin)
		slog.InfoContext(r.Context(), "auth.setup.admin_created",
			slog.String("subject", user),
			slog.String("remote", r.RemoteAddr))
		http.Redirect(w, r, "/login", http.StatusSeeOther)
	}
}
