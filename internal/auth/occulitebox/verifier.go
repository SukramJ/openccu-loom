// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

// Package occulitebox adapts the occulited client to the auth package's
// box-credential port. It is its own package so that the composition root
// and the contract tests share the one adapter: a field the box answers
// and the adapter fails to pass on would otherwise be invisible to a test
// that carried its own copy.
package occulitebox

import (
	"context"

	"github.com/SukramJ/openccu-loom/internal/auth"
	"github.com/SukramJ/openccu-loom/internal/client/transport/occulited"
)

// Verifier implements [auth.OcculiteSessionVerifier] over a box client:
// one open state call carrying the credential under test (a session id or
// a box API token) as its bearer.
type Verifier struct {
	Client *occulited.Client
}

// VerifySession asks the box about credential and returns its answer.
func (v Verifier) VerifySession(ctx context.Context, credential string) (auth.OcculiteSession, error) {
	st, err := v.Client.AuthStateOf(ctx, credential)
	if err != nil {
		return auth.OcculiteSession{}, err
	}
	return auth.OcculiteSession{
		Authenticated: st.Authenticated,
		User:          st.User,
		Level:         st.Level,
		Role:          st.Role,
		Scopes:        st.Scopes,
		AuthOff:       st.AuthOff,
		Public:        st.Public,
	}, nil
}
