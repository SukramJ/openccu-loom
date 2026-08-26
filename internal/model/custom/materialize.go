// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package custom

import (
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"sync"

	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// Constructor instantiates a custom data point for a given channel +
// rebased channel-group schema. Sub-packages (climate, cover, light,
// lock, siren, switch, textdisplay, valve) register their concrete
// constructors via [Registry.RegisterConstructor] in their `init()`
// blocks ( D.12).
//
// Until D.12 lands the constructor map is empty and
// [CreateCustomDataPoint] silently skips a profile whose constructor
// is not registered (it logs at DEBUG level so the daemon does not
// fail device hydration).
//
// The constructor receives:
//
// - channel: the *primary* channel for the profile instance — the
// custom DP attaches itself to this channel via
// [device.Channel.SetCustomDataPoint] from the materializer once
// construction succeeds.
// - group: the rebased channel-group schema (absolute channel
// numbers) the constructor consults to look up its sub-DPs on
// sibling channels of the same device.
//
// Constructors return an [device.AttachableDataPoint] (the channel
// already accepts that interface) so the materializer can SetCustomDataPoint
// without further type knowledge. Returning an error aborts that
// single profile/channel pairing — every other profile keeps materialising.
type Constructor func(channel *device.Channel, group RebasedChannelGroupConfig) (device.AttachableDataPoint, error)

// constructors maps a [hmenum.DeviceProfile] to the registered
// [Constructor]. Sub-packages contribute via
// [Registry.RegisterConstructor] inside `init()`. Reads happen on the
// hot device-hydration path; we use a separate sync.RWMutex to keep
// the registry's other fields uncontended.
type constructorRegistry struct {
	mu    sync.RWMutex
	items map[hmenum.DeviceProfile]Constructor
}

// constructorsRegistry is the per-Registry map of profile constructors.
// Stored on the Registry itself so callers that build their own
// registry instance (tests, future multi-tenant hosts) get an
// isolated constructor pool.
//
// We declare it lazily on first RegisterConstructor / Constructor
// call so the legacy zero-value Registry literal still works.
func (r *Registry) ensureConstructors() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.constructors == nil {
		r.constructors = &constructorRegistry{items: make(map[hmenum.DeviceProfile]Constructor)}
	}
}

// RegisterConstructor installs a [Constructor] for the given profile
// name. Sub-packages call this from their `init()` blocks. Returns
// [ErrConstructorConflict] when a constructor is already registered
// for the profile.
func (r *Registry) RegisterConstructor(name hmenum.DeviceProfile, ctor Constructor) error {
	if ctor == nil {
		return errors.New("custom: Constructor is nil")
	}
	r.ensureConstructors()
	r.constructors.mu.Lock()
	defer r.constructors.mu.Unlock()
	if _, ok := r.constructors.items[name]; ok {
		return ErrConstructorConflict
	}
	r.constructors.items[name] = ctor
	return nil
}

// MustRegisterConstructor panics on conflict. Convenient for `init()`.
func (r *Registry) MustRegisterConstructor(name hmenum.DeviceProfile, ctor Constructor) {
	if err := r.RegisterConstructor(name, ctor); err != nil {
		panic(err)
	}
}

// Constructor looks up the registered constructor for name. Returns
// the zero [Constructor] and false when no constructor is registered.
func (r *Registry) Constructor(name hmenum.DeviceProfile) (Constructor, bool) {
	if r == nil {
		return nil, false
	}
	r.mu.RLock()
	cr := r.constructors
	r.mu.RUnlock()
	if cr == nil {
		return nil, false
	}
	cr.mu.RLock()
	defer cr.mu.RUnlock()
	c, ok := cr.items[name]
	return c, ok
}

// ErrConstructorConflict is returned when two callers try to register
// a constructor for the same [hmenum.DeviceProfile].
var ErrConstructorConflict = errors.New("custom: constructor already registered for profile")

// CreateCustomDataPoints walks every profile registered for the device's
// model and materialises the custom data points across the device's channels.
//
// - Resolves the device's profiles via [Registry.GetConfigs] (model
// normalisation + blacklist + hierarchical match). - For each (profile,
// channel) pair, calls [CreateCustomDataPoint].
//
// Errors from individual (profile, channel) pairings are joined and returned
// together — partial success is allowed: a profile whose constructor is
// missing logs and continues; a constructor that returns an error annotates
// and continues so other profiles still land.
func CreateCustomDataPoints(dev *device.Device, registry *Registry) error {
	if dev == nil {
		return errors.New("custom: device is nil")
	}
	if registry == nil {
		return errors.New("custom: registry is nil")
	}

	profiles := registry.GetConfigs(dev.Model)
	if len(profiles) == 0 {
		return nil
	}

	// Materialise channels in deterministic address order so test
	// assertions over multi-profile devices stay stable.
	channels := dev.Channels()
	sort.Slice(channels, func(i, j int) bool { return channels[i].Address < channels[j].Address })

	var errs []error
	for _, profile := range profiles {
		// _add_channel_groups_to_device: register channel-group
		// memberships up front (Python does this once per profile,
		// not once per channel). We then iterate every channel and
		// let CreateCustomDataPoint handle the relevance check.
		addChannelGroupsToDevice(dev, profile)

		for _, ch := range channels {
			if err := CreateCustomDataPoint(dev, ch, profile, registry); err != nil {
				errs = append(errs, fmt.Errorf("custom: %s on %s: %w", profile.Name, ch.Address, err))
			}
		}
	}
	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	return nil
}

// CreateCustomDataPoint materialises one custom data point for the (channel,
// profile) pairing. Returns nil when:
//
// - the channel is not in the profile's relevant set (skip silently), - the
// constructor for the profile is not registered (skip + log).
func CreateCustomDataPoint(dev *device.Device, ch *device.Channel, profile Profile, registry *Registry) error {
	if dev == nil || ch == nil || registry == nil {
		return errors.New("custom: device, channel, registry must be non-nil")
	}

	// Profiles that ship without a Config (pre- registrations,
	// hand-written legacy literals) cannot be rebased; the
	// materializer skips them — the legacy path will be removed once
	// every profile has a Config.
	if profile.Config == nil {
		return nil
	}

	relevant := relevantChannels(profile)
	if _, ok := relevant[ch.Number]; !ok {
		return nil
	}

	// Resolve the channel's group_no. Mirrors Python's
	// `device.get_channel_group_no(channel_no=channel.no)` — we use the
	// per-Channel group number that addChannelGroupsToDevice has already
	// set (zero falls back to the Python-`None` semantics).
	groupNo := ch.GroupNumber()

	rebased := profile.Rebase(groupNo)

	// Resolve & force-mark sub-DP visibility per the profile schema.
	applyFieldVisibility(dev, ch, rebased)

	// _mark_data_points: AdditionalDataPoints (relative, must be
	// rebased by group_no) plus the legacy Extended.AdditionalDataPoints
	// (already absolute).
	markAdditionalDataPoints(dev, ch, profile, groupNo)

	// Constructor lookup. Missing constructor is a no-op (D.12 fills
	// these in for the seven sub-packages).
	ctor, ok := registry.Constructor(profile.Name)
	if !ok {
		slog.Debug(
			"custom: no constructor registered for profile; skipping",
			slog.String("profile", string(profile.Name)),
			slog.String("device_type", dev.Model),
			slog.String("channel", ch.Address),
		)
		return nil
	}

	dp, err := ctor(ch, rebased)
	if err != nil {
		return fmt.Errorf("constructor: %w", err)
	}
	if dp == nil {
		return nil
	}
	ch.SetCustomDataPoint(dp)
	return nil
}

// AddChannelGroupsToDevice mirrors
// `_add_channel_groups_to_device` (`model/custom/definition.py:142`).
// For every base channel in `profile.Channels` the function records:
//
// - the group's master channel (base + primary) → group_no = master,
// - the optional state channel offset → group_no = master,
// - every secondary channel offset → group_no = master.
//
// The resolved group_no is also written back to the [device.Channel] via
// [device.Channel.AssignGroupNumber] so [CreateCustomDataPoint] can read it
// without going through Device.GroupChannels.
//
// No-op when the profile has no Config or no PrimaryChannel set.
func addChannelGroupsToDevice(dev *device.Device, profile Profile) {
	if profile.Config == nil || !profile.Config.ChannelGroup.PrimaryChannelSet {
		return
	}
	cg := profile.Config.ChannelGroup
	primary := cg.PrimaryChannel

	for _, base := range profile.Channels {
		groupNo := base.Channel + primary
		assignChannelGroup(dev, groupNo, groupNo)

		if cg.StateChannelOffset != nil {
			assignChannelGroup(dev, base.Channel+*cg.StateChannelOffset, groupNo)
		}
		for _, sec := range cg.SecondaryChannels {
			assignChannelGroup(dev, base.Channel+sec, groupNo)
		}
	}
}

// assignChannelGroup writes the group bookkeeping to both the
// device-level group map *and* the channel itself so downstream code
// (Channel.GroupMaster, Room fallback) keeps working without re-walking
// the registry.
//
// Once a channel has been recorded in the channel-to-group map, a later
// profile registration must not overwrite the assignment (mirrors
// `model/device.py:622-623`). Devices that match multiple profiles —
// HmIP-WGT registers IPDimmer on channel 2 and IPSwitch on channel 4, and
// both claim channels 3 and 4 as secondaries — would otherwise take the
// group of the last profile materialised instead of the first, which
// re-points the sub-device split on every reconnect. The first-wins rule
// lives in [device.Channel.AssignGroupNumber] so the check and the write
// cannot be split by a concurrent north-bound read.
//
// `dev.AddChannelToGroup` is forward-only (group → channel-set) so it is
// idempotent on duplicates and remains called every time.
func assignChannelGroup(dev *device.Device, channelNo, groupNo int) {
	dev.AddChannelToGroup(groupNo, channelNo)
	for _, ch := range dev.Channels() {
		if ch.Number == channelNo {
			ch.AssignGroupNumber(groupNo)
		}
	}
}

// relevantChannels mirrors `_get_relevant_channels`
// (`model/custom/definition.py:168`). The relevant set is the
// cross-product of `[primary, *secondary]` × `device_config.channels`,
// translated to absolute channel numbers. We deliberately preserve
// Python's `None`-passthrough for primary by guarding on
// PrimaryChannelSet — when the profile has no primary channel,
// secondary offsets still contribute to the relevant set.
func relevantChannels(profile Profile) map[int]struct{} {
	out := make(map[int]struct{})
	if profile.Config == nil {
		return out
	}
	cg := profile.Config.ChannelGroup
	for _, base := range profile.Channels {
		if cg.PrimaryChannelSet {
			out[base.Channel+cg.PrimaryChannel] = struct{}{}
		}
		for _, sec := range cg.SecondaryChannels {
			out[base.Channel+sec] = struct{}{}
		}
	}
	return out
}

// applyFieldVisibility iterates the profile's primary fields, the rebased
// channel-fields, and both the profile's and Extended-config's fixed-channel
// fields, calling [generic.DataPoint.SetForcedUsage] when the field-mapping
// carries an explicit visibility decision.
//
// The custom-DP construction itself is left to the per-profile [Constructor]
// ( D.12).
func applyFieldVisibility(
	dev *device.Device,
	ch *device.Channel,
	rebased RebasedChannelGroupConfig,
) {
	model := dev.Model
	// Primary-channel fields. The "primary" channel here is the
	// *current* channel (`ch`) when it belongs to the profile's
	// primary channel set; iterating once over rebased.Fields and
	// Targeting `ch` mirrors
	// `self._channel.address` lookup.
	for field, fv := range rebased.Fields {
		applyFieldValueToChannel(ch, field, fv, model)
	}

	// Channel-specific fields (rebased to absolute channel numbers).
	for chNo, fields := range rebased.ChannelFields {
		if chNo == AnyChannelOffset {
			// Python `None` key — applied to the *primary* channel
			// (== ch) verbatim; mirrors `_add_channel_data_points`.
			for field, fv := range fields {
				applyFieldValueToChannel(ch, field, fv, model)
			}
			continue
		}
		target := lookupChannelByNumber(dev, ch, chNo)
		if target == nil {
			continue
		}
		for field, fv := range fields {
			applyFieldValueToChannel(target, field, fv, model)
		}
	}

	// Fixed channel fields from the profile config (already absolute).
	for chNo, fields := range rebased.FixedChannelFields {
		target := lookupChannelByNumber(dev, ch, chNo)
		if target == nil {
			continue
		}
		for field, fv := range fields {
			applyFieldValueToChannel(target, field, fv, model)
		}
	}
}

// applyFieldValueToChannel resolves a [FieldValue] against the channel's
// VALUES paramset and forces the underlying generic DP's usage when the field
// carries an explicit visibility decision.
//
// if is_visible is True and data_point.is_forced_sensor is False:
// data_point.force_usage(forced_usage=DataPointUsage.CDP_VISIBLE)
//
// When the DP is a forced sensor and the field is marked visible, the
// CDP_VISIBLE promotion is intentionally skipped. A forced-sensor DP (e.g.
// HmIP-eTRV LEVEL marked by `_SWITCH_DP_TO_SENSOR`) already surfaces as
// DATA_POINT via [generic.DataPoint.Usage]'s `IsForcedSensor()` head;
// layering CDP_VISIBLE on top would cause a snapshot divergence
// (`forced_usage=ce_visible` in Go vs absent in Python).
//
// The `model` argument is required to consult the static
// [generic.IsForceSensorParameter] table, which is populated at package-init
// time and is not subject to the pipeline ordering that would make the
// instance-method `IsForcedSensor()` return false here (ApplyForceSensorMarks
// runs AFTER materialiseCustomDataPoints).
func applyFieldValueToChannel(ch *device.Channel, field hmenum.Field, fv FieldValue, model string) {
	param, isVisible := ResolveFieldValue(fv)
	if isVisible == nil {
		return
	}
	dp := ch.Parameter(param)
	if dp == nil {
		return
	}
	// Skip CDP_VISIBLE promotion when the DP is a force-sensor parameter. We use
	// the static IsForceSensorParameter table rather than the instance method
	// IsForcedSensor() because MarkForcedSensor() has not yet been called at
	// this point in the pipeline — it is called by ApplyForceSensorMarks which
	// runs after materialiseCustomDataPoints.
	if *isVisible && generic.IsForceSensorParameter(model, param) {
		return
	}
	// The group-STATE field marks the status-transmitter DP a custom entity
	// spans off its primary channel — visible like CDP_VISIBLE for HA / MQTT /
	// REST, but tagged CDP_STATE so the Matter projection can drop this
	// redundant status channel by default without hiding genuine extra
	// CDP_VISIBLE sensors (HUMIDITY, a contact STATE). See
	// [hmenum.DataPointUsageCDPState].
	if *isVisible && field == hmenum.FieldGroupState {
		forceUsageOnDataPoint(dp, hmenum.DataPointUsageCDPState)
		return
	}
	forceDataPointUsage(dp, *isVisible)
}

// markAdditionalDataPoints applies the `_mark_data_points` semantics:
// every parameter listed under `additional_data_points` is force-
// marked with [hmenum.DataPointUsageDataPoint] so it survives the
// CDP_PRIMARY/CDP_SECONDARY filter that would otherwise demote
// generic DPs hidden by a custom DP.
//
// The profile config carries relative offsets (rebased here by
// `groupNo`); the Extended config carries absolute channel numbers
// already.
func markAdditionalDataPoints(dev *device.Device, ch *device.Channel, profile Profile, groupNo int) {
	if profile.Config != nil {
		// IncludeDefaultDataPoints: when the profile carries the flag (default:
		// true), apply the global [DefaultDataPoints] map first. The default DPs
		// cover the common diagnostic surface (LOW_BAT, RSSI_DEVICE,
		// OPERATING_VOLTAGE, BATTERY_STATE, …) on the maintenance channels (0 / 2 /
		// 4) — without this call the custom-DP-suppression pass below would hide
		// them.
		if profile.Config.IncludeDefaultDataPoints {
			for absCh, params := range DefaultDataPoints {
				markParametersOnChannel(dev, ch, absCh, params)
			}
		}
		for relCh, params := range profile.Config.AdditionalDataPoints {
			absCh := relCh + groupNo
			markParametersOnChannel(dev, ch, absCh, params)
		}
	}
	if profile.Extended != nil {
		for absCh, params := range profile.Extended.AdditionalDataPoints {
			markParametersOnChannel(dev, ch, absCh, params)
		}
	}
}

// SuppressUndefinedGenericDataPoints implements
// `_get_data_point_usage` fallback for devices that carry a
// custom-DP definition: every channel's VALUES paramset entry that
// has not been explicitly marked through the profile's visibility
// chain or `additional_data_points` is force-marked with
// [hmenum.DataPointUsageNoCreate]. Mirrors
// `model/generic/data_point.py:_get_data_point_usage`:
//
//	return NO_CREATE if (has_custom_data_point_definition
//	 and not allow_undefined_generic_data_points)
//
// Uses the package-level [DefaultRegistry] for profile lookups.
// Tests with isolated registries should call
// [SuppressUndefinedGenericDataPointsWith] directly.
func SuppressUndefinedGenericDataPoints(dev *device.Device) {
	SuppressUndefinedGenericDataPointsWithExempt(dev, DefaultRegistry(), nil)
}

// UnIgnoreExemption is the narrow contract the suppression pass calls to ask
// whether the visibility decider exempts a DP from the custom-DP suppression
// rule.
//
// Passing nil falls back to the per-DP `IsUnIgnored()` check
// (custom_only=True semantics) — built-in device exemptions are then missed,
// so callers wiring a real visibility registry SHOULD pass one.
type UnIgnoreExemption interface {
	ExemptFromSuppression(model, channelType string, paramset hmenum.ParamsetKey, parameter hmenum.Parameter) bool
}

// SuppressUndefinedGenericDataPointsWith is the registry-aware
// variant of [SuppressUndefinedGenericDataPoints]. Equivalent to
// [SuppressUndefinedGenericDataPointsWithExempt] with `exempt=nil`.
func SuppressUndefinedGenericDataPointsWith(dev *device.Device, registry *Registry) {
	SuppressUndefinedGenericDataPointsWithExempt(dev, registry, nil)
}

// SuppressUndefinedGenericDataPointsWithExempt is the variant that
// honours an [UnIgnoreExemption] in addition to the DP-level
// `IsUnIgnored ` check.
// `_get_data_point_usage` chain that reaches `parameter_is_un_ignored`
// with `custom_only=False`. Callers wiring a real visibility registry
// pass it as the exemption so HM-Sec-Key/HM-Sec-Win DIRECTION/ERROR
// and HmIP-DLD/HmIP-DLP ERROR_JAMMED survive the suppression
//
// Behaviour:
//
// - When dev has no attached custom DP at all (e.g. virtual or
// pure generic-only model) → no-op (the rule only applies to
// custom-DP devices).
// - When *any* attached custom DP's profile carries
// `AllowUndefinedGenericDataPoints=true` → no-op.
// - Otherwise: walk every channel of the device and for every
// VALUES paramset DP that has no [forcer.SetForcedUsage] mark
// yet AND that the exemption does not protect, set NoCreate.
//
// Idempotent.
func SuppressUndefinedGenericDataPointsWithExempt(dev *device.Device, registry *Registry, exempt UnIgnoreExemption) {
	if dev == nil || registry == nil {
		return
	}
	if !deviceHasCustomDP(dev) {
		return
	}
	if deviceAllowsUndefinedDPs(dev, registry) {
		return
	}
	for _, ch := range dev.Channels() {
		// **every** GenericDataPoint regardless of paramset (the
		// `paramset_key` argument flows through to
		// `parameter_visibility_provider.parameter_is_hidden`, but the
		// `has_custom_data_point_definition && !allow_undefined`
		// fallback at the end applies to MASTER and VALUES alike). Earlier
		// versions of this loop iterated `ch.DataPoints()` only — that
		// covered VALUES but missed MASTER, leaving e.g.
		// `HmIP-RGBW/0/MASTER.DEVICE_OPERATION_MODE` as `usage=data_point`
		// In openccu-loom while
		suppressOnDPs := func(dps []device.ParameterDataPoint, paramset hmenum.ParamsetKey) {
			for _, dp := range dps {
				if hasForcedUsage(dp) {
					continue
				}
				// Operator-un-ignored DPs survive the suppression pass.
				if r, ok := dp.(unIgnoredReader); ok && r.IsUnIgnored() {
					continue
				}
				// Built-in `unIgnoreParametersByDevice` exemption: when
				// the visibility decider reports the parameter as
				// un-ignored via the device-level rule (HM-Sec-Key
				// DIRECTION/ERROR, HmIP-DLD ERROR_JAMMED, …), skip the
				// suppression so the DP keeps its un-marked usage
				// `parameter_is_un_ignored(custom_only=False)` short-circuit.
				if exempt != nil && exempt.ExemptFromSuppression(dev.Model, ch.Type, paramset, dp.Parameter()) {
					continue
				}
				if f, ok := dp.(forcer); ok {
					f.SetForcedUsage(hmenum.DataPointUsageNoCreate)
				}
			}
		}
		suppressOnDPs(ch.DataPoints(), hmenum.ParamsetKeyValues)
		suppressOnDPs(ch.MasterDataPoints(), hmenum.ParamsetKeyMaster)
	}
}

// unIgnoredReader is the read-side counterpart to the un-ignore mark.
// Every `*generic.DataPoint[T]` satisfies it through the embedded
// [datapoint.BaseDataPointFields].
type unIgnoredReader interface {
	IsUnIgnored() bool
}

// deviceHasCustomDP reports whether at least one channel of dev carries an
// attached custom data point.
func deviceHasCustomDP(dev *device.Device) bool {
	for _, ch := range dev.Channels() {
		if ch.CustomDataPoint() != nil {
			return true
		}
	}
	return false
}

// deviceAllowsUndefinedDPs reports the aggregate of every attached custom
// DP's profile flag.
//
// The registry argument lets the pipeline use [DefaultRegistry] while tests
// pass their isolated registry.
func deviceAllowsUndefinedDPs(dev *device.Device, registry *Registry) bool {
	customs := 0
	for _, ch := range dev.Channels() {
		if ch.CustomDataPoint() == nil {
			continue
		}
		customs++
		profile, ok := lookupProfileForCustomDP(registry, dev.Model, ch.CustomDataPoint())
		if !ok {
			return false
		}
		if profile.Config == nil {
			return false
		}
		if !profile.Config.ChannelGroup.AllowUndefinedGenericDataPoints {
			return false
		}
	}
	return customs > 0
}

// lookupProfileForCustomDP resolves the [Profile] entry that
// produced a particular custom data point on a device. The match is
// by primary channel number: a custom DP's `DataPointKey` carries
// the channel address, and the profile's `Channels` list (plus
// `PrimaryChannel` offset) determines on which absolute channel
// number the custom DP attaches.
func lookupProfileForCustomDP(registry *Registry, model string, dp device.AttachableDataPoint) (Profile, bool) {
	if dp == nil || registry == nil {
		return Profile{}, false
	}
	// Defensive: a custom DP wrapping a half-formed channel (e.g. a
	// Cover whose LEVEL data point is missing) returns the zero
	// DataPointKey. There is no profile to attach in that case, and
	// the channel-number parsing below would silently match channel
	// 0 of every model otherwise.
	channelAddr := dp.DataPointKey().ChannelAddress
	if channelAddr == "" {
		return Profile{}, false
	}
	chNum := -1
	if i := indexOfColon(channelAddr); i >= 0 && i+1 < len(channelAddr) {
		n, err := atoiSmall(channelAddr[i+1:])
		if err == nil {
			chNum = n
		}
	}
	if chNum < 0 {
		return Profile{}, false
	}
	for _, profile := range registry.GetConfigs(model) {
		if profile.Config == nil {
			continue
		}
		for _, base := range profile.Channels {
			if base.Channel+profile.Config.ChannelGroup.PrimaryChannel == chNum {
				return profile, true
			}
		}
	}
	return Profile{}, false
}

// indexOfColon / atoiSmall are tiny inlined helpers that avoid
// pulling `strings` / `strconv` into this file. The channel
// numbers we deal with never exceed two digits.
func indexOfColon(s string) int {
	for i := range len(s) {
		if s[i] == ':' {
			return i
		}
	}
	return -1
}

func atoiSmall(s string) (int, error) {
	if s == "" {
		return 0, errEmpty
	}
	n := 0
	for i := range len(s) {
		c := s[i]
		if c < '0' || c > '9' {
			return 0, errBadDigit
		}
		n = n*10 + int(c-'0')
	}
	return n, nil
}

// hasForcedUsage reports whether the data point has had its forced
// usage set via [forcer.SetForcedUsage]. Returns false for DPs that
// don't implement the forcer contract (e.g. test fakes); those are
// also exempt from the suppression because the helper cannot mark
// them.
func hasForcedUsage(dp device.ParameterDataPoint) bool {
	if r, ok := dp.(forcedUsageReader); ok {
		_, set := r.ForcedUsage()
		return set
	}
	return false
}

// forcedUsageReader is the read-side companion to [forcer]; every
// `*generic.DataPoint[T]` satisfies it through the embedded base.
type forcedUsageReader interface {
	ForcedUsage() (hmenum.DataPointUsage, bool)
}

// markParametersOnChannel resolves the absolute channel number to
// the device's [device.Channel] and force-usage-marks every named
// parameter.
func markParametersOnChannel(
	dev *device.Device,
	ch *device.Channel,
	channelNo int,
	params []hmenum.Parameter,
) {
	target := lookupChannelByNumber(dev, ch, channelNo)
	if target == nil {
		return
	}
	for _, param := range params {
		dp := target.Parameter(param)
		if dp == nil {
			continue
		}
		forceUsageOnDataPoint(dp, hmenum.DataPointUsageDataPoint)
	}
}

// lookupChannelByNumber returns the device's channel whose Number
// equals chNo. Returns nil when no such channel exists. The current
// channel `ch` is preferred (avoids walking the device when chNo
// matches it) — this mirrors Python's `device.get_generic_data_point`
// which looks up by composed channel address.
func lookupChannelByNumber(dev *device.Device, ch *device.Channel, chNo int) *device.Channel {
	if ch != nil && ch.Number == chNo {
		return ch
	}
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

// forceDataPointUsage maps the FieldMapping's `IsVisible` to a
// concrete [hmenum.DataPointUsage] and applies it via
// [forceUsageOnDataPoint]. `true` → CDP_VISIBLE, `false` → NO_CREATE.
func forceDataPointUsage(dp device.ParameterDataPoint, isVisible bool) {
	usage := hmenum.DataPointUsageNoCreate
	if isVisible {
		usage = hmenum.DataPointUsageCDPVisible
	}
	forceUsageOnDataPoint(dp, usage)
}

// forceUsageOnDataPoint applies [generic.DataPoint.SetForcedUsage]
// when the underlying typed DP exposes the method. The
// [device.ParameterDataPoint] interface intentionally does not
// publish SetForcedUsage (it would force every implementation — including
// fakes used in tests — to carry the override). Instead we type-
// switch over the concrete generic specialisations our domain emits.
//
// Forwarding through a tiny interface (`forcer`) keeps the switch
// open for future generic specialisations without touching the
// materializer call sites.
func forceUsageOnDataPoint(dp device.ParameterDataPoint, usage hmenum.DataPointUsage) {
	if f, ok := dp.(forcer); ok {
		f.SetForcedUsage(usage)
	}
}

// forcer is the minimal contract a [device.ParameterDataPoint]
// satisfies to participate in custom-DP visibility forcing. Every
// `*generic.DataPoint[T]` implements it — see WX-D.11.
type forcer interface {
	SetForcedUsage(usage hmenum.DataPointUsage)
}

// errEmpty / errBadDigit guard the tiny inlined atoi helper.
var (
	errEmpty    = errors.New("custom: empty channel id")
	errBadDigit = errors.New("custom: non-numeric channel id")
)

// Compile-time assertion: the generic DataPoint specialisations the
// domain layer emits actually implement [forcer].
var (
	_ forcer = (*generic.DataPoint[bool])(nil)
	_ forcer = (*generic.DataPoint[int32])(nil)
	_ forcer = (*generic.DataPoint[float64])(nil)
	_ forcer = (*generic.DataPoint[string])(nil)
)
