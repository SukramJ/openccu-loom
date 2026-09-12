// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package mqtt

import (
	"context"
	"log/slog"

	hapublisher "github.com/SukramJ/go-hamqtt/publisher"
	hagomqtt "github.com/SukramJ/go-hamqtt/publisher/gomqtt"

	"github.com/SukramJ/openccu-loom/internal/model/naming"
)

// newDiscoveryRuntime builds the shared publisher runtime this bridge keeps
// its retained discovery state in.
//
// The runtime owns what used to be `b.declared` and `b.announced`: the
// hash-dedup gate, the claim taken before a publish reaches the broker, the
// birth replay and the orphan sweep. ADR 0070 put it in the shared module
// precisely so the six consumers stop each keeping their own copy of those
// four maps and the ordering rules around them.
//
// What stays on this side is what is not policy: the per-central metric
// labels, the validity counter, the bundle batching and the ownership
// predicate the sweep is scoped by. Those are this daemon's, and the runtime
// takes a callback or a parameter for each rather than a guess.
func newDiscoveryRuntime(b *Bridge, logger *slog.Logger) *hapublisher.Runtime {
	return hapublisher.New(hagomqtt.Split(b.client, lateSubscriber{b: b}), hapublisher.Config{
		Prefix: naming.DiscoveryTopicPrefix,
		// The daemon's own availability topic, in the daemon's own tree —
		// the one the Last Will clears and every discovery payload's
		// `availability` entry names. Handing it to the runtime is what
		// makes [hapublisher.Runtime.Will] describe the will the composition
		// root actually configures; TestDiscoveryRuntimeWillMatchesLWT pins
		// the two together.
		StatusTopic: b.topics.BridgeStatus(),
		QoS:         byte(b.cfg.QoS.Discovery),
		OnResync: func(replayed int, err error) {
			if err != nil {
				logger.Warn("mqtt.birth_sync.republish", slog.String("err", err.Error()))
				return
			}
			logger.Info("mqtt.birth_sync.republished", slog.Int("replayed", replayed))
		},
		Logger: logger,
	})
}

// lateSubscriber resolves the bridge's subscribe-capable client at call time
// rather than at construction.
//
// [hagomqtt.Split] wants the publisher and the subscriber up front, and this
// bridge has only the first of them when [NewBridge] runs: production wires
// the subscribe path afterwards through [Bridge.WithSubscriber], because the
// publish path it was handed is a publish-only circuit-breaker decorator.
// Resolving per call also keeps the existing fallback — a publish client that
// happens to satisfy [Client], which is how the tests and the no-broker
// wiring reach the sweeps — in the one place that already decides it.
type lateSubscriber struct{ b *Bridge }

// Subscribe implements [Subscriber].
func (s lateSubscriber) Subscribe(
	ctx context.Context,
	filter string,
	qos QoS,
	handler MessageHandler,
	opts ...SubscribeOption,
) (SubscribeResult, error) {
	sub, ok := s.b.cleanupSubscriber()
	if !ok {
		return SubscribeResult{}, errCleanupClientLacksSubscribe
	}
	return sub.Subscribe(ctx, filter, qos, handler, opts...)
}

// Unsubscribe implements [Subscriber].
func (s lateSubscriber) Unsubscribe(ctx context.Context, filter string) error {
	sub, ok := s.b.cleanupSubscriber()
	if !ok {
		return errCleanupClientLacksSubscribe
	}
	return sub.Unsubscribe(ctx, filter)
}

// LastWill is the Last Will the daemon's availability policy assumes,
// rendered by the shared runtime from the same status topic
// [Bridge.AnnounceOnline] and [Bridge.AnnounceOffline] publish to.
//
// It is returned as data because a will belongs to CONNECT, and CONNECT
// happens before a bridge exists — the composition root builds the client
// first. So this is not what configures the will; it is what the will is
// checked against (TestConfiguredLastWillMatchesTheBridgePolicy), which is
// the half that actually failed in the family. Two reference bridges
// configure a will whose topic no published entity references, so the broker
// dutifully writes "offline" on a hard crash and every entity stays
// available forever, showing its last value. A will nobody reads is
// indistinguishable from no will at all.
//
// The QoS the runtime reports is deliberately not carried into the returned
// value: this daemon has always connected with the will at the CONNECT
// default, and raising it would change the wire for no measured gain.
func (b *Bridge) LastWill() (Will, error) {
	w, err := b.pub.Will()
	if err != nil {
		return Will{}, err
	}
	return Will{Topic: w.Topic, Payload: w.Payload, Retain: w.Retain}, nil
}
