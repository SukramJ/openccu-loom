// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package addonupdate

import (
	"bytes"
	"os"

	"github.com/SukramJ/openccu-loom/internal/build"
)

// InstallerPath is the firmware-provided installer every OpenCCU /
// OpenCCU add-on host exposes. Stock eQ-3 CCU3 firmware has no
// such binary (ADR 0057 §Context), which is exactly the signal the
// capability probe keys on.
const InstallerPath = "/bin/install_addon"

// HostVersionPath is the firmware's version manifest. openccu-lite
// marks itself there with a `VARIANT=lite` line (its
// buildroot-external/board/lite/post-build.sh appends it), and that
// marker must veto the installer signal: lite's read-only root ships
// /bin/install_addon for occulited's own catalogue installs, but
// nothing on that system ever consumes a staged new_addon.tar.gz —
// add-on updates there belong to occulited, and a confined add-on
// cannot write the stage path anyway (EROFS).
const HostVersionPath = "/VERSION"

// liteVariantLine is the exact marker line, matched per trimmed line so
// CRLF endings and a hypothetical VARIANT=liteish never match.
var liteVariantLine = []byte("VARIANT=lite")

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
	// ReadHostVersion reads [HostVersionPath]. Defaults to [os.ReadFile]
	// in [NewCapabilityProbe]. A read error means "no lite marker" — a
	// host without /VERSION (Docker, HA add-on) is decided by the other
	// two signals exactly as before.
	ReadHostVersion func(path string) ([]byte, error)
}

// NewCapabilityProbe returns a probe wired to the real build flag and
// filesystem.
func NewCapabilityProbe() CapabilityProbe {
	return CapabilityProbe{IsAddonBuild: build.IsAddon, StatInstaller: os.Stat, ReadHostVersion: os.ReadFile}
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
	readVersion := p.ReadHostVersion
	if readVersion == nil {
		readVersion = os.ReadFile
	}
	if manifest, err := readVersion(HostVersionPath); err == nil && hasLiteVariant(manifest) {
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

// HostIsLiteVariant reports whether the version manifest at path carries
// the openccu-lite marker line — the same probe the self-update veto
// uses, offered to the composition root's add-on auto-onboarding. An
// unreadable file is simply not a lite host.
func HostIsLiteVariant(path string) bool {
	manifest, err := os.ReadFile(path) //nolint:gosec // a fixed firmware path, or a test-injected one
	if err != nil {
		return false
	}
	return hasLiteVariant(manifest)
}

// hasLiteVariant reports whether the version manifest carries the exact
// openccu-lite marker line.
func hasLiteVariant(manifest []byte) bool {
	for line := range bytes.SplitSeq(manifest, []byte("\n")) {
		if bytes.Equal(bytes.TrimSpace(line), liteVariantLine) {
			return true
		}
	}
	return false
}
