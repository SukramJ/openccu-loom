// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package mqtt

// entity_description_ext_rules.go declares per-domain slices of
// [EntityDescriptionExtRule] that carry the additional match dimensions
// (unit, postfix, var_name_contains) not representable in the static devParam
// maps.
//
// Each slice must be sorted by descending Priority; higher-priority rules
// must appear first. [LookupExtRuleInSlice] stops at the first match.
//
// Empty slices are declared as nil so [LookupExtRuleInSlice] short- circuits
// immediately; add entries when upstream rules require these extra
// dimensions.

// sensorExtRules holds Sensor rules that use unit / postfix /
// var_name_contains constraints. Currently the upstream has no sensor
// rules that require these dimensions; the slice is nil (no entries).
var sensorExtRules []EntityDescriptionExtRule //nolint:gochecknoglobals // package-level table, same pattern as the static devParam maps

// binarySensorExtRules holds BinarySensor extended rules.
var binarySensorExtRules []EntityDescriptionExtRule //nolint:gochecknoglobals // package-level table, same pattern as the static devParam maps

// numberExtRules holds Number extended rules.
// Example (illustrative): rules that match on unit "mHz" for any
// parameter named FREQUENCY — currently covered by the static devParam
// map (numberRulesByDeviceAndParam); listed here only as a reference for
// future rules that need the extra dimensions.
var numberExtRules []EntityDescriptionExtRule //nolint:gochecknoglobals // package-level table, same pattern as the static devParam maps

// switchExtRules holds Switch extended rules.
var switchExtRules []EntityDescriptionExtRule //nolint:gochecknoglobals // package-level table, same pattern as the static devParam maps
