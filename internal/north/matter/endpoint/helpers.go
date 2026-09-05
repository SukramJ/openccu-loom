// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package endpoint

import (
	"errors"
	"fmt"
	"strings"

	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/naming"
	"github.com/SukramJ/openccu-loom/internal/north/matter/schema"
	"github.com/SukramJ/openccu-loom/internal/north/matter/store"
	"github.com/SukramJ/openccu-loom/pkg/mattercontract"
)

// isNotFound matches the [store.ErrEndpointNotFound] sentinel via
// errors.Is. Wrapped to keep the assembler call sites concise.
func isNotFound(err error) bool {
	return errors.Is(err, store.ErrEndpointNotFound)
}

// modelNameResolver answers the library's [NameResolver] questions from
// the device model, so the label a bridged endpoint carries is the
// model's own answer rather than a Matter-side re-derivation.
//
// [device.Channel.NameData] ([naming.NameData.TranslatedFullName]) is
// the same naming authority MQTT discovery and REST use, including its
// device/channel de-duplication rule ([naming] package doc) — Matter
// must not re-derive that rule, or the two planes drift apart and show
// the same device under two different names.
//
// One piece has no model equivalent: a channel the operator never
// named collapses, in [naming.NameData], to the device name alone —
// fine for MQTT/REST, which disambiguate same-named entities by their
// stable id, but Matter's NodeLabel is the only thing Apple/Google
// Home show, so several unnamed channels of one device would all
// render identically. channelWord + the raw channel number is kept as
// a Matter-only disambiguator for that case.
type modelNameResolver struct {
	devices  map[string]*device.Device
	channels map[channelRef]*device.Channel
	labels   device.ParameterTranslator
	// channelWord is the localized word for a channel ("Channel",
	// "Kanal"), supplied by the host that owns the catalogue.
	channelWord string
}

// channelRef addresses one channel by device address and channel
// number — the two coordinates [store.EndpointKey] carries.
type channelRef struct {
	deviceAddress string
	channelNumber int
}

// newModelNameResolver indexes one central's fleet for label lookup.
func newModelNameResolver(devices []*device.Device, labels device.ParameterTranslator, channelWord string) *modelNameResolver {
	r := &modelNameResolver{
		devices:     make(map[string]*device.Device, len(devices)),
		channels:    make(map[channelRef]*device.Channel, len(devices)*4),
		labels:      labels,
		channelWord: channelWord,
	}
	for _, dev := range devices {
		if dev == nil {
			continue
		}
		r.devices[dev.Address] = dev
		for _, ch := range dev.Channels() {
			if ch == nil {
				continue
			}
			r.channels[channelRef{deviceAddress: dev.Address, channelNumber: ch.Number}] = ch
		}
	}
	return r
}

// channelFor returns the indexed channel for key, or nil when the key
// names a source outside the indexed fleet.
func (r *modelNameResolver) channelFor(key store.EndpointKey) *device.Channel {
	return r.channels[channelRef{deviceAddress: key.DeviceAddress, channelNumber: key.ChannelNo}]
}

// EndpointLabel implements [NameResolver].
func (r *modelNameResolver) EndpointLabel(key store.EndpointKey) string {
	ch := r.channelFor(key)
	label := ""
	if ch != nil {
		label = ch.NameData().TranslatedFullName()
	}
	if label == "" {
		// Neither the device nor the channel carries an operator name;
		// [naming.NameData] has nothing to build on either. Fall back to
		// the device address, the one identifier guaranteed to exist.
		if dev := r.devices[key.DeviceAddress]; dev != nil {
			label = dev.Address
		}
	}
	if ch != nil && ch.Number > 0 && ch.Name() == "" {
		label = strings.TrimSpace(fmt.Sprintf("%s %s %d", label, r.channelWord, ch.Number))
	}
	return label
}

// ParameterLabel implements [NameResolver]. It routes through the same
// primitives as the MQTT discovery builder and the REST data-point
// handler ([device.TranslatedParameterLabel] →
// [naming.EntityDisplayName]) so the label matches the entity name
// those surfaces emit for the same data point: locale-aware OCCU
// translation first, title-cased parameter as fallback.
//
// A parameter flagged "primary" (explicit-empty translation) yields an
// empty label — the endpoint then carries the device + channel name
// alone, mirroring how MQTT / REST collapse the entity name to the
// device name for primary parameters.
func (r *modelNameResolver) ParameterLabel(key store.EndpointKey) string {
	channelType := ""
	if ch := r.channelFor(key); ch != nil {
		channelType = ch.Type
	}
	translation, labelOmitted := device.TranslatedParameterLabel(key.DPKey, channelType, r.labels)
	name, omitted := naming.EntityDisplayName(translation, labelOmitted, key.DPKey)
	if omitted {
		return ""
	}
	return name
}

var _ NameResolver = (*modelNameResolver)(nil)

// measurementDeviceType is a thin alias for
// [mattercontract.MeasurementClassDeviceType], the canonical
// MatterMeasurementClass → DeviceType mapping. Kept here for
// backward compatibility with the existing test surface; new code
// should call the interfaces helper directly so the model layer
// remains the single source of truth (ADR 0012 "rich model, dumb
// bridge").
func measurementDeviceType(class mattercontract.MeasurementClass) uint16 {
	return mattercontract.MeasurementClassDeviceType(class)
}

// deviceTypeRevision returns the Matter Application Cluster Library
// revision the bridge advertises in `Descriptor.DeviceTypeList` for
// the supplied primary device-type ID.
//
// The general case delegates to [schema.DeviceTypeRevision], which is
// codegen'd from notes/parity/matter/matter-schema-snapshot.json via
// `make generate-matter-schema`. This guarantees that matter.js HEAD
// updates propagate automatically on the next codegen run without
// requiring manual edits here.
//
// A small customDeviceTypeRevision fallback handles bridge-specific
// overrides — currently only the Apple-Home RootNode bypass documented
// in notes/parity/by_design.md task #66. Unknown device-type IDs fall
// back to revision 1 so the function never breaks an exposure for a
// custom or vendor-specific type the schema does not yet cover.
//
// Why this matters: Apple Home's HAP service mapper validates the
// (deviceType, revision) tuple against an internal whitelist; a
// revision mismatch surfaces as "Attribute report <private> is not
// parsed into a known struct" and aborts HAP service rebuild with
// HAPErrorDomain Code=14 ("No Endpoints In Use at endpoint 0").
// Matter §1.4 guarantees strict-superset semantics — revisions never
// shrink — so a too-high value is also rejected by mappers with
// per-version schema tables.
func deviceTypeRevision(deviceType uint16) uint16 {
	// Bridge-specific overrides — device types where we deliberately
	// diverge from matter.js HEAD. Each entry must reference the
	// notes/parity/by_design.md task that documents the divergence.
	if rev, ok := customDeviceTypeRevision(deviceType); ok {
		return rev
	}
	// General case: codegen'd from matter.js HEAD schema snapshot.
	// Mirrors matter.js packages/node/src/devices/<name>.ts revision fields.
	if rev, ok := schema.DeviceTypeRevision(uint32(deviceType)); ok {
		return rev
	}
	return 1
}

// customDeviceTypeRevision contains bridge-specific device-type revision
// overrides that deliberately diverge from matter.js HEAD. Each entry is
// documented in notes/parity/by_design.md.
func customDeviceTypeRevision(_ uint16) (uint16, bool) {
	// No active overrides at this time.
	// Pattern for future additions:
	//   case 0x0016: // RootNode — Apple-Home bypass (notes/parity/by_design.md)
	//       return 3, true
	return 0, false
}
