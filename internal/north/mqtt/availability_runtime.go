// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package mqtt

import (
	"context"
	"errors"
	"log/slog"

	hapublisher "github.com/SukramJ/go-hamqtt/publisher"
	hagomqtt "github.com/SukramJ/go-hamqtt/publisher/gomqtt"
)

// newAvailabilityPublisher builds the state publisher the device, CCU,
// program-role, alarm and Security & Safety `online` items flip through.
//
// Under mqtt-smarthome 2.0 a reachability flag is a boolean status item
// (ADR 0083): `{"val": true, "ts": …, "lc": …}`, read by Home Assistant
// through `{{ value_json.val | lower }}`. The shared availability publisher
// writes an online/offline marker and refuses to run under the convention,
// so this plane is a [hapublisher.StatePublisher] of its own, whose `val`
// gate is exactly the transition gate the old publisher kept.
//
// # QoS 1, on the retraction too
//
// This is finding F5 of the runtime measurement. Both halves of an `online`
// item are written only on a transition, so neither is repaired by a later
// publish the way a state is: a lost `false` leaves every entity of a dead
// device showing its last value until something else flips the item, which
// for a crash that suppresses the broker's last will is never — and a lost
// *retraction* leaves a retained `true` standing for a device that no longer
// exists anywhere, a ghost Home Assistant reads on every restart and keeps
// permanently available. A retraction is the write with the least chance of
// ever being retried, because the code that would retry it has just finished
// deleting the thing it described. So both get QoS 1, which is also what
// keeps these items off the state plane's own QoS 0.
//
// [hapublisher.StateConfig.CommandFilters] is wired for the same reason it
// is on [newStatePublisher]: an item inside a command filter is a
// self-inflicted CCU write, and the retraction path is where it would be
// least likely to be noticed.
func newAvailabilityPublisher(b *Bridge, logger *slog.Logger) *hapublisher.StatePublisher {
	return hapublisher.NewStatePublisher(
		hagomqtt.Split(b.client, lateSubscriber{b: b}),
		hapublisher.StateConfig{
			QoS:            hapublisher.QoSAtLeastOnce,
			ExtensionKey:   statusExtKey,
			CommandFilters: commandFilters(b.cfg.Base),
			Logger:         logger,
		},
	)
}

// publishOnline writes one boolean `online` status item through the
// availability publisher and reports whether it was a transition.
func (b *Bridge) publishOnline(ctx context.Context, topic string, online bool) (bool, error) {
	if topic == "" {
		return false, nil
	}
	return b.avail.PublishStatus(ctx, topic, hapublisher.Observation{Value: online})
}

// retractOnline clears one `online` status item at the level it was
// published at and drops it from the publisher's index.
func (b *Bridge) retractOnline(ctx context.Context, topic string) error {
	if topic == "" {
		return nil
	}
	return b.avail.Evict(ctx, topic)
}

// deviceAvailabilityTopic renders one device's `online` status item — the
// string the availability publisher publishes and retracts under, and the
// one every device entity's discovery `availability` list names, both
// derived from [TopicBuilder.DeviceAvailability].
func (b *Bridge) deviceAvailabilityTopic(centralName, iface, address string) (string, error) {
	topic := b.topics.DeviceAvailability(centralName, iface, address)
	if topic == "" {
		return "", errNoDeviceAvailabilityTopic
	}
	return topic, nil
}

// errNoDeviceAvailabilityTopic reports a device coordinate that renders no
// `online` item — an empty address.
var errNoDeviceAvailabilityTopic = errors.New("mqtt: device has no availability topic")

// availabilityBridgeTopic is the instance topic every entity's
// [hamodel.LevelBridge] availability entry names, `<base>/connected`.
func (b *Bridge) availabilityBridgeTopic() string { return b.topics.Connected() }
