// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/SukramJ/openccu-loom/internal/build"
	"github.com/SukramJ/openccu-loom/internal/central/adapter"
	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/internal/secret"
	"github.com/SukramJ/openccu-loom/internal/store/sqlite"
)

// backupMagicV1 is the fixed 7-byte header of an encrypted backup container:
// "OCLBAK" + format version 0x01. Its first byte (0x4f, 'O') differs from the
// gzip magic (0x1f), so restore auto-detects a legacy plaintext (bare gzip)
// archive versus an encrypted one by peeking these bytes.
var backupMagicV1 = []byte("OCLBAK\x01")

// Decompression bounds for restore. The archive is an operator artefact, but
// a crafted (or corrupted) one must not be able to exhaust memory or disk via
// a gzip/tar bomb. The ceilings are generous — a real fleet DB is well under
// them — but finite.
const (
	// maxBackupEntryCount caps the number of tar entries.
	maxBackupEntryCount = 100_000
	// maxBackupManifestSize caps the in-memory manifest.json read.
	maxBackupManifestSize = 4 << 20 // 4 MiB
)

// maxBackupDecompressed and maxBackupEntrySize cap the total and per-entry
// decompressed byte counts. They are vars, not consts, only so tests can
// shrink them to exercise the bomb guards without materialising gigabytes;
// production never mutates them.
var (
	// maxBackupDecompressed caps the total decompressed byte count across all
	// tar entries.
	maxBackupDecompressed int64 = 8 << 30 // 8 GiB
	// maxBackupEntrySize caps a single decompressed entry.
	maxBackupEntrySize int64 = 8 << 30 // 8 GiB
)

// errBackupTooLarge is returned when a restore exceeds the decompressed-size
// ceiling — the signature of a gzip/tar bomb.
var errBackupTooLarge = errors.New("backup restore: decompressed size exceeds limit (possible archive bomb)")

// ccuBackupsDirName is the sub-directory of DataDir that holds the CCU
// archives the daemon downloads (see buildBackupAdapter). Both the storage
// wiring and the daemon's own archive walk read it from here so the exclusion
// below cannot drift away from where the archives actually land.
const ccuBackupsDirName = "backups"

// liveDatabases are the SQLite databases the daemon keeps open with a live
// WAL. A byte-for-byte copy of any of them (main file plus the -wal/-shm
// sidecars) reassembles a mismatched trio on restore — the sidecars were read
// at a different instant than the main file — so SQLite can report
// integrity_check ok while silently dropping every committed-but-uncheckpointed
// row. Each is instead snapshotted with `VACUUM INTO` (a consistent single-file
// copy) and its live sidecars are excluded from the archive walk. history.db
// is a second live database beside openccu-loom.db; both need the identical
// treatment.
var liveDatabases = [...]string{"openccu-loom.db", "history.db"}

// isLiveDBArtefact reports whether base is one of the live SQLite databases or
// one of its WAL/SHM sidecars, all of which the archive walk skips because they
// are added as a consistent VACUUM INTO snapshot instead.
func isLiveDBArtefact(base string) bool {
	for _, db := range liveDatabases {
		if base == db || base == db+"-wal" || base == db+"-shm" {
			return true
		}
	}
	return false
}

// runBackup is the entry point for the `backup` subcommand family.
func runBackup(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		printBackupUsage(stderr)
		return errors.New("backup: missing subcommand")
	}
	switch args[0] {
	case "create":
		return backupCreate(args[1:], stdout, stderr)
	case "restore":
		return backupRestore(args[1:], stdout, stderr)
	default:
		printBackupUsage(stderr)
		return fmt.Errorf("backup: unknown subcommand: %s", args[0])
	}
}

func printBackupUsage(w io.Writer) {
	_, _ = fmt.Fprintln(w, "usage: openccu-loom backup <subcommand> [flags]")
	_, _ = fmt.Fprintln(w, "")
	_, _ = fmt.Fprintln(w, "subcommands:")
	_, _ = fmt.Fprintln(w, "  create   create a backup archive")
	_, _ = fmt.Fprintln(w, "  restore  restore from a backup archive")
}

// backupManifest is the metadata embedded as manifest.json in every archive.
type backupManifest struct {
	DaemonVersion  string            `json:"daemon_version"`
	SchemaVersions map[string]int    `json:"schema_versions,omitempty"`
	CreatedAt      string            `json:"created_at"`
	SHA256         map[string]string `json:"sha256"`
}

// backupCreateResult is the --json output for scripting consumers.
type backupCreateResult struct {
	Path      string `json:"path"`
	Bytes     int64  `json:"bytes"`
	SHA256    string `json:"sha256"`
	Encrypted bool   `json:"encrypted"`
}

func backupCreate(args []string, stdout, stderr io.Writer) error { //nolint:gocognit,gocyclo,funlen // single-purpose CLI command handler with many flag/validate branches
	fs := flag.NewFlagSet("backup create", flag.ContinueOnError)
	fs.SetOutput(stderr)
	configPath := fs.String("config", "", "path to config.yaml")
	outPath := fs.String("out", "", "output path (default: backup-<hostname>-<utc>.tar.gz)")
	includeSecrets := fs.Bool("include-secrets", false, "include a secrets placeholder directory")
	asJSON := fs.Bool("json", false, "emit a single-line JSON result instead of a banner")
	if err := fs.Parse(args); err != nil {
		return err
	}

	bc, err := loadBootstrapForCLI(*configPath, stderr)
	if err != nil {
		return err
	}

	dataDir := bc.DataDir
	dbPath := filepath.Join(dataDir, "openccu-loom.db")

	// Determine output path.
	dest := *outPath
	if dest == "" {
		hostname, _ := os.Hostname()
		if hostname == "" {
			hostname = "host"
		}
		ts := time.Now().UTC().Format("20060102T150405Z")
		dest = fmt.Sprintf("backup-%s-%s.tar.gz", hostname, ts)
	}

	// Snapshot the SQLite DB via VACUUM INTO to capture a clean,
	// WAL-checkpointed copy without touching the live WAL/SHM files.
	tmpDB, err := os.CreateTemp("", "openccu-loom-backup-*.db")
	if err != nil {
		return fmt.Errorf("backup create: temp file: %w", err)
	}
	tmpDBPath := tmpDB.Name()
	_ = tmpDB.Close()
	defer func() { _ = os.Remove(tmpDBPath) }()

	if err := vacuumInto(dbPath, tmpDBPath); err != nil {
		return fmt.Errorf("backup create: vacuum: %w", err)
	}

	// Record the schema generation of the snapshot so restore can refuse a
	// backup produced by a newer daemon than the restoring binary.
	schemaVer, err := sqlite.SchemaVersionOfFile(context.Background(), tmpDBPath)
	if err != nil {
		return fmt.Errorf("backup create: schema version: %w", err)
	}

	// Collect files to archive: state/ prefix for the DB and anything
	// else under DataDir (skip WAL/SHM which are live-connection artefacts).
	type entry struct {
		archivePath string // path inside the tar
		diskPath    string // absolute path on disk
		isDir       bool
	}
	var entries []entry

	// Walk DataDir for supplementary state files; skip WAL/SHM.
	if fi, err := os.Stat(dataDir); err == nil && fi.IsDir() {
		err = filepath.Walk(dataDir, func(p string, fi os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			rel, _ := filepath.Rel(dataDir, p)
			if rel == "." {
				return nil
			}
			// Never pull the CCU archive store into the daemon's own archive.
			// Those files are finished backups of a different system —
			// keep_last × centrals × tens of megabytes — so archiving them
			// re-reads, re-hashes and re-compresses the whole rotation on
			// every run while restoring nothing the daemon needs to operate.
			if fi.IsDir() && rel == ccuBackupsDirName {
				return filepath.SkipDir
			}
			// Honour the same exclusion tag the CCU's own tar respects, so a
			// relocated archive store that still sits under DataDir is skipped
			// too. The name-based skip above only knows the default layout.
			if fi.IsDir() {
				if _, err := os.Stat(filepath.Join(p, adapter.NoBackupTagName)); err == nil {
					return filepath.SkipDir
				}
			}
			// Skip every live DB, WAL, and SHM file — a consistent VACUUMed
			// copy of each is added separately below.
			base := filepath.Base(p)
			if isLiveDBArtefact(base) {
				return nil
			}
			// Never archive the at-rest encryption key alongside the ciphertext
			// it protects. The whole archive is sealed with the master key
			// derived from this file; bundling the key would let anyone who
			// steals the backup decrypt it, defeating the at-rest encryption
			// for exactly the stolen-copy threat it exists to counter.
			if base == secret.KeyFileName {
				return nil
			}
			entries = append(entries, entry{
				archivePath: filepath.Join("state", rel),
				diskPath:    p,
				isDir:       fi.IsDir(),
			})
			return nil
		})
		if err != nil {
			return fmt.Errorf("backup create: walk data dir: %w", err)
		}
	}

	// Always include the VACUUMed DB snapshot.
	entries = append(entries, entry{
		archivePath: "state/openccu-loom.db",
		diskPath:    tmpDBPath,
	})

	// The measurement history lives in a second open database, history.db.
	// Snapshot it the same way: a raw copy of the live file plus its WAL/SHM
	// sidecars restores to a mismatched trio that loses every uncheckpointed
	// row. VACUUM INTO produces a self-contained, WAL-checkpointed copy; the
	// live sidecars are already excluded by the walk above, and commitRestore
	// clears any stale sidecars beside the restored history.db (it moves the
	// WAL/SHM of every restored `.db` aside), so the restored database is
	// internally consistent. Absent when the recording feature never ran.
	historyPath := filepath.Join(dataDir, "history.db")
	if _, err := os.Stat(historyPath); err == nil {
		tmpHist, err := os.CreateTemp("", "openccu-loom-history-backup-*.db")
		if err != nil {
			return fmt.Errorf("backup create: history temp file: %w", err)
		}
		tmpHistPath := tmpHist.Name()
		_ = tmpHist.Close()
		defer func() { _ = os.Remove(tmpHistPath) }()
		if err := vacuumInto(historyPath, tmpHistPath); err != nil {
			return fmt.Errorf("backup create: vacuum history: %w", err)
		}
		entries = append(entries, entry{
			archivePath: "state/history.db",
			diskPath:    tmpHistPath,
		})
	}

	// Optionally include config.yaml.
	if *configPath != "" {
		if _, err := os.Stat(*configPath); err == nil {
			entries = append(entries, entry{
				archivePath: "config.yaml",
				diskPath:    *configPath,
			})
		}
	}

	// Resolve the at-rest cipher. When a master key is available the whole
	// archive is sealed with AES-256-GCM; the DB carries live session tokens,
	// Matter PSKs, and CCU passwords, so a plaintext archive would leak them.
	cipher, err := secret.Load(dataDir, nil, nil)
	if err != nil {
		return fmt.Errorf("backup create: load master key: %w", err)
	}
	encrypted := cipher.Available()

	// Create the output 0600 — the archive holds secret material even when
	// encrypted (and especially when the degraded plaintext path is taken).
	outF, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600) //nolint:gosec // operator-controlled destination path; see #20
	if err != nil {
		return fmt.Errorf("backup create: create output: %w", err)
	}
	archiveHasher := sha256.New()
	fileW := io.MultiWriter(outF, archiveHasher)

	var (
		archiveSink io.Writer      // gzip writes here
		encWriter   io.WriteCloser // non-nil when encrypting
	)
	if encrypted {
		if _, err := fileW.Write(backupMagicV1); err != nil {
			_ = outF.Close()
			return fmt.Errorf("backup create: write header: %w", err)
		}
		ew, err := cipher.NewEncryptWriter(fileW)
		if err != nil {
			_ = outF.Close()
			return fmt.Errorf("backup create: encrypt writer: %w", err)
		}
		encWriter = ew
		archiveSink = ew
	} else {
		// The key could not be resolved or persisted. Do not silently write
		// plaintext: warn loudly and continue in a degraded mode so backups
		// keep working, but the operator must protect the file themselves.
		_, _ = fmt.Fprintln(stderr, "WARNING: no at-rest master key available — writing an UNENCRYPTED backup.")
		_, _ = fmt.Fprintln(stderr, "         The archive contains the plaintext SQLite database (session")
		_, _ = fmt.Fprintln(stderr, "         tokens, Matter PSKs, CCU passwords). Protect the file, and set")
		_, _ = fmt.Fprintln(stderr, "         OPENCCU_LOOM_SECRET_KEY (or use a writable data dir) to encrypt.")
		archiveSink = fileW
	}

	gz := gzip.NewWriter(archiveSink)
	tw := tar.NewWriter(gz)

	// Build sha256 map and write archive.
	sha256Map := make(map[string]string)

	fail := func(err error) error {
		_ = outF.Close()
		return err
	}

	for _, e := range entries {
		if e.isDir {
			hdr := &tar.Header{
				Typeflag: tar.TypeDir,
				Name:     e.archivePath + "/",
				Mode:     0o755,
			}
			if err := tw.WriteHeader(hdr); err != nil {
				return fail(fmt.Errorf("backup create: tar dir header: %w", err))
			}
			continue
		}
		sum, err := addFileToTar(tw, e.archivePath, e.diskPath)
		if err != nil {
			return fail(fmt.Errorf("backup create: add %s: %w", e.archivePath, err))
		}
		sha256Map[e.archivePath] = sum
	}

	if *includeSecrets {
		// Write an empty placeholder directory plus a README note.
		if err := tw.WriteHeader(&tar.Header{
			Typeflag: tar.TypeDir,
			Name:     "secrets/",
			Mode:     0o700,
		}); err != nil {
			return fail(fmt.Errorf("backup create: secrets dir: %w", err))
		}
		readmeContent := "Secrets resolved from environment variables are not stored in the archive.\n" +
			"Set the relevant environment variables (e.g. OPENCCU_LOOM_MQTT_PASSWORD) before restore.\n"
		sum, err := addBytesToTar(tw, "secrets/README.txt", []byte(readmeContent))
		if err != nil {
			return fail(fmt.Errorf("backup create: secrets readme: %w", err))
		}
		sha256Map["secrets/README.txt"] = sum
	}

	// Write manifest last so it can include all sha256 sums.
	manifest := backupManifest{
		DaemonVersion:  build.Version,
		SchemaVersions: map[string]int{"openccu-loom.db": int(schemaVer)},
		CreatedAt:      time.Now().UTC().Format(time.RFC3339),
		SHA256:         sha256Map,
	}
	manifestJSON, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fail(fmt.Errorf("backup create: manifest: %w", err))
	}
	if _, err := addBytesToTar(tw, "manifest.json", manifestJSON); err != nil {
		return fail(fmt.Errorf("backup create: manifest tar: %w", err))
	}

	if err := tw.Close(); err != nil {
		return fail(fmt.Errorf("backup create: tar close: %w", err))
	}
	if err := gz.Close(); err != nil {
		return fail(fmt.Errorf("backup create: gzip close: %w", err))
	}
	if encWriter != nil {
		if err := encWriter.Close(); err != nil {
			return fail(fmt.Errorf("backup create: encrypt close: %w", err))
		}
	}
	if err := outF.Close(); err != nil {
		return fmt.Errorf("backup create: close output: %w", err)
	}

	fi, err := os.Stat(dest)
	if err != nil {
		return fmt.Errorf("backup create: stat output: %w", err)
	}
	archiveSHA := hex.EncodeToString(archiveHasher.Sum(nil))

	if *asJSON {
		res := backupCreateResult{Path: dest, Bytes: fi.Size(), SHA256: archiveSHA, Encrypted: encrypted}
		b, _ := json.Marshal(res)
		_, _ = fmt.Fprintf(stdout, "%s\n", b)
	} else {
		enc := "encrypted"
		if !encrypted {
			enc = "UNENCRYPTED"
		}
		_, _ = fmt.Fprintf(stdout, "backup created: %s  (%d bytes, %s, sha256=%s)\n", dest, fi.Size(), enc, archiveSHA)
	}

	// The archive never contains secret.key (see the walk skip above) — remind
	// the operator on every run so the out-of-band copy is not forgotten until
	// a restore fails to decrypt.
	_, _ = fmt.Fprintf(stderr, "reminder: this archive does NOT contain %s; preserve it "+
		"(or the OPENCCU_LOOM_SECRET_KEY value) out-of-band or the archive cannot be decrypted on restore\n",
		secret.KeyFileName)

	return nil
}

// vacuumInto snapshots the database at src into dest with `VACUUM INTO`. The
// dest file must not exist; a missing src is an error rather than an empty
// snapshot.
//
// The source is opened with a bare driver connection, not [sqlite.Open]: that
// helper runs the goose migrations and the stale-paramset wipe, which would let
// a backup taken with a binary newer than the running daemon migrate the live
// database underneath it. A backup must never write to what it backs up.
//
// The DSN must be a `file:` URI: the driver strips the query string from a DSN
// that is not one, which silently turns `mode=ro` into a read-write open.
func vacuumInto(src, dest string) error {
	if _, err := os.Stat(src); err != nil {
		return fmt.Errorf("vacuum: source database %s: %w", src, err)
	}
	ctx := context.Background()
	db, err := openForVacuum(ctx, src)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	if _, err := db.ExecContext(ctx, "VACUUM INTO ?", dest); err != nil {
		return fmt.Errorf("vacuum: exec: %w", err)
	}
	return nil
}

// openForVacuum opens the database at path for the snapshot read, preferring a
// read-only connection.
//
// The read-only attempt can legitimately fail: a database left in WAL mode by
// an unclean daemon exit has a populated `-wal` but no `-shm`, and SQLite
// cannot build the missing wal-index over a read-only handle. Falling back to a
// writable handle lets that database be backed up with its committed WAL frames
// instead of failing, and still never migrates anything.
func openForVacuum(ctx context.Context, path string) (*sql.DB, error) {
	const busyTimeout = "_pragma=busy_timeout(5000)"
	var firstErr error
	for _, dsn := range []string{
		"file:" + path + "?mode=ro&" + busyTimeout,
		"file:" + path + "?mode=rw&" + busyTimeout,
	} {
		db, err := sql.Open(sqlite.DriverName, dsn)
		if err == nil {
			// Reading the schema forces the open of the database file and its
			// wal-index, which is where a read-only handle over a hot WAL
			// fails — sql.Open alone is lazy and would defer that to VACUUM.
			var probe sql.NullString
			err = db.QueryRowContext(ctx, "SELECT name FROM sqlite_schema LIMIT 1").Scan(&probe)
			if err == nil || errors.Is(err, sql.ErrNoRows) {
				return db, nil
			}
			_ = db.Close()
		}
		if firstErr == nil {
			firstErr = err
		}
	}
	return nil, fmt.Errorf("vacuum: open src: %w", firstErr)
}

// addFileToTar adds the file at diskPath to tw with the given archivePath and
// returns the hex-encoded sha256 of the file content.
func addFileToTar(tw *tar.Writer, archivePath, diskPath string) (string, error) {
	f, err := os.Open(diskPath) //nolint:gosec // caller-controlled paths; see #20
	if err != nil {
		return "", fmt.Errorf("open %s: %w", diskPath, err)
	}
	defer func() { _ = f.Close() }()

	fi, err := f.Stat()
	if err != nil {
		return "", fmt.Errorf("stat %s: %w", diskPath, err)
	}

	hdr := &tar.Header{
		Typeflag: tar.TypeReg,
		Name:     archivePath,
		Mode:     0o600,
		Size:     fi.Size(),
		ModTime:  fi.ModTime(),
	}
	if err := tw.WriteHeader(hdr); err != nil {
		return "", fmt.Errorf("write header: %w", err)
	}

	hasher := sha256.New()
	if _, err := io.Copy(tw, io.TeeReader(f, hasher)); err != nil {
		return "", fmt.Errorf("copy: %w", err)
	}
	return hex.EncodeToString(hasher.Sum(nil)), nil
}

// addBytesToTar writes buf as archivePath and returns its sha256.
func addBytesToTar(tw *tar.Writer, archivePath string, buf []byte) (string, error) {
	hdr := &tar.Header{
		Typeflag: tar.TypeReg,
		Name:     archivePath,
		Mode:     0o600,
		Size:     int64(len(buf)),
		ModTime:  time.Now().UTC(),
	}
	if err := tw.WriteHeader(hdr); err != nil {
		return "", fmt.Errorf("write header %s: %w", archivePath, err)
	}
	if _, err := tw.Write(buf); err != nil {
		return "", fmt.Errorf("write body %s: %w", archivePath, err)
	}
	sum := sha256.Sum256(buf)
	return hex.EncodeToString(sum[:]), nil
}

// cappedReader wraps a reader and returns errBackupTooLarge once more than
// `remaining` bytes have been read. Unlike io.LimitReader it surfaces an
// error rather than a silent early EOF, so a bomb is reported, not truncated.
type cappedReader struct {
	r         io.Reader
	remaining int64
}

func (c *cappedReader) Read(p []byte) (int, error) {
	if c.remaining < 0 {
		return 0, errBackupTooLarge
	}
	n, err := c.r.Read(p)
	c.remaining -= int64(n)
	if c.remaining < 0 {
		return n, errBackupTooLarge
	}
	return n, err
}

// stagedRestoreFile is one archive member written to a temp file next to its
// final destination, ready for the all-or-nothing commit.
type stagedRestoreFile struct {
	archivePath string
	live        string
	tempPath    string
	sha256      string
}

func backupRestore(args []string, stdout, stderr io.Writer) error { //nolint:gocognit,gocyclo,funlen // single-purpose CLI command handler with many flag/validate branches
	fs := flag.NewFlagSet("backup restore", flag.ContinueOnError)
	fs.SetOutput(stderr)
	configPath := fs.String("config", "", "path to config.yaml (receives extracted config.yaml when present)")
	dataDirFlag := fs.String("data-dir", "", "override DataDir (default: from --config or ./var)")
	force := fs.Bool("force", false, "overwrite an existing DB and accept a newer-schema backup")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() < 1 {
		return errors.New("backup restore: missing <file> argument")
	}
	archivePath := fs.Arg(0)

	// Determine DataDir.
	var dataDir string
	if *dataDirFlag != "" {
		dataDir = *dataDirFlag
	} else {
		bc, err := loadBootstrapForCLI(*configPath, stderr)
		if err != nil {
			return err
		}
		dataDir = bc.DataDir
	}

	targetDB := filepath.Join(dataDir, "openccu-loom.db")
	if !*force {
		if _, err := os.Stat(targetDB); err == nil {
			return fmt.Errorf("backup restore: %s exists; use --force to overwrite", targetDB)
		}
	}
	if err := os.MkdirAll(dataDir, 0o750); err != nil {
		return fmt.Errorf("backup restore: mkdir data dir: %w", err)
	}

	f, err := os.Open(archivePath) //nolint:gosec // operator-supplied path; see #20
	if err != nil {
		return fmt.Errorf("backup restore: open archive: %w", err)
	}
	defer func() { _ = f.Close() }()

	// Auto-detect the container: an encrypted archive starts with the magic
	// header; anything else is treated as a legacy plaintext (bare gzip) one.
	src, encryptedArchive, err := openBackupBody(f, dataDir, stderr)
	if err != nil {
		return err
	}

	gr, err := gzip.NewReader(src)
	if err != nil {
		if encryptedArchive {
			return fmt.Errorf("backup restore: decrypt/gzip (wrong or missing master key?): %w", err)
		}
		return fmt.Errorf("backup restore: gzip: %w", err)
	}
	defer func() { _ = gr.Close() }()

	// Bound total decompression so a gzip/tar bomb cannot exhaust resources.
	tr := tar.NewReader(&cappedReader{r: gr, remaining: maxBackupDecompressed})

	var (
		staged    []stagedRestoreFile
		manifest  backupManifest
		count     int
		committed bool
	)
	// Until the commit succeeds, no live file is touched. Clean up any staged
	// temp files on every early return.
	defer func() {
		if !committed {
			for _, s := range staged {
				_ = os.Remove(s.tempPath)
			}
		}
	}()

	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("backup restore: read tar: %w", err)
		}
		if hdr.Typeflag == tar.TypeDir {
			continue
		}
		// Skip anything that is not a plain regular file (symlinks, devices,
		// hardlinks) — they have no legitimate place in a state backup and are
		// classic archive-escape vectors. TypeRegA (0x00) is the zero-value
		// regular-file flag emitted by older archive writers, so accept it too.
		if hdr.Typeflag != tar.TypeReg && hdr.Typeflag != tar.TypeRegA { //nolint:staticcheck // TypeRegA is the legacy zero-value regular-file flag; still restorable
			continue
		}
		count++
		if count > maxBackupEntryCount {
			return fmt.Errorf("backup restore: too many entries (> %d)", maxBackupEntryCount)
		}
		if hdr.Size > maxBackupEntrySize {
			return fmt.Errorf("backup restore: entry %s too large (%d bytes)", hdr.Name, hdr.Size)
		}

		switch {
		case hdr.Name == "manifest.json":
			data, err := readEntryBounded(tr, maxBackupManifestSize)
			if err != nil {
				return fmt.Errorf("backup restore: read manifest: %w", err)
			}
			if err := json.Unmarshal(data, &manifest); err != nil {
				return fmt.Errorf("backup restore: parse manifest: %w", err)
			}
		case hdr.Name == "config.yaml":
			if *configPath == "" {
				continue
			}
			sf, err := stageEntry(tr, hdr.Name, *configPath)
			if err != nil {
				return err
			}
			staged = append(staged, sf)
		case strings.HasPrefix(hdr.Name, "state/"):
			rel := strings.TrimPrefix(hdr.Name, "state/")
			dest, err := safeDataDirJoin(dataDir, rel)
			if err != nil {
				return err
			}
			sf, err := stageEntry(tr, hdr.Name, dest)
			if err != nil {
				return err
			}
			staged = append(staged, sf)
		default:
			// Unknown / informational entry (e.g. secrets/README.txt): ignore.
			continue
		}
	}

	if manifest.CreatedAt == "" {
		return errors.New("backup restore: manifest.json not found in archive")
	}

	// Validate sha256 sums against the manifest (extra files without a sum are
	// tolerated, as before).
	for _, s := range staged {
		expected, ok := manifest.SHA256[s.archivePath]
		if !ok {
			continue
		}
		if s.sha256 != expected {
			return fmt.Errorf("backup restore: sha256 mismatch for %s: want %s got %s",
				s.archivePath, expected, s.sha256)
		}
	}

	// Refuse a backup produced by a newer daemon (its schema this binary can't
	// operate) unless the operator forces it.
	if err := checkSchemaCompat(manifest, *force); err != nil {
		return err
	}

	// All entries staged and validated — swap them into place atomically.
	if err := commitRestore(staged); err != nil {
		return fmt.Errorf("backup restore: commit: %w", err)
	}
	committed = true

	_, _ = fmt.Fprintf(stdout, "backup restored from %s to %s\n", archivePath, dataDir)
	return nil
}

// openBackupBody peeks the archive header to decide whether the body is an
// encrypted container or a legacy plaintext (bare gzip) archive, and returns
// a reader positioned at the start of the gzip stream. For an encrypted
// archive it wraps the file in a decrypting reader keyed by the data-dir
// master key. The bool reports whether the archive was encrypted.
func openBackupBody(f *os.File, dataDir string, stderr io.Writer) (io.Reader, bool, error) {
	magic := make([]byte, len(backupMagicV1))
	n, err := io.ReadFull(f, magic)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return nil, false, fmt.Errorf("backup restore: read header: %w", err)
	}
	if n == len(backupMagicV1) && bytes.Equal(magic, backupMagicV1) {
		cipher, cerr := secret.Load(dataDir, nil, nil)
		if cerr != nil {
			return nil, true, fmt.Errorf("backup restore: load master key: %w", cerr)
		}
		if !cipher.Available() {
			return nil, true, errors.New("backup restore: archive is encrypted but no master key is " +
				"available (set OPENCCU_LOOM_SECRET_KEY or restore the original secret.key first)")
		}
		return cipher.NewDecryptReader(f), true, nil
	}
	// Legacy plaintext archive: put the peeked bytes back in front of the file.
	_, _ = fmt.Fprintln(stderr, "note: restoring a legacy unencrypted backup archive")
	return io.MultiReader(bytes.NewReader(magic[:n]), f), false, nil
}

// readEntryBounded reads up to limit bytes from r, returning an error if the
// entry is larger.
func readEntryBounded(r io.Reader, limit int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("entry exceeds %d bytes", limit)
	}
	return data, nil
}

// safeDataDirJoin resolves rel against dataDir and guarantees the result stays
// strictly inside dataDir. It rejects absolute paths and any `..` traversal —
// the Zip-Slip guard for restore.
func safeDataDirJoin(dataDir, rel string) (string, error) {
	if rel == "" || rel == "." {
		return "", errors.New("backup restore: empty archive path under state/")
	}
	// Reject absolute paths on any platform. filepath.IsAbs is OS-specific — a
	// Unix "/etc/x" is not "absolute" on Windows — but a leading separator must
	// never appear in a state/-relative entry, regardless of where the archive
	// was built or is being restored.
	if filepath.IsAbs(rel) || rel[0] == '/' || rel[0] == '\\' {
		return "", fmt.Errorf("backup restore: absolute path in archive: %q", rel)
	}
	cleanBase := filepath.Clean(dataDir)
	dest := filepath.Clean(filepath.Join(cleanBase, rel))
	if dest != cleanBase && !strings.HasPrefix(dest, cleanBase+string(filepath.Separator)) {
		return "", fmt.Errorf("backup restore: path escapes data dir: %q", rel)
	}
	return dest, nil
}

// stageEntry streams one tar entry to a private temp file in the same
// directory as its final destination (so the later rename is atomic and
// same-filesystem), returning its sha256. It never writes to the live path.
func stageEntry(r io.Reader, archivePath, live string) (stagedRestoreFile, error) {
	dir := filepath.Dir(live)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return stagedRestoreFile{}, fmt.Errorf("backup restore: mkdir %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, ".restore-*.tmp")
	if err != nil {
		return stagedRestoreFile{}, fmt.Errorf("backup restore: temp for %s: %w", archivePath, err)
	}
	tmpPath := tmp.Name()

	hasher := sha256.New()
	// Bound the per-entry copy: total decompression is already capped, but a
	// single huge entry must not fill the disk either.
	written, err := io.Copy(io.MultiWriter(tmp, hasher), io.LimitReader(r, maxBackupEntrySize+1))
	if cerr := tmp.Close(); cerr != nil && err == nil {
		err = cerr
	}
	if err != nil {
		_ = os.Remove(tmpPath)
		return stagedRestoreFile{}, fmt.Errorf("backup restore: stage %s: %w", archivePath, err)
	}
	if written > maxBackupEntrySize {
		_ = os.Remove(tmpPath)
		return stagedRestoreFile{}, fmt.Errorf("backup restore: entry %s exceeds %d bytes", archivePath, maxBackupEntrySize)
	}
	// Restored files are private (0600); the DB carries secret material.
	if err := os.Chmod(tmpPath, 0o600); err != nil {
		_ = os.Remove(tmpPath)
		return stagedRestoreFile{}, fmt.Errorf("backup restore: chmod %s: %w", archivePath, err)
	}
	return stagedRestoreFile{
		archivePath: archivePath,
		live:        live,
		tempPath:    tmpPath,
		sha256:      hex.EncodeToString(hasher.Sum(nil)),
	}, nil
}

// checkSchemaCompat refuses a backup stamped with a schema newer than this
// binary supports, unless force is set. A backup without a schema stamp
// (legacy) is accepted.
func checkSchemaCompat(manifest backupManifest, force bool) error {
	backupVer, ok := manifest.SchemaVersions["openccu-loom.db"]
	if !ok {
		return nil
	}
	maxKnown, err := sqlite.MaxKnownMigration()
	if err != nil {
		return fmt.Errorf("backup restore: schema check: %w", err)
	}
	if int64(backupVer) > maxKnown && !force {
		return fmt.Errorf("backup restore: backup schema version %d is newer than this binary supports (%d); "+
			"restore with a newer daemon or pass --force to override", backupVer, maxKnown)
	}
	return nil
}

// sqliteSidecarSuffixes are the write-ahead log and shared-memory files SQLite
// keeps beside a database opened in WAL mode.
var sqliteSidecarSuffixes = [...]string{"-wal", "-shm"}

// asideEntry records a live file moved out of the way during commit so a
// failure can put it back.
type asideEntry struct{ live, backup string }

// commitRestore swaps every staged temp file into its live destination. It is
// all-or-nothing on a best-effort basis: existing live files are moved aside
// first (a same-directory rename), and any failure rolls the whole set back so
// a mid-restore error never leaves half-applied live data.
//
// A restored `.db` takes its WAL and SHM sidecars with it. They belong to the
// database that was there before — a daemon killed uncleanly leaves a populated
// `-wal` behind — and SQLite recovers a `-wal` it finds next to a database file
// on the next open, replaying the previous database's pages into the restored
// one. The sidecars are moved aside in the same all-or-nothing set as the
// database itself, so a failed commit puts the original trio back together.
func commitRestore(staged []stagedRestoreFile) error {
	var (
		movedAside []asideEntry
		placed     []string
	)
	rollback := func() {
		for _, p := range placed {
			_ = os.Remove(p)
		}
		for _, m := range movedAside {
			_ = os.Rename(m.backup, m.live)
		}
	}
	cleanup := func() {
		for _, m := range movedAside {
			_ = os.Remove(m.backup)
		}
	}

	for _, s := range staged {
		if err := os.MkdirAll(filepath.Dir(s.live), 0o750); err != nil {
			rollback()
			return fmt.Errorf("mkdir %s: %w", filepath.Dir(s.live), err)
		}
		if _, err := os.Stat(s.live); err == nil {
			backup, berr := uniqueSidecar(s.live)
			if berr != nil {
				rollback()
				return berr
			}
			if err := os.Rename(s.live, backup); err != nil {
				rollback()
				return fmt.Errorf("move aside %s: %w", s.live, err)
			}
			movedAside = append(movedAside, asideEntry{live: s.live, backup: backup})
		}
		if strings.HasSuffix(s.live, ".db") {
			aside, err := moveSidecarsAside(s.live)
			movedAside = append(movedAside, aside...)
			if err != nil {
				rollback()
				return err
			}
		}
		if err := os.Rename(s.tempPath, s.live); err != nil {
			rollback()
			return fmt.Errorf("place %s: %w", s.live, err)
		}
		placed = append(placed, s.live)
	}
	cleanup()
	return nil
}

// moveSidecarsAside renames the WAL/SHM sidecars of the database at dbPath out
// of the way and returns what it moved, so the caller can roll them back or
// discard them with the rest of the commit set. The entries collected so far
// are returned even on error — they still have to be rolled back.
func moveSidecarsAside(dbPath string) ([]asideEntry, error) {
	var moved []asideEntry
	for _, suffix := range sqliteSidecarSuffixes {
		sidecar := dbPath + suffix
		if _, err := os.Stat(sidecar); err != nil {
			continue
		}
		backup, err := uniqueSidecar(sidecar)
		if err != nil {
			return moved, err
		}
		if err := os.Rename(sidecar, backup); err != nil {
			return moved, fmt.Errorf("move aside %s: %w", sidecar, err)
		}
		moved = append(moved, asideEntry{live: sidecar, backup: backup})
	}
	return moved, nil
}

// uniqueSidecar returns a fresh, unique path in the same directory as live,
// used to move the existing live file aside during commit.
func uniqueSidecar(live string) (string, error) {
	dir := filepath.Dir(live)
	base := filepath.Base(live)
	tmp, err := os.CreateTemp(dir, base+".restore-bak-*")
	if err != nil {
		return "", fmt.Errorf("sidecar for %s: %w", live, err)
	}
	name := tmp.Name()
	_ = tmp.Close()
	return name, nil
}

// loadBootstrapForCLI loads the bootstrap config for CLI subcommands.
// When configPath is empty, DefaultBootstrap is used.
func loadBootstrapForCLI(configPath string, stderr io.Writer) (*config.BootstrapConfig, error) {
	var bc *config.BootstrapConfig
	if configPath == "" {
		bc = config.DefaultBootstrap()
	} else {
		loaded, err := config.LoadBootstrap(configPath)
		if err != nil {
			return nil, fmt.Errorf("load config: %w", err)
		}
		bc = loaded
	}
	// Honor OPENCCU_LOOM_DATA_DIR (and the other bootstrap-tier env vars) so a
	// containerised CLI run without --config opens the same /data store the
	// daemon uses, not the ephemeral "./var" default.
	bc.OverlayFromEnv(nil)
	if err := bc.Validate(); err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	if configPath == "" {
		_, _ = fmt.Fprintf(stderr, "openccu-loom: no --config given, using defaults (DataDir=%s)\n", bc.DataDir)
	}
	return bc, nil
}
