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

// Kinds of subject on the Home Assistant discovery plane.
const (
	// DiscoveryKindDataPoint is one parameter of a channel.
	DiscoveryKindDataPoint = "data_point"
	// DiscoveryKindChannelEntity is the entity of a channel's custom
	// data point.
	DiscoveryKindChannelEntity = "channel_entity"
	// DiscoveryKindEvent is a channel's press event entity.
	DiscoveryKindEvent = "event"
	// DiscoveryKindSchedule is a channel's schedule sensor.
	DiscoveryKindSchedule = "schedule"
	// DiscoveryKindScheduleSwitch is one per-target-channel switch of a
	// schedule.
	DiscoveryKindScheduleSwitch = "schedule_switch"
	// DiscoveryKindCombined is a combined data point's entity.
	DiscoveryKindCombined = "combined"
	// DiscoveryKindUpdate is the device's firmware-update entity.
	DiscoveryKindUpdate = "update"
	// DiscoveryKindCompanion is an entity announced alongside another:
	// the press button of a writable press, the notify entity of a text
	// display.
	DiscoveryKindCompanion = "companion"
)

// Reasons of the discovery plane beyond the builder's and the bridge's.
const (
	// DiscoveryReasonDeviceNotReleased: the device has not been released
	// by the onboarding step, so nothing of it is announced yet.
	DiscoveryReasonDeviceNotReleased = "device_not_released"
	// DiscoveryReasonCentralNotReady: the central is still coming up; the
	// device is announced with the central's snapshot.
	DiscoveryReasonCentralNotReady = "central_not_ready"
)

// Identity keys of an announced entity.
const (
	DiscoveryIdentityComponent = "component"
	DiscoveryIdentityNodeID    = "node_id"
	DiscoveryIdentityObjectID  = "object_id"
	// DiscoveryIdentityTopic is the entity's config topic. Absent when
	// the bridge publishes one document per device.
	DiscoveryIdentityTopic = "discovery_topic"
)

// DiscoveryPlane is the Home Assistant discovery side of a device's
// projection. It visits the paths of the device snapshot that announce an
// entity, in the snapshot's order, and asks with the events the snapshot
// publishes with, so it describes what the snapshot announces.
type DiscoveryPlane struct {
	bridge *EventBridge
}

// NewDiscoveryPlane returns the plane over b. A nil bridge answers
// inactive.
func NewDiscoveryPlane(b *EventBridge) *DiscoveryPlane { return &DiscoveryPlane{bridge: b} }

// Plane implements [projection.Source].
func (*DiscoveryPlane) Plane() string { return projection.PlaneHADiscovery }

// Project implements [projection.Source].
func (p *DiscoveryPlane) Project(_ context.Context, central string, _ []*device.Device, dev *device.Device) (projection.PlaneView, error) {
	view := projection.PlaneView{Plane: projection.PlaneHADiscovery, State: projection.StateActive, Entries: []projection.Entry{}}
	var bridge *mqtt.Bridge
	if p != nil && p.bridge != nil && p.bridge.mqtt != nil {
		bridge = p.bridge.mqtt.Bridge()
	}
	if bridge == nil || !bridge.DiscoveryEnabled() {
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
	w := discoveryWalk{b: b, bridge: bridge, central: central, dev: dev, seen: map[string]bool{}}
	w.device()
	view.Entries = w.entries
	return view, nil
}

// discoveryWalk describes one device. seen keeps an entity that several
// subjects ask for (a channel's entity, asked for by each of its observed
// data points and once directly) to one entry.
type discoveryWalk struct {
	b       *EventBridge
	bridge  *mqtt.Bridge
	central string
	dev     *device.Device
	entries []projection.Entry
	seen    map[string]bool
}

// device visits the announcing paths of
// [EventBridge.publishDeviceSnapshot] in its order. The snapshot's week
// profile step is not among them: it publishes a state and announces no
// entity. A path the snapshot gains has to be added here; the round trip
// against the broker shows a missing one only where the fleet has it.
func (w *discoveryWalk) device() {
	d, iface := w.dev, w.dev.InterfaceID
	for _, ch := range d.Channels() {
		if ch == nil {
			continue
		}
		_, channelNo := parseChannel(ch.Address)
		for _, dp := range ch.DataPoints() {
			w.dataPoint(ch, channelNo, string(dp.Parameter()), hmenum.ParamsetKeyValues, dp)
		}
		for _, dp := range ch.MasterDataPoints() {
			w.dataPoint(ch, channelNo, string(dp.Parameter()), hmenum.ParamsetKeyMaster, dp)
		}
		for _, dp := range ch.CalculatedDataPoints() {
			pdp, ok := dp.(interface {
				RawValue() (any, bool)
				Parameter() hmenum.Parameter
			})
			if !ok {
				continue
			}
			w.dataPoint(ch, channelNo, string(pdp.Parameter()), hmenum.ParamsetKeyValues, pdp)
		}
		if ev, _, ok := scheduleEntityDiscoveryEvent(w.central, iface, d, ch); ok {
			w.add(channelNo, DiscoveryKindSchedule, "", w.bridge.DescribeScheduleEntityDiscovery(w.central, ev))
			wp := ch.WeekProfile()
			switches := w.b.scheduleSwitchDiscoveryEvents(w.central, iface, d, channelNo, wp)
			for i := range switches {
				w.add(channelNo, DiscoveryKindScheduleSwitch, switches[i].Key, w.bridge.DescribeScheduleSwitchDiscovery(w.central, switches[i]))
			}
		}
		for _, cdp := range ch.CombinedDataPoints() {
			proj, ok := cdp.(payload.CombinedProjection)
			if !ok {
				continue
			}
			if ev, ok := w.b.combinedDiscoveryEvent(w.bridge.Topics(), w.central, iface, d, ch, channelNo, proj); ok {
				w.add(channelNo, DiscoveryKindCombined, ev.Kind, w.bridge.DescribeCombinedDiscovery(w.central, ev))
			}
		}
		if ev, ok := channelEventDiscoveryEvent(w.central, iface, d, ch); ok {
			w.add(channelNo, DiscoveryKindEvent, "", w.bridge.DescribeChannelEventDiscovery(ev))
		}
		if ev, ok := w.b.customDPDiscoveryEvent(w.central, iface, d, ch); ok {
			w.add(channelNo, DiscoveryKindChannelEntity, "", w.bridge.DescribeCustomDPDiscovery(ev))
		}
	}
	if ev, ok := updateDiscoveryEvent(w.central, iface, d); ok {
		w.add(device.ChannelNumberDevice, DiscoveryKindUpdate, "", w.bridge.DescribeUpdateDiscovery(w.central, ev))
	}
}

// dataPoint describes one parameter: the event the bridge builds for it
// ([EventBridge.buildPublishEventWhy]), the entities that event asks for
// ([announcedEvents]), and the MQTT bridge's description of each.
func (w *discoveryWalk) dataPoint(ch *device.Channel, channelNo int, parameter string, paramset hmenum.ParamsetKey, dp interface {
	RawValue() (any, bool)
},
) {
	d := w.dev
	raw, observed := dp.RawValue()
	if !observed {
		raw = nil
	}
	chAddr, _ := parseChannel(ch.Address)
	key := hmtypes.DataPointKey{InterfaceID: d.InterfaceID, ChannelAddress: ch.Address, ParamsetKey: paramset, Parameter: parameter}
	ev, _, ok, eligible, why := w.b.buildPublishEventWhy(w.central, d.InterfaceID, d.Address, chAddr, channelNo, d.Model, d.Name(), key, raw, paramset)
	entryKey := parameter
	if paramset == hmenum.ParamsetKeyMaster {
		entryKey = string(hmenum.ParamsetKeyMaster) + ":" + parameter
	}
	if !ok {
		w.entries = append(w.entries, projection.Entry{
			Channel: channelNo, Kind: DiscoveryKindDataPoint, Key: entryKey,
			Verdict: projection.VerdictWithheld, Reason: string(why),
		})
		return
	}
	channelEntity, own := announcedEvents(ev, eligible, observed)
	if channelEntity != nil {
		w.add(channelNo, DiscoveryKindChannelEntity, "", w.bridge.DescribeDataPointDiscovery(*channelEntity))
	}
	if own == nil {
		w.entries = append(w.entries, projection.Entry{
			Channel: channelNo, Kind: DiscoveryKindDataPoint, Key: entryKey,
			Verdict: projection.VerdictWithheld, Reason: string(why),
		})
		return
	}
	w.add(channelNo, DiscoveryKindDataPoint, entryKey, w.bridge.DescribeDataPointDiscovery(*own))
}

// add turns one description into entries: the entity or its reason, and
// each companion. An entity already described is not listed again.
func (w *discoveryWalk) add(channelNo int, kind, key string, d mqtt.DiscoveryDescription) {
	v := d.Verdict
	switch {
	case v.OK && v.ChannelEntity && kind == DiscoveryKindDataPoint:
		// The builder answered the data point's event with the channel's
		// entity: the data point is carried by it.
		w.entries = append(w.entries, projection.Entry{
			Channel: channelNo, Kind: kind, Key: key,
			Verdict: projection.VerdictFolded, FoldedInto: v.Component + "/" + v.ObjectID,
		})
		w.announced(channelNo, DiscoveryKindChannelEntity, "", v.Component, v.NodeID, v.ObjectID)
	case v.OK:
		w.announced(channelNo, kind, key, v.Component, v.NodeID, v.ObjectID)
	case kind == DiscoveryKindChannelEntity && w.hasChannelEntity(channelNo):
		// A second, refused ask for a channel whose entity is listed.
	default:
		w.entries = append(w.entries, projection.Entry{
			Channel: channelNo, Kind: kind, Key: key,
			Verdict: projection.VerdictWithheld, Reason: string(v.Reason),
		})
	}
	for _, c := range d.Companions {
		w.announced(channelNo, DiscoveryKindCompanion, c.Component+"/"+c.ObjectID, c.Component, c.NodeID, c.ObjectID)
	}
}

func (w *discoveryWalk) hasChannelEntity(channelNo int) bool {
	for i := range w.entries {
		if w.entries[i].Channel == channelNo && w.entries[i].Kind == DiscoveryKindChannelEntity {
			return true
		}
	}
	return false
}

// announced lists an announced entity once, under its identity.
func (w *discoveryWalk) announced(channelNo int, kind, key, component, nodeID, objectID string) {
	id := component + "\x00" + nodeID + "\x00" + objectID
	if w.seen[id] {
		return
	}
	w.seen[id] = true
	// A refused ask for the same channel entity that came first is
	// superseded by the announcement.
	if kind == DiscoveryKindChannelEntity {
		kept := w.entries[:0]
		for i := range w.entries {
			e := w.entries[i]
			if e.Channel == channelNo && e.Kind == DiscoveryKindChannelEntity && e.Verdict == projection.VerdictWithheld {
				continue
			}
			kept = append(kept, e)
		}
		w.entries = kept
	}
	identity := map[string]string{
		DiscoveryIdentityComponent: component,
		DiscoveryIdentityNodeID:    nodeID,
		DiscoveryIdentityObjectID:  objectID,
	}
	if topic, ok := w.bridge.DiscoveryConfigTopic(component, nodeID, objectID); ok {
		identity[DiscoveryIdentityTopic] = topic
	}
	w.entries = append(w.entries, projection.Entry{
		Channel: channelNo, Kind: kind, Key: key,
		Verdict: projection.VerdictPublished, Identity: identity,
	})
}
