// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package generic

import "errors"

// Sentinel errors. Wrap with fmt.Errorf("...: %w", …) to add detail.
var (
	// ErrNoWriter is returned when a data point without a configured
	// [Writer] is asked to send a value.
	ErrNoWriter = errors.New("generic: Writer is not configured")

	// ErrNotWritable is returned when a caller attempts to write a
	// parameter whose OPERATIONS mask lacks WRITE.
	ErrNotWritable = errors.New("generic: parameter not writable")

	// ErrOutOfRange is returned when a numeric value violates
	// Descriptor.Min / Descriptor.Max.
	ErrOutOfRange = errors.New("generic: value out of range")

	// ErrUnknownLabel is returned when a [Select.SetByLabel] call
	// references a value not in Descriptor.ValueList.
	ErrUnknownLabel = errors.New("generic: unknown ENUM label")

	// ErrIndexOutOfBounds is returned when a [Select.SetByIndex] call
	// uses an index outside the ValueList.
	ErrIndexOutOfBounds = errors.New("generic: ENUM index out of bounds")

	// ErrEmptyValueList is returned when a select/enum data point has
	// no ValueList registered (making label lookup impossible).
	ErrEmptyValueList = errors.New("generic: parameter has no VALUE_LIST")
)
