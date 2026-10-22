// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package matterendpoint

import (
	"fmt"
	"strconv"
	"strings"

	fabricdevice "github.com/SukramJ/go-fabric/device"
	fabricendpoint "github.com/SukramJ/go-fabric/endpoint"

	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// DPKind classifies a Matter endpoint's source within the model.
// matter_exposures persists it as a TEXT column with a CHECK constraint
// (migration 009), so the database rejects unknown kinds at insert time;
// in matter_endpoints it is part of the rendered key.
// loom:reachable:reason="passed by the adapter when it emits a candidate (internal/north/matteradapter/candidates.go:93); a named string type reached only as an argument is invisible to the analyzer"
type DPKind string

// DPKind values. The string forms match the dp_kind CHECK constraint of
// matter_exposures and, through [SourceKey.String], the UniqueID hash.
const (
	DPKindCustom      DPKind = "custom"
	DPKindGeneric     DPKind = "generic"
	DPKindCalculated  DPKind = "calculated"
	DPKindCombined    DPKind = "combined"
	DPKindMeasurement DPKind = "measurement"
)

// SourceKey identifies the model-side source of a Matter endpoint. The
// 5-tuple is the primary key of matter_exposures; matter_endpoints is
// keyed by its [SourceKey.String] rendering.
//
// It implements [fabricendpoint.SourceKey], so the bridge module carries
// it opaquely and hands the very same value back through the assembled
// endpoint. The endpoint store persists only [SourceKey.String] and lists
// rows back through [DecodeSourceKey]; the store refuses to write a key
// whose rendering does not decode back to itself ([ErrKeyNotDecodable]).
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
var _ fabricdevice.SourceKey = SourceKey{}

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

// DecodeSourceKey rebuilds a [SourceKey] from its [SourceKey.String]
// rendering, for go-fabric's sqlitestore (sqlitestore.WithKeyDecoder),
// which persists only the rendering. The split is unambiguous for every key
// the store accepts: the central name cannot contain "|"
// ([hmtypes.ValidateCentralName]), the device address is taken up to the
// next "|", the channel must re-render to the same decimal, the kind must be
// one of the [DPKind] values, and the data-point key keeps the remainder,
// "|" included. A rendering that does not split comes back as an
// [fabricendpoint.StringKey]; no live source carries that key, so the
// assembler garbage-collects the row.
func DecodeSourceKey(rendered string) fabricendpoint.SourceKey {
	if k, ok := parseSourceKey(rendered); ok {
		return k
	}
	return fabricendpoint.StringKey(rendered)
}

func parseSourceKey(rendered string) (SourceKey, bool) {
	central, rest, ok := strings.Cut(rendered, "|")
	if !ok || !hmtypes.IsValidCentralName(central) {
		return SourceKey{}, false
	}
	parts := strings.SplitN(rest, "|", 4)
	if len(parts) != 4 {
		return SourceKey{}, false
	}
	channel, err := strconv.Atoi(parts[1])
	if err != nil || strconv.Itoa(channel) != parts[1] {
		return SourceKey{}, false
	}
	kind := DPKind(parts[2])
	switch kind {
	case DPKindCustom, DPKindGeneric, DPKindCalculated, DPKindCombined, DPKindMeasurement:
	default:
		return SourceKey{}, false
	}
	return SourceKey{
		CentralName:   central,
		DeviceAddress: parts[0],
		ChannelNo:     channel,
		DPKind:        kind,
		DPKey:         parts[3],
	}, true
}
