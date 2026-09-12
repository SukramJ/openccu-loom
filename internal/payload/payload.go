// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package payload

import (
	hapayload "github.com/SukramJ/go-hamqtt/payload"
)

// Kind enumerates the payload categories. It mirrors the Python
// reference implementation's `Kind` enum; the wire-level names match
// the sibling projects.
type Kind string

// Kind values.
const (
	KindInfo   Kind = "info"
	KindConfig Kind = "config"
	KindState  Kind = "state"
)

// Options tweak the [ForWith] extraction.
type Options struct {
	// UseAltNames tells the extractor to prefer the tag's `alt=`
	// override over the field's lower-cased name.
	UseAltNames bool

	// IncludeZero retains zero-valued fields. Default (false) omits
	// them, matching the Python reference implementation's default
	// omit-zero behavior.
	IncludeZero bool
}

// ExtraProperties is implemented by a type that contributes properties
// reflection over its exported fields cannot see.
//
// The reason it exists: a field that several goroutines read while one
// writes it cannot stay a plain exported field — the readers tear. Moving
// such a field behind a mutex and an accessor makes it correct and, in the
// same stroke, invisible to [ForWith], which walks exported fields only.
// Losing a property that way is silent: the harvest simply returns one key
// fewer, the consumer falls back, and nothing fails. `Device.name` went
// through exactly that and dropped the device name out of every
// HA-Discovery device block.
//
// Implementations return the properties for kind k, using the same alt
// naming [Options.UseAltNames] selects, and honour opts.IncludeZero.
// Anything they return overrides a same-named reflected field.
//
// loom:reachable:reason="ForWith type-asserts every obj against it, and *device.Device implements it to publish its mutex-guarded name; an interface reached only by assertion, which the analyzer cannot see used"
type ExtraProperties interface {
	PayloadExtra(k Kind, opts Options) map[string]any
}

// ForWith returns the k-partitioned view of obj with explicit options.
//
// The reflection walk itself lives in go-hamqtt/payload; this wrapper exists
// to hold the two things that are loom's rather than the shared model's:
//
//   - The field-name policy. This daemon's own MQTT info topics publish
//     `interfaceid`, not `interface_id`, and `alt=` is what reaches snake_case
//     where a key needs it. That surface is a published contract (ADR 0067),
//     so the shared package is asked for [hapayload.NamingLower].
//   - [ExtraProperties]. Its method name and signature are loom's; rather than
//     make every implementer satisfy two near-identical interfaces, the
//     contribution is merged here, after the shared harvest and with the same
//     precedence it always had.
//
// Read-only contract: values in the returned map are the field's live
// `any`-boxed value, not a deep copy. A field holding a map, slice, or
// pointer hands back a reference to the same backing storage obj owns.
// Callers must treat the returned map (and any composite value inside it)
// as read-only; mutating a nested map/slice mutates obj's field too. A
// deep copy is deliberately not taken here — payload extraction happens on
// every state publish, and obj's underlying data points are not shaped in
// a way that this call site would ever mutate them afterward.
func ForWith(obj any, k Kind, opts Options) map[string]any {
	out := hapayload.ForWith(obj, sharedKind(k), hapayload.Options{
		Naming:      hapayload.NamingLower,
		UseAltNames: opts.UseAltNames,
		IncludeZero: opts.IncludeZero,
	})
	if ep, ok := obj.(ExtraProperties); ok {
		for name, val := range ep.PayloadExtra(k, opts) {
			out[name] = val
		}
	}
	return out
}

// sharedKind maps loom's string Kind onto the shared package's. An unknown
// kind yields the zero value, which the shared harvest matches no field
// against — the same outcome loom's own parser produced.
func sharedKind(k Kind) hapayload.Kind {
	switch k {
	case KindInfo:
		return hapayload.Info
	case KindConfig:
		return hapayload.Config
	case KindState:
		return hapayload.State
	default:
		return 0
	}
}
