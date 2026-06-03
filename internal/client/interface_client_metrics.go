// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package client

import (
	"sync"
	"time"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// MetricsTotalRequests returns the total number of [InterfaceClient.Call]
// invocations observed on this client. Lock-free; safe to call from any
// Goroutine. Mirrors the `total_requests` counter
// on `Client.statistics`.
func (c *InterfaceClient) MetricsTotalRequests() int {
	return int(c.totalRequests.Load())
}

// MetricsPendingRequests returns the number of currently in-flight
// calls. Lock-free.
func (c *InterfaceClient) MetricsPendingRequests() int {
	return int(c.pendingRequests.Load())
}

// MetricsExecutedRequests returns the number of calls that actually
// reached the transport (excludes coalesced followers). Lock-free.
func (c *InterfaceClient) MetricsExecutedRequests() int {
	return int(c.executedRequests.Load())
}

// MetricsCircuitState returns 0=closed, 1=open, 2=half-open. Mirrors
// The numeric encoding
// `MetricsAggregator.rpc()`.
func (c *InterfaceClient) MetricsCircuitState() int {
	switch c.cfg.Circuit.State() {
	case hmenum.CircuitStateOpen:
		return 1
	case hmenum.CircuitStateHalfOpen:
		return 2
	case hmenum.CircuitStateClosed:
		return 0
	}
	return 0
}

// MetricsLastFailureTime returns the timestamp of the most recent
// failure observed on this client and a boolean reporting whether any
// failure has been recorded yet. Multi-CCU safe: each client tracks
// its own failure time independently of any other [InterfaceClient].
func (c *InterfaceClient) MetricsLastFailureTime() (time.Time, bool) {
	c.failureMu.Lock()
	t := c.lastFailureAt
	c.failureMu.Unlock()
	if t.IsZero() {
		return time.Time{}, false
	}
	return t, true
}

// MetricsClientProvider exposes a slice of [*InterfaceClient] to the
// metrics aggregator without leaking the (potentially central-internal)
// container that owns them. Production code wires every active client
// once per (central, interface) pair via [Register]; the metrics
// aggregator iterates over the snapshot on each tick.
//
// All methods are safe for concurrent use. The provider is multi-CCU
// safe: each [Unit] constructs its own [MetricsClientProvider]
// scoped by `central_name` so client counters never bleed across
// centrals.
type MetricsClientProvider struct {
	centralName string

	mu      sync.RWMutex
	clients map[hmenum.Interface]*InterfaceClient
}

// NewMetricsClientProvider returns an empty provider scoped to the
// given central. centralName must match the owning [Unit] so
// the metrics aggregator can attribute counters correctly.
func NewMetricsClientProvider(centralName string) *MetricsClientProvider {
	return &MetricsClientProvider{
		centralName: centralName,
		clients:     make(map[hmenum.Interface]*InterfaceClient),
	}
}

// CentralName returns the CCU scope this provider is bound to.
func (p *MetricsClientProvider) CentralName() string { return p.centralName }

// Register adds or replaces the client for ic.Interface(). Nil
// arguments are ignored.
func (p *MetricsClientProvider) Register(ic *InterfaceClient) {
	if ic == nil {
		return
	}
	p.mu.Lock()
	p.clients[ic.Interface()] = ic
	p.mu.Unlock()
}

// Deregister drops the client for the given interface. Idempotent.
func (p *MetricsClientProvider) Deregister(iface hmenum.Interface) {
	p.mu.Lock()
	delete(p.clients, iface)
	p.mu.Unlock()
}

// Snapshot returns a stable slice of every registered client. The
// metrics-wiring layer (internal/metrics/wiring) re-exposes this via
// the metrics.ClientForMetrics adapter without creating an import
// cycle.
func (p *MetricsClientProvider) Snapshot() []*InterfaceClient {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make([]*InterfaceClient, 0, len(p.clients))
	for _, ic := range p.clients {
		out = append(out, ic)
	}
	return out
}
