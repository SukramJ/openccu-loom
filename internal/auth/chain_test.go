// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package auth

import (
	"context"
	"errors"
	"testing"
)

type fakeUserStore struct {
	subject  string
	password string
	role     Role
	err      error // non-nil overrides match logic; ErrUnauthenticated triggers fallback
}

func (f fakeUserStore) AuthenticateBasic(_ context.Context, user, pass string) (Identity, error) {
	if f.err != nil {
		return Identity{}, f.err
	}
	if user == f.subject && pass == f.password {
		return Identity{Subject: user, Scheme: SchemeBasic, Role: f.role}, nil
	}
	return Identity{}, ErrUnauthenticated
}

func TestChainedUserStore_PrimaryWins(t *testing.T) {
	chain := ChainedUserStore{
		Primary:   fakeUserStore{subject: "alice", password: "p1", role: RoleAdmin},
		Secondary: fakeUserStore{subject: "alice", password: "p2", role: RoleViewer},
	}
	id, err := chain.AuthenticateBasic(context.Background(), "alice", "p1")
	if err != nil {
		t.Fatalf("expected primary match, got err=%v", err)
	}
	if id.Role != RoleAdmin {
		t.Fatalf("expected admin role from primary, got %s", id.Role)
	}
}

func TestChainedUserStore_FallbackOnUnauthenticated(t *testing.T) {
	chain := ChainedUserStore{
		Primary:   fakeUserStore{subject: "alice", password: "p1", role: RoleAdmin},
		Secondary: fakeUserStore{subject: "bob", password: "p2", role: RoleOperator},
	}
	id, err := chain.AuthenticateBasic(context.Background(), "bob", "p2")
	if err != nil {
		t.Fatalf("expected secondary match, got err=%v", err)
	}
	if id.Subject != "bob" || id.Role != RoleOperator {
		t.Fatalf("expected bob/operator, got %s/%s", id.Subject, id.Role)
	}
}

func TestChainedUserStore_BothMissReturnsUnauthenticated(t *testing.T) {
	chain := ChainedUserStore{
		Primary:   fakeUserStore{subject: "alice"},
		Secondary: fakeUserStore{subject: "bob"},
	}
	if _, err := chain.AuthenticateBasic(context.Background(), "charlie", "x"); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("expected ErrUnauthenticated, got %v", err)
	}
}

func TestChainedUserStore_PrimaryNonAuthErrorShortCircuits(t *testing.T) {
	boom := errors.New("primary db down")
	chain := ChainedUserStore{
		Primary:   fakeUserStore{err: boom},
		Secondary: fakeUserStore{subject: "alice", password: "p", role: RoleAdmin},
	}
	if _, err := chain.AuthenticateBasic(context.Background(), "alice", "p"); !errors.Is(err, boom) {
		t.Fatalf("expected primary error to short-circuit, got %v", err)
	}
}

type fakeTokenStore struct {
	token string
	id    Identity
	err   error
}

func (f fakeTokenStore) AuthenticateToken(_ context.Context, token string) (Identity, error) {
	if f.err != nil {
		return Identity{}, f.err
	}
	if token == f.token {
		return f.id, nil
	}
	return Identity{}, ErrUnauthenticated
}

func TestChainedTokenStore_FallbackOnUnauthenticated(t *testing.T) {
	chain := ChainedTokenStore{
		Primary:   fakeTokenStore{token: "sql-token", id: Identity{Subject: "alice", Role: RoleAdmin}},
		Secondary: fakeTokenStore{token: "memory-token", id: Identity{Subject: "bob", Role: RoleViewer}},
	}
	id, err := chain.AuthenticateToken(context.Background(), "memory-token")
	if err != nil {
		t.Fatalf("expected fallback to resolve, got %v", err)
	}
	if id.Subject != "bob" {
		t.Fatalf("expected bob, got %s", id.Subject)
	}
}
