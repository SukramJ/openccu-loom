// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package generic

import (
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
)

// ResolvedKind names the concrete generic data-point shape the
// device-pipeline should construct for a given parameter.
type ResolvedKind string

// ResolvedKind values. The string form is stable for telemetry.
const (
	KindUnknown       ResolvedKind = ""
	KindSwitch        ResolvedKind = "switch"
	KindBinarySensor  ResolvedKind = "binary_sensor"
	KindSensor        ResolvedKind = "sensor"
	KindButton        ResolvedKind = "button"
	KindAction        ResolvedKind = "action"
	KindActionBoolean ResolvedKind = "action_boolean"
	KindActionFloat   ResolvedKind = "action_float"
	KindActionInteger ResolvedKind = "action_integer"
	KindActionString  ResolvedKind = "action_string"
	KindActionSelect  ResolvedKind = "action_select"
	KindNumberFloat   ResolvedKind = "number_float"
	KindNumberInteger ResolvedKind = "number_integer"
	KindSelect        ResolvedKind = "select"
	KindText          ResolvedKind = "text"
)

// ResolveInput packs the parameters [ResolveDataPointKind] consumes.
// The struct keeps the call-site readable when several optional
// hints are passed.
type ResolveInput struct {
	// Parameter is the wire-level parameter name (e.g. "LEVEL",
	// "PRESS_SHORT").
	Parameter string

	// Descriptor carries Type, Operations, ValueList from the
	// CCU paramset description.
	Descriptor hmproto.ParameterData

	// IsClickEvent indicates whether the parameter belongs to the
	// click-events family (PRESS_SHORT, PRESS_LONG, …). Mirrors
	// `parameter in CLICK_EVENTS` in Python.
	IsClickEvent bool

	// IsButtonAction indicates whether a write-only ACTION should be
	// rendered as a Button (RESET_MOTION, RESET_PRESENCE, virtual-
	// remote channels). Mirrors `_BUTTON_ACTIONS` in Python.
	IsButtonAction bool

	// IsBinarySensor indicates that the parameter should be wrapped
	// as a BinarySensor (callers detect this from the descriptor's
	// VALUE_LIST when it matches the [True, False] / on-off shape).
	IsBinarySensor bool
}

// ResolveDataPointKind picks the concrete generic data-point shape for a
// parameter. Returns [KindUnknown] when no shape applies.
func ResolveDataPointKind(in ResolveInput) ResolvedKind {
	ops := in.Descriptor.Operations
	if ops.IsWritable() {
		return resolveWritable(in)
	}
	return resolveReadonly(in)
}

func resolveReadonly(in ResolveInput) ResolvedKind {
	if in.IsClickEvent {
		return KindUnknown // click events are surfaced via the event subsystem
	}
	if in.IsBinarySensor {
		return KindBinarySensor
	}
	return KindSensor
}

func resolveWritable(in ResolveInput) ResolvedKind {
	if in.Descriptor.Type == hmenum.ParameterTypeAction {
		return resolveAction(in)
	}
	// Write-only non-ACTION parameters use the dedicated Action* shapes
	// so the DP knows it must not engage the optimistic tracker.
	writeOnly := !in.Descriptor.Operations.IsReadable() && !in.Descriptor.Operations.IsEvent()
	if writeOnly {
		if len(in.Descriptor.ValueList) > 0 {
			return KindActionSelect
		}
		switch in.Descriptor.Type { //nolint:exhaustive // Action/Dummy/Empty types fall through to KindAction below
		case hmenum.ParameterTypeFloat:
			return KindActionFloat
		case hmenum.ParameterTypeInteger:
			return KindActionInteger
		case hmenum.ParameterTypeBool:
			return KindActionBoolean
		case hmenum.ParameterTypeString:
			return KindActionString
		}
		return KindAction
	}
	// Read+Write+optional Event — the standard shapes.
	switch in.Descriptor.Type { //nolint:exhaustive // Action/Dummy/Empty types fall through to KindUnknown below
	case hmenum.ParameterTypeBool:
		return KindSwitch
	case hmenum.ParameterTypeEnum:
		return KindSelect
	case hmenum.ParameterTypeFloat:
		return KindNumberFloat
	case hmenum.ParameterTypeInteger:
		return KindNumberInteger
	case hmenum.ParameterTypeString:
		return KindText
	}
	return KindUnknown
}

// kindToCategory maps the fine-grained generic shape to the fine-grained
// [DataPointCategory] consumed by north-bound adapters.
var kindToCategory = map[ResolvedKind]hmenum.DataPointCategory{
	KindSwitch:        hmenum.DataPointCategorySwitch,
	KindBinarySensor:  hmenum.DataPointCategoryBinarySensor,
	KindSensor:        hmenum.DataPointCategorySensor,
	KindButton:        hmenum.DataPointCategoryButton,
	KindAction:        hmenum.DataPointCategoryAction,
	KindActionBoolean: hmenum.DataPointCategoryAction,
	KindActionFloat:   hmenum.DataPointCategoryActionNumber,
	KindActionInteger: hmenum.DataPointCategoryActionNumber,
	KindActionString:  hmenum.DataPointCategoryAction,
	KindActionSelect:  hmenum.DataPointCategoryActionSelect,
	KindNumberFloat:   hmenum.DataPointCategoryNumber,
	KindNumberInteger: hmenum.DataPointCategoryNumber,
	KindSelect:        hmenum.DataPointCategorySelect,
	KindText:          hmenum.DataPointCategoryText,
}

// Category returns the [DataPointCategory] a [ResolvedKind] maps to. Unknown
// kinds resolve to [DataPointCategoryUndefined].
func (k ResolvedKind) Category() hmenum.DataPointCategory {
	if c, ok := kindToCategory[k]; ok {
		return c
	}
	return hmenum.DataPointCategoryUndefined
}

// DataPointType returns the consumer-facing functional type for the resolved
// kind.
//
// Returns the zero value (empty string) when the kind has no canonical type
// (KindUnknown or any non-mapped category).
func (k ResolvedKind) DataPointType() hmenum.DataPointType {
	cat := k.Category()
	if cat == hmenum.DataPointCategoryUndefined {
		return ""
	}
	if t, ok := hmenum.CategoryToType[cat]; ok {
		return t
	}
	return ""
}

// IsAction reports whether the resolved kind falls into the
// no-optimistic-update group.
func (k ResolvedKind) IsAction() bool {
	return k.Category().IsAction()
}

func resolveAction(in ResolveInput) ResolvedKind {
	writeOnly := !in.Descriptor.Operations.IsReadable() && !in.Descriptor.Operations.IsEvent()
	if writeOnly {
		if in.IsButtonAction {
			return KindButton
		}
		if len(in.Descriptor.ValueList) > 0 {
			return KindActionSelect
		}
		return KindAction
	}
	if in.IsClickEvent {
		return KindButton
	}
	// Read+Write ACTION is treated like a switch in Python.
	return KindSwitch
}
