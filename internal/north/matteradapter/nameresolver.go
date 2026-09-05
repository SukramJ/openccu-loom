// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package matteradapter

import (
	"fmt"
	"strings"

	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/naming"
	matterendpoint "github.com/SukramJ/openccu-loom/internal/store/matterendpoint"
)

// modelNameResolver answers the Matter side's [NameResolver]
// questions from the device model, so the label a bridged endpoint
// carries is the model's own answer rather than a Matter-side
// re-derivation.
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
// number — the two coordinates [matterendpoint.SourceKey] carries.
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
func (r *modelNameResolver) channelFor(key matterendpoint.SourceKey) *device.Channel {
	return r.channels[channelRef{deviceAddress: key.DeviceAddress, channelNumber: key.ChannelNo}]
}

// EndpointLabel implements [NameResolver].
func (r *modelNameResolver) EndpointLabel(key matterendpoint.SourceKey) string {
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

// ParameterLabel implements [NameResolver]. It routes through
// the same primitives as the MQTT discovery builder and the REST
// data-point handler ([device.TranslatedParameterLabel] →
// [naming.EntityDisplayName]) so the label matches the entity name
// those surfaces emit for the same data point: locale-aware OCCU
// translation first, title-cased parameter as fallback.
//
// A parameter flagged "primary" (explicit-empty translation) yields an
// empty label — the endpoint then carries the device + channel name
// alone, mirroring how MQTT / REST collapse the entity name to the
// device name for primary parameters.
func (r *modelNameResolver) ParameterLabel(key matterendpoint.SourceKey) string {
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
