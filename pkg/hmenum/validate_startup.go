// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package hmenum

import "fmt"

// AllDataPointCategories enumerates every known DataPointCategory value except
// [DataPointCategoryUndefined]. The slice is used by [ValidateStartup] to verify
// that the three classification sets — [CategoryToType],
// [HubDataPointCategories], and [ValidationExemptDataPointCategories] — collectively
// cover every member.
//
// The list is hand-maintained and therefore able to drift from the enum; the
// contract suite parses the const block and fails on any category missing here,
// because a category the slice omits is invisible to every check driven from it.
var AllDataPointCategories = []DataPointCategory{
	DataPointCategoryAction,
	DataPointCategoryActionNumber,
	DataPointCategoryActionSelect,
	DataPointCategoryAlarmControlPanel,
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

// ValidationExemptDataPointCategories is the set of categories that
// [ValidateStartup] accepts without a device- or hub-classification
// entry: the undefined sentinel, and the composite event-group parent
// that shares its type with [DataPointCategoryEvent].
//
// It is a coverage bucket, not an export ban. An earlier name and doc
// declared these "must never be exposed to north-bound adapters", which
// was false in both halves: no production code reads this map at all —
// [ValidateStartup] is its only reader, and it only counts coverage —
// and event_group is deliberately exported, carrying a DataPointType
// here and published by MQTT discovery as a Home Assistant event
// component. A reader who trusted the old declaration and dropped that
// discovery case would silently remove live entities.
var ValidationExemptDataPointCategories = map[DataPointCategory]struct{}{
	DataPointCategoryUndefined:  {},
	DataPointCategoryEventGroup: {},
}

// ValidateStartup performs an exhaustive boot-time check that every
// [DataPointCategory] value (except [DataPointCategoryUndefined]) is
// covered by at least one of the three classification sets:
// - [CategoryToType] (device data points)
// - [HubDataPointCategories] (hub data points)
// - [ValidationExemptDataPointCategories] (needs neither)
//
// "At least one", not "exactly one": event_group is a member of both
// CategoryToType and the exempt set, and that overlap is intended.
//
// Returns an error listing all uncovered categories. The daemon does
// not call it: an uncovered category is a programming error rather than
// a deployment condition, so the contract suite drives it at build time
// instead of a running daemon reporting it once and carrying on.
func ValidateStartup() error {
	var missing []DataPointCategory
	for _, c := range AllDataPointCategories {
		_, inType := CategoryToType[c]
		_, inHub := HubDataPointCategories[c]
		_, inExempt := ValidationExemptDataPointCategories[c]
		if !inType && !inHub && !inExempt {
			missing = append(missing, c)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	return fmt.Errorf("hmenum: ValidateStartup: %d DataPointCategory values not covered by any set: %v", len(missing), missing)
}
