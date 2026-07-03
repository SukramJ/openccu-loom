// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package main

import (
	"context"
	"log/slog"
	"path/filepath"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/auth"
	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/internal/store/sqlite"
)

// TestSeedLegacyAuthFromConfig verifies the one-shot backward-compat migration
// of config-file (YAML) basic-auth users and API tokens into the SQLite stores.
// Now that credentials no longer live in the north.rest config section, this
// migration is what keeps an operator's YAML-pinned logins working after an
// upgrade — so the exact token secret must keep authenticating and the user
// must authenticate with its configured password.
func TestSeedLegacyAuthFromConfig(t *testing.T) {
	ctx := context.Background()
	db, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "auth.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	users := sqlite.NewUserStore(db)
	tokens := sqlite.NewTokenStore(db)
	logger := slog.New(slog.DiscardHandler)

	cfg := config.Default()
	cfg.North.REST.Auth.Users = map[string]string{"admin": "s3cr3t-pass"}
	cfg.North.REST.Auth.Tokens = map[string]string{"legacy-bearer-token": "operator"}

	seedLegacyAuthFromConfig(ctx, users, tokens, cfg, logger)

	// The YAML user must authenticate with its configured password.
	if _, err := users.AuthenticateBasic(ctx, "admin", "s3cr3t-pass"); err != nil {
		t.Errorf("migrated user must authenticate: %v", err)
	}
	// The YAML token must authenticate with its exact configured secret.
	id, err := tokens.AuthenticateToken(ctx, "legacy-bearer-token")
	if err != nil {
		t.Fatalf("migrated token must authenticate with its exact secret: %v", err)
	}
	if id.Role != auth.RoleOperator {
		t.Errorf("migrated token role=%q want operator", id.Role)
	}

	// Re-running the migration is a no-op: the stores are non-empty so nothing
	// is duplicated, and (critically) an operator deletion is not resurrected.
	uBefore, _ := users.Count(ctx)
	tBefore, _ := tokens.Count(ctx)
	seedLegacyAuthFromConfig(ctx, users, tokens, cfg, logger)
	uAfter, _ := users.Count(ctx)
	tAfter, _ := tokens.Count(ctx)
	if uAfter != uBefore || tAfter != tBefore {
		t.Errorf("re-run must be idempotent: users %d→%d tokens %d→%d", uBefore, uAfter, tBefore, tAfter)
	}
}

// TestSeedLegacyAuthFromConfig_NoConfigUsers verifies the migration is a safe
// no-op when the YAML carries no users/tokens (the common wizard-only setup).
func TestSeedLegacyAuthFromConfig_NoConfigUsers(t *testing.T) {
	ctx := context.Background()
	db, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "auth.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	users := sqlite.NewUserStore(db)
	tokens := sqlite.NewTokenStore(db)
	logger := slog.New(slog.DiscardHandler)

	seedLegacyAuthFromConfig(ctx, users, tokens, config.Default(), logger)

	if n, _ := users.Count(ctx); n != 0 {
		t.Errorf("no YAML users → users table should stay empty, got %d", n)
	}
	if n, _ := tokens.Count(ctx); n != 0 {
		t.Errorf("no YAML tokens → tokens table should stay empty, got %d", n)
	}
}
