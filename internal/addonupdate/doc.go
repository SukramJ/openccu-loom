// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// Package addonupdate implements the CCU add-on self-update feature
// described in ADR 0057: a daemon-driven check/download/verify/install
// cycle against this project's own GitHub releases, gated on a platform
// capability (add-on build + the firmware's /bin/install_addon
// installer).
//
// [CapabilityProbe] answers "can this platform self-update at all".
// [Checker] resolves the latest GitHub release and its add-on asset.
// [Downloader] fetches the asset, verifies its SHA256 against the
// release's checksums.txt, and atomically stages it. [Installer] hands
// the staged archive to the firmware installer in a detached session so
// it survives the daemon process the install replaces. [Updater] wires
// the four pieces behind a mutex-guarded state machine with an
// OnChange hook so REST, WebSocket and MQTT surfaces observe every
// transition. [PeriodicChecker] drives the boot-delayed, jittered
// recurring check cadence.
//
// Every I/O seam (HTTP client, filesystem stat, process spawn, wall
// clock) is injectable so the package is fully testable without a real
// CCU, network, or firmware installer.
package addonupdate
