// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/pkg/hmapi"
)

// ErrUnimplemented is returned by MVP stubs when a feature needs a
// backing service that has not been wired yet.
var ErrUnimplemented = errors.New("adapter: not implemented in MVP")

// --- Backup adapter ---

// backupRunTimeout bounds the detached create-and-download goroutine. It
// sits above the backend's own 300 s poll budget so the backend reports a
// clean timeout before this hard ceiling cancels the context.
const backupRunTimeout = 6 * time.Minute

// BackupAdapter creates CCU backups via the reference create-and-download
// flow (start → poll status → download the .sbk) and persists the archive
// to local storage so it appears in the list and is downloadable. It treats
// the first registered central as the backup source — multi-CCU backup
// support is a follow-up.
//
// List / Stream / Restore consult the optional [BackupStorage] +
// [BackupRestorer] hooks; when both are nil the adapter degrades
// gracefully (empty list, stub stream, ErrRestoreUnsupported).
type BackupAdapter struct {
	registry *central.Registry
	storage  BackupStorage
	restorer BackupRestorer
	logger   *slog.Logger

	// locksMu guards locks; locks holds one mutex per central so that a
	// scheduled create and a manual trigger for the same central never run
	// concurrently — the create-then-prune rotation must be serialized.
	locksMu sync.Mutex
	locks   map[string]*sync.Mutex
}

// NewBackupAdapter wires the live adapter.
func NewBackupAdapter(r *central.Registry) *BackupAdapter {
	return &BackupAdapter{registry: r}
}

// centralLock returns the per-central serialization mutex, creating it on
// first use.
func (a *BackupAdapter) centralLock(name string) *sync.Mutex {
	a.locksMu.Lock()
	defer a.locksMu.Unlock()
	if a.locks == nil {
		a.locks = make(map[string]*sync.Mutex)
	}
	m, ok := a.locks[name]
	if !ok {
		m = &sync.Mutex{}
		a.locks[name] = m
	}
	return m
}

// SetLogger sets the logger used for the asynchronous backup goroutine.
// Returns the receiver for chaining. A nil logger falls back to
// [slog.Default].
func (a *BackupAdapter) SetLogger(l *slog.Logger) *BackupAdapter {
	a.logger = l
	return a
}

func (a *BackupAdapter) log() *slog.Logger {
	if a.logger != nil {
		return a.logger
	}
	return slog.Default()
}

// TriggerBackup implements handlers.BackupService. It mints a backup id,
// launches the create-and-download flow on a detached context, and returns
// the id immediately (the handler answers 202 Accepted). The archive lands
// in storage once the asynchronous run completes; until then the SPA's
// backup list simply does not show it yet.
func (a *BackupAdapter) TriggerBackup(_ context.Context) (string, error) {
	if a.registry == nil {
		return "", ErrUnimplemented
	}
	for _, u := range a.registry.List() {
		if u == nil {
			continue
		}
		id := backupID(u.Name())
		// The backup deliberately outlives the request context: the handler
		// returns 202 immediately, which cancels the request ctx, so runBackup
		// must use its own background context with [backupRunTimeout].
		go a.runBackup(u, id) //nolint:gosec,contextcheck // G118: detached on purpose; runBackup uses its own backupRunTimeout context so the 202 response cannot cancel the backup; see #20
		return id, nil
	}
	return "", ErrUnimplemented
}

// TriggerBackupForCentral implements [interfaces.BackupService]. It backs up
// exactly the named central (multi-CCU-correct), minting an id and launching
// the detached create-and-download flow like [BackupAdapter.TriggerBackup].
func (a *BackupAdapter) TriggerBackupForCentral(_ context.Context, centralName string) (string, error) {
	if a.registry == nil {
		return "", ErrUnimplemented
	}
	u, ok := a.registry.Get(centralName)
	if !ok || u == nil {
		return "", fmt.Errorf("backup: unknown central %q", centralName)
	}
	id := backupID(u.Name())
	go a.runBackup(u, id) //nolint:gosec,contextcheck // G118: detached on purpose; runBackup uses its own backupRunTimeout context so the trigger context cannot cancel the backup; see #20
	return id, nil
}

// CreateBackupForCentral is the synchronous sibling of
// [BackupAdapter.TriggerBackupForCentral]: it creates and durably saves the
// backup for the named central before returning, so a caller (the scheduled
// job) can prune only after the new backup exists. It serializes on the
// per-central lock, so a concurrent manual trigger or a second scheduled run
// for the same central waits rather than racing the rotation.
func (a *BackupAdapter) CreateBackupForCentral(ctx context.Context, centralName string) (string, error) {
	if a.registry == nil {
		return "", ErrUnimplemented
	}
	u, ok := a.registry.Get(centralName)
	if !ok || u == nil {
		return "", fmt.Errorf("backup: unknown central %q", centralName)
	}
	id := backupID(u.Name())
	if err := a.createAndSave(ctx, u, id); err != nil {
		return "", err
	}
	return id, nil
}

// Prune implements [interfaces.BackupService]. It keeps the newest keepLast
// backups for the named central and deletes the rest. keepLast <= 0 (or no
// storage) is a no-op.
func (a *BackupAdapter) Prune(ctx context.Context, centralName string, keepLast int) error {
	if keepLast <= 0 || a.storage == nil {
		return nil
	}
	entries, err := a.storage.List(ctx)
	if err != nil {
		return fmt.Errorf("backup: prune list: %w", err)
	}
	safe := backupSafeName(centralName)
	mine := make([]hmapi.BackupEntry, 0, len(entries))
	for _, e := range entries {
		if backupBelongsTo(e.ID, safe) {
			mine = append(mine, e)
		}
	}
	if len(mine) <= keepLast {
		return nil
	}
	// Newest first, then delete everything past keepLast.
	sort.Slice(mine, func(i, j int) bool { return mine[i].CreatedAt.After(mine[j].CreatedAt) })
	for _, e := range mine[keepLast:] {
		if err := a.storage.Delete(ctx, e.ID); err != nil {
			return fmt.Errorf("backup: prune delete %s: %w", e.ID, err)
		}
	}
	return nil
}

// runBackup executes the create-and-download flow on a detached context and
// persists the resulting archive. It is the asynchronous tail of
// [BackupAdapter.TriggerBackup]; failures are logged, not surfaced to the
// original HTTP caller (which has already received its 202 + id).
func (a *BackupAdapter) runBackup(u *central.Unit, id string) {
	ctx, cancel := context.WithTimeout(context.Background(), backupRunTimeout)
	defer cancel()

	if err := a.createAndSave(ctx, u, id); err != nil {
		a.log().Error("backup.create.failed",
			slog.String("central", u.Name()),
			slog.String("id", id),
			slog.String("err", err.Error()))
		return
	}
	a.log().Info("backup.create.ok",
		slog.String("central", u.Name()),
		slog.String("id", id))
}

// createAndSave is the shared create-then-persist core used by both the
// asynchronous [BackupAdapter.runBackup] and the synchronous
// [BackupAdapter.CreateBackupForCentral]. It holds the per-central lock for
// the whole create+save so a rotation prune (which the scheduled job runs
// only after this returns) never sees the fleet in a mid-save state.
func (a *BackupAdapter) createAndSave(ctx context.Context, u *central.Unit, id string) error {
	lock := a.centralLock(u.Name())
	lock.Lock()
	defer lock.Unlock()

	data, err := u.CreateBackup(ctx)
	if err != nil {
		return fmt.Errorf("backup: create: %w", err)
	}
	if a.storage == nil {
		a.log().Warn("backup.create.no_storage",
			slog.String("central", u.Name()),
			slog.String("id", id),
			slog.Int("bytes", len(data)))
		return nil
	}
	if err := a.storage.Save(ctx, id, data); err != nil {
		return fmt.Errorf("backup: save: %w", err)
	}
	return nil
}

// backupTimestampLayout is the fixed-width UTC timestamp appended to every
// backup id. Its rendered length (incl. the leading separator, see
// backupID) is backupIDSuffixLen.
const backupTimestampLayout = "20060102-150405"

// backupIDSuffixLen is the length of the "-<timestamp>" suffix backupID
// appends: one separator + the 15-char timestamp = 16. backupBelongsTo strips
// exactly this to recover the central's safe name.
const backupIDSuffixLen = 1 + len(backupTimestampLayout)

// backupSafeName sanitises a central name into a single filename segment: any
// character outside [A-Za-z0-9_-] becomes '_'. Empty maps to "ccu".
func backupSafeName(centralName string) string {
	safe := strings.Map(func(r rune) rune {
		switch {
		case r >= 'A' && r <= 'Z', r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '_':
			return r
		default:
			return '_'
		}
	}, centralName)
	if safe == "" {
		safe = "ccu"
	}
	return safe
}

// backupID derives a storage-safe id from the central name and the current
// time: "<safe-name>-<timestamp>". A valid single-segment filename for
// [BackupStorage].
func backupID(centralName string) string {
	return fmt.Sprintf("%s-%s", backupSafeName(centralName), time.Now().UTC().Format(backupTimestampLayout))
}

// backupBelongsTo reports whether a backup id was minted for the central
// whose safe name is safe — i.e. id is "<safe>-<timestamp>". It strips the
// fixed-width timestamp suffix and compares the remainder, so a central named
// "ccu" is not confused with "ccu-01".
func backupBelongsTo(id, safe string) bool {
	if len(id) <= backupIDSuffixLen {
		return false
	}
	return id[:len(id)-backupIDSuffixLen] == safe
}

// List implements handlers.BackupService. When a [BackupStorage] is
// wired the adapter delegates; otherwise the SPA's "no backups yet"
// placeholder renders.
func (a *BackupAdapter) List(ctx context.Context) ([]hmapi.BackupEntry, error) {
	if a.storage == nil {
		return nil, nil
	}
	return a.storage.List(ctx)
}

// Stream implements handlers.BackupService.
func (a *BackupAdapter) Stream(ctx context.Context, id string, w io.Writer) error {
	if a.storage == nil {
		return ErrUnimplemented
	}
	rc, err := a.storage.Open(ctx, id)
	if err != nil {
		return err
	}
	defer func() { _ = rc.Close() }()
	_, err = io.Copy(w, rc)
	return err
}

// Restore implements handlers.BackupService. Reads the named backup
// from the configured storage and hands it to the configured
// restorer for upload to the CCU. Either dependency missing → the
// adapter surfaces [ErrRestoreUnsupported] so the SPA can render a
// clear "manual restore required" message.
func (a *BackupAdapter) Restore(ctx context.Context, id string) (string, error) {
	if a.storage == nil || a.restorer == nil {
		return "", ErrRestoreUnsupported
	}
	rc, err := a.storage.Open(ctx, id)
	if err != nil {
		return "", err
	}
	defer func() { _ = rc.Close() }()
	return a.restorer.Restore(ctx, id, rc)
}
