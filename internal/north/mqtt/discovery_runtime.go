// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package mqtt

import (
	"context"
	"log/slog"

	hamodel "github.com/SukramJ/go-hamqtt/model"
	hapublisher "github.com/SukramJ/go-hamqtt/publisher"
	hagomqtt "github.com/SukramJ/go-hamqtt/publisher/gomqtt"
	hatopic "github.com/SukramJ/go-hamqtt/topic"

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
	return hapublisher.New(hagomqtt.Split(b.client, lateSubscriber{b: b}), discoveryRuntimeConfig(b, logger))
}

// discoveryRuntimeConfig is the runtime's configuration, split out from
// [newDiscoveryRuntime] so the one invariant it carries — that the status
// topic the runtime publishes to is the topic the declaring side renders
// for [hamodel.LevelBridge] — is assertable without a broker.
func discoveryRuntimeConfig(b *Bridge, logger *slog.Logger) hapublisher.Config {
	return hapublisher.Config{
		Prefix: naming.DiscoveryTopicPrefix,
		// The daemon's own availability topic, in the daemon's own tree —
		// the one the Last Will clears and every discovery payload's
		// `availability` entry names. Handing it to the runtime is what
		// makes [hapublisher.Runtime.Will] describe the will the composition
		// root actually configures; TestDiscoveryRuntimeWillMatchesLWT pins
		// the two together.
		StatusTopic: b.topics.BridgeStatus(),
		// …and the layout is what makes that literal checkable rather than
		// free-form. [hapublisher.New] compares the two and panics at the
		// composition root on a disagreement, which is the only place a
		// disagreement is cheap: this is the one string every entity's
		// availability list references, and under the default
		// `availability_mode: "all"` a single typo greys out the whole fleet
		// with nothing on the wire naming the cause.
		Layout: bridgeStatusLayout{base: b.topics.Base},
		QoS:    runtimeQoS(b.cfg.QoS.Discovery),
		OnResync: func(replayed int, err error) {
			if err != nil {
				logger.Warn("mqtt.birth_sync.republish", slog.String("err", err.Error()))
				return
			}
			logger.Info("mqtt.birth_sync.republished", slog.Int("replayed", replayed))
		},
		Logger: logger,
	}
}

// bridgeStatusLayout is the [hatopic.Layout] the publisher runtime is
// configured with. It answers exactly one question — which topic
// [hamodel.LevelBridge] is — and takes its answer from
// [alarmBridgeStatusTopic], the derivation the daemon-level planes' own
// layouts ([securityTopicLayout], the alarm panel's availability list)
// already use.
//
// That is the whole point of handing the runtime a layout at all. A layout
// whose Bridge() simply returned the same string the config's StatusTopic
// was built from could never disagree with it, so the library's guard
// would be armed against nothing. Pointing it at the declaring side's
// second derivation is what gives it teeth: if that derivation ever stops
// agreeing with [TopicBuilder.BridgeStatus] — a base normalised
// differently, a segment respelled — the daemon refuses to start instead
// of coming up with a fleet whose availability sources nobody writes to.
//
// The other three methods deliberately return the empty string rather than
// a plausible topic. This layout is not a render layout and names no state,
// command or per-entity availability topic; the shared module's own rule is
// that a layout answering for a coordinate it does not own is worse than a
// compile error, because a deterministic topic nobody subscribes to looks
// like an answer.
type bridgeStatusLayout struct{ base string }

var _ hatopic.Layout = bridgeStatusLayout{}

// State implements [hatopic.Layout].
func (bridgeStatusLayout) State(hamodel.Slot) string { return "" }

// Command implements [hatopic.Layout].
func (bridgeStatusLayout) Command(hamodel.Slot) string { return "" }

// Availability implements [hatopic.Layout].
func (bridgeStatusLayout) Availability(hamodel.Slot) string { return "" }

// Bridge implements [hatopic.Layout].
func (l bridgeStatusLayout) Bridge() string { return alarmBridgeStatusTopic(l.base) }

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

// runtimeQoS translates this daemon's QoS into the shared runtime's.
//
// It is not a cast, and the difference is the whole point. In
// [hapublisher.QoS] the zero value means "unset" and resolves to QoS 1,
// because a retained discovery config lost at most-once is a config the
// consumer may never publish again. This daemon's [QoS] zero value means
// QoS 0 — an actual delivery guarantee an operator can choose. A cast
// would turn a configured most-once into an at-least-once silently, on
// every publish, with nothing on the wire or in a log saying so.
//
// go-hamqtt v0.27.0 added [hapublisher.QoSAtMostOnce] for exactly this:
// it sits outside the wire range so "unset" and "deliberately QoS 0"
// stop being the same value. Anything above QoS 2 cannot be produced by
// this daemon's own type, so it is mapped to unset and left for the
// runtime to default rather than being invented here.
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
