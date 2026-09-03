// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package client

import (
	"github.com/SukramJ/openccu-loom/internal/payload"
)

// Compile-time guarantee that *InterfaceClient satisfies the universal
// Source contract. ADR 0007 step 8 — top-level service.
//
// The write-half is provided by the embedded [payload.ServiceRegistry]
// in the [InterfaceClient] struct; service methods (e.g. `ping`,
// `reconnect`) are registered by adapters that wire transport
// specifics — the core client stays agnostic about which named
// operations the bridge exposes.
var _ payload.Source = (*InterfaceClient)(nil)

// Info returns the typed identity payload for the InterfaceClient.
func (c *InterfaceClient) Info() payload.InfoPayload {
	if c == nil {
		return nil
	}
	return &payload.InterfaceClientInfo{
		Central:   c.cfg.CentralName,
		Interface: string(c.cfg.Interface),
	}
}

// Config returns the static client configuration the daemon runs
// with: capability profile. Detailed reliability tuning (throttle /
// retry / circuit policy) lives in the operator config file.
func (c *InterfaceClient) Config() payload.ConfigPayload {
	if c == nil {
		return nil
	}
	caps := c.cfg.Capabilities
	return &payload.InterfaceClientConfig{
		RPCCallback:    caps.RPCCallback,
		PingPong:       caps.PingPong,
		ListDevices:    caps.ListDevices,
		GetAllPrograms: caps.GetAllPrograms,
		GetAllSysvars:  caps.GetAllSysvars,
	}
}

// stateTimestampLayout is the RFC3339 spelling the client state payload emits
// for its two timestamps: UTC with the fractional second padded to a fixed
// nine digits.
//
// It is deliberately not [time.RFC3339Nano], which trims trailing zeros and
// therefore varies its width with the value — an instant at .5 s renders as
// ".500000000Z" through this layout and ".5Z" through that one. Both re-parse
// to the same instant; only a consumer that string-compares or assumes a
// fixed width can tell them apart.
//
// TestStateTimestampLayoutIsFixedWidth pins the padding against a silent
// swap to time.RFC3339Nano.
const stateTimestampLayout = "2006-01-02T15:04:05.000000000Z07:00"

// State returns the live client state: state-machine bucket, request
// counters and last-failure / last-callback timestamps. The
// aggregated shape lets the MQTT bridge publish one connectivity
// topic per interface without crossing the metrics layer.
func (c *InterfaceClient) State() payload.StatePayload {
	if c == nil {
		return nil
	}
	state := c.sm.State()
	c.mu.Lock()
	closed := c.closed
	c.mu.Unlock()

	out := &payload.InterfaceClientState{
		State:            string(state),
		Closed:           closed,
		TotalRequests:    c.totalRequests.Load(),
		ExecutedRequests: c.executedRequests.Load(),
		PendingRequests:  c.pendingRequests.Load(),
	}
	c.failureMu.Lock()
	if !c.lastFailureAt.IsZero() {
		out.LastFailureAt = c.lastFailureAt.UTC().Format(stateTimestampLayout)
	}
	c.failureMu.Unlock()
	c.callbackMu.Lock()
	if !c.lastCallbackAt.IsZero() {
		out.LastCallbackAt = c.lastCallbackAt.UTC().Format(stateTimestampLayout)
	}
	c.callbackMu.Unlock()
	return out
}
