// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package hmproperty

// Kind categorises a data-point or device property so that north-bound
// adapters can collect the right subset for payloads and log contexts.
type Kind string

// Kind values.
const (
	// KindConfig marks properties that describe how a device is
	// configured (e.g. operation mode, target temperature).
	// Equivalent to Python's @config_property decorator.
	KindConfig Kind = "config"
	// KindInfo marks properties that carry read-only metadata
	// (e.g. firmware version, device address).
	// Equivalent to Python's @info_property decorator.
	KindInfo Kind = "info"
	// KindSimple marks general-purpose properties that do not fit the
	// other three categories.
	// Equivalent to Python's @hm_property decorator (default kind).
	KindSimple Kind = "simple"
	// KindState marks dynamic state properties that change at runtime
	// (e.g. current temperature, switch state).
	// Equivalent to Python's @state_property decorator.
	KindState Kind = "state"
)

// String returns the wire representation.
func (k Kind) String() string { return string(k) }

// AllKinds is the ordered list of every Kind value. Iterate over this
// when you need to cover all categories, e.g. in [GetPropertyByLogContext].
var AllKinds = []Kind{KindConfig, KindInfo, KindSimple, KindState}
