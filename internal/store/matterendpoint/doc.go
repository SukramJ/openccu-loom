// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// Package matterendpoint owns the two Matter tables whose key is a
// Homematic source identity: `matter_endpoints` (migration 007), the
// persisted source → endpoint-id mapping, and `matter_exposures`
// (migration 009), the operator's allowlist.
//
// Both live here rather than in the bridge module because their primary
// key is the 5-tuple (central_name, device_address, channel_no, dp_kind,
// dp_key) — this daemon's data-point taxonomy and Homematic addressing.
// A channel is not a Matter concept and not every host has one, so the
// module holds an opaque [fabricendpoint.SourceKey] and calls back
// through [fabricendpoint.Store]; [Store] is that implementation, and
// [SourceKey] is the concrete key it hands over and gets back by type
// assertion.
//
// The rendering in [SourceKey.String] is load-bearing beyond this
// package: it is the sole input to every bridged endpoint's Matter
// UniqueID. Changing one byte of it re-fingerprints the whole fleet, so
// a commissioned controller keeps showing the old accessories until each
// bridged device is removed and re-added by hand. TestSourceKeyRendering
// pins it.
//
// The package takes an already-migrated *sql.DB and never creates a
// table.
package matterendpoint
