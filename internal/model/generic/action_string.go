// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package generic

import (
	"context"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// ActionString is a write-only string action parameter.
type ActionString struct {
	*DataPoint[string]
}

// NewActionString constructs an ActionString. Optimistic tracking is
// force-disabled — ACTION parameters never receive CCU confirmations.
func NewActionString(cfg Spec) *ActionString {
	cfg.OptimisticDisabled = true
	return &ActionString{DataPoint: NewDataPoint[string](cfg)}
}

// Trigger sends v on the wire.
func (a *ActionString) Trigger(ctx context.Context, v string, priority hmenum.CommandPriority) error {
	if a.Writer == nil {
		return ErrNoWriter
	}
	if !a.IsWritable() && a.Descriptor.Type != hmenum.ParameterTypeAction {
		return ErrNotWritable
	}
	return a.Writer.SetValue(
		ctx,
		a.Key.ChannelAddress,
		hmenum.Parameter(a.Key.Parameter),
		v,
		priority,
	)
}
