// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package ccudata

import (
	"errors"
	"io"
	"io/fs"
	"testing"
	"testing/fstest"
	"time"
)

// TestProfilesFSSatisfiesTheFSContract runs the standard-library conformance
// suite over the adapter. The profile stores read their archives through it,
// so anything fs.FS promises — Open, Stat, Read, ReadDir, path validation —
// has to hold, not just the two calls those stores happen to make today.
func TestProfilesFSSatisfiesTheFSContract(t *testing.T) {
	t.Parallel()
	fsys := ProfilesFS()
	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("no archives listed — the embedded snapshot is empty")
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	if err := fstest.TestFS(fsys, names...); err != nil {
		t.Errorf("fstest.TestFS: %v", err)
	}
}

func TestProfilesFSOpen(t *testing.T) {
	t.Parallel()
	fsys := ProfilesFS()

	t.Run("reads an archive", func(t *testing.T) {
		t.Parallel()
		f, err := fsys.Open("ACCESS_RECEIVER.json.gz")
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		defer func() {
			if err := f.Close(); err != nil {
				t.Errorf("Close: %v", err)
			}
		}()
		data, err := io.ReadAll(f)
		if err != nil {
			t.Fatalf("ReadAll: %v", err)
		}
		if len(data) == 0 {
			t.Error("archive is empty")
		}
		info, err := f.Stat()
		if err != nil {
			t.Fatalf("Stat: %v", err)
		}
		if info.Name() != "ACCESS_RECEIVER.json.gz" {
			t.Errorf("Name() = %q", info.Name())
		}
		if info.Size() != int64(len(data)) {
			t.Errorf("Size() = %d, want %d", info.Size(), len(data))
		}
		if info.IsDir() {
			t.Error("an archive must not report as a directory")
		}
		if info.Mode() != 0o444 {
			t.Errorf("Mode() = %v, want read-only", info.Mode())
		}
		if !info.ModTime().Equal(time.Time{}) {
			t.Errorf("ModTime() = %v, want the zero time for build-time data", info.ModTime())
		}
		if info.Sys() != nil {
			t.Errorf("Sys() = %v, want nil", info.Sys())
		}
	})

	// Every rejection must surface as fs.ErrNotExist or fs.ErrInvalid, because
	// callers distinguish "unknown receiver type" from a real failure by
	// errors.Is — see linkprofile's ErrUnsupported path.
	for _, tc := range []struct {
		name    string
		path    string
		wantErr error
	}{
		{"missing archive", "NO_SUCH_RECEIVER.json.gz", fs.ErrNotExist},
		{"escaping path", "../translation_extract.json.gz", fs.ErrInvalid},
		{"absolute path", "/ACCESS_RECEIVER.json.gz", fs.ErrInvalid},
		{"empty name", "", fs.ErrInvalid},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := fsys.Open(tc.path)
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("Open(%q) error = %v, want %v", tc.path, err, tc.wantErr)
			}
			var pathErr *fs.PathError
			if !errors.As(err, &pathErr) {
				t.Errorf("Open(%q) error should be a *fs.PathError, got %T", tc.path, err)
			}
		})
	}
}

// TestProfilesFSOpenRoot covers the directory handle fs.FS requires at the
// root. No store opens it, but generic helpers such as fs.WalkDir start
// there, so it has to behave.
func TestProfilesFSOpenRoot(t *testing.T) {
	t.Parallel()
	f, err := ProfilesFS().Open(".")
	if err != nil {
		t.Fatalf("Open(.): %v", err)
	}
	defer func() {
		if err := f.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	}()
	info, err := f.Stat()
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if !info.IsDir() {
		t.Error("the root must report as a directory")
	}
	if !info.Mode().IsDir() {
		t.Errorf("Mode() = %v, want a directory mode", info.Mode())
	}

	// Reading a directory is an error, as on a real filesystem.
	if _, err := f.Read(make([]byte, 1)); err == nil {
		t.Error("reading the root should fail")
	}

	dir, ok := f.(fs.ReadDirFile)
	if !ok {
		t.Fatalf("root handle %T does not implement fs.ReadDirFile", f)
	}
	// Paging: two entries, then the rest, then io.EOF.
	first, err := dir.ReadDir(2)
	if err != nil {
		t.Fatalf("ReadDir(2): %v", err)
	}
	if len(first) != 2 {
		t.Fatalf("ReadDir(2) returned %d entries", len(first))
	}
	rest, err := dir.ReadDir(-1)
	if err != nil {
		t.Fatalf("ReadDir(-1): %v", err)
	}
	if len(rest) == 0 {
		t.Error("ReadDir(-1) returned nothing after paging")
	}
	if _, err := dir.ReadDir(1); !errors.Is(err, io.EOF) {
		t.Errorf("ReadDir after exhaustion = %v, want io.EOF", err)
	}
}

// TestProfileDirEntryInfoOnMissingArchive covers the stat path for an entry
// whose archive has gone: DirEntry.Info() reads the bytes to report a size,
// so it needs the same not-exist answer Open gives.
func TestProfileDirEntryInfoOnMissingArchive(t *testing.T) {
	t.Parallel()
	_, err := profileDirEntry("NO_SUCH_RECEIVER.json.gz").Info()
	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Info() error = %v, want fs.ErrNotExist", err)
	}
}

// TestSnapshotVersionIsReported pins that the embedded snapshot advertises
// its version — operators read it from /api/v1/info to tell which metadata
// vintage a daemon carries.
func TestSnapshotVersionIsReported(t *testing.T) {
	t.Parallel()
	if v := SnapshotVersion(); v == "" {
		t.Error("SnapshotVersion() is empty")
	}
}

func TestProfilesFSReadDir(t *testing.T) {
	t.Parallel()
	fsys := ProfilesFS()

	t.Run("lists the flat catalogue", func(t *testing.T) {
		t.Parallel()
		entries, err := fs.ReadDir(fsys, ".")
		if err != nil {
			t.Fatalf("ReadDir: %v", err)
		}
		if len(entries) < 60 {
			t.Errorf("listed %d archives, expected the full catalogue", len(entries))
		}
		e := entries[0]
		if e.IsDir() {
			t.Error("the catalogue is flat — no entry may be a directory")
		}
		if e.Type() != 0 {
			t.Errorf("Type() = %v, want a regular file", e.Type())
		}
		info, err := e.Info()
		if err != nil {
			t.Fatalf("Info: %v", err)
		}
		if info.Name() != e.Name() {
			t.Errorf("Info().Name() = %q, want %q", info.Name(), e.Name())
		}
	})

	t.Run("a nested directory does not exist", func(t *testing.T) {
		t.Parallel()
		_, err := fs.ReadDir(fsys, "subdir")
		if !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("ReadDir(subdir) error = %v, want fs.ErrNotExist", err)
		}
	})
}
