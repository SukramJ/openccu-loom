// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package mqtt

import (
	"context"
	"errors"
	"log/slog"

	"github.com/SukramJ/openccu-loom/internal/model/naming"
)

// HABirthTopic is the topic Home Assistant publishes its lifecycle events
// on. HA emits "online" once the integration boots and "offline" before the
// broker disconnects it. The daemon listens there and re-publishes every
// Discovery payload whenever HA comes back online — the retained configs
// stay in the broker, but HA does not reliably re-read them across every
// addon reload and firmware update.
//
// It shares HA's Discovery root with the config topics but not their
// grammar: this is HA's own lifecycle topic, not a `.../config` entry, so it
// is built from the prefix rather than from [naming.DiscoveryConfigTopic].
// TestHABirthTopicMatchesRuntime pins it against the shared runtime's own
// rendering of the same topic, which is what the subscription actually uses.
const HABirthTopic = naming.DiscoveryTopicPrefix + "status"

// BirthSync subscribes to `homeassistant/status` and re-publishes every
// declared Discovery config on the rising edge ("online").
//
// It is a seam now rather than an implementation. The subscription, the
// payload check, the hand-off off the read loop and the replay itself are
// [hapublisher.Runtime.WatchBirth]; the runtime holds the declared payloads,
// so the layer that used to route the event to them has nothing left to
// carry. What stays is this daemon's wiring shape: a constructor the
// composition root calls, a Start that reports a rejected subscribe, and a
// Close that drains.
//
// The dispatcher behind it changed with the move. This layer ran a
// depth-4 queue in front of one worker; the runtime collapses a burst onto a
// single pending job instead. Every job is a full idempotent replay, so
// running the second after the first changes nothing — and a queue that
// filled up would push the blocking back onto the read loop the dispatcher
// exists to keep free.
type BirthSync struct {
	sub    Subscriber
	bridge *Bridge
	logger *slog.Logger
}

// NewBirthSync constructs the listener. `bridge` and `sub` must outlive the
// lifecycle of the daemon. Call [BirthSync.Close] on teardown to drain an
// in-flight replay.
func NewBirthSync(sub Subscriber, bridge *Bridge, logger *slog.Logger) *BirthSync {
	if logger == nil {
		logger = slog.Default()
	}
	return &BirthSync{sub: sub, bridge: bridge, logger: logger}
}

// Close stops accepting new "online" events and blocks until an in-flight or
// queued replay has finished. Safe to call on a nil *BirthSync and safe to
// call twice.
//
// Draining is the whole teardown, where this layer also used to cancel the
// replay's context. The runtime detaches the replay from the delivery on
// purpose — a replay that dies with the message that triggered it replays
// nothing — so a shutdown waits it out instead. It is bounded in practice:
// once the client is down every remaining publish fails fast.
func (b *BirthSync) Close() {
	if b == nil || b.bridge == nil {
		return
	}
	b.bridge.pub.Close()
}

// Start attaches the subscription. Returns an error when the transport
// rejects the subscribe; otherwise the runtime's watch runs in the
// background until the subscription goes away.
func (b *BirthSync) Start(ctx context.Context) error {
	if b.sub == nil || b.bridge == nil {
		return errors.New("mqtt/birth_sync: subscriber or bridge missing")
	}
	if err := b.bridge.pub.WatchBirth(ctx); err != nil {
		return err
	}
	b.logger.Debug("mqtt.birth_sync.watching", slog.String("topic", HABirthTopic))
	return nil
}
