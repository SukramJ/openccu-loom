// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package coordinators

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sort"
	"sync"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/internal/client"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmerr"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
)

// ErrClientExists is returned when two clients are registered with the
// same interface ID.
var ErrClientExists = errors.New("coordinator: client already registered")

// ClientEntry bundles the reliability-stack client with its lifecycle
// hooks. The connection state is read straight from
// [client.InterfaceClient]'s state machine — there is exactly one
// state machine per client, so the wrapper carries no separate state
// of its own.
type ClientEntry struct {
	InterfaceID string
	Interface   hmenum.Interface
	Host        string
	Client      *client.InterfaceClient

	// StartFunc is an optional hook called by StartClients to perform
	// any southbound connection setup (e.g. Init RPC + callback registration).
	// Nil = no-op for this entry.
	StartFunc func(ctx context.Context) error

	// StopFunc is an optional hook called by StopClients to perform
	// teardown (e.g. DeInit RPC, close transport). Nil = no-op.
	StopFunc func(ctx context.Context) error
}

// Connected reports whether the client's state machine is in
// CONNECTED. Pure state read; the active health-check path lives in
// the `check_connection` ticker (ccu_wiring.go) which drives the
// breaker.
func (e *ClientEntry) Connected() bool {
	if e == nil || e.Client == nil {
		return false
	}
	return e.Client.ClientState() == hmenum.ClientStateConnected
}

// ClientCoordinator tracks every InterfaceClient the central owns.
type ClientCoordinator struct {
	mu    sync.RWMutex
	items map[string]*ClientEntry // keyed by interface ID

	// failureMu guards lastFailureReason / lastFailureInterfaceID.
	failureMu              sync.Mutex
	lastFailureReason      string
	lastFailureInterfaceID string
}

// NewClientCoordinator returns an empty coordinator.
func NewClientCoordinator() *ClientCoordinator {
	return &ClientCoordinator{items: make(map[string]*ClientEntry)}
}

// Register adds entry. Returns [ErrClientExists] on duplicate interface ID.
func (c *ClientCoordinator) Register(entry *ClientEntry) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.items[entry.InterfaceID]; ok {
		return ErrClientExists
	}
	c.items[entry.InterfaceID] = entry
	return nil
}

// Get returns the entry for interfaceID.
func (c *ClientCoordinator) Get(interfaceID string) (*ClientEntry, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	e, ok := c.items[interfaceID]
	return e, ok
}

// Remove deletes the registration.
func (c *ClientCoordinator) Remove(interfaceID string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.items[interfaceID]; !ok {
		return false
	}
	delete(c.items, interfaceID)
	return true
}

// List returns entries sorted by interface ID.
func (c *ClientCoordinator) List() []*ClientEntry {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]*ClientEntry, 0, len(c.items))
	for _, e := range c.items {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].InterfaceID < out[j].InterfaceID })
	return out
}

// HasClient reports whether a client is registered for interfaceID.
func (c *ClientCoordinator) HasClient(interfaceID string) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	_, ok := c.items[interfaceID]
	return ok
}

// HasClients reports whether at least one client is registered.
func (c *ClientCoordinator) HasClients() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.items) > 0
}

// InterfaceIDs returns the sorted list of registered interface IDs.
func (c *ClientCoordinator) InterfaceIDs() []string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]string, 0, len(c.items))
	for id := range c.items {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// Interfaces returns the sorted list of unique [hmenum.Interface] values
// registered with this coordinator.
func (c *ClientCoordinator) Interfaces() []hmenum.Interface {
	c.mu.RLock()
	defer c.mu.RUnlock()
	seen := make(map[hmenum.Interface]struct{}, len(c.items))
	for _, e := range c.items {
		seen[e.Interface] = struct{}{}
	}
	out := make([]hmenum.Interface, 0, len(seen))
	for iface := range seen {
		out = append(out, iface)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// primaryCandidateInterfaces is the ordered preference set used by
// PrimaryClient to select the best-suited client for sysvar / program
// calls. HmIP-RF → BidCos-RF → BidCos-Wired in descending preference;
// CUxD and other interfaces can only serve as fallbacks because they
// do not support system variable / program RPC calls.
var primaryCandidateInterfaces = []hmenum.Interface{
	hmenum.InterfaceHmIPRF,
	hmenum.InterfaceBidCosRF,
	hmenum.InterfaceBidCosWired,
}

// PrimaryClient returns the best available client for sysvar and program
// calls. The candidate set is searched in preference order (HmIP-RF →
// BidCos-RF → BidCos-Wired); within the candidate set the first connected
// client wins. If no candidate is connected, the first connected non-candidate
// is returned as a fallback. Returns nil when no clients are registered.
func (c *ClientCoordinator) PrimaryClient() *client.InterfaceClient {
	entries := c.List()
	if len(entries) == 0 {
		return nil
	}
	// Build a lookup for fast candidate detection.
	candidateSet := make(map[hmenum.Interface]struct{}, len(primaryCandidateInterfaces))
	for _, iface := range primaryCandidateInterfaces {
		candidateSet[iface] = struct{}{}
	}
	// Build an interface → entry map.
	byIface := make(map[hmenum.Interface]*ClientEntry, len(entries))
	for _, e := range entries {
		byIface[e.Interface] = e
	}
	// Preferred candidates first.
	for _, iface := range primaryCandidateInterfaces {
		if e, ok := byIface[iface]; ok && e.Connected() {
			return e.Client
		}
	}
	// Fallback: first connected non-candidate.
	for _, e := range entries {
		if _, isCandidate := candidateSet[e.Interface]; !isCandidate && e.Connected() {
			return e.Client
		}
	}
	// Fallback: first entry regardless of connection state (original behaviour).
	return entries[0].Client
}

// AllClientsActive returns true when every registered client reports
// CONNECTED. Returns false when no clients are registered.
func (c *ClientCoordinator) AllClientsActive() bool {
	entries := c.List()
	if len(entries) == 0 {
		return false
	}
	for _, e := range entries {
		if !e.Connected() {
			return false
		}
	}
	return true
}

// Available returns true when every registered client is CONNECTED.
// Returns false when no clients are registered. This mirrors the
// all-clients-must-be-connected semantic required for safe operation —
// a partial connection leaves paramset and event delivery unreliable.
func (c *ClientCoordinator) Available() bool {
	return c.AllClientsActive()
}

// AnyClientActive returns true when at least one registered client
// reports CONNECTED. Returns false when no clients are registered.
// Used to determine DEGRADED state (some but not all clients connected).
func (c *ClientCoordinator) AnyClientActive() bool {
	for _, e := range c.List() {
		if e.Connected() {
			return true
		}
	}
	return false
}

// IsAlive reports whether every registered client has an active callback
// connection. Returns true when no clients are registered (vacuously true).
// A single stale callback is enough to return false.
//
// Mirrors the is_alive property in client.py which checks
// all(client.is_callback_alive() for client in self._clients.values()).
func (c *ClientCoordinator) IsAlive() bool {
	for _, e := range c.List() {
		if !e.Client.IsCallbackAlive() {
			return false
		}
	}
	return true
}

// StartClients calls the Start hook on every registered client entry that has
// a non-nil StartFunc.
//
// Each entry may optionally carry a StartFunc; without it this method is a
// no-op for that entry. Errors from individual clients are collected and
// returned as a combined error string.
func (c *ClientCoordinator) StartClients(ctx context.Context) error {
	return c.forEachLifecycle(ctx, func(e *ClientEntry) error {
		if e.StartFunc == nil {
			return nil
		}
		return e.StartFunc(ctx)
	})
}

// StopClients calls the Stop hook on every registered client entry.
func (c *ClientCoordinator) StopClients(ctx context.Context) error {
	return c.forEachLifecycle(ctx, func(e *ClientEntry) error {
		if e.StopFunc == nil {
			return nil
		}
		return e.StopFunc(ctx)
	})
}

// restartClientsCooldown is the pause between Stop and Start in
// [RestartClients]. The 500 ms window lets in-flight RPC responses
// drain and CCU-side state settle before the new Init handshake fires.
const restartClientsCooldown = 500 * time.Millisecond

// RestartClients stops all clients, waits for a brief cooldown, then
// starts them again. The 500 ms pause lets in-flight wire responses
// drain before the reconnect Init handshake fires.
func (c *ClientCoordinator) RestartClients(ctx context.Context) error {
	if err := c.StopClients(ctx); err != nil {
		return err
	}
	// Brief cooldown: let in-flight RPC responses drain before re-init.
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(restartClientsCooldown):
	}
	return c.StartClients(ctx)
}

// forEachLifecycle iterates all registered entries (sorted) and calls fn.
// It collects errors from all entries and returns them combined.
func (c *ClientCoordinator) forEachLifecycle(ctx context.Context, fn func(*ClientEntry) error) error {
	var errs []error
	for _, e := range c.List() {
		if err := fn(e); err != nil {
			errs = append(errs, err)
		}
		// Respect context cancellation between entries.
		if ctx.Err() != nil {
			break
		}
	}
	if len(errs) == 0 {
		return nil
	}
	return errors.Join(errs...)
}

// CreateClientConfig carries the parameters needed by [ClientCoordinator.CreateClient].
type CreateClientConfig struct {
	// Host is the CCU hostname or IP address used for the TCP pre-flight probe.
	Host string
	// Port is the TCP port to probe before calling Factory. Zero skips the
	// probe (e.g. JSON-RPC-only interfaces that have no dedicated TCP port).
	Port int
	// InterfaceID is the wire-level identifier used for log messages and
	// duplicate-registration checks.
	InterfaceID string
	// Interface is the hmenum.Interface enum value carried on the entry.
	Interface hmenum.Interface

	// Factory is called after a successful TCP probe (or immediately when
	// Port == 0) to construct the [ClientEntry] that will be registered.
	// The returned entry's Client field must be non-nil. On auth failure the
	// factory must return an error wrapping [hmerr.ErrAuthFailure] so the
	// retry logic can distinguish it from other failures.
	Factory func(ctx context.Context) (*ClientEntry, error)
}

// tcpProbeTimeout is the per-attempt deadline for the TCP pre-flight check.
const tcpProbeTimeout = 3 * time.Second

// createClientAuthMaxAttempts is the maximum number of factory calls when
// every failure is an auth error. Non-auth errors abort immediately.
const createClientAuthMaxAttempts = 5

// createClientBackoffInitial is the base backoff between successive auth
// retries. Each interval doubles, capped at [createClientBackoffMax].
// Declared as a variable so tests can shorten it without sleeping.
var createClientBackoffInitial = 2 * time.Second //nolint:gochecknoglobals // package-level var so tests can shorten it

// createClientBackoffMax caps the exponential-backoff interval.
// Declared as a variable so tests can shorten it alongside [createClientBackoffInitial].
var createClientBackoffMax = 60 * time.Second //nolint:gochecknoglobals // package-level var so tests can shorten it

// WaitForTCPReady performs a TCP connectivity pre-check against host:port
// with a fixed 3 s timeout. Returns nil when the port accepts a connection,
// or a wrapped [hmerr.ErrNoConnection] otherwise. Port 0 is a no-op (returns
// nil immediately). Callers that want a longer probe loop should wrap this in
// their own retry logic.
func (c *ClientCoordinator) WaitForTCPReady(ctx context.Context, host string, port int) error {
	if port <= 0 {
		return nil
	}
	addr := net.JoinHostPort(host, fmt.Sprintf("%d", port))
	probeCtx, cancel := context.WithTimeout(ctx, tcpProbeTimeout)
	conn, err := (&net.Dialer{}).DialContext(probeCtx, "tcp", addr)
	cancel()
	if err != nil {
		return fmt.Errorf("tcp_ready %s: %w", addr, hmerr.ErrNoConnection)
	}
	_ = conn.Close()
	return nil
}

// PollClients returns every registered [ClientEntry] whose underlying
// [client.InterfaceClient] is in polling mode (i.e. Connected() == false).
// Entries are returned in the same sorted order as [List].
func (c *ClientCoordinator) PollClients() []*ClientEntry {
	var out []*ClientEntry
	for _, e := range c.List() {
		if !e.Connected() {
			out = append(out, e)
		}
	}
	return out
}

// RecordLastFailure stores the failure reason and the responsible
// interface ID so callers (recovery coordinator, health wiring) can
// surface structured diagnostics without parsing log lines.
// Thread-safe; safe to call from the recovery goroutine.
func (c *ClientCoordinator) RecordLastFailure(reason, interfaceID string) {
	c.failureMu.Lock()
	c.lastFailureReason = reason
	c.lastFailureInterfaceID = interfaceID
	c.failureMu.Unlock()
}

// LastFailureReason returns the most recent failure reason recorded via
// [RecordLastFailure]. Returns "" when no failure has been recorded.
func (c *ClientCoordinator) LastFailureReason() string {
	c.failureMu.Lock()
	defer c.failureMu.Unlock()
	return c.lastFailureReason
}

// LastFailureInterfaceID returns the interface ID associated with the most
// recent recorded failure. Returns "" when no failure has been recorded.
func (c *ClientCoordinator) LastFailureInterfaceID() string {
	c.failureMu.Lock()
	defer c.failureMu.Unlock()
	return c.lastFailureInterfaceID
}

// CreateClient performs a three-stage defensive start-up sequence for a
// single interface client and registers it on success:
//
//  1. TCP pre-flight (first attempt only, skipped when Port == 0): dials
//     cfg.Host:cfg.Port with a 3 s timeout. A failure here is a network
//     error — the method returns immediately without retrying.
//  2. Factory invocation: cfg.Factory must return a fully-constructed
//     [ClientEntry]. Success → the entry is registered and the method
//     returns (entry, nil).
//  3. Auth-retry with exponential backoff: when Factory returns an error
//     wrapping [hmerr.ErrAuthFailure] the call is retried up to
//     [createClientAuthMaxAttempts] times with a back-off starting at
//     [createClientBackoffInitial] and doubling up to [createClientBackoffMax].
//     After the last attempt the auth error is returned. Any other
//     (non-auth) factory error aborts immediately.
//
// The registered entry is returned on success so callers can wire
// lifecycle hooks without a second lookup.
func (c *ClientCoordinator) CreateClient(ctx context.Context, cfg CreateClientConfig) (*ClientEntry, error) {
	// Stage 1: TCP pre-flight on the first attempt only.
	if cfg.Port > 0 {
		addr := net.JoinHostPort(cfg.Host, fmt.Sprintf("%d", cfg.Port))
		probeCtx, cancel := context.WithTimeout(ctx, tcpProbeTimeout)
		conn, err := (&net.Dialer{}).DialContext(probeCtx, "tcp", addr)
		cancel()
		if err != nil {
			return nil, fmt.Errorf("create_client %s: tcp probe %s: %w", cfg.InterfaceID, addr, hmerr.ErrNoConnection)
		}
		_ = conn.Close()
	}

	backoff := createClientBackoffInitial
	for attempt := 1; attempt <= createClientAuthMaxAttempts; attempt++ {
		// Stage 2: factory invocation.
		entry, err := cfg.Factory(ctx)
		if err == nil {
			// Success: register and return.
			if regErr := c.Register(entry); regErr != nil {
				return nil, fmt.Errorf("create_client %s: register: %w", cfg.InterfaceID, regErr)
			}
			return entry, nil
		}

		// Stage 3: only auth failures are retried.
		if !errors.Is(err, hmerr.ErrAuthFailure) {
			return nil, fmt.Errorf("create_client %s: %w", cfg.InterfaceID, err)
		}
		if attempt == createClientAuthMaxAttempts {
			return nil, fmt.Errorf("create_client %s: auth failed after %d attempts: %w", cfg.InterfaceID, createClientAuthMaxAttempts, err)
		}

		// Wait for the next retry with exponential backoff.
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("create_client %s: context cancelled during auth retry: %w", cfg.InterfaceID, ctx.Err())
		case <-time.After(backoff):
		}
		backoff *= 2
		if backoff > createClientBackoffMax {
			backoff = createClientBackoffMax
		}
	}
	// Unreachable — the loop always returns or falls through to the last-attempt
	// branch above — but the compiler requires a terminal statement.
	return nil, fmt.Errorf("create_client %s: exhausted attempts", cfg.InterfaceID)
}

// SubscribeToHealthEvents wires the coordinator to the event bus so it
// can re-evaluate client states in response to [hmevent.HealthRecordedEvent].
// When a health record arrives for an interface owned by this coordinator,
// the entry's Connected() state is refreshed and callers that gate on
// Available() observe the update without polling.
//
// Returns an unsubscribe function; call it on teardown to avoid goroutine leaks.
// Pass a nil bus to get a no-op unsubscribe.
func (c *ClientCoordinator) SubscribeToHealthEvents(bus *events.Bus, onEval func(interfaceID string)) func() {
	if bus == nil {
		return func() {}
	}
	return events.Subscribe(bus, func(e hmevent.HealthRecordedEvent) {
		if e.InterfaceID == "" {
			return
		}
		// Re-evaluate: if the coordinator owns the interface, notify the caller.
		if _, ok := c.Get(e.InterfaceID); !ok {
			return
		}
		if onEval != nil {
			onEval(e.InterfaceID)
		}
	})
}
