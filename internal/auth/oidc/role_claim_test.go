// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package oidc

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/auth"
)

// TestIdentityFromDefaultRoleClaim covers the default "role" claim: a plain
// string value at the top level of the raw claim set.
func TestIdentityFromDefaultRoleClaim(t *testing.T) {
	c := &Client{}
	cases := []struct {
		name string
		raw  map[string]any
		want auth.Role
	}{
		{"admin", map[string]any{"role": "admin"}, auth.RoleAdmin},
		{"operator", map[string]any{"role": "operator"}, auth.RoleOperator},
		{"viewer", map[string]any{"role": "viewer"}, auth.RoleViewer},
		{"unmapped", map[string]any{"role": "nope"}, auth.RoleViewer},
		{"empty raw", map[string]any{}, auth.RoleViewer},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			id := c.IdentityFrom(&IDClaims{Raw: tc.raw, Subject: "s"})
			if id.Role != tc.want {
				t.Fatalf("role = %v, want %v", id.Role, tc.want)
			}
		})
	}
}

// TestIdentityFromTypedFieldFallback proves that when Raw is nil (a caller
// builds IDClaims directly, e.g. tests or pre-Raw call sites) the resolver
// falls back to the typed Role / Roles fields instead of yielding Viewer.
func TestIdentityFromTypedFieldFallback(t *testing.T) {
	c := &Client{}

	id := c.IdentityFrom(&IDClaims{Subject: "s", Role: "admin"})
	if id.Role != auth.RoleAdmin {
		t.Fatalf("typed Role fallback: got %v, want RoleAdmin", id.Role)
	}

	id = c.IdentityFrom(&IDClaims{Subject: "s", Roles: []any{"operator"}})
	if id.Role != auth.RoleOperator {
		t.Fatalf("typed Roles fallback: got %v, want RoleOperator", id.Role)
	}
}

// TestIdentityFromArrayClaimPrecedence covers a custom RoleClaim ("groups")
// resolving against a top-level string array, and proves the
// highest-privilege match wins regardless of array order.
func TestIdentityFromArrayClaimPrecedence(t *testing.T) {
	c := &Client{cfg: Config{RoleClaim: "groups"}}
	cases := []struct {
		name string
		raw  map[string]any
		want auth.Role
	}{
		{"operator among users", map[string]any{"groups": []any{"users", "operator"}}, auth.RoleOperator},
		{"admin after operator", map[string]any{"groups": []any{"operator", "admin"}}, auth.RoleAdmin},
		{"admin before operator", map[string]any{"groups": []any{"admin", "operator"}}, auth.RoleAdmin},
		{"no privileged group", map[string]any{"groups": []any{"users"}}, auth.RoleViewer},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			id := c.IdentityFrom(&IDClaims{Raw: tc.raw, Subject: "s"})
			if id.Role != tc.want {
				t.Fatalf("role = %v, want %v", id.Role, tc.want)
			}
		})
	}
}

// TestIdentityFromNestedClaimPath covers a dotted RoleClaim path
// ("realm_access.roles"), the shape Keycloak publishes, including the case
// where the path is missing or an intermediate segment is not an object.
func TestIdentityFromNestedClaimPath(t *testing.T) {
	c := &Client{cfg: Config{RoleClaim: "realm_access.roles"}}
	cases := []struct {
		name string
		raw  map[string]any
		want auth.Role
	}{
		{"admin", map[string]any{"realm_access": map[string]any{"roles": []any{"admin"}}}, auth.RoleAdmin},
		{"operator", map[string]any{"realm_access": map[string]any{"roles": []any{"operator"}}}, auth.RoleOperator},
		{"missing path", map[string]any{}, auth.RoleViewer},
		{"intermediate not object", map[string]any{"realm_access": "x"}, auth.RoleViewer},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			id := c.IdentityFrom(&IDClaims{Raw: tc.raw, Subject: "s"})
			if id.Role != tc.want {
				t.Fatalf("role = %v, want %v", id.Role, tc.want)
			}
		})
	}
}

// TestIdentityFromAdministratorAlias proves the "administrator" spelling
// maps to RoleAdmin alongside the shorter "admin" alias.
func TestIdentityFromAdministratorAlias(t *testing.T) {
	c := &Client{}
	id := c.IdentityFrom(&IDClaims{Raw: map[string]any{"role": "administrator"}, Subject: "s"})
	if id.Role != auth.RoleAdmin {
		t.Fatalf("role = %v, want RoleAdmin", id.Role)
	}
}

// TestIdentityFromSubjectPreference proves PreferredUser is used as the
// identity subject when present, and Subject is the fallback otherwise.
func TestIdentityFromSubjectPreference(t *testing.T) {
	c := &Client{}

	id := c.IdentityFrom(&IDClaims{PreferredUser: "alice", Subject: "sub-123"})
	if id.Subject != "alice" {
		t.Fatalf("subject = %q, want preferred_username to win", id.Subject)
	}

	id = c.IdentityFrom(&IDClaims{Subject: "sub-123"})
	if id.Subject != "sub-123" {
		t.Fatalf("subject = %q, want fallback to Subject", id.Subject)
	}
}
