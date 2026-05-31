// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package auth

import (
	"context"
	"errors"
)

// ChainedUserStore tries primary first; on ErrUnauthenticated it
// falls back to secondary. Used by the daemon to layer the
// SQLite-backed user store (Wave B) on top of the legacy
// YAML-seeded MemoryUserStore so wizard-created admins and
// YAML-pinned users both authenticate.
type ChainedUserStore struct {
	Primary   UserStore
	Secondary UserStore
}

// AuthenticateBasic delegates to Primary; on ErrUnauthenticated it
// retries against Secondary. Any other error (transport / DB)
// short-circuits so a flaky DB doesn't silently fall through to a
// less-authoritative store.
func (c ChainedUserStore) AuthenticateBasic(ctx context.Context, username, password string) (Identity, error) {
	if c.Primary != nil {
		id, err := c.Primary.AuthenticateBasic(ctx, username, password)
		if err == nil {
			return id, nil
		}
		if !errors.Is(err, ErrUnauthenticated) {
			return Identity{}, err
		}
	}
	if c.Secondary != nil {
		return c.Secondary.AuthenticateBasic(ctx, username, password)
	}
	return Identity{}, ErrUnauthenticated
}

// ChainedTokenStore mirrors ChainedUserStore for bearer tokens.
type ChainedTokenStore struct {
	Primary   TokenStore
	Secondary TokenStore
}

// AuthenticateToken delegates to Primary; on ErrUnauthenticated it
// retries against Secondary.
func (c ChainedTokenStore) AuthenticateToken(ctx context.Context, token string) (Identity, error) {
	if c.Primary != nil {
		id, err := c.Primary.AuthenticateToken(ctx, token)
		if err == nil {
			return id, nil
		}
		if !errors.Is(err, ErrUnauthenticated) {
			return Identity{}, err
		}
	}
	if c.Secondary != nil {
		return c.Secondary.AuthenticateToken(ctx, token)
	}
	return Identity{}, ErrUnauthenticated
}
