// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// Package datapoint hosts the shared parent layer for every data
// point family in the daemon: generic / parameter, custom, calculated,
// combined, week-profile, hub-level entities (sysvar / program), and
// any future DP family added to the model.
//
// The package mirrors
// (model/data_point.py) — the common identity, visibility, and
// publish-update surface every concrete data point shares regardless
// of its wire-level shape. It is intentionally minimal: it carries the
// few fields/methods all families agree on, and stays out of the
// transport / observation / value-typing concerns each family handles
// in its own package.
//
// The package provides the [BaseDataPoint] interface and the
// [BaseDataPointFields] embedding struct. Existing data-point families
// (hub.HubDataPoint, generic.DataPoint[T], custom.*, calculated.*,
// combined.*, weekprofile.ProfileDataPoint) embed [BaseDataPointFields]
// for shared identity, visibility, and publish-update behavior.
package datapoint
