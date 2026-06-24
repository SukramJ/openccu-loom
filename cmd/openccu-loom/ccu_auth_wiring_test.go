// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package main

import (
	"context"
	"errors"
	"log/slog"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/auth"
	"github.com/SukramJ/openccu-loom/internal/build"
	"github.com/SukramJ/openccu-loom/internal/config"
)

// ── parseCCURoleMapping ───────────────────────────────────────────────────────

func TestParseCCURoleMapping_ValidEntries(t *testing.T) {
	t.Parallel()
	logger := slog.New(slog.DiscardHandler)
	m := parseCCURoleMapping(map[string]string{
		"8": "admin",
		"2": "operator",
	}, logger)
	if m[8] != auth.RoleAdmin {
		t.Errorf("level 8: role = %q, want %q", m[8], auth.RoleAdmin)
	}
	if m[2] != auth.RoleOperator {
		t.Errorf("level 2: role = %q, want %q", m[2], auth.RoleOperator)
	}
}

func TestParseCCURoleMapping_InvalidKeySkipped(t *testing.T) {
	t.Parallel()
	logger := slog.New(slog.DiscardHandler)
	m := parseCCURoleMapping(map[string]string{
		"x": "admin",
		"8": "admin",
	}, logger)
	if len(m) != 1 {
		t.Errorf("len = %d, want 1 (bad key skipped)", len(m))
	}
	if m[8] != auth.RoleAdmin {
		t.Errorf("level 8: role = %q, want %q", m[8], auth.RoleAdmin)
	}
}

func TestParseCCURoleMapping_InvalidRoleSkipped(t *testing.T) {
	t.Parallel()
	logger := slog.New(slog.DiscardHandler)
	m := parseCCURoleMapping(map[string]string{
		"8": "superuser",
		"2": "operator",
	}, logger)
	if _, ok := m[8]; ok {
		t.Error("level 8 should have been skipped (bad role)")
	}
	if m[2] != auth.RoleOperator {
		t.Errorf("level 2: role = %q, want %q", m[2], auth.RoleOperator)
	}
}

func TestParseCCURoleMapping_EmptyInputReturnsNil(t *testing.T) {
	t.Parallel()
	logger := slog.New(slog.DiscardHandler)
	m := parseCCURoleMapping(nil, logger)
	if m != nil {
		t.Errorf("expected nil for empty input, got %v", m)
	}
}

// ── loginChainWithCCU ─────────────────────────────────────────────────────────

// stubUserStore is a minimal [auth.UserStore] that returns a fixed identity
// for one username and ErrUnauthenticated for all others.
type stubUserStore struct {
	name     string
	username string
	identity auth.Identity
}

func (s *stubUserStore) AuthenticateBasic(_ context.Context, username, _ string) (auth.Identity, error) {
	if username == s.username {
		return s.identity, nil
	}
	return auth.Identity{}, auth.ErrUnauthenticated
}

func TestLoginChainWithCCU_NoCCU_LocalChainOnly(t *testing.T) {
	t.Parallel()
	sqUsers := &stubUserStore{
		name:     "sqlite",
		username: "alice",
		identity: auth.Identity{Subject: "alice", Scheme: auth.SchemeBasic, Role: auth.RoleAdmin},
	}
	memUsers := &stubUserStore{
		name:     "mem",
		username: "bob",
		identity: auth.Identity{Subject: "bob", Scheme: auth.SchemeBasic, Role: auth.RoleOperator},
	}
	chain := loginChainWithCCU(sqUsers, memUsers, nil, false)

	id, err := chain.AuthenticateBasic(context.Background(), "alice", "pass")
	if err != nil || id.Subject != "alice" {
		t.Errorf("sqlite user: got (%v, %v), want alice/nil", id, err)
	}
	id, err = chain.AuthenticateBasic(context.Background(), "bob", "pass")
	if err != nil || id.Subject != "bob" {
		t.Errorf("mem user: got (%v, %v), want bob/nil", id, err)
	}
	_, err = chain.AuthenticateBasic(context.Background(), "carol", "pass")
	if !errors.Is(err, auth.ErrUnauthenticated) {
		t.Errorf("unknown user: got %v, want ErrUnauthenticated", err)
	}
}

func TestLoginChainWithCCU_WithCCU_LocalTakesPrecedence(t *testing.T) {
	t.Parallel()
	sqUsers := &stubUserStore{
		name:     "sqlite",
		username: "alice",
		identity: auth.Identity{Subject: "alice", Scheme: auth.SchemeBasic, Role: auth.RoleAdmin},
	}
	memUsers := &stubUserStore{
		name:     "mem",
		username: "bob",
		identity: auth.Identity{Subject: "bob", Scheme: auth.SchemeBasic, Role: auth.RoleOperator},
	}
	// CCU recognises alice with a lower role; local should win.
	ccuStore := &stubUserStore{
		name:     "ccu",
		username: "alice",
		identity: auth.Identity{Subject: "alice", Scheme: auth.SchemeBasic, Role: auth.RoleViewer},
	}
	chain := loginChainWithCCU(sqUsers, memUsers, ccuStore, false)

	id, err := chain.AuthenticateBasic(context.Background(), "alice", "pass")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id.Role != auth.RoleAdmin {
		t.Errorf("local should win: role = %q, want %q", id.Role, auth.RoleAdmin)
	}
}

func TestLoginChainWithCCU_WithCCU_FallsThroughToCCU(t *testing.T) {
	t.Parallel()
	sqUsers := &stubUserStore{
		name:     "sqlite",
		username: "alice",
		identity: auth.Identity{Subject: "alice", Scheme: auth.SchemeBasic, Role: auth.RoleAdmin},
	}
	memUsers := &stubUserStore{
		name:     "mem",
		username: "bob",
		identity: auth.Identity{Subject: "bob", Scheme: auth.SchemeBasic, Role: auth.RoleOperator},
	}
	ccuStore := &stubUserStore{
		name:     "ccu",
		username: "carol",
		identity: auth.Identity{Subject: "carol", Scheme: auth.SchemeBasic, Role: auth.RoleViewer},
	}
	chain := loginChainWithCCU(sqUsers, memUsers, ccuStore, false)

	id, err := chain.AuthenticateBasic(context.Background(), "carol", "pass")
	if err != nil {
		t.Fatalf("unexpected error for CCU-only user: %v", err)
	}
	if id.Subject != "carol" {
		t.Errorf("subject = %q, want %q", id.Subject, "carol")
	}
	if id.Role != auth.RoleViewer {
		t.Errorf("role = %q, want %q", id.Role, auth.RoleViewer)
	}
}

func TestLoginChainWithCCU_WithCCU_UnknownUserDenied(t *testing.T) {
	t.Parallel()
	sqUsers := &stubUserStore{name: "sqlite", username: "alice"}
	memUsers := &stubUserStore{name: "mem", username: "bob"}
	ccuStore := &stubUserStore{name: "ccu", username: "carol"}
	chain := loginChainWithCCU(sqUsers, memUsers, ccuStore, false)

	_, err := chain.AuthenticateBasic(context.Background(), "dave", "pass")
	if !errors.Is(err, auth.ErrUnauthenticated) {
		t.Errorf("unknown user: got %v, want ErrUnauthenticated", err)
	}
}

// ── ccuAuthEnabled ────────────────────────────────────────────────────────────

func ptrBool(b bool) *bool { return &b }

func TestCCUAuthEnabled_ExplicitTrue(t *testing.T) {
	t.Parallel()
	cc := config.CCUAuthConfig{Enabled: ptrBool(true)}
	if !ccuAuthEnabled(cc) {
		t.Error("expected true when Enabled=&true")
	}
}

func TestCCUAuthEnabled_ExplicitFalse(t *testing.T) {
	t.Parallel()
	cc := config.CCUAuthConfig{Enabled: ptrBool(false)}
	if ccuAuthEnabled(cc) {
		t.Error("expected false when Enabled=&false")
	}
}

func TestCCUAuthEnabled_NilFallsBackToBuildStamp(t *testing.T) {
	t.Parallel()
	cc := config.CCUAuthConfig{Enabled: nil}
	got := ccuAuthEnabled(cc)
	if got != build.IsAddon() {
		t.Errorf("nil Enabled: got %v, want %v (build.IsAddon())", got, build.IsAddon())
	}
}

// ── ccuAuthPrimary ────────────────────────────────────────────────────────────

func TestCCUAuthPrimary_NilDefaultsToTrue(t *testing.T) {
	t.Parallel()
	cc := config.CCUAuthConfig{Primary: nil}
	if !ccuAuthPrimary(cc) {
		t.Error("expected true when Primary=nil")
	}
}

func TestCCUAuthPrimary_ExplicitFalse(t *testing.T) {
	t.Parallel()
	cc := config.CCUAuthConfig{Primary: ptrBool(false)}
	if ccuAuthPrimary(cc) {
		t.Error("expected false when Primary=&false")
	}
}

func TestCCUAuthPrimary_ExplicitTrue(t *testing.T) {
	t.Parallel()
	cc := config.CCUAuthConfig{Primary: ptrBool(true)}
	if !ccuAuthPrimary(cc) {
		t.Error("expected true when Primary=&true")
	}
}

// ── loginChainWithCCU primary=true ────────────────────────────────────────────

func TestLoginChainWithCCU_Primary_CCUWinsForSharedUser(t *testing.T) {
	t.Parallel()
	// Both stores know "alice" but return different roles.
	sqUsers := &stubUserStore{
		name:     "sqlite",
		username: "alice",
		identity: auth.Identity{Subject: "alice-local", Scheme: auth.SchemeBasic, Role: auth.RoleAdmin},
	}
	memUsers := &stubUserStore{name: "mem", username: "nobody"}
	ccuStore := &stubUserStore{
		name:     "ccu",
		username: "alice",
		identity: auth.Identity{Subject: "alice-ccu", Scheme: auth.SchemeBasic, Role: auth.RoleViewer},
	}
	chain := loginChainWithCCU(sqUsers, memUsers, ccuStore, true)

	id, err := chain.AuthenticateBasic(context.Background(), "alice", "pass")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id.Subject != "alice-ccu" {
		t.Errorf("primary=true: subject = %q, want %q (CCU wins)", id.Subject, "alice-ccu")
	}
}

func TestLoginChainWithCCU_Primary_LocalBreakGlassWhenCCURejectsUser(t *testing.T) {
	t.Parallel()
	// Only the local store knows "alice"; CCU returns ErrUnauthenticated.
	sqUsers := &stubUserStore{
		name:     "sqlite",
		username: "alice",
		identity: auth.Identity{Subject: "alice", Scheme: auth.SchemeBasic, Role: auth.RoleAdmin},
	}
	memUsers := &stubUserStore{name: "mem", username: "nobody"}
	ccuStore := &stubUserStore{name: "ccu", username: "nobody"}
	chain := loginChainWithCCU(sqUsers, memUsers, ccuStore, true)

	id, err := chain.AuthenticateBasic(context.Background(), "alice", "pass")
	if err != nil {
		t.Fatalf("break-glass fallback failed: %v", err)
	}
	if id.Subject != "alice" {
		t.Errorf("subject = %q, want %q", id.Subject, "alice")
	}
}

func TestLoginChainWithCCU_Primary_NilCCUPureLoca(t *testing.T) {
	t.Parallel()
	sqUsers := &stubUserStore{
		name:     "sqlite",
		username: "alice",
		identity: auth.Identity{Subject: "alice", Scheme: auth.SchemeBasic, Role: auth.RoleAdmin},
	}
	memUsers := &stubUserStore{name: "mem", username: "nobody"}
	chain := loginChainWithCCU(sqUsers, memUsers, nil, true)

	id, err := chain.AuthenticateBasic(context.Background(), "alice", "pass")
	if err != nil {
		t.Fatalf("unexpected error with nil CCU: %v", err)
	}
	if id.Subject != "alice" {
		t.Errorf("subject = %q, want %q", id.Subject, "alice")
	}
}
