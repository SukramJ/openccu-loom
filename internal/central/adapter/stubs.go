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

	"github.com/SukramJ/openccu-loom/internal/backup/sbk"
	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/pkg/hmapi"
	"github.com/SukramJ/openccu-loom/pkg/hmerr"
)

// ErrNoCentralRegistered reports that a backup operation found no
// central to act on: the adapter was constructed without a registry,
// or the registry holds no centrals. That is a wiring / configuration
// state of this daemon, not a missing feature — callers must not
// present it as "not implemented".
var ErrNoCentralRegistered = errors.New("backup: no central registered")

// errStorageNotConfigured reports that no [BackupStorage] is wired, so
// stored archives cannot be streamed. It wraps [hmerr.ErrUnsupported]
// so callers can recognise "the daemon cannot do this" without
// importing the adapter, mirroring [errUploadUnsupported].
var errStorageNotConfigured = fmt.Errorf("backup: no storage configured: %w", hmerr.ErrUnsupported)

// --- Backup adapter ---

// backupRunTimeout bounds the detached create-and-download goroutine. It
// sits above the backend's own 300 s poll budget so the backend reports a
// clean timeout before this hard ceiling cancels the context.
const backupRunTimeout = 6 * time.Minute

// BackupAdapter creates CCU backups via the reference create-and-download
// flow (start → poll status → download the .sbk) and persists the archive
// to local storage so it appears in the list and is downloadable. It treats
// the first registered central as the backup source — multi-CCU backup
// support is a follow-up. TriggerBackupForCentral, CreateBackupForCentral,
// and Restore are multi-CCU-correct: each backup id is minted from its
// owning central's name (see [BackupAdapter.mintBackupID]) and Restore resolves that owner
// back out of the id before picking a restorer, so a fleet with several
// registered centrals never uploads a backup to the wrong CCU.
//
// List / Stream / Restore consult the optional [BackupStorage] +
// [BackupRestorer] hooks; when both are nil the adapter degrades
// gracefully (empty list, stub stream, ErrRestoreUnsupported).
type BackupAdapter struct {
	registry *central.Registry
	storage  BackupStorage
	// restorer is the legacy single-restorer fallback, wired via
	// [BackupAdapter.SetRestorer]. It is only consulted when a backup id's
	// owning central cannot be resolved against the registry (e.g. a
	// manually-placed archive, or a test id that does not follow the
	// "<central>-<timestamp>" shape) — never as a substitute for a known
	// owner's dedicated restorer.
	restorer BackupRestorer
	// restorers holds one [BackupRestorer] per central name, wired via
	// [BackupAdapter.SetRestorerForCentral]. Restore prefers the entry
	// keyed by the backup id's resolved owning central.
	restorers map[string]BackupRestorer
	// restorersMu guards restorer and restorers. Both are written from the
	// per-central gated bring-up goroutines — one per configured central,
	// re-running on every re-gate after a CCU reboot — and read from the
	// REST restore handler, so the map has as many concurrent writers as
	// the installation has CCUs plus concurrent readers.
	restorersMu sync.RWMutex

	logger *slog.Logger

	// locksMu guards locks; locks holds one mutex per central so that a
	// scheduled create and a manual trigger for the same central never run
	// concurrently — the create-then-prune rotation must be serialized.
	locksMu sync.Mutex
	locks   map[string]*sync.Mutex

	// mintMu guards lastMinted; lastMinted holds the timestamp of the most
	// recent id minted per central's safe name so [BackupAdapter.mintBackupID]
	// can keep ids strictly increasing. See that method for why.
	mintMu     sync.Mutex
	lastMinted map[string]time.Time
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
		return "", ErrNoCentralRegistered
	}
	for _, u := range a.registry.List() {
		if u == nil {
			continue
		}
		id := a.mintBackupID(u.Name())
		// The backup deliberately outlives the request context: the handler
		// returns 202 immediately, which cancels the request ctx, so runBackup
		// must use its own background context with [backupRunTimeout].
		go a.runBackup(u, id) //nolint:gosec,contextcheck // G118: detached on purpose; runBackup uses its own backupRunTimeout context so the 202 response cannot cancel the backup; see #20
		return id, nil
	}
	return "", ErrNoCentralRegistered
}

// TriggerBackupForCentral implements [interfaces.BackupService]. It backs up
// exactly the named central (multi-CCU-correct), minting an id and launching
// the detached create-and-download flow like [BackupAdapter.TriggerBackup].
func (a *BackupAdapter) TriggerBackupForCentral(_ context.Context, centralName string) (string, error) {
	if a.registry == nil {
		return "", ErrNoCentralRegistered
	}
	u, ok := a.registry.Get(centralName)
	if !ok || u == nil {
		return "", fmt.Errorf("backup: unknown central %q", centralName)
	}
	id := a.mintBackupID(u.Name())
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
		return "", ErrNoCentralRegistered
	}
	u, ok := a.registry.Get(centralName)
	if !ok || u == nil {
		return "", fmt.Errorf("backup: unknown central %q", centralName)
	}
	id := a.mintBackupID(u.Name())
	if err := a.createAndSave(ctx, u, id); err != nil {
		return "", err
	}
	return id, nil
}

// Prune implements [interfaces.BackupService]. It keeps the newest keepLast
// backups for the named central and deletes the rest. keepLast <= 0 (or no
// storage) is a no-op.
//
// It takes the same per-central lock [BackupAdapter.createAndSave] holds:
// rotation reasons about the complete set of archives, and a create that is
// still running has not published its archive yet. Running the two
// concurrently lets the pruner compute its keep window from a set that is
// about to change and delete a complete backup the new one has not replaced.
func (a *BackupAdapter) Prune(ctx context.Context, centralName string, keepLast int) error {
	if keepLast <= 0 || a.storage == nil {
		return nil
	}
	lock := a.centralLock(centralName)
	lock.Lock()
	defer lock.Unlock()

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
	if err := a.storage.Save(ctx, id, ccuArchiveName(u, time.Now()), data); err != nil {
		return fmt.Errorf("backup: save: %w", err)
	}
	return nil
}

// ccuArchiveName renders the archive's name the way the CCU names its own:
// `<hostname>-<firmware version>-<YYYY-MM-DD-HHMM>.sbk`. Two archives are
// then immediately distinguishable by which CCU they came from and which
// firmware they can be restored onto — neither of which the storage id
// carries, because that id is a key the rotation pruner parses.
//
// The time is local, matching the CCU's own naming; the id keeps its UTC
// stamp. Returns "" when the CCU has not reported hostname or version yet,
// which reads back as "no name recorded" rather than as a name with holes
// in it — a half-filled name is worse than none, because it looks like a
// fact about the archive.
func ccuArchiveName(u *central.Unit, at time.Time) string {
	if u == nil {
		return ""
	}
	info := u.SystemInformation()
	host := strings.TrimSpace(info.Hostname)
	if host == "" {
		// The CCU has no hostname of its own to give; the central's name is
		// what the operator calls this CCU, which is the same distinction the
		// name has to carry.
		host = u.Name()
	}
	host = backupNameSegment(host)
	version := backupNameSegment(strings.TrimSpace(info.Version))
	if host == "" || version == "" {
		return ""
	}
	return fmt.Sprintf("%s-%s-%s.sbk", host, version, at.Format("2006-01-02-1504"))
}

// backupNameSegment sanitises one segment of a display filename. It is not
// backupSafeName: that one maps '.' to '_' because an id must stay parseable,
// and a firmware version put through it comes out as `3_87_6_20260404` — a
// version string nobody can match against a release. Dots survive here; only
// what would make the name unsafe in a filesystem path or an HTTP header does
// not. A segment left with nothing usable returns "", which suppresses the
// whole name.
func backupNameSegment(s string) string {
	out := strings.Map(func(r rune) rune {
		switch {
		case r >= 'A' && r <= 'Z', r >= 'a' && r <= 'z', r >= '0' && r <= '9',
			r == '-', r == '_', r == '.':
			return r
		default:
			return '_'
		}
	}, s)
	if strings.Trim(out, "._-") == "" {
		return ""
	}
	return out
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

// formatBackupID renders the id for a central and a mint time:
// "<safe-name>-<timestamp>". A valid single-segment filename for
// [BackupStorage].
func formatBackupID(centralName string, at time.Time) string {
	return fmt.Sprintf("%s-%s", backupSafeName(centralName), at.UTC().Format(backupTimestampLayout))
}

// mintBackupID returns a fresh backup id for centralName, guaranteed not
// to repeat an id this adapter has already handed out for that central.
//
// The timestamp resolves to one second, and [BackupStorage.Save]
// overwrites an existing id, so two creates for the same central inside
// the same second used to produce one archive where the operator had
// asked for two — the scheduled rotation then pruned against a set that
// was one backup shorter than it looked. When the clock has not moved on
// far enough, the mint time is advanced to one second past the previous
// one instead: ids stay strictly increasing, so they also keep sorting
// in creation order.
//
// The rendered width is unchanged, which matters beyond cosmetics:
// [backupBelongsTo] recovers the owning central by stripping exactly
// backupIDSuffixLen characters, and every id ever minted — including
// those already on disk from earlier releases — must keep satisfying it.
func (a *BackupAdapter) mintBackupID(centralName string) string {
	safe := backupSafeName(centralName)
	at := time.Now().UTC()

	a.mintMu.Lock()
	defer a.mintMu.Unlock()
	if a.lastMinted == nil {
		a.lastMinted = make(map[string]time.Time)
	}
	// Truncate first: two mints inside the same second render identically,
	// so the comparison has to happen at the resolution the id carries.
	at = at.Truncate(time.Second)
	if prev, ok := a.lastMinted[safe]; ok && !at.After(prev) {
		at = prev.Add(time.Second)
	}
	a.lastMinted[safe] = at
	return formatBackupID(centralName, at)
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

// ownerCentralName resolves the central that minted id, by matching id
// against every registered central's [backupSafeName] via
// [backupBelongsTo]. Returns "" when no registered central's name
// produced id (unknown shape, or the owning central has since been
// removed from the registry).
func (a *BackupAdapter) ownerCentralName(id string) string {
	if a.registry == nil {
		return ""
	}
	for _, u := range a.registry.List() {
		if u == nil {
			continue
		}
		if backupBelongsTo(id, backupSafeName(u.Name())) {
			return u.Name()
		}
	}
	return ""
}

// resolveRestorer picks the [BackupRestorer] that must handle a restore
// of id, and says why when it cannot.
//
// When id resolves to a known central via
// [BackupAdapter.ownerCentralName] the lookup is strict: only that
// central's own restorer (or nil, meaning "not yet available") is ever
// returned, so a central whose restorer has not come up cannot silently
// receive another central's restore.
//
// An uploaded archive carries no owner — its id is "upload-<timestamp>",
// which matches no central's safe name. That case used to fall through
// to the legacy single-restorer field, which nothing in production ever
// set, so every restore of an uploaded backup ended in
// ErrRestoreUnsupported and a 502. It now resolves to the only
// configured central when there is exactly one, which is the ordinary
// installation, and refuses with [hmerr.ErrRestoreTargetAmbiguous] when
// there are several.
//
// The whole decision runs under a single read lock: a bring-up goroutine
// wiring another central's restorer between the sole-restorer check and the
// central count would otherwise produce a verdict that matches neither state.
// The owning-central lookup is done before the lock because it only reads the
// registry, which has its own.
func (a *BackupAdapter) resolveRestorer(id string) (BackupRestorer, error) {
	owner := a.ownerCentralName(id)

	a.restorersMu.RLock()
	defer a.restorersMu.RUnlock()

	if owner != "" {
		return a.restorers[owner], nil
	}
	// An explicitly installed single restorer wins over everything below:
	// somebody chose it, and this code has no better information.
	if a.restorer != nil {
		return a.restorer, nil
	}
	if r := a.soleRestorerLocked(); r != nil {
		return r, nil
	}
	if n := a.countCentralsLocked(); n > 1 {
		return nil, fmt.Errorf("%w: an uploaded backup names no central and %d are configured; "+
			"restore it from that central's own backup list",
			hmerr.ErrRestoreTargetAmbiguous, n)
	}
	return nil, nil
}

// soleRestorerLocked returns the restorer of the only configured central, or
// nil when zero or several are configured. Caller holds restorersMu.
func (a *BackupAdapter) soleRestorerLocked() BackupRestorer {
	if len(a.restorers) != 1 {
		return nil
	}
	for _, r := range a.restorers {
		return r
	}
	return nil
}

// countCentralsLocked reports how many centrals could be a restore target.
// Caller holds restorersMu.
func (a *BackupAdapter) countCentralsLocked() int {
	if len(a.restorers) > 0 {
		return len(a.restorers)
	}
	if a.registry == nil {
		return 0
	}
	return len(a.registry.List())
}

// List implements handlers.BackupService. When a [BackupStorage] is
// wired the adapter delegates; otherwise the SPA's "no backups yet"
// placeholder renders. Entries the storage backend left without a
// Central (e.g. [FilesystemBackupStorage], which has no registry access)
// are backfilled from the id via [BackupAdapter.ownerCentralName] so the
// SPA can render an owning-CCU column and a restore-target picker.
func (a *BackupAdapter) List(ctx context.Context) ([]hmapi.BackupEntry, error) {
	if a.storage == nil {
		return nil, nil
	}
	entries, err := a.storage.List(ctx)
	if err != nil {
		return nil, err
	}
	for i := range entries {
		if entries[i].Central == "" {
			entries[i].Central = a.ownerCentralName(entries[i].ID)
		}
	}
	return entries, nil
}

// StorageInfo implements [interfaces.BackupService]. It reports where the
// archives are kept and what is currently in there.
//
// The location comes from the storage backend itself rather than from the
// configuration: `backup.dir` is empty in the common case (the daemon then
// falls back to `<data_dir>/backups`) and on a CCU add-on install it is set
// from the CCU's own backup target at every start, so the config value and
// the directory actually in use are routinely different strings. A backend
// that does not implement [BackupStorageLocator] reports no location, which
// reads back as "not known" rather than as a wrong path.
func (a *BackupAdapter) StorageInfo(ctx context.Context) (hmapi.BackupStorageInfo, error) {
	if a.storage == nil {
		return hmapi.BackupStorageInfo{}, nil
	}
	info := hmapi.BackupStorageInfo{Available: true}
	if loc, ok := a.storage.(BackupStorageLocator); ok {
		info.Dir = loc.Location()
	}
	entries, err := a.storage.List(ctx)
	if err != nil {
		return hmapi.BackupStorageInfo{}, err
	}
	info.Count = len(entries)
	for _, e := range entries {
		info.Bytes += e.Bytes
	}
	return info, nil
}

// Delete implements [interfaces.BackupService]. It removes one stored
// archive and its recorded display name.
//
// It takes the owning central's lock, so a delete never runs while that
// central's create-and-save or rotation prune is mid-flight: the pruner
// lists, sorts and deletes under the same lock, and a concurrent delete
// would otherwise let it act on a listing that no longer describes the
// storage.
func (a *BackupAdapter) Delete(ctx context.Context, id string) error {
	if a.storage == nil {
		return errStorageNotConfigured
	}
	lock := a.centralLock(a.ownerCentralName(id))
	lock.Lock()
	defer lock.Unlock()
	return a.storage.Delete(ctx, id)
}

// Stream implements handlers.BackupService.
func (a *BackupAdapter) Stream(ctx context.Context, id string, w io.Writer) error {
	if a.storage == nil {
		return errStorageNotConfigured
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
// from the configured storage and hands it to the restorer that owns
// the backup's originating central for upload to that CCU. Either
// dependency missing, or a resolved owner with no dedicated restorer
// wired, → the adapter surfaces [ErrRestoreUnsupported] rather than ever
// falling back to a different central's restorer (which would upload
// the archive to the wrong CCU — see ADR 0002).
//
// The archive is inspected before a single byte reaches the CCU. Upload
// is not a reversible step: the CCU accepts the file, unpacks it and
// reboots into the restored state, so an archive that was truncated by a
// proxy, damaged on disk or never a system backup at all takes the CCU
// down with it. The same structural check the upload endpoint runs is
// the last point at which that can still be refused.
func (a *BackupAdapter) Restore(ctx context.Context, id string) (string, error) {
	if a.storage == nil {
		return "", ErrRestoreUnsupported
	}
	restorer, err := a.resolveRestorer(id)
	if err != nil {
		return "", err
	}
	if restorer == nil {
		return "", ErrRestoreUnsupported
	}
	if err := a.inspectStored(ctx, id); err != nil {
		return "", err
	}
	rc, err := a.storage.Open(ctx, id)
	if err != nil {
		return "", err
	}
	defer func() { _ = rc.Close() }()
	return restorer.Restore(ctx, id, rc)
}

// inspectStored streams the stored archive through [sbk.Inspect] and
// reports what is wrong with it, if anything.
//
// It opens the archive a second time rather than buffering it: a .sbk
// runs to tens of megabytes, and the restore has to hand the restorer a
// reader from the first byte anyway. Two sequential reads of a local
// file cost far less than holding the whole archive in memory for the
// duration of an upload.
func (a *BackupAdapter) inspectStored(ctx context.Context, id string) error {
	rc, err := a.storage.Open(ctx, id)
	if err != nil {
		return err
	}
	defer func() { _ = rc.Close() }()
	if _, err := sbk.Inspect(rc); err != nil {
		return fmt.Errorf("backup: refusing to restore %s: %w", id, err)
	}
	return nil
}

// uploadedBackupSaver is the narrow capability a storage backend exposes
// when it can take in an externally-supplied archive. Kept as an optional
// interface so a storage that is read-only (or absent) simply reports the
// feature unavailable instead of forcing every backend to implement it.
type uploadedBackupSaver interface {
	SaveUploaded(ctx context.Context, filename string, data []byte) (hmapi.BackupEntry, error)
}

// errUploadUnsupported reports that no storage is wired, or that the wired
// storage cannot take in externally-supplied archives. It wraps the shared
// [hmerr.ErrUnsupported] so the REST layer can recognise the condition
// without importing this package - handlers deliberately depend on narrow
// interfaces, not on the adapter.
var errUploadUnsupported = fmt.Errorf("backup: storage does not accept uploads: %w", hmerr.ErrUnsupported)

// SaveUploaded stores an operator-supplied backup archive so it becomes
// restorable through the ordinary restore path.
func (a *BackupAdapter) SaveUploaded(
	ctx context.Context, filename string, data []byte,
) (hmapi.BackupEntry, error) {
	if a == nil || a.storage == nil {
		return hmapi.BackupEntry{}, errUploadUnsupported
	}
	saver, ok := a.storage.(uploadedBackupSaver)
	if !ok {
		return hmapi.BackupEntry{}, errUploadUnsupported
	}
	return saver.SaveUploaded(ctx, filename, data)
}
