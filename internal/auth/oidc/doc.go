// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// Package oidc implements the OpenID Connect login flow with PKCE
// (RFC 7636 + RFC 8252):
//
//   - `/login/oidc` starts the authorization-code flow by redirecting
//     the browser to the IdP.
//   - `/login/oidc/callback` receives the authorization code, swaps
//     it for tokens, validates the ID token, and issues a local
//     session.
//
// Discovery is zero-config: only `Issuer` + `ClientID` are required.
// Every other endpoint comes from the well-known document at
// `<issuer>/.well-known/openid-configuration`.
package oidc
