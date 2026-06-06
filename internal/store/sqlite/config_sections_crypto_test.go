// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package sqlite_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/configstore"
	"github.com/SukramJ/openccu-loom/internal/secret"
	"github.com/SukramJ/openccu-loom/internal/store/sqlite"
)

// openTestDBExternal opens a migrated SQLite database in t's temp directory
// for use in external (_test) packages that cannot call the internal
// openTestDB helper.
func openTestDBExternal(t *testing.T, name string) *sql.DB {
	t.Helper()
	dsn := "file:" + filepath.Join(t.TempDir(), name) + "?_pragma=journal_mode(WAL)"
	db, err := sqlite.Open(context.Background(), dsn)
	if err != nil {
		t.Fatalf("sqlite.Open %s: %v", name, err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// loadCipher returns an available Cipher backed by an auto-generated
// key file in t's temp directory.
func loadCipher(t *testing.T) *secret.Cipher {
	t.Helper()
	c, err := secret.Load(t.TempDir(), func(string) string { return "" }, nil)
	if err != nil {
		t.Fatalf("secret.Load: %v", err)
	}
	if !c.Available() {
		t.Fatal("cipher not available")
	}
	return c
}

// TestConfigSectionStoreCryptoRoundTrip wires a ConfigSectionStore with the
// configstore.TransformSectionJSON hook, writes a north.mqtt payload that
// includes a password, and verifies that:
//   - the raw DB row contains the enc:v1: ciphertext (not the plaintext),
//   - Get returns the decrypted password.
func TestConfigSectionStoreCryptoRoundTrip(t *testing.T) {
	db := openTestDBExternal(t, "sections_crypto.db")
	c := loadCipher(t)

	store := sqlite.NewConfigSectionStore(db)
	store.SetSecretTransform(func(section string, value []byte, seal bool) ([]byte, error) {
		return configstore.TransformSectionJSON(c, configstore.Section(section), value, seal)
	})

	ctx := context.Background()
	raw := []byte(`{"broker_url":"tcp://x:1883","password":"hunter2","topic_base":"t"}`)

	if _, err := store.Put(ctx, "north.mqtt", raw, "test"); err != nil {
		t.Fatalf("Put: %v", err)
	}

	// Read the raw DB value directly — the cipher must have sealed the password.
	var rawDB string
	err := db.QueryRowContext(ctx,
		`SELECT value_json FROM config_sections WHERE section = 'north.mqtt'`).
		Scan(&rawDB)
	if err != nil {
		t.Fatalf("raw query: %v", err)
	}
	if strings.Contains(rawDB, "hunter2") {
		t.Errorf("raw DB value contains plaintext password: %s", rawDB)
	}
	if !strings.Contains(rawDB, "enc:v1:") {
		t.Errorf("raw DB value does not contain enc:v1: marker: %s", rawDB)
	}

	// Get must return the decrypted password.
	row, err := store.Get(ctx, "north.mqtt")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !strings.Contains(string(row.ValueJSON), "hunter2") {
		t.Errorf("Get ValueJSON does not contain decrypted password: %s", row.ValueJSON)
	}
	if strings.Contains(string(row.ValueJSON), "enc:v1:") {
		t.Errorf("Get ValueJSON still contains enc marker after open: %s", row.ValueJSON)
	}
}
