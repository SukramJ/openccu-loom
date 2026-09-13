// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package mqtt

import (
	"context"
	"errors"
	"log/slog"
	"sync"
)

// SweepSubscriber is the subscribe-only client the bridge's retained-store
// sweeps ride on, and the reason they no longer ride the command plane's.
//
// # Why a second connection
//
// Every sweep installs a broad wildcard — `<base>/#` for the retain scrub,
// the central's whole raw subtree for the orphan pass, `homeassistant/#` for
// the discovery passes — and every one of those overlaps the command plane's
// filters. Two overlapping filters on ONE client is not a cosmetic
// duplication: a broker sends one copy of a PUBLISH per matching
// subscription (MQTT 3.1.1 §4.7.3 / 5.0 §3.3.4), and go-mqtt's
// `TCPClient.dispatch` then re-matches each arriving copy against its whole
// local filter list, so both handlers run for both copies. The command
// handler therefore executes the inbound write TWICE for the length of every
// sweep window — measured against Mosquitto 2.1.2 on both dialects, with
// nothing in any log. An idempotent value write survives that; a
// `PRESS_SHORT`, a program trigger and an alarm arm are each a second
// physical action.
//
// Two other fixes were considered and are worse:
//
//   - MQTT 5.0 Subscription Identifiers (go-mqtt's WithSubscriptionID) let the
//     client attribute a copy to the subscription it arrived for. But they
//     are v5-only, `north.mqtt.protocol_version: "3.1.1"` is an
//     operator-reachable key, and an identifier has to be on EVERY
//     subscription of the client — one identifier-less filter drops the whole
//     message back into the topic-match fallback. That means putting the
//     command router into attributed mode, which [CommandSubscriber.Start]
//     refuses on purpose: the router never retries an attributed route
//     unattributed, so on a v3.1.1 link it would turn the whole command plane
//     into a boot failure.
//   - No Local (go-mqtt's WithNoLocal) does not address this at all. It
//     suppresses only the echo of what THIS connection published; an inbound
//     command is published by Home Assistant, so both copies still arrive.
//
// A separate connection is dialect-independent, needs no cooperation from the
// command plane, and costs one extra broker connection for the seconds a
// sweep is open.
//
// # Lifetime
//
// The client is opened on the first subscription and closed again when the
// last one goes away, so the extra connection exists only while a sweep does.
// It is also dropped whenever an UNSUBSCRIBE fails, which is the structural
// half of the other defect here: go-mqtt returns early from `Unsubscribe` on
// `ErrNotConnected` or an ack timeout WITHOUT removing the local
// registration, and `replaySubscriptions` then re-installs that filter on
// every reconnect for the rest of the process. Tearing the connection down
// and building a fresh client for the next sweep is what makes a failed
// teardown recoverable rather than permanent and silent.
//
// The bridge serialises its sweeps behind one slot, so the reference count
// this keeps is 0 or 1 in practice; it is a count rather than a flag so a
// future concurrent sweep cannot close a connection another one is using.
type SweepSubscriber struct {
	// open builds a fresh, unconnected client. Called once per connection,
	// so a poisoned client is never reused.
	open   func() (Client, Connector)
	logger *slog.Logger

	mu     sync.Mutex
	client Client
	conn   Connector
	active int
}

// NewSweepSubscriber returns a [SweepSubscriber] that builds its connection
// through open. open must return a client that is NOT the command plane's —
// that is the entire point — and must not carry a last will: this connection
// comes and goes with each sweep, and a will on it would publish the bridge's
// `offline` marker every time a sweep ends.
//
// Both halves are checked rather than trusted to this paragraph. The daemon's
// TestTheBridgeRoutesItsSweepsThroughTheSweepConnection reads which subscriber
// the assembled bridge really sweeps on, and its
// TestTheSweepConnectionCarriesNoLastWill reads the CONNECT this connection is
// really dialled with.
func NewSweepSubscriber(open func() (Client, Connector), logger *slog.Logger) *SweepSubscriber {
	if logger == nil {
		logger = slog.Default()
	}
	return &SweepSubscriber{open: open, logger: logger}
}

// Subscribe implements [Subscriber]. It connects the sweep client if this is
// the first live subscription, then installs filter on it.
func (s *SweepSubscriber) Subscribe(
	ctx context.Context, filter string, qos QoS, handler MessageHandler, opts ...SubscribeOption,
) (SubscribeResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.client == nil {
		client, conn := s.open()
		if client == nil || conn == nil {
			return SubscribeResult{}, errors.New("mqtt/sweep: no sweep client available")
		}
		if err := conn.Connect(ctx); err != nil && !errors.Is(err, ErrAlreadyConnected) {
			return SubscribeResult{}, err
		}
		s.client, s.conn = client, conn
	}
	res, err := s.client.Subscribe(ctx, filter, qos, handler, opts...)
	if err != nil {
		if s.active == 0 {
			// Nothing else is using the connection this call opened; do not
			// leave it standing for a sweep that never started.
			s.closeLocked(ctx)
		}
		return res, err
	}
	s.active++
	return res, nil
}

// Unsubscribe implements [Subscriber]. It removes filter and, once the last
// subscription is gone — or as soon as one teardown fails — closes the sweep
// connection, which is what keeps a refused UNSUBSCRIBE from stranding a
// wildcard on a client that replays its subscriptions forever.
func (s *SweepSubscriber) Unsubscribe(ctx context.Context, filter string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.client == nil {
		return nil
	}
	err := s.client.Unsubscribe(ctx, filter)
	if s.active > 0 {
		s.active--
	}
	if err != nil {
		// go-mqtt keeps the local registration on a failed UNSUBSCRIBE and
		// replays it on every reconnect. Drop the whole client so the next
		// sweep starts from an empty filter set instead of inheriting this
		// one for the life of the process.
		s.logger.Warn("mqtt.sweep.unsubscribe_failed",
			slog.String("filter", filter),
			slog.String("effect", "dropping the sweep connection so the filter cannot be replayed"),
			slog.String("err", err.Error()))
		s.active = 0
		s.closeLocked(ctx)
		return err
	}
	if s.active == 0 {
		s.closeLocked(ctx)
	}
	return nil
}

// Close tears the sweep connection down unconditionally. Safe on a nil
// receiver, before the first sweep, and twice.
func (s *SweepSubscriber) Close(ctx context.Context) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.active = 0
	s.closeLocked(ctx)
}

// connected reports whether a sweep connection is currently open. Unexported:
// every caller is a test in this package, and an exported accessor nothing in
// the daemon reaches is dead production surface the dead-code ratchet carries.
func (s *SweepSubscriber) connected() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.client != nil
}

// closeLocked disconnects and forgets the current client. Caller holds mu.
func (s *SweepSubscriber) closeLocked(ctx context.Context) {
	if s.conn != nil {
		// Detached from ctx: a shutdown or a cancelled sweep is exactly when
		// the disconnect must still reach the broker.
		if err := s.conn.Disconnect(context.WithoutCancel(ctx)); err != nil {
			s.logger.Debug("mqtt.sweep.disconnect", slog.String("err", err.Error()))
		}
	}
	s.client, s.conn = nil, nil
}
