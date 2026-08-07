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
	"github.com/SukramJ/openccu-loom/pkg/hmerr"
)

// ---------------------------------------------------------------------------
// Inline fakes
// ---------------------------------------------------------------------------

type stubBackupStorage struct {
	entries   []hmapi.BackupEntry
	content   map[string]string // id → raw content
	openErr   error
	saveErr   error
	deleteErr error
	saved     map[string][]byte // id → saved bytes; populated by Save
	deleted   []string          // ids passed to Delete, in call order
	mu        sync.Mutex
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

// Delete implements [BackupStorage]. Records the id in s.deleted, drops it
// from s.entries, and returns s.deleteErr (nil by default).
func (s *stubBackupStorage) Delete(_ context.Context, id string) error {
	if s.deleteErr != nil {
		return s.deleteErr
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.deleted = append(s.deleted, id)
	kept := s.entries[:0]
	for _, e := range s.entries {
		if e.ID != id {
			kept = append(kept, e)
		}
	}
	s.entries = kept
	return nil
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
// FilesystemBackupStorage — save atomicity / torso rejection
// ---------------------------------------------------------------------------

// TestFilesystemBackupStorageListSkipsTruncatedFile guards the visible half
// of an interrupted save: a zero-byte ".sbk" is the residue a killed or
// out-of-disk write leaves behind, never a backup. Listing it puts a torso in
// the UI that an operator can then upload to a CCU.
func TestFilesystemBackupStorageListSkipsTruncatedFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "alpha-20260701-100000.sbk"), nil, 0o640); err != nil {
		t.Fatalf("write torso: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "alpha-20260701-110000.sbk"), []byte("complete"), 0o640); err != nil {
		t.Fatalf("write complete: %v", err)
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
		t.Fatalf("want only the complete archive listed, got %d entries: %+v", len(list), list)
	}
	if list[0].ID != "alpha-20260701-110000" {
		t.Errorf("listed the wrong archive: %q", list[0].ID)
	}
}

// TestFilesystemBackupStorageOpenRefusesTruncatedFile covers the second half
// of the same defect: List hiding the torso is not enough, because Restore
// opens an id directly. An empty archive must never reach a CCU.
func TestFilesystemBackupStorageOpenRefusesTruncatedFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "torso.sbk"), nil, 0o640); err != nil {
		t.Fatalf("write torso: %v", err)
	}

	st, err := NewFilesystemBackupStorage(dir)
	if err != nil {
		t.Fatalf("new storage: %v", err)
	}

	rc, err := st.Open(context.Background(), "torso")
	if err == nil {
		_ = rc.Close()
		t.Fatal("expected Open to refuse an empty archive, got nil error")
	}
}

// TestFilesystemBackupStorageSaveLeavesNoVisibleFileWhenWriteFails simulates a
// failing save (the final publish cannot happen because the target name is
// occupied by a non-empty directory) and asserts the invariant the atomic
// write exists for: nothing new is visible, and no half-written scratch file
// is left behind either.
func TestFilesystemBackupStorageSaveLeavesNoVisibleFileWhenWriteFails(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	st, err := NewFilesystemBackupStorage(dir)
	if err != nil {
		t.Fatalf("new storage: %v", err)
	}

	const id = "alpha-20260701-100000"
	blocked := filepath.Join(dir, id+".sbk")
	if err := os.Mkdir(blocked, 0o750); err != nil {
		t.Fatalf("mkdir blocker: %v", err)
	}
	if err := os.WriteFile(filepath.Join(blocked, "keep"), []byte("x"), 0o640); err != nil {
		t.Fatalf("fill blocker: %v", err)
	}

	if err := st.Save(context.Background(), id, []byte("payload")); err == nil {
		t.Fatal("expected Save to fail when the archive cannot be published")
	}

	list, err := st.List(context.Background())
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("a failed save must leave no listable backup, got %+v", list)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	for _, e := range entries {
		if e.Name() != id+".sbk" {
			t.Errorf("failed save left %q behind in the storage directory", e.Name())
		}
	}
}

// TestFilesystemBackupStorageInterruptedSaveIsInvisible pins the shape of the
// scratch file a save in progress uses: a process killed mid-write leaves
// exactly that file, and it must be neither listable nor openable under the
// backup's id.
func TestFilesystemBackupStorageInterruptedSaveIsInvisible(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	st, err := NewFilesystemBackupStorage(dir)
	if err != nil {
		t.Fatalf("new storage: %v", err)
	}

	partial, err := os.CreateTemp(dir, backupTempPattern)
	if err != nil {
		t.Fatalf("create partial: %v", err)
	}
	if _, err := partial.WriteString("half a backup"); err != nil {
		t.Fatalf("write partial: %v", err)
	}
	if err := partial.Close(); err != nil {
		t.Fatalf("close partial: %v", err)
	}

	list, err := st.List(context.Background())
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("an interrupted save must not be listable, got %+v", list)
	}
	if rc, err := st.Open(context.Background(), "alpha-20260701-100000"); err == nil {
		_ = rc.Close()
		t.Fatal("an interrupted save must not be openable as a backup")
	}
}

// TestFilesystemBackupStorageSaveOverwritesCompletely verifies that replacing
// an existing backup lands the full new payload — the rename must publish the
// finished file, not merge with what was there.
func TestFilesystemBackupStorageSaveOverwritesCompletely(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	st, err := NewFilesystemBackupStorage(dir)
	if err != nil {
		t.Fatalf("new storage: %v", err)
	}

	ctx := context.Background()
	const id = "alpha-20260701-100000"
	if err := st.Save(ctx, id, []byte("a much longer first payload")); err != nil {
		t.Fatalf("first save: %v", err)
	}
	want := []byte("second")
	if err := st.Save(ctx, id, want); err != nil {
		t.Fatalf("second save: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(dir, id+".sbk"))
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("content after overwrite = %q, want %q", got, want)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("storage dir must hold only the archive, got %d entries", len(entries))
	}
}

// ---------------------------------------------------------------------------
// FilesystemBackupStorage.SaveUploaded
// ---------------------------------------------------------------------------

// TestFilesystemBackupStorageSaveUploadedIDCarriesUploadPrefix verifies the
// generated id starts with "upload-" — that prefix is how List/the
// scheduled-backup pruner tell an operator-imported archive apart from one
// this daemon pulled from a CCU itself.
func TestFilesystemBackupStorageSaveUploadedIDCarriesUploadPrefix(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	st, err := NewFilesystemBackupStorage(dir)
	if err != nil {
		t.Fatalf("new storage: %v", err)
	}

	entry, err := st.SaveUploaded(context.Background(), "whatever.sbk", []byte("payload"))
	if err != nil {
		t.Fatalf("SaveUploaded: %v", err)
	}
	if !strings.HasPrefix(entry.ID, uploadedBackupPrefix) {
		t.Errorf("ID = %q, want the %q prefix", entry.ID, uploadedBackupPrefix)
	}
}

// TestFilesystemBackupStorageSaveUploadedIgnoresSuppliedFilename verifies
// that a hostile filename (path traversal) cannot influence the generated
// id or where the archive is written — the id is derived from the wall
// clock, never from browser-supplied input, and the stored file stays
// inside the backup directory.
func TestFilesystemBackupStorageSaveUploadedIgnoresSuppliedFilename(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	st, err := NewFilesystemBackupStorage(dir)
	if err != nil {
		t.Fatalf("new storage: %v", err)
	}

	entry, err := st.SaveUploaded(context.Background(), "../../etc/passwd", []byte("payload"))
	if err != nil {
		t.Fatalf("SaveUploaded: %v", err)
	}
	if strings.ContainsAny(entry.ID, "/\\") {
		t.Fatalf("id must not carry path separators from the supplied filename, got %q", entry.ID)
	}

	path, err := st.pathForID(entry.ID)
	if err != nil {
		t.Fatalf("pathForID(%q): %v", entry.ID, err)
	}
	wantPrefix := filepath.Clean(dir) + string(filepath.Separator)
	if !strings.HasPrefix(filepath.Clean(path), wantPrefix) {
		t.Fatalf("stored path %q escaped the backup dir %q", path, dir)
	}
	if _, statErr := os.Stat(path); statErr != nil {
		t.Fatalf("expected the archive to be written at %q: %v", path, statErr)
	}
}

// TestFilesystemBackupStorageSaveUploadedReturnsMatchingByteCount verifies
// the returned entry's Bytes field matches the payload actually stored.
func TestFilesystemBackupStorageSaveUploadedReturnsMatchingByteCount(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	st, err := NewFilesystemBackupStorage(dir)
	if err != nil {
		t.Fatalf("new storage: %v", err)
	}

	payload := []byte("a payload of a known, specific length")
	entry, err := st.SaveUploaded(context.Background(), "x.sbk", payload)
	if err != nil {
		t.Fatalf("SaveUploaded: %v", err)
	}
	if entry.Bytes != int64(len(payload)) {
		t.Errorf("Bytes = %d, want %d", entry.Bytes, len(payload))
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

func TestBackupAdapterStreamWithoutStorageReportsUnsupported(t *testing.T) {
	t.Parallel()

	reg := newRegistryForBackupTest(t)
	a := NewBackupAdapter(reg)

	err := a.Stream(context.Background(), "any", &bytes.Buffer{})
	if !errors.Is(err, hmerr.ErrUnsupported) {
		t.Fatalf("want hmerr.ErrUnsupported, got %v", err)
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

// ---------------------------------------------------------------------------
// Multi-CCU restore-target resolution (ADR 0002)
// ---------------------------------------------------------------------------

// newMultiCentralRegistryForBackupTest registers one central per name and
// returns the shared registry, for tests that need more than one central
// to exercise owning-central resolution.
func newMultiCentralRegistryForBackupTest(t *testing.T, names ...string) *central.Registry {
	t.Helper()
	reg := central.NewRegistry()
	for _, n := range names {
		c, err := central.New(central.Config{Name: n})
		if err != nil {
			t.Fatalf("central.New(%s): %v", n, err)
		}
		if err := reg.Register(c); err != nil {
			t.Fatalf("register(%s): %v", n, err)
		}
	}
	return reg
}

// TestBackupAdapterRestoreTargetsOwningCentralNotAnyOther is the core
// multi-CCU regression guard for B2: with two registered centrals and a
// per-central restorer wired for each, restoring a backup owned by the
// second central must invoke only that central's restorer — never the
// first one's. Before this fix, [BackupAdapter] held a single global
// restorer so any backup id, regardless of the central that produced it,
// was uploaded to whichever CCU happened to be wired first.
func TestBackupAdapterRestoreTargetsOwningCentralNotAnyOther(t *testing.T) {
	t.Parallel()

	reg := newMultiCentralRegistryForBackupTest(t, "alpha", "beta")
	a := NewBackupAdapter(reg)

	idBeta := backupID("beta")
	a.SetStorage(&stubBackupStorage{content: map[string]string{idBeta: "beta payload"}})

	restorerAlpha := &stubBackupRestorer{jobID: "alpha-job"}
	restorerBeta := &stubBackupRestorer{jobID: "beta-job"}
	a.SetRestorerForCentral("alpha", restorerAlpha)
	a.SetRestorerForCentral("beta", restorerBeta)

	jobID, err := a.Restore(context.Background(), idBeta)
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	if jobID != "beta-job" {
		t.Errorf("jobID: want %q, got %q", "beta-job", jobID)
	}
	if restorerBeta.capturedID != idBeta {
		t.Errorf("beta restorer id: want %q, got %q", idBeta, restorerBeta.capturedID)
	}
	if string(restorerBeta.capturedPayload) != "beta payload" {
		t.Errorf("beta restorer payload: want %q, got %q", "beta payload", restorerBeta.capturedPayload)
	}
	if restorerAlpha.capturedID != "" {
		t.Errorf("alpha restorer must not be invoked for a beta-owned backup, got id %q", restorerAlpha.capturedID)
	}
}

// TestBackupAdapterRestoreUnknownOwnerNeverFallsBackToOtherCentral checks
// the failure mode adjacent to the happy path above: a backup id that
// resolves to a known central (beta) with NO restorer of its own must
// never fall back to a different central's restorer (alpha's), even when
// alpha's restorer is wired and a legacy default restorer is wired too.
func TestBackupAdapterRestoreUnknownOwnerNeverFallsBackToOtherCentral(t *testing.T) {
	t.Parallel()

	reg := newMultiCentralRegistryForBackupTest(t, "alpha", "beta")
	a := NewBackupAdapter(reg)

	idBeta := backupID("beta")
	a.SetStorage(&stubBackupStorage{content: map[string]string{idBeta: "beta payload"}})

	restorerAlpha := &stubBackupRestorer{jobID: "alpha-job"}
	a.SetRestorerForCentral("alpha", restorerAlpha)
	// A legacy default restorer is wired too — it must only ever be
	// consulted for an id whose owner cannot be resolved, not as a
	// substitute for a resolved-but-unready owner.
	a.SetRestorer(&stubBackupRestorer{jobID: "legacy-job"})

	_, err := a.Restore(context.Background(), idBeta)
	if !errors.Is(err, ErrRestoreUnsupported) {
		t.Fatalf("want ErrRestoreUnsupported, got %v", err)
	}
	if restorerAlpha.capturedID != "" {
		t.Errorf("alpha restorer must not be invoked for a beta-owned backup, got id %q", restorerAlpha.capturedID)
	}
}

// TestBackupAdapterRestoreForCentralWithNoOwnerFallsBackToLegacyRestorer
// preserves single-CCU / manually-placed-archive behaviour: an id that
// does not resolve to any registered central's name still restores via
// the legacy [BackupAdapter.SetRestorer] fallback.
func TestBackupAdapterRestoreForCentralWithNoOwnerFallsBackToLegacyRestorer(t *testing.T) {
	t.Parallel()

	reg := newMultiCentralRegistryForBackupTest(t, "alpha", "beta")
	a := NewBackupAdapter(reg)

	a.SetStorage(&stubBackupStorage{content: map[string]string{"manually-imported": "data"}})
	legacy := &stubBackupRestorer{jobID: "legacy-job"}
	a.SetRestorer(legacy)

	jobID, err := a.Restore(context.Background(), "manually-imported")
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	if jobID != "legacy-job" {
		t.Errorf("jobID: want %q, got %q", "legacy-job", jobID)
	}
	if legacy.capturedID != "manually-imported" {
		t.Errorf("legacy restorer id: want %q, got %q", "manually-imported", legacy.capturedID)
	}
}

// TestBackupAdapterRestorerForCentralGetter exercises the getter used by
// wiring code to check whether a given central already has a restorer
// installed (the ccu_wiring re-gate on reconnect).
func TestBackupAdapterRestorerForCentralGetter(t *testing.T) {
	t.Parallel()

	reg := newMultiCentralRegistryForBackupTest(t, "alpha")
	a := NewBackupAdapter(reg)
	if a.RestorerForCentral("alpha") != nil {
		t.Fatal("fresh adapter must report no restorer for alpha")
	}
	rest := &stubBackupRestorer{}
	a.SetRestorerForCentral("alpha", rest)
	if a.RestorerForCentral("alpha") != rest {
		t.Fatal("RestorerForCentral did not return the wired instance")
	}
}

// TestBackupAdapterListPopulatesCentralFromID verifies that List
// backfills BackupEntry.Central from the id when the storage backend
// (e.g. [FilesystemBackupStorage], which has no registry access) leaves
// it empty — the SPA's backup table and restore-target picker depend on
// this field to show which CCU owns each backup.
func TestBackupAdapterListPopulatesCentralFromID(t *testing.T) {
	t.Parallel()

	reg := newMultiCentralRegistryForBackupTest(t, "alpha", "beta")
	a := NewBackupAdapter(reg)

	idBeta := backupID("beta")
	a.SetStorage(&stubBackupStorage{entries: []hmapi.BackupEntry{
		{ID: idBeta, Bytes: 42},
		{ID: "unresolvable-id", Bytes: 7},
	}})

	list, err := a.List(context.Background())
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("want 2 entries, got %d", len(list))
	}
	if list[0].Central != "beta" {
		t.Errorf("entry 0 central: want %q, got %q", "beta", list[0].Central)
	}
	if list[1].Central != "" {
		t.Errorf("entry 1 central: want empty (unresolvable id), got %q", list[1].Central)
	}
}
