// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package mqtt

import (
	"context"
	"errors"
	"log/slog"
	"strings"

	"github.com/SukramJ/openccu-loom/internal/model/naming"
)

// HABirthTopic is the topic Home Assistant publishes its lifecycle
// events on. HA emits "online" once the integration boots and
// "offline" before the broker disconnects it. We listen on this
// topic and re-publish every Discovery payload whenever HA comes
// back online — retained discoveries stay in the broker but HA
// occasionally misses them across firmware-updates / addon reloads.
//
// It shares HA's Discovery root with the config topics but not their
// grammar: this is HA's own lifecycle topic, not a `.../config` entry,
// so it is built from the prefix rather than from
// [naming.DiscoveryConfigTopic].
const HABirthTopic = naming.DiscoveryTopicPrefix + "status"

// birthDispatchWorkers is 1: RepublishDiscovery is idempotent and there
// is nothing to gain from running two republishes concurrently, so a
// single worker (trivially ordered) is the simplest correct choice.
const birthDispatchWorkers = 1

// birthDispatchQueueDepth bounds how many pending "online" events can
// queue up behind a slow in-flight republish before Enqueue starts
// blocking (with a logged warning) the go-mqtt read loop that delivered
// them. HA does not emit birth events in a tight loop, so a shallow
// queue is enough to absorb a burst without growing unbounded.
const birthDispatchQueueDepth = 4

// birthDispatchKey is the single dispatch key BirthSync uses — every
// republish job is idempotent and there is exactly one worker, so no
// per-message key is needed to preserve order.
const birthDispatchKey = "republish"

// BirthSync subscribes to `homeassistant/status` and re-publishes
// every cached Discovery config on the rising edge ("online"). The
// bridge already keeps the per-topic payload cache, so this layer
// stays a thin event-router.
type BirthSync struct {
	sub          Subscriber
	bridge       *Bridge
	logger       *slog.Logger
	lifecycleCtx context.Context // bounds RepublishDiscovery to daemon lifetime

	// dispatcher runs RepublishDiscovery off the go-mqtt client's
	// synchronous read loop. Without it, handle would call a blocking
	// QoS1 Publish per declared topic on the very goroutine that also
	// processes the PUBACK the broker sends back — a self-deadlock on
	// every HA online birth message. See [boundedDispatcher].
	dispatcher *boundedDispatcher
}

// NewBirthSync constructs the listener. `bridge` and `sub` must
// outlive the lifecycle of the daemon. Call [BirthSync.Close] on
// teardown to drain the dispatcher's worker goroutine cleanly.
func NewBirthSync(sub Subscriber, bridge *Bridge, logger *slog.Logger) *BirthSync {
	if logger == nil {
		logger = slog.Default()
	}
	return &BirthSync{
		sub:          sub,
		bridge:       bridge,
		logger:       logger,
		lifecycleCtx: context.Background(),
		dispatcher:   newBoundedDispatcher(birthDispatchWorkers, birthDispatchQueueDepth, "birth_sync", logger),
	}
}

// Close stops accepting new "online" events and blocks until any
// in-flight or already-queued republish has finished. Safe to call on
// a zero-value or nil *BirthSync.
func (b *BirthSync) Close() {
	if b == nil {
		return
	}
	b.dispatcher.Close()
}

// WithLifecycleContext sets the daemon-lifetime context used by the
// republish handler so that a shutdown mid-republish is cancelled promptly
// rather than running until broker timeout. A nil ctx is ignored.
// Returns the receiver for call-site chaining.
func (b *BirthSync) WithLifecycleContext(ctx context.Context) *BirthSync {
	if ctx != nil {
		b.lifecycleCtx = ctx
	}
	return b
}

// Start attaches the subscription. Returns an error when the
// transport rejects the subscribe; otherwise the loop runs in the
// background until `sub` reports the topic gone.
func (b *BirthSync) Start(ctx context.Context) error {
	if b.sub == nil || b.bridge == nil {
		return errors.New("mqtt/birth_sync: subscriber or bridge missing")
	}
	_, err := b.sub.Subscribe(ctx, HABirthTopic, QoS1, LegacyHandler(b.handle))
	return err
}

// handle runs on the MQTT client's synchronous read loop (the same
// goroutine that processes every PUBACK/PINGRESP), so it must return
// fast. Topic/payload parsing stays inline here (microseconds); the
// actual republish — a blocking QoS1 Publish per declared discovery
// topic, each waiting on a PUBACK only that same read loop can deliver
// — is handed to b.dispatcher so handle can return before it runs.
func (b *BirthSync) handle(topic string, payload []byte, _ bool) {
	// retained is ignored here: HA publishes the `homeassistant/status`
	// online/offline state retained, and the very first subscribe-time
	// replay is exactly the signal we need to republish discovery.
	state := strings.TrimSpace(string(payload))
	if state != "online" {
		// HA emits "offline" pre-restart; nothing to do.
		return
	}
	b.dispatcher.Enqueue(birthDispatchKey, b.republish)
}

// republish runs off the read loop, on a [boundedDispatcher] worker.
func (b *BirthSync) republish() {
	// Derive a per-republish cancellable context from the daemon-lifetime
	// context so a shutdown mid-republish is cancelled promptly instead of
	// running until broker timeout on a detached background context.
	ctx, cancel := context.WithCancel(b.lifecycleCtx)
	defer cancel()
	if err := b.bridge.RepublishDiscovery(ctx); err != nil {
		b.logger.Warn("mqtt.birth_sync.republish",
			slog.String("err", err.Error()))
		return
	}
	b.logger.Info("mqtt.birth_sync.republished")
}
