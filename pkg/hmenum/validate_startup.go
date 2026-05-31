// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package hmenum

import "fmt"

// AllDataPointCategories enumerates every known DataPointCategory value. The
// slice is used by [ValidateStartup] to verify that the three classification
// sets — [CategoryToType], [HubDataPointCategories], and
// [BlockedDataPointCategories] — collectively cover every member except
// [DataPointCategoryUndefined].
var AllDataPointCategories = []DataPointCategory{
	DataPointCategoryAction,
	DataPointCategoryActionNumber,
	DataPointCategoryActionSelect,
	DataPointCategoryBinarySensor,
	DataPointCategoryButton,
	DataPointCategoryClimate,
	DataPointCategoryCover,
	DataPointCategoryEvent,
	DataPointCategoryEventGroup,
	DataPointCategoryHubBinarySensor,
	DataPointCategoryHubButton,
	DataPointCategoryHubNumber,
	DataPointCategoryHubSelect,
	DataPointCategoryHubSensor,
	DataPointCategoryHubSwitch,
	DataPointCategoryHubText,
	DataPointCategoryHubUpdate,
	DataPointCategoryLight,
	DataPointCategoryLock,
	DataPointCategoryNumber,
	DataPointCategoryScheduleSwitch,
	DataPointCategorySelect,
	DataPointCategorySensor,
	DataPointCategorySiren,
	DataPointCategorySwitch,
	DataPointCategoryText,
	DataPointCategoryTextDisplay,
	DataPointCategoryUpdate,
	DataPointCategoryValve,
	DataPointCategoryWeekProfile,
}

// HubDataPointCategories is the set of categories that belong to hub
// entities (sysvars, programs). These are backed by [hub.HubDataPoint]
// rather than [generic.DataPoint].
var HubDataPointCategories = map[DataPointCategory]struct{}{
	DataPointCategoryHubBinarySensor: {},
	DataPointCategoryHubButton:       {},
	DataPointCategoryHubNumber:       {},
	DataPointCategoryHubSelect:       {},
	DataPointCategoryHubSensor:       {},
	DataPointCategoryHubSwitch:       {},
	DataPointCategoryHubText:         {},
	DataPointCategoryHubUpdate:       {},
}

// BlockedDataPointCategories is the set of categories that must never
// be exposed to north-bound adapters. They are internal model nodes
// only (e.g. composite "parent" entries for event groups or the
// undefined sentinel).
var BlockedDataPointCategories = map[DataPointCategory]struct{}{
	DataPointCategoryUndefined:  {},
	DataPointCategoryEventGroup: {},
}

// ValidateStartup performs an exhaustive boot-time check that every
// [DataPointCategory] value (except [DataPointCategoryUndefined]) is
// covered by exactly one of the three classification sets:
// - [CategoryToType] (device data points)
// - [HubDataPointCategories] (hub data points)
// - [BlockedDataPointCategories] (blocked from north-bound export)
//
// Returns an error listing all uncovered categories. Call this once
// from the daemon entry point to catch stale enum/set divergences at
// startup rather than at runtime.
func ValidateStartup() error {
	var missing []DataPointCategory
	for _, c := range AllDataPointCategories {
		_, inType := CategoryToType[c]
		_, inHub := HubDataPointCategories[c]
		_, inBlocked := BlockedDataPointCategories[c]
		if !inType && !inHub && !inBlocked {
			missing = append(missing, c)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	return fmt.Errorf("hmenum: ValidateStartup: %d DataPointCategory values not covered by any set: %v", len(missing), missing)
}
