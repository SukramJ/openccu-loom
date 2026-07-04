// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/secret"
	"github.com/SukramJ/openccu-loom/internal/store/sqlite"
)

// backupTestKey is a fixed base64 32-byte master key. Setting it via
// OPENCCU_LOOM_SECRET_KEY makes create and restore (in different data dirs)
// share the same at-rest key, so an encrypted round-trip decrypts.
func backupTestKey() string {
	return base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x2a}, 32))
}

// useBackupTestKey pins a deterministic master key for the test process. It
// disables t.Parallel (t.Setenv does too), which is fine — these tests mutate
// process-global state (env, decompression-limit vars).
func useBackupTestKey(t *testing.T) {
	t.Helper()
	t.Setenv(secret.EnvKeyVar, backupTestKey())
}

// backupFile is one member of a hand-built legacy (plaintext) backup archive.
// A non-empty sha overrides the manifest checksum, which lets a test record a
// checksum over the original bytes while writing tampered bytes to the tar.
type backupFile struct {
	name string
	data []byte
	sha  string
}

// buildPlaintextBackup writes a bare gzip/tar (legacy, unencrypted) backup to
// out, in the given entry order, with a manifest computed from the entries.
// schema > 0 stamps manifest.SchemaVersions["openccu-loom.db"].
func buildPlaintextBackup(t *testing.T, out string, files []backupFile, schema int) {
	t.Helper()
	shaMap := make(map[string]string, len(files))
	for _, bf := range files {
		if bf.sha != "" {
			shaMap[bf.name] = bf.sha
			continue
		}
		sum := sha256.Sum256(bf.data)
		shaMap[bf.name] = hex.EncodeToString(sum[:])
	}
	manifest := backupManifest{
		DaemonVersion: "test",
		CreatedAt:     time.Now().UTC().Format(time.RFC3339),
		SHA256:        shaMap,
	}
	if schema > 0 {
		manifest.SchemaVersions = map[string]int{"openccu-loom.db": schema}
	}
	mjson, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}

	f, err := os.Create(out) //nolint:gosec // test-controlled path
	if err != nil {
		t.Fatalf("create %s: %v", out, err)
	}
	defer func() { _ = f.Close() }()
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	writeOne := func(name string, data []byte) {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o600, Size: int64(len(data))}); err != nil {
			t.Fatalf("tar header %s: %v", name, err)
		}
		if _, err := tw.Write(data); err != nil {
			t.Fatalf("tar body %s: %v", name, err)
		}
	}
	for _, bf := range files {
		writeOne(bf.name, bf.data)
	}
	writeOne("manifest.json", mjson)
	if err := tw.Close(); err != nil {
		t.Fatalf("tar close: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}
}

// cleanDBBytes returns a VACUUMed single-file snapshot of the DB at path, so
// the bytes are self-contained (no WAL sidecar) and openable after restore.
func cleanDBBytes(t *testing.T, dbPath string) []byte {
	t.Helper()
	clean := filepath.Join(t.TempDir(), "clean.db")
	if err := vacuumInto(dbPath, clean); err != nil {
		t.Fatalf("vacuum: %v", err)
	}
	b, err := os.ReadFile(clean) //nolint:gosec // test-controlled path
	if err != nil {
		t.Fatalf("read clean db: %v", err)
	}
	return b
}

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
	useBackupTestKey(t) // shared master key so the encrypted archive round-trips
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

	// The archive must be encrypted at rest: it starts with the container
	// magic, not the gzip magic.
	assertEncryptedArchive(t, archivePath)

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
	useBackupTestKey(t)
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

// TestBackupRestoreManifestMismatch verifies that a payload whose bytes do not
// match the manifest sha256 is rejected. Built as a legacy plaintext archive
// so the checksum layer is exercised directly (an encrypted archive would be
// caught even earlier by GCM authentication).
func TestBackupRestoreManifestMismatch(t *testing.T) {
	srcDir := t.TempDir()
	dbPath := filepath.Join(srcDir, "openccu-loom.db")
	seedDB(t, dbPath)
	original := cleanDBBytes(t, dbPath)

	// Record the sha over the ORIGINAL bytes but write garbage to the tar.
	origSHA := sha256.Sum256(original)
	archivePath := filepath.Join(t.TempDir(), "tampered.tar.gz")
	buildPlaintextBackup(t, archivePath, []backupFile{
		{name: "state/openccu-loom.db", data: []byte("not a valid SQLite database"), sha: hex.EncodeToString(origSHA[:])},
	}, 0)

	dstDir := t.TempDir()
	var stdout, stderr bytes.Buffer
	err := runBackup([]string{"restore", "--data-dir", dstDir, archivePath}, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected sha256 mismatch error, got nil")
	}
	if !strings.Contains(err.Error(), "sha256 mismatch") {
		t.Fatalf("expected sha256 mismatch error, got: %v", err)
	}
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
	useBackupTestKey(t) // encrypt via env key; the key file below must still be excluded
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
// contained in the archive at path, transparently decrypting an encrypted
// container (keyed by the current OPENCCU_LOOM_SECRET_KEY).
func archiveEntryNames(t *testing.T, path string) []string {
	t.Helper()
	entries := readArchiveEntries(t, path)
	names := make([]string, 0, len(entries))
	for n := range entries {
		names = append(names, n)
	}
	return names
}

// readArchiveEntries decodes every tar member of a backup archive at path,
// auto-detecting the encrypted container vs a legacy plaintext (gzip) one.
func readArchiveEntries(t *testing.T, path string) map[string][]byte {
	t.Helper()
	f, err := os.Open(path) //nolint:gosec // test-controlled path
	if err != nil {
		t.Fatalf("readArchiveEntries: open: %v", err)
	}
	defer func() { _ = f.Close() }()

	magic := make([]byte, len(backupMagicV1))
	n, _ := io.ReadFull(f, magic)
	var src io.Reader
	if n == len(backupMagicV1) && bytes.Equal(magic, backupMagicV1) {
		c, cerr := secret.Load("", nil, nil)
		if cerr != nil || !c.Available() {
			t.Fatalf("readArchiveEntries: need master key to read encrypted archive: %v", cerr)
		}
		src = c.NewDecryptReader(f)
	} else {
		src = io.MultiReader(bytes.NewReader(magic[:n]), f)
	}

	gr, err := gzip.NewReader(src)
	if err != nil {
		t.Fatalf("readArchiveEntries: gzip: %v", err)
	}
	defer func() { _ = gr.Close() }()
	tr := tar.NewReader(gr)

	out := map[string][]byte{}
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("readArchiveEntries: read tar: %v", err)
		}
		data, _ := io.ReadAll(tr)
		out[hdr.Name] = data
	}
	return out
}

// assertEncryptedArchive fails unless the file at path begins with the
// encrypted-container magic header.
func assertEncryptedArchive(t *testing.T, path string) {
	t.Helper()
	f, err := os.Open(path) //nolint:gosec // test-controlled path
	if err != nil {
		t.Fatalf("open archive: %v", err)
	}
	defer func() { _ = f.Close() }()
	head := make([]byte, len(backupMagicV1))
	if _, err := io.ReadFull(f, head); err != nil {
		t.Fatalf("read header: %v", err)
	}
	if !bytes.Equal(head, backupMagicV1) {
		t.Fatalf("archive is not encrypted: header %x != magic %x", head, backupMagicV1)
	}
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

// TestBackupCreateEncryptedFilePerms checks a created backup is 0600 (not the
// world-readable 0644 that leaked plaintext DB secrets) and encrypted.
func TestBackupCreateEncryptedFilePerms(t *testing.T) {
	useBackupTestKey(t)
	srcDir := t.TempDir()
	seedDB(t, filepath.Join(srcDir, "openccu-loom.db"))
	configFile := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(configFile, []byte("data_dir: "+srcDir+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	archivePath := filepath.Join(t.TempDir(), "backup.tar.gz")
	var stdout, stderr bytes.Buffer
	if err := runBackup([]string{"create", "--config", configFile, "--out", archivePath}, &stdout, &stderr); err != nil {
		t.Fatalf("create: %v\nstderr: %s", err, stderr.String())
	}
	fi, err := os.Stat(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	// Windows does not honour Unix file modes (os.Create yields 0666), so the
	// 0600 request the production code makes is only observable off Windows.
	if runtime.GOOS != "windows" {
		if perm := fi.Mode().Perm(); perm != 0o600 {
			t.Errorf("archive perms = %o, want 0600", perm)
		}
	}
	assertEncryptedArchive(t, archivePath)
}

// TestBackupRestoreRejectsPathTraversal is the Zip-Slip guard: a tar entry
// that resolves outside the data dir must be rejected and must not write
// anything to the escape target.
func TestBackupRestoreRejectsPathTraversal(t *testing.T) {
	base := t.TempDir()
	dstDir := filepath.Join(base, "data")

	archivePath := filepath.Join(t.TempDir(), "evil.tar.gz")
	buildPlaintextBackup(t, archivePath, []backupFile{
		{name: "state/../evil.txt", data: []byte("pwned")},
		{name: "state/openccu-loom.db", data: []byte("db")},
	}, 0)

	var stdout, stderr bytes.Buffer
	err := runBackup([]string{"restore", "--data-dir", dstDir, archivePath}, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected path-traversal rejection, got nil")
	}
	if !strings.Contains(err.Error(), "escapes data dir") {
		t.Fatalf("expected 'escapes data dir' error, got: %v", err)
	}
	if _, err := os.Stat(filepath.Join(base, "evil.txt")); !os.IsNotExist(err) {
		t.Fatalf("escape target was written outside the data dir: %v", err)
	}
}

// TestBackupRestoreRejectsAbsolutePath rejects an absolute path smuggled into
// a state/ entry.
func TestBackupRestoreRejectsAbsolutePath(t *testing.T) {
	dstDir := t.TempDir()
	archivePath := filepath.Join(t.TempDir(), "abs.tar.gz")
	// "state//etc/passwd" → TrimPrefix "state/" → "/etc/passwd" (absolute).
	buildPlaintextBackup(t, archivePath, []backupFile{
		{name: "state//etc/cron.d/evil", data: []byte("x")},
	}, 0)
	var stdout, stderr bytes.Buffer
	err := runBackup([]string{"restore", "--data-dir", dstDir, archivePath}, &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "absolute path") {
		t.Fatalf("expected absolute-path rejection, got: %v", err)
	}
}

// TestBackupRestoreRejectsDecompressionBomb caps decompression: an entry that
// inflates beyond the ceiling is rejected rather than buffered into OOM.
func TestBackupRestoreRejectsDecompressionBomb(t *testing.T) {
	// Shrink the ceiling for the test; restore on function exit.
	orig := maxBackupDecompressed
	maxBackupDecompressed = 8 << 10 // 8 KiB
	defer func() { maxBackupDecompressed = orig }()

	archivePath := filepath.Join(t.TempDir(), "bomb.tar.gz")
	buildPlaintextBackup(t, archivePath, []backupFile{
		// Highly compressible so the archive is tiny but inflates far past cap.
		{name: "state/openccu-loom.db", data: bytes.Repeat([]byte{'A'}, 1<<20)},
	}, 0)

	dstDir := t.TempDir()
	var stdout, stderr bytes.Buffer
	err := runBackup([]string{"restore", "--data-dir", dstDir, archivePath}, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected decompression-bomb rejection, got nil")
	}
	if !errors.Is(err, errBackupTooLarge) {
		t.Fatalf("expected errBackupTooLarge, got: %v", err)
	}
}

// TestCappedReaderErrorsPastLimit unit-tests the decompression guard directly.
func TestCappedReaderErrorsPastLimit(t *testing.T) {
	t.Parallel()
	// Within limit: full read succeeds.
	r := &cappedReader{r: bytes.NewReader(bytes.Repeat([]byte{1}, 40)), remaining: 100}
	if _, err := io.ReadAll(r); err != nil {
		t.Fatalf("within-limit read: %v", err)
	}
	// Past limit: surfaces the bomb error, not a silent EOF.
	r = &cappedReader{r: bytes.NewReader(bytes.Repeat([]byte{1}, 200)), remaining: 100}
	if _, err := io.ReadAll(r); !errors.Is(err, errBackupTooLarge) {
		t.Fatalf("past-limit read: want errBackupTooLarge, got %v", err)
	}
}

// TestBackupRestoreLegacyPlaintextArchive verifies a pre-encryption (bare
// gzip) archive is still auto-detected and restorable — the versioned
// container's backward-compatibility contract.
func TestBackupRestoreLegacyPlaintextArchive(t *testing.T) {
	srcDir := t.TempDir()
	dbPath := filepath.Join(srcDir, "openccu-loom.db")
	seedDB(t, dbPath)
	dbBytes := cleanDBBytes(t, dbPath)

	archivePath := filepath.Join(t.TempDir(), "legacy.tar.gz")
	buildPlaintextBackup(t, archivePath, []backupFile{
		{name: "state/openccu-loom.db", data: dbBytes},
	}, 0)

	dstDir := t.TempDir()
	var stdout, stderr bytes.Buffer
	if err := runBackup([]string{"restore", "--data-dir", dstDir, archivePath}, &stdout, &stderr); err != nil {
		t.Fatalf("legacy restore failed: %v\nstderr: %s", err, stderr.String())
	}

	// The restored DB opens and carries the seeded row.
	ctx := context.Background()
	db, err := sqlite.Open(ctx, filepath.Join(dstDir, "openccu-loom.db"))
	if err != nil {
		t.Fatalf("open restored db: %v", err)
	}
	defer func() { _ = db.Close() }()
	row, err := sqlite.NewConfigSectionStore(db).Get(ctx, "test.section")
	if err != nil {
		t.Fatalf("get section: %v", err)
	}
	if string(row.ValueJSON) != `{"hello":"world"}` {
		t.Errorf("unexpected section value: %s", row.ValueJSON)
	}
}

// TestBackupRestoreRejectsNewerSchema refuses a backup stamped with a schema
// newer than this binary supports, unless --force overrides it.
func TestBackupRestoreRejectsNewerSchema(t *testing.T) {
	srcDir := t.TempDir()
	dbPath := filepath.Join(srcDir, "openccu-loom.db")
	seedDB(t, dbPath)
	dbBytes := cleanDBBytes(t, dbPath)

	maxKnown, err := sqlite.MaxKnownMigration()
	if err != nil {
		t.Fatalf("MaxKnownMigration: %v", err)
	}

	archivePath := filepath.Join(t.TempDir(), "newer.tar.gz")
	buildPlaintextBackup(t, archivePath, []backupFile{
		{name: "state/openccu-loom.db", data: dbBytes},
	}, int(maxKnown)+5)

	// Without --force: rejected.
	dstDir := t.TempDir()
	var stdout, stderr bytes.Buffer
	err = runBackup([]string{"restore", "--data-dir", dstDir, archivePath}, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected newer-schema rejection, got nil")
	}
	if !strings.Contains(err.Error(), "newer than this binary supports") {
		t.Fatalf("expected schema-compat error, got: %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(dstDir, "openccu-loom.db")); !os.IsNotExist(statErr) {
		t.Fatal("DB was written despite schema rejection")
	}

	// With --force: allowed.
	dstDir2 := t.TempDir()
	stdout.Reset()
	stderr.Reset()
	if err := runBackup([]string{"restore", "--data-dir", dstDir2, "--force", archivePath}, &stdout, &stderr); err != nil {
		t.Fatalf("forced restore should succeed: %v\nstderr: %s", err, stderr.String())
	}
}

// TestBackupRestoreAtomicRollbackOnError verifies that a mid-commit failure
// rolls back so the pre-existing live DB is left intact — no half-applied
// restore. The failure is forced by a state entry whose live destination is
// occupied by a directory (an un-renameable target).
func TestBackupRestoreAtomicRollbackOnError(t *testing.T) {
	// The commit failure is induced by renaming a directory onto an existing
	// sidecar file, which fails on Unix (ENOTDIR/EEXIST) but not on Windows,
	// whose MoveFileEx tolerates it. The rollback logic itself is not
	// OS-specific and is exercised on the Unix runners; there is no portable
	// way to force a mid-commit rename failure on Windows here.
	if runtime.GOOS == "windows" {
		t.Skip("commit-failure induction relies on Unix rename semantics")
	}
	dstDir := t.TempDir()
	liveDB := filepath.Join(dstDir, "openccu-loom.db")
	const original = "ORIGINAL-DB-CONTENT"
	if err := os.WriteFile(liveDB, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	// Occupy the destination of the second entry with a NON-EMPTY directory so
	// its commit-time rename fails after the DB has already been placed. A
	// non-empty directory cannot be replaced by a file rename on either Unix or
	// Windows (an empty one can be, on Windows), so the failure is deterministic.
	blockedDir := filepath.Join(dstDir, "blocked")
	if err := os.Mkdir(blockedDir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(blockedDir, "keep"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	archivePath := filepath.Join(t.TempDir(), "atomic.tar.gz")
	buildPlaintextBackup(t, archivePath, []backupFile{
		{name: "state/openccu-loom.db", data: []byte("NEW-DB-CONTENT-should-not-survive")},
		{name: "state/blocked", data: []byte("payload")},
	}, 0)

	var stdout, stderr bytes.Buffer
	err := runBackup([]string{"restore", "--data-dir", dstDir, "--force", archivePath}, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected commit failure, got nil")
	}

	got, rerr := os.ReadFile(liveDB) //nolint:gosec // test-controlled path
	if rerr != nil {
		t.Fatalf("live DB missing after rollback: %v", rerr)
	}
	if string(got) != original {
		t.Fatalf("live DB was mutated by a failed restore: got %q want %q", got, original)
	}
}
