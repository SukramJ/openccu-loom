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
type Policy struct {
	cur atomic.Pointer[Resolution]
}

// NewPolicy seeds a policy from the boot configuration.
func NewPolicy(ui config.NorthUI) *Policy {
	p := &Policy{}
	p.Set(ui)
	return p
}

// Set replaces the live resolution. Called by the composition root at
// boot and by the surfaces write handler after a successful save.
func (p *Policy) Set(ui config.NorthUI) {
	res := Resolve(ui)
	p.cur.Store(&res)
}

// Resolution returns the live resolution. A zero-value Policy resolves
// as standalone-with-defaults, which is the safe direction: everything
// visible, nothing refused.
func (p *Policy) Resolution() Resolution {
	if p == nil {
		return Resolve(config.NorthUI{})
	}
	if res := p.cur.Load(); res != nil {
		return *res
	}
	return Resolve(config.NorthUI{})
}

// RefusedBy is the request-path helper the middleware calls.
func (p *Policy) RefusedBy(method, path string) ID {
	return p.Resolution().RefusedBy(method, path)
}
