// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package main

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/internal/configstore"
	"github.com/SukramJ/openccu-loom/internal/secret"
	sqlitestore "github.com/SukramJ/openccu-loom/internal/store/sqlite"
	"github.com/SukramJ/openccu-loom/internal/wiring"
)

// TestBootRefusesCleartextCentralPasswordWithoutMasterKey pins
// security.allow_plaintext_secrets through the composition root: the daemon's
// own wiring, not a hand-assembled store, must refuse to persist a CCU
// password it cannot encrypt. The observable effect is the write — the row is
// rejected while the flag is at its documented default and accepted once the
// operator opts in — never that a setter was called.
//
// The degraded state is produced the way it happens in the field: a data dir
// whose secret.key is unreadable (a restore that dropped it, a rotated key)
// leaves the daemon with an unavailable cipher and one warning line, after
// which every stored password is cleartext.
func TestBootRefusesCleartextCentralPasswordWithoutMasterKey(t *testing.T) {
	dataDir := t.TempDir()
	// A malformed key file makes secret.Load fall back to an unavailable
	// cipher — the ADR 0027 resilient fallback that stores values as-is.
	if err := os.WriteFile(filepath.Join(dataDir, secret.KeyFileName), []byte("not-a-key\n"), 0o600); err != nil {
		t.Fatalf("write key file: %v", err)
	}
	t.Setenv(secret.EnvKeyVar, "")

	cfg := config.Default()
	cfg.DataDir = dataDir
	ctx := context.Background()

	gooseMigrateMu.Lock()
	ov, teardown := wireAuditOverlay(ctx, wiring.NewManifest(), cfg, slog.New(slog.DiscardHandler))
	gooseMigrateMu.Unlock()
	t.Cleanup(teardown)

	if ov.sqCentrals == nil || ov.sqSections == nil {
		t.Fatal("audit overlay did not wire the SQLite stores")
	}
	if ov.secretsAvailable {
		t.Fatal("precondition: the cipher must be unavailable for this test")
	}

	row := sqlitestore.CentralRow{
		Name:          "ccu1",
		Host:          "10.0.0.1",
		PasswordPlain: "ccu-secret",
		Enabled:       true,
	}
	err := ov.sqCentrals.Put(ctx, row)
	if !errors.Is(err, sqlitestore.ErrPlaintextSecretNotAllowed) {
		t.Fatalf("Put err=%v want ErrPlaintextSecretNotAllowed at the documented default", err)
	}
	if _, gerr := ov.sqCentrals.Get(ctx, "ccu1"); !errors.Is(gerr, sqlitestore.ErrCentralNotFound) {
		t.Errorf("Get err=%v want ErrCentralNotFound — the refused password must not reach the database", gerr)
	}

	// The operator opts in through the security section the SPA writes.
	optIn, err := json.Marshal(configstore.SecurityConfig{AllowPlaintextSecrets: true})
	if err != nil {
		t.Fatalf("marshal security section: %v", err)
	}
	if _, err := ov.sqSections.Put(ctx, string(configstore.SectionSecurity), optIn, "test"); err != nil {
		t.Fatalf("persist security section: %v", err)
	}
	if err := ov.sqCentrals.Put(ctx, row); err != nil {
		t.Fatalf("Put after opt-in: %v", err)
	}
	got, err := ov.sqCentrals.Get(ctx, "ccu1")
	if err != nil {
		t.Fatalf("Get after opt-in: %v", err)
	}
	if got.PasswordPlain != "ccu-secret" {
		t.Errorf("PasswordPlain=%q want ccu-secret", got.PasswordPlain)
	}
}
