// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package statemachine

import (
	"strings"
	"sync"
	"time"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// IssueKind classifies the transport layer an issue belongs to. Closes
// C-CSTATE-1.
type IssueKind int

const (
	// IssueKindRPCProxy represents XML-RPC / BIN-RPC proxy issues.
	// Source strings that start with known interface ID prefixes
	// (HmIP-RF, BidCos-RF, BidCos-Wired, VirtualDevices, CUxD,
	// Homegear) are classified as RPC-proxy issues.
	IssueKindRPCProxy IssueKind = iota
	// IssueKindJSON represents JSON-RPC (CCU-level API) issues.
	// Sources that don't match any known RPC-proxy prefix are treated
	// as JSON issues (rega, json-rpc, …).
	IssueKindJSON
)

// rpcProxyPrefixes are the well-known RPC-proxy interface-ID prefixes.
var rpcProxyPrefixes = []string{
	"HmIP-RF", "BidCos-RF", "BidCos-Wired",
	"VirtualDevices", "CUxD", "Homegear",
}

// classifySource returns [IssueKindRPCProxy] when source matches a
// known RPC-proxy interface ID prefix, [IssueKindJSON] otherwise.
func classifySource(source string) IssueKind {
	for _, p := range rpcProxyPrefixes {
		if strings.HasPrefix(source, p) {
			return IssueKindRPCProxy
		}
	}
	return IssueKindJSON
}

// ConnectionIssue is one open issue tracked on [ConnectionState].
// Mirrors the per-issue payload
// `connection_state.add_issue` (`central/connection_state.py:58`).
type ConnectionIssue struct {
	// Source identifies the component that raised the issue
	// (interface_id, "ping_pong", "rega", …).
	Source string
	// Reason classifies the failure for downstream consumers.
	Reason hmenum.FailureReason
	// Message is a free-form, sanitised description.
	Message string
	// FirstSeen is the timestamp of the first add_issue call.
	FirstSeen time.Time
	// LastSeen is bumped every time the same issue is re-added.
	LastSeen time.Time
	// Count tracks how often the issue has been re-added without an
	// intervening clear.
	Count int
}

// ConnectionState aggregates per-source connection issues for a central. Used
// by the diagnostics surface and by the state-machine evaluator to decide
// whether the central should transition into DEGRADED.
//
// All methods are safe for concurrent use.
type ConnectionState struct {
	mu        sync.RWMutex
	issues    map[string]ConnectionIssue
	now       func() time.Time
	onChanged func(source string, connected bool) // C-CSTATE-2 publish hook
}

// NewConnectionState returns an empty issue tracker.
func NewConnectionState() *ConnectionState {
	return &ConnectionState{
		issues: make(map[string]ConnectionIssue),
		now:    time.Now,
	}
}

// SetOnChanged registers a callback fired whenever an issue is added or
// removed. The callback receives the source identifier and whether the
// connection is now considered connected (true = issue removed, false = issue
// added). Callers use this to publish [hmevent.SystemStatusChangedEvent]
// without creating a direct dependency from the state-machine package on the
// event bus. Closes C-CSTATE-2. Passing nil disables publishing.
func (c *ConnectionState) SetOnChanged(fn func(source string, connected bool)) {
	c.mu.Lock()
	c.onChanged = fn
	c.mu.Unlock()
}

// AddIssue records an issue under `source`. Re-adding the same source
// bumps `LastSeen` and `Count` instead of overwriting `FirstSeen` —
// `add_issue` (`connection_state.py:58-72`).
// When the issue is new the registered [SetOnChanged] callback is fired
// with connected=false. Closes C-CSTATE-2.
func (c *ConnectionState) AddIssue(source string, reason hmenum.FailureReason, message string) {
	if source == "" {
		return
	}
	c.mu.Lock()
	now := c.now()
	if existing, ok := c.issues[source]; ok {
		existing.LastSeen = now
		existing.Count++
		existing.Reason = reason
		existing.Message = message
		c.issues[source] = existing
		c.mu.Unlock()
		return
	}
	c.issues[source] = ConnectionIssue{
		Source:    source,
		Reason:    reason,
		Message:   message,
		FirstSeen: now,
		LastSeen:  now,
		Count:     1,
	}
	fn := c.onChanged
	c.mu.Unlock()
	if fn != nil {
		fn(source, false)
	}
}

// RemoveIssue clears the issue for `source`. Returns true when an
// issue actually existed. When an issue is removed the registered
// [SetOnChanged] callback is fired with connected=true.
// Closes C-CSTATE-2.
func (c *ConnectionState) RemoveIssue(source string) bool {
	c.mu.Lock()
	if _, ok := c.issues[source]; !ok {
		c.mu.Unlock()
		return false
	}
	delete(c.issues, source)
	fn := c.onChanged
	c.mu.Unlock()
	if fn != nil {
		fn(source, true)
	}
	return true
}

// ClearAllIssues drops every tracked issue and fires the registered
// [SetOnChanged] callback with connected=true for each removed source.
// Returns the number of issues that were cleared.
// Used by the recovery coordinator after a successful reconnect.
func (c *ConnectionState) ClearAllIssues() int {
	c.mu.Lock()
	sources := make([]string, 0, len(c.issues))
	for src := range c.issues {
		sources = append(sources, src)
	}
	c.issues = make(map[string]ConnectionIssue)
	fn := c.onChanged
	c.mu.Unlock()
	if fn != nil {
		for _, src := range sources {
			fn(src, true)
		}
	}
	return len(sources)
}

// HasAnyIssue reports whether at least one issue is tracked.
func (c *ConnectionState) HasAnyIssue() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.issues) > 0
}

// IssueCount returns the number of tracked issues.
func (c *ConnectionState) IssueCount() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.issues)
}

// Issues snapshots every tracked issue. Order is not specified.
func (c *ConnectionState) Issues() []ConnectionIssue {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if len(c.issues) == 0 {
		return nil
	}
	out := make([]ConnectionIssue, 0, len(c.issues))
	for _, v := range c.issues {
		out = append(out, v)
	}
	return out
}

// IssueFor returns the tracked issue for `source` (if any).
func (c *ConnectionState) IssueFor(source string) (ConnectionIssue, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	v, ok := c.issues[source]
	return v, ok
}

// IsRPCProxyIssue reports whether there is an active issue for the given
// source that is classified as an XML-RPC / BIN-RPC proxy issue. Closes
// C-CSTATE-1.
func (c *ConnectionState) IsRPCProxyIssue(source string) bool {
	c.mu.RLock()
	_, ok := c.issues[source]
	c.mu.RUnlock()
	return ok && classifySource(source) == IssueKindRPCProxy
}

// IsJSONIssue reports whether there is an active issue for the given
// source that is classified as a JSON-RPC issue. Mirrors the implicit
// Counterpart
func (c *ConnectionState) IsJSONIssue(source string) bool {
	c.mu.RLock()
	_, ok := c.issues[source]
	c.mu.RUnlock()
	return ok && classifySource(source) == IssueKindJSON
}

// RPCProxyIssueCount returns the number of tracked issues classified as
// XML-RPC / BIN-RPC proxy issues.
func (c *ConnectionState) RPCProxyIssueCount() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	n := 0
	for source := range c.issues {
		if classifySource(source) == IssueKindRPCProxy {
			n++
		}
	}
	return n
}

// JSONIssueCount returns the number of tracked issues classified as JSON-RPC
// issues.
func (c *ConnectionState) JSONIssueCount() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	n := 0
	for source := range c.issues {
		if classifySource(source) == IssueKindJSON {
			n++
		}
	}
	return n
}
