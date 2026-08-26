// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package generic

import (
	"context"
	"fmt"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// ActionFloat is a write-only float action parameter. Values are
// range-checked against [Descriptor.Min] / [Descriptor.Max]; special
// labels from [Descriptor.Special] are not supported on the action
// path.
type ActionFloat struct {
	*DataPoint[float64]
}

// NewActionFloat constructs an ActionFloat. Optimistic tracking is
// force-disabled — ACTION parameters never receive CCU confirmations.
func NewActionFloat(cfg Spec) *ActionFloat {
	cfg.OptimisticDisabled = true
	return &ActionFloat{DataPoint: NewDataPoint[float64](cfg)}
}

// Trigger sends v after range validation.
func (a *ActionFloat) Trigger(ctx context.Context, v float64, priority hmenum.CommandPriority) error {
	if err := checkFloatBounds(a.Descriptor, v); err != nil {
		return err
	}
	if a.Writer == nil {
		return ErrNoWriter
	}
	return a.Writer.SetValue(
		ctx,
		a.Key.ChannelAddress,
		hmenum.Parameter(a.Key.Parameter),
		v,
		priority,
	)
}

// ActionInteger is a write-only int32 action parameter.
type ActionInteger struct {
	*DataPoint[int32]
}

// NewActionInteger constructs an ActionInteger. Optimistic tracking is
// force-disabled — ACTION parameters never receive CCU confirmations.
func NewActionInteger(cfg Spec) *ActionInteger {
	cfg.OptimisticDisabled = true
	return &ActionInteger{DataPoint: NewDataPoint[int32](cfg)}
}

// Trigger sends v after range validation.
func (a *ActionInteger) Trigger(ctx context.Context, v int32, priority hmenum.CommandPriority) error {
	if err := checkIntBounds(a.Descriptor, int64(v)); err != nil {
		return err
	}
	if a.Writer == nil {
		return ErrNoWriter
	}
	return a.Writer.SetValue(
		ctx,
		a.Key.ChannelAddress,
		hmenum.Parameter(a.Key.Parameter),
		v,
		priority,
	)
}

// wrapRangeError turns a bounds violation into ErrOutOfRange with a
// human-readable detail for logs.
func wrapRangeError(kind string, v, lo, hi any) error {
	return fmt.Errorf("%s=%v not in [%v, %v]: %w", kind, v, lo, hi, ErrOutOfRange)
}
