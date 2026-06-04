// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package main

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
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
	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/internal/store/sqlite"
)

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
	Path   string `json:"path"`
	Bytes  int64  `json:"bytes"`
	SHA256 string `json:"sha256"`
}

func backupCreate(args []string, stdout, stderr io.Writer) error {
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
			// Skip the live DB, WAL, and SHM files — the VACUUMed copy is added separately.
			base := filepath.Base(p)
			if base == "openccu-loom.db" || base == "openccu-loom.db-wal" || base == "openccu-loom.db-shm" {
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

	// Optionally include config.yaml.
	if *configPath != "" {
		if _, err := os.Stat(*configPath); err == nil {
			entries = append(entries, entry{
				archivePath: "config.yaml",
				diskPath:    *configPath,
			})
		}
	}

	// Build sha256 map and write archive.
	sha256Map := make(map[string]string)
	outF, err := os.Create(dest) //nolint:gosec // operator-controlled destination path
	if err != nil {
		return fmt.Errorf("backup create: create output: %w", err)
	}
	archiveHasher := sha256.New()
	mw := io.MultiWriter(outF, archiveHasher)
	gz := gzip.NewWriter(mw)
	tw := tar.NewWriter(gz)

	for _, e := range entries {
		if e.isDir {
			hdr := &tar.Header{
				Typeflag: tar.TypeDir,
				Name:     e.archivePath + "/",
				Mode:     0o755,
			}
			if err := tw.WriteHeader(hdr); err != nil {
				_ = outF.Close()
				return fmt.Errorf("backup create: tar dir header: %w", err)
			}
			continue
		}
		sum, err := addFileToTar(tw, e.archivePath, e.diskPath)
		if err != nil {
			_ = outF.Close()
			return fmt.Errorf("backup create: add %s: %w", e.archivePath, err)
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
			_ = outF.Close()
			return fmt.Errorf("backup create: secrets dir: %w", err)
		}
		readmeContent := "Secrets resolved from environment variables are not stored in the archive.\n" +
			"Set the relevant environment variables (e.g. OPENCCU_LOOM_MQTT_PASSWORD) before restore.\n"
		sum, err := addBytesToTar(tw, "secrets/README.txt", []byte(readmeContent))
		if err != nil {
			_ = outF.Close()
			return fmt.Errorf("backup create: secrets readme: %w", err)
		}
		sha256Map["secrets/README.txt"] = sum
	}

	// Write manifest last so it can include all sha256 sums.
	manifest := backupManifest{
		DaemonVersion: build.Version,
		CreatedAt:     time.Now().UTC().Format(time.RFC3339),
		SHA256:        sha256Map,
	}
	manifestJSON, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		_ = outF.Close()
		return fmt.Errorf("backup create: manifest: %w", err)
	}
	if _, err := addBytesToTar(tw, "manifest.json", manifestJSON); err != nil {
		_ = outF.Close()
		return fmt.Errorf("backup create: manifest tar: %w", err)
	}

	if err := tw.Close(); err != nil {
		_ = outF.Close()
		return fmt.Errorf("backup create: tar close: %w", err)
	}
	if err := gz.Close(); err != nil {
		_ = outF.Close()
		return fmt.Errorf("backup create: gzip close: %w", err)
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
		res := backupCreateResult{Path: dest, Bytes: fi.Size(), SHA256: archiveSHA}
		b, _ := json.Marshal(res)
		_, _ = fmt.Fprintf(stdout, "%s\n", b)
	} else {
		_, _ = fmt.Fprintf(stdout, "backup created: %s  (%d bytes, sha256=%s)\n", dest, fi.Size(), archiveSHA)
	}
	return nil
}

// vacuumInto opens src with a read-only connection and executes
// `VACUUM INTO dest`. The dest file must not exist.
func vacuumInto(src, dest string) error {
	ctx := context.Background()
	db, err := sqlite.Open(ctx, src+"?mode=ro")
	if err != nil {
		// Fallback: open without read-only hint for compatibility.
		db, err = sqlite.Open(ctx, src)
		if err != nil {
			return fmt.Errorf("vacuum: open src: %w", err)
		}
	}
	defer func() { _ = db.Close() }()
	_, err = db.ExecContext(ctx, "VACUUM INTO ?", dest)
	if err != nil {
		return fmt.Errorf("vacuum: exec: %w", err)
	}
	return nil
}

// addFileToTar adds the file at diskPath to tw with the given archivePath and
// returns the hex-encoded sha256 of the file content.
func addFileToTar(tw *tar.Writer, archivePath, diskPath string) (string, error) {
	f, err := os.Open(diskPath) //nolint:gosec // caller-controlled paths
	if err != nil {
		return "", fmt.Errorf("open %s: %w", diskPath, err)
	}
	defer func() { _ = f.Close() }()

	fi, err := f.Stat()
	if err != nil {
		return "", fmt.Errorf("stat %s: %w", diskPath, err)
	}

	hdr := &tar.Header{
		Name:    archivePath,
		Mode:    0o600,
		Size:    fi.Size(),
		ModTime: fi.ModTime(),
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
		Name:    archivePath,
		Mode:    0o600,
		Size:    int64(len(buf)),
		ModTime: time.Now().UTC(),
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

func backupRestore(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("backup restore", flag.ContinueOnError)
	fs.SetOutput(stderr)
	configPath := fs.String("config", "", "path to config.yaml (receives extracted config.yaml when present)")
	dataDirFlag := fs.String("data-dir", "", "override DataDir (default: from --config or ./var)")
	force := fs.Bool("force", false, "overwrite existing DB without prompt")
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

	// Open and read the archive, validate sha256 sums, collect entries.
	f, err := os.Open(archivePath) //nolint:gosec // operator-supplied path
	if err != nil {
		return fmt.Errorf("backup restore: open archive: %w", err)
	}
	defer func() { _ = f.Close() }()

	gr, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("backup restore: gzip: %w", err)
	}
	defer func() { _ = gr.Close() }()

	tr := tar.NewReader(gr)

	type fileEntry struct {
		archivePath string
		data        []byte
	}
	var files []fileEntry

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
		data, err := io.ReadAll(tr)
		if err != nil {
			return fmt.Errorf("backup restore: read entry %s: %w", hdr.Name, err)
		}
		files = append(files, fileEntry{archivePath: hdr.Name, data: data})
	}

	// Parse manifest.
	var manifest backupManifest
	for _, fe := range files {
		if fe.archivePath == "manifest.json" {
			if err := json.Unmarshal(fe.data, &manifest); err != nil {
				return fmt.Errorf("backup restore: parse manifest: %w", err)
			}
			break
		}
	}
	if manifest.CreatedAt == "" {
		return errors.New("backup restore: manifest.json not found in archive")
	}

	// Validate sha256 sums.
	for _, fe := range files {
		if fe.archivePath == "manifest.json" {
			continue
		}
		expected, ok := manifest.SHA256[fe.archivePath]
		if !ok {
			continue // extra files without a sha256 entry are tolerated
		}
		sum := sha256.Sum256(fe.data)
		got := hex.EncodeToString(sum[:])
		if got != expected {
			return fmt.Errorf("backup restore: sha256 mismatch for %s: want %s got %s",
				fe.archivePath, expected, got)
		}
	}

	// Write to a staging directory, then atomically rename the DB.
	if err := os.MkdirAll(dataDir, 0o750); err != nil {
		return fmt.Errorf("backup restore: mkdir data dir: %w", err)
	}
	stagingDir, err := os.MkdirTemp(dataDir, ".restore-staging-*")
	if err != nil {
		return fmt.Errorf("backup restore: staging dir: %w", err)
	}
	defer func() { _ = os.RemoveAll(stagingDir) }()

	for _, fe := range files {
		switch {
		case fe.archivePath == "state/openccu-loom.db":
			stagingDB := filepath.Join(stagingDir, "openccu-loom.db")
			if err := os.WriteFile(stagingDB, fe.data, 0o600); err != nil {
				return fmt.Errorf("backup restore: write staging db: %w", err)
			}
			if err := os.Rename(stagingDB, targetDB); err != nil {
				return fmt.Errorf("backup restore: rename db: %w", err)
			}
		case fe.archivePath == "config.yaml" && *configPath != "":
			if err := os.WriteFile(*configPath, fe.data, 0o600); err != nil {
				return fmt.Errorf("backup restore: write config.yaml: %w", err)
			}
		case strings.HasPrefix(fe.archivePath, "state/"):
			rel := strings.TrimPrefix(fe.archivePath, "state/")
			dest := filepath.Join(dataDir, rel)
			if err := os.MkdirAll(filepath.Dir(dest), 0o750); err != nil {
				return fmt.Errorf("backup restore: mkdir for %s: %w", rel, err)
			}
			if err := os.WriteFile(dest, fe.data, 0o600); err != nil {
				return fmt.Errorf("backup restore: write %s: %w", rel, err)
			}
		}
	}

	_, _ = fmt.Fprintf(stdout, "backup restored from %s to %s\n", archivePath, dataDir)
	return nil
}

// loadBootstrapForCLI loads the bootstrap config for CLI subcommands.
// When configPath is empty, DefaultBootstrap is used.
func loadBootstrapForCLI(configPath string, stderr io.Writer) (*config.BootstrapConfig, error) {
	if configPath == "" {
		_, _ = fmt.Fprintln(stderr, "openccu-loom: no --config given, using defaults (DataDir=./var)")
		return config.DefaultBootstrap(), nil
	}
	bc, err := config.LoadBootstrap(configPath)
	if err != nil {
		return nil, fmt.Errorf("load config: %w", err)
	}
	return bc, nil
}
