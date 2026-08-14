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

// SubjectTokenPurger is a token store that can drop every token bound to
// a subject. [ChainedTokenStore] dispatches on it so a member that cannot
// purge is skipped instead of blocking the members that can.
type SubjectTokenPurger interface {
	DeleteBySubject(ctx context.Context, subject string) (int, error)
}

// DeleteBySubject purges the subject's tokens from every member that
// supports it and returns the total removed. The chain — not either
// member alone — is the set of stores that can authenticate a bearer
// token, so purging only the primary leaves the account a live
// credential in the fallback store: authentication falls through to it
// the moment the durable store misses, which is exactly what a purge of
// the durable store produces.
//
// The first error is returned once every member has been tried, so one
// failing store cannot leave the others un-purged.
func (c ChainedTokenStore) DeleteBySubject(ctx context.Context, subject string) (int, error) {
	total := 0
	var firstErr error
	for _, member := range []TokenStore{c.Primary, c.Secondary} {
		purger, ok := member.(SubjectTokenPurger)
		if !ok || purger == nil {
			continue
		}
		n, err := purger.DeleteBySubject(ctx, subject)
		total += n
		if err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return total, firstErr
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
