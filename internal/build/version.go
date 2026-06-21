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
	Version = "0.8.0"
	// Commit is the short git SHA the binary was built from.
	Commit = "none"
	// BuildDate is the UTC RFC3339 timestamp of the build.
	BuildDate = "unknown"
)
