// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package generic

import (
	"context"
	"fmt"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// Select is an enum-typed data point whose values are integer indices
// into [Descriptor.ValueList]. It exposes index- and label-based
// accessors so callers can talk in whichever form they already have.
type Select struct {
	*DataPoint[int32]

	// matterModeVersion is the per-cluster DataVersion counter of the
	// ModeSelect projection in `select_matter.go`. It lives on the data
	// point rather than inside the cluster server because the server is
	// rebuilt on every topology assembly and every eligibility query,
	// and a counter that restarted with each rebuild would tell a
	// subscribed controller its cached mode had changed when nothing did.
	matterModeVersion hmtypes.DataVersionTracker
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
//
// An index-typed caller always puts the index on the wire, whichever domain
// the descriptor declares — the reference implementation makes the same
// allowance for a numeric argument (select.py:39-41).
func (s *Select) SetIndex(ctx context.Context, idx int32, priority hmenum.CommandPriority) error {
	if !s.IsWritable() {
		return ErrNotWritable
	}
	if err := checkEnumIndex(s.Descriptor.ValueList, int(idx)); err != nil {
		return err
	}
	return s.sendAndObserve(ctx, idx, idx, priority)
}

// SetLabel resolves label in VALUE_LIST and sends it in the form the
// descriptor declares — see [DataPoint.EnumWireValue] for who decides and on
// what authority. The observed value stays the index either way, because that
// is what this data point's type is.
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
	typed := int32(idx) //nolint:gosec // a VALUE_LIST index is bounded by the list length
	return s.sendAndObserve(ctx, s.EnumWireValue(label, typed), typed, priority)
}
