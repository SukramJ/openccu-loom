// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package generic

import (
	"github.com/SukramJ/openccu-loom/internal/parameter"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// quantity.go — the model's read chain onto the parameter-classification
// tables.
//
// The tables themselves live in internal/parameter (metadata.go) and are
// the single home for the (device model, parameter, unit) → quantity /
// value-behavior rule: that package already owns wire-parameter semantics
// and is importable from both internal/model and internal/north without a
// cycle. This file must never grow a table of its own — a second copy
// silently answers differently from the one the north-bound planes read.
// tests/contract/quantity_metadata_single_source_test.go pins that.

// Quantity returns the semantic [hmenum.Quantity] this data point reports.
//
//  1. `Category() == BinarySensor` → device+param binary-sensor override →
//     param-only binary-sensor.
//  2. Sensor device+param override (per-model overlay).
//  3. Sensor param-only classification.
//  4. Unit fallback (raw `Descriptor.Unit`, after CleanupUnit).
//
// The resolution is field-wise, not record-wise: a stage that classifies
// the value behavior but not the quantity (HM-CC-RT-DN.VALVE_STATE) does
// not stop the search for a quantity.
//
// The device-aware lookups consult `Spec.DeviceModel` set by the pipeline
// at construction time. When `DeviceModel` is empty (test fixtures, virtual
// DPs) the chain degrades to the param-only path.
func (d *DataPoint[T]) Quantity() hmenum.Quantity {
	param := d.Parameter()
	name := string(param)
	model := d.DeviceModel

	if d.Category() == hmenum.DataPointCategoryBinarySensor {
		return parameter.BinarySensorQuantityFor(model, name)
	}

	if q := parameter.MetadataByDeviceAndParam(model, name).Quantity; q != hmenum.QuantityNone {
		return q
	}
	if q := parameter.MetadataByParam(name).Quantity; q != hmenum.QuantityNone {
		return q
	}
	// Unit-fallback as last resort. Use the cleaned unit so spelling
	// quirks ("Lux" vs "lx") don't break the lookup.
	if cleaned := CleanupUnit(param, d.Descriptor.Unit); cleaned != "" {
		if q := parameter.MetadataByUnit(cleaned).Quantity; q != hmenum.QuantityNone {
			return q
		}
	}
	return hmenum.QuantityNone
}

// ValueBehavior returns the value-behavior classification for this data point
// using a three-stage read chain:
//
//  1. Device+parameter override — per-model specialisation, e.g.
//     HM-CC-RT-DN.VALVE_STATE → INSTANTANEOUS.
//  2. Parameter-only classification. A parameter that is classified at all
//     stops the chain here, even when its behavior is NONE (LOCK_STATE is
//     an enum reading, not a measurement) — otherwise the unit fallback
//     would promote it to a measurement behind the classification's back.
//  3. Unit fallback — last resort using the descriptor's unit string after
//     [CleanupUnit].
//
// Returns [hmenum.ValueBehaviorNone] when none of the stages match.
func (d *DataPoint[T]) ValueBehavior() hmenum.ValueBehavior {
	param := d.Parameter()
	name := string(param)
	model := d.DeviceModel

	if b := parameter.MetadataByDeviceAndParam(model, name).ValueBehavior; b != hmenum.ValueBehaviorNone {
		return b
	}
	if md := parameter.MetadataByParam(name); md != (parameter.Metadata{}) {
		return md.ValueBehavior
	}
	if cleaned := CleanupUnit(param, d.Descriptor.Unit); cleaned != "" {
		if b := parameter.MetadataByUnit(cleaned).ValueBehavior; b != hmenum.ValueBehaviorNone {
			return b
		}
	}
	return hmenum.ValueBehaviorNone
}
