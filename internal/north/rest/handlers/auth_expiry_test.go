// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/auth"
)

// A long-lived consumer resolves its credential once and keeps the snapshot:
// a WebSocket captures its identity at the upgrade and is closed the instant
// the deadline passes. These guards pin the only two places the daemon tells
// such a client when that will be. Without them the rotation can only be
// discovered through the resulting 401, after the connection is already gone.

// TestMeReportsCredentialExpiry pins the deadline onto GET /auth/me for a
// credential that has one, and pins its ABSENCE for one that does not — the
// negative half matters just as much, because a zero time.Time would marshal
// as 0001-01-01 and read as long expired.
func TestMeReportsCredentialExpiry(t *testing.T) {
	t.Parallel()
	deadline := time.Date(2026, 9, 1, 12, 30, 0, 0, time.UTC)

	cases := []struct {
		name     string
		identity auth.Identity
		want     *time.Time
	}{
		{
			name: "session carries its deadline",
			identity: auth.Identity{
				Subject: "markus", Role: auth.RoleAdmin,
				Scheme: auth.SchemeSession, ExpiresAt: deadline,
			},
			want: &deadline,
		},
		{
			name: "bounded bearer token carries its deadline",
			identity: auth.Identity{
				Subject: "ha-bridge", Role: auth.RoleOperator,
				Scheme: auth.SchemeBearer, TokenID: "tok-1", ExpiresAt: deadline,
			},
			want: &deadline,
		},
		{
			name: "unbounded bearer token omits the field",
			identity: auth.Identity{
				Subject: "ha-bridge", Role: auth.RoleOperator,
				Scheme: auth.SchemeBearer, TokenID: "tok-2",
			},
			want: nil,
		},
		{
			name: "basic auth omits the field",
			identity: auth.Identity{
				Subject: "markus", Role: auth.RoleAdmin, Scheme: auth.SchemeBasic,
			},
			want: nil,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", http.NoBody)
			req = req.WithContext(auth.ContextWithIdentity(req.Context(), tc.identity))
			w := httptest.NewRecorder()
			Me().ServeHTTP(w, req)

			if w.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
			}

			// Decode into a map as well as the struct: only the raw JSON can
			// tell "field absent" from "field present and zero", which is the
			// distinction the whole contract rests on.
			var raw map[string]any
			if err := json.Unmarshal(w.Body.Bytes(), &raw); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			got, present := raw["expires_at"]

			if tc.want == nil {
				if present {
					t.Fatalf("expires_at = %v, want the field absent — a credential "+
						"without a server-side deadline must not appear to have one", got)
				}
				return
			}
			if !present {
				t.Fatalf("expires_at absent, want %s — the client cannot refill a "+
					"credential whose deadline it is never told", tc.want.Format(time.RFC3339))
			}
			var resp meResponse
			if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
				t.Fatalf("unmarshal into meResponse: %v", err)
			}
			if resp.ExpiresAt == nil || !resp.ExpiresAt.Equal(*tc.want) {
				t.Errorf("expires_at = %v, want %s", resp.ExpiresAt, tc.want.Format(time.RFC3339))
			}
		})
	}
}

// TestLoginReportsSessionExpiry pins the deadline onto the login response.
// The identity the user store returns carries none — AuthenticateBasic
// resolves a credential, it does not mint one — so the handler has to read
// the session it just issued. Getting this wrong is silent: the field simply
// goes missing, which the contract defines as "never expires".
func TestLoginReportsSessionExpiry(t *testing.T) {
	t.Parallel()
	users := auth.NewMemoryUserStore()
	users.Put("markus", "correct-horse-battery", auth.RoleAdmin)
	sessions := auth.NewSessionStore()
	ttl := sessions.TTL
	d := &AuthDeps{Users: users, Sessions: sessions}

	before := time.Now()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login",
		strings.NewReader(`{"username":"markus","password":"correct-horse-battery"}`))
	w := httptest.NewRecorder()
	Login(d).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	var resp meResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.ExpiresAt == nil {
		t.Fatal("expires_at absent — the response announces a session the client " +
			"cannot know the lifetime of")
	}
	// The store stamps now+TTL at issue time; bound it rather than pin it so
	// the guard measures the wiring, not the clock.
	if resp.ExpiresAt.Before(before.Add(ttl)) || resp.ExpiresAt.After(time.Now().Add(ttl)) {
		t.Errorf("expires_at = %s, want roughly %s (TTL %s)",
			resp.ExpiresAt.Format(time.RFC3339), before.Add(ttl).Format(time.RFC3339), ttl)
	}
}
