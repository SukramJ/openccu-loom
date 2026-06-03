// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package central

import (
	"context"
	"errors"
	"sort"
	"sync"

	"github.com/SukramJ/openccu-loom/internal/model/hub"
	"github.com/SukramJ/openccu-loom/internal/routingkey"
)

// Registry holds every configured [*Unit] keyed by name.
// Multi-CCU support (ADR 0002) requires this registry as the entry
// point for every cross-cutting concern — REST, MQTT, UI iterate it
// to aggregate state across CCUs.
type Registry struct {
	mu    sync.RWMutex
	items map[string]*Unit
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{items: make(map[string]*Unit)}
}

// ErrAlreadyRegistered is returned on duplicate Register.
var ErrAlreadyRegistered = errors.New("central: name already registered")

// Register adds c. Returns [ErrAlreadyRegistered] when the name is
// already taken.
func (r *Registry) Register(c *Unit) error {
	if c == nil || c.Name() == "" {
		return errors.New("central: cannot register nil / unnamed unit")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.items[c.Name()]; ok {
		return ErrAlreadyRegistered
	}
	r.items[c.Name()] = c
	return nil
}

// Get returns the unit bound to name.
func (r *Registry) Get(name string) (*Unit, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	c, ok := r.items[name]
	return c, ok
}

// SerialSuffix returns the routing-key central-id discriminator for the
// named central: the CCU serial's last-10 lower suffix
// ([routingkey.SerialSuffix]). It feeds the canonical external
// unique_id for hub / internal / virtual-remote addresses (normal
// devices need no prefix). The serial is only known post-connect, so an
// unknown or not-yet-connected central yields an empty suffix; callers
// building hub-level keys must tolerate that (hub keys are themselves
// only built for post-connect entities — see
// docs/external-clients/ha-unique-id-migration.md).
func (r *Registry) SerialSuffix(name string) string {
	r.mu.RLock()
	c, ok := r.items[name]
	r.mu.RUnlock()
	if !ok || c == nil {
		return ""
	}
	return routingkey.SerialSuffix(c.SystemInformation().Serial)
}

// List returns every registered unit sorted by name for stable
// iteration.
func (r *Registry) List() []*Unit {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*Unit, 0, len(r.items))
	for _, c := range r.items {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name() < out[j].Name() })
	return out
}

// Unregister atomically removes the central with the given name. Returns true
// when an entry was found and removed, false when the name was not
// registered.
func (r *Registry) Unregister(name string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.items[name]; !ok {
		return false
	}
	delete(r.items, name)
	return true
}

// Len returns the number of registered centrals. Safe for concurrent
// use.
func (r *Registry) Len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.items)
}

// Names returns the registered central names in sorted order.
func (r *Registry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.items))
	for n := range r.items {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// HubFor returns the [*hub.Hub] associated with the named central, or nil when
// the central is not registered or its HubModel is nil. This is the
// convenience shortcut for the common "look up a central, then get its hub"
// pattern that north-bound adapters use repeatedly.
func (r *Registry) HubFor(centralName string) *hub.Hub {
	r.mu.RLock()
	defer r.mu.RUnlock()
	c, ok := r.items[centralName]
	if !ok || c == nil {
		return nil
	}
	return c.HubModel
}

// StartAll fans out Start to every registered unit. First error
// short-circuits; the caller should teardown via [StopAll].
func (r *Registry) StartAll(ctx context.Context) error {
	for _, u := range r.List() {
		if err := u.Start(ctx); err != nil {
			return err
		}
	}
	return nil
}

// StopAll tears down every unit. Errors are best-effort only.
func (r *Registry) StopAll() {
	for _, u := range r.List() {
		u.Stop()
	}
}
