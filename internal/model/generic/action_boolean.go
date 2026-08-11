// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package generic

import (
	"context"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// ActionBoolean is a write-only bool action parameter. Unlike
// [Switch], no CCU confirmation is expected.
type ActionBoolean struct {
	*DataPoint[bool]
}

// NewActionBoolean constructs an ActionBoolean. Optimistic tracking is
// force-disabled — ACTION parameters are write-only triggers and the CCU
// never echoes a confirmation.
func NewActionBoolean(cfg Spec) *ActionBoolean {
	cfg.OptimisticDisabled = true
	return &ActionBoolean{DataPoint: NewDataPoint[bool](cfg)}
}

// Trigger sends v without recording optimistic state (ACTION never
// receives a confirmation).
func (a *ActionBoolean) Trigger(ctx context.Context, v bool, priority hmenum.CommandPriority) error {
	if !a.IsWritable() && a.Descriptor.Type != hmenum.ParameterTypeAction {
		return ErrNotWritable
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

// FireAction implements [ActionTrigger]. `true` is the trigger value for
// a write-only boolean: the parameter carries no state a caller could
// toggle, so the only meaningful write is the firing one.
func (a *ActionBoolean) FireAction(ctx context.Context, priority hmenum.CommandPriority) error {
	return a.Trigger(ctx, true, priority)
}
