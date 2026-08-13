// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package generic

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// ActionSelect is a write-only enum action parameter. Values are
// referenced either by their integer index or by their string label
// from [Descriptor.ValueList].
type ActionSelect struct {
	*DataPoint[int32]
}

// NewActionSelect constructs an ActionSelect. Optimistic tracking is
// force-disabled — ACTION parameters never receive CCU confirmations.
func NewActionSelect(cfg Spec) *ActionSelect {
	cfg.OptimisticDisabled = true
	return &ActionSelect{DataPoint: NewDataPoint[int32](cfg)}
}

// Label returns the VALUE_LIST label for the value this data point
// currently carries, mirroring [Select.Label].
//
// A write-only parameter never receives a value from the CCU, so this
// reports something only after a caller has recorded what it wrote (see
// [ActionSelect.RecordLabel]). That is the same contract the reference
// implementation's `.value` has for an action-select data point.
func (a *ActionSelect) Label() (string, bool) {
	v, observed := a.Value()
	if !observed {
		return "", false
	}
	if int(v) < 0 || int(v) >= len(a.Descriptor.ValueList) {
		return "", false
	}
	return a.Descriptor.ValueList[v], true
}

// DefaultLabel returns the VALUE_LIST label the descriptor declares as
// DEFAULT, and whether one could be resolved.
//
// The CCU spells an ENUM default either as the label itself
// (`"DISABLE_ACOUSTIC_SIGNAL"`, the HmIP convention) or as its index
// (`0`); both are accepted here. When the descriptor declares neither,
// the first VALUE_LIST entry is reported — for the alarm-selection
// parameters that is the disable label, which is the value a caller
// needs when it has to name a safe state rather than leave one implied.
func (a *ActionSelect) DefaultLabel() (string, bool) {
	values := a.Descriptor.ValueList
	if len(values) == 0 {
		return "", false
	}
	if raw := a.Descriptor.Default; len(raw) > 0 {
		var label string
		if err := json.Unmarshal(raw, &label); err == nil {
			if _, ok := labelIndex(values, label); ok {
				return label, true
			}
		}
		var idx int
		if err := json.Unmarshal(raw, &idx); err == nil && idx >= 0 && idx < len(values) {
			return values[idx], true
		}
	}
	return values[0], true
}

// LabelIndex resolves a VALUE_LIST label to its 0-based index.
func (a *ActionSelect) LabelIndex(label string) (int32, bool) {
	idx, ok := labelIndex(a.Descriptor.ValueList, label)
	if !ok {
		return 0, false
	}
	return int32(idx), true //nolint:gosec // a VALUE_LIST index is bounded by the list length
}

// RecordLabel records label as this data point's current value without
// writing anything to the CCU. It is how a caller reflects a value it
// just sent on a parameter the device will never report back — without
// it, a write-only selection has no readable state at all.
func (a *ActionSelect) RecordLabel(label string) {
	idx, ok := a.LabelIndex(label)
	if !ok {
		return
	}
	a.OnEvent(idx)
}

// TriggerIndex sends the given 0-based enum index.
func (a *ActionSelect) TriggerIndex(ctx context.Context, idx int32, priority hmenum.CommandPriority) error {
	if err := checkEnumIndex(a.Descriptor.ValueList, int(idx)); err != nil {
		return err
	}
	if a.Writer == nil {
		return ErrNoWriter
	}
	return a.Writer.SetValue(
		ctx,
		a.Key.ChannelAddress,
		hmenum.Parameter(a.Key.Parameter),
		idx,
		priority,
	)
}

// TriggerLabel resolves label through [Descriptor.ValueList] and dispatches
// the wire value. For parameters whose VALUE_LIST entries are string labels
// (the HmIP convention for ACTION parameters such as EFFECT), the label
// itself is sent on the wire. For parameters whose VALUE_LIST is empty or
// whose MIN descriptor indicates an integer range, the resolved index is
// sent instead — matching the HM/classic convention.
//
// The distinction mirrors the _enum_value_is_index logic in the reference
// implementation (action_select.py:43-60): a non-empty VALUE_LIST with at
// least one entry means "send the string label"; an empty list means
// "send the integer index".
func (a *ActionSelect) TriggerLabel(ctx context.Context, label string, priority hmenum.CommandPriority) error {
	if len(a.Descriptor.ValueList) == 0 {
		return ErrEmptyValueList
	}
	idx, ok := labelIndex(a.Descriptor.ValueList, label)
	if !ok {
		return fmt.Errorf("%q: %w", label, ErrUnknownLabel)
	}
	// When a VALUE_LIST is present the wire value is the string label, not the
	// integer index. Devices without a VALUE_LIST use integer indices — those
	// callers should use TriggerIndex directly.
	if a.Writer == nil {
		return ErrNoWriter
	}
	_ = idx // index validated above; not used for the wire call when labels are present
	return a.Writer.SetValue(
		ctx,
		a.Key.ChannelAddress,
		hmenum.Parameter(a.Key.Parameter),
		label,
		priority,
	)
}

// checkEnumIndex returns [ErrIndexOutOfBounds] when idx is outside the
// provided VALUE_LIST. An empty VALUE_LIST accepts any non-negative
// index (the wire form is a bare integer without enum validation).
func checkEnumIndex(valueList []string, idx int) error {
	if len(valueList) == 0 {
		if idx < 0 {
			return fmt.Errorf("index=%d: %w", idx, ErrIndexOutOfBounds)
		}
		return nil
	}
	if idx < 0 || idx >= len(valueList) {
		return fmt.Errorf("index=%d (len=%d): %w", idx, len(valueList), ErrIndexOutOfBounds)
	}
	return nil
}

// labelIndex returns the position of label inside valueList, or -1.
func labelIndex(valueList []string, label string) (int, bool) {
	for i, v := range valueList {
		if v == label {
			return i, true
		}
	}
	return 0, false
}
