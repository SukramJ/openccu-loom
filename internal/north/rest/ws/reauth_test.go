// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package ws

import (
	"context"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/auth"
)

// reauthFakeTokens is a minimal in-test TokenStore: token "valid"
// returns a known identity, anything else returns ErrUnauthenticated.
type reauthFakeTokens struct {
	id auth.Identity
}

func (s reauthFakeTokens) AuthenticateToken(_ context.Context, token string) (auth.Identity, error) {
	if token == "valid" {
		return s.id, nil
	}
	return auth.Identity{}, auth.ErrUnauthenticated
}

func TestHubSetTokenStore_NilByDefault(t *testing.T) {
	t.Parallel()
	h := NewHub()
	if h.TokenStore() != nil {
		t.Fatal("fresh hub should have no token store")
	}
}

func TestHubSetTokenStore_RoundTrip(t *testing.T) {
	t.Parallel()
	h := NewHub()
	store := reauthFakeTokens{id: auth.Identity{Subject: "alice", Role: auth.RoleAdmin}}
	h.SetTokenStore(store)
	if h.TokenStore() == nil {
		t.Fatal("SetTokenStore did not wire the store")
	}
}

func TestClient_SetAndGetIdentity(t *testing.T) {
	t.Parallel()
	c := &client{}
	id := auth.Identity{Subject: "bob", Role: auth.RoleOperator}
	c.SetIdentity(id)
	if got := c.Identity(); got.Subject != "bob" || got.Role != auth.RoleOperator {
		t.Fatalf("Identity round-trip failed: %+v", got)
	}
}
