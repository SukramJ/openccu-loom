// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package matteradapter

import (
	"context"
	"errors"

	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/store/matterendpoint"
)

// SourceOutcome is what the walk did with one subject of a channel.
type SourceOutcome string

const (
	// OutcomePublished: the subject is described as an endpoint of its own.
	OutcomePublished SourceOutcome = "published"
	// OutcomeFolded: the subject is carried by another endpoint, named
	// in [SourceDecision.Into].
	OutcomeFolded SourceOutcome = "folded"
	// OutcomeWithheld: the subject projects nothing, for
	// [SourceDecision.Reason].
	OutcomeWithheld SourceOutcome = "withheld"
)

// WithheldReason names the rule by which the walk leaves a channel or a
// source out of the Matter projection. The set is closed: the walk states
// one of these or states nothing.
type WithheldReason string

const (
	// WithheldChannelHidden: the operator hid the channel.
	WithheldChannelHidden WithheldReason = "channel_hidden"
	// WithheldSecondaryChannel: a secondary actor channel of a custom
	// data point, collapsed onto the primary unless the operator opts in.
	WithheldSecondaryChannel WithheldReason = "secondary_channel"
	// WithheldUsageIgnored: a service, status or overflow parameter that
	// no surface carries.
	WithheldUsageIgnored WithheldReason = "usage_ignored"
	// WithheldConsumedByParent: a raw constituent the channel's
	// aggregating data point already carries.
	WithheldConsumedByParent WithheldReason = "consumed_by_parent"
	// WithheldSecondaryMember: a secondary member or the group-state
	// transmitter of a custom data point, hidden unless the operator
	// opts in.
	WithheldSecondaryMember WithheldReason = "secondary_member"
	// WithheldNoMapping: the source projects no Matter device.
	WithheldNoMapping WithheldReason = "no_matter_mapping"
	// WithheldNotOnAllowList: the source could be exposed and the
	// operator has not switched it on.
	WithheldNotOnAllowList WithheldReason = "not_on_allow_list"
	// WithheldMeasurementsOff: a measurement of a calculated data point,
	// which the bridge carries only with measurements included.
	WithheldMeasurementsOff WithheldReason = "measurements_not_included"
)

// WithheldReasons is the closed set, for the guards that hold a
// decision's reason to it.
func WithheldReasons() []WithheldReason {
	return []WithheldReason{
		WithheldChannelHidden, WithheldSecondaryChannel, WithheldUsageIgnored,
		WithheldConsumedByParent, WithheldSecondaryMember, WithheldNoMapping,
		WithheldNotOnAllowList, WithheldMeasurementsOff,
	}
}

// FoldedIntoPowerSource is [SourceDecision.Into] for a battery data
// point: it becomes the PowerSource cluster on one of the device's
// endpoints and has no endpoint of its own.
const FoldedIntoPowerSource = "POWER_SOURCE"

// SourceDecision is the walk's decision about one subject: a whole
// channel (Key.DPKind empty) or one source on it. A group endpoint
// ([ButtonGroupDPKey], [ElectricalGroupDPKey]) is a published subject of
// its own, and its members are folded into it.
type SourceDecision struct {
	Key     matterendpoint.SourceKey
	Outcome SourceOutcome
	// Reason is set when Outcome is [OutcomeWithheld].
	Reason WithheldReason
	// Into is set when Outcome is [OutcomeFolded]: the dp_key of the
	// endpoint that carries the subject, or [FoldedIntoPowerSource].
	Into string
}

// DecisionSink receives the decisions of a walk in walk order.
type DecisionSink func(SourceDecision)

// noteGroup records the members of a consolidated endpoint: folded into
// it when the group was described, without a mapping when it was not.
func noteGroup(
	note func(matterendpoint.DPKind, string, SourceOutcome, WithheldReason, string),
	members []device.ParameterDataPoint,
	described bool,
	groupKey string,
) {
	for _, m := range members {
		key := genericDPKeyForMeasurement(m)
		if described {
			note(matterendpoint.DPKindGeneric, key, OutcomeFolded, "", groupKey)
			continue
		}
		note(matterendpoint.DPKindGeneric, key, OutcomeWithheld, WithheldNoMapping, "")
	}
}

// DecideDevice is the walk over one device with its decisions: what
// [Assembler.DescribeDevices] describes as an endpoint, what it folds
// into one, and what it leaves out and why. It is the same pass that
// produces the specs the bridge is built from, run for its decisions
// alone, so a reader of the result and a controller see the same set.
//
// fleet is the central's device list: endpoint labels are resolved
// against it, as they are in the full walk.
func (a *Assembler) DecideDevice(ctx context.Context, centralName string, fleet []*device.Device, dev *device.Device) ([]SourceDecision, error) {
	if centralName == "" {
		return nil, errors.New("matteradapter: DecideDevice needs a central name")
	}
	if dev == nil {
		return nil, nil
	}
	names := a.cfg.NameResolver
	if names == nil {
		names = newModelNameResolver(fleet, a.cfg.Labels, a.channelLabel())
	}
	var out []SourceDecision
	sink := func(d SourceDecision) { out = append(out, d) }
	for _, ch := range dev.Channels() {
		if ch == nil {
			continue
		}
		if _, err := a.channelSpecs(ctx, centralName, names, dev, ch, sink); err != nil {
			return nil, err
		}
	}
	return out, nil
}
