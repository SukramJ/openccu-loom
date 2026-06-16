// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/pkg/hmapi"
)

// ---------------------------------------------------------------------------
// Inline fakes
// ---------------------------------------------------------------------------

type stubBackupStorage struct {
	entries []hmapi.BackupEntry
	content map[string]string // id → raw content
	openErr error
	saveErr error
	saved   map[string][]byte // id → saved bytes; populated by Save
	mu      sync.Mutex
}

func (s *stubBackupStorage) List(_ context.Context) ([]hmapi.BackupEntry, error) {
	return s.entries, nil
}

func (s *stubBackupStorage) Open(_ context.Context, id string) (io.ReadCloser, error) {
	if s.openErr != nil {
		return nil, s.openErr
	}
	body, ok := s.content[id]
	if !ok {
		return nil, errors.New("stub: not found")
	}
	return io.NopCloser(strings.NewReader(body)), nil
}

// Save implements [BackupStorage]. Records the payload in s.saved and returns
// s.saveErr (nil by default).
func (s *stubBackupStorage) Save(_ context.Context, id string, data []byte) error {
	if s.saveErr != nil {
		return s.saveErr
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.saved == nil {
		s.saved = make(map[string][]byte)
	}
	s.saved[id] = data
	return nil
}

// lookup returns the saved payload for id, safe for concurrent use.
func (s *stubBackupStorage) lookup(id string) ([]byte, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, ok := s.saved[id]
	return b, ok
}

type stubBackupRestorer struct {
	capturedID      string
	capturedPayload []byte
	jobID           string
	err             error
}

func (r *stubBackupRestorer) Restore(_ context.Context, id string, payload io.Reader) (string, error) {
	r.capturedID = id
	b, _ := io.ReadAll(payload)
	r.capturedPayload = b
	if r.err != nil {
		return "", r.err
	}
	return r.jobID, nil
}

// ---------------------------------------------------------------------------
// FilesystemBackupStorage tests
// ---------------------------------------------------------------------------

func TestFilesystemBackupStorageEmptyDirReturnsEmptyList(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	st, err := NewFilesystemBackupStorage(dir)
	if err != nil {
		t.Fatalf("new storage: %v", err)
	}

	list, err := st.List(context.Background())
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("expected empty list, got %d entries", len(list))
	}
}

func TestFilesystemBackupStorageListsSbkFiles(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	content := []byte("backup payload here")
	if err := os.WriteFile(filepath.Join(dir, "mybackup.sbk"), content, 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	st, err := NewFilesystemBackupStorage(dir)
	if err != nil {
		t.Fatalf("new storage: %v", err)
	}

	list, err := st.List(context.Background())
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(list))
	}
	if list[0].ID != "mybackup" {
		t.Errorf("ID: want %q, got %q", "mybackup", list[0].ID)
	}
	if list[0].Bytes != int64(len(content)) {
		t.Errorf("Bytes: want %d, got %d", len(content), list[0].Bytes)
	}
}

func TestFilesystemBackupStorageIgnoresNonSbk(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("ignore me"), 0o644); err != nil {
		t.Fatalf("write txt: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "real.sbk"), []byte("real"), 0o644); err != nil {
		t.Fatalf("write sbk: %v", err)
	}

	st, err := NewFilesystemBackupStorage(dir)
	if err != nil {
		t.Fatalf("new storage: %v", err)
	}

	list, err := st.List(context.Background())
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("expected exactly 1 entry, got %d", len(list))
	}
	if list[0].ID != "real" {
		t.Errorf("wrong ID: %q", list[0].ID)
	}
}

func TestFilesystemBackupStorageIgnoresDirectories(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	// A directory whose name ends in ".sbk" must not be listed.
	if err := os.Mkdir(filepath.Join(dir, "subdir.sbk"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	st, err := NewFilesystemBackupStorage(dir)
	if err != nil {
		t.Fatalf("new storage: %v", err)
	}

	list, err := st.List(context.Background())
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("expected 0 entries (directories skipped), got %d", len(list))
	}
}

func TestFilesystemBackupStorageOpenRejectsTraversal(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	st, err := NewFilesystemBackupStorage(dir)
	if err != nil {
		t.Fatalf("new storage: %v", err)
	}

	_, err = st.Open(context.Background(), "../etc/passwd")
	if err == nil {
		t.Fatal("expected error for path-traversal id, got nil")
	}
}

func TestFilesystemBackupStorageOpenStreamsContent(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	want := []byte("backup bytes 1234")
	if err := os.WriteFile(filepath.Join(dir, "snap1.sbk"), want, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	st, err := NewFilesystemBackupStorage(dir)
	if err != nil {
		t.Fatalf("new storage: %v", err)
	}

	rc, err := st.Open(context.Background(), "snap1")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer rc.Close()

	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("content mismatch: want %q, got %q", want, got)
	}
}

func TestFilesystemBackupStorageOpenMissingFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	st, err := NewFilesystemBackupStorage(dir)
	if err != nil {
		t.Fatalf("new storage: %v", err)
	}

	_, err = st.Open(context.Background(), "missing")
	if err == nil {
		t.Fatal("expected error for missing file, got nil")
	}
}

func TestNewFilesystemBackupStorageRejectsEmptyDir(t *testing.T) {
	t.Parallel()

	_, err := NewFilesystemBackupStorage("")
	if err == nil {
		t.Fatal("expected error for empty dir, got nil")
	}
}

func TestNewFilesystemBackupStorageCreatesDir(t *testing.T) {
	t.Parallel()

	base := t.TempDir()
	newDir := filepath.Join(base, "created", "nested")

	st, err := NewFilesystemBackupStorage(newDir)
	if err != nil {
		t.Fatalf("expected dir creation, got %v", err)
	}

	if _, statErr := os.Stat(newDir); os.IsNotExist(statErr) {
		t.Fatal("directory was not created")
	}

	// Storage is usable: listing the fresh dir must return empty, not error.
	list, err := st.List(context.Background())
	if err != nil {
		t.Fatalf("list on newly created dir: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("expected empty list, got %d", len(list))
	}
}

// ---------------------------------------------------------------------------
// BackupAdapter integration tests
// ---------------------------------------------------------------------------

func newRegistryForBackupTest(t *testing.T) *central.Registry {
	t.Helper()
	c, err := central.New(central.Config{Name: "c1"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	reg := central.NewRegistry()
	if err := reg.Register(c); err != nil {
		t.Fatalf("register: %v", err)
	}
	return reg
}

func TestBackupAdapterListEmptyWithoutStorage(t *testing.T) {
	t.Parallel()

	reg := newRegistryForBackupTest(t)
	a := NewBackupAdapter(reg)

	list, err := a.List(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if list != nil {
		t.Fatalf("expected nil list, got %v", list)
	}
}

func TestBackupAdapterListDelegatesToStorage(t *testing.T) {
	t.Parallel()

	reg := newRegistryForBackupTest(t)
	a := NewBackupAdapter(reg)

	want := []hmapi.BackupEntry{
		{ID: "bk1", Bytes: 100},
		{ID: "bk2", Bytes: 200},
	}
	a.SetStorage(&stubBackupStorage{entries: want})

	list, err := a.List(context.Background())
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(list))
	}
	if list[0].ID != "bk1" || list[1].ID != "bk2" {
		t.Errorf("entries mismatch: %+v", list)
	}
}

func TestBackupAdapterStreamWithoutStorageReturnsErrUnimplemented(t *testing.T) {
	t.Parallel()

	reg := newRegistryForBackupTest(t)
	a := NewBackupAdapter(reg)

	err := a.Stream(context.Background(), "any", &bytes.Buffer{})
	if !errors.Is(err, ErrUnimplemented) {
		t.Fatalf("want ErrUnimplemented, got %v", err)
	}
}

func TestBackupAdapterStreamCopiesPayload(t *testing.T) {
	t.Parallel()

	reg := newRegistryForBackupTest(t)
	a := NewBackupAdapter(reg)
	a.SetStorage(&stubBackupStorage{
		content: map[string]string{"snap": "payload"},
	})

	var buf bytes.Buffer
	if err := a.Stream(context.Background(), "snap", &buf); err != nil {
		t.Fatalf("stream: %v", err)
	}
	if buf.String() != "payload" {
		t.Errorf("body: want %q, got %q", "payload", buf.String())
	}
}

func TestBackupAdapterRestoreWithoutStorageOrRestorerReturnsErrRestoreUnsupported(t *testing.T) {
	t.Parallel()

	reg := newRegistryForBackupTest(t)

	// Both nil.
	aBoth := NewBackupAdapter(reg)
	_, err := aBoth.Restore(context.Background(), "bk1")
	if !errors.Is(err, ErrRestoreUnsupported) {
		t.Fatalf("both nil: want ErrRestoreUnsupported, got %v", err)
	}

	// Storage set, restorer nil.
	aNoRestorer := NewBackupAdapter(reg)
	aNoRestorer.SetStorage(&stubBackupStorage{
		content: map[string]string{"bk1": "data"},
	})
	_, err = aNoRestorer.Restore(context.Background(), "bk1")
	if !errors.Is(err, ErrRestoreUnsupported) {
		t.Fatalf("no restorer: want ErrRestoreUnsupported, got %v", err)
	}

	// Restorer set, storage nil.
	aNoStorage := NewBackupAdapter(reg)
	aNoStorage.SetRestorer(&stubBackupRestorer{jobID: "j1"})
	_, err = aNoStorage.Restore(context.Background(), "bk1")
	if !errors.Is(err, ErrRestoreUnsupported) {
		t.Fatalf("no storage: want ErrRestoreUnsupported, got %v", err)
	}
}

func TestBackupAdapterRestoreCallsRestorer(t *testing.T) {
	t.Parallel()

	reg := newRegistryForBackupTest(t)
	a := NewBackupAdapter(reg)

	const payload = "raw backup content"
	st := &stubBackupStorage{content: map[string]string{"bk1": payload}}
	re := &stubBackupRestorer{jobID: "job-42"}
	a.SetStorage(st)
	a.SetRestorer(re)

	jobID, err := a.Restore(context.Background(), "bk1")
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	if jobID != "job-42" {
		t.Errorf("jobID: want %q, got %q", "job-42", jobID)
	}
	if re.capturedID != "bk1" {
		t.Errorf("captured id: want %q, got %q", "bk1", re.capturedID)
	}
	if string(re.capturedPayload) != payload {
		t.Errorf("captured payload: want %q, got %q", payload, re.capturedPayload)
	}
}

func TestBackupAdapterRestorePropagatesStorageError(t *testing.T) {
	t.Parallel()

	reg := newRegistryForBackupTest(t)
	a := NewBackupAdapter(reg)

	storageErr := errors.New("disk full")
	re := &stubBackupRestorer{jobID: "j"}
	a.SetStorage(&stubBackupStorage{openErr: storageErr})
	a.SetRestorer(re)

	_, err := a.Restore(context.Background(), "bk1")
	if err == nil {
		t.Fatal("expected error from storage Open, got nil")
	}
	// Restorer must not have been called.
	if re.capturedID != "" {
		t.Errorf("restorer must not be called when storage Open fails")
	}
}

func TestBackupAdapterRestorePropagatesRestorerError(t *testing.T) {
	t.Parallel()

	reg := newRegistryForBackupTest(t)
	a := NewBackupAdapter(reg)

	restorerErr := errors.New("CCU rejected upload")
	a.SetStorage(&stubBackupStorage{content: map[string]string{"bk1": "data"}})
	a.SetRestorer(&stubBackupRestorer{err: restorerErr})

	_, err := a.Restore(context.Background(), "bk1")
	if !errors.Is(err, restorerErr) {
		t.Fatalf("want restorer error, got %v", err)
	}
}

func TestBackupAdapterRestorerGetterReportsWired(t *testing.T) {
	t.Parallel()

	reg := newRegistryForBackupTest(t)
	a := NewBackupAdapter(reg)
	if a.Restorer() != nil {
		t.Fatal("fresh adapter must report no restorer")
	}
	rest := &stubBackupRestorer{}
	a.SetRestorer(rest)
	if a.Restorer() != rest {
		t.Fatalf("Restorer() did not return the wired instance")
	}
}

func TestBackupAdapterRestorerGetterNilSafe(t *testing.T) {
	t.Parallel()
	var a *BackupAdapter
	if a.Restorer() != nil {
		t.Fatal("nil receiver must return nil")
	}
}
