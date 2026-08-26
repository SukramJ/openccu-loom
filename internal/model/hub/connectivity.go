// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package hub

import (
	"sort"
	"sync"

	"github.com/SukramJ/openccu-loom/internal/model/naming"
	"github.com/SukramJ/openccu-loom/internal/payload"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// InterfaceReachability records one interface's connectivity status.
//
// Interface carries the resolved [hmenum.Interface] enum value for the
// interface. When the enum cannot be resolved from InterfaceID the field
// remains zero ("").
type InterfaceReachability struct {
	InterfaceID string
	// Interface is the typed enum for InterfaceID. Populated by
	// [OnStateWithInterface] or resolved as a fallback from InterfaceID
	// via [hmenum.Interface](InterfaceID) when [Interface] is empty.
	Interface hmenum.Interface
	Reachable bool
}

// ResolvedInterface returns the [hmenum.Interface] for this entry.
// When [Interface] is already set it is returned directly; otherwise
// the method attempts to parse [InterfaceID] as an interface token.
// Returns "" when neither field can be resolved.
func (r InterfaceReachability) ResolvedInterface() hmenum.Interface {
	if r.Interface != "" {
		return r.Interface
	}
	return hmenum.Interface(r.InterfaceID)
}

// Connectivity tracks per-interface reachability across a central.
type Connectivity struct {
	// ServiceRegistry implements the write-half of [payload.Source].
	// Connectivity is read-only; the zero value gives correct
	// no-service behaviour (ServiceMethodNames returns nil, Invoke
	// returns ErrUnknownServiceMethod).
	payload.ServiceRegistry

	mu        sync.RWMutex
	states    map[string]bool
	observed  bool
	callbacks []func(InterfaceReachability)
}

// NewConnectivity returns an empty tracker.
func NewConnectivity() *Connectivity {
	return &Connectivity{states: map[string]bool{}}
}

// Reachable returns the reachability flag for an interface and whether
// it has been observed.
func (c *Connectivity) Reachable(interfaceID string) (reachable, observed bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	v, ok := c.states[interfaceID]
	return v, ok
}

// AllReachable reports whether every tracked interface is reachable.
// Empty tracker returns (false, false).
func (c *Connectivity) AllReachable() (allReachable, observed bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if !c.observed {
		return false, false
	}
	for _, ok := range c.states {
		if !ok {
			return false, true
		}
	}
	return true, true
}

// List returns every tracked (interface, reachable) pair sorted by
// interface ID. The [InterfaceReachability.Interface] field is
// populated from the ID via type cast; callers that need a strict
// match against a known [hmenum.Interface] value should check
// [InterfaceReachability.ResolvedInterface].
func (c *Connectivity) List() []InterfaceReachability {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]InterfaceReachability, 0, len(c.states))
	for id, r := range c.states {
		out = append(out, InterfaceReachability{
			InterfaceID: id,
			Interface:   hmenum.Interface(id),
			Reachable:   r,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].InterfaceID < out[j].InterfaceID })
	return out
}

// OnState records a reachability change. Fires callbacks when the
// per-interface state actually flips. The [InterfaceReachability]
// passed to callbacks has [Interface] resolved from InterfaceID via
// the [hmenum.Interface] type cast; use [OnStateWithInterface] to
// supply an already-typed enum value.
func (c *Connectivity) OnState(interfaceID string, reachable bool) {
	c.OnStateWithInterface(interfaceID, hmenum.Interface(interfaceID), reachable)
}

// OnStateWithInterface records a reachability change with an explicit typed
// [hmenum.Interface] enum value. Fires callbacks when the per- interface
// state actually flips.
func (c *Connectivity) OnStateWithInterface(interfaceID string, iface hmenum.Interface, reachable bool) {
	c.mu.Lock()
	prev, existed := c.states[interfaceID]
	c.states[interfaceID] = reachable
	c.observed = true
	cbs := make([]func(InterfaceReachability), len(c.callbacks))
	copy(cbs, c.callbacks)
	c.mu.Unlock()
	if existed && prev == reachable {
		return
	}
	ev := InterfaceReachability{InterfaceID: interfaceID, Interface: iface, Reachable: reachable}
	for _, cb := range cbs {
		if cb != nil {
			cb(ev)
		}
	}
}

// TranslationKey returns the HA translation key used to localise the
// per-interface connectivity sensor entity.
func (c *Connectivity) TranslationKey() string { return "interface_connectivity" }

// EnabledByDefault reports whether the connectivity sensor is enabled by
// default. Connectivity sensors are always included in the default
// north-bound surface without requiring explicit operator opt-in.
func (*Connectivity) EnabledByDefault() bool { return true }

// MQTTTopicsForInterface returns the canonical ADR-0011 connectivity
// topic `<base>/<central>/hub/connectivity/<iface>` for one interface.
// Connectivity is a multi-interface aggregate, so callers parametrise
// over the interface ID rather than calling [payload.MQTTAddressable]
// on the aggregate as a whole.
func (c *Connectivity) MQTTTopicsForInterface(base, centralName, iface string) payload.MQTTTopicSet {
	if iface == "" {
		return payload.MQTTTopicSet{}
	}
	return payload.MQTTTopicSet{
		State: naming.MQTTHubConnectivity(base, centralName, iface),
	}
}

// Available reports whether the connectivity aggregate has been observed.
// Always returns true once any state has been recorded.
func (c *Connectivity) Available() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.observed
}

// StateUncertain reports whether the connectivity aggregate state is
// uncertain. Returns true when no interface state has been observed yet
// (c.observed == false). Once at least one interface state is recorded
// the aggregate is considered known.
func (c *Connectivity) StateUncertain() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return !c.observed
}

// OnUpdate registers a subscription.
func (c *Connectivity) OnUpdate(fn func(InterfaceReachability)) func() {
	c.mu.Lock()
	c.callbacks = append(c.callbacks, fn)
	idx := len(c.callbacks) - 1
	c.mu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			c.mu.Lock()
			defer c.mu.Unlock()
			if idx < len(c.callbacks) {
				c.callbacks[idx] = nil
			}
		})
	}
}
