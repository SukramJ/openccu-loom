// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// Package build exposes build-time metadata.
//
// Values are injected via -ldflags at link time; see the Makefile
// and .goreleaser.yaml for the wiring.
package build

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Build metadata, populated by the linker via -ldflags. See the Makefile
// and .goreleaser.yaml for the wiring; unset at `go run` time.
var (
	// Version is the SemVer tag or `git describe` output.
	Version = "0.59.0"
	// Commit is the short git SHA the binary was built from.
	Commit = "none"
	// BuildDate is the UTC RFC3339 timestamp of the build.
	BuildDate = "unknown"
	// AddonBuild is "true" only in the CCU/RaspberryMatic add-on build
	// (stamped by script/build_ccu_addon.sh). It flips defaults that
	// only make sense when the daemon runs on the CCU itself — chiefly
	// CCU-delegated authentication (ADR 0043). Default "false" so a
	// plain `go build` / Docker image keeps the standalone behaviour.
	AddonBuild = "false"
)

// addonInstallPrefix is where the CCU add-on's update_script installs
// the daemon on the CCU (see packaging/ccu-addon/ccu/update_script).
const addonInstallPrefix = "/usr/local/addons/openccu-loom/"

// IsAddon reports whether this daemon runs as the CCU add-on: either
// the build-time stamp is set, or the running executable lives under
// the add-on install prefix. The runtime check matters because the
// release pipeline packages the prebuilt standalone binaries into the
// add-on tarball (script/build_ccu_addon.sh reuses them and cannot
// re-stamp a compiled binary) — without it every released add-on
// install reported itself standalone, which suppressed the
// CCU-delegated auth default (ADR 0043) and the self-update
// capability (ADR 0057).
func IsAddon() bool { return AddonBuild == "true" || runsFromAddonDir() }

// runsFromAddonDir resolves the running executable once and reports
// whether it lives under the add-on install prefix.
func runsFromAddonDir() bool { return addonDirCheck() }

var addonDirCheck = sync.OnceValue(func() bool {
	exe, err := os.Executable()
	if err != nil {
		return false
	}
	if resolved, rerr := filepath.EvalSymlinks(exe); rerr == nil {
		exe = resolved
	}
	return isAddonInstallPath(exe)
})

// isAddonInstallPath reports whether path lies under the add-on
// install prefix. Split out so the rule is testable without swapping
// process state.
func isAddonInstallPath(path string) bool {
	return strings.HasPrefix(path, addonInstallPrefix)
}
