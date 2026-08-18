// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package custom

import (
	"maps"
	"slices"

	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// FieldSlot is the wire target a profile field resolves to: the channel
// that carries the parameter plus the parameter name itself.
//
// A custom DP must not assume that every field it composes lives on its
// own channel under a fixed parameter name. The profile's channel-group
// schema states both, and the two do diverge: HM-CC-TC maps
// [hmenum.FieldSetpoint] to SETPOINT on the *next* channel (the
// CLIMATECONTROL_REGULATOR channel) while the custom DP itself is
// materialised on the WEATHER channel, and it maps
// [hmenum.FieldTemperature] to TEMPERATURE rather than the
// ACTUAL_TEMPERATURE its HmIP and classic-RF siblings use. Resolving
// through the schema instead of a hard-coded parameter name is what
// keeps those devices bound.
type FieldSlot struct {
	// Channel carries the parameter. Never nil when the slot resolved.
	Channel *device.Channel
	// Parameter is the wire parameter name the field maps to.
	Parameter hmenum.Parameter
}

// ResolveFieldSlot resolves one profile field against the rebased
// channel-group schema and returns the channel + parameter it maps to.
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
func ResolveFieldSlot(ch *device.Channel, group RebasedChannelGroupConfig, field hmenum.Field) (FieldSlot, bool) {
	if ch == nil {
		return FieldSlot{}, false
	}
	if fv, ok := group.Fields[field]; ok {
		p, _ := ResolveFieldValue(fv)
		return FieldSlot{Channel: ch, Parameter: p}, true
	}
	if fv, ok := group.ChannelFields[AnyChannelOffset][field]; ok {
		p, _ := ResolveFieldValue(fv)
		return FieldSlot{Channel: ch, Parameter: p}, true
	}
	if slot, ok := resolveChannelKeyedField(ch, group.ChannelFields, field); ok {
		return slot, true
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
) (FieldSlot, bool) {
	for _, chNo := range slices.Sorted(maps.Keys(byChannel)) {
		if chNo == AnyChannelOffset {
			continue
		}
		fv, ok := byChannel[chNo][field]
		if !ok {
			continue
		}
		target := siblingChannel(ch, chNo)
		if target == nil {
			continue
		}
		p, _ := ResolveFieldValue(fv)
		return FieldSlot{Channel: target, Parameter: p}, true
	}
	return FieldSlot{}, false
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

// MappedFloatField resolves `field` through the profile schema and
// returns the *generic.Float behind it, or nil when the field is
// unmapped, the channel absent, or the data point of a different type.
func MappedFloatField(ch *device.Channel, group RebasedChannelGroupConfig, field hmenum.Field) *generic.Float {
	slot, ok := ResolveFieldSlot(ch, group, field)
	if !ok {
		return nil
	}
	return FloatField(slot.Channel, slot.Parameter)
}

// MappedFloatSensorField is [MappedFloatField] for read-only FLOAT
// parameters (which the resolver projects onto *generic.Sensor[float64]).
func MappedFloatSensorField(ch *device.Channel, group RebasedChannelGroupConfig, field hmenum.Field) *generic.Sensor[float64] {
	slot, ok := ResolveFieldSlot(ch, group, field)
	if !ok {
		return nil
	}
	return FloatSensorField(slot.Channel, slot.Parameter)
}

// MappedIntegerSensorField is [MappedFloatField] for read-only INTEGER
// parameters (HUMIDITY on the classic-RF weather channels).
func MappedIntegerSensorField(ch *device.Channel, group RebasedChannelGroupConfig, field hmenum.Field) *generic.Sensor[int32] {
	slot, ok := ResolveFieldSlot(ch, group, field)
	if !ok {
		return nil
	}
	return IntegerSensorField(slot.Channel, slot.Parameter)
}
