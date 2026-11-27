// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

// Package datalock is the exclusive lock of a data directory: the daemon
// holds it while it runs, and the offline commands that rewrite the
// database (`secret rotate`, `matter reset`, ADR 0092) take it before they
// write, so neither runs under the other. The lock is the operating
// system's — flock on Unix, an unshared open on Windows — so it falls with
// the process that holds it, a crash included, and leaves nothing stale.
package datalock

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// FileName is the lock file under the data directory.
const FileName = "openccu-loom.lock"

// ErrHeld is returned by Acquire when another process holds the lock.
var ErrHeld = errors.New("datalock: the data directory is in use by another openccu-loom process")

// Lock is a held data-directory lock.
type Lock struct {
	release func() error
}

// Acquire takes the lock of dataDir without waiting, creating the directory
// and the lock file when missing. It returns ErrHeld when another process
// holds it.
func Acquire(dataDir string) (*Lock, error) {
	if err := os.MkdirAll(dataDir, 0o750); err != nil {
		return nil, fmt.Errorf("datalock: %w", err)
	}
	release, err := lockFile(filepath.Join(dataDir, FileName))
	if err != nil {
		return nil, err
	}
	return &Lock{release: release}, nil
}

// Release gives the lock up. A nil Lock or a second Release does nothing.
func (l *Lock) Release() error {
	if l == nil || l.release == nil {
		return nil
	}
	release := l.release
	l.release = nil
	return release()
}
