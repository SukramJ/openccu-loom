// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package generic

import (
	"context"
	"fmt"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// Select is an enum-typed data point whose values are integer indices
// into [Descriptor.ValueList]. It exposes index- and label-based
// accessors so callers can talk in whichever form they already have.
type Select struct {
	*DataPoint[int32]
}

// NewSelect constructs a Select.
func NewSelect(cfg Spec) *Select {
	s := &Select{DataPoint: NewDataPoint[int32](cfg)}
	s.RegisterServiceWithArg("select_option", "option", func(ctx context.Context, params map[string]any, priority hmenum.CommandPriority) error {
		if label, err := paramString(params, "option"); err == nil {
			return s.SetLabel(ctx, label, priority)
		}
		idx, err := paramInt32(params, "index")
		if err != nil {
			return err
		}
		return s.SetIndex(ctx, idx, priority)
	})
	return s
}

// Label returns the current value's label (via VALUE_LIST lookup) and
// whether the lookup succeeded — false means either no value observed
// yet, or the stored index is out of bounds.
func (s *Select) Label() (string, bool) {
	v, observed := s.Value()
	if !observed {
		return "", false
	}
	if int(v) < 0 || int(v) >= len(s.Descriptor.ValueList) {
		return "", false
	}
	return s.Descriptor.ValueList[v], true
}

// SetIndex sends the given 0-based index.
func (s *Select) SetIndex(ctx context.Context, idx int32, priority hmenum.CommandPriority) error {
	if !s.IsWritable() {
		return ErrNotWritable
	}
	if err := checkEnumIndex(s.Descriptor.ValueList, int(idx)); err != nil {
		return err
	}
	return s.sendAndObserve(ctx, idx, idx, priority)
}

// SetLabel resolves label in VALUE_LIST and forwards to [SetIndex].
func (s *Select) SetLabel(ctx context.Context, label string, priority hmenum.CommandPriority) error {
	if !s.IsWritable() {
		return ErrNotWritable
	}
	if len(s.Descriptor.ValueList) == 0 {
		return ErrEmptyValueList
	}
	idx, ok := labelIndex(s.Descriptor.ValueList, label)
	if !ok {
		return fmt.Errorf("%q: %w", label, ErrUnknownLabel)
	}
	return s.SetIndex(ctx, int32(idx), priority) //nolint:gosec // bounds-checked above; see #20
}
