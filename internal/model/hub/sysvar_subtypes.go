// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package hub

import (
	"context"
	"fmt"
	"strconv"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// Ensure SysvarDpSensor satisfies HubDataPointer at compile time.
var _ HubDataPointer = (*SysvarDpSensor)(nil)

// SysvarTextMaxLength is the maximum allowed character count for a
// [SysvarDpText] value. Mirrors the constant limit enforced by
// Py:197).
const SysvarTextMaxLength = 255

// SysvarDpSwitch wraps a [*Sysvar] whose ValueType is
// [hmenum.HubValueTypeLogic] and that has a Writer configured (writable).
// It exposes boolean semantics — IsOn, TurnOn, TurnOff — modelled after
// Py).
//
// All Sysvar methods remain reachable through the embedded pointer.
type SysvarDpSwitch struct {
	*Sysvar
}

// IsOn returns true when the current sysvar value is a boolean true.
// Returns false when the value has not been observed yet or is non-bool.
func (s *SysvarDpSwitch) IsOn() bool {
	v, ok := s.Value()
	if !ok {
		return false
	}
	if v.Kind != hmtypes.ValueKindBool {
		return false
	}
	return v.Bool
}

// TurnOn sets the sysvar value to true.
func (s *SysvarDpSwitch) TurnOn(ctx context.Context) error {
	return s.Set(ctx, hmtypes.BoolValue(true))
}

// TurnOff sets the sysvar value to false.
func (s *SysvarDpSwitch) TurnOff(ctx context.Context) error {
	return s.Set(ctx, hmtypes.BoolValue(false))
}

// SysvarDpBinarySensor wraps a [*Sysvar] whose ValueType is
// [hmenum.HubValueTypeLogic] and that is read-only (no Writer).
// It exposes the read-only IsOn property. Modelled after
// Py).
//
// All Sysvar methods remain reachable through the embedded pointer.
type SysvarDpBinarySensor struct {
	*Sysvar
}

// IsOn returns true when the current sysvar value is a boolean true.
// Returns false when the value has not been observed yet or is non-bool.
func (s *SysvarDpBinarySensor) IsOn() bool {
	v, ok := s.Value()
	if !ok {
		return false
	}
	if v.Kind != hmtypes.ValueKindBool {
		return false
	}
	return v.Bool
}

// BoolValue returns the current bool value and an ok flag. Returns (false,
// false) when the value has not been observed yet or is non-bool.
func (s *SysvarDpBinarySensor) BoolValue() (value, ok bool) {
	pv, observed := s.Value()
	if !observed {
		return false, false
	}
	if pv.Kind != hmtypes.ValueKindBool {
		return false, false
	}
	return pv.Bool, true
}

// SysvarDpText wraps a [*Sysvar] whose ValueType is
// [hmenum.HubValueTypeString]. It exposes TextValue() and SetTextValue()
// With a length guard matching
// (model/hub/text.py, model/support.py:195).
//
// All Sysvar methods remain reachable through the embedded pointer.
type SysvarDpText struct {
	*Sysvar
	// MaxLength constrains the maximum accepted string length for
	// SetTextValue. When zero the [SysvarTextMaxLength] default (255)
	// Is applied.
	MaxLength int
}

// TextValue returns the current string value. Returns ("", false) when
// the value has not yet been observed or the stored value is not a string.
func (s *SysvarDpText) TextValue() (string, bool) {
	v, ok := s.Value()
	if !ok {
		return "", false
	}
	if v.Kind != hmtypes.ValueKindString {
		return "", false
	}
	return v.String, true
}

// SetTextValue writes a new string value after enforcing the maximum
// length. The effective limit is MaxLength when > 0, otherwise
// [SysvarTextMaxLength] (255).
func (s *SysvarDpText) SetTextValue(ctx context.Context, text string) error {
	limit := s.MaxLength
	if limit <= 0 {
		limit = SysvarTextMaxLength
	}
	if len(text) > limit {
		return fmt.Errorf("sysvar %q: text value length %d exceeds maximum %d", s.Name, len(text), limit)
	}
	return s.Set(ctx, hmtypes.StringValue(text))
}

// SysvarDpNumber wraps a [*Sysvar] whose ValueType is
// [hmenum.HubValueTypeFloat] or [hmenum.HubValueTypeInteger] and that
// has a Writer configured. It exposes a range-validated [SendVariable]
// method that rejects values outside [Sysvar.Min] .. [Sysvar.Max].
// Modelled after py).
//
// All Sysvar methods remain reachable through the embedded pointer.
type SysvarDpNumber struct {
	*Sysvar
}

// SendVariable writes a numeric value after validating against the
// declared min/max bounds. When Min or Max is nil the bound is skipped.
// Returns an error when the value is out of range or the underlying
// write fails.
func (n *SysvarDpNumber) SendVariable(ctx context.Context, value float64) error {
	// Snapshot the declared bounds under the lock: the hub scan rewrites Min /
	// Max in place through [Sysvar.ApplyMeta] while a write is in flight.
	m := n.Meta()
	if m.Min != nil && m.Max != nil {
		minVal := paramValueToFloat64(*m.Min)
		maxVal := paramValueToFloat64(*m.Max)
		if value < minVal || value > maxVal {
			return fmt.Errorf("sysvar %q: value %v out of range [%v, %v]", n.Name, value, minVal, maxVal)
		}
	}
	return n.Set(ctx, hmtypes.FloatValue(value))
}

// SysvarDpSelect wraps a [*Sysvar] whose ValueType is
// [hmenum.HubValueTypeList] and that has a Writer configured. It
// exposes selection semantics: [SelectValue] returns the string label
// for the current index, and [SendVariable] accepts either a numeric
// index or a string label and validates it against [Sysvar.ValueList].
// Modelled after py).
//
// All Sysvar methods remain reachable through the embedded pointer.
type SysvarDpSelect struct {
	*Sysvar
}

// SelectValue returns the string label of the currently selected entry.
// Returns ("", false) when no value has been observed or the current
// index is out of range for the declared value list.
func (s *SysvarDpSelect) SelectValue() (string, bool) {
	v, ok := s.Value()
	if !ok {
		return "", false
	}
	// The underlying sysvar stores the numeric index as Int.
	if v.Kind != hmtypes.ValueKindInt {
		return "", false
	}
	valueList := s.Meta().ValueList
	idx := v.Int
	if idx < 0 || idx >= len(valueList) {
		return "", false
	}
	return valueList[idx], true
}

// SendVariable writes a new selection. value may be:
// - int / float64: treated as a zero-based list index
// - string: looked up in ValueList; the corresponding index is written
//
// Returns an error when the index is out of range, the string is not
// found in ValueList, or the underlying write fails.
func (s *SysvarDpSelect) SendVariable(ctx context.Context, value any) error {
	valueList := s.Meta().ValueList
	if len(valueList) == 0 {
		return fmt.Errorf("sysvar %q: no value list configured", s.Name)
	}
	idx := -1
	switch v := value.(type) {
	case int:
		idx = v
	case float64:
		idx = int(v)
	case string:
		for i, label := range valueList {
			if label == v {
				idx = i
				break
			}
		}
		if idx < 0 {
			return fmt.Errorf("sysvar %q: value %q not in value list", s.Name, v)
		}
	default:
		return fmt.Errorf("sysvar %q: unsupported value type %T", s.Name, value)
	}
	if idx < 0 || idx >= len(valueList) {
		return fmt.Errorf("sysvar %q: index %d out of range [0, %d)", s.Name, idx, len(valueList))
	}
	return s.Set(ctx, hmtypes.IntValue(idx))
}

// SysvarDpSensor wraps a [*Sysvar] that is read-only and whose value
// should be presented as a human-readable label. When the sysvar's
// ValueType is [hmenum.HubValueTypeList] the numeric index stored in
// the Sysvar is resolved to the corresponding entry in ValueList via
// [SysvarDpSensor.SensorValue]. For all other value types SensorValue
// falls back to the raw stored value.
//
// This type is the Go equivalent of Python's SysvarDpSensor (with
// SensorValueMixin) (model/hub/sensor.py). The
// list→label transform mirrors SensorValueMixin._transform_sensor_value
// (model/mixins/sensor_value.py:60-61): when a value_list is provided
// and the numeric index maps to a valid entry, that string entry is
// returned.
//
// SysvarDpSensor is selected by [WrapSysvar] for read-only LIST sysvars
// (Writer == nil) that are not "extended" (IsExtended == false). For
// extended list sysvars WrapSysvar still falls back to the base Sysvar.
type SysvarDpSensor struct {
	*Sysvar
}

// SensorValue applies the list→label transform. When the sysvar's
// ValueType is LIST and its current value is a valid integer index into
// ValueList, the corresponding string label is returned. Otherwise the
// raw [hmtypes.ParamValue] and observed flag from [Sysvar.Value] are
// returned unchanged.
//
// Return signature: (label string, raw hmtypes.ParamValue, ok bool).
// When ValueType is LIST and mapping succeeds, label is the string
// entry and raw is the zero value. When mapping is not applicable or
// fails, label is "" and raw carries the actual stored value.
func (s *SysvarDpSensor) SensorValue() (label string, observed bool) {
	v, ok := s.Value()
	if !ok {
		return "", false
	}
	m := s.Meta()
	if m.ValueType == hmenum.HubValueTypeList && len(m.ValueList) > 0 {
		if v.Kind == hmtypes.ValueKindInt {
			idx := v.Int
			if idx >= 0 && idx < len(m.ValueList) {
				return m.ValueList[idx], true
			}
		}
	}
	// For non-list types or when the index is out of range, render
	// the raw value as a string for sensor display purposes.
	switch v.Kind {
	case hmtypes.ValueKindString:
		return v.String, true
	case hmtypes.ValueKindBool:
		if v.Bool {
			return "true", true
		}
		return "false", true
	case hmtypes.ValueKindInt:
		return strconv.Itoa(v.Int), true
	case hmtypes.ValueKindFloat:
		return fmt.Sprintf("%g", v.Float), true
	default:
		return "", true
	}
}

// WrapSysvar inspects sv and returns the narrowest typed wrapper that
// matches its ValueType and writability:
//
// - Logic + Writer != nil → [*SysvarDpSwitch]
// - Logic + Writer == nil → [*SysvarDpBinarySensor]
// - String → [*SysvarDpText]
// - List + Writer != nil → [*SysvarDpSelect]
// - List + Writer == nil + !IsExtended → [*SysvarDpSensor] (label lookup)
// - Float/Integer + Writer != nil → [*SysvarDpNumber]
// - everything else → sv unchanged (returned as [HubDataPointer])
//
// The function mirrors
// the concrete DataPoint subclass based on data_type and writability
// (model/hub/hub.py).
func WrapSysvar(sv *Sysvar) HubDataPointer {
	m := sv.Meta()
	switch m.ValueType {
	case hmenum.HubValueTypeLogic, hmenum.HubValueTypeAlarm:
		if sv.Writable() {
			return &SysvarDpSwitch{Sysvar: sv}
		}
		return &SysvarDpBinarySensor{Sysvar: sv}
	case hmenum.HubValueTypeString:
		return &SysvarDpText{Sysvar: sv}
	case hmenum.HubValueTypeList:
		if sv.Writable() {
			return &SysvarDpSelect{Sysvar: sv}
		}
		// Read-only list sysvar: use SysvarDpSensor for label transform.
		// Extended sysvars are returned as-is (Python _is_extended=True
		// subclasses behave differently; we keep the base until a
		// concrete extended type is defined).
		if !m.IsExtended {
			return &SysvarDpSensor{Sysvar: sv}
		}
		return sv
	case hmenum.HubValueTypeFloat, hmenum.HubValueTypeInteger:
		if sv.Writable() {
			return &SysvarDpNumber{Sysvar: sv}
		}
		return sv
	default:
		return sv
	}
}

// paramValueToFloat64 converts a [hmtypes.ParamValue] to float64.
// Int and Float kinds are supported; other kinds return 0.
func paramValueToFloat64(v hmtypes.ParamValue) float64 {
	switch v.Kind {
	case hmtypes.ValueKindFloat:
		return v.Float
	case hmtypes.ValueKindInt:
		return float64(v.Int)
	default:
		return 0
	}
}
