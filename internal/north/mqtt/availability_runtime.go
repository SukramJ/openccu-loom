// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package mqtt

import (
	"log/slog"

	hamodel "github.com/SukramJ/go-hamqtt/model"
	hapublisher "github.com/SukramJ/go-hamqtt/publisher"
	hagomqtt "github.com/SukramJ/go-hamqtt/publisher/gomqtt"
	hatopic "github.com/SukramJ/go-hamqtt/topic"
)

// availabilityLayout is the [hatopic.Layout] the shared availability
// publisher renders this daemon's reachability topics with.
//
// It answers one coordinate — [hamodel.LevelDevice] — and it answers it
// through [TopicBuilder.DeviceAvailability], the same derivation the
// declaring side renders every config's `availability` entry from
// ([pload.SlotLayout] reaches it via `HADiscoveryTopics`). That is the whole
// reason the publishing side goes through a layout at all rather than
// formatting the string a second time: the availability topic is the one
// string that has to be identical on both sides, and under the default
// `availability_mode: all` a disagreement does not degrade an entity, it
// makes it permanently unavailable with nothing on the wire naming the cause.
//
// The slot carries the central and the interface in [hamodel.Slot.Scope].
// That is this daemon's own filling: `Scope` is documented as the containers
// a device sits in, and here those are the CCU and the wire interface, which
// are exactly the two segments the topic needs and which
// [discovery.DeviceSlot] cannot supply — nothing in this daemon's entity
// bindings populates `Scope`, so a slot built from a device and an entity
// comes back with the address alone. See [deviceAvailabilitySlot].
//
// State and Command deliberately return the empty string. This is not a
// render layout and names no state or command topic; the shared module's own
// rule is that a layout answering for a coordinate it does not own is worse
// than a compile error, because a deterministic topic nobody subscribes to
// looks like an answer.
type availabilityLayout struct{ topics *TopicBuilder }

var _ hatopic.Layout = availabilityLayout{}

// State implements [hatopic.Layout].
func (availabilityLayout) State(hamodel.Slot) string { return "" }

// Command implements [hatopic.Layout].
func (availabilityLayout) Command(hamodel.Slot) string { return "" }

// Availability implements [hatopic.Layout].
func (l availabilityLayout) Availability(s hamodel.Slot) string {
	if l.topics == nil || s.Address == "" || len(s.Scope) < 2 {
		return ""
	}
	return l.topics.DeviceAvailability(s.Scope[0], s.Scope[1], s.Address)
}

// Bridge implements [hatopic.Layout]. It is the same second derivation
// [bridgeStatusLayout] points at, so the two layouts of one daemon cannot
// disagree about the one topic every entity's availability list references.
func (l availabilityLayout) Bridge() string {
	if l.topics == nil {
		return ""
	}
	return alarmBridgeStatusTopic(l.topics.Base)
}

// deviceAvailabilitySlot is the [hamodel.Slot] one device's reachability
// topic is addressed by: the CCU and the interface as the containers, the
// device address as the address.
//
// It exists rather than [hapublisher.DeviceSlot] because that function reads
// the coordinate off an entity's bindings, and nothing in this daemon fills
// [hamodel.Slot.Scope] — a slot built there comes back carrying the address
// alone, which renders no topic at all here. The two segments this daemon's
// topic tree needs live outside the slot, on the publish call, so they are
// put into the slot on the way in. Every call that names a device
// availability topic goes through here, so the publish and the retraction
// cannot address different strings.
func deviceAvailabilitySlot(centralName, iface, address string) hamodel.Slot {
	return hamodel.Slot{Scope: []string{centralName, iface}, Address: address}
}

// newAvailabilityPublisher builds the shared availability publisher the
// device, program-role, alarm and Security & Safety planes flip through.
//
// # QoS 1, on the retraction too
//
// This is finding F5 of the runtime measurement, and it is the substantive
// change in this step. The three availability *publishes* already pinned
// QoS 1 by stated policy; every availability *retraction* — and the
// program-role publish — used `cfg.QoS.State`, which resolves to QoS 0.
// [hapublisher.AvailabilityConfig.QoS] is deliberately one field for both,
// so adopting the type settles the question rather than leaving it open.
//
// It settles it at QoS 1, and the reasoning is symmetric in a way the old
// split was not. Both halves of an availability topic are written only on a
// transition, so neither is repaired by a later publish the way a state is:
// a lost `offline` leaves every entity of a dead device showing its last
// value until something else flips the topic, which for a crash that
// suppresses the broker's last will is never — and a lost *retraction*
// leaves a retained `online` standing for a device that no longer exists
// anywhere, a ghost Home Assistant reads on every restart and keeps
// permanently available. A retraction is the write with the least chance of
// ever being retried, because the code that would retry it has just finished
// deleting the thing it described. If either half deserves the weaker
// guarantee it is the publish, not the retraction; so both get the stronger
// one, which is also what the state plane's own QoS 0 is being kept away
// from here.
//
// [hapublisher.AvailabilityConfig.CommandFilters] is unset for the same
// reason [hapublisher.StateConfig.CommandFilters] is — see
// [newStatePublisher].
func newAvailabilityPublisher(b *Bridge, logger *slog.Logger) *hapublisher.AvailabilityPublisher {
	return hapublisher.NewAvailability(
		hagomqtt.Split(b.client, lateSubscriber{b: b}),
		hapublisher.AvailabilityConfig{
			Layout: availabilityLayout{topics: b.topics},
			QoS:    hapublisher.QoSAtLeastOnce,
			Logger: logger,
		},
	)
}

// deviceAvailabilityTopic renders one device's reachability topic through the
// availability publisher, which is the string it publishes and retracts under.
func (b *Bridge) deviceAvailabilityTopic(centralName, iface, address string) (string, error) {
	return b.avail.DeviceTopic(deviceAvailabilitySlot(centralName, iface, address))
}

// availabilityBridgeTopic is the daemon status topic the shared availability
// publisher's layout renders — the string every entity's
// [hamodel.LevelBridge] availability entry names.
//
// It exists because that agreement is the one invariant this plane cannot
// get from the library: [hapublisher.AvailabilityConfig] holds no status
// topic of its own, so unlike [hapublisher.New] — which compares
// [hapublisher.Config.StatusTopic] against its layout and panics at the
// composition root on a disagreement — the availability constructor has
// nothing to compare. The check has to be made here instead, and
// TestAvailabilityLayoutAgreesWithTheBridgeStatusTopic makes it.
func (b *Bridge) availabilityBridgeTopic() (string, error) { return b.avail.Bridge() }
