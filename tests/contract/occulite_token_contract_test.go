// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package contract

import (
	"net/http"
	"testing"

	"github.com/SukramJ/godevccu/pkg/litefake"

	"github.com/SukramJ/openccu-loom/internal/auth"
)

// A box API token the gate accepted for this add-on (ADR 0080), pinned end
// to end against the fake box: the box's open state route answers for the
// token with a "token:<name>" user, its stored scopes and no role, and the
// production resolver turns that into a token identity only for this
// add-on's gate scope or Full access.

// Box tokens: "olt_" and 32 lower-case hex, the shape the resolver accepts.
const (
	boxTokAddon   = "olt_a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0"
	boxTokFull    = "olt_b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0"
	boxTokOther   = "olt_c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0"
	boxTokAPIOnly = "olt_d0d0d0d0d0d0d0d0d0d0d0d0d0d0d0d0"
	// boxTokUnissued has the token shape but is unknown to the box.
	boxTokUnissued = "olt_e0e0e0e0e0e0e0e0e0e0e0e0e0e0e0e0"
	// boxAddonScope is the gate scope the trust names for the add-on.
	boxAddonScope = "addon:openccu-loom"
)

// TestOcculiteTokenAgainstLiteFake pins ADR 0080 against the fake box: a box token resolves to operator with the add-on's scope and to admin with Full access, and to nothing for another add-on's scope, API-only scopes, an unissued token or an auth-off answer.
func TestOcculiteTokenAgainstLiteFake(t *testing.T) {
	f := startLiteFake(t, litefake.Options{
		Tokens: map[string][]string{
			boxTokAddon:   {boxAddonScope},
			boxTokFull:    {"*"},
			boxTokOther:   {"addon:redmatic"},
			boxTokAPIOnly: {"system:write", "rpc:admin"},
		},
	})
	box := liteClient(t, f, "")

	var seen auth.Identity
	probe := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if id, ok := auth.IdentityFrom(r.Context()); ok {
			seen = id
		}
		w.WriteHeader(http.StatusNoContent)
	})
	mw := auth.OcculiteSSOPassthrough(auth.OcculiteSSOTrust{
		Enabled: true, Verifier: ssoVerifier{c: box}, AddonScope: boxAddonScope,
	}, nil)(probe)

	for _, tc := range []struct {
		name, tok string
		role      auth.Role // empty: no identity
	}{
		{"add-on scope resolves to operator", boxTokAddon, auth.RoleOperator},
		{"full access resolves to admin", boxTokFull, auth.RoleAdmin},
		{"another add-on's token resolves nothing", boxTokOther, ""},
		{"an API-only token resolves nothing", boxTokAPIOnly, ""},
		{"a token the box never issued resolves nothing", boxTokUnissued, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			id, ok := ssoProbe(t, mw, &seen, tc.tok)
			if tc.role == "" {
				if ok {
					t.Fatalf("identity %+v, want none", id)
				}
				return
			}
			want := auth.Identity{
				Subject: "occulite-token:" + litefake.TokenName(tc.tok),
				Scheme:  auth.SchemeOcculiteToken,
				Role:    tc.role,
			}
			if !ok || id != want {
				t.Fatalf("identity = %+v (resolved %v), want %+v", id, ok, want)
			}
		})
	}

	t.Run("auth-off box confirms no token", func(t *testing.T) {
		// A token the resolver has not cached yet, so the verdict must come
		// from the box's auth-off state rather than an earlier answer.
		const fresh = "olt_f0f0f0f0f0f0f0f0f0f0f0f0f0f0f0f0"
		f.SetTokens(map[string][]string{fresh: {"*"}})
		f.SetAuthOff(true)
		t.Cleanup(func() { f.SetAuthOff(false) })
		if id, ok := ssoProbe(t, mw, &seen, fresh); ok {
			t.Fatalf("identity %+v from an auth-off answer that is not about the presented token", id)
		}
	})
}
