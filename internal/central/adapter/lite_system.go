// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/client/backends"
	"github.com/SukramJ/openccu-loom/internal/client/transport/occulited"
	"github.com/SukramJ/openccu-loom/internal/model/hub"
	"github.com/SukramJ/openccu-loom/internal/model/taxonomy"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmerr"
)

// liteSystemDownloadTimeout bounds the release download a firmware
// install starts when nothing is staged yet; the box streams the image
// from its release feed and verifies it before answering.
const liteSystemDownloadTimeout = 30 * time.Minute

// liteSystem is an openccu-lite central's system management through the
// box's system API. Every operation first asks the central's feature set,
// so a token without the scope — or an operation the box does not have —
// is refused with a *hmerr.FeatureUnavailableError wrapping the error the
// caller already branches on, before anything reaches the box.
type liteSystem struct {
	client *occulited.Client
	unit   *central.Unit
	name   string
}

func newLiteSystem(p *liteProfile, unit *central.Unit) *liteSystem {
	return &liteSystem{client: p.client, unit: unit, name: p.cc.Name}
}

func (s *liteSystem) require(k hmenum.Feature, legacy error) error {
	return s.unit.Features().Require(s.name, k, legacy)
}

// services returns the central's management ports.
func (s *liteSystem) services() central.SystemServices {
	return central.SystemServices{
		Power: s, Position: s, Firmware: s,
		Groups:   liteHeatingGroups{liteSystem: s},
		Accounts: liteAccountVerifier{client: s.client},
	}
}

// Reboot implements [central.PowerControl].
func (s *liteSystem) Reboot(ctx context.Context) error {
	if err := s.require(hmenum.FeatureSystemReboot, backends.ErrUnsupported); err != nil {
		return err
	}
	_, err := s.client.Reboot(ctx)
	return err
}

// PowerOff implements [central.PowerControl].
func (s *liteSystem) PowerOff(ctx context.Context) error {
	if err := s.require(hmenum.FeatureSystemPowerOff, backends.ErrUnsupported); err != nil {
		return err
	}
	_, err := s.client.Halt(ctx)
	return err
}

// EnterSafeMode implements [central.PowerControl]: openccu-lite has no
// ReGa to keep down, so there is no safe mode.
func (s *liteSystem) EnterSafeMode(context.Context) error {
	return s.require(hmenum.FeatureSystemSafeMode, backends.ErrUnsupported)
}

// EnterRecoveryMode implements [central.PowerControl].
func (s *liteSystem) EnterRecoveryMode(ctx context.Context) error {
	if err := s.require(hmenum.FeatureSystemRecoveryMode, backends.ErrUnsupported); err != nil {
		return err
	}
	_, err := s.client.RebootRecovery(ctx)
	return err
}

// SetPosition implements [central.PositionWriter]: the astro position
// lives in ReGa, which openccu-lite does not run.
func (s *liteSystem) SetPosition(context.Context, float64, float64) error {
	return s.require(hmenum.FeatureSystemPosition, backends.ErrUnsupported)
}

// DownloadSystemFirmware implements [central.SystemFirmwareDownloader]:
// the box downloads, verifies and stages the release its feed offers.
func (s *liteSystem) DownloadSystemFirmware(ctx context.Context) error {
	if err := s.require(hmenum.FeatureHubSystemUpdateInstall, backends.ErrUnsupported); err != nil {
		return err
	}
	_, err := s.client.DownloadSystemUpdate(ctx)
	return err
}

// createBackup downloads a backup the box creates on request. The archive
// is kept as served, with the box's own file name — encrypted (".sbk.age")
// when the box owner switched backup encryption on, which is their choice
// to make, not the daemon's.
func (s *liteSystem) createBackup(ctx context.Context) (central.BackupArchive, error) {
	if err := s.require(hmenum.FeatureSystemBackupCreate, backends.ErrUnsupported); err != nil {
		return central.BackupArchive{}, err
	}
	b, err := s.client.DownloadBackup(ctx)
	if err != nil {
		return central.BackupArchive{}, err
	}
	defer func() { _ = b.Body.Close() }()
	data, err := io.ReadAll(b.Body)
	if err != nil {
		return central.BackupArchive{}, fmt.Errorf("openccu-lite backup: read archive: %w", err)
	}
	return central.BackupArchive{Data: data, FileName: b.FileName}, nil
}

// errLiteRestoreNeedsRecoveryKey refuses an archive the box can only open
// with its recovery key, which the daemon never holds.
var errLiteRestoreNeedsRecoveryKey = fmt.Errorf("%w: restore: the archive needs the box's recovery key; restore it on the box", hmerr.ErrValidation)

// Restore implements [BackupRestorer]: the box checks the archive first
// and only an archive it accepts is applied. The box reboots afterwards;
// the readiness gate takes the central through "waiting" back to ready.
func (s *liteSystem) Restore(ctx context.Context, id string, payload io.Reader) (string, error) {
	if err := s.require(hmenum.FeatureSystemBackupRestore, ErrRestoreUnsupported); err != nil {
		return "", err
	}
	data, err := io.ReadAll(payload)
	if err != nil {
		return "", fmt.Errorf("openccu-lite restore: read archive: %w", err)
	}
	check, err := s.client.CheckRestore(ctx, id, bytes.NewReader(data))
	if err != nil {
		return "", fmt.Errorf("openccu-lite restore check: %w", err)
	}
	if check.Encryption.NeedsRecoveryKey {
		return "", errLiteRestoreNeedsRecoveryKey
	}
	if !check.Check.OK {
		return "", fmt.Errorf("%w: restore: the box refused the archive: %s", hmerr.ErrValidation, check.Check.Output)
	}
	applied, err := s.client.ApplyRestore(ctx, occulited.RestoreApplyRequest{File: check.File})
	if err != nil {
		return "", fmt.Errorf("openccu-lite restore apply: %w", err)
	}
	if !applied.OK {
		return "", fmt.Errorf("openccu-lite restore: the box did not apply the archive: %s", applied.Output)
	}
	return id, nil
}

// RestoresEncryptedArchives implements [EncryptedArchiveRestorer]: the box
// opens an archive encrypted to its own key, and its restore check runs
// before anything is applied — an archive it cannot open is refused there
// (or answered with "needs the recovery key"), never unpacked blind.
func (*liteSystem) RestoresEncryptedArchives() bool { return true }

// TriggerBackup implements [hub.BackupTrigger]: the box runs its own
// backup to its configured targets.
func (s *liteSystem) TriggerBackup(ctx context.Context) error {
	if err := s.require(hmenum.FeatureSystemBackupCreate, hub.ErrNoBackupTrigger); err != nil {
		return err
	}
	_, err := s.client.RunBackup(ctx, "")
	return err
}

// Backup run states [liteSystem.BackupStatus] reports.
const (
	liteBackupRunning = "running"
	liteBackupOK      = "ok"
	liteBackupFailed  = "failed"
	liteBackupIdle    = "idle"
)

// BackupStatus implements [hub.BackupTrigger] from the box's backup
// targets: running while any target runs, otherwise the outcome of the
// newest backup across all targets, idle when none has run.
func (s *liteSystem) BackupStatus(ctx context.Context) (string, error) {
	targets, err := s.client.BackupTargets(ctx)
	if err != nil {
		// The status read needs system:read, a different scope than
		// creating a backup; the box names the one it wants.
		if sm, ok := errors.AsType[*hmerr.ScopeMissingError](err); ok {
			return "", &hmerr.FeatureUnavailableError{
				Central: s.name,
				Feature: hmenum.FeatureSystemBackupCreate,
				Reason:  hmenum.FeatureReasonMissingScope,
				Scope:   sm.Scope,
				Legacy:  hub.ErrNoBackupTrigger,
			}
		}
		return "", err
	}
	return liteBackupState(targets), nil
}

func liteBackupState(targets occulited.BackupTargets) string {
	var newest *occulited.LastBackup
	var newestAt time.Time
	for i := range targets.Targets {
		t := targets.Targets[i]
		if t.State.State == liteBackupRunning {
			return liteBackupRunning
		}
		if t.LastBackup == nil {
			continue
		}
		at, err := time.Parse(time.RFC3339, t.LastBackup.At)
		if err != nil {
			continue
		}
		if newest == nil || at.After(newestAt) {
			newest, newestAt = t.LastBackup, at
		}
	}
	switch {
	case newest == nil:
		return liteBackupIdle
	case newest.OK:
		return liteBackupOK
	default:
		return liteBackupFailed
	}
}

// TriggerFirmwareUpdate implements [hub.FirmwareUpdater]: download the
// offered release unless one is staged, then install it — the box arms
// its recovery system and reboots into the installation.
func (s *liteSystem) TriggerFirmwareUpdate(ctx context.Context) error {
	if err := s.require(hmenum.FeatureHubSystemUpdateInstall, hub.ErrNoFirmwareUpdater); err != nil {
		return err
	}
	state, err := s.client.SystemUpdate(ctx)
	if err != nil {
		return err
	}
	if state.Staged == nil {
		dctx, cancel := context.WithTimeout(ctx, liteSystemDownloadTimeout)
		_, err := s.client.DownloadSystemUpdate(dctx)
		cancel()
		if err != nil {
			return fmt.Errorf("openccu-lite system update download: %w", err)
		}
	}
	_, err = s.client.InstallSystemUpdate(ctx)
	return err
}

// updateInfo reads the box's update state as the hub model keeps it: the
// running openccu-lite version, and what the release feed offers.
func (s *liteSystem) updateInfo(ctx context.Context) (hub.UpdateInfo, error) {
	state, err := s.client.SystemUpdate(ctx)
	if err != nil {
		return hub.UpdateInfo{}, err
	}
	info := hub.UpdateInfo{CurrentFirmware: state.Running.Lite, CheckScriptAvailable: state.Feed != nil}
	if info.CurrentFirmware == "" {
		info.CurrentFirmware = state.Running.Version
	}
	if state.Feed != nil && state.Feed.Available != nil {
		info.AvailableFirmware = state.Feed.Available.Version
		info.UpdateAvailable = state.Feed.Available.Newer
	}
	return info, nil
}

// refreshSystemUpdate is the system-update refresh hook. A token without
// system:read has no update state to read; the feature set already says
// why, so the hook stays quiet.
func (s *liteSystem) refreshSystemUpdate(ctx context.Context) error {
	if s.unit.HubModel == nil || !s.unit.Features().Available(hmenum.FeatureHubSystemUpdate) {
		return nil
	}
	info, err := s.updateInfo(ctx)
	if err != nil {
		return fmt.Errorf("openccu-lite system update: %w", err)
	}
	s.unit.HubModel.Update.OnInfo(info)
	return nil
}

// monitorInstall clears the hub's in-progress flag once the box runs a
// different version after an install, or the deadline passes. The box is
// unreachable while it installs; failed polls are expected.
func (s *liteSystem) monitorInstall(upd *hub.Update) {
	ctx, cancel := context.WithTimeout(context.Background(),
		systemUpdateProgressCheckInterval*time.Duration(systemUpdateProgressMaxPolls)+time.Minute)
	SafeGo("lite.system_update.monitor."+s.name, func() {
		defer cancel()
		upd.MonitorProgress(ctx, func(ctx context.Context) (string, error) {
			info, err := s.updateInfo(ctx)
			return info.CurrentFirmware, err
		}, systemUpdateProgressCheckInterval, systemUpdateProgressMaxPolls)
	})
}

// liteHubWriter is the hub's write side on an openccu-lite central: room
// and function assignments through the metadata store, backups and the
// system update through the system API, and a refusal for everything that
// needs ReGa (system variables and their usage, the inbox).
type liteHubWriter struct {
	*liteSystem
	meta *liteMetaWriter
}

var (
	_ hub.Mutator             = (*liteHubWriter)(nil)
	_ hub.SysvarUsageReader   = (*liteHubWriter)(nil)
	_ hub.TaxonomyAdmin       = (*liteHubWriter)(nil)
	_ hub.TaxonomyPathMutator = (*liteHubWriter)(nil)
)

// SetDeviceRooms implements [hub.RoomMutator].
func (w *liteHubWriter) SetDeviceRooms(ctx context.Context, address string, rooms []string) error {
	return w.meta.SetDeviceRooms(ctx, address, rooms)
}

// SetTaxonomyRefs implements [hub.TaxonomyPathMutator].
func (w *liteHubWriter) SetTaxonomyRefs(ctx context.Context, address string, enum taxonomy.EnumID, refs []taxonomy.Ref) error {
	return w.meta.SetTaxonomyRefs(ctx, address, enum, refs)
}

// CreateNode implements [hub.TaxonomyAdmin].
func (w *liteHubWriter) CreateNode(ctx context.Context, enum taxonomy.EnumID, parent taxonomy.Path, name string) (taxonomy.Ref, error) {
	return w.meta.CreateNode(ctx, enum, parent, name)
}

// RenameNode implements [hub.TaxonomyAdmin].
func (w *liteHubWriter) RenameNode(ctx context.Context, r taxonomy.Ref, name string) error {
	return w.meta.RenameNode(ctx, r, name)
}

// MoveNode implements [hub.TaxonomyAdmin].
func (w *liteHubWriter) MoveNode(ctx context.Context, r taxonomy.Ref, parent taxonomy.Path, position *int) error {
	return w.meta.MoveNode(ctx, r, parent, position)
}

// DeleteNode implements [hub.TaxonomyAdmin].
func (w *liteHubWriter) DeleteNode(ctx context.Context, r taxonomy.Ref) error {
	return w.meta.DeleteNode(ctx, r)
}

// SetDeviceFunctions implements [hub.FunctionMutator].
func (w *liteHubWriter) SetDeviceFunctions(ctx context.Context, address string, functions []string) error {
	return w.meta.SetDeviceFunctions(ctx, address, functions)
}

// CreateSysvar implements [hub.SysvarMutator]; openccu-lite has no system
// variables.
func (w *liteHubWriter) CreateSysvar(context.Context, hub.SysvarCreateSpec) error {
	return w.require(hmenum.FeatureHubSysvars, hub.ErrNoSysvarMutator)
}

// UpdateSysvar implements [hub.SysvarMutator].
func (w *liteHubWriter) UpdateSysvar(context.Context, hub.SysvarUpdateSpec) error {
	return w.require(hmenum.FeatureHubSysvars, hub.ErrNoSysvarMutator)
}

// DeleteSysvar implements [hub.SysvarMutator].
func (w *liteHubWriter) DeleteSysvar(context.Context, string) error {
	return w.require(hmenum.FeatureHubSysvars, hub.ErrNoSysvarMutator)
}

// SysvarUsagePrograms implements [hub.SysvarUsageReader].
func (w *liteHubWriter) SysvarUsagePrograms(context.Context, string) ([]hub.SysvarUsage, error) {
	return nil, w.require(hmenum.FeatureHubSysvars, hub.ErrNoSysvarUsageReader)
}

// AcceptDeviceInInbox implements [hub.InboxAccepter]; the inbox is a ReGa
// concept.
func (w *liteHubWriter) AcceptDeviceInInbox(context.Context, string) error {
	return w.require(hmenum.FeatureHubInbox, hub.ErrNoInboxAccepter)
}
