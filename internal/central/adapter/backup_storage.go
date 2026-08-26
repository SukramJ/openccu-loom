// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/SukramJ/openccu-loom/pkg/hmerr"

	"github.com/SukramJ/openccu-loom/pkg/hmapi"
)

// BackupStorage abstracts where the daemon keeps locally-stored backup files.
// The default implementation reads `.sbk` files from a directory; other
// backends (S3, NFS, encrypted volume) can satisfy the same surface.
type BackupStorage interface {
	// List enumerates the backups currently held in storage.
	List(ctx context.Context) ([]hmapi.BackupEntry, error)
	// Open returns a reader for the backup payload. The caller closes
	// the reader.
	Open(ctx context.Context, id string) (io.ReadCloser, error)
	// Save persists a freshly downloaded backup payload under id. The
	// id must be a bare token (no path separators or dot segments); the
	// implementation appends its own extension. Overwrites an existing
	// backup with the same id.
	//
	// filename is the archive's name in the CCU's own convention, to be
	// returned as [hmapi.BackupEntry.Filename] and served as the download
	// name. It is stored, not used as the storage key: the id owns the
	// layout, the filename only describes the archive. Empty is allowed —
	// the CCU may not have reported its system information yet — and reads
	// back as empty, which callers render as `<id>.sbk`.
	Save(ctx context.Context, id, filename string, data []byte) error
	// Delete removes the backup stored under id. A missing backup is not an
	// error (idempotent). Used by the scheduled-backup rotation (Prune).
	Delete(ctx context.Context, id string) error
}

// BackupStorageLocator is the optional capability a [BackupStorage]
// implements when it can name where it keeps its archives.
//
// It is separate from [BackupStorage] because not every backend has a
// location an operator can act on — a future object-store backend would
// report a bucket URL, an in-memory one nothing at all — while the
// filesystem backend's directory is the single most useful fact about it:
// on a CCU add-on install it is resolved at every start from the CCU's own
// backup target, so it differs per installation and changes when a USB
// stick is plugged in.
//
// It is a capability interface rather than a type assertion on
// [FilesystemBackupStorage] so a second filesystem-shaped backend does not
// silently lose the location the moment it is swapped in.
type BackupStorageLocator interface {
	// Location returns the human-readable place archives are kept —
	// an absolute directory path for the filesystem backend. Empty means
	// the backend has no location to report.
	Location() string
}

// BackupRestorer uploads a backup payload back to the CCU. The
// implementation is responsible for the HTTP-multipart POST against
// the CCU's `cp_security.cgi` (or equivalent) endpoint.
//
// Returning [ErrRestoreUnsupported] keeps the adapter degraded —
// operators still see the trigger / list / download paths, but the
// SPA renders a clear "manual restore required" message.
type BackupRestorer interface {
	// Restore reinstalls the backup identified by id. Returns the
	// (re-used) id so the caller can poll for completion via the
	// regular job-tracking endpoints.
	Restore(ctx context.Context, id string, payload io.Reader) (string, error)
}

// ErrRestoreUnsupported is returned by [BackupAdapter.Restore] when no
// concrete [BackupRestorer] has been wired. The handler surfaces a
// 502/501 with this error's message so the SPA can render a clear
// status.
var ErrRestoreUnsupported = hmerr.ErrRestoreUnsupported

// FilesystemBackupStorage reads `.sbk` files from a directory. The
// directory is created on construction if it doesn't exist.
type FilesystemBackupStorage struct {
	Dir string
}

// backupTempPattern names the scratch file a [FilesystemBackupStorage.Save]
// writes before it publishes the finished archive under its real name. It
// deliberately does not end in `.sbk`: a save that dies half-way (out of
// disk, SIGKILL) then leaves a file that List skips and Open cannot resolve,
// instead of a torso the SPA offers for download and Restore uploads to a CCU.
const backupTempPattern = ".partial-*.tmp"

// NoBackupTagName is the marker file that excludes a directory's contents
// from a CCU backup. Both archive producers on the CCU side — the WebUI's
// `create_backup` CGI and `/bin/createBackup.sh` — tar `/usr/local` with
// GNU tar's `--exclude-tag=.nobackup`, which drops the contents of every
// directory holding this file while keeping the directory itself.
//
// It matters when the daemon runs as a CCU add-on: its data directory then
// lives under `/usr/local/addons/`, so without the marker every CCU backup
// would contain all previously downloaded `.sbk` archives, and the next
// backup would contain those in turn. Because the tag only masks the
// containing directory, the sibling state beside it — the SQLite database,
// the at-rest key — still travels in the CCU backup, which is what an
// operator restoring a CCU wants.
//
// Off a CCU the file is inert.
const NoBackupTagName = ".nobackup"

// NewFilesystemBackupStorage constructs the storage and ensures the
// directory exists.
func NewFilesystemBackupStorage(dir string) (*FilesystemBackupStorage, error) {
	if dir == "" {
		return nil, errors.New("backup: empty directory")
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, fmt.Errorf("backup: mkdir %s: %w", dir, err)
	}
	writeNoBackupTag(dir)
	return &FilesystemBackupStorage{Dir: dir}, nil
}

// writeNoBackupTag drops [NoBackupTagName] into dir. It is best-effort by
// design: a directory that rejects the marker (read-only mount, foreign
// ownership) can still serve every archive already in it, and failing
// construction would take the whole backup surface down over a file whose
// only reader is tar on a different machine.
func writeNoBackupTag(dir string) {
	path := filepath.Join(dir, NoBackupTagName)
	if _, err := os.Stat(path); err == nil {
		return
	}
	// The marker carries no content — only its presence is read, and only by
	// a tar running as root — so it needs no group access.
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0o600) // #nosec G304 — a fixed name inside the configured storage directory
	if err != nil {
		return
	}
	_ = f.Close()
}

// List implements [BackupStorage].
func (s *FilesystemBackupStorage) List(_ context.Context) ([]hmapi.BackupEntry, error) {
	entries, err := os.ReadDir(s.Dir)
	if err != nil {
		return nil, fmt.Errorf("backup: read dir: %w", err)
	}
	out := make([]hmapi.BackupEntry, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".sbk") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if info.Size() == 0 {
			// A zero-byte archive is not a backup: it is what an interrupted
			// write left behind (or a hand-placed stub). Listing it offers an
			// empty file for download and lets Restore push it to a CCU.
			continue
		}
		id := strings.TrimSuffix(name, ".sbk")
		out = append(out, hmapi.BackupEntry{
			ID:        id,
			Bytes:     info.Size(),
			CreatedAt: info.ModTime().UTC(),
			Filename:  s.readName(id),
		})
	}
	return out, nil
}

// Open implements [BackupStorage].
func (s *FilesystemBackupStorage) Open(_ context.Context, id string) (io.ReadCloser, error) {
	path, err := s.pathForID(id)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(path) // #nosec G304 — path is validated against s.Dir above
	if err != nil {
		return nil, fmt.Errorf("backup: open %s: %w", path, err)
	}
	// Refuse an empty archive here as well as in List: a restore addresses a
	// backup by id, so hiding the torso from the listing alone would still
	// leave it uploadable to a CCU.
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("backup: stat %s: %w", path, err)
	}
	if info.Size() == 0 {
		_ = f.Close()
		return nil, fmt.Errorf("backup: %s is empty (interrupted save)", path)
	}
	return f, nil
}

// Save implements [BackupStorage]. It writes data to `<id>.sbk` inside the
// storage directory, replacing any existing file with the same id.
//
// The archive is published atomically: the payload goes to a scratch file in
// the same directory, is flushed to disk, and only then renamed onto the
// final name. A truncating write in place would expose every intermediate
// state under the real name — a save killed by SIGKILL or a full disk would
// leave a torso that List shows and Restore uploads to a CCU — and, on a
// replace, would destroy the previous complete archive before the new one
// exists.
func (s *FilesystemBackupStorage) Save(_ context.Context, id, filename string, data []byte) error {
	path, err := s.pathForID(id)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(s.Dir, backupTempPattern)
	if err != nil {
		return fmt.Errorf("backup: temp file in %s: %w", s.Dir, err)
	}
	tmpPath := tmp.Name()
	abandon := func(cause error) error {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return cause
	}
	if _, err := tmp.Write(data); err != nil {
		return abandon(fmt.Errorf("backup: write %s: %w", tmpPath, err))
	}
	// CreateTemp makes the file 0600; widen it to the mode the storage has
	// always used — operator-readable, never world-readable.
	if err := tmp.Chmod(0o640); err != nil {
		return abandon(fmt.Errorf("backup: chmod %s: %w", tmpPath, err))
	}
	// Flush before the rename. Without it a power loss can publish the final
	// name over data the kernel has not written yet.
	if err := tmp.Sync(); err != nil {
		return abandon(fmt.Errorf("backup: sync %s: %w", tmpPath, err))
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("backup: close %s: %w", tmpPath, err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("backup: publish %s: %w", path, err)
	}
	// The name is written after the archive is published, and its failure is
	// not the save's failure: an archive without its name reads back as
	// `<id>.sbk`, while a name without an archive would be a listing entry
	// with nothing behind it.
	s.writeName(id, filename)
	// Best-effort: make the rename itself durable. A directory that cannot be
	// synced (or a platform that does not support it) is not a reason to fail
	// a save whose payload is already on disk.
	if dir, err := os.Open(s.Dir); err == nil { // #nosec G304 — the configured storage directory
		_ = dir.Sync()
		_ = dir.Close()
	}
	return nil
}

// backupNameSuffix marks the sidecar that records an archive's CCU-convention
// name. A sidecar rather than an encoded id because the id is parsed: the
// owning-central lookup and the rotation pruner both recover the central by
// stripping a fixed-width timestamp suffix, so anything else in the id breaks
// both. It deliberately does not end in `.sbk`, which keeps it out of List's
// own scan.
const backupNameSuffix = ".name"

// writeName records filename as the archive's display name. Best-effort: see
// the call site in [FilesystemBackupStorage.Save].
func (s *FilesystemBackupStorage) writeName(id, filename string) {
	path, err := s.namePathForID(id)
	if err != nil {
		return
	}
	if filename == "" {
		// An empty name is the absence of one, not a name that is empty. Drop
		// a stale sidecar rather than leave it describing a replaced archive.
		_ = os.Remove(path)
		return
	}
	// 0o640 matches the archive the sidecar describes: operator-readable,
	// never world-readable.
	_ = os.WriteFile(path, []byte(filename), 0o640) // #nosec G306 — group-readable by design, not group-writable
}

// readName returns the recorded display name for id, or "" when none was
// recorded — an archive taken before the sidecar existed, or one saved while
// the CCU had not reported its system information yet.
//
// The value is sanitised on the way out rather than trusted: it reaches an
// HTTP Content-Disposition header, and a name carrying a path separator or a
// control character there is a header-injection and path-traversal vector. A
// name that does not survive sanitising is dropped, which degrades to
// `<id>.sbk`.
func (s *FilesystemBackupStorage) readName(id string) string {
	path, err := s.namePathForID(id)
	if err != nil {
		return ""
	}
	raw, err := os.ReadFile(path) // #nosec G304 — path is validated against s.Dir
	if err != nil {
		return ""
	}
	name := strings.TrimSpace(string(raw))
	if name == "" || len(name) > 255 || strings.ContainsAny(name, "/\\\r\n\"") {
		return ""
	}
	for _, r := range name {
		if r < 0x20 || r == 0x7f {
			return ""
		}
	}
	return name
}

// namePathForID resolves id to its name sidecar, derived from the archive
// path [FilesystemBackupStorage.pathForID] already validated rather than
// rebuilt from the raw id.
//
// The distinction matters even though both spellings reject the same ids
// today: building a second path from the unvalidated id leaves the check and
// the use in different expressions, so a later change to pathForID's
// containment rule silently stops applying here — and a reader (or a taint
// analyser) has to prove the equivalence by hand. Deriving keeps the sidecar
// beside the archive it describes by construction.
func (s *FilesystemBackupStorage) namePathForID(id string) (string, error) {
	archive, err := s.pathForID(id)
	if err != nil {
		return "", err
	}
	return strings.TrimSuffix(archive, ".sbk") + backupNameSuffix, nil
}

// Delete implements [BackupStorage]. A missing file is not an error.
func (s *FilesystemBackupStorage) Delete(_ context.Context, id string) error {
	path, err := s.pathForID(id)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("backup: delete %s: %w", path, err)
	}
	// The name sidecar goes with the archive it describes. Left behind, the
	// next id to collide with it would inherit the deleted archive's name.
	s.writeName(id, "")
	return nil
}

// Location implements [BackupStorageLocator].
func (s *FilesystemBackupStorage) Location() string { return s.Dir }

// pathForID resolves id to a `.sbk` path inside Dir, rejecting ids that
// contain path separators or dot segments so a crafted id cannot escape
// the storage directory.
func (s *FilesystemBackupStorage) pathForID(id string) (string, error) {
	if strings.ContainsAny(id, "/\\") || id == "" || id == "." || id == ".." {
		return "", fmt.Errorf("backup: invalid id %q", id)
	}
	path := filepath.Clean(filepath.Join(s.Dir, id+".sbk"))
	if !strings.HasPrefix(path, filepath.Clean(s.Dir)+string(filepath.Separator)) {
		return "", fmt.Errorf("backup: invalid id %q", id)
	}
	return path, nil
}

// SetStorage swaps the storage backend. Default is no storage (List
// returns empty, Stream reports the storage as not configured).
// Returns the receiver for chaining.
func (a *BackupAdapter) SetStorage(s BackupStorage) *BackupAdapter {
	a.storage = s
	return a
}

// SetRestorer swaps the legacy fallback restore client, used only when
// a restore's owning central cannot be resolved from the backup id (see
// [BackupAdapter.resolveRestorer]). Default is no restorer; calls to
// [BackupAdapter.Restore] for an unresolvable id then return
// [ErrRestoreUnsupported]. Returns the receiver for chaining.
func (a *BackupAdapter) SetRestorer(r BackupRestorer) *BackupAdapter {
	a.restorersMu.Lock()
	defer a.restorersMu.Unlock()
	a.restorer = r
	return a
}

// SetRestorerForCentral wires the restorer that owns restores for
// centralName. This is the multi-CCU-correct wiring path: the daemon
// composition root calls it once per central as each one comes up, so a
// fleet with several registered centrals always uploads a restore to
// the CCU that produced the backup, never to a different one. Returns
// the receiver for chaining.
func (a *BackupAdapter) SetRestorerForCentral(centralName string, r BackupRestorer) *BackupAdapter {
	if centralName == "" {
		return a
	}
	a.restorersMu.Lock()
	defer a.restorersMu.Unlock()
	if a.restorers == nil {
		a.restorers = make(map[string]BackupRestorer)
	}
	a.restorers[centralName] = r
	return a
}

// Restorer returns the currently wired legacy fallback restorer, or
// nil. The wiring path consults this to decide whether the fallback has
// already been installed.
func (a *BackupAdapter) Restorer() BackupRestorer {
	if a == nil {
		return nil
	}
	a.restorersMu.RLock()
	defer a.restorersMu.RUnlock()
	return a.restorer
}

// RestorerForCentral returns the restorer wired for centralName via
// [BackupAdapter.SetRestorerForCentral], or nil when none has been
// wired yet (the central has not finished bring-up, or is unknown).
func (a *BackupAdapter) RestorerForCentral(centralName string) BackupRestorer {
	if a == nil {
		return nil
	}
	a.restorersMu.RLock()
	defer a.restorersMu.RUnlock()
	return a.restorers[centralName]
}

// uploadedBackupPrefix marks ids of archives the operator supplied rather
// than ones this daemon pulled from a CCU. It keeps them apart in the
// listing and, more importantly, keeps the scheduled-backup pruner from
// ever treating an imported archive as one of its own rotations.
const uploadedBackupPrefix = "upload-"

// SaveUploaded stores an externally-supplied archive under a generated id
// and returns the entry describing it.
//
// The id is derived from the wall clock rather than the uploaded file
// name: a name comes from a browser and cannot be trusted to be unique,
// path-safe, or even present. The original name is deliberately not kept
// — nothing downstream reads it, and storing attacker-controlled text
// would be a liability for no gain.
func (s *FilesystemBackupStorage) SaveUploaded(
	ctx context.Context, _ string, data []byte,
) (hmapi.BackupEntry, error) {
	now := time.Now().UTC()
	id := uploadedBackupPrefix + now.Format("20060102-150405.000")
	// The generated id carries a dot from the millisecond field, which
	// pathForID tolerates; only separators and the dot-only names are
	// rejected. Guard anyway so a future format change cannot silently
	// produce a traversal.
	if _, err := s.pathForID(id); err != nil {
		return hmapi.BackupEntry{}, err
	}
	// No display name: the uploaded one is untrusted (see above) and there is
	// no CCU behind this archive to derive one from. It lists and downloads as
	// `<id>.sbk`, which is honest about where it came from.
	if err := s.Save(ctx, id, "", data); err != nil {
		return hmapi.BackupEntry{}, err
	}
	return hmapi.BackupEntry{
		ID:        id,
		Bytes:     int64(len(data)),
		CreatedAt: now,
	}, nil
}
