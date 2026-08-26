// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package generic

import (
	"context"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// Float is a writable 64-bit floating-point data point. Range
// validation runs on [Float.Set]; values outside [MIN, MAX] are
// rejected with [ErrOutOfRange] before any wire activity.
type Float struct {
	*DataPoint[float64]
}

// NewFloat constructs a Float.
func NewFloat(cfg Spec) *Float {
	f := &Float{DataPoint: NewDataPoint[float64](cfg)}
	f.RegisterService("set_value", func(ctx context.Context, params map[string]any, priority hmenum.CommandPriority) error {
		v, err := paramFloat64(params, "value")
		if err != nil {
			return err
		}
		return f.Set(ctx, v, priority)
	})
	return f
}

// DescriptorRange returns the (min, max) bounds from the parameter
// descriptor. ok is true only when both MIN and MAX are present in the
// descriptor. Used by callers that need the CCU-advertised setpoint
// range (e.g. the climate MinTemp / MaxTemp read chain).
func (f *Float) DescriptorRange() (lo, hi float64, ok bool) {
	loV, loOK := parseFloat(f.Descriptor.Min)
	hiV, hiOK := parseFloat(f.Descriptor.Max)
	if !loOK || !hiOK {
		return 0, 0, false
	}
	return loV, hiV, true
}

// DescriptorMin returns the wire-descriptor's MIN bound. ok=true when MIN is
// present, regardless of whether MAX is set.
func (f *Float) DescriptorMin() (float64, bool) {
	return parseFloat(f.Descriptor.Min)
}

// DescriptorMax returns the wire-descriptor's MAX bound. ok=true when
// MAX is present, regardless of whether MIN is set.
func (f *Float) DescriptorMax() (float64, bool) {
	return parseFloat(f.Descriptor.Max)
}

// Set sends v after validating range and writability.
func (f *Float) Set(ctx context.Context, v float64, priority hmenum.CommandPriority) error {
	if !f.IsWritable() {
		return ErrNotWritable
	}
	if err := checkFloatBounds(f.Descriptor, v); err != nil {
		return err
	}
	return f.sendAndObserve(ctx, v, v, priority)
}

// Integer is a writable 32-bit integer data point.
type Integer struct {
	*DataPoint[int32]
}

// NewInteger constructs an Integer.
func NewInteger(cfg Spec) *Integer {
	i := &Integer{DataPoint: NewDataPoint[int32](cfg)}
	i.RegisterService("set_value", func(ctx context.Context, params map[string]any, priority hmenum.CommandPriority) error {
		v, err := paramInt32(params, "value")
		if err != nil {
			return err
		}
		return i.Set(ctx, v, priority)
	})
	return i
}

// Set sends v after validating range and writability.
func (i *Integer) Set(ctx context.Context, v int32, priority hmenum.CommandPriority) error {
	if !i.IsWritable() {
		return ErrNotWritable
	}
	if err := checkIntBounds(i.Descriptor, int64(v)); err != nil {
		return err
	}
	return i.sendAndObserve(ctx, v, v, priority)
}
