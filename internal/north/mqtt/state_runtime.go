// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package mqtt

import (
	"context"
	"log/slog"

	hapublisher "github.com/SukramJ/go-hamqtt/publisher"
	hagomqtt "github.com/SukramJ/go-hamqtt/publisher/gomqtt"
)

// runtimeQoS states one of this daemon's configured QoS levels in the shared
// publisher's own [hapublisher.QoS] vocabulary.
//
// It exists because the two types disagree about what zero means. A
// [QoSProfile] field is the wire byte, so `QoS.State` is literally `0` and
// that is the level this daemon has always published its state plane at
// ([DefaultQoS]). In the shared publisher, `0` is [hapublisher.QoSUnset] —
// "no opinion" — and every runtime type there reads it as QoS 1. Handing the
// byte straight over would therefore have raised the whole state plane from
// at-most-once to at-least-once on the first boot after this migration:
// a change to every publish's delivery guarantee, with no payload difference
// anywhere for a golden file to catch.
//
// go-hamqtt v0.27.0 added [hapublisher.QoSAtMostOnce] for exactly this, so
// that "unset" and "deliberately QoS 0" stop being the same value. This
// function is the one place the translation happens, and
// TestEveryMovedStatePublishIsAtMostOnce reads the level back off the
// transport call rather than trusting it.
//
// An unrecognised level maps to [hapublisher.QoSUnset], which the shared
// constructors refuse with a panic naming the field — at the composition
// root, where a bad level is cheap, rather than on a publish at a level
// nobody chose.
func runtimeQoS(q QoS) hapublisher.QoS {
	switch q {
	case QoS0:
		return hapublisher.QoSAtMostOnce
	case QoS1:
		return hapublisher.QoSAtLeastOnce
	case QoS2:
		return hapublisher.QoSExactlyOnce
	default:
		return hapublisher.QoSUnset
	}
}

// newStatePublisher builds the shared state publisher the daemon-level and
// hub planes publish through.
//
// What it adds over a bare client call is a byte dedup gate, an index of the
// retained topics this process holds a value for, and one renderer. The gate
// is the reason the per-datapoint plane is deliberately NOT on it: a
// [pload.PerDPState] carries `modified_at` and `refreshed_at` off the event,
// so every payload on that plane is unique and a byte gate there is inert by
// construction. Of this daemon's retained state shapes it is the only one
// left with a per-emission field — the legacy mirror's publish-time wall
// clock was the other, and that mirror is gone — and it is the whole
// per-datapoint plane, which is where the traffic is. The planes routed here
// carry no such field, so the gate actually fires; the runtime measurement
// behind ADR 0070 counted the shapes.
//
// [hapublisher.StateConfig.Encoding] is deliberately left at its zero value.
// It selects what [hapublisher.StatePublisher.PublishValue] renders, and no
// path in this daemon calls that: every publisher here marshals its own body
// and hands the bytes to [hapublisher.StatePublisher.Publish], because the
// shapes on this plane are a scalar, a JSON aggregate and a bare Home
// Assistant token rather than one envelope. Setting an encoding would
// suggest a rendering decision that nothing reads.
//
// [hapublisher.StateConfig.CommandFilters] is likewise unset, and that one is
// a gap rather than a choice: the guard needs the topic filters this daemon
// subscribes to, and [CommandSubscriber.Start] builds them as inline literals
// with no accessor to read them back from. The invariant is covered by tests
// instead — TestEveryStatePlaneIsDisjointFromCommandSubscriptions sweeps all
// six planes against the really registered filters — so what is missing is
// the runtime half, not the guarantee.
func newStatePublisher(b *Bridge, logger *slog.Logger) *hapublisher.StatePublisher {
	return hapublisher.NewStatePublisher(
		hagomqtt.Split(b.client, lateSubscriber{b: b}),
		hapublisher.StateConfig{
			// Stated, not inherited. [hapublisher.StateFor] would take the
			// runtime's QoS, which is this daemon's *discovery* QoS 1, and
			// silently raise the state plane with it.
			QoS: runtimeQoS(b.cfg.QoS.State),
			// The pulse planes do not publish through here yet, but the
			// default is stated for the same reason the state level is: a
			// pulse is QoS 0 by this daemon's policy, not by omission.
			PulseQoS: hapublisher.QoSAtMostOnce,
			Logger:   logger,
		},
	)
}

// ResetRuntimeGates opens the dedup gates of the shared state and
// availability publishers without forgetting what they carry, so the next
// publish of each remembered topic goes out once even when its value has not
// changed.
//
// This is the reconnect call, and it is not optional. Both gates suppress a
// repeat of the bytes the broker last accepted, which is right while the
// broker still holds them — and wrong the moment it does not. A broker
// restarted without a persistent retained store drops every retained state
// and every availability marker while this process reconnects underneath;
// without this reset the gates would answer "already published" for bytes
// nothing holds any more, and every entity would sit blank, or wrongly
// available, until its value next happened to change. On a sensor that
// reports on change alone that is forever.
//
// It is called from [Bridge.AnnounceOnline], which the lifecycle runs on
// every successful (re)connect, and again from the domain's own boot
// snapshot pass next to the availability cache it clears for the identical
// reason. Twice rather than once because the two hooks are registered on
// different objects and their order is not fixed, and a reset is idempotent
// and cheap: it publishes nothing, it only stops the next publish being
// suppressed.
func (b *Bridge) ResetRuntimeGates() {
	if b == nil {
		return
	}
	if b.state != nil {
		b.state.Reset()
	}
	if b.avail != nil {
		b.avail.Reset()
	}
}

// publishRuntimeState writes one retained daemon- or hub-plane payload
// through the shared state publisher, and keeps this daemon's own
// bookkeeping in step with what the broker actually accepted.
//
// The bookkeeping is split on the gate's answer on purpose. The retained
// topic index has to name the topic either way — a suppressed publish means
// the broker still holds the bytes, so the topic is still one this process
// owns and a retraction still has to reach it — while `messages_sent` counts
// messages, and a publish the gate swallowed is not one. Counting it would
// make the metric describe intent rather than traffic, which is the opposite
// of what an operator reads it for.
func (b *Bridge) publishRuntimeState(ctx context.Context, centralName, topic string, body []byte) error {
	sent, err := b.state.Publish(ctx, topic, body)
	if err != nil {
		b.incPublishErrors(centralName)
		return err
	}
	b.rememberRawTopic(topic)
	if sent {
		b.incMessagesSent(centralName)
	}
	return nil
}

// evictRuntimeState clears one retained daemon- or hub-plane topic and drops
// it from both indexes.
//
// Through [hapublisher.StatePublisher.Evict] rather than a zero-length
// publish, because the shared publisher refuses an empty payload on the
// publish path with [hapublisher.ErrEmptyStatePayload] — a retraction is a
// deletion, not a state, and reaching it by handing over no bytes is how
// this daemon's own nil-valued sysvar used to delete its entity. Evict is
// the way to say it deliberately.
func (b *Bridge) evictRuntimeState(ctx context.Context, centralName string, topics ...string) error {
	if err := b.state.Evict(ctx, topics...); err != nil {
		b.incPublishErrors(centralName)
		return err
	}
	for _, t := range topics {
		b.forgetRawTopic(t)
		b.incMessagesSent(centralName)
	}
	return nil
}
