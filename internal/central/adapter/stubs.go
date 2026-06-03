// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/north/rest/handlers"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// ErrUnimplemented is returned by MVP stubs when a feature needs a
// backing service that has not been wired yet.
var ErrUnimplemented = errors.New("adapter: not implemented in MVP")

// --- Paramsets stub ---

// ParamsetsAdapter is the MVP stub. Real implementations come with
// the backend wiring (§14.6 CallParameterCollector).
type ParamsetsAdapter struct{}

// NewParamsetsAdapter constructs the stub.
//
// loom:reachable:reason="wired into REST paramset handler in daemon.go north-side setup"
func NewParamsetsAdapter() *ParamsetsAdapter { return &ParamsetsAdapter{} }

// GetParamset implements handlers.ParamsetService.
func (a *ParamsetsAdapter) GetParamset(
	_ context.Context, _ string, _ hmenum.ParamsetKey,
) (map[string]any, error) {
	return nil, ErrUnimplemented
}

// PutParamset implements handlers.ParamsetService.
func (a *ParamsetsAdapter) PutParamset(
	_ context.Context, _ string, _ hmenum.ParamsetKey, _ map[string]any,
) error {
	return ErrUnimplemented
}

// --- Incidents stub ---

// IncidentsAdapter returns an empty list until persistent incident
// storage is wired.
type IncidentsAdapter struct{}

// NewIncidentsAdapter constructs the stub.
func NewIncidentsAdapter() *IncidentsAdapter { return &IncidentsAdapter{} }

// Incidents implements handlers.IncidentsReader.
func (a *IncidentsAdapter) Incidents() []handlers.Incident { return nil }

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
}

// NewBackupAdapter wires the live adapter.
func NewBackupAdapter(r *central.Registry) *BackupAdapter {
	return &BackupAdapter{registry: r}
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
		go a.runBackup(u, id) //nolint:gosec // G118: detached on purpose; see comment above
		return id, nil
	}
	return "", ErrUnimplemented
}

// runBackup executes the create-and-download flow on a detached context and
// persists the resulting archive. It is the asynchronous tail of
// [BackupAdapter.TriggerBackup]; failures are logged, not surfaced to the
// original HTTP caller (which has already received its 202 + id).
func (a *BackupAdapter) runBackup(u *central.Unit, id string) {
	ctx, cancel := context.WithTimeout(context.Background(), backupRunTimeout)
	defer cancel()

	data, err := u.CreateBackup(ctx)
	if err != nil {
		a.log().Error("backup.create.failed",
			slog.String("central", u.Name()),
			slog.String("id", id),
			slog.String("err", err.Error()))
		return
	}
	if a.storage == nil {
		a.log().Warn("backup.create.no_storage",
			slog.String("central", u.Name()),
			slog.String("id", id),
			slog.Int("bytes", len(data)))
		return
	}
	if err := a.storage.Save(ctx, id, data); err != nil {
		a.log().Error("backup.save.failed",
			slog.String("central", u.Name()),
			slog.String("id", id),
			slog.String("err", err.Error()))
		return
	}
	a.log().Info("backup.create.ok",
		slog.String("central", u.Name()),
		slog.String("id", id),
		slog.Int("bytes", len(data)))
}

// backupID derives a storage-safe id from the central name and the current
// time. Any character outside [A-Za-z0-9_-] is replaced so the id is a valid
// single-segment filename for [BackupStorage].
func backupID(centralName string) string {
	ts := time.Now().UTC().Format("20060102-150405")
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
	return fmt.Sprintf("%s-%s", safe, ts)
}

// List implements handlers.BackupService. When a [BackupStorage] is
// wired the adapter delegates; otherwise the SPA's "no backups yet"
// placeholder renders.
func (a *BackupAdapter) List(ctx context.Context) ([]handlers.BackupEntry, error) {
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

// --- InstallMode stub ---

// InstallModeAdapter is the MVP stub. It reports "off" and rejects
// writes until the client wiring lands.
type InstallModeAdapter struct{}

// NewInstallModeAdapter constructs the stub.
func NewInstallModeAdapter() *InstallModeAdapter { return &InstallModeAdapter{} }

// InstallModeState implements handlers.InstallModeController.
func (a *InstallModeAdapter) InstallModeState() (bool, time.Duration) { return false, 0 }

// SetInstallMode implements handlers.InstallModeController.
func (a *InstallModeAdapter) SetInstallMode(context.Context, bool, time.Duration) error {
	return ErrUnimplemented
}
