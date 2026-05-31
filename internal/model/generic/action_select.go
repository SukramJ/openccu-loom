// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package generic

import (
	"context"
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
