// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/secret"
	"github.com/SukramJ/openccu-loom/internal/store/sqlite"
)

// seedDB opens (and auto-migrates) an SQLite database at path.
func seedDB(t *testing.T, path string) {
	t.Helper()
	ctx := context.Background()
	db, err := sqlite.Open(ctx, path)
	if err != nil {
		t.Fatalf("seedDB: open: %v", err)
	}
	_, _ = db.ExecContext(ctx, `INSERT INTO config_sections (section, value_json, version, updated_at, updated_by)
		VALUES ('test.section', '{"hello":"world"}', 1, CURRENT_TIMESTAMP, 'test')
		ON CONFLICT(section) DO NOTHING`)
	_ = db.Close()
}

// TestBackupCreateRestore exercises the full round-trip:
// create an archive from a DataDir, restore it into a fresh DataDir,
// verify the restored DB contains the same content as the original.
func TestBackupCreateRestore(t *testing.T) {
	srcDir := t.TempDir()
	dbPath := filepath.Join(srcDir, "openccu-loom.db")
	seedDB(t, dbPath)

	// Write a minimal config.yaml so the --config flag can be exercised.
	configFile := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(configFile, []byte("data_dir: "+srcDir+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	archiveDir := t.TempDir()
	archivePath := filepath.Join(archiveDir, "test.tar.gz")

	// --- create ---
	var stdout, stderr bytes.Buffer
	err := runBackup([]string{
		"create",
		"--config", configFile,
		"--out", archivePath,
		"--include-secrets",
	}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("backup create: %v\nstderr: %s", err, stderr.String())
	}
	if _, statErr := os.Stat(archivePath); statErr != nil {
		t.Fatalf("archive not created at %s: %v", archivePath, statErr)
	}
	t.Logf("create output: %s", stdout.String())

	// --- restore into a fresh DataDir ---
	dstDir := t.TempDir()
	dstConfig := filepath.Join(t.TempDir(), "config.yaml")

	stdout.Reset()
	stderr.Reset()
	err = runBackup([]string{
		"restore",
		"--data-dir", dstDir,
		"--config", dstConfig,
		archivePath,
	}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("backup restore: %v\nstderr: %s", err, stderr.String())
	}
	t.Logf("restore output: %s", stdout.String())

	// Verify the restored DB exists and is readable.
	restoredDB := filepath.Join(dstDir, "openccu-loom.db")
	if _, err := os.Stat(restoredDB); err != nil {
		t.Fatalf("restored DB missing: %v", err)
	}

	// Open the restored DB and verify the seeded row is present.
	ctx := context.Background()
	db, err := sqlite.Open(ctx, restoredDB)
	if err != nil {
		t.Fatalf("open restored db: %v", err)
	}
	defer func() { _ = db.Close() }()

	store := sqlite.NewConfigSectionStore(db)
	row, err := store.Get(ctx, "test.section")
	if err != nil {
		t.Fatalf("get section from restored db: %v", err)
	}
	if string(row.ValueJSON) != `{"hello":"world"}` {
		t.Errorf("unexpected section value: %s", row.ValueJSON)
	}
}

// TestBackupRestoreRefusesWithoutForce verifies that restore refuses to
// overwrite an existing DB unless --force is specified.
func TestBackupRestoreRefusesWithoutForce(t *testing.T) {
	srcDir := t.TempDir()
	dbPath := filepath.Join(srcDir, "openccu-loom.db")
	seedDB(t, dbPath)

	configFile := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(configFile, []byte("data_dir: "+srcDir+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	archivePath := filepath.Join(t.TempDir(), "backup.tar.gz")
	var stdout, stderr bytes.Buffer
	if err := runBackup([]string{"create", "--config", configFile, "--out", archivePath}, &stdout, &stderr); err != nil {
		t.Fatalf("create: %v", err)
	}

	// The target DataDir already has a DB — restore must fail without --force.
	dstDir := t.TempDir()
	existingDB := filepath.Join(dstDir, "openccu-loom.db")
	if err := os.WriteFile(existingDB, []byte("existing"), 0o600); err != nil {
		t.Fatal(err)
	}

	stdout.Reset()
	stderr.Reset()
	err := runBackup([]string{"restore", "--data-dir", dstDir, archivePath}, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected error when DB exists without --force, got nil")
	}

	// With --force it must succeed.
	stdout.Reset()
	stderr.Reset()
	if err := runBackup([]string{"restore", "--data-dir", dstDir, "--force", archivePath}, &stdout, &stderr); err != nil {
		t.Fatalf("restore with --force failed: %v\nstderr: %s", err, stderr.String())
	}
}

// TestBackupRestoreManifestMismatch verifies that a tampered archive is
// rejected with a sha256 mismatch error.
func TestBackupRestoreManifestMismatch(t *testing.T) {
	srcDir := t.TempDir()
	dbPath := filepath.Join(srcDir, "openccu-loom.db")
	seedDB(t, dbPath)

	configFile := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(configFile, []byte("data_dir: "+srcDir+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	archivePath := filepath.Join(t.TempDir(), "backup.tar.gz")
	var stdout, stderr bytes.Buffer
	if err := runBackup([]string{"create", "--config", configFile, "--out", archivePath}, &stdout, &stderr); err != nil {
		t.Fatalf("create: %v", err)
	}

	// Tamper: read all entries, corrupt the DB bytes, rebuild the archive.
	tamperedPath := filepath.Join(t.TempDir(), "tampered.tar.gz")
	if err := tamperArchiveDB(archivePath, tamperedPath); err != nil {
		t.Fatalf("tamper: %v", err)
	}

	dstDir := t.TempDir()
	stdout.Reset()
	stderr.Reset()
	err := runBackup([]string{"restore", "--data-dir", dstDir, tamperedPath}, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected sha256 mismatch error, got nil")
	}
	t.Logf("got expected error: %v", err)
}

// tamperArchiveDB rebuilds a backup archive with the DB content replaced by
// garbage while keeping the original sha256 in the manifest unchanged.
func tamperArchiveDB(src, dst string) error {
	// Read all entries.
	sf, err := os.Open(src) //nolint:gosec // src is a test-controlled path generated by the test harness
	if err != nil {
		return err
	}
	defer func() { _ = sf.Close() }()
	gr, err := gzip.NewReader(sf)
	if err != nil {
		return err
	}
	defer func() { _ = gr.Close() }()
	tr := tar.NewReader(gr)

	type entry struct {
		hdr  *tar.Header
		data []byte
	}
	var entries []entry
	for {
		hdr, err := tr.Next()
		if err != nil {
			break
		}
		data, _ := io.ReadAll(tr)
		entries = append(entries, entry{hdr: hdr, data: data})
	}

	// Write tampered archive: corrupt DB bytes but leave manifest sha256 unchanged.
	df, err := os.Create(dst) //nolint:gosec // dst is a test-controlled path generated by the test harness
	if err != nil {
		return err
	}
	defer func() { _ = df.Close() }()
	gz := gzip.NewWriter(df)
	tw := tar.NewWriter(gz)

	for _, e := range entries {
		data := e.data
		if e.hdr.Name == "state/openccu-loom.db" {
			// corrupt the content
			data = []byte("this is not a valid SQLite database")
		}
		hdr := e.hdr
		hdr.Size = int64(len(data))
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		if _, err := tw.Write(data); err != nil {
			return err
		}
	}
	if err := tw.Close(); err != nil {
		return err
	}
	return gz.Close()
}

// TestBackupCreateJSONOutput verifies the --json flag produces parseable output.
func TestBackupCreateJSONOutput(t *testing.T) {
	srcDir := t.TempDir()
	dbPath := filepath.Join(srcDir, "openccu-loom.db")
	seedDB(t, dbPath)

	configFile := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(configFile, []byte("data_dir: "+srcDir+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	archivePath := filepath.Join(t.TempDir(), "out.tar.gz")
	var stdout, stderr bytes.Buffer
	if err := runBackup([]string{
		"create", "--config", configFile, "--out", archivePath, "--json",
	}, &stdout, &stderr); err != nil {
		t.Fatalf("create --json: %v", err)
	}

	var res backupCreateResult
	if err := json.Unmarshal(bytes.TrimSpace(stdout.Bytes()), &res); err != nil {
		t.Fatalf("parse JSON output: %v\nraw: %s", err, stdout.String())
	}
	if res.Path == "" {
		t.Error("JSON result missing path")
	}
	if res.Bytes <= 0 {
		t.Error("JSON result bytes should be positive")
	}
	if len(res.SHA256) != 64 {
		t.Errorf("expected 64-char hex sha256, got %q", res.SHA256)
	}
}

// TestBackupCreateExcludesSecretKey verifies that the at-rest encryption
// key file living in DataDir is never bundled into a backup archive: a
// stolen archive must not carry both the encrypted DB and the key that
// unlocks it. An ordinary state file alongside it must still be archived.
func TestBackupCreateExcludesSecretKey(t *testing.T) {
	srcDir := t.TempDir()
	dbPath := filepath.Join(srcDir, "openccu-loom.db")
	seedDB(t, dbPath)

	// An ordinary state file that must survive the walk.
	stateFile := filepath.Join(srcDir, "some-state.json")
	if err := os.WriteFile(stateFile, []byte(`{"ok":true}`), 0o600); err != nil {
		t.Fatal(err)
	}

	// The at-rest key file that must NEVER be archived.
	keyFile := filepath.Join(srcDir, secret.KeyFileName)
	if err := os.WriteFile(keyFile, []byte("super-secret-key-material"), 0o600); err != nil {
		t.Fatal(err)
	}

	configFile := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(configFile, []byte("data_dir: "+srcDir+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	archivePath := filepath.Join(t.TempDir(), "backup.tar.gz")
	var stdout, stderr bytes.Buffer
	if err := runBackup([]string{
		"create", "--config", configFile, "--out", archivePath,
	}, &stdout, &stderr); err != nil {
		t.Fatalf("backup create: %v\nstderr: %s", err, stderr.String())
	}

	names := archiveEntryNames(t, archivePath)

	foundState := false
	for _, n := range names {
		if filepath.Base(n) == secret.KeyFileName {
			t.Fatalf("archive contains the at-rest key file (entry %q); entries: %v", n, names)
		}
		if filepath.Base(n) == filepath.Base(stateFile) {
			foundState = true
		}
	}
	if !foundState {
		t.Errorf("archive missing ordinary state file %q; entries: %v", filepath.Base(stateFile), names)
	}
}

// archiveEntryNames returns the tar entry names (archive-internal paths)
// contained in the gzip-compressed tar archive at path.
func archiveEntryNames(t *testing.T, path string) []string {
	t.Helper()
	f, err := os.Open(path) //nolint:gosec // test-controlled path generated by the test harness
	if err != nil {
		t.Fatalf("archiveEntryNames: open: %v", err)
	}
	defer func() { _ = f.Close() }()
	gr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatalf("archiveEntryNames: gzip: %v", err)
	}
	defer func() { _ = gr.Close() }()
	tr := tar.NewReader(gr)

	var names []string
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("archiveEntryNames: read tar: %v", err)
		}
		names = append(names, hdr.Name)
	}
	return names
}

// TestLoadBootstrapForCLIHonorsDataDirEnv guards that a CLI subcommand run
// without --config still resolves DataDir from OPENCCU_LOOM_DATA_DIR (the
// add-on /data), not the ephemeral "./var" bootstrap default.
func TestLoadBootstrapForCLIHonorsDataDirEnv(t *testing.T) {
	t.Setenv("OPENCCU_LOOM_DATA_DIR", "/data")
	bc, err := loadBootstrapForCLI("", io.Discard)
	if err != nil {
		t.Fatalf("loadBootstrapForCLI: %v", err)
	}
	if bc.DataDir != "/data" {
		t.Errorf("DataDir=%q, want /data (OPENCCU_LOOM_DATA_DIR must apply without --config)", bc.DataDir)
	}
}
