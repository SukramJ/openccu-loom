// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/auth"
	"github.com/SukramJ/openccu-loom/internal/store/sqlite"
)

// makeConfigDB creates a migrated SQLite DB at <dir>/openccu-loom.db
// and returns a minimal config.yaml pointing at that dir.
func makeConfigDB(t *testing.T, dir string) string {
	t.Helper()
	dbPath := filepath.Join(dir, "openccu-loom.db")
	ctx := context.Background()
	db, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("makeConfigDB open: %v", err)
	}
	_ = db.Close()

	cfgFile := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(cfgFile, []byte("data_dir: "+dir+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return cfgFile
}

// seedConfigDB populates a section and a central in the database at dir.
func seedConfigDB(t *testing.T, dir string) {
	t.Helper()
	ctx := context.Background()
	db, err := sqlite.Open(ctx, filepath.Join(dir, "openccu-loom.db"))
	if err != nil {
		t.Fatalf("seedConfigDB open: %v", err)
	}
	defer func() { _ = db.Close() }()

	ss := sqlite.NewConfigSectionStore(db)
	// The MQTT section carries a cfg:"secret" password alongside plain
	// settings, which is what makes it the right fixture for the redaction
	// tests: a section row is not secret or non-secret as a whole.
	if _, err := ss.Put(ctx, "north.mqtt",
		[]byte(`{"broker":"mqtt://localhost","password":"mqtt-s3cret"}`), "test"); err != nil {
		t.Fatalf("put section: %v", err)
	}

	cs := sqlite.NewCentralsStore(db)
	if err := cs.Put(ctx, sqlite.CentralRow{
		Name:          "ccu1",
		Host:          "192.168.0.1",
		Enabled:       true,
		PasswordPlain: "secret123",
		Interfaces:    nil,
	}); err != nil {
		t.Fatalf("put central: %v", err)
	}

	us := sqlite.NewUserStore(db)
	if err := us.Put(ctx, "admin", "password", auth.RoleAdmin); err != nil {
		t.Fatalf("put user: %v", err)
	}

	ts := sqlite.NewTokenStore(db)
	if _, err := ts.Create(ctx, sqlite.CreateInput{Subject: "admin", Role: auth.RoleAdmin}); err != nil {
		t.Fatalf("create token: %v", err)
	}
}

// TestConfigExportContainsSectionAndCentral exports a seeded DB and verifies
// that the section and central appear in the JSON output.
func TestConfigExportContainsSectionAndCentral(t *testing.T) {
	dir := t.TempDir()
	cfgFile := makeConfigDB(t, dir)
	seedConfigDB(t, dir)

	var stdout, stderr bytes.Buffer
	if err := runConfigCLI([]string{"export", "--config", cfgFile}, &stdout, &stderr); err != nil {
		t.Fatalf("config export: %v\nstderr: %s", err, stderr.String())
	}

	var doc configExportDoc
	if err := json.Unmarshal(stdout.Bytes(), &doc); err != nil {
		t.Fatalf("parse export: %v\nraw: %s", err, stdout.String())
	}

	if _, ok := doc.Sections["north.mqtt"]; !ok {
		t.Error("section north.mqtt missing from export")
	}

	found := false
	for _, c := range doc.Centrals {
		if c.Name == "ccu1" {
			found = true
		}
	}
	if !found {
		t.Error("central ccu1 missing from export")
	}
}

// TestConfigExportRedactsSecretsByDefault verifies that without
// --include-secrets, password_plain is empty in the export.
func TestConfigExportRedactsSecretsByDefault(t *testing.T) {
	dir := t.TempDir()
	cfgFile := makeConfigDB(t, dir)
	seedConfigDB(t, dir)

	var stdout, stderr bytes.Buffer
	if err := runConfigCLI([]string{"export", "--config", cfgFile}, &stdout, &stderr); err != nil {
		t.Fatalf("config export: %v", err)
	}

	var doc configExportDoc
	if err := json.Unmarshal(stdout.Bytes(), &doc); err != nil {
		t.Fatalf("parse: %v", err)
	}
	for _, c := range doc.Centrals {
		if c.PasswordPlain != "" {
			t.Errorf("central %s: expected redacted password_plain, got %q", c.Name, c.PasswordPlain)
		}
	}

	// Section-tier secrets must be redacted too. The section store decrypts
	// on read (wireConfigStoreCrypto), so exporting a row verbatim writes the
	// operator's MQTT / OIDC / Matter credentials into a file whose whole
	// purpose is to be copied elsewhere — while --include-secrets, the flag
	// that is supposed to govern exactly this, is off.
	raw := stdout.String()
	if strings.Contains(raw, "mqtt-s3cret") {
		t.Errorf("section secret leaked into the export without --include-secrets:\n%s", raw)
	}
	// The non-secret half of the same section must survive, or the redaction
	// has simply dropped the section.
	if !strings.Contains(raw, "mqtt://localhost") {
		t.Errorf("redaction removed non-secret section fields:\n%s", raw)
	}
}

// TestConfigExportIncludesSecretsWhenFlagged verifies that
// --include-secrets carries the plaintext password.
func TestConfigExportIncludesSecretsWhenFlagged(t *testing.T) {
	dir := t.TempDir()
	cfgFile := makeConfigDB(t, dir)
	seedConfigDB(t, dir)

	var stdout, stderr bytes.Buffer
	if err := runConfigCLI([]string{"export", "--config", cfgFile, "--include-secrets"}, &stdout, &stderr); err != nil {
		t.Fatalf("config export: %v", err)
	}

	var doc configExportDoc
	if err := json.Unmarshal(stdout.Bytes(), &doc); err != nil {
		t.Fatalf("parse: %v", err)
	}
	found := false
	for _, c := range doc.Centrals {
		if c.Name == "ccu1" && c.PasswordPlain == "secret123" {
			found = true
		}
	}
	if !found {
		t.Error("expected password_plain=secret123 with --include-secrets")
	}
}

// TestConfigExportUsersAndTokensMetadata verifies that user and token
// metadata is exported (subject/role/fingerprint) but no sensitive material.
func TestConfigExportUsersAndTokensMetadata(t *testing.T) {
	dir := t.TempDir()
	cfgFile := makeConfigDB(t, dir)
	seedConfigDB(t, dir)

	var stdout, stderr bytes.Buffer
	if err := runConfigCLI([]string{"export", "--config", cfgFile}, &stdout, &stderr); err != nil {
		t.Fatalf("config export: %v", err)
	}

	var doc configExportDoc
	if err := json.Unmarshal(stdout.Bytes(), &doc); err != nil {
		t.Fatalf("parse: %v", err)
	}

	if len(doc.Users) == 0 {
		t.Error("expected at least one user in export")
	}
	for _, u := range doc.Users {
		if u.Subject == "" {
			t.Error("user subject is empty")
		}
		if u.Role == "" {
			t.Error("user role is empty")
		}
	}

	if len(doc.Tokens) == 0 {
		t.Error("expected at least one token in export")
	}
	for _, tk := range doc.Tokens {
		if tk.Fingerprint == "" {
			t.Error("token fingerprint is empty")
		}
	}
}

// TestConfigImportDryRunDoesNotMutate verifies that --dry-run does not
// write anything to the database.
func TestConfigImportDryRunDoesNotMutate(t *testing.T) {
	dir := t.TempDir()
	cfgFile := makeConfigDB(t, dir)

	// Write a JSON import file with one section.
	importDoc := configExportDoc{
		Sections: map[string]json.RawMessage{
			"north.mqtt": json.RawMessage(`{"broker":"mqtt://dryrun"}`),
		},
	}
	importFile := filepath.Join(t.TempDir(), "import.json")
	data, _ := json.MarshalIndent(importDoc, "", "  ")
	if err := os.WriteFile(importFile, data, 0o600); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	if err := runConfigCLI([]string{"import", "--config", cfgFile, "--dry-run", importFile}, &stdout, &stderr); err != nil {
		t.Fatalf("config import --dry-run: %v", err)
	}
	if !strings.Contains(stdout.String(), "dry-run") {
		t.Errorf("expected dry-run indicator in output, got: %s", stdout.String())
	}

	// Verify nothing was written.
	ctx := context.Background()
	db, err := sqlite.Open(ctx, filepath.Join(dir, "openccu-loom.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	ss := sqlite.NewConfigSectionStore(db)
	if _, err := ss.Get(ctx, "north.mqtt"); err == nil {
		t.Error("expected section to be absent after dry-run, but it was found")
	}
}

// TestConfigImportMergeWritesSections verifies that --merge upserts sections.
func TestConfigImportMergeWritesSections(t *testing.T) {
	dir := t.TempDir()
	cfgFile := makeConfigDB(t, dir)

	importDoc := configExportDoc{
		Sections: map[string]json.RawMessage{
			"north.mqtt": json.RawMessage(`{"broker":"mqtt://imported"}`),
		},
	}
	importFile := filepath.Join(t.TempDir(), "import.json")
	data, _ := json.MarshalIndent(importDoc, "", "  ")
	if err := os.WriteFile(importFile, data, 0o600); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	if err := runConfigCLI([]string{"import", "--config", cfgFile, "--merge", importFile}, &stdout, &stderr); err != nil {
		t.Fatalf("config import --merge: %v\nstderr: %s", err, stderr.String())
	}

	ctx := context.Background()
	db, err := sqlite.Open(ctx, filepath.Join(dir, "openccu-loom.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	ss := sqlite.NewConfigSectionStore(db)
	row, err := ss.Get(ctx, "north.mqtt")
	if err != nil {
		t.Fatalf("get section after import: %v", err)
	}
	if !strings.Contains(string(row.ValueJSON), "mqtt://imported") {
		t.Errorf("unexpected section value: %s", row.ValueJSON)
	}
}

// TestConfigImportReplaceDeletesExistingRows verifies that --replace clears
// existing sections before upserting.
func TestConfigImportReplaceDeletesExistingRows(t *testing.T) {
	dir := t.TempDir()
	cfgFile := makeConfigDB(t, dir)
	seedConfigDB(t, dir) // seeds north.mqtt + ccu1

	// Import a doc that only has a different section; north.mqtt should disappear.
	importDoc := configExportDoc{
		Sections: map[string]json.RawMessage{
			"locale": json.RawMessage(`{"locale":"de"}`),
		},
	}
	importFile := filepath.Join(t.TempDir(), "import.json")
	data, _ := json.MarshalIndent(importDoc, "", "  ")
	if err := os.WriteFile(importFile, data, 0o600); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	if err := runConfigCLI([]string{"import", "--config", cfgFile, "--replace", importFile}, &stdout, &stderr); err != nil {
		t.Fatalf("config import --replace: %v\nstderr: %s", err, stderr.String())
	}

	ctx := context.Background()
	db, err := sqlite.Open(ctx, filepath.Join(dir, "openccu-loom.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	ss := sqlite.NewConfigSectionStore(db)
	// north.mqtt must be gone after --replace.
	if _, err := ss.Get(ctx, "north.mqtt"); err == nil {
		t.Error("north.mqtt should have been deleted by --replace but was found")
	}
	// locale should be present.
	if _, err := ss.Get(ctx, "locale"); err != nil {
		t.Errorf("locale section missing after --replace: %v", err)
	}

	// ccu1 central must be gone.
	cs := sqlite.NewCentralsStore(db)
	if _, err := cs.Get(ctx, "ccu1"); err == nil {
		t.Error("ccu1 central should have been deleted by --replace but was found")
	}
}

// TestConfigImportSkipsUsersAndTokensWithWarning verifies that user/token
// entries in the import file are skipped and a warning is emitted.
func TestConfigImportSkipsUsersAndTokensWithWarning(t *testing.T) {
	dir := t.TempDir()
	cfgFile := makeConfigDB(t, dir)

	importDoc := configExportDoc{
		Users:  []exportedUser{{Subject: "hacker", Role: "admin"}},
		Tokens: []exportedToken{{Fingerprint: "abc123", Subject: "hacker", Role: "admin"}},
	}
	importFile := filepath.Join(t.TempDir(), "import.json")
	data, _ := json.MarshalIndent(importDoc, "", "  ")
	if err := os.WriteFile(importFile, data, 0o600); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	if err := runConfigCLI([]string{"import", "--config", cfgFile, "--merge", importFile}, &stdout, &stderr); err != nil {
		t.Fatalf("config import: %v", err)
	}

	if !strings.Contains(stderr.String(), "skipped") {
		t.Errorf("expected skip warning on stderr, got: %s", stderr.String())
	}

	// Confirm no users were created.
	ctx := context.Background()
	db, err := sqlite.Open(ctx, filepath.Join(dir, "openccu-loom.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	us := sqlite.NewUserStore(db)
	count, err := us.Count(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Errorf("expected 0 users after import (users skipped), got %d", count)
	}
}
