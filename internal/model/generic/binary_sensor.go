// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package generic

// BinarySensor is a bool-typed read-only data point. It has no Set
// method — state arrives exclusively via [DataPoint.OnEvent] from the
// event coordinator.
type BinarySensor struct {
	*DataPoint[bool]
}

// NewBinarySensor constructs a BinarySensor.
func NewBinarySensor(cfg Spec) *BinarySensor {
	return &BinarySensor{DataPoint: NewDataPoint[bool](cfg)}
}

// IsOn is a convenience wrapper around [DataPoint.Value]. The second
// return flags whether the value has been observed yet.
func (b *BinarySensor) IsOn() (on, observed bool) {
	v, ok := b.Value()
	return v, ok
}
