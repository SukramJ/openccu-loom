// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// Package visibility decides which parameters the daemon publishes
// to the north-bound layers. Rules come from:
//
//   - a small built-in deny list (IDs, internal flags)
//   - the device-profile registry
//   - user overrides (future: loaded from SQLite)
//
// The MVP ships the built-in rules only; the override store is a hook
// for future user-configurable overrides.
package visibility
