// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package sqlite_test

import (
	"context"
	"strings"
	"testing"

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
