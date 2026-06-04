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

	"github.com/SukramJ/openccu-loom/internal/north/rest/handlers"
)

// BackupStorage abstracts where the daemon keeps locally-stored backup files.
// The default implementation reads `.sbk` files from a directory; other
// backends (S3, NFS, encrypted volume) can satisfy the same surface.
type BackupStorage interface {
	// List enumerates the backups currently held in storage.
	List(ctx context.Context) ([]handlers.BackupEntry, error)
	// Open returns a reader for the backup payload. The caller closes
	// the reader.
	Open(ctx context.Context, id string) (io.ReadCloser, error)
	// Save persists a freshly downloaded backup payload under id. The
	// id must be a bare token (no path separators or dot segments); the
	// implementation appends its own extension. Overwrites an existing
	// backup with the same id.
	Save(ctx context.Context, id string, data []byte) error
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
func (s *FilesystemBackupStorage) List(_ context.Context) ([]handlers.BackupEntry, error) {
	entries, err := os.ReadDir(s.Dir)
	if err != nil {
		return nil, fmt.Errorf("backup: read dir: %w", err)
	}
	out := make([]handlers.BackupEntry, 0, len(entries))
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
		out = append(out, handlers.BackupEntry{
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

// SetRestorer swaps the restore client. Default is no restorer; calls
// to [BackupAdapter.Restore] then return [ErrRestoreUnsupported].
// Returns the receiver for chaining.
func (a *BackupAdapter) SetRestorer(r BackupRestorer) *BackupAdapter {
	a.restorer = r
	return a
}

// Restorer returns the currently wired restorer, or nil. The wiring
// path consults this to decide whether to install one.
func (a *BackupAdapter) Restorer() BackupRestorer {
	if a == nil {
		return nil
	}
	return a.restorer
}
