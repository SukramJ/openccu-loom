// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package custom

import (
	"maps"
	"slices"

	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// ResolveFieldSlot resolves one profile field against the rebased
// channel-group schema and returns the channel that carries it plus the
// wire parameter it maps to.
//
// A custom DP must not assume that every field it composes lives on its
// own channel under a fixed parameter name. The profile's channel-group
// schema states both, and the two do diverge: HM-CC-TC maps
// [hmenum.FieldSetpoint] to SETPOINT on the *next* channel (the
// CLIMATECONTROL_REGULATOR channel) while the custom DP itself is
// materialised on the WEATHER channel, and it maps
// [hmenum.FieldTemperature] to TEMPERATURE rather than the
// ACTUAL_TEMPERATURE its HmIP and classic-RF siblings use. Resolving
// through the schema instead of a hard-coded parameter name is what keeps
// those devices bound.
//
// The lookup order mirrors [applyFieldVisibility] so a field binds to
// exactly the data point whose visibility the materializer forced:
//
//  1. `Fields` — the primary channel's own field map (→ ch),
//  2. `ChannelFields[AnyChannelOffset]` — Python's `None` key (→ ch),
//  3. `ChannelFields[n]` — rebased to absolute channel numbers,
//  4. `FixedChannelFields[n]` — already absolute.
//
// Returns ok=false when the schema does not map the field, or when it
// maps it to a channel the concrete device does not carry. Callers treat
// that as "fall back to the caller's own convention" rather than as an
// error — profiles are shared across device types that expose different
// channel counts.
func ResolveFieldSlot(
	ch *device.Channel,
	group RebasedChannelGroupConfig,
	field hmenum.Field,
) (target *device.Channel, parameter hmenum.Parameter, ok bool) {
	if ch == nil {
		return nil, "", false
	}
	if fv, found := group.Fields[field]; found {
		p, _ := ResolveFieldValue(fv)
		return ch, p, true
	}
	if fv, found := group.ChannelFields[AnyChannelOffset][field]; found {
		p, _ := ResolveFieldValue(fv)
		return ch, p, true
	}
	if t, p, found := resolveChannelKeyedField(ch, group.ChannelFields, field); found {
		return t, p, true
	}
	return resolveChannelKeyedField(ch, group.FixedChannelFields, field)
}

// resolveChannelKeyedField walks a channel-keyed field map in ascending
// channel order (deterministic across runs — Go map iteration is not)
// and returns the first entry that maps `field` onto a channel the
// device actually carries.
func resolveChannelKeyedField(
	ch *device.Channel,
	byChannel map[int]map[hmenum.Field]FieldValue,
	field hmenum.Field,
) (target *device.Channel, parameter hmenum.Parameter, ok bool) {
	for _, chNo := range slices.Sorted(maps.Keys(byChannel)) {
		if chNo == AnyChannelOffset {
			continue
		}
		fv, found := byChannel[chNo][field]
		if !found {
			continue
		}
		sibling := siblingChannel(ch, chNo)
		if sibling == nil {
			continue
		}
		p, _ := ResolveFieldValue(fv)
		return sibling, p, true
	}
	return nil, "", false
}

// siblingChannel returns the channel with the given number on the same
// device as ch, or nil when the device does not carry it.
func siblingChannel(ch *device.Channel, chNo int) *device.Channel {
	if ch == nil {
		return nil
	}
	if ch.Number == chNo {
		return ch
	}
	dev := ch.Device()
	if dev == nil {
		return nil
	}
	for _, sibling := range dev.Channels() {
		if sibling.Number == chNo {
			return sibling
		}
	}
	return nil
}

// ResolveSlotOr resolves one composed field through the profile schema and
// falls back to `fallback` on the caller's own channel when the schema does
// not map it.
//
// This is the binding every custom DP should use for a field the profile
// declares. Reaching for a fixed parameter name on the DP's own channel
// instead is the defect this exists to prevent: the schema states both the
// parameter and the channel per device family, and where the two disagree the
// lookup returns nil, the accessor reports the feature as unsupported, and
// nothing fails — no log line, no failing test. The value is simply absent
// forever.
//
// The fallback is not a courtesy. A profile is shared across device types
// that expose different channel counts, and a custom DP can be constructed
// without a schema at all (unit tests, the group variants); for the families
// whose schema names exactly the parameter the fallback does, the two agree
// and the fallback is what keeps those paths working.
func ResolveSlotOr(
	ch *device.Channel,
	group RebasedChannelGroupConfig,
	field hmenum.Field,
	fallback hmenum.Parameter,
) (target *device.Channel, parameter hmenum.Parameter) {
	if t, p, ok := ResolveFieldSlot(ch, group, field); ok {
		return t, p
	}
	return ch, fallback
}

// ResolveSlotOnCarryingChannel resolves a field through the schema like
// [ResolveSlotOr], and then makes one further check the schema cannot make:
// whether the resolved channel actually carries the parameter. When it does
// not, the custom DP's own channel is used instead.
//
// The schema states where a field lives per profile, and a profile is shared
// by device families that place the same parameter differently. IPLock maps
// FieldError to ERROR_JAMMED at offset -1, which is where the HmIP-DLD
// reports it — the lock sits on channel 1, the fault on channel 0. The
// HmIP-DLP carries the same parameter on the lock's own channel, so the
// offset is right for one model and wrong for the other. Binding on the
// offset alone fixes the DLD and breaks the DLP.
//
// Kept separate from [ResolveFieldSlot] on purpose. That function answers a
// question about the profile — "where does the schema say this field lives?"
// — and its answer must not depend on which device is in front of it.
// Whether a concrete channel carries a parameter is a question about the
// device, and it belongs to the binding rather than to the schema lookup.
//
// This is a deliberate divergence from the reference stack, which resolves
// the mapped channel or nothing; see notes/parity/by_design.md.
func ResolveSlotOnCarryingChannel(
	ch *device.Channel,
	group RebasedChannelGroupConfig,
	field hmenum.Field,
	fallback hmenum.Parameter,
) (target *device.Channel, parameter hmenum.Parameter) {
	t, p := ResolveSlotOr(ch, group, field, fallback)
	if t != nil && t.Parameter(p) != nil {
		return t, p
	}
	if ch != nil && ch.Parameter(p) != nil {
		return ch, p
	}
	return t, p
}
