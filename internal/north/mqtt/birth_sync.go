// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package mqtt

import (
	"context"
	"errors"
	"log/slog"
	"strings"
)

// HABirthTopic is the topic Home Assistant publishes its lifecycle
// events on. HA emits "online" once the integration boots and
// "offline" before the broker disconnects it. We listen on this
// topic and re-publish every Discovery payload whenever HA comes
// back online — retained discoveries stay in the broker but HA
// occasionally misses them across firmware-updates / addon reloads.
const HABirthTopic = "homeassistant/status"

// BirthSync subscribes to `homeassistant/status` and re-publishes
// every cached Discovery config on the rising edge ("online"). The
// bridge already keeps the per-topic payload cache, so this layer
// stays a thin event-router.
type BirthSync struct {
	sub          Subscriber
	bridge       *Bridge
	logger       *slog.Logger
	lifecycleCtx context.Context // bounds RepublishDiscovery to daemon lifetime
}

// NewBirthSync constructs the listener. `bridge` and `sub` must
// outlive the lifecycle of the daemon.
func NewBirthSync(sub Subscriber, bridge *Bridge, logger *slog.Logger) *BirthSync {
	if logger == nil {
		logger = slog.Default()
	}
	return &BirthSync{sub: sub, bridge: bridge, logger: logger, lifecycleCtx: context.Background()}
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
	return b.sub.Subscribe(ctx, HABirthTopic, QoS1, b.handle)
}

func (b *BirthSync) handle(topic string, payload []byte, _ bool) {
	// retained is ignored here: HA publishes the `homeassistant/status`
	// online/offline state retained, and the very first subscribe-time
	// replay is exactly the signal we need to republish discovery.
	state := strings.TrimSpace(string(payload))
	if state != "online" {
		// HA emits "offline" pre-restart; nothing to do.
		return
	}
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
