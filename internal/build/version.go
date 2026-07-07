// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// Package build exposes build-time metadata.
//
// Values are injected via -ldflags at link time; see the Makefile
// and .goreleaser.yaml for the wiring.
package build

// Build metadata, populated by the linker via -ldflags. See the Makefile
// and .goreleaser.yaml for the wiring; unset at `go run` time.
var (
	// Version is the SemVer tag or `git describe` output.
	Version = "0.27.0"
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

// IsAddon reports whether this binary was built as the CCU add-on.
func IsAddon() bool { return AddonBuild == "true" }
