// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

// Package matterendpoint is the daemon's access to the two Matter tables
// whose key is a Homematic source identity: `matter_endpoints`, the
// persisted source → endpoint-id mapping, and `matter_exposures`
// (migration 009), the operator's allowlist.
//
// The identity is the 5-tuple (central_name, device_address, channel_no,
// dp_kind, dp_key) — this daemon's data-point taxonomy and Homematic
// addressing. A channel is not a Matter concept, so the bridge module
// holds an opaque [fabricendpoint.SourceKey]. `matter_endpoints` is
// go-fabric's endpoint/sqlitestore schema (migration 049), keyed by the
// key's rendering; [Store] runs sqlitestore under this package's key type
// and [DecodeSourceKey] turns a rendering back into a [SourceKey].
// `matter_exposures` is this package's own and keeps the 5-tuple as its
// primary key.
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
