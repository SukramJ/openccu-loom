// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"context"

	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/north/mqtt"
	"github.com/SukramJ/openccu-loom/internal/north/projection"
	"github.com/SukramJ/openccu-loom/internal/payload"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// Kinds of subject on the raw MQTT plane.
const (
	// RawKindDataPoint is the state of one parameter.
	RawKindDataPoint = "data_point"
	// RawKindDevice is a topic of the device itself.
	RawKindDevice = "device"
	// RawKindChannelState is the aggregate state of a channel's custom
	// data point.
	RawKindChannelState = "channel_state"
	// RawKindWeekProfile is the active profile of a channel's week
	// profile.
	RawKindWeekProfile = "week_profile"
	// RawKindSchedule is a topic of a channel's schedule sensor.
	RawKindSchedule = "schedule"
	// RawKindScheduleSwitch is the state of one schedule switch.
	RawKindScheduleSwitch = "schedule_switch"
	// RawKindCombined is the state of a combined data point.
	RawKindCombined = "combined"
)

// Reasons of the raw plane beyond the event bridge's gate. Both describe
// now and pass by themselves.
const (
	// RawReasonNotObservedYet: the topic is written when a value of the
	// channel arrives, and none has since the daemon started.
	RawReasonNotObservedYet = "not_observed_yet"
	// RawReasonEmptyValue: there is no value to write; an empty retained
	// payload would clear the topic.
	RawReasonEmptyValue = "empty_value"
)

// Keys of a schedule sensor's two topics.
const (
	RawKeyScheduleEntries    = "active_entries"
	RawKeyScheduleAttributes = "attributes"
)

// Keys of the device's own raw topics.
const (
	RawKeyOnline      = "online"
	RawKeyInfo        = "info"
	RawKeyDiagnostics = "diagnostics"
	RawKeyUpdate      = "update"
)

// RawIdentityTopic is the identity key of a raw entry: the retained topic
// its state is published to.
const RawIdentityTopic = "topic"

// RawPlane is the raw MQTT side of a device's projection: the retained
// topics the daemon writes for the device, and for a parameter without
// one the rule that refuses it. It asks the event bridge's own gate and
// names topics through the bridge's topic builder, which the publishers
// use.
type RawPlane struct {
	bridge *EventBridge
}

// NewRawPlane returns the plane over b. A nil bridge answers inactive.
func NewRawPlane(b *EventBridge) *RawPlane { return &RawPlane{bridge: b} }

// Plane implements [projection.Source].
func (*RawPlane) Plane() string { return projection.PlaneMQTTRaw }

// Project implements [projection.Source].
func (p *RawPlane) Project(_ context.Context, central string, _ []*device.Device, dev *device.Device) (projection.PlaneView, error) {
	view := projection.PlaneView{Plane: projection.PlaneMQTTRaw, State: projection.StateActive, Entries: []projection.Entry{}}
	var bridge *mqtt.Bridge
	if p != nil && p.bridge != nil && p.bridge.mqtt != nil {
		bridge = p.bridge.mqtt.Bridge()
	}
	if bridge == nil || !bridge.RawEnabled() || bridge.Topics() == nil {
		view.State, view.Reason = projection.StateInactive, projection.ReasonPlaneDisabled
		return view, nil
	}
	b := p.bridge
	u, ok := b.registry.Get(central)
	if !ok || u == nil || !u.IsSouthboundReady() {
		view.State, view.Reason = projection.StateInactive, DiscoveryReasonCentralNotReady
		return view, nil
	}
	if u.Devices != nil && !u.Devices.IsReleased(hmtypes.ParseWireInterfaceID(dev.InterfaceID), dev.Address) {
		view.Entries = append(view.Entries, projection.Entry{
			Channel: device.ChannelNumberDevice, Verdict: projection.VerdictWithheld, Reason: DiscoveryReasonDeviceNotReleased,
		})
		return view, nil
	}
	topics, iface := bridge.Topics(), dev.InterfaceID
	deviceTopic := func(key, topic string) {
		view.Entries = append(view.Entries, projection.Entry{
			Channel: device.ChannelNumberDevice, Kind: RawKindDevice, Key: key,
			Verdict: projection.VerdictPublished, Identity: map[string]string{RawIdentityTopic: topic},
		})
	}
	deviceTopic(RawKeyOnline, topics.DeviceAvailability(central, iface, dev.Address))
	deviceTopic(RawKeyInfo, topics.DeviceInfo(central, iface, dev.Address))
	deviceTopic(RawKeyDiagnostics, topics.DeviceDiagnostics(central, iface, dev.Address))
	if dev.Update() != nil {
		deviceTopic(RawKeyUpdate, topics.DeviceUpdateState(central, iface, dev.Address))
	}
	for _, ch := range dev.Channels() {
		if ch == nil {
			continue
		}
		_, channelNo := parseChannel(ch.Address)
		for _, dp := range ch.DataPoints() {
			view.Entries = append(view.Entries, p.dataPoint(topics, central, dev, ch, channelNo, string(dp.Parameter()), hmenum.ParamsetKeyValues))
		}
		for _, dp := range ch.MasterDataPoints() {
			view.Entries = append(view.Entries, p.dataPoint(topics, central, dev, ch, channelNo, string(dp.Parameter()), hmenum.ParamsetKeyMaster))
		}
		for _, dp := range ch.CalculatedDataPoints() {
			pdp, ok := dp.(interface{ Parameter() hmenum.Parameter })
			if !ok {
				continue
			}
			view.Entries = append(view.Entries, p.dataPoint(topics, central, dev, ch, channelNo, string(pdp.Parameter()), hmenum.ParamsetKeyValues))
		}
		view.Entries = append(view.Entries, p.channelTopics(bridge, central, dev, ch, channelNo)...)
	}
	return view, nil
}

// channelTopics are the entries of a channel beyond its parameters: the
// aggregate state of its custom data point, its week profile, schedule
// sensor and switches, and its combined data points.
func (p *RawPlane) channelTopics(bridge *mqtt.Bridge, central string, d *device.Device, ch *device.Channel, channelNo int) []projection.Entry {
	topics, iface := bridge.Topics(), d.InterfaceID
	var out []projection.Entry
	published := func(kind, key, topic string) {
		out = append(out, projection.Entry{
			Channel: channelNo, Kind: kind, Key: key,
			Verdict: projection.VerdictPublished, Identity: map[string]string{RawIdentityTopic: topic},
		})
	}
	withheld := func(kind, key, reason string) {
		out = append(out, projection.Entry{Channel: channelNo, Kind: kind, Key: key, Verdict: projection.VerdictWithheld, Reason: reason})
	}
	// The aggregate state is written with a value of the channel, not by
	// a rule that can be asked, so the bridge's own record of what it
	// wrote is the answer.
	if slot, _, ok := customDPStateSlot(ch, d.Address, channelNo); ok {
		for _, topic := range []string{topics.SlotState(central, iface, slot), topics.SlotHAState(central, iface, slot)} {
			switch {
			case topic == "":
			case bridge.HoldsRawTopic(topic):
				published(RawKindChannelState, slot.Parameter, topic)
			default:
				withheld(RawKindChannelState, slot.Parameter, RawReasonNotObservedYet)
			}
		}
	}
	if ev, wp, ok := scheduleEntityDiscoveryEvent(central, iface, d, ch); ok {
		if wp.CurrentProfile() != "" {
			published(RawKindWeekProfile, "", topics.WeekProfileState(central, iface, d.Address, channelNo))
		} else {
			withheld(RawKindWeekProfile, "", RawReasonEmptyValue)
		}
		published(RawKindSchedule, RawKeyScheduleEntries, topics.ScheduleEntityState(central, iface, d.Address, ev.ChannelNo))
		published(RawKindSchedule, RawKeyScheduleAttributes, topics.ScheduleEntityAttrs(central, iface, d.Address, ev.ChannelNo))
		switches := p.bridge.scheduleSwitchDiscoveryEvents(central, iface, d, channelNo, wp)
		for i := range switches {
			published(RawKindScheduleSwitch, switches[i].Key, topics.ScheduleSwitchState(central, iface, d.Address, channelNo, switches[i].Key))
		}
	}
	for _, cdp := range ch.CombinedDataPoints() {
		proj, ok := cdp.(payload.CombinedProjection)
		if !ok {
			continue
		}
		kind := proj.CombinedKind()
		if kind == "" {
			continue
		}
		if _, observed := proj.CombinedStatePayload(); observed {
			published(RawKindCombined, kind, topics.CombinedState(central, iface, d.Address, channelNo, kind))
		} else {
			withheld(RawKindCombined, kind, RawReasonNotObservedYet)
		}
	}
	return out
}

// dataPoint is the entry of one parameter: its state topic when the event
// bridge's gate lets it onto the plane, the gate's reason when not. The
// state is written whether or not a value has been observed, so the
// answer does not depend on one.
func (p *RawPlane) dataPoint(topics *mqtt.TopicBuilder, central string, d *device.Device, ch *device.Channel, channelNo int, parameter string, paramset hmenum.ParamsetKey) projection.Entry {
	chAddr, _ := parseChannel(ch.Address)
	key := hmtypes.DataPointKey{InterfaceID: d.InterfaceID, ChannelAddress: ch.Address, ParamsetKey: paramset, Parameter: parameter}
	entryKey := parameter
	if paramset == hmenum.ParamsetKeyMaster {
		entryKey = string(hmenum.ParamsetKeyMaster) + ":" + parameter
	}
	e := projection.Entry{Channel: channelNo, Kind: RawKindDataPoint, Key: entryKey}
	if _, _, ok, _, why := p.bridge.buildPublishEventWhy(central, d.InterfaceID, d.Address, chAddr, channelNo, d.Model, d.Name(), key, nil, paramset); !ok {
		e.Verdict, e.Reason = projection.VerdictWithheld, string(why)
		return e
	}
	e.Verdict = projection.VerdictPublished
	e.Identity = map[string]string{RawIdentityTopic: topics.SlotState(central, d.InterfaceID, dataPointSlot(ch, d.Address, channelNo, key))}
	return e
}
