// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package ccudata

import (
	"bytes"
	"io"
	"io/fs"
	"path"
	"time"

	openccudata "github.com/SukramJ/go-openccu-data"
)

// ProfilesFS exposes the embedded profile archives as an [fs.FS] rooted at
// the snapshot's profiles/ directory, so a store can read them the same way
// it read a local embed.
//
// It exists because the archives used to be duplicated: each profile store
// carried its own copy of the same 65 files, which then aged independently
// of the module. Reading through one filesystem keeps a single copy, and a
// data refresh reaches every consumer at once.
//
// The returned value satisfies [fs.ReadDirFS], which is what
// [fs.ReadDir] needs to enumerate the catalogue without a directory walk.
func ProfilesFS() fs.FS { return profilesFS{} }

// profilesFS adapts the module's ReadFile/ReadDir pair to fs.FS. The module
// deliberately exposes no filesystem of its own so its path layout stays an
// implementation detail; this adapter pins that layout in exactly one place.
type profilesFS struct{}

const profilesDir = "profiles"

// Open implements [fs.FS]. The root opens as a directory handle, because
// fs.FS requires it — generic helpers such as [fs.WalkDir] start there —
// even though the stores reading this filesystem only ever open archives.
func (f profilesFS) Open(name string) (fs.File, error) {
	if !fs.ValidPath(name) {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrInvalid}
	}
	if name == "." {
		entries, err := f.ReadDir(".")
		if err != nil {
			return nil, err
		}
		return &profileRoot{entries: entries}, nil
	}
	data, err := openccudata.ReadFile(path.Join(profilesDir, name))
	if err != nil {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
	}
	return &profileFile{name: path.Base(name), Reader: bytes.NewReader(data)}, nil
}

// profileRoot is the directory handle for the flat profiles/ root. It
// implements [fs.ReadDirFile] so a caller that walks the filesystem behaves
// the same as one that calls [fs.ReadDir] directly.
type profileRoot struct {
	entries []fs.DirEntry
	offset  int
}

func (r *profileRoot) Stat() (fs.FileInfo, error) { return profileInfo{name: ".", dir: true}, nil }
func (r *profileRoot) Close() error               { return nil }

// Read reports the same error a real filesystem does for a directory.
func (r *profileRoot) Read([]byte) (int, error) {
	return 0, &fs.PathError{Op: "read", Path: ".", Err: fs.ErrInvalid}
}

// ReadDir implements [fs.ReadDirFile]: n <= 0 returns everything at once,
// a positive n pages through and reports io.EOF once drained.
func (r *profileRoot) ReadDir(n int) ([]fs.DirEntry, error) {
	if n <= 0 {
		rest := r.entries[r.offset:]
		r.offset = len(r.entries)
		return rest, nil
	}
	if r.offset >= len(r.entries) {
		return nil, io.EOF
	}
	end := min(r.offset+n, len(r.entries))
	page := r.entries[r.offset:end]
	r.offset = end
	return page, nil
}

// ReadDir implements [fs.ReadDirFS] for the flat profiles/ directory. Any
// name other than the root resolves to nothing, mirroring the snapshot's
// actual shape rather than inventing nesting.
func (profilesFS) ReadDir(name string) ([]fs.DirEntry, error) {
	if name != "." && name != "" {
		return nil, &fs.PathError{Op: "readdir", Path: name, Err: fs.ErrNotExist}
	}
	names, err := openccudata.ReadDir(profilesDir)
	if err != nil {
		return nil, &fs.PathError{Op: "readdir", Path: name, Err: err}
	}
	out := make([]fs.DirEntry, 0, len(names))
	for _, n := range names {
		out = append(out, profileDirEntry(n))
	}
	return out, nil
}

// profileFile is a read-only fs.File over an in-memory archive.
type profileFile struct {
	name string
	*bytes.Reader
}

// Stat reports the archive's full size. It reads Size(), not Len(): the
// latter counts the bytes still unread, so a caller that stats after
// reading would be told the file is empty.
func (f *profileFile) Stat() (fs.FileInfo, error) {
	return profileInfo{name: f.name, size: f.Size()}, nil
}
func (f *profileFile) Close() error { return nil }

// Compile-time proof that the adapter serves the interfaces its consumers
// assert on: a plain FS for Open, ReadDirFS for fs.ReadDir.
var (
	_ fs.FS          = profilesFS{}
	_ fs.ReadDirFS   = profilesFS{}
	_ fs.File        = (*profileFile)(nil)
	_ io.Reader      = (*profileFile)(nil)
	_ fs.ReadDirFile = (*profileRoot)(nil)
)

type profileDirEntry string

func (e profileDirEntry) Name() string      { return string(e) }
func (e profileDirEntry) IsDir() bool       { return false }
func (e profileDirEntry) Type() fs.FileMode { return 0 }

// Info reports the entry's size, which means reading the archive. fs.FS
// requires DirEntry.Info() and File.Stat() to agree, and the size is only
// knowable from the bytes; listing stays cheap because ReadDir does not
// call this.
func (e profileDirEntry) Info() (fs.FileInfo, error) {
	data, err := openccudata.ReadFile(path.Join(profilesDir, string(e)))
	if err != nil {
		return nil, &fs.PathError{Op: "stat", Path: string(e), Err: fs.ErrNotExist}
	}
	return profileInfo{name: string(e), size: int64(len(data))}, nil
}

// profileInfo reports the little that is knowable about an embedded
// artifact. Size is filled where the caller already holds the bytes; the
// zero modification time is honest about data baked in at build time.
type profileInfo struct {
	name string
	size int64
	dir  bool
}

func (i profileInfo) Name() string { return i.name }
func (i profileInfo) Size() int64  { return i.size }
func (i profileInfo) Mode() fs.FileMode {
	if i.dir {
		return fs.ModeDir | 0o555
	}
	return 0o444
}
func (i profileInfo) ModTime() time.Time { return time.Time{} }
func (i profileInfo) IsDir() bool        { return i.dir }
func (i profileInfo) Sys() any           { return nil }
