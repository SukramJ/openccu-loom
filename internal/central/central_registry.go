// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package central

import (
	"context"
	"errors"
	"slices"
	"sort"
	"sync"

	"github.com/SukramJ/openccu-loom/internal/model/hub"
	"github.com/SukramJ/openccu-loom/internal/routingkey"
	"github.com/SukramJ/openccu-loom/internal/wiring"
)

// Registry holds every configured [*Unit] keyed by name.
// Multi-CCU support (ADR 0002) requires this registry as the entry
// point for every cross-cutting concern — REST, MQTT, UI iterate it
// to aggregate state across CCUs.
type Registry struct {
	mu    sync.RWMutex
	items map[string]*Unit

	// wireMu serializes every membership transition against the observer
	// fan-out, and is held while an observer or an unwire runs. It is a
	// second lock rather than a reuse of mu because observers read the
	// registry (List, Get, SerialSuffix) while they wire themselves, and mu
	// is what those reads take.
	//
	// The one rule it imposes: an observer must not itself Register,
	// Unregister or OnRegister. No collaborator needs to, and the
	// alternative — running observers outside the lock — reopens the window
	// where a replay and a concurrent Register wire the same unit twice.
	wireMu sync.Mutex
	// observers holds every registered observer in registration order, which
	// is the order they are run in for each unit.
	observers []*centralObserver
	// unwires is the ledger of what each observer attached to each central,
	// keyed by central name and kept in attach order.
	unwires map[string][]attachedUnwire

	// manifest records what each observer *claims* to wire, declared by
	// the wiring code as it attaches (ADR 0065). unwires answers "what is
	// currently attached"; manifest answers "what was ever meant to be" —
	// and the difference is the whole point, because a wire call that is
	// deleted, skipped by a nil guard or never reached leaves neither.
	manifest *wiring.Manifest
}

// Observer is wired for every unit that enters the registry: the ones
// already present when it is registered AND every one registered afterwards.
// It returns the teardown for whatever it attached, or nil when it attached
// nothing.
//
// It exists because the daemon's second-largest defect class was a
// collaborator that walked the registry once at boot and never learned about a
// CCU adopted later — measurement history stayed empty, no webhook was ever
// sent, WebSocket topics carried nothing. Every one of those is a boot walk
// that should have been an observer, and the replay over the units already
// present is what makes the two cases one case.
type Observer func(u *Unit) (unwire func())

// centralObserver is one registered observer. It is tracked by pointer so a
// removal can find exactly its own attachments in the ledger.
type centralObserver struct {
	observe Observer
}

// attachedUnwire is one observer's teardown for one central.
type attachedUnwire struct {
	owner  *centralObserver
	unwire func()
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return NewRegistryWithManifest(wiring.NewManifest())
}

// NewRegistryWithManifest returns an empty registry that records its
// seams into m.
//
// It exists because the manifest has to outlive the registry's own
// construction. The composition root wires several things before the
// registry exists — the audit overlay's cipher and secret transform among
// them — and a manifest created by [NewRegistry] could not hold their
// seams, so the one place where a missing seam means credentials in
// cleartext was the one place the manifest could not see. Building the
// manifest first and handing it over closes that.
//
// A nil manifest is replaced with a fresh one rather than accepted:
// [wiring.Manifest] tolerates nil, but a registry that silently recorded
// nothing would make every guard reading it pass for the wrong reason.
func NewRegistryWithManifest(m *wiring.Manifest) *Registry {
	if m == nil {
		m = wiring.NewManifest()
	}
	return &Registry{
		items:    make(map[string]*Unit),
		unwires:  make(map[string][]attachedUnwire),
		manifest: m,
	}
}

// Manifest returns the ledger of declared per-central seams. Never nil
// for a registry built by [NewRegistry]; a nil *Registry returns nil,
// which [wiring.Manifest] handles.
func (r *Registry) Manifest() *wiring.Manifest {
	if r == nil {
		return nil
	}
	return r.manifest
}

// OnRegisterDeclared is [Registry.OnRegister] with the seam written
// down: it declares s in the registry's manifest, then attaches observe.
//
// Prefer it everywhere. Plain OnRegister wires just as correctly; what
// it cannot do is leave a trace, and a seam that declares nothing can
// only be looked for by name. Name matching is what let a whole
// eviction subsystem — store method, overlay method, unit tests, a doc
// comment naming its trigger — reach production with no line anywhere
// calling it, and no guard able to see the gap (ADR 0065).
//
// The declaration happens first, so a seam whose observer panics on
// some central still shows in the ledger as attempted rather than
// silently absent.
func (r *Registry) OnRegisterDeclared(s wiring.Seam, observe Observer) (remove func()) {
	r.Manifest().Declare(s)
	return r.OnRegister(observe)
}

// OnRegister registers observe for every central in the registry — those
// already registered are wired immediately, in List order, and every later
// [Registry.Register] runs it too. The returned remove detaches everything
// observe attached and stops it receiving further centrals; it is idempotent.
//
// This is the sanctioned way to wire something per central. A boot walk plus a
// separate runtime-adopt hook is the same wiring written twice, and the second
// half is what gets forgotten.
func (r *Registry) OnRegister(observe Observer) (remove func()) {
	if r == nil || observe == nil {
		return func() {}
	}
	entry := &centralObserver{observe: observe}

	r.wireMu.Lock()
	r.observers = append(r.observers, entry)
	for _, u := range r.List() {
		r.attachLocked(entry, u)
	}
	r.wireMu.Unlock()

	var once sync.Once
	return func() { once.Do(func() { r.removeObserver(entry) }) }
}

// attachLocked runs one observer for one unit and records what it attached.
// Caller holds wireMu.
func (r *Registry) attachLocked(entry *centralObserver, u *Unit) {
	if u == nil {
		return
	}
	unwire := entry.observe(u)
	if unwire == nil {
		return
	}
	if r.unwires == nil {
		r.unwires = make(map[string][]attachedUnwire)
	}
	name := u.Name()
	r.unwires[name] = append(r.unwires[name], attachedUnwire{owner: entry, unwire: unwire})
}

// removeObserver drops entry from the observer list and runs every unwire it
// attached, newest central first so the teardown mirrors the attach.
func (r *Registry) removeObserver(entry *centralObserver) {
	r.wireMu.Lock()
	defer r.wireMu.Unlock()

	r.observers = slices.DeleteFunc(r.observers, func(o *centralObserver) bool { return o == entry })

	names := make([]string, 0, len(r.unwires))
	for name := range r.unwires {
		names = append(names, name)
	}
	sort.Sort(sort.Reverse(sort.StringSlice(names)))
	for _, name := range names {
		kept := r.unwires[name][:0]
		var run []func()
		for _, a := range r.unwires[name] {
			if a.owner == entry {
				run = append(run, a.unwire)
				continue
			}
			kept = append(kept, a)
		}
		if len(kept) == 0 {
			delete(r.unwires, name)
		} else {
			r.unwires[name] = kept
		}
		for i := len(run) - 1; i >= 0; i-- {
			run[i]()
		}
	}
}

// ErrAlreadyRegistered is returned on duplicate Register.
var ErrAlreadyRegistered = errors.New("central: name already registered")

// Register adds c and runs every registered [Observer] for it, in
// registration order. Returns [ErrAlreadyRegistered] when the name is already
// taken — a rejected registration wires nothing, because a subscription for a
// unit the registry does not hold has no teardown path.
func (r *Registry) Register(c *Unit) error {
	if c == nil || c.Name() == "" {
		return errors.New("central: cannot register nil / unnamed unit")
	}
	r.wireMu.Lock()
	defer r.wireMu.Unlock()

	r.mu.Lock()
	if _, ok := r.items[c.Name()]; ok {
		r.mu.Unlock()
		return ErrAlreadyRegistered
	}
	r.items[c.Name()] = c
	r.mu.Unlock()

	for _, entry := range r.observers {
		r.attachLocked(entry, c)
	}
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

// CanonicalSerial returns the canonical (last-10, case-preserved) serial of
// the named central, or "" when it is unknown or unresolved. This is the exact
// string GET /system/ccu reports; consumers that must line up with that
// surface — the mDNS ccus= advertisement HA de-dupes discovery on — use this
// rather than the lower-cased [SerialSuffix] routing form.
func (r *Registry) CanonicalSerial(name string) string {
	r.mu.RLock()
	c, ok := r.items[name]
	r.mu.RUnlock()
	if !ok || c == nil {
		return ""
	}
	return routingkey.CanonicalSerial(c.SystemInformation().Serial)
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

// Unregister atomically removes the central with the given name and runs every
// observer unwire attached to it, in reverse attach order. Returns true when an
// entry was found and removed, false when the name was not registered.
//
// The unwires run here and not at the call site because that is the only place
// that sees every observer: a removed central whose subscriptions stay live
// keeps publishing on planes it no longer belongs to.
func (r *Registry) Unregister(name string) bool {
	r.wireMu.Lock()
	defer r.wireMu.Unlock()

	r.mu.Lock()
	_, ok := r.items[name]
	if ok {
		delete(r.items, name)
	}
	r.mu.Unlock()
	if !ok {
		return false
	}

	attached := r.unwires[name]
	delete(r.unwires, name)
	for i := len(attached) - 1; i >= 0; i-- {
		attached[i].unwire()
	}
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
