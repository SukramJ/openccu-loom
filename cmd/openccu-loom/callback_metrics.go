// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package main

import (
	"strings"
	"sync"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/adapter"
	"github.com/SukramJ/openccu-loom/internal/metrics"
)

// callbackMetrics fans the two shared callback listeners' observations out
// onto the per-central metrics observers, which is what fills the
// `rpc_server` section of `GET /api/v1/diagnostics`. Without it the section
// renders permanent zeroes — and a zero error count next to a zero request
// count reads as a confident 100 % success rate rather than as "nothing was
// measured".
//
// The listeners are daemon-wide and the observers are per-central, so the
// routing key each listener resolved is what re-establishes the scope: the
// central name for XML-RPC, the interface id for BIN-RPC.
type callbackMetrics struct {
	reg *central.Registry

	mu sync.Mutex
	// inFlight counts callbacks currently being dispatched, per central.
	// It is kept here rather than on the listeners because a listener
	// serves every central at once, so its own in-flight count would
	// report the same daemon-wide number for each of them.
	inFlight map[string]int
}

// newCallbackMetrics returns the observer both callback listeners report to.
func newCallbackMetrics(reg *central.Registry) *callbackMetrics {
	return &callbackMetrics{reg: reg, inFlight: make(map[string]int)}
}

// CallbackStarted implements rpcserver.CallbackObserver.
func (m *callbackMetrics) CallbackStarted(routeKey string) {
	name, obs := m.observerFor(routeKey)
	if obs == nil {
		return
	}
	m.mu.Lock()
	m.inFlight[name]++
	n := m.inFlight[name]
	m.mu.Unlock()
	obs.ObserveGauge(metrics.MetricKeys.RPCServerActiveTasks().String(), float64(n))
}

// CallbackFinished implements rpcserver.CallbackObserver.
func (m *callbackMetrics) CallbackFinished(routeKey string, duration time.Duration, failed bool) {
	name, obs := m.observerFor(routeKey)
	if obs == nil {
		return
	}
	m.mu.Lock()
	if m.inFlight[name] > 0 {
		m.inFlight[name]--
	}
	n := m.inFlight[name]
	m.mu.Unlock()

	obs.ObserveGauge(metrics.MetricKeys.RPCServerActiveTasks().String(), float64(n))
	obs.ObserveCounter(metrics.MetricKeys.RPCServerRequest().String(), 1)
	obs.ObserveLatency(metrics.MetricKeys.RPCServerRequestLatency().String(),
		float64(duration.Nanoseconds())/float64(time.Millisecond))
	if failed {
		obs.ObserveCounter(metrics.MetricKeys.RPCServerError().String(), 1)
	}
}

// observerFor resolves a listener routing key to the owning central's
// metrics observer. Returns a nil observer when the key names no live
// central, or when that central has no aggregator wired yet — a callback
// arriving during bring-up is dropped from the metrics rather than
// attributed to the wrong CCU.
func (m *callbackMetrics) observerFor(routeKey string) (string, *metrics.Observer) {
	if routeKey == "" || m.reg == nil {
		return "", nil
	}
	// XML-RPC routes by central name, so the direct lookup hits first.
	if u, ok := m.reg.Get(routeKey); ok {
		return u.Name(), observerOfCentral(u)
	}
	// BIN-RPC routes by the interface id the CCU echoes back. Reduce it
	// with the same function the ingest path uses, so the daemon's own
	// prefix and instance name are stripped exactly once and the result
	// is the canonical `<central>-<interface>` shape.
	var fallback *central.Unit
	ambiguous := false
	for _, u := range m.reg.List() {
		canonical := adapter.CanonicalInterfaceID(u.InstanceName(), u.Name(), routeKey)
		if strings.HasPrefix(canonical, u.Name()+"-") {
			return u.Name(), observerOfCentral(u)
		}
		// The reduction above needs the instance name the id was built
		// with. An id minted by an earlier daemon identity (or by a
		// release that spelled the prefix differently) survives in the
		// CCU's callback registry, so fall back to matching the central
		// name as a dash-delimited segment. Central names may themselves
		// contain dashes, which makes that match ambiguous in principle —
		// so it is only used when exactly one central claims the id.
		if strings.Contains("-"+routeKey+"-", "-"+u.Name()+"-") {
			if fallback != nil {
				ambiguous = true
			}
			fallback = u
		}
	}
	if fallback != nil && !ambiguous {
		return fallback.Name(), observerOfCentral(fallback)
	}
	return "", nil
}

// observerOfCentral returns the central's metrics observer, or nil when the
// aggregator has not been wired yet.
func observerOfCentral(u *central.Unit) *metrics.Observer {
	if u == nil || u.Aggregator == nil {
		return nil
	}
	return u.Aggregator.Observer()
}
