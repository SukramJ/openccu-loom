// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// Package visibility decides which parameters the daemon publishes
// to the north-bound layers. Rules come from:
//
//   - a small built-in deny list (IDs, internal flags)
//   - the device-profile registry
//   - per-central un-ignore overrides (the central's
//     `visibility.un_ignore` patterns and the visibility API), loaded
//     through LoadUnIgnore
package visibility
