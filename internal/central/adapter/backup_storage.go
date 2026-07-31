// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

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
	Save(ctx context.Context, id string, data []byte) error
	// Delete removes the backup stored under id. A missing backup is not an
	// error (idempotent). Used by the scheduled-backup rotation (Prune).
	Delete(ctx context.Context, id string) error
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
var ErrRestoreUnsupported = errors.New("backup: restore not configured (wire a BackupRestorer)")

// FilesystemBackupStorage reads `.sbk` files from a directory. The
// directory is created on construction if it doesn't exist.
type FilesystemBackupStorage struct {
	Dir string
}

// NewFilesystemBackupStorage constructs the storage and ensures the
// directory exists.
func NewFilesystemBackupStorage(dir string) (*FilesystemBackupStorage, error) {
	if dir == "" {
		return nil, errors.New("backup: empty directory")
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, fmt.Errorf("backup: mkdir %s: %w", dir, err)
	}
	return &FilesystemBackupStorage{Dir: dir}, nil
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
		out = append(out, hmapi.BackupEntry{
			ID:        strings.TrimSuffix(name, ".sbk"),
			Bytes:     info.Size(),
			CreatedAt: info.ModTime().UTC(),
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
	return f, nil
}

// Save implements [BackupStorage]. It writes data to `<id>.sbk` inside the
// storage directory, replacing any existing file with the same id.
func (s *FilesystemBackupStorage) Save(_ context.Context, id string, data []byte) error {
	path, err := s.pathForID(id)
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, data, 0o640); err != nil { // #nosec G306 — backup archives are operator-readable, not world
		return fmt.Errorf("backup: write %s: %w", path, err)
	}
	return nil
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
	return nil
}

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
// returns empty, Stream returns ErrUnimplemented). Returns the
// receiver for chaining.
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
	return a.restorer
}

// RestorerForCentral returns the restorer wired for centralName via
// [BackupAdapter.SetRestorerForCentral], or nil when none has been
// wired yet (the central has not finished bring-up, or is unknown).
func (a *BackupAdapter) RestorerForCentral(centralName string) BackupRestorer {
	if a == nil {
		return nil
	}
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
	if err := s.Save(ctx, id, data); err != nil {
		return hmapi.BackupEntry{}, err
	}
	return hmapi.BackupEntry{
		ID:        id,
		Bytes:     int64(len(data)),
		CreatedAt: now,
	}, nil
}
