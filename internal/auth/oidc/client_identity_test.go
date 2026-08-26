// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package oidc

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/auth"
)

// TestIdentityFromFoldsPreferredUsername pins that the provider's casing of
// `preferred_username` never reaches the session subject. The claim is
// neither stable nor unique (OpenID Connect Core §5.1), and directories emit
// whatever spelling the user typed, so the same person signing in twice can
// produce two subjects — splitting the audit trail and every subject-keyed
// lookup in two.
func TestIdentityFromFoldsPreferredUsername(t *testing.T) {
	t.Parallel()
	c := &Client{}
	id := c.IdentityFrom(&IDClaims{PreferredUser: " Markus@Example.COM ", Subject: "sub-123"})
	if id.Subject != "markus@example.com" {
		t.Fatalf("subject = %q, want the folded preferred_username", id.Subject)
	}
}

// TestIdentityFromKeepsSubjectClaimVerbatim pins the asymmetry: `sub` is an
// opaque, case-sensitive identifier (OpenID Connect Core §2), so two
// principals may differ in casing alone. Folding it would merge them.
func TestIdentityFromKeepsSubjectClaimVerbatim(t *testing.T) {
	t.Parallel()
	c := &Client{}
	id := c.IdentityFrom(&IDClaims{Subject: "AbC123"})
	if id.Subject != "AbC123" {
		t.Fatalf("subject = %q, want the sub claim byte-for-byte", id.Subject)
	}
}

// TestIdentityFromCarriesTheFederatedScheme pins that a principal vouched
// for by the provider is marked as such. A local account and an external
// principal whose name folds to the same string are different people, and
// only the scheme keeps them apart once both are sessions.
func TestIdentityFromCarriesTheFederatedScheme(t *testing.T) {
	t.Parallel()
	c := &Client{}
	id := c.IdentityFrom(&IDClaims{PreferredUser: "markus"})
	if id.Scheme != auth.SchemeOIDC {
		t.Fatalf("scheme = %q, want %q", id.Scheme, auth.SchemeOIDC)
	}
}
