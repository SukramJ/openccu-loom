// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/backup/sbk"
	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/pkg/hmapi"
	"github.com/SukramJ/openccu-loom/pkg/hmerr"
)

// validBackupArchive builds the tar a CCU system backup actually is, so a
// restore test exercises the same bytes the adapter's pre-upload
// inspection accepts. Restore refuses anything else, which is the point —
// a test that hands it "payload" would be testing a path production must
// never take.
func validBackupArchive(t *testing.T) string {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	members := []struct{ name, body string }{
		{"usr_local.tar.gz", "config-archive-bytes"},
		{"signature", "sig-bytes"},
		{"firmware_version", "VERSION=3.89.8.20260719\nPRODUCT=HM-CCU3\n"},
		{"key_index", "1"},
	}
	for _, m := range members {
		if err := tw.WriteHeader(&tar.Header{Name: m.name, Mode: 0o644, Size: int64(len(m.body))}); err != nil {
			t.Fatalf("write header %s: %v", m.name, err)
		}
		if _, err := tw.Write([]byte(m.body)); err != nil {
			t.Fatalf("write body %s: %v", m.name, err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("close tar writer: %v", err)
	}
	return buf.String()
}

// ---------------------------------------------------------------------------
// Inline fakes
// ---------------------------------------------------------------------------

type stubBackupStorage struct {
	entries    []hmapi.BackupEntry
	content    map[string]string // id → raw content
	openErr    error
	saveErr    error
	deleteErr  error
	saved      map[string][]byte // id → saved bytes; populated by Save
	savedNames map[string]string // id → display name passed to Save
	deleted    []string          // ids passed to Delete, in call order
	mu         sync.Mutex
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

// Save implements [BackupStorage]. Records the payload in s.saved and the
// display name in s.savedNames, and returns s.saveErr (nil by default).
func (s *stubBackupStorage) Save(_ context.Context, id, filename string, data []byte) error {
	if s.saveErr != nil {
		return s.saveErr
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.saved == nil {
		s.saved = make(map[string][]byte)
	}
	if s.savedNames == nil {
		s.savedNames = make(map[string]string)
	}
	s.saved[id] = data
	s.savedNames[id] = filename
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
// FilesystemBackupStorage — .nobackup exclusion marker
// ---------------------------------------------------------------------------

// TestNewFilesystemBackupStorageWritesNoBackupTagForFreshDir verifies that
// constructing storage over a directory NewFilesystemBackupStorage itself
// creates also drops the CCU's --exclude-tag=.nobackup marker into it.
func TestNewFilesystemBackupStorageWritesNoBackupTagForFreshDir(t *testing.T) {
	t.Parallel()

	base := t.TempDir()
	dir := filepath.Join(base, "fresh")

	if _, err := NewFilesystemBackupStorage(dir); err != nil {
		t.Fatalf("new storage: %v", err)
	}

	if _, err := os.Stat(filepath.Join(dir, NoBackupTagName)); err != nil {
		t.Fatalf("expected %s in a freshly created directory: %v", NoBackupTagName, err)
	}
}

// TestNewFilesystemBackupStorageWritesNoBackupTagForExistingDir verifies the
// marker is also dropped into a directory that already existed before
// construction — the tag must not depend on this daemon having created the
// directory itself (e.g. an operator-provided storage path reused across
// restarts).
func TestNewFilesystemBackupStorageWritesNoBackupTagForExistingDir(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	if _, err := NewFilesystemBackupStorage(dir); err != nil {
		t.Fatalf("new storage: %v", err)
	}

	if _, err := os.Stat(filepath.Join(dir, NoBackupTagName)); err != nil {
		t.Fatalf("expected %s in a pre-existing directory: %v", NoBackupTagName, err)
	}
}

// TestNewFilesystemBackupStorageDoesNotTruncateExistingNoBackupTag verifies
// that a marker already carrying content survives construction untouched —
// the helper returns early once the file is present rather than reopening
// it and truncating it.
func TestNewFilesystemBackupStorageDoesNotTruncateExistingNoBackupTag(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	want := []byte("pre-existing marker content")
	if err := os.WriteFile(filepath.Join(dir, NoBackupTagName), want, 0o640); err != nil {
		t.Fatalf("seed marker: %v", err)
	}

	if _, err := NewFilesystemBackupStorage(dir); err != nil {
		t.Fatalf("new storage: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(dir, NoBackupTagName))
	if err != nil {
		t.Fatalf("read marker: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("marker content = %q, want untouched %q", got, want)
	}
}

// TestFilesystemBackupStorageListNeverReportsNoBackupTag verifies the
// exclusion marker never surfaces as a backup entry, and that it survives a
// full Save/Delete cycle of an actual archive alongside it — Save's
// scratch-file dance and Delete's removal must never touch anything but the
// archive's own path.
func TestFilesystemBackupStorageListNeverReportsNoBackupTag(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	st, err := NewFilesystemBackupStorage(dir)
	if err != nil {
		t.Fatalf("new storage: %v", err)
	}
	ctx := context.Background()

	list, err := st.List(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("expected empty list before any Save, got %+v", list)
	}

	const id = "alpha-20260701-100000"
	if err := st.Save(ctx, id, "", []byte("payload")); err != nil {
		t.Fatalf("save: %v", err)
	}

	list, err = st.List(ctx)
	if err != nil {
		t.Fatalf("list after save: %v", err)
	}
	if len(list) != 1 || list[0].ID != id {
		t.Fatalf("list after save = %+v, want exactly [%s]", list, id)
	}
	if _, err := os.Stat(filepath.Join(dir, NoBackupTagName)); err != nil {
		t.Fatalf("marker did not survive Save: %v", err)
	}

	if err := st.Delete(ctx, id); err != nil {
		t.Fatalf("delete: %v", err)
	}

	list, err = st.List(ctx)
	if err != nil {
		t.Fatalf("list after delete: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("expected empty list after delete (marker must not be reported), got %+v", list)
	}
	if _, err := os.Stat(filepath.Join(dir, NoBackupTagName)); err != nil {
		t.Fatalf("marker did not survive Delete: %v", err)
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

	if err := st.Save(context.Background(), id, "", []byte("payload")); err == nil {
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
		if e.Name() != id+".sbk" && e.Name() != NoBackupTagName {
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
	if err := st.Save(ctx, id, "", []byte("a much longer first payload")); err != nil {
		t.Fatalf("first save: %v", err)
	}
	want := []byte("second")
	if err := st.Save(ctx, id, "", want); err != nil {
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
	// The archive plus the exclusion marker every storage directory carries.
	if len(entries) != 2 {
		t.Fatalf("storage dir must hold only the archive and %s, got %d entries", NoBackupTagName, len(entries))
	}
}

// ---------------------------------------------------------------------------
// FilesystemBackupStorage — display filename sidecar
// ---------------------------------------------------------------------------

// TestFilesystemBackupStorageSaveListDeleteRoundTripsTheDisplayName verifies
// the whole sidecar lifecycle: Save records the CCU-convention display name,
// List reports it back on the matching entry, and Delete removes the
// sidecar along with the archive so a later id reuse cannot inherit a
// deleted archive's name.
func TestFilesystemBackupStorageSaveListDeleteRoundTripsTheDisplayName(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	st, err := NewFilesystemBackupStorage(dir)
	if err != nil {
		t.Fatalf("new storage: %v", err)
	}
	ctx := context.Background()
	const id = "alpha-20260817-140500"
	const name = "ccu-01.local-3.87.6.20260404-2026-08-17-1405.sbk"

	if err := st.Save(ctx, id, name, []byte("payload")); err != nil {
		t.Fatalf("save: %v", err)
	}

	sidecar := filepath.Join(dir, id+backupNameSuffix)
	if _, statErr := os.Stat(sidecar); statErr != nil {
		t.Fatalf("expected sidecar %q to exist after save: %v", sidecar, statErr)
	}

	list, err := st.List(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 1 || list[0].Filename != name {
		t.Fatalf("list = %+v, want a single entry with Filename %q", list, name)
	}

	if err := st.Delete(ctx, id); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, statErr := os.Stat(sidecar); !os.IsNotExist(statErr) {
		t.Fatalf("expected sidecar %q to be removed by Delete, stat err = %v", sidecar, statErr)
	}
}

// TestFilesystemBackupStorageSaveWithEmptyNameRemovesAStaleSidecar verifies
// that re-saving under the same id with an empty display name (e.g. the CCU
// had reported its system information for the first save but not for a
// later re-save under a reused id) drops the previous sidecar rather than
// leaving a name on disk that no longer describes the current archive.
func TestFilesystemBackupStorageSaveWithEmptyNameRemovesAStaleSidecar(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	st, err := NewFilesystemBackupStorage(dir)
	if err != nil {
		t.Fatalf("new storage: %v", err)
	}
	ctx := context.Background()
	const id = "alpha-20260817-140500"

	if err := st.Save(ctx, id, "ccu-01.local-3.87.6.20260404-2026-08-17-1405.sbk", []byte("first")); err != nil {
		t.Fatalf("first save: %v", err)
	}
	if err := st.Save(ctx, id, "", []byte("second")); err != nil {
		t.Fatalf("second save: %v", err)
	}

	sidecar := filepath.Join(dir, id+backupNameSuffix)
	if _, statErr := os.Stat(sidecar); !os.IsNotExist(statErr) {
		t.Fatalf("expected the stale sidecar %q to be gone, stat err = %v", sidecar, statErr)
	}
	list, err := st.List(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 1 || list[0].Filename != "" {
		t.Fatalf("list = %+v, want a single entry with an empty Filename", list)
	}
}

// TestFilesystemBackupStorageListDoesNotReportTheNameSidecarAsABackup
// verifies the sidecar file itself never surfaces as an entry in List — it
// does not end in .sbk, and List's own directory scan only picks up that
// suffix.
func TestFilesystemBackupStorageListDoesNotReportTheNameSidecarAsABackup(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	st, err := NewFilesystemBackupStorage(dir)
	if err != nil {
		t.Fatalf("new storage: %v", err)
	}
	ctx := context.Background()
	const id = "alpha-20260817-140500"

	if err := st.Save(ctx, id, "ccu-01.local-3.87.6.20260404-2026-08-17-1405.sbk", []byte("payload")); err != nil {
		t.Fatalf("save: %v", err)
	}

	list, err := st.List(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("expected exactly 1 entry (the archive, not its name sidecar), got %d: %+v", len(list), list)
	}
	if list[0].ID != id {
		t.Fatalf("listed entry ID = %q, want %q", list[0].ID, id)
	}
}

// ---------------------------------------------------------------------------
// FilesystemBackupStorage.readName — sanitising what comes back off disk
// ---------------------------------------------------------------------------

// writeRawSidecar plants a sidecar file directly, bypassing writeName, so a
// test can exercise readName against content Save itself would never
// produce (a hand-edited or hostile sidecar).
func writeRawSidecar(t *testing.T, dir, id, raw string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, id+backupNameSuffix), []byte(raw), 0o640); err != nil {
		t.Fatalf("write raw sidecar: %v", err)
	}
}

// TestFilesystemBackupStorageReadNameRejectsAPathSeparator verifies a
// sidecar carrying a path separator degrades to an empty Filename rather
// than being served — the value ends up as an HTTP Content-Disposition
// filename, where a separator is a path-traversal-flavoured payload for a
// client that trusts it verbatim.
func TestFilesystemBackupStorageReadNameRejectsAPathSeparator(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	st, err := NewFilesystemBackupStorage(dir)
	if err != nil {
		t.Fatalf("new storage: %v", err)
	}
	ctx := context.Background()
	const id = "alpha-20260817-140500"
	if err := os.WriteFile(filepath.Join(dir, id+".sbk"), []byte("payload"), 0o640); err != nil {
		t.Fatalf("write archive: %v", err)
	}
	writeRawSidecar(t, dir, id, "../../etc/passwd")

	list, err := st.List(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 1 || list[0].Filename != "" {
		t.Fatalf("list = %+v, want the tainted name dropped to empty", list)
	}
}

// TestFilesystemBackupStorageReadNameRejectsCRLF verifies a sidecar carrying
// a CRLF sequence — the header-injection case — degrades to an empty
// Filename instead of being written into the Content-Disposition header
// verbatim.
func TestFilesystemBackupStorageReadNameRejectsCRLF(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	st, err := NewFilesystemBackupStorage(dir)
	if err != nil {
		t.Fatalf("new storage: %v", err)
	}
	ctx := context.Background()
	const id = "alpha-20260817-140500"
	if err := os.WriteFile(filepath.Join(dir, id+".sbk"), []byte("payload"), 0o640); err != nil {
		t.Fatalf("write archive: %v", err)
	}
	writeRawSidecar(t, dir, id, "evil.sbk\r\nX-Injected: 1")

	list, err := st.List(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 1 || list[0].Filename != "" {
		t.Fatalf("list = %+v, want the CRLF-carrying name dropped to empty", list)
	}
}

// TestFilesystemBackupStorageReadNameRejectsAnOverlongName verifies a
// sidecar longer than 255 bytes degrades to an empty Filename.
func TestFilesystemBackupStorageReadNameRejectsAnOverlongName(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	st, err := NewFilesystemBackupStorage(dir)
	if err != nil {
		t.Fatalf("new storage: %v", err)
	}
	ctx := context.Background()
	const id = "alpha-20260817-140500"
	if err := os.WriteFile(filepath.Join(dir, id+".sbk"), []byte("payload"), 0o640); err != nil {
		t.Fatalf("write archive: %v", err)
	}
	writeRawSidecar(t, dir, id, strings.Repeat("a", 256)+".sbk")

	list, err := st.List(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 1 || list[0].Filename != "" {
		t.Fatalf("list = %+v, want the overlong name dropped to empty", list)
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

// TestFilesystemBackupStorageSaveUploadedLeavesFilenameEmpty verifies an
// imported archive lists with an empty Filename: the browser-supplied name
// is untrusted and there is no CCU behind the archive to derive a
// CCU-convention name from, so it renders as `<id>.sbk` rather than
// something that looks like a fact about the archive.
func TestFilesystemBackupStorageSaveUploadedLeavesFilenameEmpty(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	st, err := NewFilesystemBackupStorage(dir)
	if err != nil {
		t.Fatalf("new storage: %v", err)
	}
	ctx := context.Background()

	entry, err := st.SaveUploaded(ctx, "operator-supplied-name.sbk", []byte("payload"))
	if err != nil {
		t.Fatalf("SaveUploaded: %v", err)
	}

	list, err := st.List(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	var found *hmapi.BackupEntry
	for i := range list {
		if list[i].ID == entry.ID {
			found = &list[i]
		}
	}
	if found == nil {
		t.Fatalf("uploaded entry %q not found in list %+v", entry.ID, list)
	}
	if found.Filename != "" {
		t.Errorf("Filename = %q, want empty for an uploaded archive", found.Filename)
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

	payload := validBackupArchive(t)
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
	a.SetStorage(&stubBackupStorage{content: map[string]string{"bk1": validBackupArchive(t)}})
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

	idBeta := formatBackupID("beta", time.Now())
	betaPayload := validBackupArchive(t)
	a.SetStorage(&stubBackupStorage{content: map[string]string{idBeta: betaPayload}})

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
	if string(restorerBeta.capturedPayload) != betaPayload {
		t.Errorf("beta restorer payload: want the stored archive, got %d bytes", len(restorerBeta.capturedPayload))
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

	idBeta := formatBackupID("beta", time.Now())
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

	a.SetStorage(&stubBackupStorage{content: map[string]string{"manually-imported": validBackupArchive(t)}})
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

	idBeta := formatBackupID("beta", time.Now())
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

// ---------------------------------------------------------------------------
// BackupAdapter.Restore — inspecting the archive before it reaches the CCU
// ---------------------------------------------------------------------------

// archiveMissingMember builds the same tar [validBackupArchive] does, minus
// the member named omit, so a case can pin exactly which missing member
// sbk.Inspect refuses.
func archiveMissingMember(t *testing.T, omit string) string {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	members := []struct{ name, body string }{
		{"usr_local.tar.gz", "config-archive-bytes"},
		{"signature", "sig-bytes"},
		{"firmware_version", "VERSION=3.89.8.20260719\nPRODUCT=HM-CCU3\n"},
		{"key_index", "1"},
	}
	for _, m := range members {
		if m.name == omit {
			continue
		}
		if err := tw.WriteHeader(&tar.Header{Name: m.name, Mode: 0o644, Size: int64(len(m.body))}); err != nil {
			t.Fatalf("write header %s: %v", m.name, err)
		}
		if _, err := tw.Write([]byte(m.body)); err != nil {
			t.Fatalf("write body %s: %v", m.name, err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("close tar writer: %v", err)
	}
	return buf.String()
}

// TestBackupAdapterRestoreRefusesAnArchiveThatIsNotACCUBackup is the
// regression guard for the pre-upload inspection [BackupAdapter.Restore]
// now runs via [BackupAdapter.inspectStored]. Uploading a CCU backup is not
// reversible — the CCU unpacks the archive and reboots into the restored
// state — so a corrupt, incomplete, or truncated stored archive must never
// reach the restorer. Before this fix, Restore opened the stored archive
// once and handed it straight to the restorer: structural validation only
// ran on the operator-facing upload endpoint, so an archive damaged on
// disk after being accepted, or a manually placed non-backup file, went
// to the CCU untouched.
func TestBackupAdapterRestoreRefusesAnArchiveThatIsNotACCUBackup(t *testing.T) {
	t.Parallel()

	validArchive := validBackupArchive(t)

	cases := []struct {
		name    string
		content string
		wantErr error
	}{
		{
			name:    "random bytes are not a tar",
			content: "this is definitely not a tar archive, just plain bytes",
			wantErr: sbk.ErrNotAnArchive,
		},
		{
			name:    "valid tar missing the configuration archive",
			content: archiveMissingMember(t, "usr_local.tar.gz"),
			wantErr: sbk.ErrIncomplete,
		},
		{
			name:    "valid tar missing the signature",
			content: archiveMissingMember(t, "signature"),
			wantErr: sbk.ErrIncomplete,
		},
		{
			// A truncated tar can fail either as an unreadable header or as
			// a missing member depending on exactly where the cut lands;
			// only "Restore must refuse it" is guaranteed, not which of
			// the two sbk sentinels it wraps.
			name:    "truncated archive",
			content: validArchive[:len(validArchive)/2],
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			reg := newRegistryForBackupTest(t)
			a := NewBackupAdapter(reg)
			a.SetStorage(&stubBackupStorage{content: map[string]string{"bk1": tc.content}})
			restorer := &stubBackupRestorer{jobID: "job-should-never-run"}
			a.SetRestorer(restorer)

			_, err := a.Restore(context.Background(), "bk1")
			if err == nil {
				t.Fatal("expected Restore to refuse the archive, got nil error")
			}
			if tc.wantErr != nil && !errors.Is(err, tc.wantErr) {
				t.Errorf("error = %v, want wrapping %v", err, tc.wantErr)
			}
			if restorer.capturedID != "" {
				t.Errorf("restorer must not be invoked when inspection refuses the archive, got capturedID %q", restorer.capturedID)
			}
		})
	}
}

// TestBackupAdapterRestoreAcceptsAValidArchive guards the other side of the
// same change: [BackupAdapter.inspectStored] reads the stored archive to
// verify it, and Restore must still hand the restorer a fresh, complete
// reader afterwards — not the one inspection already drained to EOF. A
// valid archive has to arrive at the restorer byte-for-byte intact.
func TestBackupAdapterRestoreAcceptsAValidArchive(t *testing.T) {
	t.Parallel()

	reg := newRegistryForBackupTest(t)
	a := NewBackupAdapter(reg)
	payload := validBackupArchive(t)
	a.SetStorage(&stubBackupStorage{content: map[string]string{"bk1": payload}})
	restorer := &stubBackupRestorer{jobID: "job-99"}
	a.SetRestorer(restorer)

	jobID, err := a.Restore(context.Background(), "bk1")
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	if jobID != "job-99" {
		t.Errorf("jobID: want %q, got %q", "job-99", jobID)
	}
	if restorer.capturedID != "bk1" {
		t.Errorf("captured id: want %q, got %q", "bk1", restorer.capturedID)
	}
	if string(restorer.capturedPayload) != payload {
		t.Errorf("captured payload: want the full stored archive (%d bytes), got %d bytes",
			len(payload), len(restorer.capturedPayload))
	}
}

// ---------------------------------------------------------------------------
// BackupAdapter.mintBackupID — ids stay unique and sortable per central
// ---------------------------------------------------------------------------

// TestMintBackupIDNeverRepeatsForTheSameCentral is the regression guard for
// the truncated-to-second collision: minting several ids for the same
// central back-to-back used to render identically whenever the wall clock
// had not ticked over to the next second between calls, and
// [BackupStorage.Save] overwrites an existing id — a second backup
// silently replaced the first instead of ever being created.
// [BackupAdapter.mintBackupID] advances the mint time by one second when
// the clock has not moved on, so ids stay both distinct and strictly
// increasing (which matters because [backupBelongsTo] and any list sort
// depend on the fixed-width timestamp suffix and on ids sorting in
// creation order).
func TestMintBackupIDNeverRepeatsForTheSameCentral(t *testing.T) {
	t.Parallel()

	a := NewBackupAdapter(nil)
	safe := backupSafeName("ccu1")

	const n = 5
	ids := make([]string, n)
	for i := range ids {
		ids[i] = a.mintBackupID("ccu1")
	}

	seen := make(map[string]bool, n)
	for i, id := range ids {
		if seen[id] {
			t.Fatalf("id %d (%q) repeats an earlier mint: %v", i, id, ids)
		}
		seen[id] = true
		if !backupBelongsTo(id, safe) {
			t.Errorf("id %d (%q) does not resolve back to central %q", i, id, "ccu1")
		}
		if len(id) != len(ids[0]) {
			t.Errorf("id %d (%q) has length %d, want %d (backupBelongsTo assumes a fixed-width suffix)",
				i, id, len(id), len(ids[0]))
		}
	}
	for i := 1; i < n; i++ {
		if ids[i-1] >= ids[i] {
			t.Errorf("ids must sort in mint order: ids[%d]=%q is not < ids[%d]=%q", i-1, ids[i-1], i, ids[i])
		}
	}
}

// TestMintBackupIDIsPerCentral verifies the per-central id sequence stays
// independent: minting for two different centrals never lets one resolve
// to the other's owner, and a central named "ccu" is not confused with a
// differently-named one that merely shares a prefix — [backupBelongsTo]
// strips a fixed-width suffix and compares the remainder exactly, not as a
// prefix match.
func TestMintBackupIDIsPerCentral(t *testing.T) {
	t.Parallel()

	a := NewBackupAdapter(nil)

	idAlpha := a.mintBackupID("alpha")
	idBeta := a.mintBackupID("beta")

	if !backupBelongsTo(idAlpha, backupSafeName("alpha")) {
		t.Errorf("idAlpha %q does not resolve to alpha", idAlpha)
	}
	if backupBelongsTo(idAlpha, backupSafeName("beta")) {
		t.Errorf("idAlpha %q must not resolve to beta", idAlpha)
	}
	if !backupBelongsTo(idBeta, backupSafeName("beta")) {
		t.Errorf("idBeta %q does not resolve to beta", idBeta)
	}
	if backupBelongsTo(idBeta, backupSafeName("alpha")) {
		t.Errorf("idBeta %q must not resolve to alpha", idBeta)
	}

	idCcu := a.mintBackupID("ccu")
	idCcu01 := a.mintBackupID("ccu-01")
	if backupBelongsTo(idCcu, backupSafeName("ccu-01")) {
		t.Errorf("a backup minted for %q must not resolve to %q, got id %q", "ccu", "ccu-01", idCcu)
	}
	if backupBelongsTo(idCcu01, backupSafeName("ccu")) {
		t.Errorf("a backup minted for %q must not resolve to %q, got id %q", "ccu-01", "ccu", idCcu01)
	}
	if !backupBelongsTo(idCcu, backupSafeName("ccu")) {
		t.Errorf("idCcu %q does not resolve to its own central %q", idCcu, "ccu")
	}
	if !backupBelongsTo(idCcu01, backupSafeName("ccu-01")) {
		t.Errorf("idCcu01 %q does not resolve to its own central %q", idCcu01, "ccu-01")
	}
}

// TestMintBackupIDConcurrentMintsForTheSameCentralAreAllDistinct exercises
// the mutex [BackupAdapter.mintBackupID] holds: 20 goroutines minting for
// the same central concurrently must still hand out 20 distinct ids.
// Without mintMu serializing the read-modify-write of lastMinted, two
// goroutines racing the same truncated-to-second clock value could both
// mint the id for that second — and, run under -race, an unguarded map
// read/write from concurrent goroutines is its own separate failure.
func TestMintBackupIDConcurrentMintsForTheSameCentralAreAllDistinct(t *testing.T) {
	t.Parallel()

	a := NewBackupAdapter(nil)

	const n = 20
	var (
		wg  sync.WaitGroup
		mu  sync.Mutex
		ids = make(map[string]bool, n)
	)
	wg.Add(n)
	for range n {
		go func() {
			defer wg.Done()
			id := a.mintBackupID("concurrent")
			mu.Lock()
			ids[id] = true
			mu.Unlock()
		}()
	}
	wg.Wait()

	if len(ids) != n {
		t.Fatalf("expected %d distinct ids from %d concurrent mints, got %d: %v", n, n, len(ids), ids)
	}
}

// concurrentRestorer is a stateless [BackupRestorer] for the concurrency
// test: stubBackupRestorer records its arguments and would itself race,
// masking the adapter-side race under test.
type concurrentRestorer struct{ jobID string }

func (r concurrentRestorer) Restore(_ context.Context, _ string, _ io.Reader) (string, error) {
	return r.jobID, nil
}

// TestBackupAdapterRestorerWiringIsConcurrencySafe reproduces the multi-CCU
// bring-up race: every configured central wires its own restorer from its own
// gated bring-up goroutine, and it re-wires on every re-gate after a CCU
// reboot, while the REST restore handler reads the same map. Two CCUs coming
// back together after a power outage clear their readiness gate inside the
// same poll window, so the two writes land concurrently and the runtime aborts
// the daemon with "concurrent map writes" — during bring-up, when an operator
// can least diagnose it. A restore issued while a second central re-gates hits
// the read side of the same map.
func TestBackupAdapterRestorerWiringIsConcurrencySafe(t *testing.T) {
	t.Parallel()

	names := []string{"alpha", "beta", "gamma", "delta"}
	reg := newMultiCentralRegistryForBackupTest(t, names...)
	a := NewBackupAdapter(reg)

	idAlpha := formatBackupID("alpha", time.Now())
	a.SetStorage(&stubBackupStorage{content: map[string]string{idAlpha: validBackupArchive(t)}})

	var wg sync.WaitGroup
	start := make(chan struct{})
	for _, name := range names {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for range 50 {
				a.SetRestorerForCentral(name, concurrentRestorer{jobID: name + "-job"})
			}
		}()
	}
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for range 50 {
				_, _ = a.Restore(context.Background(), idAlpha)
				_ = a.RestorerForCentral("beta")
			}
		}()
	}
	close(start)
	wg.Wait()

	if a.RestorerForCentral("gamma") == nil {
		t.Fatal("every central's restorer must survive the concurrent wiring")
	}
}

// TestBackupAdapterStorageInfoReportsTheDirectoryInUse pins the fact the
// route exists for: the directory reported is the one the storage actually
// writes to, not the configured value. On a CCU add-on install those are
// routinely different strings — `backup.dir` is empty in the config while
// the service script points the storage at the CCU's own backup target —
// so an operator reading the config still cannot tell where an archive
// went.
func TestBackupAdapterStorageInfoReportsTheDirectoryInUse(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	storage, err := NewFilesystemBackupStorage(dir)
	if err != nil {
		t.Fatalf("new storage: %v", err)
	}
	if err := storage.Save(t.Context(), "ccu-20260818-140257", "", []byte("payload")); err != nil {
		t.Fatalf("save: %v", err)
	}
	a := NewBackupAdapter(central.NewRegistry()).SetStorage(storage)

	info, err := a.StorageInfo(t.Context())
	if err != nil {
		t.Fatalf("storage info: %v", err)
	}
	if info.Dir != dir {
		t.Fatalf("dir = %q, want %q", info.Dir, dir)
	}
	if !info.Available {
		t.Fatal("available = false, want true for a wired filesystem storage")
	}
	if info.Count != 1 || info.Bytes != int64(len("payload")) {
		t.Fatalf("count/bytes = %d/%d, want 1/%d", info.Count, info.Bytes, len("payload"))
	}
}

// TestBackupAdapterStorageInfoWithoutStorageReportsUnavailable is the
// negative control for the test above: with no storage wired the same call
// must report available=false and no directory, so "storage is missing"
// cannot be mistaken for "storage is empty".
func TestBackupAdapterStorageInfoWithoutStorageReportsUnavailable(t *testing.T) {
	t.Parallel()
	a := NewBackupAdapter(central.NewRegistry())

	info, err := a.StorageInfo(t.Context())
	if err != nil {
		t.Fatalf("storage info: %v", err)
	}
	if info.Available || info.Dir != "" || info.Count != 0 {
		t.Fatalf("expected unavailable storage, got %+v", info)
	}
}

// TestBackupAdapterStorageInfoWithoutLocatorReportsNoDirectory covers a
// storage backend that cannot name a location. It must come back empty
// rather than inventing one — a wrong path sends an operator looking in
// the wrong place, which is worse than saying nothing.
func TestBackupAdapterStorageInfoWithoutLocatorReportsNoDirectory(t *testing.T) {
	t.Parallel()
	a := NewBackupAdapter(central.NewRegistry()).SetStorage(&locationlessStorage{})

	info, err := a.StorageInfo(t.Context())
	if err != nil {
		t.Fatalf("storage info: %v", err)
	}
	if !info.Available {
		t.Fatal("available = false, want true — a storage is wired, it just has no location")
	}
	if info.Dir != "" {
		t.Fatalf("dir = %q, want empty", info.Dir)
	}
	if info.Count != 1 || info.Bytes != 7 {
		t.Fatalf("count/bytes = %d/%d, want 1/7", info.Count, info.Bytes)
	}
}

// locationlessStorage is a BackupStorage that does not implement
// BackupStorageLocator.
type locationlessStorage struct{}

func (*locationlessStorage) List(context.Context) ([]hmapi.BackupEntry, error) {
	return []hmapi.BackupEntry{{ID: "x", Bytes: 7}}, nil
}

func (*locationlessStorage) Open(context.Context, string) (io.ReadCloser, error) {
	return nil, errors.New("not implemented")
}
func (*locationlessStorage) Save(context.Context, string, string, []byte) error { return nil }
func (*locationlessStorage) Delete(context.Context, string) error               { return nil }

// TestBackupAdapterDeleteRemovesTheArchiveAndItsName pins the whole delete
// path down to the filesystem: the archive is gone from the listing, its
// bytes are gone from disk, and the sidecar carrying its display name goes
// with it — a name left behind would be inherited by the next id that
// collides with it.
func TestBackupAdapterDeleteRemovesTheArchiveAndItsName(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	storage, err := NewFilesystemBackupStorage(dir)
	if err != nil {
		t.Fatalf("new storage: %v", err)
	}
	const id = "ccu-20260818-140257"
	if err := storage.Save(t.Context(), id, "ccu-3.89.8-2026-08-18-1402.sbk", []byte("payload")); err != nil {
		t.Fatalf("save: %v", err)
	}
	a := NewBackupAdapter(central.NewRegistry()).SetStorage(storage)

	if err := a.Delete(t.Context(), id); err != nil {
		t.Fatalf("delete: %v", err)
	}
	entries, err := a.List(t.Context())
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("entries after delete = %+v, want none", entries)
	}
	if _, err := os.Stat(filepath.Join(dir, id+".sbk")); !os.IsNotExist(err) {
		t.Fatalf("archive still on disk: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, id+backupNameSuffix)); !os.IsNotExist(err) {
		t.Fatalf("name sidecar still on disk: %v", err)
	}
}

// TestBackupAdapterDeleteIsIdempotent covers the second click and the retry
// after a lost response: the caller asked for the archive to be gone, and
// it is. An error here would read as "the storage is broken".
func TestBackupAdapterDeleteIsIdempotent(t *testing.T) {
	t.Parallel()
	storage, err := NewFilesystemBackupStorage(t.TempDir())
	if err != nil {
		t.Fatalf("new storage: %v", err)
	}
	a := NewBackupAdapter(central.NewRegistry()).SetStorage(storage)

	if err := a.Delete(t.Context(), "never-existed"); err != nil {
		t.Fatalf("delete of a missing archive: %v", err)
	}
}

// TestBackupAdapterDeleteWithoutStorageIsUnsupported is the negative
// control: without storage the call must report ErrUnsupported so the
// handler can answer 503 (a deployment state) instead of 502 (an upstream
// fault the CCU never caused).
func TestBackupAdapterDeleteWithoutStorageIsUnsupported(t *testing.T) {
	t.Parallel()
	a := NewBackupAdapter(central.NewRegistry())

	err := a.Delete(t.Context(), "b1")
	if !errors.Is(err, hmerr.ErrUnsupported) {
		t.Fatalf("err = %v, want ErrUnsupported", err)
	}
}

// TestBackupAdapterDeleteLeavesOtherArchivesAlone is the bite the id-based
// tests cannot give on their own: a delete that removed the directory, or
// resolved the wrong path, would pass every assertion about the archive
// that was asked for.
func TestBackupAdapterDeleteLeavesOtherArchivesAlone(t *testing.T) {
	t.Parallel()
	storage, err := NewFilesystemBackupStorage(t.TempDir())
	if err != nil {
		t.Fatalf("new storage: %v", err)
	}
	for _, id := range []string{"ccu-20260818-140257", "ccu-20260819-140257"} {
		if err := storage.Save(t.Context(), id, "", []byte("payload")); err != nil {
			t.Fatalf("save %s: %v", id, err)
		}
	}
	a := NewBackupAdapter(central.NewRegistry()).SetStorage(storage)

	if err := a.Delete(t.Context(), "ccu-20260818-140257"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	entries, err := a.List(t.Context())
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(entries) != 1 || entries[0].ID != "ccu-20260819-140257" {
		t.Fatalf("entries = %+v, want only ccu-20260819-140257", entries)
	}
}

// TestNamePathForIDRejectsEveryIDTheArchivePathRejects pins the sidecar path
// to the archive path's containment rule.
//
// The two used to be independent expressions — one validated, one rebuilt
// from the raw id — which is exactly the shape that goes wrong quietly: the
// rule can be tightened in one and not the other, and nothing fails until a
// crafted id reaches the delete route the REST surface now exposes.
func TestNamePathForIDRejectsEveryIDTheArchivePathRejects(t *testing.T) {
	t.Parallel()
	s, err := NewFilesystemBackupStorage(t.TempDir())
	if err != nil {
		t.Fatalf("new storage: %v", err)
	}
	for _, id := range []string{
		"", ".", "..", "../escape", "..\\escape", "sub/dir", "a/../../etc/passwd",
	} {
		if _, err := s.pathForID(id); err == nil {
			t.Fatalf("pathForID(%q) accepted an id it must reject; the case no longer tests anything", id)
		}
		if _, err := s.namePathForID(id); err == nil {
			t.Fatalf("namePathForID(%q) accepted an id the archive path rejects", id)
		}
	}
}

// TestNamePathForIDSitsBesideItsArchive is the positive half: an accepted id
// resolves to a sidecar in the same directory as its archive, differing only
// in the suffix.
func TestNamePathForIDSitsBesideItsArchive(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	s, err := NewFilesystemBackupStorage(dir)
	if err != nil {
		t.Fatalf("new storage: %v", err)
	}
	const id = "ccu-20260818-140257"
	archive, err := s.pathForID(id)
	if err != nil {
		t.Fatalf("pathForID: %v", err)
	}
	name, err := s.namePathForID(id)
	if err != nil {
		t.Fatalf("namePathForID: %v", err)
	}
	if filepath.Dir(name) != filepath.Dir(archive) {
		t.Fatalf("sidecar %q is not in the archive's directory %q", name, filepath.Dir(archive))
	}
	if want := strings.TrimSuffix(archive, ".sbk") + backupNameSuffix; name != want {
		t.Fatalf("sidecar = %q, want %q", name, want)
	}
}
