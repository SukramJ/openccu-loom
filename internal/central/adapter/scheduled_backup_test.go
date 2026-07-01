// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/pkg/hmapi"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// newRegistryWith builds a Registry that contains one Unit per name.
func newRegistryWith(t *testing.T, names ...string) *central.Registry {
	t.Helper()
	reg := central.NewRegistry()
	for _, name := range names {
		u, err := central.New(central.Config{Name: name})
		if err != nil {
			t.Fatalf("central.New(%q): %v", name, err)
		}
		if err := reg.Register(u); err != nil {
			t.Fatalf("reg.Register(%q): %v", name, err)
		}
	}
	return reg
}

// makeAlphaEntries returns n BackupEntry values whose IDs are valid for
// "alpha" (i.e. backupBelongsTo returns true) and whose CreatedAt times are
// spaced one second apart so newest-first sorting is deterministic.
// i=0 is the oldest, i=n-1 is the newest.
func makeAlphaEntries(n int) []hmapi.BackupEntry {
	base := time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC)
	entries := make([]hmapi.BackupEntry, n)
	for i := range n {
		// ID format: "<safe>-YYYYMMDD-HHMMSS" → len(suffix) == backupIDSuffixLen == 16.
		ts := base.Add(time.Duration(i) * time.Second)
		id := "alpha-" + ts.UTC().Format(backupTimestampLayout)
		entries[i] = hmapi.BackupEntry{
			ID:        id,
			CreatedAt: ts,
		}
	}
	return entries
}

// ---------------------------------------------------------------------------
// Prune — full matrix
// ---------------------------------------------------------------------------

// TestPruneDeletesOldestWhenOverLimit verifies that Prune(ctx, "alpha", 2)
// over five entries deletes exactly the three oldest.
func TestPruneDeletesOldestWhenOverLimit(t *testing.T) {
	t.Parallel()

	entries := makeAlphaEntries(5)
	// Copy the slice so the stub's Delete (which reuses the backing array via
	// s.entries[:0]) does not corrupt the local entries variable.
	stubEntries := make([]hmapi.BackupEntry, len(entries))
	copy(stubEntries, entries)
	stub := &stubBackupStorage{entries: stubEntries}

	reg := newRegistryWith(t, "alpha")
	a := NewBackupAdapter(reg).SetStorage(stub)

	if err := a.Prune(context.Background(), "alpha", 2); err != nil {
		t.Fatalf("Prune: %v", err)
	}

	// Three oldest (indices 0, 1, 2) must appear in deleted.
	if len(stub.deleted) != 3 {
		t.Fatalf("want 3 deleted, got %d: %v", len(stub.deleted), stub.deleted)
	}
	// The oldest three IDs — entries are sorted oldest-first in the slice.
	wantDeleted := map[string]bool{
		entries[0].ID: true,
		entries[1].ID: true,
		entries[2].ID: true,
	}
	for _, id := range stub.deleted {
		if !wantDeleted[id] {
			t.Errorf("unexpected deleted id %q", id)
		}
	}
	// The two newest must still be in storage.
	remaining := stub.entries
	if len(remaining) != 2 {
		t.Fatalf("want 2 remaining entries, got %d", len(remaining))
	}
	for _, e := range remaining {
		if e.ID != entries[3].ID && e.ID != entries[4].ID {
			t.Errorf("unexpected remaining entry %q", e.ID)
		}
	}
}

// TestPruneNoopWhenKeepLastGteLen checks that Prune does nothing when
// keepLast is ≥ the number of matching entries.
func TestPruneNoopWhenKeepLastGteLen(t *testing.T) {
	t.Parallel()

	entries := makeAlphaEntries(3)
	stubEntries := make([]hmapi.BackupEntry, len(entries))
	copy(stubEntries, entries)
	stub := &stubBackupStorage{entries: stubEntries}

	reg := newRegistryWith(t, "alpha")
	a := NewBackupAdapter(reg).SetStorage(stub)

	// keepLast == len(entries): nothing should be deleted.
	if err := a.Prune(context.Background(), "alpha", 3); err != nil {
		t.Fatalf("Prune (equal): %v", err)
	}
	if len(stub.deleted) != 0 {
		t.Errorf("want 0 deleted (keepLast==len), got %d", len(stub.deleted))
	}

	// keepLast > len(entries): also no-op.
	if err := a.Prune(context.Background(), "alpha", 10); err != nil {
		t.Fatalf("Prune (greater): %v", err)
	}
	if len(stub.deleted) != 0 {
		t.Errorf("want 0 deleted (keepLast>len), got %d", len(stub.deleted))
	}
}

// TestPruneNoopWhenKeepLastZero checks the keepLast<=0 early-exit.
func TestPruneNoopWhenKeepLastZero(t *testing.T) {
	t.Parallel()

	entries := makeAlphaEntries(5)
	stubEntries := make([]hmapi.BackupEntry, len(entries))
	copy(stubEntries, entries)
	stub := &stubBackupStorage{entries: stubEntries}

	reg := newRegistryWith(t, "alpha")
	a := NewBackupAdapter(reg).SetStorage(stub)

	if err := a.Prune(context.Background(), "alpha", 0); err != nil {
		t.Fatalf("Prune(0): %v", err)
	}
	if len(stub.deleted) != 0 {
		t.Errorf("want 0 deleted for keepLast=0, got %d", len(stub.deleted))
	}
}

// TestPruneIsolatesMultiCCU proves that pruning "alpha" never deletes any
// "beta-…" entry, and that a central named "beta-2" is also not confused
// with "beta".
func TestPruneIsolatesMultiCCU(t *testing.T) {
	t.Parallel()

	alphaEntries := makeAlphaEntries(3) // oldest=index 0, newest=index 2

	// Two beta entries (different central).
	betaBase := time.Date(2026, 7, 1, 11, 0, 0, 0, time.UTC)
	betaEntries := []hmapi.BackupEntry{
		{ID: "beta-" + betaBase.UTC().Format(backupTimestampLayout), CreatedAt: betaBase},
		{ID: "beta-" + betaBase.Add(time.Second).UTC().Format(backupTimestampLayout), CreatedAt: betaBase.Add(time.Second)},
	}

	// One "beta-2" entry — must not be confused with "beta".
	beta2Base := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	beta2Entry := hmapi.BackupEntry{
		ID:        "beta-2-" + beta2Base.UTC().Format(backupTimestampLayout),
		CreatedAt: beta2Base,
	}

	allEntries := append(append(alphaEntries, betaEntries...), beta2Entry)
	stub := &stubBackupStorage{entries: allEntries}

	reg := newRegistryWith(t, "alpha")
	a := NewBackupAdapter(reg).SetStorage(stub)

	// Prune alpha keeping only 1; should delete 2 oldest alpha entries.
	if err := a.Prune(context.Background(), "alpha", 1); err != nil {
		t.Fatalf("Prune: %v", err)
	}

	// Exactly 2 entries deleted (the 2 oldest alpha ones).
	if len(stub.deleted) != 2 {
		t.Fatalf("want 2 deleted, got %d: %v", len(stub.deleted), stub.deleted)
	}
	for _, id := range stub.deleted {
		if !strings.HasPrefix(id, "alpha-") {
			t.Errorf("deleted non-alpha id %q", id)
		}
		if strings.HasPrefix(id, "beta-") {
			t.Errorf("deleted beta id %q", id)
		}
	}
}

// TestPruneNilStorageIsNoop checks that Prune with nil storage returns nil.
func TestPruneNilStorageIsNoop(t *testing.T) {
	t.Parallel()

	reg := newRegistryWith(t, "alpha")
	a := NewBackupAdapter(reg) // storage is nil

	if err := a.Prune(context.Background(), "alpha", 1); err != nil {
		t.Fatalf("Prune with nil storage must be a no-op, got %v", err)
	}
}

// TestPruneStorageDeleteErrorPropagates verifies that a Delete failure is
// returned as a wrapped error.
func TestPruneStorageDeleteErrorPropagates(t *testing.T) {
	t.Parallel()

	entries := makeAlphaEntries(3)
	stubEntries := make([]hmapi.BackupEntry, len(entries))
	copy(stubEntries, entries)
	deleteErr := errors.New("disk full")
	stub := &stubBackupStorage{
		entries:   stubEntries,
		deleteErr: deleteErr,
	}

	reg := newRegistryWith(t, "alpha")
	a := NewBackupAdapter(reg).SetStorage(stub)

	err := a.Prune(context.Background(), "alpha", 1)
	if err == nil {
		t.Fatal("want error from Delete, got nil")
	}
	if !errors.Is(err, deleteErr) {
		t.Errorf("want wrapped deleteErr, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// TriggerBackupForCentral
// ---------------------------------------------------------------------------

// TestTriggerBackupForCentralUnknownCentralReturnsError checks that an
// unregistered central name yields a non-nil error and empty id.
func TestTriggerBackupForCentralUnknownCentralReturnsError(t *testing.T) {
	t.Parallel()

	reg := newRegistryWith(t, "alpha")
	a := NewBackupAdapter(reg)

	id, err := a.TriggerBackupForCentral(context.Background(), "no-such-central")
	if err == nil {
		t.Fatal("want error for unknown central, got nil")
	}
	if id != "" {
		t.Errorf("want empty id for error case, got %q", id)
	}
}

// TestTriggerBackupForCentralKnownCentralReturnsID verifies that a known
// central yields a non-empty id that belongs to the central's safe name.
// The detached goroutine is allowed to fail async (no live CCU client);
// the test only asserts on the synchronous return values.
func TestTriggerBackupForCentralKnownCentralReturnsID(t *testing.T) {
	t.Parallel()

	reg := newRegistryWith(t, "alpha")
	a := NewBackupAdapter(reg)

	id, err := a.TriggerBackupForCentral(context.Background(), "alpha")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id == "" {
		t.Fatal("want non-empty id, got empty string")
	}
	safe := backupSafeName("alpha")
	if !backupBelongsTo(id, safe) {
		t.Errorf("id %q does not belong to safe name %q", id, safe)
	}
	if !strings.HasPrefix(id, safe+"-") {
		t.Errorf("id %q does not start with %q", id, safe+"-")
	}
}

// TestTriggerBackupForCentralNilRegistryReturnsErrUnimplemented checks the
// nil-registry guard.
func TestTriggerBackupForCentralNilRegistryReturnsErrUnimplemented(t *testing.T) {
	t.Parallel()

	a := NewBackupAdapter(nil)

	id, err := a.TriggerBackupForCentral(context.Background(), "alpha")
	if !errors.Is(err, ErrUnimplemented) {
		t.Fatalf("want ErrUnimplemented, got %v", err)
	}
	if id != "" {
		t.Errorf("want empty id, got %q", id)
	}
}

// ---------------------------------------------------------------------------
// FilesystemBackupStorage.Delete
// ---------------------------------------------------------------------------

// TestFilesystemBackupStorageDeleteRemovesFile saves a file then deletes it;
// List must no longer include the entry. A second Delete of the same id must
// return nil (idempotent / missing-file is not an error).
func TestFilesystemBackupStorageDeleteRemovesFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	st, err := NewFilesystemBackupStorage(dir)
	if err != nil {
		t.Fatalf("new storage: %v", err)
	}

	ctx := context.Background()
	const id = "alpha-20260701-100000"

	if err := st.Save(ctx, id, []byte("payload")); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Confirm the file is there.
	list, err := st.List(ctx)
	if err != nil {
		t.Fatalf("List after Save: %v", err)
	}
	if len(list) != 1 || list[0].ID != id {
		t.Fatalf("expected 1 entry with id %q, got %v", id, list)
	}

	// Delete it.
	if err := st.Delete(ctx, id); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	// Must no longer appear.
	list, err = st.List(ctx)
	if err != nil {
		t.Fatalf("List after Delete: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("expected empty list after Delete, got %v", list)
	}

	// Second delete of the same id must be idempotent (no error).
	if err := st.Delete(ctx, id); err != nil {
		t.Fatalf("second Delete must be idempotent, got %v", err)
	}
}

// TestFilesystemBackupStorageDeleteRejectsPathTraversal checks that an id
// containing ".." is rejected rather than escaping the storage directory.
func TestFilesystemBackupStorageDeleteRejectsPathTraversal(t *testing.T) {
	t.Parallel()

	// Write a sentinel file one directory above the storage dir to make it
	// clear what a traversal would target.
	base := t.TempDir()
	storageDir := filepath.Join(base, "backups")
	sentinel := filepath.Join(base, "secret.sbk")
	if err := os.WriteFile(sentinel, []byte("secret"), 0o644); err != nil {
		t.Fatalf("write sentinel: %v", err)
	}

	st, err := NewFilesystemBackupStorage(storageDir)
	if err != nil {
		t.Fatalf("new storage: %v", err)
	}

	ctx := context.Background()
	if err := st.Delete(ctx, "../secret"); err == nil {
		t.Fatal("expected error for path-traversal id, got nil")
	}
}
