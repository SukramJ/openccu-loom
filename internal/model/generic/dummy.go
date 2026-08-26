// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package generic

// Dummy is a placeholder data point for CCU parameters the daemon
// deliberately ignores (deprecated inputs, internal scratch values).
// It stores the last observed wire value without any validation or
// outbound behaviour so coordinators can still route events without
// tripping "unknown parameter" warnings.
type Dummy struct {
	*DataPoint[any]
}

// NewDummy constructs a Dummy.
func NewDummy(cfg Spec) *Dummy {
	return &Dummy{DataPoint: NewDataPoint[any](cfg)}
}
