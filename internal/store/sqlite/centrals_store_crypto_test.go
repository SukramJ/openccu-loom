// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package sqlite_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/secret"
	"github.com/SukramJ/openccu-loom/internal/store/sqlite"
)

// TestCentralsStoreCryptoRoundTrip wires a CentralsStore with a Cipher,
// writes a row that has a non-empty PasswordPlain, and verifies that:
//   - the raw DB column contains the enc:v1: ciphertext (not the plaintext),
//   - Get and List return the decrypted password.
func TestCentralsStoreCryptoRoundTrip(t *testing.T) {
	db := openTestDBExternal(t, "centrals_crypto.db")
	c := loadCipher(t)

	store := sqlite.NewCentralsStore(db)
	store.SetCipher(c)

	ctx := context.Background()
	row := sqlite.CentralRow{
		Name:          "ccu-enc",
		Host:          "10.0.0.1",
		PasswordPlain: "ccu-secret",
		Enabled:       true,
	}

	if err := store.Put(ctx, row); err != nil {
		t.Fatalf("Put: %v", err)
	}

	// Read the raw DB column — the cipher must have sealed the password.
	var rawPass string
	err := db.QueryRowContext(ctx,
		`SELECT password_plain FROM centrals WHERE name = 'ccu-enc'`).
		Scan(&rawPass)
	if err != nil {
		t.Fatalf("raw query: %v", err)
	}
	if strings.Contains(rawPass, "ccu-secret") {
		t.Errorf("raw DB password_plain contains plaintext: %s", rawPass)
	}
	if !strings.Contains(rawPass, "enc:v1:") {
		t.Errorf("raw DB password_plain does not contain enc:v1: marker: %s", rawPass)
	}

	// Get must return the decrypted password.
	got, err := store.Get(ctx, "ccu-enc")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.PasswordPlain != "ccu-secret" {
		t.Errorf("Get PasswordPlain=%q want ccu-secret", got.PasswordPlain)
	}

	// List must also return the decrypted password.
	rows, err := store.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("List len=%d want 1", len(rows))
	}
	if rows[0].PasswordPlain != "ccu-secret" {
		t.Errorf("List[0].PasswordPlain=%q want ccu-secret", rows[0].PasswordPlain)
	}
}

// TestCentralsStorePlaintextPolicy pins the documented promise behind
// security.allow_plaintext_secrets: with no at-rest master key a central's
// password would land in the database in the clear, and the daemon refuses
// that write unless the operator opted in. The refusal is enforced in the
// store because every path that persists a central — the SPA's CRUD, the
// onboarding wizard, the live-adopt orchestrator and the YAML seed — goes
// through it.
func TestCentralsStorePlaintextPolicy(t *testing.T) {
	tests := []struct {
		name       string
		withCipher bool
		allow      bool
		password   string
		wantErr    bool
	}{
		{name: "no key and no opt-in refuses", password: "ccu-secret", wantErr: true},
		{name: "no key with opt-in stores cleartext", allow: true, password: "ccu-secret"},
		{name: "master key seals regardless of the flag", withCipher: true, password: "ccu-secret"},
		{name: "env-only central is unaffected", password: ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			db := openTestDBExternal(t, "centrals_policy.db")
			store := sqlite.NewCentralsStore(db)
			if tc.withCipher {
				store.SetCipher(loadCipher(t))
			} else {
				// The ADR 0027 degraded fallback: a Cipher that resolved no
				// master key passes values through unchanged.
				store.SetCipher(&secret.Cipher{})
			}
			store.SetPlaintextSecretPolicy(func(context.Context) bool { return tc.allow })

			ctx := context.Background()
			row := sqlite.CentralRow{
				Name:          "ccu-policy",
				Host:          "10.0.0.1",
				PasswordEnv:   "CCU_PASSWORD",
				PasswordPlain: tc.password,
				Enabled:       true,
			}
			err := store.Put(ctx, row)
			if tc.wantErr {
				if !errors.Is(err, sqlite.ErrPlaintextSecretNotAllowed) {
					t.Fatalf("Put err=%v want ErrPlaintextSecretNotAllowed", err)
				}
				if _, gerr := store.Get(ctx, "ccu-policy"); !errors.Is(gerr, sqlite.ErrCentralNotFound) {
					t.Errorf("Get err=%v want ErrCentralNotFound — the refused row must not be persisted", gerr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Put: %v", err)
			}
			got, gerr := store.Get(ctx, "ccu-policy")
			if gerr != nil {
				t.Fatalf("Get: %v", gerr)
			}
			if got.PasswordPlain != tc.password {
				t.Errorf("PasswordPlain=%q want %q", got.PasswordPlain, tc.password)
			}
		})
	}
}

// TestCentralsStoreWithoutPolicyKeepsPlaintextFallback pins the default for
// callers that install no policy at all (the admin CLI, tests): the ADR 0027
// resilient fallback still applies, so a store nobody told about the
// operator's choice must not start rejecting writes.
func TestCentralsStoreWithoutPolicyKeepsPlaintextFallback(t *testing.T) {
	db := openTestDBExternal(t, "centrals_nopolicy.db")
	store := sqlite.NewCentralsStore(db)
	ctx := context.Background()
	if err := store.Put(ctx, sqlite.CentralRow{
		Name:          "ccu-nopolicy",
		Host:          "10.0.0.1",
		PasswordPlain: "ccu-secret",
		Enabled:       true,
	}); err != nil {
		t.Fatalf("Put: %v", err)
	}
}
