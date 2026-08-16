// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package rpcserver

import "time"

// CallbackObserver receives one pair of observations per inbound callback
// the listener was able to route.
//
// Both callback listeners are daemon-wide while metrics are scoped per
// central, so every observation carries the routing key the listener already
// resolved for the request — the central name for XML-RPC (`/RPC2/<name>`),
// the interface id for BIN-RPC (carried in every envelope). Mapping that key
// onto a central's metrics sink is the composition root's job; this package
// deliberately knows neither the metric names nor the central registry.
//
// A callback the listener cannot route is not observed at all: it belongs to
// no central, and charging it to one would show a healthy CCU as failing.
// Those are counted by the listener's own health counters instead.
//
// Implementations are called from the request path of every callback and
// must not block.
type CallbackObserver interface {
	// CallbackStarted reports that dispatch of a callback routed to
	// routeKey has begun. Paired with exactly one CallbackFinished, so an
	// implementation can derive how many callbacks are in flight per
	// central rather than per (shared) listener.
	CallbackStarted(routeKey string)

	// CallbackFinished reports the completed dispatch, how long it took,
	// and whether it failed.
	CallbackFinished(routeKey string, duration time.Duration, failed bool)
}

// observeCallbackStart is the nil-safe start call site used by both
// listeners. It returns the finish function so the two halves cannot drift
// apart at the call site.
func observeCallbackStart(obs CallbackObserver, routeKey string) func(failed bool) {
	if obs == nil || routeKey == "" {
		return func(bool) {}
	}
	obs.CallbackStarted(routeKey)
	started := time.Now()
	return func(failed bool) {
		obs.CallbackFinished(routeKey, time.Since(started), failed)
	}
}
