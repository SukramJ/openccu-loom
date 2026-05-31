// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package hmenum

// DataPointCategory is the fine-grained classification used by our
// domain model; north-bound adapters collapse it into [DataPointType].
type DataPointCategory string

// DataPointCategory values.
const (
	DataPointCategoryAction          DataPointCategory = "action"
	DataPointCategoryActionNumber    DataPointCategory = "action_number"
	DataPointCategoryActionSelect    DataPointCategory = "action_select"
	DataPointCategoryBinarySensor    DataPointCategory = "binary_sensor"
	DataPointCategoryButton          DataPointCategory = "button"
	DataPointCategoryClimate         DataPointCategory = "climate"
	DataPointCategoryCover           DataPointCategory = "cover"
	DataPointCategoryEvent           DataPointCategory = "event"
	DataPointCategoryEventGroup      DataPointCategory = "event_group"
	DataPointCategoryHubBinarySensor DataPointCategory = "hub_binary_sensor"
	DataPointCategoryHubButton       DataPointCategory = "hub_button"
	DataPointCategoryHubNumber       DataPointCategory = "hub_number"
	DataPointCategoryHubSelect       DataPointCategory = "hub_select"
	DataPointCategoryHubSensor       DataPointCategory = "hub_sensor"
	DataPointCategoryHubSwitch       DataPointCategory = "hub_switch"
	DataPointCategoryHubText         DataPointCategory = "hub_text"
	DataPointCategoryHubUpdate       DataPointCategory = "hub_update"
	DataPointCategoryLight           DataPointCategory = "light"
	DataPointCategoryLock            DataPointCategory = "lock"
	DataPointCategoryNumber          DataPointCategory = "number"
	DataPointCategoryScheduleSwitch  DataPointCategory = "schedule_switch"
	DataPointCategorySelect          DataPointCategory = "select"
	DataPointCategorySensor          DataPointCategory = "sensor"
	DataPointCategorySiren           DataPointCategory = "siren"
	DataPointCategorySwitch          DataPointCategory = "switch"
	DataPointCategoryText            DataPointCategory = "text"
	DataPointCategoryTextDisplay     DataPointCategory = "text_display"
	DataPointCategoryUndefined       DataPointCategory = "undefined"
	DataPointCategoryUpdate          DataPointCategory = "update"
	DataPointCategoryValve           DataPointCategory = "valve"
	DataPointCategoryWeekProfile     DataPointCategory = "week_profile"
)

// String returns the wire representation.
func (c DataPointCategory) String() string { return string(c) }

// DataPointType is the canonical functional type consumed by north-bound
// adapters (MQTT, REST, UI).
type DataPointType string

// DataPointType values.
const (
	DataPointTypeBinarySensor DataPointType = "binary_sensor"
	DataPointTypeButton       DataPointType = "button"
	DataPointTypeClimate      DataPointType = "climate"
	DataPointTypeCover        DataPointType = "cover"
	DataPointTypeEvent        DataPointType = "event"
	DataPointTypeLight        DataPointType = "light"
	DataPointTypeLock         DataPointType = "lock"
	DataPointTypeNumber       DataPointType = "number"
	DataPointTypeSelect       DataPointType = "select"
	DataPointTypeSensor       DataPointType = "sensor"
	DataPointTypeSiren        DataPointType = "siren"
	DataPointTypeSwitch       DataPointType = "switch"
	DataPointTypeText         DataPointType = "text"
	DataPointTypeUpdate       DataPointType = "update"
	DataPointTypeValve        DataPointType = "valve"
)

// String returns the wire representation.
func (t DataPointType) String() string { return string(t) }

// CategoryToType is the authoritative mapping from fine-grained category
// to consumer-facing functional type.
var CategoryToType = map[DataPointCategory]DataPointType{
	DataPointCategoryAction:          DataPointTypeButton,
	DataPointCategoryActionNumber:    DataPointTypeNumber,
	DataPointCategoryActionSelect:    DataPointTypeSelect,
	DataPointCategoryBinarySensor:    DataPointTypeBinarySensor,
	DataPointCategoryButton:          DataPointTypeButton,
	DataPointCategoryClimate:         DataPointTypeClimate,
	DataPointCategoryCover:           DataPointTypeCover,
	DataPointCategoryEvent:           DataPointTypeEvent,
	DataPointCategoryEventGroup:      DataPointTypeEvent,
	DataPointCategoryHubBinarySensor: DataPointTypeBinarySensor,
	DataPointCategoryHubButton:       DataPointTypeButton,
	DataPointCategoryHubNumber:       DataPointTypeNumber,
	DataPointCategoryHubSelect:       DataPointTypeSelect,
	DataPointCategoryHubSensor:       DataPointTypeSensor,
	DataPointCategoryHubSwitch:       DataPointTypeSwitch,
	DataPointCategoryHubText:         DataPointTypeText,
	DataPointCategoryHubUpdate:       DataPointTypeUpdate,
	DataPointCategoryLight:           DataPointTypeLight,
	DataPointCategoryLock:            DataPointTypeLock,
	DataPointCategoryNumber:          DataPointTypeNumber,
	DataPointCategoryScheduleSwitch:  DataPointTypeSwitch,
	DataPointCategorySelect:          DataPointTypeSelect,
	DataPointCategorySensor:          DataPointTypeSensor,
	DataPointCategorySiren:           DataPointTypeSiren,
	DataPointCategorySwitch:          DataPointTypeSwitch,
	DataPointCategoryText:            DataPointTypeText,
	DataPointCategoryTextDisplay:     DataPointTypeText,
	DataPointCategoryUpdate:          DataPointTypeUpdate,
	DataPointCategoryValve:           DataPointTypeValve,
	DataPointCategoryWeekProfile:     DataPointTypeSensor,
}

// ActionDataPointCategories enumerates the categories whose data points
// never receive a CCU event confirmation. Optimistic updates must be
// skipped for them to avoid spurious timeout rollbacks.
var ActionDataPointCategories = map[DataPointCategory]struct{}{
	DataPointCategoryAction:       {},
	DataPointCategoryActionNumber: {},
	DataPointCategoryActionSelect: {},
	DataPointCategoryButton:       {},
}

// IsAction reports whether c falls into the no-optimistic-update set.
func (c DataPointCategory) IsAction() bool {
	_, ok := ActionDataPointCategories[c]
	return ok
}

// DataPointUsage classifies a data point's role relative to a custom
// device profile.
type DataPointUsage string

// DataPointUsage values.
//
// openccu-loom carries a seventh value, `Ignored`, that splits two
// populations which are both `Visible() == false` but have very
// different semantics:
//
//   - `Ignored` — the DP exists but the visibility gate suppressed
//     it via a static rule (`IGNORED_PARAMETERS`,
//     `HIDDEN_PARAMETERS`, wildcard regex, channel-operation-mode
//     mask). This is the population the un-ignore feature operates
//     on; a matching un-ignore entry clears the mark so the DP
//     surfaces normally.
//   - `NoCreate` — the generic parameter DP exists but is consumed
//     by an aggregating parent DP (Custom / Combined / Week-Profile)
//     and therefore should not surface as a duplicate standalone
//     entity. Not user-toggleable.
//
// See docs/adr/0015-datapoint-usage-ignored.md for the rationale.
const (
	DataPointUsageCDPPrimary   DataPointUsage = "ce_primary"
	DataPointUsageCDPSecondary DataPointUsage = "ce_secondary"
	DataPointUsageCDPVisible   DataPointUsage = "ce_visible"
	DataPointUsageDataPoint    DataPointUsage = "data_point"
	DataPointUsageEvent        DataPointUsage = "event"
	DataPointUsageIgnored      DataPointUsage = "ignored"
	DataPointUsageNoCreate     DataPointUsage = "no_create"
)

// String returns the wire representation.
func (u DataPointUsage) String() string { return string(u) }

// ValueBehavior moved to quantity.go — kept side-by-side with
// Quantity since the two are co-resolved by
// `internal/parameter.MetadataFor`.
