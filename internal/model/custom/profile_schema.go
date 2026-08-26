// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package custom

import (
	"math"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// AnyChannelOffset is the sentinel key used in [ChannelGroupConfig.ChannelFields]
// To mirror
// primary channel itself and is *not* rebased by [RebaseChannelGroup].
//
// We deliberately do not reuse -1 (which is [hmenum.ChannelOffsetState]) so
// that the sentinel cannot collide with any legitimate
// [hmenum.ChannelOffset] value. [math.MinInt32] is well outside any
// physical channel range and survives addition with reasonable group_no
// offsets without overflow concerns at runtime (callers never arithmetic
// the sentinel; the rebase algorithm preserves it verbatim).
const AnyChannelOffset = math.MinInt32

// FieldMapping mirrors the Python reference implementation's field
// mapping. It pairs a [hmenum.Parameter] with explicit visibility
// forcing.
//
// IsVisible semantics:
//   - nil     → no forcing (the data point inherits its default visibility)
//   - true    → force visible (CDP_VISIBLE)
//   - false   → force hidden (NO_CREATE)
//
// In Python, profile authors use the [Visible] / [Hidden] helpers to wrap
// a [hmenum.Parameter] into a [FieldMapping]; bare parameters are sugar for
// "no visibility forcing". openccu-loom collapses both representations into a
// single [FieldValue] type — see [Bare], [Visible], [Hidden], and
// [ResolveFieldValue] below.
type FieldMapping struct {
	// Parameter is the underlying CCU parameter the field maps to.
	Parameter hmenum.Parameter

	// IsVisible carries the explicit visibility decision; nil means
	// "no forcing".
	IsVisible *bool
}

// FieldValue is a discriminated union over a bare [hmenum.Parameter] (no
// forcing) and a [FieldMapping] (with forcing).
//
// We collapse the two cases into a single struct: the [Parameter] field is
// always populated, and [Mapping] is non-nil iff the author wanted explicit
// visibility forcing. This keeps the Go API ergonomic (no type assertions at
// every call site) while preserving the semantic distinction observable
// through [ResolveFieldValue].
type FieldValue struct {
	// Parameter is the underlying CCU parameter; always populated.
	Parameter hmenum.Parameter

	// Mapping is non-nil when the field value was authored with explicit
	// visibility forcing via [Visible] or [Hidden]. Bare parameter values
	// authored via [Bare] leave this nil.
	Mapping *FieldMapping
}

// Bare returns a [FieldValue] that wraps a parameter without visibility
// forcing — the equivalent of writing `Parameter.X` (bare) in
func Bare(parameter hmenum.Parameter) FieldValue {
	return FieldValue{Parameter: parameter}
}

// Visible returns a [FieldValue] that forces the data point to be created as
// visible (CDP_VISIBLE).
func Visible(parameter hmenum.Parameter) FieldValue {
	t := true
	return FieldValue{
		Parameter: parameter,
		Mapping:   &FieldMapping{Parameter: parameter, IsVisible: &t},
	}
}

// Hidden returns a [FieldValue] that forces the data point to be hidden
// (NO_CREATE).
func Hidden(parameter hmenum.Parameter) FieldValue {
	f := false
	return FieldValue{
		Parameter: parameter,
		Mapping:   &FieldMapping{Parameter: parameter, IsVisible: &f},
	}
}

// ResolveFieldValue extracts the underlying parameter and the optional
// Visibility decision from a [FieldValue]
// `resolve_field_value`. The returned `isVisible` is nil for bare values
// and points at the forcing decision for mapped values.
func ResolveFieldValue(fv FieldValue) (parameter hmenum.Parameter, isVisible *bool) {
	if fv.Mapping == nil {
		return fv.Parameter, nil
	}
	return fv.Mapping.Parameter, fv.Mapping.IsVisible
}

// ChannelGroupConfig describes the channel structure of a custom device
// Profile.
//
// Channel-number conventions:
//
//   - PrimaryChannel, SecondaryChannels, StateChannelOffset, and
//     ChannelFields keys are *relative* offsets from a base channel
//     (group_no). The base channel is decided at device-registration
//     time. [RebaseChannelGroup] converts a [ChannelGroupConfig] into a
//     [RebasedChannelGroupConfig] with absolute channel numbers.
//
//   - FixedChannelFields keys are *absolute* channel numbers and are
//     passed through unchanged. They model fields that must always
//     reference specific device channels regardless of the group offset
//     (e.g. parameters on channel 0 that apply to the whole device).
//
// The boolean PrimaryChannelSet distinguishes "no primary channel" from
// "primary channel = 0", matching Python's `int | None = 0` default
// where authors can explicitly set `primary_channel=None`.
//
// The ChannelFields map uses `int` keys; the special key
// [AnyChannelOffset] mirrors Python's `None` key — a fields block that
// is preserved verbatim through rebasing.
type ChannelGroupConfig struct {
	// PrimaryChannel is the relative offset of the primary channel.
	// Combined with PrimaryChannelSet to distinguish 0 from "unset".
	PrimaryChannel int

	// PrimaryChannelSet reports whether PrimaryChannel was authored
	// explicitly. `false` mirrors Python's `primary_channel=None`.
	PrimaryChannelSet bool

	// SecondaryChannels are the relative offsets of secondary channels.
	SecondaryChannels []int

	// StateChannelOffset is the relative offset of an optional state
	// channel. nil means "no state channel".
	StateChannelOffset *int

	// AllowUndefinedGenericDataPoints, when true, lets a profile accept
	// extra generic data points that the channel exposes but the
	// profile does not name explicitly.
	AllowUndefinedGenericDataPoints bool

	// Fields are the field mappings applied to the primary channel.
	Fields map[hmenum.Field]FieldValue

	// ChannelFields are channel-specific field mappings, keyed by
	// relative channel offset. The special key [AnyChannelOffset]
	// preserves Python's `None` key semantics — entries under it are
	// passed through [RebaseChannelGroup] unchanged.
	ChannelFields map[int]map[hmenum.Field]FieldValue

	// FixedChannelFields are channel-specific field mappings keyed by
	// *absolute* channel number; never rebased.
	FixedChannelFields map[int]map[hmenum.Field]FieldValue
}

// ProfileConfig is a complete profile configuration for a device type,
type ProfileConfig struct {
	// ProfileType is the [hmenum.DeviceProfile] this configuration
	// applies to.
	ProfileType hmenum.DeviceProfile

	// ChannelGroup is the channel structure and field mappings.
	ChannelGroup ChannelGroupConfig

	// AdditionalDataPoints maps a relative channel offset to a list of
	// extra generic [hmenum.Parameter] values to expose on top of the
	// custom data points.
	AdditionalDataPoints map[int][]hmenum.Parameter

	// IncludeDefaultDataPoints decides whether the global default data
	// points (battery, RSSI, ...) are added to the custom data point.
	// Defaults to true; profile authors must use [NewProfileConfig]
	// (or set the field explicitly) to disable it.
	IncludeDefaultDataPoints bool
}

// NewProfileConfig constructs a [ProfileConfig] with [ProfileConfig.IncludeDefaultDataPoints]
// defaulted to true — matching the Python pydantic default. Profile
// authors that need to opt out can either set the field explicitly on the
// returned struct or build the literal directly.
func NewProfileConfig(profileType hmenum.DeviceProfile, cg ChannelGroupConfig) ProfileConfig {
	return ProfileConfig{
		ProfileType:              profileType,
		ChannelGroup:             cg,
		IncludeDefaultDataPoints: true,
	}
}

// RebasedChannelGroupConfig holds a [ChannelGroupConfig] after rebasing every
// relative channel number with a group offset.
//
// All channel numbers in this struct are absolute, except entries in
// [ChannelFields] keyed by [AnyChannelOffset] which preserve the
// Python-`None` semantics, and [FixedChannelFields] which were never relative
// to begin with.
type RebasedChannelGroupConfig struct {
	// PrimaryChannel is the absolute primary-channel number, or nil if
	// the source [ChannelGroupConfig] had no primary channel.
	PrimaryChannel *int

	// SecondaryChannels are the absolute secondary-channel numbers.
	SecondaryChannels []int

	// StateChannel is the absolute state-channel number, or nil if
	// the source had no [ChannelGroupConfig.StateChannelOffset].
	StateChannel *int

	// AllowUndefinedGenericDataPoints carries [ChannelGroupConfig.AllowUndefinedGenericDataPoints]
	// through unchanged.
	AllowUndefinedGenericDataPoints bool

	// Fields carries [ChannelGroupConfig.Fields] through unchanged.
	Fields map[hmenum.Field]FieldValue

	// ChannelFields are channel-specific field mappings keyed by
	// *absolute* channel number after rebasing. The special key
	// [AnyChannelOffset] preserves the Python-`None` semantics.
	ChannelFields map[int]map[hmenum.Field]FieldValue

	// FixedChannelFields carries [ChannelGroupConfig.FixedChannelFields]
	// through unchanged (already absolute).
	FixedChannelFields map[int]map[hmenum.Field]FieldValue
}

// RebaseChannelGroup applies a `groupNo` offset to every relative channel
// number in `cfg.ChannelGroup` and returns a [RebasedChannelGroupConfig]
// with absolute channel numbers.
//
// Behaviour:
//
//   - PrimaryChannel: shifted by `groupNo` if [ChannelGroupConfig.PrimaryChannelSet]
//     is true; otherwise the result's PrimaryChannel is nil.
//   - SecondaryChannels: each element shifted by `groupNo`.
//   - StateChannelOffset: shifted by `groupNo` if non-nil; otherwise nil.
//   - ChannelFields: each non-sentinel key shifted by `groupNo`.
//     Entries keyed by [AnyChannelOffset] are preserved verbatim.
//   - FixedChannelFields: copied through unchanged (absolute already).
//   - Fields: copied through unchanged.
//
// The implementation does not deep-copy the inner field maps; callers
// must treat both the source and the result as logically immutable
// (matching Python's `frozen=True` BaseModels).
func RebaseChannelGroup(cfg ProfileConfig, groupNo int) RebasedChannelGroupConfig {
	cg := cfg.ChannelGroup

	var primary *int
	if cg.PrimaryChannelSet {
		v := cg.PrimaryChannel + groupNo
		primary = &v
	}

	secondary := make([]int, len(cg.SecondaryChannels))
	for i, ch := range cg.SecondaryChannels {
		secondary[i] = ch + groupNo
	}

	var state *int
	if cg.StateChannelOffset != nil {
		v := *cg.StateChannelOffset + groupNo
		state = &v
	}

	var rebased map[int]map[hmenum.Field]FieldValue
	if cg.ChannelFields != nil {
		rebased = make(map[int]map[hmenum.Field]FieldValue, len(cg.ChannelFields))
		for ch, fields := range cg.ChannelFields {
			if ch == AnyChannelOffset {
				rebased[AnyChannelOffset] = fields
				continue
			}
			rebased[ch+groupNo] = fields
		}
	}

	return RebasedChannelGroupConfig{
		PrimaryChannel:                  primary,
		SecondaryChannels:               secondary,
		StateChannel:                    state,
		AllowUndefinedGenericDataPoints: cg.AllowUndefinedGenericDataPoints,
		Fields:                          cg.Fields,
		ChannelFields:                   rebased,
		FixedChannelFields:              cg.FixedChannelFields,
	}
}
