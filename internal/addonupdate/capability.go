// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package addonupdate

import (
	"os"

	"github.com/SukramJ/openccu-loom/internal/build"
)

// InstallerPath is the firmware-provided installer every OpenCCU /
// OpenCCU add-on host exposes. Stock eQ-3 CCU3 firmware has no
// such binary (ADR 0057 §Context), which is exactly the signal the
// capability probe keys on.
const InstallerPath = "/bin/install_addon"

// CapabilityProbe answers whether this platform supports the add-on
// self-update surfaces (ADR 0057 decision 1): an add-on build AND an
// executable firmware installer. Both seams are injectable so tests
// never touch the real filesystem or linker-stamped build variable.
type CapabilityProbe struct {
	// IsAddonBuild reports the build-time add-on flag. Defaults to
	// [build.IsAddon] in [NewCapabilityProbe].
	IsAddonBuild func() bool
	// StatInstaller resolves [InstallerPath]'s file info. Defaults to
	// [os.Stat] in [NewCapabilityProbe].
	StatInstaller func(path string) (os.FileInfo, error)
}

// NewCapabilityProbe returns a probe wired to the real build flag and
// filesystem.
func NewCapabilityProbe() CapabilityProbe {
	return CapabilityProbe{IsAddonBuild: build.IsAddon, StatInstaller: os.Stat}
}

// Supported runs the capability check. A nil IsAddonBuild or
// StatInstaller falls back to the real implementation so a
// zero-value CapabilityProbe still behaves like [NewCapabilityProbe]
// — only tests that want to override one seam need to set both.
func (p CapabilityProbe) Supported() bool {
	isAddon := p.IsAddonBuild
	if isAddon == nil {
		isAddon = build.IsAddon
	}
	if !isAddon() {
		return false
	}
	stat := p.StatInstaller
	if stat == nil {
		stat = os.Stat
	}
	info, err := stat(InstallerPath)
	if err != nil || info == nil || info.IsDir() {
		return false
	}
	// At least one execute bit must be set. The installer runs as
	// root (the add-on's own process), so we only need to rule out a
	// staged-but-not-yet-chmod'd file, not check the specific owner
	// bit.
	return info.Mode()&0o111 != 0
}
