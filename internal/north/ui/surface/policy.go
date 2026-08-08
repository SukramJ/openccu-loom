// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package surface

import (
	"sync/atomic"

	"github.com/SukramJ/openccu-loom/internal/config"
)

// Policy holds the live resolution so the write boundary reacts to a
// saved profile immediately, without a daemon reload and without a
// database round-trip per request.
//
// Both properties matter. A profile change that only took effect after
// a restart would make the editor lie — it reports "saved" while Home
// Assistant keeps writing to a surface the operator just hid — and
// re-deriving the effective config on every request would put a SQLite
// read in front of every write endpoint.
//
// loom:reachable:reason="constructed in the daemon composition root and handed to rest.Deps.SurfacePolicy, which the write middleware calls per request; the analyzer's type heuristic cannot see a struct used only through its methods"
type Policy struct {
	cur atomic.Pointer[Resolution]
	ui  atomic.Pointer[config.NorthUI]
	// centrals reports how many CCUs the daemon currently serves. A CCU
	// can be adopted at runtime, and it moves two shipped defaults
	// (see Surface.MultiCentralVisible), so the count is read per
	// resolution rather than frozen at boot.
	centrals func() int
}

// NewPolicy seeds a policy from the boot configuration. centrals may be
// nil, which reads as the single-CCU case.
func NewPolicy(ui config.NorthUI, centrals func() int) *Policy {
	p := &Policy{centrals: centrals}
	p.Set(ui)
	return p
}

// Set replaces the live configuration. Called by the composition root at
// boot and by the surfaces write handler after a successful save.
func (p *Policy) Set(ui config.NorthUI) {
	copied := ui
	p.ui.Store(&copied)
	res := ResolveFleet(ui, p.fleet())
	p.cur.Store(&res)
}

// fleet reads the current central count.
func (p *Policy) fleet() Fleet {
	if p == nil || p.centrals == nil {
		return Fleet{}
	}
	return Fleet{Centrals: p.centrals()}
}

// Resolution returns the live resolution. A zero-value Policy resolves
// as standalone-with-defaults, which is the safe direction: everything
// visible, nothing refused.
//
// The cached resolution is recomputed when the central count has moved
// since it was built — a CCU adopted at runtime must widen the two
// fleet-dependent defaults without waiting for the next config save.
// The check is an int comparison against a registry read under RLock;
// the recompute happens only on the request that first sees the change.
func (p *Policy) Resolution() Resolution {
	if p == nil {
		return Resolve(config.NorthUI{})
	}
	res := p.cur.Load()
	if res == nil {
		return Resolve(config.NorthUI{})
	}
	fleet := p.fleet()
	if fleet == res.Fleet {
		return *res
	}
	ui := p.ui.Load()
	if ui == nil {
		return *res
	}
	next := ResolveFleet(*ui, fleet)
	p.cur.Store(&next)
	return next
}

// RefusedBy is the request-path helper the middleware calls.
func (p *Policy) RefusedBy(method, path string) ID {
	return p.Resolution().RefusedBy(method, path)
}
