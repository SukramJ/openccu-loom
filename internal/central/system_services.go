// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package central

import "context"

// The per-central system-management ports. A central's south profile
// installs them; the north-bound maintenance paths call them without
// knowing which system answers. A port may refuse an operation the system
// does not offer, or that its credential does not permit, with a
// *hmerr.FeatureUnavailableError wrapping the error today's callers
// already branch on.

// PowerControl reboots, halts, or restarts the system into a maintenance
// mode.
type PowerControl interface {
	Reboot(ctx context.Context) error
	PowerOff(ctx context.Context) error
	EnterSafeMode(ctx context.Context) error
	EnterRecoveryMode(ctx context.Context) error
}

// PositionWriter writes the astro reference position of the system.
type PositionWriter interface {
	SetPosition(ctx context.Context, longitude, latitude float64) error
}

// SystemFirmwareDownloader has the system fetch its newest firmware and
// stage it for a later install.
type SystemFirmwareDownloader interface {
	DownloadSystemFirmware(ctx context.Context) error
}

// SystemServices is the set of management ports of one central. A nil
// member means nothing is installed yet (the central has not come up).
type SystemServices struct {
	Power    PowerControl
	Position PositionWriter
	Firmware SystemFirmwareDownloader
}

// BackupArchive is a backup as the system produced it. FileName is the
// name the system gave it; empty means the caller derives one.
type BackupArchive struct {
	Data     []byte
	FileName string
}

// SetSystemServices installs the central's management ports.
func (u *Unit) SetSystemServices(s SystemServices) {
	u.services.mu.Lock()
	u.services.system = s
	u.services.mu.Unlock()
}

// SystemServices returns the central's management ports.
func (u *Unit) SystemServices() SystemServices {
	u.services.mu.RLock()
	defer u.services.mu.RUnlock()
	return u.services.system
}
