// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package matterendpoint

import (
	"fmt"

	fabricendpoint "github.com/SukramJ/go-fabric/endpoint"
)

// DPKind classifies a Matter endpoint's source within the model.
// Persisted as a TEXT column with a CHECK constraint — the database
// rejects unknown kinds at insert time.
type DPKind string

// DPKind values. The string forms match the dp_kind CHECK constraint in
// migration 007 and, through [SourceKey.String], the UniqueID hash.
const (
	DPKindCustom      DPKind = "custom"
	DPKindGeneric     DPKind = "generic"
	DPKindCalculated  DPKind = "calculated"
	DPKindCombined    DPKind = "combined"
	DPKindMeasurement DPKind = "measurement"
)

// SourceKey identifies the model-side source of a Matter endpoint. The
// 5-tuple is the primary key of both matter_endpoints and
// matter_exposures.
//
// It implements [fabricendpoint.SourceKey], so the bridge module carries
// it opaquely and hands the very same value back — through
// [fabricendpoint.Store] and through the assembled endpoint — for this
// package to recover by type assertion. Nothing parses [String]; the
// separator is not escaped and a central name may legitimately contain
// one, so splitting the rendered form back apart would be a guess about
// operator-supplied data.
type SourceKey struct {
	CentralName   string
	DeviceAddress string
	ChannelNo     int
	DPKind        DPKind
	DPKey         string
}

// String implements [fabricendpoint.SourceKey].
//
// This exact rendering — five fields, in this order, separated by "|",
// with the channel in decimal and no escaping — is the sole input to
// every bridged endpoint's Matter UniqueID hash. A commissioner caches
// the UniqueID per accessory, so changing the format (adding a field,
// reordering, padding the channel, escaping the separator) silently
// re-fingerprints the whole fleet and the operator has to remove and
// re-add every bridged device by hand. Treat it as wire format, not as
// a debug string.
func (k SourceKey) String() string {
	return fmt.Sprintf("%s|%s|%d|%s|%s",
		k.CentralName, k.DeviceAddress, k.ChannelNo, k.DPKind, k.DPKey)
}

// Ensure the module's contract is satisfied by the value type — the
// assembler uses the key as a map key, so it must stay comparable and
// must not need a pointer to render.
var _ fabricendpoint.SourceKey = SourceKey{}

// keyOf recovers this package's key from the opaque one the module
// carries. It returns an error rather than an empty key so a store wired
// to a foreign key type fails the assembly loudly instead of quietly
// treating every source as unknown and reallocating every endpoint id.
func keyOf(key fabricendpoint.SourceKey) (SourceKey, error) {
	switch k := key.(type) {
	case SourceKey:
		return k, nil
	case *SourceKey:
		if k == nil {
			return SourceKey{}, fmt.Errorf("matter endpoint store: nil %T source key", k)
		}
		return *k, nil
	default:
		return SourceKey{}, fmt.Errorf(
			"matter endpoint store: source key is %T, want %T", key, SourceKey{},
		)
	}
}
