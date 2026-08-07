// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
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

// TestTriggerBackupForCentralNilRegistryReportsNoCentral checks the
// nil-registry guard.
func TestTriggerBackupForCentralNilRegistryReportsNoCentral(t *testing.T) {
	t.Parallel()

	a := NewBackupAdapter(nil)

	id, err := a.TriggerBackupForCentral(context.Background(), "alpha")
	if !errors.Is(err, ErrNoCentralRegistered) {
		t.Fatalf("want ErrNoCentralRegistered, got %v", err)
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

// ---------------------------------------------------------------------------
// CreateBackupForCentral — synchronous create + rotation ordering
// ---------------------------------------------------------------------------

// wireCreateBackup gives the named central a create-and-download function that
// returns fixed bytes, so CreateBackupForCentral can complete synchronously.
func wireCreateBackup(t *testing.T, reg *central.Registry, name string, fn func(context.Context) ([]byte, error)) {
	t.Helper()
	u, ok := reg.Get(name)
	if !ok || u == nil {
		t.Fatalf("central %q not registered", name)
	}
	u.SetCreateBackupFn(fn)
}

// TestCreateBackupForCentralIsSynchronous verifies the new synchronous variant
// saves the backup before it returns (unlike the detached TriggerBackup).
func TestCreateBackupForCentralIsSynchronous(t *testing.T) {
	t.Parallel()

	reg := newRegistryWith(t, "alpha")
	wireCreateBackup(t, reg, "alpha", func(context.Context) ([]byte, error) {
		return []byte("payload"), nil
	})
	dir := t.TempDir()
	storage, err := NewFilesystemBackupStorage(dir)
	if err != nil {
		t.Fatalf("storage: %v", err)
	}
	a := NewBackupAdapter(reg).SetStorage(storage)

	id, err := a.CreateBackupForCentral(context.Background(), "alpha")
	if err != nil {
		t.Fatalf("CreateBackupForCentral: %v", err)
	}
	// The file exists immediately on return — no polling / sleep needed.
	if _, err := os.Stat(filepath.Join(dir, id+".sbk")); err != nil {
		t.Fatalf("backup not durably saved on return: %v", err)
	}
}

// TestCreateBackupForCentralUnknownCentral checks the unknown-central guard.
func TestCreateBackupForCentralUnknownCentral(t *testing.T) {
	t.Parallel()
	reg := newRegistryWith(t, "alpha")
	a := NewBackupAdapter(reg)
	if _, err := a.CreateBackupForCentral(context.Background(), "nope"); err == nil {
		t.Fatal("want error for unknown central, got nil")
	}
}

// TestScheduledCreateThenPruneKeepsExactlyKeepLast reproduces the rotation
// race: because the create is now awaited before Prune runs, the steady state
// is exactly KeepLast — not KeepLast+1 as the old detached trigger left it.
func TestScheduledCreateThenPruneKeepsExactlyKeepLast(t *testing.T) {
	t.Parallel()

	const keepLast = 3
	ctx := context.Background()

	reg := newRegistryWith(t, "alpha")
	wireCreateBackup(t, reg, "alpha", func(context.Context) ([]byte, error) {
		return []byte("payload"), nil
	})
	dir := t.TempDir()
	storage, err := NewFilesystemBackupStorage(dir)
	if err != nil {
		t.Fatalf("storage: %v", err)
	}
	a := NewBackupAdapter(reg).SetStorage(storage)

	// Seed keepLast existing alpha backups with distinct, older mtimes so the
	// freshly-created one is unambiguously the newest.
	base := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	seeded := make([]string, keepLast)
	for i := range keepLast {
		id := "alpha-" + base.Add(time.Duration(i)*time.Hour).Format(backupTimestampLayout)
		seeded[i] = id
		if err := storage.Save(ctx, id, []byte("old")); err != nil {
			t.Fatalf("seed save: %v", err)
		}
		mt := base.Add(time.Duration(i) * time.Hour)
		if err := os.Chtimes(filepath.Join(dir, id+".sbk"), mt, mt); err != nil {
			t.Fatalf("chtimes: %v", err)
		}
	}

	// The scheduled job body: create (awaited) then prune.
	newID, err := a.CreateBackupForCentral(ctx, "alpha")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := a.Prune(ctx, "alpha", keepLast); err != nil {
		t.Fatalf("prune: %v", err)
	}

	list, err := storage.List(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != keepLast {
		t.Fatalf("after create+prune: %d backups, want exactly %d (rotation race would leave %d)",
			len(list), keepLast, keepLast+1)
	}
	// The new backup survives; the oldest seed is pruned.
	present := map[string]bool{}
	for _, e := range list {
		present[e.ID] = true
	}
	if !present[newID] {
		t.Errorf("newly-created backup %q was pruned", newID)
	}
	if present[seeded[0]] {
		t.Errorf("oldest backup %q should have been pruned", seeded[0])
	}
}

// createObservingStorage records the ordering between a create's Save and the
// List a concurrent Prune issues, so a test can assert that rotation never
// inspects storage while a create for the same central is still running.
type createObservingStorage struct {
	entries []hmapi.BackupEntry

	listCalls      atomic.Int32
	saves          atomic.Int32
	listedMidSave  atomic.Bool
	deletedIDs     []string
	deletedIDsLock sync.Mutex
}

func (s *createObservingStorage) List(context.Context) ([]hmapi.BackupEntry, error) {
	if s.saves.Load() == 0 {
		s.listedMidSave.Store(true)
	}
	s.listCalls.Add(1)
	return s.entries, nil
}

func (s *createObservingStorage) Open(context.Context, string) (io.ReadCloser, error) {
	return nil, errors.New("not used")
}

func (s *createObservingStorage) Save(context.Context, string, []byte) error {
	s.saves.Add(1)
	return nil
}

func (s *createObservingStorage) Delete(_ context.Context, id string) error {
	s.deletedIDsLock.Lock()
	defer s.deletedIDsLock.Unlock()
	s.deletedIDs = append(s.deletedIDs, id)
	return nil
}

// TestPruneWaitsForInFlightCreateOfSameCentral is the rotation-safety guard:
// Prune must take the same per-central lock the create holds. Without it the
// pruner reads the backup list while a create is still running, computes the
// keep window from an incomplete picture, and can drop a finished archive in
// favour of one that does not exist yet.
func TestPruneWaitsForInFlightCreateOfSameCentral(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	reg := newRegistryWith(t, "alpha")

	started := make(chan struct{})
	release := make(chan struct{})
	wireCreateBackup(t, reg, "alpha", func(context.Context) ([]byte, error) {
		close(started)
		<-release
		return []byte("payload"), nil
	})

	st := &createObservingStorage{entries: makeAlphaEntries(3)}
	a := NewBackupAdapter(reg).SetStorage(st)

	createDone := make(chan error, 1)
	go func() {
		_, err := a.CreateBackupForCentral(ctx, "alpha")
		createDone <- err
	}()
	<-started

	pruneDone := make(chan error, 1)
	go func() { pruneDone <- a.Prune(ctx, "alpha", 1) }()

	// The create is parked inside its CCU call. Give the prune ample time to
	// run: holding the per-central lock, it must not have touched storage yet.
	time.Sleep(50 * time.Millisecond)
	if n := st.listCalls.Load(); n != 0 {
		t.Fatalf("Prune inspected storage %d time(s) while a create for the same central was in flight", n)
	}

	close(release)
	if err := <-createDone; err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := <-pruneDone; err != nil {
		t.Fatalf("prune: %v", err)
	}
	if st.listedMidSave.Load() {
		t.Fatal("Prune listed the backups before the in-flight create had saved its archive")
	}
}

// TestConcurrentCreateAndPruneKeepsACompleteBackup drives the rotation the way
// the scheduled job's worst case looks — a create and a prune with
// keep_last: 1 racing over the same central — and asserts the outcome that
// matters to an operator: a complete, readable archive survives, and no empty
// one is ever kept.
func TestConcurrentCreateAndPruneKeepsACompleteBackup(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	reg := newRegistryWith(t, "alpha")
	payload := []byte("fresh backup payload")
	wireCreateBackup(t, reg, "alpha", func(context.Context) ([]byte, error) {
		return payload, nil
	})

	dir := t.TempDir()
	storage, err := NewFilesystemBackupStorage(dir)
	if err != nil {
		t.Fatalf("storage: %v", err)
	}
	a := NewBackupAdapter(reg).SetStorage(storage)

	seeded := "alpha-" + time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC).Format(backupTimestampLayout)
	if err := storage.Save(ctx, seeded, []byte("older complete backup")); err != nil {
		t.Fatalf("seed: %v", err)
	}

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		if _, err := a.CreateBackupForCentral(ctx, "alpha"); err != nil {
			t.Errorf("create: %v", err)
		}
	}()
	go func() {
		defer wg.Done()
		if err := a.Prune(ctx, "alpha", 1); err != nil {
			t.Errorf("prune: %v", err)
		}
	}()
	wg.Wait()

	list, err := storage.List(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) == 0 {
		t.Fatal("create+prune left no backup at all")
	}
	for _, e := range list {
		if e.Bytes == 0 {
			t.Fatalf("rotation kept an empty archive %q", e.ID)
		}
		rc, err := storage.Open(ctx, e.ID)
		if err != nil {
			t.Fatalf("surviving backup %q is not readable: %v", e.ID, err)
		}
		_ = rc.Close()
	}
}

// TestCreateBackupForCentralSerializesPerCentral verifies the per-central lock
// prevents two create runs for the same central from overlapping.
func TestCreateBackupForCentralSerializesPerCentral(t *testing.T) {
	t.Parallel()

	reg := newRegistryWith(t, "alpha")
	var (
		inFlight atomic.Int32
		maxSeen  atomic.Int32
	)
	wireCreateBackup(t, reg, "alpha", func(context.Context) ([]byte, error) {
		n := inFlight.Add(1)
		for {
			m := maxSeen.Load()
			if n <= m || maxSeen.CompareAndSwap(m, n) {
				break
			}
		}
		time.Sleep(5 * time.Millisecond)
		inFlight.Add(-1)
		return []byte("payload"), nil
	})
	dir := t.TempDir()
	storage, err := NewFilesystemBackupStorage(dir)
	if err != nil {
		t.Fatalf("storage: %v", err)
	}
	a := NewBackupAdapter(reg).SetStorage(storage)

	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = a.CreateBackupForCentral(context.Background(), "alpha")
		}()
	}
	wg.Wait()

	if got := maxSeen.Load(); got != 1 {
		t.Fatalf("per-central create ran with concurrency %d, want serialized (1)", got)
	}
}
